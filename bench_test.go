package oapi

// Benchmarks establishing baselines for the request hot path (Invoke → bind →
// validate → render) and for one-shot OpenAPI document generation, so Wave-4
// perf changes can be compared with benchstat.

import (
	"context"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"
)

type benchHeader struct {
	Auth string `header:"Authorization"`
}

type benchQuery struct {
	Page    int `form:"page"`
	PerPage int `form:"per_page"`
}

type benchBody struct {
	Name  string  `json:"name"  binding:"required"`
	Price float64 `json:"price" binding:"required,gt=0"`
}

type benchResp struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type benchValidator struct{}

func (benchValidator) Validate(any, string) error { return nil }

// benchCarrier is a reusable in-memory Carrier whose reads are fixed, so a
// benchmark measures Invoke's per-call cost (new execution, bind, render) rather
// than request construction.
type benchCarrier struct {
	header http.Header
	query  url.Values
	body   []byte
	ctx    context.Context
	status int
}

func newBenchCarrier() *benchCarrier {
	return &benchCarrier{
		header: http.Header{"Authorization": {"Bearer x"}},
		query:  url.Values{"page": {"2"}, "per_page": {"20"}},
		body:   []byte(`{"name":"Keyboard","price":49.9}`),
		ctx:    context.Background(),
		status: 0,
	}
}

func (c *benchCarrier) Method() string         { return http.MethodPost }
func (c *benchCarrier) Header(n string) string { return c.header.Get(n) }
func (c *benchCarrier) HeaderValues(n string) []string {
	return c.header.Values(n)
}
func (c *benchCarrier) Param(string) string                        { return "" }
func (c *benchCarrier) Query() url.Values                          { return c.query }
func (c *benchCarrier) ContentType() string                        { return mimeJSON }
func (c *benchCarrier) Body() ([]byte, error)                      { return c.body, nil }
func (c *benchCarrier) MultipartForm() (*multipart.Form, error)    { return nil, nil }
func (c *benchCarrier) SetHeader(string, string)                   {}
func (c *benchCarrier) WriteJSON(s int, _ any) error               { c.status = s; return nil }
func (c *benchCarrier) WriteBytes(s int, _ string, _ []byte) error { c.status = s; return nil }
func (c *benchCarrier) WriteEmpty(s int) error                     { c.status = s; return nil }
func (c *benchCarrier) Context() context.Context                   { return c.ctx }
func (c *benchCarrier) SetContext(ctx context.Context)             { c.ctx = ctx }
func (c *benchCarrier) Abort()                                     {}
func (c *benchCarrier) RecordError(error)                          {}

func benchFullRoute() Route {
	app := New(WithValidator(benchValidator{}))
	return NewRoute(
		http.MethodPost, "/things",
		func(_ context.Context, req Request[benchHeader, struct{}, benchQuery, benchBody]) (*benchResp, error) {
			return &benchResp{ID: 1, Name: req.Body.Name}, nil
		},
		WithApp(app),
	)
}

func BenchmarkInvoke_FullBind(b *testing.B) {
	route := benchFullRoute()
	c := newBenchCarrier()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		route.Invoke(c)
	}
}

// BenchmarkInvoke_WithMiddleware exercises the parse-once cache: a typed
// middleware that binds the same Request shape as the handler should reuse the
// single cached parse.
func BenchmarkInvoke_WithMiddleware(b *testing.B) {
	app := New(WithValidator(benchValidator{}))
	mw := func(ctx context.Context, _ Request[benchHeader, struct{}, struct{}, struct{}]) (context.Context, error) {
		return ctx, nil
	}
	route := NewRoute(
		http.MethodPost, "/things",
		func(_ context.Context, req Request[benchHeader, struct{}, benchQuery, benchBody]) (*benchResp, error) {
			return &benchResp{ID: 1, Name: req.Body.Name}, nil
		},
		WithApp(app),
		WithTypedBefore(mw),
	)
	c := newBenchCarrier()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		route.Invoke(c)
	}
}

func benchRegistry(useComponents bool) *Registry {
	get := NewRoute(http.MethodGet, "/things/:id",
		func(context.Context, Request[struct{}, widgetURI, struct{}, struct{}]) (*benchResp, error) {
			return nil, nil
		})
	list := NewRoute(http.MethodGet, "/things",
		func(context.Context, Request[struct{}, struct{}, benchQuery, struct{}]) (*benchResp, error) {
			return nil, nil
		})
	create := NewRoute(http.MethodPost, "/things",
		func(context.Context, Request[benchHeader, struct{}, struct{}, benchBody]) (*benchResp, error) {
			return nil, nil
		})
	rg := NewRegistry("bench", "v1").Add(get, list, create)
	if useComponents {
		rg.UseComponents()
	}
	return rg
}

func BenchmarkOpenAPI_Inline(b *testing.B) {
	rg := benchRegistry(false)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = rg.OpenAPI()
	}
}

func BenchmarkOpenAPI_Components(b *testing.B) {
	rg := benchRegistry(true)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = rg.OpenAPI()
	}
}
