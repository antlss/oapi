// Package oapitest is a stdlib-only test harness for unit-testing oapi typed
// handlers without a running server or a real router.
//
// An oapi.Route is normally driven by a framework adapter that implements
// oapi.Carrier around an in-flight request/response. This package provides the
// same seam backed by an in-memory *http.Request and an *httptest.ResponseRecorder,
// so a test can build a request fluently, Invoke the route, and inspect exactly
// what the handler wrote.
//
//	rec := oapitest.New(http.MethodGet, "/products/1?page=2").
//		Header("Authorization", "Bearer t").
//		Param("id", "1").
//		Invoke(route)
//	if rec.Status != http.StatusOK {
//		t.Fatalf("status = %d", rec.Status)
//	}
//	var out Product
//	_ = rec.DecodeJSON(&out)
//
// The carrier mirrors the net/http adapter's behavior (canonical case-insensitive
// headers, media-type stripping, re-readable cached body, byte-identical WriteJSON)
// so a route behaves under test exactly as it would in production.
package oapitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"

	"github.com/antlss/oapi"
)

// FileParam is one file part for a multipart/form-data request body.
type FileParam struct {
	Filename string
	Content  []byte
}

// Builder fluently assembles an in-memory request and invokes a route against it.
// The zero value is not usable; create one with New. Every setter returns the
// Builder so calls can be chained, and Invoke is terminal.
type Builder struct {
	method      string
	target      string
	header      http.Header
	params      map[string]string
	extraQuery  url.Values
	body        io.Reader
	contentType string
	ctx         context.Context
}

// New starts a request for the given method and target. target is a URL path with
// an optional query string, e.g. "/products/1?page=2".
func New(method, target string) *Builder {
	return &Builder{
		method:     method,
		target:     target,
		header:     http.Header{},
		params:     map[string]string{},
		extraQuery: url.Values{},
		ctx:        context.Background(),
	}
}

// Header adds a request header value. Repeated calls with the same key append,
// matching net/http multi-value header semantics.
func (b *Builder) Header(key, value string) *Builder {
	b.header.Add(key, value)
	return b
}

// Param injects a path parameter. The real router fills these from the matched
// route pattern; in a harness the test provides them directly so the bound `uri`
// struct sees the same values.
func (b *Builder) Param(name, value string) *Builder {
	b.params[name] = value
	return b
}

// Query adds a query parameter. Values merge with any query already present in the
// target, so New("/x?a=1").Query("b", "2") yields a=1&b=2.
func (b *Builder) Query(key, value string) *Builder {
	b.extraQuery.Add(key, value)
	return b
}

// JSON marshals v as the request body and sets Content-Type application/json.
func (b *Builder) JSON(v any) *Builder {
	raw, err := json.Marshal(v)
	if err != nil {
		// Surface the failure as an unreadable body rather than panicking in a
		// setter; Invoke will see a body read error and the handler renders 400.
		b.body = &errReader{err: err}
		b.contentType = "application/json"
		return b
	}
	b.body = bytes.NewReader(raw)
	b.contentType = "application/json"
	return b
}

// Form sets an application/x-www-form-urlencoded request body.
func (b *Builder) Form(values url.Values) *Builder {
	b.body = strings.NewReader(values.Encode())
	b.contentType = "application/x-www-form-urlencoded"
	return b
}

// Multipart builds a multipart/form-data body from text fields and files.
func (b *Builder) Multipart(fields map[string]string, files map[string]FileParam) *Builder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		_ = w.WriteField(name, value)
	}
	for field, fp := range files {
		part, err := w.CreateFormFile(field, fp.Filename)
		if err == nil {
			_, _ = part.Write(fp.Content)
		}
	}
	_ = w.Close()
	b.body = &buf
	b.contentType = w.FormDataContentType()
	return b
}

// Body sets a raw request body with an explicit content type.
func (b *Builder) Body(contentType string, r io.Reader) *Builder {
	b.body = r
	b.contentType = contentType
	return b
}

// Context sets the base per-request context the handler observes via
// oapi's Context()/SetContext seam. Defaults to context.Background().
func (b *Builder) Context(ctx context.Context) *Builder {
	if ctx != nil {
		b.ctx = ctx
	}
	return b
}

