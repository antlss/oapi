package oapi

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

// HTTPCarrier is the shared net/http implementation of [Carrier] used by every
// net/http-based adapter (nethttp, chi, echo). Those three carriers were ~90%
// byte-identical, and the library requires them to behave identically (the
// WriteJSON/Body/error bytes must match across adapters), so the implementation
// lives here once where the compiler keeps that invariant.
//
// An adapter embeds *HTTPCarrier in its own carrier and seeds W, R and MaxBody at
// construction, overriding only the framework-specific methods — Param everywhere,
// plus SetContext for echo, whose request lives inside echo.Context rather than
// being owned directly. MaxBody <= 0 disables the request body cap.
type HTTPCarrier struct {
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

// maxMultipartMemory bounds the in-memory portion of a parsed multipart form; the
// remainder streams to temp files.
const maxMultipartMemory = 32 << 20

// encodeErrBody is the sanitized body written when response marshalling fails;
// the unmarshalable value's message is never forwarded to the client.
const encodeErrBody = `{"error":{"message":"failed to encode response"}}`

func (c *HTTPCarrier) Method() string                    { return c.R.Method }
func (c *HTTPCarrier) Header(name string) string         { return c.R.Header.Get(name) }
func (c *HTTPCarrier) HeaderValues(name string) []string { return c.R.Header.Values(name) }

func (c *HTTPCarrier) Query() url.Values {
	c.queryOnce.Do(func() { c.query = c.R.URL.Query() })
	return c.query
}

func (c *HTTPCarrier) ContentType() string {
	ct := c.R.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ct
	}
	return media
}

func (c *HTTPCarrier) Body() ([]byte, error) {
	c.bodyOnce.Do(func() {
		if c.R.Body == nil {
			return
		}
		if c.MaxBody > 0 {
			c.R.Body = http.MaxBytesReader(c.W, c.R.Body, c.MaxBody)
		}
		c.body, c.bodyErr = io.ReadAll(c.R.Body)
		c.R.Body = io.NopCloser(bytes.NewReader(c.body))
	})
	return c.body, c.bodyErr
}

func (c *HTTPCarrier) MultipartForm() (*multipart.Form, error) {
	// Bound the whole upload (not just the in-memory part) so large multipart
	// bodies cannot exhaust disk via temp files.
	if c.MaxBody > 0 {
		c.R.Body = http.MaxBytesReader(c.W, c.R.Body, c.MaxBody)
	}
	if err := c.R.ParseMultipartForm(maxMultipartMemory); err != nil {
		return nil, err
	}
	return c.R.MultipartForm, nil
}

// Cleanup removes any temp files net/http spilled to disk while parsing a
// multipart form. net/http never does this for you, so without it large uploads
// leak files into the temp dir for the lifetime of the process. Adapters call it
// in a deferred cleanup from their handler.
func (c *HTTPCarrier) Cleanup() {
	if c.R.MultipartForm != nil {
		_ = c.R.MultipartForm.RemoveAll()
	}
}

func (c *HTTPCarrier) SetHeader(key, value string) { c.W.Header().Set(key, value) }

func (c *HTTPCarrier) WriteJSON(status int, body any) error {
	// Marshal first so an encode failure is caught before the status/body are
	// committed, and so the bytes match across adapters exactly (encoding/json
	// with no trailing newline, unlike json.Encoder).
	raw, err := json.Marshal(body)
	c.W.Header().Set("Content-Type", jsonContentType)
	if err != nil {
		c.W.WriteHeader(http.StatusInternalServerError)
		_, _ = c.W.Write([]byte(encodeErrBody))
		return err
	}
	c.W.WriteHeader(status)
	_, werr := c.W.Write(raw)
	return werr
}

func (c *HTTPCarrier) WriteBytes(status int, contentType string, data []byte) error {
	c.W.Header().Set("Content-Type", contentType)
	c.W.WriteHeader(status)
	_, err := c.W.Write(data)
	return err
}

func (c *HTTPCarrier) WriteEmpty(status int) error {
	c.W.WriteHeader(status)
	return nil
}

func (c *HTTPCarrier) Context() context.Context { return c.R.Context() }

// SetContext swaps the request's context. nethttp and chi own the request
// directly, so this default is enough; echo overrides it to also push the new
// request back into its echo.Context.
func (c *HTTPCarrier) SetContext(ctx context.Context) { c.R = c.R.WithContext(ctx) }

// Abort is a no-op for net/http-style adapters: native middleware wraps the whole
// handler, so it cannot observe an abort from inside. The core calls it when
// rendering an error; gin uses it for real.
func (c *HTTPCarrier) Abort()                {}
func (c *HTTPCarrier) RecordError(err error) { c.errs = append(c.errs, err) }

// Errors exposes recorded errors for logging middleware.
func (c *HTTPCarrier) Errors() []error { return c.errs }
