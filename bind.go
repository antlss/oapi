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
func parseRequest[Header, Param, Query, Body any](cfg *appConfig, c Carrier) (Request[Header, Param, Query, Body], error) {
	binders := bindKit()
	req := Request[Header, Param, Query, Body]{} //nolint:exhaustruct

	if shouldBind[Header, struct{}]() {
		values := collectValues(reflect.TypeFor[Header](), c.HeaderValues, tagHeader)
		if err := binders.bind(cfg, &req.Header, values, tagHeader); err != nil {
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
		if err := binders.bind(cfg, &req.Param, values, tagURI); err != nil {
			return req, err
		}
	}
	if shouldBind[Query, struct{}]() {
		if err := binders.bind(cfg, &req.Query, c.Query(), tagForm); err != nil {
			return req, err
		}
	}
	if shouldBind[Body, struct{}]() {
		if err := bindBody(cfg, c, &req.Body, binders); err != nil {
			return req, err
		}
	}

	return req, nil
}

// bindBody chooses the body binder from the content type, falling back to the
// struct shape when the content type is absent or unrecognised.
func bindBody[Body any](cfg *appConfig, c Carrier, body *Body, binders *kit) error {
	shape := bodyShapeOf(reflect.TypeFor[Body]())

	switch ct := c.ContentType(); {
	case ct == mimeMultipart || (ct == "" && shape.hasFile):
		return bindMultipart(cfg, c, body, binders)
	case ct == mimeURLEncoded:
		return bindURLEncoded(cfg, c, body, binders)
	case ct == mimeJSON:
		return bindJSON(cfg, c, body)
	default:
		if shape.isForm {
			if shape.hasFile {
				return bindMultipart(cfg, c, body, binders)
			}
			return bindURLEncoded(cfg, c, body, binders)
		}
		return bindJSON(cfg, c, body)
	}
}

// bodyShape caches, per body type, the two reflection predicates the body binder
// needs on every request: whether the body is a form (urlencoded/multipart) body
// and whether it carries a file field. Both are pure functions of the type, so
// caching them keeps the request path from re-walking the struct each time.
type bodyShape struct {
	isForm  bool
	hasFile bool
}

// bodyShapeCache maps reflect.Type -> bodyShape. reflect.Type is comparable and
// stable, so it is a safe sync.Map key; entries are bounded by the number of
// distinct body types in the program (one per route shape).
var bodyShapeCache sync.Map //nolint:gochecknoglobals

func bodyShapeOf(t reflect.Type) bodyShape {
	if v, ok := bodyShapeCache.Load(t); ok {
		return v.(bodyShape)
	}
	s := bodyShape{isForm: isFormBody(t), hasFile: hasFileField(t)}
	bodyShapeCache.Store(t, s)
	return s
}

func bindJSON[Body any](cfg *appConfig, c Carrier, body *Body) error {
	raw, err := c.Body()
	if err != nil {
		return badRequest("failed to read request body", nil)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, body); err != nil {
			return jsonBindError(err)
		}
	}
	return runValidation(cfg, body, tagJSON)
}

func bindURLEncoded[Body any](cfg *appConfig, c Carrier, body *Body, binders *kit) error {
	raw, err := c.Body()
	if err != nil {
		return badRequest("failed to read request body", nil)
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return badRequest("invalid form body", nil)
	}
	return binders.bind(cfg, body, values, tagForm)
}

func bindMultipart[Body any](cfg *appConfig, c Carrier, body *Body, binders *kit) error {
	mf, err := c.MultipartForm()
	if err != nil {
		return badRequest("invalid multipart form", nil)
	}
	if err := binders.decode(body, url.Values(mf.Value), tagForm); err != nil {
		return err
	}
	assignFiles(reflect.ValueOf(body).Elem(), mf.File)
	return runValidation(cfg, body, tagForm)
}

// collectValues builds a url.Values from a per-name getter for every bindable
// field of t under tagKey, so the shared form decoder can map it onto the struct.
// The set of field names is computed once per (type, tag) and cached by
// boundFieldNames, so header/path binding does not re-walk the struct each request.
func collectValues(t reflect.Type, get func(name string) []string, tagKey string) url.Values {
	values := url.Values{}
	for _, name := range boundFieldNames(t, tagKey) {
		if vs := get(name); len(vs) > 0 {
			values[name] = vs
		}
	}
	return values
}

// fieldNameKey keys the cache of bindable field names by struct type and source
// tag (a type binds different names under header vs uri vs form).
type fieldNameKey struct {
	t   reflect.Type
	tag string
}

// fieldNamesCache maps fieldNameKey -> []string (the cached wire names). The
// returned slice is shared and must be treated as read-only by callers.
var fieldNamesCache sync.Map //nolint:gochecknoglobals

