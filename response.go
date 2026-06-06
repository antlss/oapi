package oapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
)

// Result is the library's HTTP response value. Its success body is shaped by the
// route's [ResponseEnvelope] (the default [DataEnvelope] gives the {"data": ...} /
// {"meta": ...} shape; configure it with [SetResponseEnvelope]/[WithEnvelope] or
// drop it with [WithRawResponse]); errors render through the resolution chain. The
// zero value is not usable — build one with [NewResult] (raw model) or
// [NewDataResult] (enveloped).
type Result struct {
	Status int              `json:"-"`
	Data   any              `json:"data,omitempty"`
	Error  *json.RawMessage `json:"error,omitempty"`
	Meta   any              `json:"meta,omitempty"`

	bizErr         error
	statusOverride bool // an explicit WithStatus was called; render must keep it
	fileName       string
	isFileOutput   bool

	headers     [][2]string
	errorMapper ErrorMapper
	envelope    ResponseEnvelope
}

// NewResult creates a 200 OK result carrying data rendered AS-IS — the raw handler
// model, with no envelope. Use [NewDataResult] for the standard {"data": ...}
// wrapping (or whatever envelope the route/process configures). Use the With*
// builders to adjust status, turn it into an error or a file download.
//
// A raw result pins itself to [RawEnvelope], so it stays raw even on a route whose
// configured envelope wraps — the explicit constructor choice wins.
func NewResult(data any) *Result {
	return &Result{Status: http.StatusOK, Data: data, envelope: RawEnvelope} //nolint:exhaustruct
}

// NewDataResult creates a 200 OK result whose payload is wrapped by the route's
// configured [ResponseEnvelope] (the default [DataEnvelope] gives {"data": ...}).
// Leaving the envelope unset lets the route or process-wide setting decide.
func NewDataResult(data any) *Result {
	return &Result{Status: http.StatusOK, Data: data} //nolint:exhaustruct
}

// NewListDataResult is a convenience for a paged collection: the items wrapped by
// the configured envelope, with standard pagination meta attached (see WithPaging).
func NewListDataResult(items any, count int64, perPage, current int) *Result {
	return NewDataResult(items).WithPaging(count, perPage, current)
}

// WithStatus overrides the HTTP status code. On an error result it takes
// precedence over the status resolved from the error at render time.
func (r *Result) WithStatus(status int) *Result {
	r.Status = status
	r.statusOverride = true
	return r
}

// WithMeta attaches an arbitrary meta object (e.g. custom pagination cursors).
func (r *Result) WithMeta(meta any) *Result {
	r.Meta = meta
	return r
}

// WithHeader sets a response header written just before the body, letting a
// handler attach metadata it computed (Location on a 201, ETag, Cache-Control,
// a single Set-Cookie, rate-limit headers, ...) without dropping down to a
// native framework handler. Calls are applied in order with Set semantics, so a
// repeated key overwrites; for multiple values of the same header (e.g. several
// Set-Cookie) use native adapter middleware.
func (r *Result) WithHeader(key, value string) *Result {
	r.headers = append(r.headers, [2]string{key, value})
	return r
}

// PagingMeta is the standard pagination metadata attached by [Result.WithPaging]
// under the envelope's "meta" key. It is exported so a route can document it with
// WithMetaType[PagingMeta](); the example:"" tags give the docs sample values.
type PagingMeta struct {
	Count   int64 `json:"count"    example:"137"`
	Pages   int   `json:"pages"    example:"7"`
	PerPage int   `json:"per_page" example:"20"`
	Current int   `json:"current"  example:"1"`
}

// WithPaging attaches standard pagination meta computed from the total count.
func (r *Result) WithPaging(count int64, perPage, current int) *Result {
	if perPage <= 0 {
		perPage = 1 // guard against divide-by-zero
	}
	r.Meta = PagingMeta{
		Count:   count,
		Pages:   int(math.Ceil(float64(count) / float64(perPage))),
		PerPage: perPage,
		Current: current,
	}
	return r
}