// Invoke builds the request, runs route.Invoke against an in-memory carrier and
// returns what the handler wrote.
func (b *Builder) Invoke(route oapi.Route) *Recorded {
	target := b.target
	if len(b.extraQuery) > 0 {
		target = mergeQuery(target, b.extraQuery)
	}

	body := b.body
	if body == nil {
		body = http.NoBody
	}
	req := httptest.NewRequest(b.method, target, body).WithContext(b.ctx)

	// Copy headers, then set Content-Type from the chosen body encoder unless the
	// caller already set one explicitly.
	for key, values := range b.header {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	if b.contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", b.contentType)
	}

	rec := httptest.NewRecorder()
	c := &carrier{
		req:    req,
		rec:    rec,
		params: b.params,
	}
	route.Invoke(c)

	return &Recorded{
		Status: rec.Code,
		Header: rec.Header(),
		Body:   rec.Body.Bytes(),
		Errors: c.errs,
	}
}

// mergeQuery appends extra query parameters to target's existing query string.
func mergeQuery(target string, extra url.Values) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	for key, values := range extra {
		for _, v := range values {
			q.Add(key, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Recorded is the captured outcome of an invoked route.
type Recorded struct {
	Status int
	Header http.Header
	Body   []byte
	// Errors are the non-fatal errors the handler/middleware reported via the
	// carrier's RecordError (e.g. the business error behind a rendered 4xx/5xx).
	Errors []error
}

// DecodeJSON unmarshals the response body into v.
func (r *Recorded) DecodeJSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// BodyString returns the response body as a string.
func (r *Recorded) BodyString() string {
	return string(r.Body)
}

// errReader is an io.Reader that always fails, used to propagate a JSON marshal
// error from the JSON setter into the request-body read path.
type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

// carrier implements oapi.Carrier over an in-memory *http.Request and an
// *httptest.ResponseRecorder. It mirrors the net/http adapter's behavior so a
// route under test behaves exactly as it would in production.
type carrier struct {
	req    *http.Request
	rec    *httptest.ResponseRecorder
	params map[string]string

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	aborted bool
	errs    []error
}

func (c *carrier) Method() string                    { return c.req.Method }
func (c *carrier) Header(name string) string         { return c.req.Header.Get(name) }
func (c *carrier) HeaderValues(name string) []string { return c.req.Header.Values(name) }
func (c *carrier) Param(name string) string          { return c.params[name] }

func (c *carrier) Query() url.Values {
	c.queryOnce.Do(func() { c.query = c.req.URL.Query() })
	return c.query
}

func (c *carrier) ContentType() string {
	ct := c.req.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	return media
}

func (c *carrier) Body() ([]byte, error) {
	c.bodyOnce.Do(func() {
		if c.req.Body == nil {
			return
		}
		c.body, c.bodyErr = io.ReadAll(c.req.Body)
		// Make the body re-readable so a typed middleware and the handler can both
		// bind it, matching the adapter contract.
		c.req.Body = io.NopCloser(bytes.NewReader(c.body))
	})
	return c.body, c.bodyErr
}

func (c *carrier) MultipartForm() (*multipart.Form, error) {
	if err := c.req.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return c.req.MultipartForm, nil
}

func (c *carrier) SetHeader(key, value string) { c.rec.Header().Set(key, value) }

func (c *carrier) WriteJSON(status int, body any) error {
	// Marshal first so an encode failure is caught before status/body are
	// committed, and so the bytes match the adapters exactly (encoding/json with
	// no trailing newline).
	raw, err := json.Marshal(body)
	c.rec.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		c.rec.WriteHeader(http.StatusInternalServerError)
		_, _ = c.rec.Write([]byte(`{"error":{"message":"failed to encode response"}}`))
		return err
	}
	c.rec.WriteHeader(status)
	_, werr := c.rec.Write(raw)
	return werr
}

func (c *carrier) WriteBytes(status int, contentType string, data []byte) error {
	c.rec.Header().Set("Content-Type", contentType)
	c.rec.WriteHeader(status)
	_, err := c.rec.Write(data)
	return err
}

func (c *carrier) WriteEmpty(status int) error {
	c.rec.WriteHeader(status)
	return nil
}

func (c *carrier) Context() context.Context { return c.req.Context() }
func (c *carrier) SetContext(ctx context.Context) {
	c.req = c.req.WithContext(ctx)
}

func (c *carrier) Abort()                { c.aborted = true }
func (c *carrier) RecordError(err error) { c.errs = append(c.errs, err) }

// compile-time assertion that carrier satisfies the Carrier seam.
var _ oapi.Carrier = (*carrier)(nil)
