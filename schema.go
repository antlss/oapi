package oapi

import (
	"math"
	"mime/multipart"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
)

var (
	fileHeaderType = reflect.TypeFor[multipart.FileHeader]()
	timeType       = reflect.TypeFor[time.Time]()
)

// rangeFields calls fn for each exported field of struct type t, recursing into
// embedded (anonymous) struct fields so their promoted fields are visited too.
// The binder and the OpenAPI generator both use it, which keeps "what binds" and
// "what is documented" in lockstep — including for embedded structs.
func rangeFields(t reflect.Type, fn func(reflect.StructField)) {
	t = deref(t)
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			if ft := deref(field.Type); ft != nil && ft.Kind() == reflect.Struct {
				rangeFields(ft, fn)
				continue
			}
		}
		fn(field)
	}
}

func paramsFromStruct(t reflect.Type, in, tagKey string) []*openapi3.Parameter {
	t = deref(t)
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}

	var params []*openapi3.Parameter
	rangeFields(t, func(field reflect.StructField) {
		name := tagName(field, tagKey)
		if name == "" || name == "-" {
			return
		}

		var param *openapi3.Parameter
		switch in {
		case openapi3.ParameterInPath:
			param = openapi3.NewPathParameter(name)
		case openapi3.ParameterInHeader:
			param = openapi3.NewHeaderParameter(name)
		default:
			param = openapi3.NewQueryParameter(name)
		}

		param.Schema = openapi3.NewSchemaRef("", scalarSchema(field.Type))
		applyBinding(param.Schema.Value, field.Type, field.Tag.Get(RuleTag))
		applyExample(param.Schema.Value, field)
		// Path params are always required; others only when tagged binding:"required".
		param.Required = in == openapi3.ParameterInPath || hasRequired(field.Tag.Get(RuleTag))
		params = append(params, param)
	})

	return params
}

func requestBody(t reflect.Type, set *schemaSet) *openapi3.RequestBody {
	t = deref(t)
	if t == nil {
		return nil
	}
	if t.Kind() != reflect.Struct {
		// A top-level non-struct JSON body (e.g. a bulk POST whose body is a
		// []Item). bindJSON unmarshals it at runtime, so document its schema too
		// rather than emitting no body. Only slice/array/map shapes are meaningful
		// as a JSON body; anything else is treated as "no documented body". Such a
		// body carries no top-level `binding` rules (no validation overlay) and is
		// documented as not strictly required.
		switch t.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			return openapi3.NewRequestBody().WithJSONSchemaRef(typeSchemaRef(t, set))
		default:
			return nil
		}
	}

	if isFormBody(t) {
		return formRequestBody(t)
	}

	// JSON body: let openapi3gen build the full schema from the Go type/json tags,
	// then overlay the validation contract it cannot see (it only reads `json`
	// tags, not `binding`): required, enum, format and bounds.
	ref, err := openapi3gen.NewSchemaRefForValue(reflect.New(t).Elem().Interface(), nil)
	if err != nil {
		ref = openapi3.NewSchemaRef("", openapi3.NewObjectSchema())
	}

	required := true
	if ref.Value != nil {
		// openapi3gen returns the SAME *Schema instance for every field of a given
		// type (e.g. all `string` properties share one object). Enriching in place
		// would bleed one field's binding rules onto all same-typed fields, so
		// de-alias the tree first — a JSON round-trip gives every property its own
		// independent schema. Cheap: this runs once at doc-generation time.
		schema := deAliasSchema(ref.Value)
		enrichSchema(schema, t, true)
		ref = openapi3.NewSchemaRef("", schema)
		// The body is "required" when validating a zero value would reject it: a
		// top-level required field, or a required field inside an always-present
		// (non-pointer) nested struct. Using only the top-level schema.Required
		// would wrongly document a body as optional while the validator rejects an
		// empty one because of a nested requirement.
		required = bodyHasRequiredField(t)
	}

	return openapi3.NewRequestBody().WithRequired(required).WithJSONSchemaRef(ref)
}