// boundFieldNames returns the wire names of every bindable field of t under
// tagKey (recursing into embedded structs, exactly like the OpenAPI generator),
// walking the struct once per (type, tag) and caching the result.
func boundFieldNames(t reflect.Type, tagKey string) []string {
	key := fieldNameKey{t: t, tag: tagKey}
	if v, ok := fieldNamesCache.Load(key); ok {
		return v.([]string)
	}
	var names []string
	rangeFields(t, func(field reflect.StructField) {
		name := tagName(field, tagKey)
		if name == "" || name == "-" {
			return
		}
		names = append(names, name)
	})
	fieldNamesCache.Store(key, names)
	return names
}

// assignFiles sets *multipart.FileHeader and []*multipart.FileHeader fields
// (tagged `form`) from the parsed multipart files, recursing into embedded
// structs. v is the route's Body value (or any struct reachable from it).
func assignFiles(v reflect.Value, files map[string][]*multipart.FileHeader) {
	target, ok := structForFileBinding(v)
	if !ok {
		return
	}

	t := target.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fieldValue := target.Field(i)

		if field.Anonymous && isEmbeddedStruct(field.Type) {
			assignEmbeddedFiles(fieldValue, field, files)
			continue
		}
		assignFileField(fieldValue, field, files)
	}
}

// structForFileBinding normalises v to the struct value whose fields receive the
// uploaded files. It follows pointer levels (e.g. a route declared with a pointer
// Body type such as *Form), allocating a settable nil pointer so a file field
// reachable only through it can still be bound, and reports ok=false when v is
// not — and cannot become — a struct, so the caller skips it instead of panicking
// with "reflect: NumField of non-struct type".
func structForFileBinding(v reflect.Value) (reflect.Value, bool) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			if !v.CanSet() {
				return reflect.Value{}, false
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return v, true
}

// isEmbeddedStruct reports whether an embedded field's type is a struct, or a
// pointer to one — the only embedded shapes that can themselves carry file fields.
func isEmbeddedStruct(t reflect.Type) bool {
	dt := deref(t)
	return dt != nil && dt.Kind() == reflect.Struct
}

// assignEmbeddedFiles recurses into an embedded struct field. The form decoder
// leaves an embedded *Struct nil when it had no scalar values to set; this
// allocates such a pointer only when an upload actually targets a file field
// inside it, so an unrelated empty embedded struct is never silently materialised.
func assignEmbeddedFiles(fieldValue reflect.Value, field reflect.StructField, files map[string][]*multipart.FileHeader) {
	if field.Type.Kind() == reflect.Ptr {
		if fieldValue.IsNil() {
			if !fieldValue.CanSet() || !embeddedHasFile(deref(field.Type), files) {
				return
			}
			fieldValue.Set(reflect.New(field.Type.Elem()))
		}
		fieldValue = fieldValue.Elem()
	}
	assignFiles(fieldValue, files)
}

// assignFileField sets a single form-tagged file field — a *multipart.FileHeader
// or a []*multipart.FileHeader — from the uploaded files matching its name. Other
// fields (no/blank/"-" tag, no matching upload, non-file types) are left untouched.
func assignFileField(fieldValue reflect.Value, field reflect.StructField, files map[string][]*multipart.FileHeader) {
	name := tagName(field, tagForm)
	if name == "" || name == "-" {
		return
	}
	fhs := files[name]
	if len(fhs) == 0 {
		return
	}
	switch {
	case isSingleFileField(field.Type):
		fieldValue.Set(reflect.ValueOf(fhs[0]))
	case isFileSliceField(field.Type):
		fieldValue.Set(reflect.ValueOf(fhs))
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
func (k *kit) bind(cfg *appConfig, dst any, values url.Values, tagKey string) error {
	if err := k.decode(dst, values, tagKey); err != nil {
		return err
	}
	return runValidation(cfg, dst, tagKey)
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
// The raw decoder error is attached as the (unexported) cause so logging
// middleware can diagnose the failure server-side without it reaching the client.
func decodeBindError(err error) error {
	var derrs form.DecodeErrors
	if errors.As(err, &derrs) {
		fields := make([]FieldError, 0, len(derrs))
		for name := range derrs {
			fields = append(fields, FieldError{Field: name, Rule: "invalid", Message: "invalid value"})
		}
		slices.SortFunc(fields, func(a, b FieldError) int { return cmp.Compare(a.Field, b.Field) })
		return badRequest("request binding failed", fields).withCause(err)
	}
	return badRequest("request binding failed", nil).withCause(err)
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
			}}).withCause(err)
		}
		return badRequest("request body has an invalid field type", nil).withCause(err)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return badRequest("request body is not valid JSON", nil).withCause(err)
	}
	return badRequest("invalid request body", nil).withCause(err)
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
