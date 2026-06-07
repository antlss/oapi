package oapi

import (
	"net/http"
	"reflect"
)

// Route is a fully described HTTP endpoint: method, path, the folded typed
// handler chain (typed middlewares -> handler), the success status and the
// reflection schema used for documentation. It is built once at startup via
// NewRoute / NewRichRoute and is immutable afterwards.
//
// A Route is framework-agnostic: it exposes Invoke(Carrier), and an adapter
// (adapter/gin, adapter/nethttp, adapter/fiber) turns it into a native handler.
type Route struct {
	method string
	path   string

	typedBefore   []erasedMiddleware
	invoke        func(*execution)
	successStatus int
	errorMapper   ErrorMapper
	// envelope shapes the success body (and its documented schema). nil means
	// "inherit the process-wide default" (see resolveEnvelope).
	envelope ResponseEnvelope
	// cfg is the App configuration this route reads its Validator/ErrorParser/
	// body-cap from. nil means "read the process-wide globals" — how a route built
	// without [WithApp] keeps the original behaviour. Set via [WithApp].
	cfg *appConfig

	// hasRules is precomputed at construction: true when any bound request part
	// carries a validation rule tag. The request path reads only this bool (never
	// the reflection schema) to drive the "no validator configured" warning.
	hasRules bool

	// doc holds everything consumed only by the OpenAPI generator: the reflection
	// schema (single source of truth for the spec) plus the human-authored
	// metadata. It is never read on the request path, so a served route carries
	// no documentation weight beyond this one pointer, and the Registry is its
	// only consumer. Always non-nil after construction via newBaseRoute.
	doc *routeDoc
}

// routeDoc groups the documentation-only data captured at NewRoute time.
// Isolating it behind a single pointer keeps the runtime Route lean and turns
// the runtime/doc boundary into a type boundary rather than a comment.
type routeDoc struct {
	schema      RouteSchema
	summary     string
	description string
	tags        []string
	deprecated  bool
	responses   []responseDoc
	security    []securityRequirement
	// binary, when set, documents the success response as a binary stream (a file
	// download) instead of a JSON body. A RichHandler's *Result body cannot be
	// inferred from generics, so without this a file route (Result.WithFile) would
	// default to 204 No Content in the docs while returning 200 with bytes at
	// runtime. Set via [WithBinaryResponse]. nil means "not a binary response".
	binary *binaryResponse
}

// binaryResponse documents a binary (file-download) success response: the media
// type written on the wire and a human description.
type binaryResponse struct {
	contentType string
	description string
}

// erasedMiddleware is a type-erased typed middleware: it parses (shared cache),
// runs the middleware and returns an error to abort the chain.
type erasedMiddleware func(*execution) error

// responseDoc documents an additional (usually non-success) response.
type responseDoc struct {
	status      int
	typ         reflect.Type
	description string
}

// securityRequirement names a security scheme (declared on the Registry) and
// the scopes it needs.
type securityRequirement struct {
	scheme string
	scopes []string
}

// RouteOption customises a Route at construction time. Options run before the
// handler closure is built, so the closure always observes the final values.
type RouteOption func(*Route)

// RouteSchema captures the concrete Go types of every part of the request and
// of the response. nil means "this part is absent". It is the single source of
// truth for OpenAPI generation so the docs can never drift from the handler.
type RouteSchema struct {
	Header   reflect.Type
	Param    reflect.Type
	Query    reflect.Type
	Body     reflect.Type
	Response reflect.Type
	Meta     reflect.Type
}

// WithSuccessStatus overrides the HTTP status returned on success (default 200,
// or 204 when there is no response body). Setting 204 forces an empty body.
func WithSuccessStatus(status int) RouteOption {
	return func(route *Route) { route.successStatus = status }
}

// WithSummary sets the OpenAPI operation summary.
func WithSummary(summary string) RouteOption {
	return func(route *Route) { route.doc.summary = summary }
}

// WithDescription sets the OpenAPI operation description.
func WithDescription(description string) RouteOption {
	return func(route *Route) { route.doc.description = description }
}