// bodyHasRequiredField reports whether validating a zero value of t would require
// at least one field — i.e. whether the request body is mandatory. It mirrors the
// validator (go-playground with WithRequiredStructEnabled): a top-level
// `binding:"required"` field, or a required field inside a non-pointer nested
// struct (always present as a zero value, so dived into), makes the body
// required. An optional pointer sub-struct does not, since a nil pointer is not
// validated. rangeFields already flattens embedded structs, so only named
// non-embedded fields reach the callback.
func bodyHasRequiredField(t reflect.Type) bool {
	t = deref(t)
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	required := false
	rangeFields(t, func(field reflect.StructField) {
		switch {
		case required:
			return
		case hasRequired(field.Tag.Get(RuleTag)):
			required = true
		case field.Type.Kind() == reflect.Struct && bodyHasRequiredField(field.Type):
			required = true
		}
	})
	return required
}

// deAliasSchema returns a copy of s with no shared sub-schema pointers, by
// round-tripping through JSON. openapi3gen reuses one *Schema per Go type across
// all properties of that type; mutating such a shared instance (as the binding
// overlay does) would corrupt every sibling. The round-trip rebuilds an
// independent tree so each property can be enriched in isolation. On any error it
// falls back to the original (no worse than before).
func deAliasSchema(s *openapi3.Schema) *openapi3.Schema {
	raw, err := s.MarshalJSON()
	if err != nil {
		return s
	}
	clone := &openapi3.Schema{} //nolint:exhaustruct
	if err := clone.UnmarshalJSON(raw); err != nil {
		return s
	}
	return clone
}

// enrichSchema overlays per-field metadata that openapi3gen cannot see (it reads
// only `json` tags) onto a generated JSON schema, matching properties by wire
// name and recursing into named nested structs and arrays of structs. The
// example:"" tag is always applied; the `binding` validation overlay
// (required/enum/format/bounds) is applied only when withBinding is true — true
// for request bodies (the binder enforces those rules), false for responses
// (which still benefit from examples but carry no input contract).
//
// The recursion matters because the validator validates the fields of nested
// structs too, so their rules and examples must reach the docs or the spec would
// drift from what the binder enforces.
func enrichSchema(schema *openapi3.Schema, t reflect.Type, withBinding bool) {
	rangeFields(t, func(field reflect.StructField) {
		name := tagName(field, tagJSON)
		if name == "" || name == "-" {
			return
		}
		prop, ok := schema.Properties[name]
		if !ok || prop.Value == nil {
			return
		}
		if withBinding {
			if binding := field.Tag.Get(RuleTag); binding != "" {
				applyBinding(prop.Value, field.Type, binding)
				if hasRequired(binding) && !slices.Contains(schema.Required, name) {
					schema.Required = append(schema.Required, name)
				}
			}
		}
		applyExample(prop.Value, field)
		enrichNestedSchema(prop.Value, field.Type, withBinding)
	})
}

// enrichNestedSchema follows a property schema into nested object and array
// schemas, applying enrichSchema to the struct type behind each. It only
// descends into object schemas that actually have properties, so formatted
// scalar structs (e.g. time.Time, rendered as a date-time string) are left
// untouched. It also serves as the entry point for slice-typed top levels
// (e.g. a []Product response body).
func enrichNestedSchema(prop *openapi3.Schema, t reflect.Type, withBinding bool) {
	t = deref(t)
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		if len(prop.Properties) > 0 {
			enrichSchema(prop, t, withBinding)
		}
	case reflect.Slice, reflect.Array:
		if prop.Items != nil && prop.Items.Value != nil {
			enrichNestedSchema(prop.Items.Value, t.Elem(), withBinding)
		}
	}
}

// applyExample reads the example:"" struct tag and sets it as the schema example
// (parsed to the field's Go type), so the docs show a real sample value instead
// of a type placeholder, and applies any field description (see applyDescription).
// No tag leaves the schema untouched.
func applyExample(schema *openapi3.Schema, field reflect.StructField) {
	if schema == nil {
		return
	}
	if raw, ok := field.Tag.Lookup("example"); ok && raw != "" {
		schema.Example = parseExample(raw, field.Type)
	}
	applyDescription(schema, field)
}

