package oapi

import (
	"reflect"

	"github.com/getkin/kin-openapi/openapi3"
)

// ResponseEnvelope governs how a successful payload (plus optional meta) becomes
// the response body, AND how that wrapping is described in the OpenAPI docs. The
// two halves are defined together so the documented schema can never drift from
// the bytes on the wire — the same invariant the rest of the library upholds for
// request binding.
//
// Install one process-wide with [SetResponseEnvelope], or per route with
// [WithEnvelope] / [WithRawResponse]. The default is [DataEnvelope] (the standard
// {"data": ...} / {"data": ..., "meta": ...} shape).
//
// Implement this directly only for exotic shapes; most projects are served by the
// declarative [KeyedEnvelope] (which never touches openapi3) or by [RawEnvelope].
//
// Contract: WrapSchema MUST mirror Wrap — they must agree on which keys exist —
// or the docs will misdescribe the response.
type ResponseEnvelope interface {
	// Wrap returns the value to JSON-marshal for a success response. data is the
	// handler payload (nil for a body-less response); meta is whatever
	// WithMeta/WithPaging attached (nil if none).
	Wrap(data any, meta any) any
	// WrapSchema mirrors Wrap for documentation: given the schema of the data type
	// (and meta type, nil when none), return the schema of the wrapped body.
	WrapSchema(data, meta *openapi3.SchemaRef) *openapi3.SchemaRef
}

// KeyedEnvelope is the declarative, common-case [ResponseEnvelope]: it nests the
// payload under DataKey and the meta object under MetaKey, optionally merging a
// set of constant fields (e.g. {"success": true, "code": 0}). It expresses both
// the runtime body and the documented schema from one definition, so the two
// cannot diverge — and a project configures its envelope without ever importing
// openapi3.
//
//	oapi.SetResponseEnvelope(oapi.KeyedEnvelope{DataKey: "result", MetaKey: "meta",
//		Constants: map[string]any{"success": true}})
type KeyedEnvelope struct {
	DataKey   string         // key the payload nests under; "" defaults to "data"
	MetaKey   string         // key the meta object nests under; "" omits meta entirely
	Constants map[string]any // fixed fields merged into every success body
}

// DataEnvelope is the library default: the standard {"data": ...} envelope, with
// a "meta" key added when the route documents/attaches meta. A fresh install
// behaves exactly as the library did before envelopes were pluggable.
var DataEnvelope = KeyedEnvelope{DataKey: "data", MetaKey: "meta"} //nolint:exhaustruct,gochecknoglobals

// RawEnvelope renders the payload as-is, with no wrapper (the pure handler model).
// It has nowhere to put meta, so any attached meta is dropped — prefer documenting
// meta-bearing routes with the default envelope. [NewResult] selects it; a route
// opts in with [WithRawResponse].
var RawEnvelope ResponseEnvelope = rawEnvelope{} //nolint:gochecknoglobals

func (e KeyedEnvelope) dataKey() string {
	if e.DataKey == "" {
		return "data"
	}
	return e.DataKey
}

// Wrap builds the success body: constant fields, then the payload under the data
// key (omitted when nil, matching the previous "data,omitempty" behaviour) and the
// meta object under the meta key (only when a meta key is configured and meta is
// non-nil).
func (e KeyedEnvelope) Wrap(data, meta any) any {
	out := make(map[string]any, len(e.Constants)+2)
	for k, v := range e.Constants {
		out[k] = v
	}
	if !isNilValue(data) {
		out[e.dataKey()] = data
	}
	if e.MetaKey != "" && !isNilValue(meta) {
		out[e.MetaKey] = meta
	}
	return out
}

// WrapSchema mirrors Wrap key-for-key: the same constant/data/meta keys, so the
// documented schema always matches the wire body.
func (e KeyedEnvelope) WrapSchema(data, meta *openapi3.SchemaRef) *openapi3.SchemaRef {
	obj := openapi3.NewObjectSchema()
	obj.Properties = openapi3.Schemas{}
	for k, v := range e.Constants {
		obj.Properties[k] = openapi3.NewSchemaRef("", constSchema(v))
	}
	if data != nil {
		obj.Properties[e.dataKey()] = data
	}
	if e.MetaKey != "" && meta != nil {
		obj.Properties[e.MetaKey] = meta
	}
	return openapi3.NewSchemaRef("", obj)
}

// rawEnvelope is the no-wrapper envelope behind [RawEnvelope].
type rawEnvelope struct{}

func (rawEnvelope) Wrap(data, _ any) any { return data }
func (rawEnvelope) WrapSchema(data, _ *openapi3.SchemaRef) *openapi3.SchemaRef {
	return data
}

// constSchema builds the schema for a constant envelope field from the Go value's
// kind and sets it as the example, so the docs show the literal (success: true)
// instead of a bare type placeholder.
func constSchema(v any) *openapi3.Schema {
	s := scalarSchema(reflect.TypeOf(v))
	s.Example = v
	return s
}

// responseEnvelope is the process-wide default, read on every success render and
// during doc generation. Like the validator seam it is not lock-guarded: install
// it before serving (the documented contract).
var responseEnvelope ResponseEnvelope = DataEnvelope //nolint:gochecknoglobals

// SetResponseEnvelope installs the process-wide [ResponseEnvelope] used to shape
// every success response that does not override it per route. Call it once during
// startup, before serving. Passing nil restores the default [DataEnvelope].
//
// It is not safe to call concurrently with in-flight requests.
func SetResponseEnvelope(e ResponseEnvelope) {
	if e == nil {
		e = DataEnvelope
	}
	responseEnvelope = e
}

// resolveEnvelope picks the effective envelope: an explicit per-route/per-result
// value, else the process-wide default, else [DataEnvelope].
func resolveEnvelope(e ResponseEnvelope) ResponseEnvelope {
	if e != nil {
		return e
	}
	if responseEnvelope != nil {
		return responseEnvelope
	}
	return DataEnvelope
}
