// Package nethttp adapts the framework-agnostic oapi core onto the net/http
// standard library (Go 1.22+ method-aware ServeMux patterns). Register oapi
// routes with Register / RegisterAll and serve the OpenAPI document with
// SpecHandler.
package nethttp

import (
	"net/http"
	"strings"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/internal/httpcarrier"
)

// Middleware is a standard net/http wrapping middleware.
type Middleware func(http.Handler) http.Handler

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
	spec := reg.SpecBytesOnce()
	return func(w http.ResponseWriter, _ *http.Request) {
		raw, err := spec()
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
		cr := &carrier{Base: &httpcarrier.Base{W: w, R: r, MaxBody: route.MaxRequestBytesOr(DefaultMaxRequestBytes)}} //nolint:exhaustruct
		defer cr.Cleanup()
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

// carrier adapts net/http to oapi.Carrier. Everything except path-parameter
// lookup is the shared net/http behaviour in [httpcarrier.Base]; only Param is
// net/http-specific (Go 1.22+ ServeMux PathValue).
type carrier struct {
	*httpcarrier.Base
}

func (a *carrier) Param(name string) string { return a.R.PathValue(name) }