// applyDescription reads a human field description from the doc:"" struct tag
// (with description:"" accepted as an alias) and sets it as the schema
// description, so generated docs explain each field. No tag leaves the schema
// untouched. It is applied wherever a field's example is, so params, request
// bodies and response bodies all carry their descriptions.
func applyDescription(schema *openapi3.Schema, field reflect.StructField) {
	if schema == nil {
		return
	}
	if raw, ok := field.Tag.Lookup("doc"); ok && raw != "" {
		schema.Description = raw
		return
	}
	if raw, ok := field.Tag.Lookup("description"); ok && raw != "" {
		schema.Description = raw
	}
}

// parseExample converts the raw example tag string into a value of the field's
// kind, so numbers/bools render unquoted and slices render as arrays. Anything
// it cannot parse falls back to the raw string.
func parseExample(raw string, t reflect.Type) any {
	dt := deref(t)
	if dt == nil {
		return raw
	}
	switch dt.Kind() {
	case reflect.Bool:
		if b, err := strconv.ParseBool(raw); err == nil {
			return b
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return n
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
	case reflect.Slice, reflect.Array:
		parts := strings.Split(raw, ",")
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, parseExample(strings.TrimSpace(p), dt.Elem()))
		}
		return out
	}
	return raw
}

func formRequestBody(t reflect.Type) *openapi3.RequestBody {
	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{}
	hasFile := false

	rangeFields(t, func(field reflect.StructField) {
		name := tagName(field, tagForm)
		if name == "" || name == "-" {
			return
		}

		if isFileField(field.Type) {
			hasFile = true
			fileSchema := openapi3.NewStringSchema()
			fileSchema.Format = "binary"
			if isFileSliceField(field.Type) {
				arr := openapi3.NewArraySchema()
				arr.Items = openapi3.NewSchemaRef("", fileSchema)
				schema.Properties[name] = openapi3.NewSchemaRef("", arr)
			} else {
				schema.Properties[name] = openapi3.NewSchemaRef("", fileSchema)
			}
		} else {
			propSchema := scalarSchema(field.Type)
			applyBinding(propSchema, field.Type, field.Tag.Get(RuleTag))
			applyExample(propSchema, field)
			schema.Properties[name] = openapi3.NewSchemaRef("", propSchema)
		}

		if hasRequired(field.Tag.Get(RuleTag)) {
			schema.Required = append(schema.Required, name)
		}
	})

	mediaType := mimeURLEncoded
	if hasFile {
		mediaType = mimeMultipart
	}

	return openapi3.NewRequestBody().
		WithRequired(true).
		WithSchemaRef(openapi3.NewSchemaRef("", schema), []string{mediaType})
}

