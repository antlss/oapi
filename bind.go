package oapi

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/go-playground/form/v4"
)

// parseRequest binds and validates every declared part of the request from its
// source. Binding is framework-agnostic: it reads raw values through the Carrier
// and maps them onto the typed structs using the same struct tags the OpenAPI
// generator reads (`header`/`uri`/`form`/`json`), so one Request type binds
// identically on every adapter and can never drift from the docs.
func parseRequest[Header, Param, Query, Body any](c Carrier) (Request[Header, Param, Query, Body], error) {
	binders := bindKit()
	req := Request[Header, Param, Query, Body]{} //nolint:exhaustruct

	if shouldBind[Header, struct{}]() {
		values := collectValues(reflect.TypeFor[Header](), c.HeaderValues, tagHeader)
		if err := binders.bind(&req.Header, values, tagHeader); err != nil {
			return req, err
		}
	}
	if shouldBind[Param, struct{}]() {
		values := collectValues(reflect.TypeFor[Param](), func(name string) []string {
			if v := c.Param(name); v != "" {
				return []string{v}
			}
			return nil
		}, tagURI)
		if err := binders.bind(&req.Param, values, tagURI); err != nil {
			return req, err
		}
	}
	if shouldBind[Query, struct{}]() {
		if err := binders.bind(&req.Query, c.Query(), tagForm); err != nil {
			return req, err
		}
	}
	if shouldBind[Body, struct{}]() {
		if err := bindBody(c, &req.Body, binders); err != nil {
			return req, err
		}
	}

	return req, nil
}

// bindBody chooses the body binder from the content type, falling back to the
// struct shape when the content type is absent or unrecognised.
func bindBody[Body any](c Carrier, body *Body, binders *kit) error {
	t := reflect.TypeFor[Body]()

	switch ct := c.ContentType(); {
	case ct == mimeMultipart || (ct == "" && hasFileField(t)):
		return bindMultipart(c, body, binders)
	case ct == mimeURLEncoded:
		return bindURLEncoded(c, body, binders)
	case ct == mimeJSON:
		return bindJSON(c, body)
	default:
		if isFormBody(t) {
			if hasFileField(t) {
				return bindMultipart(c, body, binders)
			}
			return bindURLEncoded(c, body, binders)
		}
		return bindJSON(c, body)
	}
}

func bindJSON[Body any](c Carrier, body *Body) error {
	raw, err := c.Body()
	if err != nil {
		return badRequest("failed to read request body", nil)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, body); err != nil {
			return jsonBindError(err)
		}
	}
	return runValidation(body, tagJSON)
}

func bindURLEncoded[Body any](c Carrier, body *Body, binders *kit) error {
	raw, err := c.Body()
	if err != nil {
		return badRequest("failed to read request body", nil)
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return badRequest("invalid form body", nil)
	}
	return binders.bind(body, values, tagForm)
}

func bindMultipart[Body any](c Carrier, body *Body, binders *kit) error {
	mf, err := c.MultipartForm()
	if err != nil {
		return badRequest("invalid multipart form", nil)
	}
	if err := binders.decode(body, url.Values(mf.Value), tagForm); err != nil {
		return err
	}
	assignFiles(reflect.ValueOf(body).Elem(), mf.File)
	return runValidation(body, tagForm)
}

// collectValues walks the struct (recursing into embedded structs, exactly like
// the OpenAPI generator) and builds a url.Values from a per-name getter, so the
// shared form decoder can map it onto the struct.
func collectValues(t reflect.Type, get func(name string) []string, tagKey string) url.Values {
	values := url.Values{}
	rangeFields(t, func(field reflect.StructField) {
		name := tagName(field, tagKey)
		if name == "" || name == "-" {
			return
		}
		if vs := get(name); len(vs) > 0 {
			values[name] = vs
		}
	})
	return values
}

// assignFiles sets *multipart.FileHeader and []*multipart.FileHeader fields
// (tagged `form`) from the parsed multipart files, recursing into embedded
// structs.
func assignFiles(v reflect.Value, files map[string][]*multipart.FileHeader) {
	// Normalise a pointer value (e.g. a route declared with a pointer Body type
	// such as *Form) to the struct it points at, allocating when nil, so the
	// multipart path does not panic with "reflect: NumField of non-struct type".
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			if !v.CanSet() {
				return
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}

	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := v.Field(i)
		if field.Anonymous {
			if ft := deref(field.Type); ft != nil && ft.Kind() == reflect.Struct {
				if field.Type.Kind() == reflect.Ptr {
					if fv.IsNil() {
						// The form decoder leaves an embedded *Struct nil when it has
						// no scalar values to set; allocate it so a file field that
						// only lives inside it is still bound — but only when an
						// upload actually targets it, so an unrelated empty embedded
						// struct is not silently materialised.
						if !fv.CanSet() || !embeddedHasFile(ft, files) {
							continue
						}
						fv.Set(reflect.New(field.Type.Elem()))
					}
					fv = fv.Elem()
				}
				assignFiles(fv, files)
				continue
			}
		}

		name := tagName(field, tagForm)
		if name == "" || name == "-" {
			continue
		}
		fhs := files[name]
		if len(fhs) == 0 {
			continue
		}
		switch {
		case isSingleFileField(field.Type):
			fv.Set(reflect.ValueOf(fhs[0]))
		case isFileSliceField(field.Type):
			fv.Set(reflect.ValueOf(fhs))
		}
	}
}

