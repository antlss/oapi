package oapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

// HTTPError is the contract an error can satisfy to control the HTTP response it
// produces. Any error returned by a handler or middleware that implements it is
// rendered with the given status; everything else falls back to 500. This is the
// only error abstraction the library depends on, which keeps the package free of
// any private error module.
//
// Optionally implement ErrorBody to supply a custom JSON body; otherwise the
// standard {"error": {"code": ..., "message": ...}} envelope is used.
type HTTPError interface {
	error
	HTTPStatus() int
}

// ErrorBody lets an HTTPError provide the exact JSON value placed under the
// "error" key of the response envelope. Return any json-marshalable value.
type ErrorBody interface {
	ErrorBody() any
}

// FieldError describes a single request field that failed binding or validation.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule,omitempty"`
	Message string `json:"message,omitempty"`
}

// Error is the library's built-in HTTPError, used for binding and validation
// failures and available to callers who want a simple status-carrying error.
type Error struct {
	Status  int          `json:"-"`
	Code    string       `json:"code,omitempty"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"fields,omitempty"`
}

// NewError builds an Error with the given status, code and message.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// NewValidationError builds the library's standard field-level 400 — the same
// {"error":{"code":"bad_request","message":...,"fields":[...]}} envelope the
// built-in binder produces. A [Validator] implementation should return this so
// custom validation renders identically to the library's own binding errors.
func NewValidationError(message string, fields []FieldError) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: message, Fields: fields}
}

func (e *Error) Error() string   { return e.Message }
func (e *Error) HTTPStatus() int { return e.Status }

// ErrorBody renders the error itself (code/message/fields) as the envelope body.
func (e *Error) ErrorBody() any { return e }

// ErrorMapper maps an arbitrary error to an HTTPError. Register one per Registry
// (or per adapter) with the matching option to translate a project's own error
// type into a documented HTTP response. Return ok=false to defer to the default
// handling (the built-in aerror-compatible recognition, then a 500 fallback).
type ErrorMapper func(error) (status int, body json.RawMessage, ok bool)

// statusCoder and jsonError are the two methods the widely used aerror package
// exposes. Recognising them structurally lets aerror-based services keep working
// with zero glue and without this package importing aerror. Both must be present
// before an error is treated as aerror-shaped, so unrelated types are never
// misclassified.
type (
	statusCoder interface{ HTTPStatusCode() int }
	jsonError   interface{ ToJSON() json.RawMessage }
)

// internalErrorMessage is the generic body returned for unrecognised errors
// (the 500 fallback). The real error is never sent to the client — it would leak
// internal details (database errors, file paths, stack hints) — it is recorded
// on the carrier via RecordError so logging middleware can report it server-side.
const internalErrorMessage = "internal server error"

// resolveError turns any error into the (status, body) pair to render. mapper may
// be nil. The order is: explicit mapper -> HTTPError -> aerror-shaped duck typing
// -> 500 fallback. Only errors that opt into a status (HTTPError / aerror-shaped)
// or a custom mapper expose their own message; everything else is treated as an
// unexpected failure and rendered with a generic 500 body.
func resolveError(err error, mapper ErrorMapper) (int, json.RawMessage) {
	if mapper != nil {
		if status, body, ok := mapper(err); ok {
			return sanitizeErrorStatus(status), body
		}
	}

	var he HTTPError
	if errors.As(err, &he) && !isNilValue(he) {
		return sanitizeErrorStatus(he.HTTPStatus()), errorBodyJSON(he)
	}

	// aerror-compatible: requires BOTH a status and a JSON body method.
	var sc statusCoder
	var je jsonError
	if errors.As(err, &sc) && errors.As(err, &je) && !isNilValue(sc) && !isNilValue(je) {
		return sanitizeErrorStatus(sc.HTTPStatusCode()), je.ToJSON()
	}

	return http.StatusInternalServerError, simpleErrorJSON(internalErrorMessage)
}

// isNilValue reports whether v carries a nil pointer (or other nil-able kind)
// underneath its interface. A handler that returns a typed-nil error — the common
// `var e *Error; ...; return nil, e` mistake — would otherwise reach resolveError
// as a non-nil error whose methods dereference a nil receiver and panic the
// request; treating it as nil lets it fall through to the generic 500 fallback.
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// sanitizeErrorStatus guards against an error type or a custom ErrorMapper
// reporting a status outside the range net/http's WriteHeader accepts: such a
// value (0, negative, > 599) would otherwise panic the request when the
// response is written. An out-of-range code is treated as an unexpected failure
// and rendered as 500.
func sanitizeErrorStatus(status int) int {
	if status < 100 || status > 599 {
		return http.StatusInternalServerError
	}
	return status
}

// errorBodyJSON marshals the JSON value an HTTPError wants under "error".
func errorBodyJSON(he HTTPError) json.RawMessage {
	var body any = he
	if eb, ok := he.(ErrorBody); ok {
		body = eb.ErrorBody()
	} else {
		body = map[string]string{"message": he.Error()}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return simpleErrorJSON(he.Error())
	}
	return raw
}

func simpleErrorJSON(message string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"message": message})
	return raw
}