func responsesFor(route Route, set *schemaSet) *openapi3.Responses {
	doc := route.doc
	env := resolveEnvelope(route.envelope)
	successStatus := route.successStatus
	if successStatus == 0 {
		switch {
		case doc.binary != nil:
			// A documented binary download returns 200 with bytes, not 204.
			successStatus = http.StatusOK
		case doc.schema.Response == nil:
			successStatus = http.StatusNoContent
		default:
			successStatus = http.StatusOK
		}
	}

	opts := make([]openapi3.NewResponsesOption, 0, len(doc.responses)+2)

	successResp := openapi3.NewResponse().WithDescription(http.StatusText(successStatus))
	switch {
	case doc.binary != nil && successStatus != http.StatusNoContent:
		// Binary (file-download) body: document the declared media type with a
		// string/binary schema instead of the JSON success envelope.
		if doc.binary.description != "" {
			successResp = successResp.WithDescription(doc.binary.description)
		}
		successResp = successResp.WithContent(binaryContent(doc.binary.contentType))
	case doc.schema.Response != nil && successStatus != http.StatusNoContent:
		successResp = successResp.WithJSONSchemaRef(
			env.WrapSchema(typeSchemaRef(doc.schema.Response, set), metaSchemaRef(doc.schema.Meta, set)))
	}
	opts = append(opts, openapi3.WithStatus(successStatus, &openapi3.ResponseRef{Value: successResp})) //nolint:exhaustruct

	// Additional documented responses (errors / alternative statuses).
	for _, rd := range doc.responses {
		if rd.status == successStatus {
			continue
		}
		desc := rd.description
		if desc == "" {
			desc = http.StatusText(rd.status)
		}
		resp := openapi3.NewResponse().WithDescription(desc)
		switch {
		case rd.status >= http.StatusBadRequest:
			// Error statuses are never wrapped in the success envelope: a declared
			// body type is the full error body, otherwise use the configured error
			// schema.
			if rd.typ != nil {
				resp = resp.WithJSONSchemaRef(typeSchemaRef(rd.typ, set))
			} else {
				resp = resp.WithJSONSchemaRef(errorSchemaRef(set))
			}
		case rd.typ != nil:
			resp = resp.WithJSONSchemaRef(env.WrapSchema(typeSchemaRef(rd.typ, set), nil))
		}
		opts = append(opts, openapi3.WithStatus(rd.status, &openapi3.ResponseRef{Value: resp})) //nolint:exhaustruct
	}

	// Generic catch-all error response.
	opts = append(opts, openapi3.WithName("default", errorResponse(set)))

	return openapi3.NewResponses(opts...)
}

// metaSchemaRef builds the schema for an envelope's meta type, or nil when no meta
// type is declared (so the envelope omits the meta key), mirroring how Result only
// emits meta when it is attached.
func metaSchemaRef(t reflect.Type, set *schemaSet) *openapi3.SchemaRef {
	if t == nil {
		return nil
	}
	return typeSchemaRef(t, set)
}

// binaryContent builds the OpenAPI content for a binary (file-download) response:
// a string schema with format "binary" under the given media type (empty defaults
// to application/octet-stream), the standard way to describe a downloadable file.
func binaryContent(contentType string) openapi3.Content {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	schema := openapi3.NewStringSchema()
	schema.Format = "binary"
	return openapi3.NewContentWithSchemaRef(openapi3.NewSchemaRef("", schema), []string{contentType})
}

// errorSchemaRef is the schema for documented error responses: the configured
// [ErrorParser]'s body type when it describes one, else the built-in
// {"error": ...} schema. Both the per-status error responses and the default
// catch-all use it, so they can never describe different shapes.
func errorSchemaRef(set *schemaSet) *openapi3.SchemaRef {
	if errorParser != nil {
		if t := errorParser.ErrorType(); t != nil {
			return typeSchemaRef(t, set)
		}
	}
	return errorEnvelopeSchemaRef()
}

// typeSchemaRef builds an example-enriched schema for a single Go type, overlaying
// any example:"" tags so the docs show sample output. No `binding` overlay: a
// response carries no input contract.
//
// When set is non-nil (the Registry opted into components), a named struct type —
// or the element of a slice of one — is registered as a reusable
// #/components/schemas entry and returned as a $ref, so a type shared across
// endpoints is described once. A nil set inlines everything, the default, so the
// generated schema is byte-identical to before. The schema is de-aliased first,
// since openapi3gen shares one *Schema per Go type and setting an example would
// otherwise bleed across all same-typed fields.
func typeSchemaRef(t reflect.Type, set *schemaSet) *openapi3.SchemaRef {
	dt := deref(t)
	if dt == nil {
		return openapi3.NewSchemaRef("", openapi3.NewObjectSchema())
	}
	// A named struct → a reusable component (nil set / unnameable type → inline).
	if ref := set.componentRef(dt); ref != nil {
		return ref
	}
	// A slice/array of a named struct → an array whose items $ref the component.
	if set != nil && (dt.Kind() == reflect.Slice || dt.Kind() == reflect.Array) {
		if elemRef := set.componentRef(dt.Elem()); elemRef != nil {
			arr := openapi3.NewArraySchema()
			arr.Items = elemRef
			return openapi3.NewSchemaRef("", arr)
		}
	}
	// Inline: the original behaviour (also the path for scalars, maps and slices of
	// non-struct elements).
	return openapi3.NewSchemaRef("", buildTypeSchema(dt))
}

