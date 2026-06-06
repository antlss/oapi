package oapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"reflect"
	"sync"
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

// ErrorMapper maps an arbitrary error to an HTTP response for a single route
// (register it with [WithErrorMapper]). When ok is true it OWNS the full wire
// body: body is marshalled and written as-is, with no {"error": ...} wrapper, so a
// mapper controls the exact shape clients see. Return ok=false to defer to the
// global [ErrorParser], then the built-in HTTPError/aerror recognition and the 500
// fallback. body may be any json-marshalable value; a [json.RawMessage] is written
// verbatim.
type ErrorMapper func(error) (status int, body any, ok bool)

// ErrorParser is the process-wide, all-in-one error seam — the error counterpart
// of [Validator]. It owns a project's error handling end to end: recognising the
// project's error types, mapping them to a status, producing the exact wire body,
// AND describing that body's shape for the OpenAPI docs (so error responses stay
// type-driven and cannot drift). Install one with [SetErrorParser].
//
// It runs after any per-route [ErrorMapper] and before the built-in HTTPError /
// aerror recognition and the 500 fallback, so unclaimed errors still get the
// library's safe, non-leaking default.
type ErrorParser interface {
	// Resolve maps err to a status and the FULL json-marshalable wire body (no
	// {"error": ...} wrapper is added). Return ok=false to defer to the next
	// resolver. A [json.RawMessage] body is written verbatim.
	Resolve(err error) (status int, body any, ok bool)
	// ErrorType returns the Go type of the error body, whose `json`/`example` tags
	// drive its documented schema — exactly like a response type. Return nil to
	// document errors with the built-in {"error": {...}} schema (but then Resolve
	// should also produce that shape, or the docs will not match).
	ErrorType() reflect.Type
}

// errorParser is configured once at startup and read on every error render and
// during doc generation. Not lock-guarded — install before serving, like the rest
// of the library's process-wide configuration.
var (
	errorParser        ErrorParser //nolint:gochecknoglobals
	errorParserWarning sync.Once   //nolint:gochecknoglobals
)

// SetErrorParser installs the process-wide [ErrorParser]. Call it once during
// startup, before serving requests. Passing nil clears it (errors fall back to the
// built-in handling). It is not safe to call concurrently with in-flight requests.
//
// A parser whose ErrorType is nil cannot describe its body for the docs; since an
// all-in-one parser almost always renders a custom shape, this logs a one-time
// warning to catch the drift early.
func SetErrorParser(p ErrorParser) {
	errorParser = p
	if p != nil && p.ErrorType() == nil {
		errorParserWarning.Do(func() {
			log.Println("[oapi] SetErrorParser: the parser's ErrorType() is nil, so error " +
				"responses are documented with the built-in error schema and may not match the " +
				"body the parser renders. Return a non-nil ErrorType() to keep the docs in sync.")
		})
	}
}

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

// resolveError turns any error into the (status, body, wrap) triple to render.
// mapper may be nil. The order is: per-route mapper -> global ErrorParser ->
// HTTPError -> aerror-shaped duck typing -> 500 fallback. The first two layers OWN
// the full wire body (wrap=false); the built-in layers produce the inner value
// placed under the standard {"error": ...} envelope (wrap=true). Only errors that
// opt into a status (a mapper/parser, HTTPError or aerror-shaped) expose their own
// message; everything else is rendered with a generic, non-leaking 500 body.
func resolveError(err error, mapper ErrorMapper) (int, json.RawMessage, bool) {
	// 1. Per-route mapper — owns the full wire body.
	if mapper != nil {
		if status, body, ok := mapper(err); ok {
			if raw, mErr := marshalErrorBody(body); mErr == nil {
				return sanitizeErrorStatus(status), raw, false
			}
		}
	}

	// 2. Process-wide ErrorParser — owns the full wire body.
	if errorParser != nil {
		if status, body, ok := errorParser.Resolve(err); ok {
			if raw, mErr := marshalErrorBody(body); mErr == nil {
				return sanitizeErrorStatus(status), raw, false
			}
		}
	}

	// 3. Built-in HTTPError — inner body under the {"error": ...} envelope.
	var he HTTPError
	if errors.As(err, &he) && !isNilValue(he) {
		return sanitizeErrorStatus(he.HTTPStatus()), errorBodyJSON(he), true
	}

	// 4. aerror-compatible: requires BOTH a status and a JSON body method.
	var sc statusCoder
	var je jsonError
	if errors.As(err, &sc) && errors.As(err, &je) && !isNilValue(sc) && !isNilValue(je) {
		return sanitizeErrorStatus(sc.HTTPStatusCode()), je.ToJSON(), true
	}

	// 5. Unrecognised: generic 500, original error never leaked.
	return http.StatusInternalServerError, simpleErrorJSON(internalErrorMessage), true
}

// marshalErrorBody renders a mapper/parser body to JSON. A json.RawMessage is
// passed through verbatim (so existing mappers keep their exact bytes); anything
// else is marshalled. A marshalling failure is reported so resolveError can fall
// through to the safe built-in handling instead of writing a broken or leaking
// body.
func marshalErrorBody(body any) (json.RawMessage, error) {
	if raw, ok := body.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(body)
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
