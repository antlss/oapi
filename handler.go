package oapi

import (
	"context"
	"net/http"
)

// RequestHandler is the common handler shape: pure business logic over a typed
// request, returning a typed response pointer. nil response => 204 No Content.
type RequestHandler[Header, Param, Query, Body, Response any] func(
	ctx context.Context,
	req Request[Header, Param, Query, Body],
) (*Response, error)

// RichHandler is the escape hatch for endpoints that need full control over the
// response envelope (paging, meta, file download, custom status). It returns a
// fully built *Result. nil => 204 No Content.
type RichHandler[Header, Param, Query, Body any] func(
	ctx context.Context,
	req Request[Header, Param, Query, Body],
) (*Result, error)

// NewRoute builds an endpoint from a typed RequestHandler. The request is parsed
// exactly once (shared with any typed middleware), the response is wrapped by the
// route's configured envelope (the default [DataEnvelope] gives {"data": ...}) and
// errors are rendered via Result.
func NewRoute[Header, Param, Query, Body, Response any](
	method string,
	path string,
	handler RequestHandler[Header, Param, Query, Body, Response],
	opts ...RouteOption,
) Route {
	route := newBaseRoute[Header, Param, Query, Body, Response](method, path, opts)

	successStatus := route.successStatus
	mapper := route.errorMapper
	env := route.envelope
	route.invoke = func(ex *execution) {
		req, err := cachedRequest[Header, Param, Query, Body](ex)
		if err != nil {
			renderError(ex, err, mapper)
			return
		}

		res, err := handler(ex.Context(), req)
		if err != nil {
			renderError(ex, err, mapper)
			return
		}

		writeSuccess(ex, res, successStatus, mapper, env)
	}

	return route
}

// NewRichRoute builds an endpoint from a RichHandler that returns a fully formed
// *Result (paging, file, custom status, ...). Use WithResponseType to keep the
// generated docs accurate.
func NewRichRoute[Header, Param, Query, Body any](
	method string,
	path string,
	handler RichHandler[Header, Param, Query, Body],
	opts ...RouteOption,
) Route {
	route := newBaseRoute[Header, Param, Query, Body, struct{}](method, path, opts)

	mapper := route.errorMapper
	env := route.envelope
	route.invoke = func(ex *execution) {
		req, err := cachedRequest[Header, Param, Query, Body](ex)
		if err != nil {
			renderError(ex, err, mapper)
			return
		}

		res, err := handler(ex.Context(), req)
		if err != nil {
			renderError(ex, err, mapper)
			return
		}

		if res == nil {
			_ = ex.WriteEmpty(http.StatusNoContent)
			return
		}

		res.withErrorMapper(mapper)
		// Inject the route's envelope only when the handler's constructor did not
		// pin one (NewResult pins RawEnvelope; NewDataResult leaves it to inherit).
		if res.envelope == nil {
			res.withEnvelope(resolveEnvelope(env))
		}
		_ = res.render(ex)
	}

	return route
}