// strSchemaWithExample is a string schema carrying an example value.
func strSchemaWithExample(example string) *openapi3.Schema {
	s := openapi3.NewStringSchema()
	s.Example = example
	return s
}

// errorEnvelopeSchemaRef is the {"error": {code, message, fields}} schema, with
// example values so the docs show a realistic error instead of bare "string"s.
func errorEnvelopeSchemaRef() *openapi3.SchemaRef {
	inner := openapi3.NewObjectSchema()
	inner.Properties = openapi3.Schemas{
		"code":    openapi3.NewSchemaRef("", strSchemaWithExample("bad_request")),
		"message": openapi3.NewSchemaRef("", strSchemaWithExample("request validation failed")),
	}
	fieldItem := openapi3.NewObjectSchema()
	fieldItem.Properties = openapi3.Schemas{
		"field":   openapi3.NewSchemaRef("", strSchemaWithExample("email")),
		"rule":    openapi3.NewSchemaRef("", strSchemaWithExample("required")),
		"message": openapi3.NewSchemaRef("", strSchemaWithExample("email is required")),
	}
	fields := openapi3.NewArraySchema()
	fields.Items = openapi3.NewSchemaRef("", fieldItem)
	inner.Properties["fields"] = openapi3.NewSchemaRef("", fields)

	envelope := openapi3.NewObjectSchema()
	envelope.Properties = openapi3.Schemas{"error": openapi3.NewSchemaRef("", inner)}

	return openapi3.NewSchemaRef("", envelope)
}

// errorResponse is the generic "default" error response, using the same error
// schema as the per-status error responses (see errorSchemaRef).
func errorResponse(set *schemaSet) *openapi3.Response {
	return openapi3.NewResponse().
		WithDescription("Error").
		WithJSONSchemaRef(errorSchemaRef(set))
}

func scalarSchema(t reflect.Type) *openapi3.Schema {
	t = deref(t)
	if t == nil {
		return openapi3.NewStringSchema()
	}

	// time.Time binds from an RFC 3339 string (see decodeTime), so document it as a
	// date-time string rather than the struct it is — matching how a JSON body's
	// time field is rendered, so params/form and body agree.
	if t == timeType {
		s := openapi3.NewStringSchema()
		s.Format = "date-time"
		return s
	}

	switch t.Kind() {
	case reflect.Bool:
		return openapi3.NewBoolSchema()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return openapi3.NewIntegerSchema()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Unsigned integers can never be negative; document the floor of 0 so the
		// schema reflects the Go type's domain.
		s := openapi3.NewIntegerSchema()
		zero := 0.0
		s.Min = &zero
		return s
	case reflect.Float32, reflect.Float64:
		numberType := openapi3.Types{openapi3.TypeNumber}
		schema := openapi3.NewSchema()
		schema.Type = &numberType
		return schema
	case reflect.String:
		return openapi3.NewStringSchema()
	case reflect.Slice, reflect.Array:
		schema := openapi3.NewArraySchema()
		schema.Items = openapi3.NewSchemaRef("", scalarSchema(t.Elem()))
		return schema
	default:
		return openapi3.NewStringSchema()
	}
}

// isFormBody decides whether the body is sent as form data (multipart /
// urlencoded) rather than JSON: true when it contains a file field, or has
// `form` tags and no `json` tags.
func isFormBody(t reflect.Type) bool {
	hasForm, hasJSON, hasFile := false, false, false

	rangeFields(t, func(field reflect.StructField) {
		if isFileField(field.Type) {
			hasFile = true
		}
		if v := tagName(field, tagForm); v != "" && v != "-" {
			hasForm = true
		}
		if v := tagName(field, tagJSON); v != "" && v != "-" {
			hasJSON = true
		}
	})

	return hasFile || (hasForm && !hasJSON)
}

