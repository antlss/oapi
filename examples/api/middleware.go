package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/antlss/oapi"
)

type userKey struct{}

// requireAuth is a typed middleware. It runs on the already-parsed request, so
// it can read the validated Authorization header (binding:"required") and inject
// the authenticated user into the context for the handler. Returning an error
// aborts the chain and renders it via the standard envelope — here an
// oapi.HTTPError carrying 401.
//
// Its request shape (only the header) is independent of the handler's: a typed
// middleware declares just the parts it needs and composes with any route.
func requireAuth(ctx context.Context, req oapi.Request[AuthHeader, struct{}, struct{}, struct{}]) (context.Context, error) {
	token := strings.TrimPrefix(req.Header.Token, "Bearer ")
	if token == "" || token == req.Header.Token { // empty, or no "Bearer " prefix
		return ctx, oapi.NewError(http.StatusUnauthorized, "unauthorized", "a valid Bearer token is required")
	}
	user := User{ID: "u_" + token, Roles: []string{"editor"}}
	return context.WithValue(ctx, userKey{}, user), nil
}

// userFrom retrieves the user injected by requireAuth.
func userFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}
