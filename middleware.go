package oapi

import "context"

// Middleware is a typed, framework-agnostic middleware. Like a RequestHandler it
// operates on the already-parsed Request instead of a raw framework context, so
// it is easy to unit test and impossible to misuse the binding.
//
// It may return a derived context.Context (e.g. carrying an authenticated user
// or resolved tenant) which is propagated to every downstream middleware and to
// the handler. Return the incoming ctx unchanged when there is nothing to add.
// A non-nil error aborts the chain and renders the error via the Result
// envelope, exactly like a handler error.
type Middleware[Header, Param, Query, Body any] func(
	ctx context.Context,
	req Request[Header, Param, Query, Body],
) (context.Context, error)

// WithTypedBefore registers one or more typed middlewares to run before the
// handler. They reuse the same parse-once cache as the handler, so declaring
// the request shape here does not cause the request to be parsed twice.
//
// The request shape is independent of the handler's: a middleware may declare
// only the parts it needs (e.g. Request[AuthHeader, struct{}, struct{},
// struct{}]) and still compose with any handler on the route.
func WithTypedBefore[Header, Param, Query, Body any](
	middlewares ...Middleware[Header, Param, Query, Body],
) RouteOption {
	return func(route *Route) {
		for _, mw := range middlewares {
			route.typedBefore = append(route.typedBefore, erase(mw))
		}
	}
}

// erase converts a typed middleware into the type-erased form stored on a Route.
func erase[Header, Param, Query, Body any](
	mw Middleware[Header, Param, Query, Body],
) erasedMiddleware {
	return func(ex *execution) error {
		req, err := cachedRequest[Header, Param, Query, Body](ex)
		if err != nil {
			return err
		}

		newCtx, err := mw(ex.Context(), req)
		if err != nil {
			return err
		}

		if newCtx != nil {
			ex.SetContext(newCtx)
		}
		return nil
	}
}
