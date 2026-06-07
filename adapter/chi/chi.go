// Package chi adapts the framework-agnostic oapi core onto the go-chi/chi v5
// router. Register oapi routes with Register / RegisterAll and serve the
// generated OpenAPI document with SpecHandler.
package chi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/antlss/oapi"
)

// Middleware is a standard net/http wrapping middleware, as used by chi.
type Middleware func(http.Handler) http.Handler

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
		cr := &carrier{HTTPCarrier: &oapi.HTTPCarrier{W: w, R: r, MaxBody: route.MaxRequestBytesOr(DefaultMaxRequestBytes)}} //nolint:exhaustruct
		defer cr.Cleanup()
		route.Invoke(cr)
	}
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

// carrier adapts net/http (driven by chi) to oapi.Carrier. Everything except
// path-parameter lookup is the shared net/http behaviour in [oapi.HTTPCarrier];
// only Param is chi-specific (chi.URLParam, with catch-all fallback).
type carrier struct {
	*oapi.HTTPCarrier
}

func (a *carrier) Param(name string) string {
	if v := chi.URLParam(a.R, name); v != "" {
		return v
	}
	// A catch-all segment (`*path`) is registered as chi's anonymous "*"
	// wildcard (see toChiPath), so the original name no longer resolves. Fall
	// back to the wildcard value so catch-all params bind the same as on the
	// other adapters.
	return chi.URLParam(a.R, "*")
}
