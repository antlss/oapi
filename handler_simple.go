package oapi

import "context"

// The generic Request[Header, Param, Query, Body] is fully general, but many
// endpoints bind only one part. The constructors below take a handler over just
// that part, so the common cases read without three struct{} placeholders. Each is
// a thin wrapper over [NewRoute] and composes with every RouteOption (typed
// middleware, envelopes, doc metadata, ...) exactly as NewRoute does.

// NewBodyRoute builds an endpoint whose handler receives only the decoded request
// Body — the common shape for a create/update endpoint with no header, path or
// query binding. Equivalent to NewRoute with Request[struct{}, struct{}, struct{},
// Body].
func NewBodyRoute[Body, Response any](
	method, path string,
	handler func(ctx context.Context, body Body) (*Response, error),
	opts ...RouteOption,
) Route {
	return NewRoute(method, path,
		func(ctx context.Context, req Request[struct{}, struct{}, struct{}, Body]) (*Response, error) {
			return handler(ctx, req.Body)
		},
		opts...,
	)
}

// NewQueryRoute builds an endpoint whose handler receives only the decoded Query
// struct — the common shape for a list/search GET. Equivalent to NewRoute with
// Request[struct{}, struct{}, Query, struct{}].
func NewQueryRoute[Query, Response any](
	method, path string,
	handler func(ctx context.Context, query Query) (*Response, error),
	opts ...RouteOption,
) Route {
	return NewRoute(method, path,
		func(ctx context.Context, req Request[struct{}, struct{}, Query, struct{}]) (*Response, error) {
			return handler(ctx, req.Query)
		},
		opts...,
	)
}

// NewParamRoute builds an endpoint whose handler receives only the decoded path
// parameters (the Param struct) — the common shape for a GET/DELETE by id.
// Equivalent to NewRoute with Request[struct{}, Param, struct{}, struct{}].
func NewParamRoute[Param, Response any](
	method, path string,
	handler func(ctx context.Context, param Param) (*Response, error),
	opts ...RouteOption,
) Route {
	return NewRoute(method, path,
		func(ctx context.Context, req Request[struct{}, Param, struct{}, struct{}]) (*Response, error) {
			return handler(ctx, req.Param)
		},
		opts...,
	)
}
