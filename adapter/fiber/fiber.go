// Package fiber adapts the framework-agnostic oapi core onto the Fiber v2 web
// framework (fasthttp). Register oapi routes with Register / RegisterAll and
// serve the OpenAPI document with SpecHandler.
//
// Request body size is bounded by Fiber itself via fiber.Config.BodyLimit
// (default 4 MiB), which rejects oversized bodies before the handler runs, so
// this adapter needs no extra cap. fasthttp also cleans up multipart temp files
// automatically at the end of each request.
package fiber

import (
	"context"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"

	"github.com/antlss/oapi"
)

// Register mounts a single oapi.Route on a Fiber router. Optional native Fiber
// handlers run before the route handler.
func Register(router fiber.Router, route oapi.Route, native ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(native)+1)
	handlers = append(handlers, native...)
	handlers = append(handlers, handlerFor(route))
	router.Add(route.Method(), toFiberPath(route.Path()), handlers...)
}

// RegisterAll mounts every route on the router.
func RegisterAll(router fiber.Router, routes ...oapi.Route) {
	for _, route := range routes {
		Register(router, route)
	}
}

// SpecHandler serves a registry's OpenAPI document as JSON, built once.
func SpecHandler(reg *oapi.Registry) fiber.Handler {
	var (
		once sync.Once
		raw  []byte
		err  error
	)
	return func(c *fiber.Ctx) error {
		once.Do(func() { raw, err = reg.JSON() })
		if err != nil {
			return c.Status(http.StatusInternalServerError).
				JSON(fiber.Map{"message": "failed to render openapi spec"})
		}
		c.Set("Content-Type", "application/json; charset=utf-8")
		return c.Send(raw)
	}
}

func handlerFor(route oapi.Route) fiber.Handler {
	return func(c *fiber.Ctx) error {
		cr := &carrier{c: c} //nolint:exhaustruct
		route.Invoke(cr)
		return cr.writeErr
	}
}

// toFiberPath converts the canonical route syntax to Fiber's: :id stays, and
// *path becomes Fiber's "*" wildcard.
func toFiberPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "*") {
			segments[i] = "*"
		}
	}
	return strings.Join(segments, "/")
}

// carrier adapts *fiber.Ctx to oapi.Carrier.
type carrier struct {
	c *fiber.Ctx

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte

	writeErr error
	aborted  bool
	errs     []error
}

func (a *carrier) Method() string            { return a.c.Method() }
func (a *carrier) Header(name string) string { return a.c.Get(name) }

func (a *carrier) HeaderValues(name string) []string {
	peek := a.c.Request().Header.PeekAll(name)
	out := make([]string, 0, len(peek))
	for _, v := range peek {
		out = append(out, string(v))
	}
	return out
}

func (a *carrier) Param(name string) string {
	if v := a.c.Params(name); v != "" {
		return v
	}
	// A catch-all segment (`*path`) is registered as Fiber's anonymous "*"
	// wildcard (see toFiberPath), so the original name no longer resolves. Fall
	// back to the wildcard value so catch-all params bind the same as on the gin
	// and net/http adapters.
	return a.c.Params("*")
}

func (a *carrier) Query() url.Values {
	a.queryOnce.Do(func() {
		a.query = url.Values{}
		a.c.Context().QueryArgs().VisitAll(func(k, v []byte) {
			a.query.Add(string(k), string(v)) // copy: the args buffer is reused
		})
	})
	return a.query
}

func (a *carrier) ContentType() string {
	ct := a.c.Get("Content-Type")
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
		// fasthttp reuses the body buffer after the handler returns; copy it.
		a.body = append([]byte(nil), a.c.Body()...)
	})
	return a.body, nil
}

func (a *carrier) MultipartForm() (*multipart.Form, error) { return a.c.MultipartForm() }

func (a *carrier) SetHeader(key, value string) { a.c.Set(key, value) }

func (a *carrier) WriteJSON(status int, body any) error {
	a.writeErr = a.c.Status(status).JSON(body)
	return a.writeErr
}

func (a *carrier) WriteBytes(status int, contentType string, data []byte) error {
	a.c.Set("Content-Type", contentType)
	a.writeErr = a.c.Status(status).Send(data)
	return a.writeErr
}

func (a *carrier) WriteEmpty(status int) error {
	a.c.Status(status)
	return nil
}

func (a *carrier) Context() context.Context       { return a.c.UserContext() }
func (a *carrier) SetContext(ctx context.Context) { a.c.SetUserContext(ctx) }

func (a *carrier) Abort()                { a.aborted = true }
func (a *carrier) RecordError(err error) { a.errs = append(a.errs, err) }
