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
// over the default HTTPError / aerror-compatible resolution.
func WithErrorMapper(mapper ErrorMapper) RouteOption {
	return func(route *Route) { route.errorMapper = mapper }
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
	ex := newExecution(c)
	for _, mw := range route.typedBefore {
		if err := mw(ex); err != nil {
			renderError(ex, err, route.errorMapper)
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

// --- shared render helpers (used by handler closures) -----------------------

func writeSuccess[Response any](c Carrier, res *Response, successStatus int, mapper ErrorMapper) {
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

	_ = NewResult(res).WithStatus(status).withErrorMapper(mapper).render(c)
}

// renderError renders a business error. Status resolution honours an optional
// custom mapper, then HTTPError, then aerror-compatible duck typing, else 500.
func renderError(c Carrier, err error, mapper ErrorMapper) {
	c.Abort()
	_ = NewResult(nil).withErrorMapper(mapper).WithError(err).render(c)
}
