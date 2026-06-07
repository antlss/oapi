// Package httpcarrier holds the shared net/http implementation of oapi.Carrier
// used by every net/http-based adapter (nethttp, chi, echo). The three carriers
// were ~90% byte-identical; the library already requires them to behave
// identically (the WriteJSON/Body/error bytes must match across adapters), so
// the duplication is consolidated here where the compiler keeps that invariant.
//
// It lives under internal/ so it is not part of the public API. The separate
// adapter modules can still import it: Go's internal rule is import-path based,
// and adapter/chi and adapter/echo are rooted under github.com/antlss/oapi, so
// github.com/antlss/oapi/internal/httpcarrier is in scope for them.
//
// Adapters embed [Base] (as *Base) in their own carrier and override only the
// framework-specific methods — Param everywhere, plus SetContext for echo, whose
// request lives inside echo.Context rather than being owned directly.
package httpcarrier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
)

const (
	// MaxMultipartMemory bounds the in-memory portion of a parsed multipart form;
	// the remainder streams to temp files.
	MaxMultipartMemory = 32 << 20

	// jsonContentType matches the media type written for every JSON body so the
	// success path stays byte-identical across adapters.
	jsonContentType = "application/json; charset=utf-8"

	// encodeErrBody is the sanitized body written when response marshalling fails;
	// the unmarshalable value's message is never forwarded to the client.
	encodeErrBody = `{"error":{"message":"failed to encode response"}}`
)

// Base is the shared net/http carrier. Embed it as *Base in an adapter's carrier
// and seed W, R and MaxBody at construction. MaxBody <= 0 disables the request
// body cap. It implements every oapi.Carrier method except Param, which is
// framework-specific; echo additionally overrides SetContext.
type Base struct {
	W       http.ResponseWriter
	R       *http.Request
	MaxBody int64 // request body cap in bytes; <= 0 means unlimited

	queryOnce sync.Once
	query     url.Values
	bodyOnce  sync.Once
	body      []byte
	bodyErr   error

	// errs collects RecordError calls for logging middleware (see Errors).
	errs []error
}

func (b *Base) Method() string                    { return b.R.Method }
func (b *Base) Header(name string) string         { return b.R.Header.Get(name) }
func (b *Base) HeaderValues(name string) []string { return b.R.Header.Values(name) }

func (b *Base) Query() url.Values {
	b.queryOnce.Do(func() { b.query = b.R.URL.Query() })
	return b.query
}

func (b *Base) ContentType() string {
	ct := b.R.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	return media
}

func (b *Base) Body() ([]byte, error) {
	b.bodyOnce.Do(func() {
		if b.R.Body == nil {
			return
		}
		if b.MaxBody > 0 {
			b.R.Body = http.MaxBytesReader(b.W, b.R.Body, b.MaxBody)
		}
		b.body, b.bodyErr = io.ReadAll(b.R.Body)
		b.R.Body = io.NopCloser(bytes.NewReader(b.body))
	})
	return b.body, b.bodyErr
}

func (b *Base) MultipartForm() (*multipart.Form, error) {
	// Bound the whole upload (not just the in-memory part) so large multipart
	// bodies cannot exhaust disk via temp files.
	if b.MaxBody > 0 {
		b.R.Body = http.MaxBytesReader(b.W, b.R.Body, b.MaxBody)
	}
	if err := b.R.ParseMultipartForm(MaxMultipartMemory); err != nil {
		return nil, err
	}
	return b.R.MultipartForm, nil
}

// Cleanup removes any temp files net/http spilled to disk while parsing a
// multipart form. net/http never does this for you, so without it large uploads
// leak files into the temp dir for the lifetime of the process. Adapters call it
// in a deferred cleanup from their handler.
func (b *Base) Cleanup() {
	if b.R.MultipartForm != nil {
		_ = b.R.MultipartForm.RemoveAll()
	}
}

func (b *Base) SetHeader(key, value string) { b.W.Header().Set(key, value) }

func (b *Base) WriteJSON(status int, body any) error {
	// Marshal first so an encode failure is caught before the status/body are
	// committed, and so the bytes match across adapters exactly (encoding/json
	// with no trailing newline, unlike json.Encoder).
	raw, err := json.Marshal(body)
	b.W.Header().Set("Content-Type", jsonContentType)
	if err != nil {
		b.W.WriteHeader(http.StatusInternalServerError)
		_, _ = b.W.Write([]byte(encodeErrBody))
		return err
	}
	b.W.WriteHeader(status)
	_, werr := b.W.Write(raw)
	return werr
}

func (b *Base) WriteBytes(status int, contentType string, data []byte) error {
	b.W.Header().Set("Content-Type", contentType)
	b.W.WriteHeader(status)
	_, err := b.W.Write(data)
	return err
}

func (b *Base) WriteEmpty(status int) error {
	b.W.WriteHeader(status)
	return nil
}

func (b *Base) Context() context.Context { return b.R.Context() }

// SetContext swaps the request's context. nethttp and chi own the request
// directly, so this default is enough; echo overrides it to also push the new
// request back into its echo.Context.
func (b *Base) SetContext(ctx context.Context) { b.R = b.R.WithContext(ctx) }

// Abort is a no-op for net/http-style adapters: native middleware wraps the whole
// handler, so it cannot observe an abort from inside. The core calls it when
// rendering an error; gin uses it for real.
func (b *Base) Abort()                {}
func (b *Base) RecordError(err error) { b.errs = append(b.errs, err) }

// Errors exposes recorded errors for logging middleware.
func (b *Base) Errors() []error { return b.errs }