func isSingleFileField(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Elem() == fileHeaderType
}

func isFileSliceField(t reflect.Type) bool {
	return t.Kind() == reflect.Slice &&
		t.Elem().Kind() == reflect.Ptr && t.Elem().Elem() == fileHeaderType
}

func isFileField(t reflect.Type) bool {
	return isSingleFileField(t) || isFileSliceField(t)
}

func hasFileField(t reflect.Type) bool {
	found := false
	rangeFields(t, func(field reflect.StructField) {
		if isFileField(field.Type) {
			found = true
		}
	})
	return found
}

func deref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	return t
}

func tagName(field reflect.StructField, key string) string {
	tag := field.Tag.Get(key)
	if tag == "" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}

	return tag
}

func hasRequired(bindingTag string) bool {
	return slices.Contains(strings.Split(bindingTag, ","), "required")
}

// WireName returns the name a request field is bound under for the given source
// ("header", "uri", "form" or "json") — the name clients actually use — falling
// back to the Go field name when the tag is absent or "-". [Validator]
// implementations use it so the field names they report match the binding
// source, exactly as the OpenAPI parameters and schema do.
func WireName(field reflect.StructField, source string) string {
	if name := tagName(field, source); name != "" && name != "-" {
		return name
	}
	return field.Name
}

// applyBinding maps the common go-playground/validator rules in a `binding` tag
// onto an OpenAPI schema, so the generated docs reflect the constraints the
// binder actually enforces (enum, format, numeric and length bounds). Unknown
// rules are ignored. `required` is handled by the caller (it lives at the parent
// object / parameter level, not on the field schema).
func applyBinding(schema *openapi3.Schema, t reflect.Type, bindingTag string) {
	if schema == nil || bindingTag == "" {
		return
	}
	kind := reflect.Invalid
	if dt := deref(t); dt != nil {
		kind = dt.Kind()
	}

	for rule := range strings.SplitSeq(bindingTag, ",") {
		key, val, _ := strings.Cut(rule, "=")
		switch key {
		case "oneof":
			if e := enumValues(val, kind); len(e) > 0 {
				schema.Enum = e
			}
		case "email":
			schema.Format = "email"
		case "uuid", "uuid3", "uuid4", "uuid5":
			schema.Format = "uuid"
		case "url", "uri", "http_url", "https_url":
			schema.Format = "uri"
		case "min", "gte":
			setLowerBound(schema, kind, val, false)
		case "gt":
			setLowerBound(schema, kind, val, true)
		case "max", "lte":
			setUpperBound(schema, kind, val, false)
		case "lt":
			setUpperBound(schema, kind, val, true)
		case "len":
			setLowerBound(schema, kind, val, false)
			setUpperBound(schema, kind, val, false)
		case "alpha", "alphanum", "numeric", "number", "startswith", "endswith", "contains":
			// String-shape rules the validator enforces but openapi3gen cannot see;
			// surface them as a regex pattern so the docs describe the constraint.
			if kind == reflect.String {
				setPattern(schema, patternForRule(key, val))
			}
		}
	}
}

// patternForRule maps a go-playground string rule to an OpenAPI (regex) pattern.
// startswith/endswith/contains escape their literal argument; the character-class
// rules use a fixed anchored regex. An empty result means "no pattern".
func patternForRule(key, val string) string {
	switch key {
	case "alpha":
		return "^[a-zA-Z]+$"
	case "alphanum":
		return "^[a-zA-Z0-9]+$"
	case "numeric", "number":
		return `^[-+]?[0-9]+(?:\.[0-9]+)?$`
	case "startswith":
		if val != "" {
			return "^" + regexp.QuoteMeta(val)
		}
	case "endswith":
		if val != "" {
			return regexp.QuoteMeta(val) + "$"
		}
	case "contains":
		if val != "" {
			return regexp.QuoteMeta(val)
		}
	}
	return ""
}

