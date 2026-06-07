// Package fiber adapts the framework-agnostic oapi core onto the Fiber v2 web
// framework (fasthttp). Register oapi routes with Register / RegisterAll and
// serve the OpenAPI document with SpecHandler.
//
// Request body size is bounded by Fiber itself via fiber.Config.BodyLimit
// (default 4 MiB), which rejects oversized bodies before the handler runs.
// In addition, this adapter honours an App-configured per-route body cap
// (route.MaxRequestBytes) and the package-level DefaultMaxRequestBytes so the
// three adapters agree on the cap; see maxBodyFor and the carrier's read path.
// fasthttp also cleans up multipart temp files automatically at the end of each
// request.
package fiber

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"

	"github.com/antlss/oapi"
)

// jsonContentType matches net/http's WriteJSON content type byte-for-byte so the
// success path stays identical across adapters.
const jsonContentType = "application/json; charset=utf-8"

// DefaultMaxRequestBytes caps how many bytes the adapter reads from a request
// body (JSON, urlencoded and multipart) in addition to Fiber's own
// fiber.Config.BodyLimit, guarding against memory/disk exhaustion. Set it to 0
// to disable the extra cap (relying solely on BodyLimit). Override before
// registering routes. A per-route App cap (route.MaxRequestBytes) takes
// precedence over this fallback.
var DefaultMaxRequestBytes int64 = 10 << 20 // 10 MiB

// errBodyTooLarge is returned from the body/multipart read path when the request
// exceeds the resolved cap. The core's binder turns any Body/MultipartForm error
// into a 400 "failed to read request body" (the same status net/http's
// MaxBytesReader error produces there), so the three adapters agree on the
// outcome. It is reported to the core, never to fiber's DefaultErrorHandler.
var errBodyTooLarge = errors.New("request body too large")

// recordedErrorKey is the c.Locals key under which RecordError accumulates
// non-fatal errors, giving fiber logging middleware a place to read them (fiber
// has no native c.Errors slice like gin). Exported as a typed key would widen
// the API surface; a package-private string keeps it adapter-internal yet
// inspectable via c.Locals(fiber-internal lookups in this package).
const recordedErrorKey = "oapi.recordedErrors"

// RecordedErrors returns the non-fatal errors stashed by RecordError on this
// fiber context, in order, for logging middleware. It is the accessor the old
// dead errs slice lacked.
func RecordedErrors(c *fiber.Ctx) []error {
	errs, _ := c.Locals(recordedErrorKey).([]error)
	return errs
}

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
		// Seed the user context from the fasthttp request context so Context()
		// (which returns c.UserContext()) carries the request's cancellation and
		// deadline instead of fiber's default context.Background(). A later
		// SetContext (e.g. from typed middleware) still overrides this, and value
		// lookups fall through to the request ctx's user values. *fasthttp.RequestCtx
		// satisfies context.Context (Deadline/Done/Err/Value); in fasthttp v1.51
		// Done() tracks server shutdown (non-nil only under a listening server),
		// so this is the most cancellation the framework can offer today.
		c.SetUserContext(c.Context())

		cr := &carrier{c: c, maxBody: maxBodyFor(route)} //nolint:exhaustruct
		route.Invoke(cr)
		return cr.writeErr
	}
}

// maxBodyFor resolves the body cap for a route: an App-configured per-route cap
// (route.MaxRequestBytes, where 0 means "no cap") takes precedence over the
// package-level DefaultMaxRequestBytes fallback. Kept identical across adapters.
func maxBodyFor(route oapi.Route) int64 {
	if limit, ok := route.MaxRequestBytes(); ok {
		return limit
	}
	return DefaultMaxRequestBytes
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
	c       *fiber.Ctx
	maxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	writeErr error
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
		body := a.c.Body()
		// Honour the resolved per-adapter cap on top of fiber's BodyLimit so the
		// three adapters agree. fasthttp has no MaxBytesReader, but the body is
		// already fully buffered here, so a length check is equivalent: reject
		// (matching net/http's MaxBytesReader behaviour) rather than truncate.
		if a.maxBody > 0 && int64(len(body)) > a.maxBody {
			a.bodyErr = errBodyTooLarge
			return
		}
		a.body = append([]byte(nil), body...)
	})
	return a.body, a.bodyErr
}

func (a *carrier) MultipartForm() (*multipart.Form, error) {
	// Bound the upload by the ACTUAL buffered body size, not the declared
	// Content-Length. fasthttp has already read the whole request into memory
	// (bounded by fiber.Config.BodyLimit), so len(Body()) is the true size; the
	// declared length is unreliable — a chunked request reports ContentLength() ==
	// -1 (which is < any cap, silently bypassing it) and a header value can be
	// spoofed. len(Body()) closes both gaps and matches the Body() cap above.
	if a.maxBody > 0 && int64(len(a.c.Body())) > a.maxBody {
		return nil, errBodyTooLarge
	}
	return a.c.MultipartForm()
}

func (a *carrier) SetHeader(key, value string) { a.c.Set(key, value) }

func (a *carrier) WriteJSON(status int, body any) error {
	// Marshal first (rather than c.JSON) so an encode failure is caught before the
	// status/body are committed and surfaces as the same sanitized 500 the
	// net/http and gin adapters emit. The bytes also match net/http exactly
	// (encoding/json, no trailing newline, "application/json; charset=utf-8").
	raw, err := json.Marshal(body)
	a.c.Set("Content-Type", jsonContentType)
	if err != nil {
		// Write the sanitized body ourselves and DO NOT return the raw error:
		// returning it would hand the unmarshalable value's message to fiber's
		// DefaultErrorHandler, which could leak it. The Send error (if any) is
		// what we surface to the handler chain.
		a.writeErr = a.c.Status(http.StatusInternalServerError).
			Send([]byte(`{"error":{"message":"failed to encode response"}}`))
		return err
	}
	a.writeErr = a.c.Status(status).Send(raw)
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

// Context returns the per-request context. handlerFor seeds the user context
// from the fasthttp request context, so this carries the request's cancellation
// and deadline (rather than fiber's default context.Background()); a SetContext
// from middleware overrides it while still chaining value lookups.
func (a *carrier) Context() context.Context       { return a.c.UserContext() }
func (a *carrier) SetContext(ctx context.Context) { a.c.SetUserContext(ctx) }

// Abort is a no-op: fiber has no adapter-side after-middleware for this carrier
// to skip (native handlers registered before the route run to completion before
// it). The core calls it when rendering an error; gin uses it for real.
func (a *carrier) Abort() {}

// RecordError accumulates a non-fatal error on the fiber context (read back via
// RecordedErrors) for logging middleware; fiber has no native c.Errors slice
// like gin, so we stash them under a well-known Locals key. Best-effort, per the
// Carrier contract.
func (a *carrier) RecordError(err error) {
	prev, _ := a.c.Locals(recordedErrorKey).([]error)
	a.c.Locals(recordedErrorKey, append(prev, err))
}