// WithTags sets the OpenAPI operation tags (used to group endpoints in docs).
func WithTags(tags ...string) RouteOption {
	return func(route *Route) { route.doc.tags = tags }
}

// WithDeprecated marks the operation as deprecated in the docs.
func WithDeprecated() RouteOption {
	return func(route *Route) { route.doc.deprecated = true }
}

// WithResponseType declares the response body type for documentation when it
// cannot be inferred from generics (i.e. for NewRichRoute handlers).
func WithResponseType[T any]() RouteOption {
	return func(route *Route) { route.doc.schema.Response = typeOrNil[T, struct{}]() }
}

// WithMetaType documents the type placed under the response envelope's "meta"
// key (e.g. [PagingMeta] for paged endpoints), so a success response that
// carries meta is fully described. Documentation only — the handler still
// attaches the meta at runtime via Result.WithMeta / WithPaging.
func WithMetaType[T any]() RouteOption {
	return func(route *Route) { route.doc.schema.Meta = typeOrNil[T, struct{}]() }
}

// WithResponse documents an additional response (typically an error or an
// alternative success status), e.g. 201, 404, 409, 422. T is the body type under
// the standard envelope; use struct{} for a body-less response. This is
// documentation only — the handler still decides what to return at runtime.
func WithResponse[T any](status int, description string) RouteOption {
	return func(route *Route) {
		route.doc.responses = append(route.doc.responses, responseDoc{
			status:      status,
			typ:         typeOrNil[T, struct{}](),
			description: description,
		})
	}
}

// WithSecurity declares that the operation requires the named security scheme
// (registered on the Registry via AddSecurityScheme) with the given scopes.
// This documents the requirement; enforcement remains the middleware's job.
func WithSecurity(scheme string, scopes ...string) RouteOption {
	return func(route *Route) {
		route.doc.security = append(route.doc.security, securityRequirement{scheme: scheme, scopes: scopes})
	}
}

// WithErrorMapper sets a custom error mapper for this route, taking precedence
// over the global ErrorParser and the default HTTPError / aerror resolution.
func WithErrorMapper(mapper ErrorMapper) RouteOption {
	return func(route *Route) { route.errorMapper = mapper }
}

// WithEnvelope overrides the success-response envelope for this route, taking
// precedence over the process-wide [SetResponseEnvelope] default. It drives both
// the wire body and the documented schema, so they stay in lockstep.
func WithEnvelope(e ResponseEnvelope) RouteOption {
	return func(route *Route) { route.envelope = e }
}

// WithRawResponse renders this route's success body as the raw handler model with
// no envelope (and documents it the same way). Shorthand for
// WithEnvelope(RawEnvelope).
func WithRawResponse() RouteOption {
	return func(route *Route) { route.envelope = RawEnvelope }
}

// WithBinaryResponse documents the success response as a binary stream — a file
// download — with the given media type (empty defaults to
// "application/octet-stream") and description. Use it for file-download
// RichHandlers (those returning [Result.WithFile]), whose body cannot be inferred
// from generics: without it the docs would default to 204 No Content while the
// handler returns 200 with bytes. It sets the documented success status to 200
// unless an explicit [WithSuccessStatus] overrides it. Documentation only — the
// handler still streams the bytes at runtime.
func WithBinaryResponse(contentType, description string) RouteOption {
	return func(route *Route) {
		route.doc.binary = &binaryResponse{contentType: contentType, description: description}
	}
}

func newBaseRoute[Header, Param, Query, Body, Response any](
	method, path string,
	opts []RouteOption,
) Route {
	route := Route{ //nolint:exhaustruct
		method: method,
		path:   path,
		doc: &routeDoc{ //nolint:exhaustruct
			schema: RouteSchema{
				Header:   typeOrNil[Header, struct{}](),
				Param:    typeOrNil[Param, struct{}](),
				Query:    typeOrNil[Query, struct{}](),
				Body:     typeOrNil[Body, struct{}](),
				Response: typeOrNil[Response, struct{}](),
			},
		},
	}

	// Options run before the handler closure is built so the closure observes
	// the final values (no closure-over-mutated-field footgun).
	for _, opt := range opts {
		opt(&route)
	}

	// Precompute once whether any bound part declares validation rules, so the
	// per-request warning path never has to touch the reflection schema.
	route.hasRules = routeHasBindingRules(route)

	return route
}

