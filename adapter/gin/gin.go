// Package gin adapts the framework-agnostic oapi core onto the gin web
// framework. Register oapi routes with Register / RegisterAll and serve the
// generated OpenAPI document with SpecHandler.
package gin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antlss/oapi"
)

// jsonContentType matches net/http's WriteJSON content type byte-for-byte so the
// success path stays identical across adapters.
const jsonContentType = "application/json; charset=utf-8"

// DefaultMaxRequestBytes caps how many bytes the adapter reads from a request
// body (JSON, urlencoded and multipart), guarding against memory/disk
// exhaustion from oversized uploads. Set it to 0 to disable the cap (e.g. when
// you enforce limits with your own middleware). Override before registering
// routes.
var DefaultMaxRequestBytes int64 = 10 << 20 // 10 MiB

// Register mounts a single oapi.Route on a gin router. Optional native gin
// middlewares run before the route handler; an aborting middleware skips it.
func Register(router gin.IRoutes, route oapi.Route, native ...gin.HandlerFunc) {
	handlers := make([]gin.HandlerFunc, 0, len(native)+1)
	handlers = append(handlers, native...)
	handlers = append(handlers, handlerFor(route))
	router.Handle(route.Method(), toGinPath(route.Path()), handlers...)
}

// RegisterAll mounts every route on the router.
func RegisterAll(router gin.IRoutes, routes ...oapi.Route) {
	for _, route := range routes {
		Register(router, route)
	}
}

// SpecHandler serves a registry's OpenAPI document as JSON. The document is
// built once on first request.
func SpecHandler(reg *oapi.Registry) gin.HandlerFunc {
	spec := reg.SpecBytesOnce()
	return func(c *gin.Context) {
		raw, err := spec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to render openapi spec"})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}

func handlerFor(route oapi.Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		cr := &carrier{c: c, maxBody: route.MaxRequestBytesOr(DefaultMaxRequestBytes)} //nolint:exhaustruct
		defer cr.cleanup()
		route.Invoke(cr)
	}
}

// toGinPath is the identity: the canonical route syntax (:id, *path) is gin's.
func toGinPath(path string) string { return path }

// carrier adapts *gin.Context to oapi.Carrier.
type carrier struct {
	c       *gin.Context
	maxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error
}

func (a *carrier) Method() string            { return a.c.Request.Method }
func (a *carrier) Header(name string) string { return a.c.GetHeader(name) }
func (a *carrier) HeaderValues(name string) []string {
	return a.c.Request.Header.Values(name)
}
func (a *carrier) Param(name string) string {
	// gin captures a catch-all (*param) value WITH a leading slash (e.g. a
	// `/assets/*path` request for /assets/img/logo.png yields "/img/logo.png"),
	// while the net/http and fiber adapters — and the generated OpenAPI {param}
	// docs — use no leading slash ("img/logo.png"). A single-segment :param can
	// never contain a slash, so trimming one leading "/" only ever normalises a
	// catch-all, making `*path` bind identically on every adapter.
	return strings.TrimPrefix(a.c.Param(name), "/")
}
func (a *carrier) Query() url.Values {
	// Memoize: url.Query() re-parses the raw query string on each call, so without
	// this a typed middleware and the handler binding different request shapes would
	// each re-parse it. Matches the queryOnce caching in the other adapters.
	a.queryOnce.Do(func() { a.query = a.c.Request.URL.Query() })
	return a.query
}
func (a *carrier) ContentType() string { return a.c.ContentType() }

func (a *carrier) Body() ([]byte, error) {
	a.bodyOnce.Do(func() {
		if a.c.Request.Body == nil {
			return
		}
		if a.maxBody > 0 {
			a.c.Request.Body = http.MaxBytesReader(a.c.Writer, a.c.Request.Body, a.maxBody)
		}
		a.body, a.bodyErr = io.ReadAll(a.c.Request.Body)
		a.c.Request.Body = io.NopCloser(bytes.NewReader(a.body))
	})
	return a.body, a.bodyErr
}

func (a *carrier) MultipartForm() (*multipart.Form, error) {
	// Bound the whole upload (not just the in-memory part) so large multipart
	// bodies cannot exhaust disk via temp files.
	if a.maxBody > 0 {
		a.c.Request.Body = http.MaxBytesReader(a.c.Writer, a.c.Request.Body, a.maxBody)
	}
	return a.c.MultipartForm()
}

// cleanup removes any temp files spilled to disk while parsing a multipart form;
// neither net/http nor gin do this for you, so without it large uploads leak
// files into the temp dir for the lifetime of the process.
func (a *carrier) cleanup() {
	if a.c.Request.MultipartForm != nil {
		_ = a.c.Request.MultipartForm.RemoveAll()
	}
}

func (a *carrier) SetHeader(key, value string) { a.c.Header(key, value) }

func (a *carrier) WriteJSON(status int, body any) error {
	// Marshal first (rather than c.JSON, which writes a 200 then fails mid-stream
	// on an unmarshalable body) so an encode failure is caught before the
	// status/body are committed and surfaces as the same sanitized 500 the
	// net/http and fiber adapters emit. The bytes also match net/http exactly
	// (encoding/json, no trailing newline, "application/json; charset=utf-8").
	raw, err := json.Marshal(body)
	if err != nil {
		a.c.Data(http.StatusInternalServerError, jsonContentType,
			[]byte(`{"error":{"message":"failed to encode response"}}`))
		return err
	}
	a.c.Data(status, jsonContentType, raw)
	return nil
}

func (a *carrier) WriteBytes(status int, contentType string, data []byte) error {
	a.c.Data(status, contentType, data)
	return nil
}

func (a *carrier) WriteEmpty(status int) error {
	a.c.Status(status)
	a.c.Writer.WriteHeaderNow()
	return nil
}

func (a *carrier) Context() context.Context { return a.c.Request.Context() }
func (a *carrier) SetContext(ctx context.Context) {
	a.c.Request = a.c.Request.WithContext(ctx)
}

func (a *carrier) Abort()                { a.c.Abort() }
func (a *carrier) RecordError(err error) { _ = a.c.Error(err) }
