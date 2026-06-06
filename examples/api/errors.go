package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// The demo wires up every error mechanism the library understands, each to a
// different endpoint so the live responses illustrate them:
//
//  1. demoError      — implements oapi.HTTPError + oapi.ErrorBody (custom body)
//  2. apiError       — "aerror-shaped" duck typing (HTTPStatusCode + ToJSON)
//  3. sentinel + ErrorMapper — plain domain errors mapped to HTTP responses
//  4. validation     — produced automatically by the validator (see types.go)

// 1) demoError controls BOTH its status and the exact JSON under the "error" key.
type demoError struct {
	status  int
	code    string
	message string
}

func (e demoError) Error() string   { return e.message }
func (e demoError) HTTPStatus() int { return e.status }
func (e demoError) ErrorBody() any {
	return map[string]string{"code": e.code, "message": e.message}
}

// 2) apiError exposes HTTPStatusCode() + ToJSON(), the two methods many error
// libraries provide. The core recognises this shape structurally — no glue, no
// import — and renders the ToJSON() payload with that status.
type apiError struct {
	status  int
	payload json.RawMessage
}

func (e apiError) Error() string           { return string(e.payload) }
func (e apiError) HTTPStatusCode() int     { return e.status }
func (e apiError) ToJSON() json.RawMessage { return e.payload }

func notFoundAPIError(id int) apiError {
	return apiError{
		status:  http.StatusNotFound,
		payload: json.RawMessage(fmt.Sprintf(`{"code":"not_found","message":"product %d does not exist"}`, id)),
	}
}

// 3) Sentinel domain errors translated by productErrorMapper. A handler can
// return one of these as a plain error and have it rendered with the right
// status — without knowing anything about HTTP.
var (
	errProductConflict = errors.New("product version conflict")
	errProductGone     = errors.New("product permanently deleted")
)

// productErrorMapper maps the sentinel errors above to HTTP responses. Returning
// ok=false defers to the default handling (HTTPError → aerror-shaped → 500).
func productErrorMapper(err error) (int, json.RawMessage, bool) {
	switch {
	case errors.Is(err, errProductConflict):
		return http.StatusConflict,
			json.RawMessage(`{"code":"version_conflict","message":"the product was modified by someone else"}`), true
	case errors.Is(err, errProductGone):
		return http.StatusGone,
			json.RawMessage(`{"code":"gone","message":"the product was permanently deleted"}`), true
	}
	return 0, nil, false
}
