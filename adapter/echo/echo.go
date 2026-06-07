// Package echo adapts the framework-agnostic oapi core onto the Echo v4 web
// framework. Register oapi routes with Register / RegisterAll and serve the
// generated OpenAPI document with SpecHandler.
package echo

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/antlss/oapi"
)

// DefaultMaxRequestBytes caps how many bytes the adapter reads from a request
// body (JSON, urlencoded and multipart), guarding against memory/disk
// exhaustion from oversized uploads. Set it to 0 to disable the cap (e.g. when
// you enforce limits with your own middleware). Override before registering
// routes. A per-route App cap ([oapi.WithApp] + [oapi.WithMaxRequestBytes])
// takes precedence over this value.
var DefaultMaxRequestBytes int64 = 10 << 20 // 10 MiB

// router is the subset of Echo's routing surface shared by *echo.Echo and
// *echo.Group, so routes can be mounted on either.
type router interface {
	Add(method, path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route
}

// Register mounts a single oapi.Route on an Echo router (*echo.Echo or
// *echo.Group). Optional native Echo middlewares wrap the route handler.
func Register(r router, route oapi.Route, native ...echo.MiddlewareFunc) {
	r.Add(route.Method(), toEchoPath(route.Path()), handlerFor(route), native...)
}

// RegisterAll mounts every route on the router.
func RegisterAll(r router, routes ...oapi.Route) {
	for _, route := range routes {
		Register(r, route)
	}
}

// SpecHandler serves a registry's OpenAPI document as JSON, built once.
func SpecHandler(reg *oapi.Registry) echo.HandlerFunc {
	spec := reg.SpecBytesOnce()
	return func(c echo.Context) error {
		raw, err := spec()
		if err != nil {
			return c.JSONBlob(http.StatusInternalServerError,
				[]byte(`{"message":"failed to render openapi spec"}`))
		}
		return c.Blob(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}

func handlerFor(route oapi.Route) echo.HandlerFunc {
	return func(c echo.Context) error {
		cr := &carrier{
			HTTPCarrier: &oapi.HTTPCarrier{W: c.Response(), R: c.Request(), MaxBody: route.MaxRequestBytesOr(DefaultMaxRequestBytes)}, //nolint:exhaustruct
			c:           c,
		}
		defer cr.Cleanup()
		route.Invoke(cr)
		return nil
	}
}

// toEchoPath converts the canonical route syntax to Echo's: :id params are
// already Echo's syntax, and *path catch-all becomes Echo's "*" wildcard.
func toEchoPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "*") {
			segments[i] = "*"
		}
	}
	return strings.Join(segments, "/")
}

// carrier adapts echo.Context to oapi.Carrier. The read/write plumbing is the
// shared net/http behaviour in [oapi.HTTPCarrier], seeded from c.Request() /
// c.Response(). Only the two methods that go *through* echo.Context are
// overridden: Param (echo's router) and SetContext (echo owns the request).
type carrier struct {
	*oapi.HTTPCarrier
	c echo.Context
}

func (a *carrier) Param(name string) string {
	if v := a.c.Param(name); v != "" {
		return v
	}
	// A catch-all segment (`*path`) is registered as Echo's anonymous "*"
	// wildcard (see toEchoPath), so the original name no longer resolves. Fall
	// back to the wildcard value so catch-all params bind the same as on the
	// other adapters.
	return a.c.Param("*")
}

// SetContext swaps the request context. Echo holds the request inside its
// echo.Context, so the new request must be pushed back via SetRequest;
// HTTPCarrier.R is then re-synced so the shared methods (Context, Body, writes)
// see the same one.
func (a *carrier) SetContext(ctx context.Context) {
	a.c.SetRequest(a.R.WithContext(ctx))
	a.R = a.c.Request()
}