// Invoke runs the full typed chain (typed middlewares -> handler -> render) for
// one request, through the given carrier. Adapters call this from their native
// handler.
func (route Route) Invoke(c Carrier) {
	warnIfNoValidator(route)
	ex := newExecution(c, route.cfg)
	for _, mw := range route.typedBefore {
		if err := mw(ex); err != nil {
			renderError(ex, err, route.errorMapper, route.cfg.errorParserOrGlobal())
			return
		}
	}
	route.invoke(ex)
}

// Method, Path, Schema and the documentation getters expose route metadata for
// the OpenAPI generator and for adapter integration.
func (route Route) Method() string { return route.method }
func (route Route) Path() string   { return route.path }
func (route Route) Schema() RouteSchema {
	if route.doc == nil {
		return RouteSchema{} //nolint:exhaustruct
	}
	return route.doc.schema
}

func (route Route) Summary() string {
	if route.doc == nil {
		return ""
	}
	return route.doc.summary
}

func (route Route) Description() string {
	if route.doc == nil {
		return ""
	}
	return route.doc.description
}

func (route Route) Tags() []string {
	if route.doc == nil {
		return nil
	}
	return route.doc.tags
}

func (route Route) SuccessStatus() int { return route.successStatus }

// MaxRequestBytes reports the per-route request body cap configured via an App
// ([WithApp] + [WithMaxRequestBytes]). ok is false when the route inherits no
// App-level cap, in which case an adapter falls back to its own
// DefaultMaxRequestBytes. A returned value of 0 means "no cap".
func (route Route) MaxRequestBytes() (limit int64, ok bool) {
	if route.cfg == nil || !route.cfg.hasMaxBody {
		return 0, false
	}
	return route.cfg.maxBodyBytes, true
}

// MaxRequestBytesOr returns the route's App-configured request body cap, or
// fallback when the route inherits no App-level cap. A configured cap of 0 ("no
// cap") is returned as 0. It is the one-liner every adapter needs to resolve the
// effective cap from its own package-level DefaultMaxRequestBytes:
//
//	cap := route.MaxRequestBytesOr(DefaultMaxRequestBytes)
func (route Route) MaxRequestBytesOr(fallback int64) int64 {
	if limit, ok := route.MaxRequestBytes(); ok {
		return limit
	}
	return fallback
}

// --- shared render helpers (used by handler closures) -----------------------

func writeSuccess[Response any](c Carrier, res *Response, successStatus int, mapper ErrorMapper, env ResponseEnvelope) {
	// No response body type declared, an explicit 204, or a nil pointer was
	// returned: write no body, but honour a custom success status (e.g. a 201
	// Created with an empty body) so the status on the wire matches what
	// responsesFor documented; fall back to 204 only when none was set.
	if !shouldBind[Response, struct{}]() || successStatus == http.StatusNoContent || res == nil {
		status := successStatus
		if status == 0 {
			status = http.StatusNoContent
		}
		_ = c.WriteEmpty(status)
		return
	}

	status := successStatus
	if status == 0 {
		status = http.StatusOK
	}

	// Typed routes wrap their payload with the route's configured envelope (the
	// default DataEnvelope yields {"data": ...}); NewDataResult inherits it.
	_ = NewDataResult(res).
		WithStatus(status).
		withErrorMapper(mapper).
		withEnvelope(resolveEnvelope(env)).
		render(c)
}

// renderError renders a business error. Status resolution honours an optional
// custom mapper, then the configured ErrorParser, then HTTPError, then
// aerror-compatible duck typing, else 500. parser is the route's App-scoped parser
// (or the process-wide one for a route without an App).
func renderError(c Carrier, err error, mapper ErrorMapper, parser ErrorParser) {
	c.Abort()
	_ = NewResult(nil).withErrorMapper(mapper).withErrorParser(parser).WithError(err).render(c)
}