// embeddedHasFile reports whether struct type t (recursing through embedded
// structs, exactly like the binder) declares a `form`-tagged file field for which
// an upload is actually present in files. assignFiles uses it to decide whether to
// allocate a nil embedded pointer struct, so it materialises one only when there
// is a file to put in it.
func embeddedHasFile(t reflect.Type, files map[string][]*multipart.FileHeader) bool {
	found := false
	rangeFields(t, func(field reflect.StructField) {
		if found || !isFileField(field.Type) {
			return
		}
		name := tagName(field, tagForm)
		if name == "" || name == "-" {
			return
		}
		if len(files[name]) > 0 {
			found = true
		}
	})
	return found
}

const (
	tagHeader = "header"
	tagURI    = "uri"
	tagForm   = "form"
	tagJSON   = "json"

	mimeJSON       = "application/json"
	mimeMultipart  = "multipart/form-data"
	mimeURLEncoded = "application/x-www-form-urlencoded"
)

// kit bundles the configured form decoders, one per source tag. They are built
// once and are safe for concurrent reuse. Validation is a separate, pluggable
// concern (see validate.go), so the binder no longer owns a validator.
type kit struct {
	decoders map[string]*form.Decoder
}

var (
	kitOnce   sync.Once
	sharedKit *kit
)

func bindKit() *kit {
	kitOnce.Do(func() {
		sharedKit = newKit()
	})
	return sharedKit
}

func newKit() *kit {
	k := &kit{decoders: map[string]*form.Decoder{}}
	for _, tag := range []string{tagHeader, tagURI, tagForm} {
		dec := form.NewDecoder()
		dec.SetTagName(tag)
		dec.SetMode(form.ModeExplicit) // only bind fields that carry the tag
		dec.RegisterCustomTypeFunc(decodeTime, time.Time{})
		k.decoders[tag] = dec
	}
	return k
}

// bind decodes values onto dst using the decoder for tagKey, then runs the
// configured validator (if any) over the result.
func (k *kit) bind(dst any, values url.Values, tagKey string) error {
	if err := k.decode(dst, values, tagKey); err != nil {
		return err
	}
	return runValidation(dst, tagKey)
}

func (k *kit) decode(dst any, values url.Values, tagKey string) error {
	if err := k.decoders[tagKey].Decode(dst, values); err != nil {
		return decodeBindError(err)
	}
	return nil
}

// validationTarget unwraps the pointer the binder passes down to the single
// pointer level a Validator expects. A value part arrives as *T (passed as-is);
// a pointer part type (e.g. Body = *Form) arrives as **T, so collapse the extra
// pointer levels — allocating any nil intermediate pointer so an unset pointer
// body still validates (its required fields fail as expected) instead of the
// validator choking on a double pointer.
func validationTarget(dst any) any {
	v := reflect.ValueOf(dst)
	for v.Kind() == reflect.Ptr && v.Elem().Kind() == reflect.Ptr {
		next := v.Elem()
		if next.IsNil() {
			if !next.CanSet() {
				break
			}
			next.Set(reflect.New(next.Type().Elem()))
		}
		v = next
	}
	return v.Interface()
}

// decodeTime parses a time.Time from common wire formats (form/query/header).
// Per-field time_format tags are not honoured; the common formats below cover
// typical date/datetime params and RFC3339.
func decodeTime(vals []string) (any, error) {
	if len(vals) == 0 || vals[0] == "" {
		return time.Time{}, nil
	}
	v := vals[0]
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time value %q", v)
}

// --- error construction -----------------------------------------------------

// badRequest is the binder's internal shortcut for the library's standard 400.
func badRequest(message string, fields []FieldError) *Error {
	return NewValidationError(message, fields)
}

// decodeBindError turns form.DecodeErrors (type/parse failures) into a 400. The
// per-field messages are deliberately generic: the raw decoder text embeds Go
// type names and field namespaces, which the library never forwards to the client
// (mirroring the 500 path's internal-detail hiding). Fields are sorted so the
// body is deterministic — DecodeErrors is a map, whose iteration order is random.
func decodeBindError(err error) error {
	var derrs form.DecodeErrors
	if errors.As(err, &derrs) {
		fields := make([]FieldError, 0, len(derrs))
		for name := range derrs {
			fields = append(fields, FieldError{Field: name, Rule: "invalid", Message: "invalid value"})
		}
		slices.SortFunc(fields, func(a, b FieldError) int { return cmp.Compare(a.Field, b.Field) })
		return badRequest("request binding failed", fields)
	}
	return badRequest("request binding failed", nil)
}

// jsonBindError turns json decode failures into a 400. It reports the wire (json)
// field path the client already knows and the EXPECTED json type, but never the
// internal Go type name or the raw decoder text — those would leak struct/type
// detail the 500 path is careful to hide.
func jsonBindError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field != "" {
			return badRequest("request body has an invalid field type", []FieldError{{
				Field:   typeErr.Field,
				Rule:    "type",
				Message: "expected " + jsonTypeName(typeErr.Type),
			}})
		}
		return badRequest("request body has an invalid field type", nil)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return badRequest("request body is not valid JSON", nil)
	}
	return badRequest("invalid request body", nil)
}

// jsonTypeName maps the Go type the decoder expected to the JSON type name a
// client recognises, so a type-mismatch error says what shape was expected
// without exposing the internal Go type.
func jsonTypeName(t reflect.Type) string {
	dt := deref(t)
	if dt == nil {
		return "value"
	}
	switch dt.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "value"
	}
}
