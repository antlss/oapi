package oapi

// Tests for OpenAPI fidelity improvements (Wave 3 / P1-2).
// Currently: the doc:"" / description:"" field tag → schema.Description.

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// widgetModel is a shared response type used by the components tests below. It is
// package-level so its reflect Name() is stable ("widgetModel").
type widgetModel struct {
	ID   int    `json:"id"`
	Name string `json:"name" doc:"the widget name"`
}

type widgetURI struct {
	ID int `uri:"id"`
}

func widgetRoutes() (get, list Route) {
	get = NewRoute(http.MethodGet, "/widgets/:id",
		func(context.Context, Request[struct{}, widgetURI, struct{}, struct{}]) (*widgetModel, error) {
			return &widgetModel{}, nil
		})
	list = NewRoute(http.MethodGet, "/widgets",
		func(context.Context, Request[struct{}, struct{}, struct{}, struct{}]) (*widgetModel, error) {
			return &widgetModel{}, nil
		})
	return get, list
}

func TestUseComponents_SharedTypeRefdOnceAndValidates(t *testing.T) {
	get, list := widgetRoutes()
	reg := NewRegistry("t", "v").UseComponents().Add(get, list)
	doc := reg.OpenAPI()

	// The shared type is registered once under components/schemas, enriched.
	if doc.Components == nil || doc.Components.Schemas["widgetModel"] == nil {
		t.Fatal("widgetModel not registered under components/schemas")
	}
	comp := doc.Components.Schemas["widgetModel"].Value
	if comp == nil || comp.Properties["name"].Value.Description != "the widget name" {
		t.Fatal("component schema is not the enriched widget model")
	}

	// Both responses reference it by $ref under the envelope's data key.
	for _, path := range []string{"/widgets/{id}", "/widgets"} {
		op := doc.Paths.Value(path).Get
		env := op.Responses.Status(http.StatusOK).Value.Content.Get("application/json").Schema.Value
		dataRef := env.Properties["data"]
		if dataRef.Ref != componentRefPath+"widgetModel" {
			t.Fatalf("%s data ref = %q, want %q", path, dataRef.Ref, componentRefPath+"widgetModel")
		}
	}

	// The emitted spec must still load and validate with the $refs resolved.
	raw, err := reg.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	loaded, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := loaded.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestUseComponents_DefaultInlinesNoComponents(t *testing.T) {
	get, list := widgetRoutes()
	doc := NewRegistry("t", "v").Add(get, list).OpenAPI() // no UseComponents()

	if doc.Components != nil && len(doc.Components.Schemas) > 0 {
		t.Fatalf("default should inline, but components/schemas has %d entries", len(doc.Components.Schemas))
	}
	op := doc.Paths.Value("/widgets/{id}").Get
	env := op.Responses.Status(http.StatusOK).Value.Content.Get("application/json").Schema.Value
	dataRef := env.Properties["data"]
	if dataRef.Ref != "" || dataRef.Value == nil {
		t.Fatalf("default data should be inline (no $ref), got ref=%q value=%v", dataRef.Ref, dataRef.Value)
	}
}

type docTagBody struct {
	Email string `json:"email" doc:"the user's email address" binding:"required"`
	Name  string `json:"name"  description:"display name"`
	Plain string `json:"plain"`
}

func TestApplyDescription_RequestBodyProperties(t *testing.T) {
	rb := requestBody(reflect.TypeFor[docTagBody](), nil)
	if rb == nil {
		t.Fatal("requestBody returned nil")
	}
	mt := rb.Content["application/json"]
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		t.Fatal("request body has no application/json schema")
	}
	props := mt.Schema.Value.Properties

	if got := props["email"].Value.Description; got != "the user's email address" {
		t.Fatalf("email description = %q, want the doc tag value", got)
	}
	if got := props["name"].Value.Description; got != "display name" {
		t.Fatalf("name description = %q, want the description-alias tag value", got)
	}
	if got := props["plain"].Value.Description; got != "" {
		t.Fatalf("plain description = %q, want empty (no tag)", got)
	}
}

type docTagQuery struct {
	Q string `form:"q" doc:"full-text search query"`
}

func TestApplyDescription_QueryParameter(t *testing.T) {
	params := paramsFromStruct(reflect.TypeFor[docTagQuery](), "query", tagForm)
	if len(params) != 1 {
		t.Fatalf("got %d params, want 1", len(params))
	}
	if got := params[0].Schema.Value.Description; got != "full-text search query" {
		t.Fatalf("query param description = %q, want the doc tag value", got)
	}
}

type docTagResp struct {
	ID int `json:"id" doc:"unique identifier"`
}

func TestApplyDescription_ResponseSchema(t *testing.T) {
	ref := typeSchemaRef(reflect.TypeFor[docTagResp](), nil)
	if ref == nil || ref.Value == nil {
		t.Fatal("typeSchemaRef returned nil")
	}
	prop, ok := ref.Value.Properties["id"]
	if !ok || prop.Value == nil {
		t.Fatal("response schema missing id property")
	}
	if got := prop.Value.Description; got != "unique identifier" {
		t.Fatalf("response id description = %q, want the doc tag value", got)
	}
}

func TestApplyBinding_StringRulesBecomePattern(t *testing.T) {
	cases := []struct {
		rule, want string
	}{
		{"alphanum", "^[a-zA-Z0-9]+$"},
		{"alpha", "^[a-zA-Z]+$"},
		{"numeric", `^[-+]?[0-9]+(?:\.[0-9]+)?$`},
		{"startswith=a.b", `^a\.b`}, // the '.' must be regex-escaped
		{"endswith=.png", `\.png$`}, // ditto
		{"contains=x/y", `x/y`},     // QuoteMeta leaves '/' as-is
	}
	for _, c := range cases {
		s := openapi3.NewStringSchema()
		applyBinding(s, reflect.TypeFor[string](), c.rule)
		if s.Pattern != c.want {
			t.Errorf("rule %q: pattern = %q, want %q", c.rule, s.Pattern, c.want)
		}
	}
}

func TestApplyBinding_PatternRulesOnlyForStrings(t *testing.T) {
	// A non-string field must not get a string pattern (the validator's string
	// rules don't apply to it).
	s := openapi3.NewIntegerSchema()
	applyBinding(s, reflect.TypeFor[int](), "alphanum")
	if s.Pattern != "" {
		t.Fatalf("integer field got pattern %q, want none", s.Pattern)
	}
}

func TestScalarSchema_UnsignedHasMinimumZero(t *testing.T) {
	s := scalarSchema(reflect.TypeFor[uint32]())
	if s.Min == nil || *s.Min != 0 {
		t.Fatalf("uint32 schema Min = %v, want 0", s.Min)
	}
	signed := scalarSchema(reflect.TypeFor[int32]())
	if signed.Min != nil {
		t.Fatalf("int32 schema Min = %v, want nil (no floor)", *signed.Min)
	}
}

func TestScalarSchema_TimeIsDateTimeString(t *testing.T) {
	s := scalarSchema(reflect.TypeFor[time.Time]())
	if s.Type == nil || !s.Type.Is(openapi3.TypeString) {
		t.Fatalf("time.Time schema type = %v, want string", s.Type)
	}
	if s.Format != "date-time" {
		t.Fatalf("time.Time schema format = %q, want date-time", s.Format)
	}
}
