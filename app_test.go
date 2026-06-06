package oapi

// Tests for P1-1 scoped configuration (App). They prove that:
//   - two Apps with different validators/envelopes serve independently;
//   - a route built without an App still reads the process-wide globals (back-compat);
//   - WithApp(nil) is a safe no-op;
//   - WithMaxRequestBytes is exposed via Route.MaxRequestBytes;
//   - concurrent Apps do not race (run with -race).
//
// They run in the internal package so they can save/restore the unexported global
// validator and use a small in-package carrier.

import (
	"context"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// errValidator is a Validator that always returns its configured error, so a test
// can tell which App's validator ran by the outcome.
type errValidator struct{ err error }

func (e errValidator) Validate(any, string) error { return e.err }

type appBody struct {
	Name string `json:"name" binding:"required"`
}

type appOK struct {
	A int `json:"a"`
}

// appCarrier is a minimal in-memory oapi.Carrier for these tests (the shared
// mockCarrier moved out with review_fixes_test.go). It records what was written.
type appCarrier struct {
	status   int
	wrote    string // "json" | "bytes" | "empty"
	jsonBody any
	ctx      context.Context
}

func newAppCarrier() *appCarrier { return &appCarrier{ctx: context.Background()} } //nolint:exhaustruct

func (c *appCarrier) Method() string                          { return http.MethodPost }
func (c *appCarrier) Header(string) string                    { return "" }
func (c *appCarrier) HeaderValues(string) []string            { return nil }
func (c *appCarrier) Param(string) string                     { return "" }
func (c *appCarrier) Query() url.Values                       { return url.Values{} }
func (c *appCarrier) ContentType() string                     { return "" }
func (c *appCarrier) Body() ([]byte, error)                   { return nil, nil }
func (c *appCarrier) MultipartForm() (*multipart.Form, error) { return nil, nil }
func (c *appCarrier) SetHeader(string, string)                {}
func (c *appCarrier) WriteJSON(s int, b any) error {
	c.status, c.wrote, c.jsonBody = s, "json", b
	return nil
}
func (c *appCarrier) WriteBytes(s int, _ string, _ []byte) error {
	c.status, c.wrote = s, "bytes"
	return nil
}
func (c *appCarrier) WriteEmpty(s int) error         { c.status, c.wrote = s, "empty"; return nil }
func (c *appCarrier) Context() context.Context       { return c.ctx }
func (c *appCarrier) SetContext(ctx context.Context) { c.ctx = ctx }
func (c *appCarrier) Abort()                         {}
func (c *appCarrier) RecordError(error)              {}

// appRoute builds a typed POST route that returns a fixed body, attaching app when
// non-nil.
func appRoute(app *App) Route {
	opts := []RouteOption{}
	if app != nil {
		opts = append(opts, WithApp(app))
	}
	return NewRoute(
		http.MethodPost, "/x",
		func(_ context.Context, _ Request[struct{}, struct{}, struct{}, appBody]) (*appOK, error) {
			return &appOK{A: 1}, nil
		},
		opts...,
	)
}

func TestApp_PerAppValidatorIsIndependent(t *testing.T) {
	reject := New(WithValidator(errValidator{err: NewValidationError("rejected by A", nil)}))
	accept := New(WithValidator(errValidator{err: nil}))

	cReject := newAppCarrier()
	appRoute(reject).Invoke(cReject)
	if cReject.status != http.StatusBadRequest {
		t.Fatalf("reject App: status = %d, want 400", cReject.status)
	}

	cAccept := newAppCarrier()
	appRoute(accept).Invoke(cAccept)
	if cAccept.status != http.StatusOK {
		t.Fatalf("accept App: status = %d, want 200", cAccept.status)
	}
}

func TestApp_PerAppEnvelopeIsIndependent(t *testing.T) {
	raw := New(WithValidator(errValidator{}), WithResponseEnvelope(RawEnvelope))
	data := New(WithValidator(errValidator{}), WithResponseEnvelope(DataEnvelope))

	cRaw := newAppCarrier()
	appRoute(raw).Invoke(cRaw)
	if _, ok := cRaw.jsonBody.(*appOK); !ok {
		t.Fatalf("raw App: body is %T, want *appOK (no envelope)", cRaw.jsonBody)
	}

	cData := newAppCarrier()
	appRoute(data).Invoke(cData)
	m, ok := cData.jsonBody.(map[string]any)
	if !ok || m["data"] == nil {
		t.Fatalf("data App: body is %T (%v), want map with a \"data\" key", cData.jsonBody, cData.jsonBody)
	}
}

func TestApp_BackCompatGlobalValidator(t *testing.T) {
	// Save and restore the process-wide validator so this test does not leak state.
	savedV, savedSet := validatorImpl, validatorConfigured
	t.Cleanup(func() { validatorImpl, validatorConfigured = savedV, savedSet })

	SetValidator(errValidator{err: NewValidationError("global reject", nil)})

	c := newAppCarrier()
	appRoute(nil).Invoke(c) // no App → must read the global validator
	if c.status != http.StatusBadRequest {
		t.Fatalf("global validator path: status = %d, want 400", c.status)
	}
}

func TestApp_WithAppNilIsSafe(t *testing.T) {
	c := newAppCarrier()
	r := NewRoute(
		http.MethodPost, "/x",
		func(_ context.Context, _ Request[struct{}, struct{}, struct{}, appBody]) (*appOK, error) {
			return &appOK{A: 1}, nil
		},
		WithApp(nil),
	)
	r.Invoke(c) // must not panic; cfg stays nil → global behaviour
	if c.wrote == "" {
		t.Fatal("WithApp(nil) route did not write a response")
	}
}

func TestApp_MaxRequestBytes(t *testing.T) {
	app := New(WithMaxRequestBytes(123))
	withApp := appRoute(app)
	if limit, ok := withApp.MaxRequestBytes(); !ok || limit != 123 {
		t.Fatalf("MaxRequestBytes = (%d, %v), want (123, true)", limit, ok)
	}

	noApp := appRoute(nil)
	if _, ok := noApp.MaxRequestBytes(); ok {
		t.Fatal("route without an App should report no configured cap (ok=false)")
	}
}

func TestApp_ConcurrentAppsDoNotRace(t *testing.T) {
	reject := New(WithValidator(errValidator{err: NewValidationError("A", nil)}))
	accept := New(WithValidator(errValidator{err: nil}))
	rReject, rAccept := appRoute(reject), appRoute(accept)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); rReject.Invoke(newAppCarrier()) }()
		go func() { defer wg.Done(); rAccept.Invoke(newAppCarrier()) }()
	}
	wg.Wait()
}
