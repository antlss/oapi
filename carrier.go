package oapi

import (
	"context"
	"mime/multipart"
	"net/url"
)

// Carrier is the adapter seam between the framework-agnostic core and a concrete
// HTTP transport (gin, net/http, fiber, ...). Each adapter implements it once,
// wrapping a single in-flight request/response. The core reads the request
// through the read-side methods, runs the typed handler chain, and writes the
// outcome through the write-side methods.
//
// Contract:
//   - All SetHeader calls happen before the first Write* call.
//   - Exactly one of WriteJSON / WriteBytes / WriteEmpty is called per request.
//   - The core never calls Body and MultipartForm for the same request.
type Carrier interface {
	// Method returns the request method (for diagnostics; routing is the
	// adapter's job).
	Method() string
	// Header returns the first value of the named request header, or "".
	// Lookup is case-insensitive (canonicalized) on every adapter.
	Header(name string) string
	// HeaderValues returns every value of the named request header.
	HeaderValues(name string) []string
	// Param returns the named path parameter, or "".
	Param(name string) string
	// Query returns the parsed, multi-value-safe query string.
	Query() url.Values
	// ContentType returns the request media type with parameters (charset,
	// boundary) stripped, e.g. "application/json".
	ContentType() string
	// Body returns the raw request body. Implementations cache it so it can be
	// read more than once (a typed middleware and the handler may both bind it).
	Body() ([]byte, error)
	// MultipartForm parses and returns the multipart form.
	MultipartForm() (*multipart.Form, error)

	// SetHeader sets a response header. Must precede any Write* call.
	SetHeader(key, value string)
	// WriteJSON writes status and a JSON-encoded body.
	WriteJSON(status int, body any) error
	// WriteBytes writes status, content type and a raw body.
	WriteBytes(status int, contentType string, data []byte) error
	// WriteEmpty writes status with no body (e.g. 204).
	WriteEmpty(status int) error

	// Context returns the per-request context (carrying values injected by typed
	// middleware).
	Context() context.Context
	// SetContext replaces the per-request context.
	SetContext(ctx context.Context)

	// Abort marks the request handled so adapter-side after-middleware is
	// skipped. Called by the core when it renders an error.
	Abort()
	// RecordError records a non-fatal error for logging middleware to observe.
	// Best-effort and adapter-defined (gin appends to c.Errors; others store it).
	RecordError(err error)
}
