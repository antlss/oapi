package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/antlss/oapi"
)

// The demo wires up every error mechanism the library understands, each to a
// different endpoint so the live responses illustrate them:
//
//  1. demoError      — implements oapi.HTTPError + oapi.ErrorBody (custom body)
//  2. apiError       — "aerror-shaped" duck typing (HTTPStatusCode + ToJSON)
//  3. sentinel + ErrorMapper — plain domain errors mapped to HTTP responses (per route)
//  4. validation     — produced automatically by the validator (see types.go)
//  5. AppErrorParser — a process-wide oapi.ErrorParser giving the WHOLE API one
//     custom error shape (see cmd/customized); composes under the per-route mapper.

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

// 3) Sentinel domain error translated by productErrorMapper. A handler can
// return it as a plain error and have it rendered with the right status —
// without knowing anything about HTTP.
var errProductConflict = errors.New("product version conflict")

// productErrorMapper maps the sentinel error above to an HTTP response. A claiming
// mapper now owns the FULL wire body, so it returns the complete {"error":{...}}
// envelope itself (the library no longer wraps it). Returning ok=false defers to
// the global ErrorParser, then the default handling (HTTPError → aerror → 500).
func productErrorMapper(err error) (int, any, bool) {
	if errors.Is(err, errProductConflict) {
		return http.StatusConflict,
			json.RawMessage(`{"error":{"code":"version_conflict","message":"the product was modified by someone else"}}`), true
	}
	return 0, nil, false
}

// 5) AppError is this project's uniform error envelope — a DIFFERENT shape from
// the library's built-in {"error":{code,message,fields}}. Installing AppErrorParser
// process-wide (see cmd/customized) makes every error render in this shape, and its
// ErrorType keeps the generated docs in sync.
type AppError struct {
	Success bool           `json:"success" example:"false"`
	Error   AppErrorDetail `json:"error"`
}

// AppErrorDetail is the inner error object of [AppError].
type AppErrorDetail struct {
	Code    string            `json:"code"             example:"bad_request"`
	Message string            `json:"message"          example:"request validation failed"`
	Fields  []oapi.FieldError `json:"fields,omitempty"`
}

// AppErrorParser renders EVERY error as an [AppError], so the whole API speaks one
// error shape. It recognises the library's own oapi.Error (forwarding field-level
// validation details), the demo's aerror-shaped apiError, and any oapi.HTTPError;
// anything unrecognised becomes a non-leaking 500. ErrorType() lets the OpenAPI
// generator document the shape it produces.
type AppErrorParser struct{}

// compile-time assurance the parser satisfies the core seam.
var _ oapi.ErrorParser = AppErrorParser{}

func (AppErrorParser) Resolve(err error) (int, any, bool) {
	var oe *oapi.Error
	if errors.As(err, &oe) {
		return oe.Status, AppError{Error: AppErrorDetail{Code: oe.Code, Message: oe.Message, Fields: oe.Fields}}, true
	}
	var ae apiError
	if errors.As(err, &ae) {
		var d AppErrorDetail
		_ = json.Unmarshal(ae.payload, &d)
		return ae.status, AppError{Error: d}, true
	}
	var he oapi.HTTPError
	if errors.As(err, &he) {
		return he.HTTPStatus(), AppError{Error: AppErrorDetail{Code: "error", Message: he.Error()}}, true
	}
	return http.StatusInternalServerError,
		AppError{Error: AppErrorDetail{Code: "internal_error", Message: "internal server error"}}, true
}

func (AppErrorParser) ErrorType() reflect.Type { return reflect.TypeFor[AppError]() }
