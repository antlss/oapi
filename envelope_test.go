package oapi

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// widget is a tiny response/error body used across the envelope tests.
type widget struct {
	Name string `json:"name"`
}

// --- a minimal Carrier that captures what the core wrote --------------------

type testCarrier struct {
	ctx     context.Context
	status  int
	json    any
	raw     []byte
	rawCT   string
	empty   bool
	headers map[string]string
	errs    []error
}

func newTestCarrier() *testCarrier {
	return &testCarrier{ctx: context.Background(), headers: map[string]string{}} //nolint:exhaustruct
}

func (c *testCarrier) Method() string                          { return http.MethodGet }
func (c *testCarrier) Header(string) string                    { return "" }
func (c *testCarrier) HeaderValues(string) []string            { return nil }
func (c *testCarrier) Param(string) string                     { return "" }
func (c *testCarrier) Query() url.Values                       { return url.Values{} }
func (c *testCarrier) ContentType() string                     { return "" }
func (c *testCarrier) Body() ([]byte, error)                   { return nil, nil }
func (c *testCarrier) MultipartForm() (*multipart.Form, error) { return nil, nil }
func (c *testCarrier) SetHeader(k, v string)                   { c.headers[k] = v }
func (c *testCarrier) WriteJSON(s int, body any) error         { c.status, c.json = s, body; return nil }
func (c *testCarrier) WriteBytes(s int, ct string, d []byte) error {
	c.status, c.rawCT, c.raw = s, ct, d
	return nil
}
func (c *testCarrier) WriteEmpty(s int) error         { c.status, c.empty = s, true; return nil }
func (c *testCarrier) Context() context.Context       { return c.ctx }
func (c *testCarrier) SetContext(ctx context.Context) { c.ctx = ctx }
func (c *testCarrier) Abort()                         {}
func (c *testCarrier) RecordError(err error)          { c.errs = append(c.errs, err) }

// writtenJSON marshals whatever the carrier captured (WriteJSON value or raw
// bytes) so a test can compare against an expected wire string.
func (c *testCarrier) writtenJSON(t *testing.T) string {
	t.Helper()
	if c.raw != nil {
		return string(c.raw)
	}
	raw, err := json.Marshal(c.json)
	if err != nil {
		t.Fatalf("marshal written body: %v", err)
	}
	return string(raw)
}

// --- envelope unit tests ----------------------------------------------------

