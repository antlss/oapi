package oapi

import "reflect"

// Request is the typed, framework-agnostic representation of an inbound HTTP
// request. Each part is bound from a different source:
//
//	Header -> `header:"..."` tags
//	Param  -> `uri:"..."`    tags (path params)
//	Query  -> `form:"..."`   tags (query string)
//	Body   -> `json:"..."` (JSON) or `form:"..."` (multipart / urlencoded) tags
//
// Use struct{} for any part the endpoint does not consume.
type Request[Header, Param, Query, Body any] struct {
	Header Header
	Param  Param
	Query  Query
	Body   Body
}

// execution carries per-request state shared by the whole typed chain (the typed
// middlewares and the handler): the Carrier plus a parse-once cache keyed by the
// concrete Request type. Keying by reflect.Type (not its String()) avoids the
// cross-package name collision the previous string key was prone to.
//
// The cache stores its first Request shape inline (key0/val0) and allocates a map
// only when a second, differently-shaped part is cached — e.g. a typed middleware
// that binds only a header while the handler binds the full request. Most routes
// bind a single shape, so the hot path allocates no cache map.
type execution struct {
	Carrier
	// cfg is the route's App configuration (nil = read the process-wide globals),
	// carried here so the binding/validation path can reach it without a global.
	cfg *appConfig

	key0     reflect.Type
	val0     any
	overflow map[reflect.Type]any
}

func newExecution(c Carrier, cfg *appConfig) *execution {
	return &execution{Carrier: c, cfg: cfg} //nolint:exhaustruct
}

// cacheGet returns the parsed request stored under key, if any.
func (ex *execution) cacheGet(key reflect.Type) (any, bool) {
	if ex.key0 == key {
		return ex.val0, true
	}
	if ex.overflow != nil {
		v, ok := ex.overflow[key]
		return v, ok
	}
	return nil, false
}

// cacheSet stores a parsed request under key, keeping the first shape inline and
// allocating the overflow map only for additional shapes.
func (ex *execution) cacheSet(key reflect.Type, val any) {
	switch {
	case ex.key0 == nil:
		ex.key0, ex.val0 = key, val
	case ex.key0 == key:
		ex.val0 = val
	default:
		if ex.overflow == nil {
			ex.overflow = map[reflect.Type]any{}
		}
		ex.overflow[key] = val
	}
}

// cachedRequest parses the request at most once per execution, so a typed
// middleware and the handler that declare the same Request shape share a single
// parse. A different shape simply gets its own cache entry; the raw body bytes
// are cached on the Carrier so re-binding stays cheap.
func cachedRequest[Header, Param, Query, Body any](ex *execution) (Request[Header, Param, Query, Body], error) {
	key := reflect.TypeFor[Request[Header, Param, Query, Body]]()
	if cached, ok := ex.cacheGet(key); ok {
		if req, ok := cached.(Request[Header, Param, Query, Body]); ok {
			return req, nil
		}
	}

	req, err := parseRequest[Header, Param, Query, Body](ex.cfg, ex.Carrier)
	if err != nil {
		return req, err
	}

	ex.cacheSet(key, req)
	return req, nil
}

// shouldBind reports whether Value is a real (non-placeholder) type, i.e. it
// differs from the Marker sentinel (struct{}). It is the gate that lets each
// request part be optional without a separate "present" flag.
func shouldBind[Value, Marker any]() bool {
	return reflect.TypeFor[Value]() != reflect.TypeFor[Marker]()
}

// typeOrNil returns the reflect.Type of Value, or nil when Value is the Marker
// sentinel. The nil is what RouteSchema uses to mean "this part is absent".
func typeOrNil[Value, Marker any]() reflect.Type {
	if !shouldBind[Value, Marker]() {
		return nil
	}

	return reflect.TypeFor[Value]()
}
