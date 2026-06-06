// Package echo adapts the framework-agnostic oapi core onto the Echo v4 web
// framework. Register oapi routes with Register / RegisterAll and serve the
// generated OpenAPI document with SpecHandler.
package echo

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

	"github.com/labstack/echo/v4"

	"github.com/antlss/oapi"
)

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
	var (
		once sync.Once
		raw  []byte
		err  error
	)
	return func(c echo.Context) error {
		once.Do(func() { raw, err = reg.JSON() })
		if err != nil {
			return c.JSONBlob(http.StatusInternalServerError,
				[]byte(`{"message":"failed to render openapi spec"}`))
		}
		return c.Blob(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}

func handlerFor(route oapi.Route) echo.HandlerFunc {
	return func(c echo.Context) error {
		cr := &carrier{c: c, maxBody: maxBodyFor(route)} //nolint:exhaustruct
		defer cr.cleanup()
		route.Invoke(cr)
		return nil
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

// carrier adapts echo.Context to oapi.Carrier.
type carrier struct {
	c       echo.Context
	maxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	aborted bool
	errs    []error
}

func (a *carrier) Method() string            { return a.c.Request().Method }
func (a *carrier) Header(name string) string { return a.c.Request().Header.Get(name) }
func (a *carrier) HeaderValues(name string) []string {
	return a.c.Request().Header.Values(name)
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

func (a *carrier) Query() url.Values {
	a.queryOnce.Do(func() { a.query = a.c.Request().URL.Query() })
	return a.query
}

func (a *carrier) ContentType() string {
	ct := a.c.Request().Header.Get("Content-Type")
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
		r := a.c.Request()
		if r.Body == nil {
			return
		}
		if a.maxBody > 0 {
			r.Body = http.MaxBytesReader(a.c.Response(), r.Body, a.maxBody)
		}
		a.body, a.bodyErr = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(a.body))
	})
	return a.body, a.bodyErr
}

func (a *carrier) MultipartForm() (*multipart.Form, error) {
	r := a.c.Request()
	// Bound the whole upload (not just the in-memory part) so large multipart
	// bodies cannot exhaust disk via temp files.
	if a.maxBody > 0 {
		r.Body = http.MaxBytesReader(a.c.Response(), r.Body, a.maxBody)
	}
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		return nil, err
	}
	return r.MultipartForm, nil
}

// cleanup removes any temp files net/http spilled to disk while parsing a
// multipart form. net/http never does this for you, so without it large uploads
// leak files into the temp dir for the lifetime of the process.
func (a *carrier) cleanup() {
	if r := a.c.Request(); r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

func (a *carrier) SetHeader(key, value string) { a.c.Response().Header().Set(key, value) }

func (a *carrier) WriteJSON(status int, body any) error {
	// Marshal first so an encode failure is caught before the status/body are
	// committed, and so the bytes match the other adapters exactly (encoding/json
	// with no trailing newline, unlike json.Encoder).
	raw, err := json.Marshal(body)
	w := a.c.Response()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"failed to encode response"}}`))
		return err
	}
	w.WriteHeader(status)
	_, werr := w.Write(raw)
	return werr
}

func (a *carrier) WriteBytes(status int, contentType string, data []byte) error {
	w := a.c.Response()
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, err := w.Write(data)
	return err
}

func (a *carrier) WriteEmpty(status int) error {
	a.c.Response().WriteHeader(status)
	return nil
}

func (a *carrier) Context() context.Context { return a.c.Request().Context() }
func (a *carrier) SetContext(ctx context.Context) {
	a.c.SetRequest(a.c.Request().WithContext(ctx))
}

func (a *carrier) Abort()                { a.aborted = true }
func (a *carrier) RecordError(err error) { a.errs = append(a.errs, err) }

// Errors exposes recorded errors for Echo logging middleware.
func (a *carrier) Errors() []error { return a.errs }