// WithError turns the result into an error response. The status and JSON body are
// resolved at render time via resolveError — an optional custom mapper, then
// HTTPError, then aerror-compatible duck typing, then a 500 fallback — so the
// route's ErrorMapper applies even when a RichHandler calls WithError before that
// mapper is attached. An explicit WithStatus still overrides the resolved status.
// The original error is recorded on the carrier (see render) so logging
// middleware can report it.
func (r *Result) WithError(err error) *Result {
	r.Data = nil
	r.bizErr = err
	return r
}

// WithFile marks the result as a binary file download. The data passed to
// NewResult must be the file bytes ([]byte).
func (r *Result) WithFile(filename string) *Result {
	r.isFileOutput = true
	r.fileName = filename
	return r
}

// withErrorMapper wires the route's error mapper so WithError can use it.
func (r *Result) withErrorMapper(m ErrorMapper) *Result {
	r.errorMapper = m
	return r
}

// withEnvelope wires the route's success envelope so render can wrap the payload.
func (r *Result) withEnvelope(e ResponseEnvelope) *Result {
	r.envelope = e
	return r
}

// jsonContentType is the media type written for JSON bodies that go out through
// WriteBytes (a custom error body), matching the adapters' WriteJSON header.
const jsonContentType = "application/json; charset=utf-8"

// render writes the result through the carrier.
func (r *Result) render(c Carrier) error {
	// Custom headers must precede any Write* call (Carrier contract).
	for _, h := range r.headers {
		c.SetHeader(h[0], h[1])
	}

	if r.isFileOutput {
		body, ok := r.Data.([]byte)
		if !ok {
			// Hardening: a file result whose data is not []byte is a programming
			// error — surface it as a 500 rather than silently writing an empty
			// file.
			c.Abort()
			return NewResult(nil).
				withErrorMapper(r.errorMapper).
				WithError(NewError(http.StatusInternalServerError, "render_error", "file result data is not []byte")).
				render(c)
		}
		c.SetHeader("Content-Disposition", contentDisposition(r.fileName))
		return c.WriteBytes(r.Status, "application/octet-stream", body)
	}

	if r.bizErr != nil {
		// Resolve the error here, not in WithError, so the route's ErrorMapper is
		// honoured even when a RichHandler built this Result via WithError before
		// the mapper was attached (handler.go wires it only after the handler
		// returns). An explicit WithStatus still wins.
		status, body, wrap := resolveError(r.bizErr, r.errorMapper)
		if !r.statusOverride {
			r.Status = status
		}
		// Surface to logging middleware (e.g. gin's c.Errors); does not write.
		c.RecordError(r.bizErr)

		if r.Status == http.StatusNoContent {
			return c.WriteEmpty(http.StatusNoContent)
		}
		if !wrap {
			// A custom ErrorMapper / ErrorParser owns the full wire body, so write
			// it verbatim with no {"error": ...} wrapper.
			return c.WriteBytes(r.Status, jsonContentType, body)
		}
		// Built-in resolution keeps the standard {"error": ...} envelope, produced
		// by marshalling the Result struct (data is nil here, so only "error"
		// — and any attached meta — appears).
		r.Error = &body
		return c.WriteJSON(r.Status, r)
	}

	if r.Status == http.StatusNoContent {
		return c.WriteEmpty(http.StatusNoContent)
	}
	return c.WriteJSON(r.Status, resolveEnvelope(r.envelope).Wrap(r.Data, r.Meta))
}

// contentDisposition builds a safe attachment Content-Disposition value. The
// filename is quoted (so spaces work) with quotes/backslashes/control characters
// stripped to avoid breaking the header or injecting CRLF, and a RFC 5987
// filename* parameter carries the original UTF-8 name for clients that support
// it.
func contentDisposition(filename string) string {
	ascii := make([]rune, 0, len(filename))
	for _, r := range filename {
		switch {
		case r < 0x20 || r == 0x7f: // control chars (incl. CR/LF) — drop
			continue
		case r == '"' || r == '\\' || r > 0x7f: // quotes, escapes, non-ASCII
			ascii = append(ascii, '_')
		default:
			ascii = append(ascii, r)
		}
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		string(ascii), url.PathEscape(filename))
}
