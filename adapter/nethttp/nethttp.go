// Package nethttp adapts the framework-agnostic oapi core onto the net/http
// standard library (Go 1.22+ method-aware ServeMux patterns). Register oapi
// routes with Register / RegisterAll and serve the OpenAPI document with
// SpecHandler.
package nethttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/antlss/oapi"
)

// Middleware is a standard net/http wrapping middleware.
type Middleware func(http.Handler) http.Handler

// maxMultipartMemory bounds the in-memory portion of a parsed multipart form;
// the remainder streams to temp files.
const maxMultipartMemory = 32 << 20

// DefaultMaxRequestBytes caps how many bytes the adapter reads from a request
// body (JSON, urlencoded and multipart), guarding against memory/disk
// exhaustion from oversized uploads. Set it to 0 to disable the cap (e.g. when
// you enforce limits with your own middleware). Override before registering
// routes.
var DefaultMaxRequestBytes int64 = 10 << 20 // 10 MiB

// Register mounts a single oapi.Route on a ServeMux using a method-aware
// pattern. Optional native middlewares wrap the route handler.
func Register(mux *http.ServeMux, route oapi.Route, native ...Middleware) {
	var handler http.Handler = handlerFor(route)
	for i := len(native) - 1; i >= 0; i-- {
		handler = native[i](handler)
	}
	pattern := route.Method() + " " + toStdPath(route.Path())
	mux.Handle(pattern, handler)
}

// RegisterAll mounts every route on the mux.
func RegisterAll(mux *http.ServeMux, routes ...oapi.Route) {
	for _, route := range routes {
		Register(mux, route)
	}
}

// SpecHandler serves a registry's OpenAPI document as JSON, built once.
func SpecHandler(reg *oapi.Registry) http.HandlerFunc {
	var (
		once sync.Once
		raw  []byte
		err  error
	)
	return func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { raw, err = reg.JSON() })
		if err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed to render openapi spec"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(raw)
	}
}

func handlerFor(route oapi.Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cr := &carrier{w: w, r: r, maxBody: DefaultMaxRequestBytes} //nolint:exhaustruct
		defer cr.cleanup()
		route.Invoke(cr)
	}
}

// toStdPath converts the canonical route syntax (:id, *path) to net/http
// wildcard syntax ({id}, {path...}).
func toStdPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case strings.HasPrefix(segment, ":"):
			segments[i] = "{" + segment[1:] + "}"
		case strings.HasPrefix(segment, "*"):
			segments[i] = "{" + segment[1:] + "...}"
		}
	}
	return strings.Join(segments, "/")
}

// carrier adapts net/http to oapi.Carrier.
type carrier struct {
	w       http.ResponseWriter
	r       *http.Request
	maxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	aborted bool
	errs    []error
}

func (a *carrier) Method() string                    { return a.r.Method }
func (a *carrier) Header(name string) string         { return a.r.Header.Get(name) }
func (a *carrier) HeaderValues(name string) []string { return a.r.Header.Values(name) }
func (a *carrier) Param(name string) string          { return a.r.PathValue(name) }

func (a *carrier) Query() url.Values {
	a.queryOnce.Do(func() { a.query = a.r.URL.Query() })
	return a.query
}

func (a *carrier) ContentType() string {
	ct := a.r.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	return media
}

func (a *carrier) Body() ([]byte, error) {
	a.bodyOnce.Do(func() {
		if a.r.Body == nil {
			return
		}
		if a.maxBody > 0 {
			a.r.Body = http.MaxBytesReader(a.w, a.r.Body, a.maxBody)
		}
		a.body, a.bodyErr = io.ReadAll(a.r.Body)
		a.r.Body = io.NopCloser(bytes.NewReader(a.body))
	})
	return a.body, a.bodyErr
}

func (a *carrier) MultipartForm() (*multipart.Form, error) {
	// Bound the whole upload (not just the in-memory part) so large multipart
	// bodies cannot exhaust disk via temp files.
	if a.maxBody > 0 {
		a.r.Body = http.MaxBytesReader(a.w, a.r.Body, a.maxBody)
	}
	if err := a.r.ParseMultipartForm(maxMultipartMemory); err != nil {
		return nil, err
	}
	return a.r.MultipartForm, nil
}

// cleanup removes any temp files net/http spilled to disk while parsing a
// multipart form. net/http never does this for you, so without it large uploads
// leak files into the temp dir for the lifetime of the process.
func (a *carrier) cleanup() {
	if a.r.MultipartForm != nil {
		_ = a.r.MultipartForm.RemoveAll()
	}
}

func (a *carrier) SetHeader(key, value string) { a.w.Header().Set(key, value) }

func (a *carrier) WriteJSON(status int, body any) error {
	// Marshal first so an encode failure is caught before the status/body are
	// committed, and so the bytes match the other adapters exactly (encoding/json
	// with no trailing newline, unlike json.Encoder).
	raw, err := json.Marshal(body)
	a.w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		a.w.WriteHeader(http.StatusInternalServerError)
		_, _ = a.w.Write([]byte(`{"error":{"message":"failed to encode response"}}`))
		return err
	}
	a.w.WriteHeader(status)
	_, werr := a.w.Write(raw)
	return werr
}

func (a *carrier) WriteBytes(status int, contentType string, data []byte) error {
	a.w.Header().Set("Content-Type", contentType)
	a.w.WriteHeader(status)
	_, err := a.w.Write(data)
	return err
}

func (a *carrier) WriteEmpty(status int) error {
	a.w.WriteHeader(status)
	return nil
}

func (a *carrier) Context() context.Context { return a.r.Context() }
func (a *carrier) SetContext(ctx context.Context) {
	a.r = a.r.WithContext(ctx)
}

func (a *carrier) Abort()                { a.aborted = true }
func (a *carrier) RecordError(err error) { a.errs = append(a.errs, err) }

// Errors exposes recorded errors for net/http logging middleware.
func (a *carrier) Errors() []error { return a.errs }
