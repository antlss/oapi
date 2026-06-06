// Package chi adapts the framework-agnostic oapi core onto the go-chi/chi v5
// router. Register oapi routes with Register / RegisterAll and serve the
// generated OpenAPI document with SpecHandler.
package chi

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

	"github.com/go-chi/chi/v5"

	"github.com/antlss/oapi"
)

// Middleware is a standard net/http wrapping middleware, as used by chi.
type Middleware func(http.Handler) http.Handler

// maxMultipartMemory bounds the in-memory portion of a parsed multipart form;
// the remainder streams to temp files.
const maxMultipartMemory = 32 << 20

// DefaultMaxRequestBytes caps how many bytes the adapter reads from a request
// body (JSON, urlencoded and multipart), guarding against memory/disk
// exhaustion from oversized uploads. Set it to 0 to disable the cap (e.g. when
// you enforce limits with your own middleware). Override before registering
// routes. A per-route App cap ([oapi.WithApp] + [oapi.WithMaxRequestBytes])
// takes precedence over this value.
var DefaultMaxRequestBytes int64 = 10 << 20 // 10 MiB

// Register mounts a single oapi.Route on a chi.Router using a method-aware
// pattern. Optional native chi middlewares wrap the route handler.
func Register(router chi.Router, route oapi.Route, native ...Middleware) {
	r := router
	if len(native) > 0 {
		mws := make([]func(http.Handler) http.Handler, len(native))
		for i, m := range native {
			mws[i] = m
		}
		r = router.With(mws...)
	}
	r.MethodFunc(route.Method(), toChiPath(route.Path()), handlerFor(route))
}

// RegisterAll mounts every route on the router.
func RegisterAll(router chi.Router, routes ...oapi.Route) {
	for _, route := range routes {
		Register(router, route)
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed to render openapi spec"}`))
			return
		}
		_, _ = w.Write(raw)
	}
}

func handlerFor(route oapi.Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cr := &carrier{w: w, r: r, maxBody: maxBodyFor(route)} //nolint:exhaustruct
		defer cr.cleanup()
		route.Invoke(cr)
	}
}

// maxBodyFor resolves the request body cap for a route: the per-route App cap
// when one is configured, otherwise the package DefaultMaxRequestBytes.
func maxBodyFor(route oapi.Route) int64 {
	if limit, ok := route.MaxRequestBytes(); ok {
		return limit
	}
	return DefaultMaxRequestBytes
}

// toChiPath converts the canonical route syntax to chi's: :id params become
// {id}, and a *path catch-all becomes chi's trailing "/*" wildcard.
func toChiPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case strings.HasPrefix(segment, ":"):
			segments[i] = "{" + segment[1:] + "}"
		case strings.HasPrefix(segment, "*"):
			segments[i] = "*"
		}
	}
	return strings.Join(segments, "/")
}

// carrier adapts net/http (driven by chi) to oapi.Carrier.
type carrier struct {
	w       http.ResponseWriter
	r       *http.Request
	maxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	// errs collects RecordError calls for logging middleware (see Errors).
	errs []error
}

func (a *carrier) Method() string                    { return a.r.Method }
func (a *carrier) Header(name string) string         { return a.r.Header.Get(name) }
func (a *carrier) HeaderValues(name string) []string { return a.r.Header.Values(name) }

func (a *carrier) Param(name string) string {
	if v := chi.URLParam(a.r, name); v != "" {
		return v
	}
	// A catch-all segment (`*path`) is registered as chi's anonymous "*"
	// wildcard (see toChiPath), so the original name no longer resolves. Fall
	// back to the wildcard value so catch-all params bind the same as on the
	// other adapters.
	return chi.URLParam(a.r, "*")
}

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

// Abort is a no-op: chi has no adapter-side after-middleware to skip (native
// middleware registered via With wraps the whole handler, so it cannot observe
// an abort from inside). The core calls it when rendering an error; gin uses it
// for real.
func (a *carrier) Abort()                {}
func (a *carrier) RecordError(err error) { a.errs = append(a.errs, err) }

// Errors exposes recorded errors for chi logging middleware.
func (a *carrier) Errors() []error { return a.errs }