// setPattern sets a regex pattern on the schema, but only the first time, so a
// field that combines two pattern-producing rules keeps the first (most specific)
// one rather than silently overwriting it. An empty pattern is ignored.
func setPattern(schema *openapi3.Schema, pattern string) {
	if pattern != "" && schema.Pattern == "" {
		schema.Pattern = pattern
	}
}

// enumValues turns a validator `oneof` parameter (space-separated tokens) into
// OpenAPI enum values, typed as integers when the Go field is numeric.
func enumValues(spec string, kind reflect.Kind) []any {
	tokens := strings.Fields(spec)
	if len(tokens) == 0 {
		return nil
	}
	out := make([]any, 0, len(tokens))
	for _, tok := range tokens {
		if isNumericKind(kind) {
			if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
				out = append(out, n)
				continue
			}
		}
		out = append(out, tok)
	}
	return out
}

// setLowerBound applies a min/gte/gt rule: a value floor for numbers, a minimum
// length for strings, or a minimum item count for slices/arrays.
func setLowerBound(schema *openapi3.Schema, kind reflect.Kind, val string, exclusive bool) {
	n, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return
	}
	switch {
	case isNumericKind(kind):
		schema.Min = &n
		if exclusive {
			schema.ExclusiveMin = openapi3.ExclusiveBound{Bool: &exclusive} //nolint:exhaustruct
		}
	case kind == reflect.String:
		if v, ok := minLengthBound(n, exclusive); ok {
			schema.MinLength = v
		}
	case kind == reflect.Slice || kind == reflect.Array:
		if v, ok := minLengthBound(n, exclusive); ok {
			schema.MinItems = v
		}
	}
}

// setUpperBound applies a max/lte/lt rule, mirroring setLowerBound.
func setUpperBound(schema *openapi3.Schema, kind reflect.Kind, val string, exclusive bool) {
	n, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return
	}
	switch {
	case isNumericKind(kind):
		schema.Max = &n
		if exclusive {
			schema.ExclusiveMax = openapi3.ExclusiveBound{Bool: &exclusive} //nolint:exhaustruct
		}
	case kind == reflect.String:
		if v, ok := maxLengthBound(n, exclusive); ok {
			schema.MaxLength = &v
		}
	case kind == reflect.Slice || kind == reflect.Array:
		if v, ok := maxLengthBound(n, exclusive); ok {
			schema.MaxItems = &v
		}
	}
}

// minLengthBound converts a min/gte/gt rule value into a non-negative OpenAPI
// minLength/minItems. A strict bound (gt) is the smallest integer strictly
// greater than n; OpenAPI has no "exclusive" length, so gt=2 becomes 3 rather
// than the inclusive 2 the old code emitted. A negative result is dropped (ok
// false) instead of wrapping through uint64.
func minLengthBound(n float64, exclusive bool) (uint64, bool) {
	v := math.Ceil(n)
	if exclusive && v == n {
		v++
	}
	if v < 0 {
		return 0, false
	}
	return uint64(v), true
}

// maxLengthBound converts a max/lte/lt rule value into a non-negative OpenAPI
// maxLength/maxItems. A strict bound (lt) is the largest integer strictly less
// than n (lt=3 becomes 2). It reports ok=false when no valid non-negative length
// satisfies the rule, so a nonsensical bound is dropped rather than wrapping.
func maxLengthBound(n float64, exclusive bool) (uint64, bool) {
	v := math.Floor(n)
	if exclusive && v == n {
		v--
	}
	if v < 0 {
		return 0, false
	}
	return uint64(v), true
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// toOpenAPIPath converts the canonical path syntax (:id, *path) to OpenAPI
// ({id}, {path}).
func toOpenAPIPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[i] = "{" + segment[1:] + "}"
		}
	}

	return strings.Join(segments, "/")
}

func operationID(route Route) string {
	cleaner := strings.NewReplacer("/", "_", ":", "", "*", "", "{", "", "}", "")
	return strings.ToLower(route.method) + cleaner.Replace(route.path)
}