func TestKeyedEnvelopeWrap(t *testing.T) {
	cases := []struct {
		name string
		env  ResponseEnvelope
		data any
		meta any
		want string
	}{
		{"data default", DataEnvelope, widget{Name: "x"}, nil, `{"data":{"name":"x"}}`},
		{"data + meta", DataEnvelope, widget{Name: "x"}, PagingMeta{Count: 1, Pages: 1, PerPage: 1, Current: 1},
			`{"data":{"name":"x"},"meta":{"count":1,"pages":1,"per_page":1,"current":1}}`},
		{"nil data omitted", DataEnvelope, nil, nil, `{}`},
		{"raw drops envelope", RawEnvelope, widget{Name: "x"}, PagingMeta{}, `{"name":"x"}`},
		{"keyed + constants", KeyedEnvelope{DataKey: "result", MetaKey: "", Constants: map[string]any{"success": true}},
			widget{Name: "x"}, nil, `{"result":{"name":"x"},"success":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.env.Wrap(tc.data, tc.meta))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(raw) != tc.want {
				t.Fatalf("Wrap = %s, want %s", raw, tc.want)
			}
		})
	}
}

func TestNewResultRawVsData(t *testing.T) {
	// NewResult renders the pure model (raw); NewDataResult wraps with the default
	// envelope.
	rawC := newTestCarrier()
	_ = NewResult(widget{Name: "x"}).render(rawC)
	if got := rawC.writtenJSON(t); got != `{"name":"x"}` {
		t.Fatalf("NewResult raw = %s", got)
	}

	dataC := newTestCarrier()
	_ = NewDataResult(widget{Name: "x"}).render(dataC)
	if got := dataC.writtenJSON(t); got != `{"data":{"name":"x"}}` {
		t.Fatalf("NewDataResult = %s", got)
	}
}

func TestNewListDataResult(t *testing.T) {
	c := newTestCarrier()
	_ = NewListDataResult([]widget{{Name: "a"}}, 1, 20, 1).render(c)
	want := `{"data":[{"name":"a"}],"meta":{"count":1,"pages":1,"per_page":20,"current":1}}`
	if got := c.writtenJSON(t); got != want {
		t.Fatalf("NewListDataResult = %s, want %s", got, want)
	}
}

// --- doc generation honours the route envelope ------------------------------

func successSchema(t *testing.T, route Route) *openapi3.Schema {
	t.Helper()
	resp := responsesFor(route, nil).Status(http.StatusOK)
	if resp == nil || resp.Value == nil {
		t.Fatal("no 200 response")
	}
	mt := resp.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil {
		t.Fatal("no application/json schema")
	}
	return mt.Schema.Value
}

func widgetRoute(opts ...RouteOption) Route {
	h := func(context.Context, Request[struct{}, struct{}, struct{}, struct{}]) (*widget, error) {
		return &widget{Name: "x"}, nil
	}
	return NewRoute(http.MethodGet, "/w", h, opts...)
}

func TestRawRouteDocsHaveNoEnvelope(t *testing.T) {
	s := successSchema(t, widgetRoute(WithRawResponse()))
	if _, ok := s.Properties["data"]; ok {
		t.Fatal("raw route should not document a data wrapper")
	}
	if _, ok := s.Properties["name"]; !ok {
		t.Fatalf("raw route should document the model directly, got props %v", keysOf(s.Properties))
	}
}

func TestKeyedEnvelopeDocs(t *testing.T) {
	env := KeyedEnvelope{DataKey: "result", MetaKey: "meta", Constants: map[string]any{"success": true}}
	s := successSchema(t, widgetRoute(WithEnvelope(env)))
	for _, key := range []string{"result", "success"} {
		if _, ok := s.Properties[key]; !ok {
			t.Fatalf("missing %q in envelope schema, got %v", key, keysOf(s.Properties))
		}
	}
	if _, ok := s.Properties["data"]; ok {
		t.Fatal("custom DataKey should replace the default data key")
	}
}

func keysOf(props openapi3.Schemas) []string {
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	return out
}

// --- error parser: runtime body + docs --------------------------------------

type stubParser struct{}

func (stubParser) Resolve(err error) (int, any, bool) {
	return http.StatusTeapot, widget{Name: "boom"}, true
}
func (stubParser) ErrorType() reflect.Type { return reflect.TypeFor[widget]() }

func TestErrorParserOwnsBodyAndDocs(t *testing.T) {
	SetErrorParser(stubParser{})
	t.Cleanup(func() { SetErrorParser(nil) })

	// Runtime: the parser owns the FULL body (no {"error": ...} wrapper). Pass the
	// global parser explicitly, the way a route without an App resolves it.
	status, body, wrap := resolveError(context.DeadlineExceeded, nil, loadErrorParser())
	if status != http.StatusTeapot || wrap {
		t.Fatalf("resolveError = (%d, wrap=%v), want (418, wrap=false)", status, wrap)
	}
	if string(body) != `{"name":"boom"}` {
		t.Fatalf("parser body = %s", body)
	}

	// Docs: the default error response is documented from ErrorType(), not the
	// built-in {error} schema.
	resp := responsesFor(widgetRoute(), nil).Value("default")
	schema := resp.Value.Content.Get("application/json").Schema.Value
	if _, ok := schema.Properties["name"]; !ok {
		t.Fatalf("error docs should follow ErrorType(), got %v", keysOf(schema.Properties))
	}
	if _, ok := schema.Properties["error"]; ok {
		t.Fatal("error docs should not use the built-in {error} wrapper when a parser type is set")
	}
}

func TestWithResponseErrorStatusIsNotDataWrapped(t *testing.T) {
	// A documented error status with a body type is the full error body, never
	// {data: T} (the bug this fix addresses).
	route := widgetRoute(WithResponse[widget](http.StatusConflict, "conflict"))
	resp := responsesFor(route, nil).Status(http.StatusConflict)
	schema := resp.Value.Content.Get("application/json").Schema.Value
	if _, ok := schema.Properties["data"]; ok {
		t.Fatal("error-status WithResponse[T] must not wrap in {data}")
	}
	if _, ok := schema.Properties["name"]; !ok {
		t.Fatalf("error-status WithResponse[T] should document T directly, got %v", keysOf(schema.Properties))
	}
}

func TestErrorMapperOwnsFullBody(t *testing.T) {
	mapper := func(error) (int, any, bool) {
		return http.StatusConflict, json.RawMessage(`{"oops":true}`), true
	}
	c := newTestCarrier()
	_ = NewResult(nil).withErrorMapper(mapper).WithError(context.DeadlineExceeded).render(c)
	if c.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", c.status)
	}
	if string(c.raw) != `{"oops":true}` {
		t.Fatalf("mapper body = %s (raw), want full custom body", c.raw)
	}
	if c.rawCT != jsonContentType {
		t.Fatalf("content type = %q", c.rawCT)
	}
}

func TestBuiltinErrorStillWrapped(t *testing.T) {
	// With no mapper/parser, a built-in HTTPError keeps the {"error": ...} envelope.
	c := newTestCarrier()
	_ = NewResult(nil).WithError(NewError(http.StatusBadRequest, "bad", "nope")).render(c)
	got := c.writtenJSON(t)
	want := `{"error":{"code":"bad","message":"nope"}}`
	if got != want {
		t.Fatalf("built-in error = %s, want %s", got, want)
	}
}
