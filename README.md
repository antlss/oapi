# oapi

Turn typed Go handlers into HTTP endpoints whose request and response structs
are the single source of truth for **binding**, **validation** _and_ **OpenAPI 3
documentation**.

The struct tags you already write to bind and validate a request are the same
tags the OpenAPI generator reads. The docs are generated from the exact Go types
the handler binds, so **they can never drift** from the running code.

[![Go Reference](https://pkg.go.dev/badge/github.com/antlss/oapi.svg)](https://pkg.go.dev/github.com/antlss/oapi)
[![Go Version](https://img.shields.io/badge/go-1.25-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

> Status: **pre-1.0**. The API is still settling and may change before a tagged
> `v1`.

## Features

- **Typed handlers** — a handler is `func(ctx, Request[Header, Param, Query, Body]) (*Response, error)`. Each request part is bound from a different source; use `struct{}` for parts you do not consume.
- **Typed middleware** — `WithTypedBefore` middleware sees the same parsed, typed request as the handler (the request is parsed once and shared).
- **Five adapters, one route set** — the framework-agnostic core runs unchanged on **net/http**, **gin**, **Fiber v2**, **chi** and **Echo v4**. The same `[]Route` mounts on any of them.
- **OpenAPI 3 generation** — a `Registry` turns your routes into a validated OpenAPI 3 document (`JSON`/`YAML`/`Write`), reading the same struct tags used for binding and validation.
- **Pluggable seams** — the **validator**, the **response envelope**, and the **error parser** are all swappable interfaces. The core ships none of them by default and depends on no validation library.
- **File upload & download** — multipart bodies (`[]*multipart.FileHeader`) bind like any other field; binary downloads stream via `NewResult(bytes).WithFile(...)`.
- **Paging & envelopes** — the default `{"data": ...}` / `{"data": ..., "meta": ...}` envelope with paging meta, plus per-route custom envelopes and raw (un-enveloped) responses.
- **Decoupled error model** — `HTTPError`, per-route `ErrorMapper`, and a process-wide `ErrorParser` seam. Unrecognised errors never leak: they render a generic 500 and are recorded for server-side logging.

## Install

```sh
go get github.com/antlss/oapi
```

The net/http adapter ships with the core (no extra dependencies). The gin,
Fiber, chi and Echo adapters live in their own modules, so you pull in only what
you import:

```sh
go get github.com/antlss/oapi/adapter/gin
go get github.com/antlss/oapi/adapter/fiber
go get github.com/antlss/oapi/adapter/chi
go get github.com/antlss/oapi/adapter/echo
```

Validation is opt-in via the [`Validator`](#validation-seam) seam — the core
ships none and depends on no validation library. A go-playground/validator-backed
reference implementation lives in `examples/validation`; copy it into your project
or implement the small interface yourself.

## Quickstart

A complete net/http service: one typed route, validation turned on, and the
OpenAPI document served at `/openapi.json`.

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/antlss/oapi"
	nethttp "github.com/antlss/oapi/adapter/nethttp"
)

// The request struct is the single source of truth: the `header`/`uri`/`form`/
// `json` tags drive binding, the `binding` tags drive both runtime validation
// and the generated OpenAPI schema (required/enum/bounds), and `example` drives
// the sample values shown in the docs.
type CreateProductBody struct {
	Name     string  `json:"name"     binding:"required,min=2,max=120"        example:"Mechanical Keyboard"`
	Price    float64 `json:"price"    binding:"required,gt=0"                 example:"49.90"`
	Currency string  `json:"currency" binding:"required,oneof=USD EUR JPY"    example:"USD"`
}

type Product struct {
	ID       int     `json:"id"       example:"1001"`
	Name     string  `json:"name"     example:"Mechanical Keyboard"`
	Price    float64 `json:"price"    example:"49.90"`
	Currency string  `json:"currency" example:"USD"`
}

// CreateProduct is a typed RequestHandler. Header/Param/Query are unused here,
// so they are struct{}. Returning a *Product lets NewRoute wrap it in the
// default {"data": ...} envelope automatically.
var CreateProduct = oapi.NewRoute(
	http.MethodPost, "/products",
	func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, CreateProductBody]) (*Product, error) {
		return &Product{
			ID:       1001,
			Name:     req.Body.Name,
			Price:    req.Body.Price,
			Currency: req.Body.Currency,
		}, nil
	},
	oapi.WithSummary("Create a product"),
	oapi.WithTags("catalog"),
	oapi.WithSuccessStatus(http.StatusCreated),
	oapi.WithResponseType[Product](),
)

func main() {
	// Validation is opt-in: install a Validator so the `binding` rules are
	// enforced (the core ships none). A go-playground-backed reference lives in
	// examples/validation — copy it in, then oapi.SetValidator(validation.New()).
	// Without one, the library logs a one-time warning and skips the rules.

	mux := http.NewServeMux()

	// Mount the typed route(s) on the standard-library mux.
	nethttp.RegisterAll(mux, CreateProduct)

	// Build the OpenAPI document from the SAME routes and serve it.
	reg := oapi.NewRegistry("Catalog API", "v1").
		Describe("A tiny example API.").
		AddServer("http://localhost:8080", "Local").
		Add(CreateProduct)
	mux.HandleFunc("GET /openapi.json", nethttp.SpecHandler(reg))

	log.Println("listening on :8080  (spec at /openapi.json)")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
```

`POST /products` now binds `CreateProductBody` (and validates it once you install
a `Validator`), returns `201 Created` with `{"data": {...}}`, and
`GET /openapi.json` serves an OpenAPI 3 document whose `CreateProductBody`/
`Product` schemas — required fields, the `oneof` enum, the bounds — are generated
from the exact same struct.

## Adapters

The core is framework-agnostic: a `Registry` produces bytes and an
`*openapi3.T`, and routes are mounted by a thin adapter. Each adapter exposes the
same surface: `Register`, `RegisterAll` and `SpecHandler`.

| Framework   | Import path                              | Notes                                  |
| ----------- | ---------------------------------------- | -------------------------------------- |
| net/http    | `github.com/antlss/oapi/adapter/nethttp` | Ships with the core, no extra deps. Go 1.22+ method-aware `ServeMux`. |
| gin         | `github.com/antlss/oapi/adapter/gin`     | Separate module.                       |
| Fiber v2    | `github.com/antlss/oapi/adapter/fiber`   | Separate module.                       |
| chi         | `github.com/antlss/oapi/adapter/chi`     | Separate module (go-chi/chi v5).       |
| Echo v4     | `github.com/antlss/oapi/adapter/echo`    | Separate module.                       |

Switching frameworks is just a different `RegisterAll` call over the same
`api.Routes()`.

## Documentation generation

A `Registry` is the OpenAPI side of the library. Add routes and document-level
metadata, then render or write the spec:

```go
reg := oapi.NewRegistry("Catalog API", "v1").
	Describe("...").
	Contact("API Team", "https://example.com/support", "api@example.com").
	License("Apache-2.0", "https://www.apache.org/licenses/LICENSE-2.0").
	AddServer("https://api.example.com", "Production").
	AddSecurityScheme("bearerAuth", oapi.BearerAuth()).
	AddTag("catalog", "Browse and manage products").
	Add(routes...)

jsonBytes, err := reg.JSON() // indented JSON
yamlBytes, err := reg.YAML() // block-style YAML
err = reg.Validate(ctx)      // check against the OpenAPI 3 schema
```

To write the spec to disk, use `Registry.Write`:

```go
paths, err := reg.Write(ctx, oapi.GenConfig{Dir: "openapi"})
// writes openapi/openapi.json and openapi/openapi.yaml (validated first)
```

For a turnkey `go:generate` command, `tools/gen_doc` wraps `Write` in a `Main`
that parses flags (`-out`, `-format`, `-base`, `-no-validate`):

```go
//go:generate go run ./cmd/openapi-gen -out ./openapi

package main

import (
	gendoc "github.com/antlss/oapi/tools/gen_doc"
	"example.com/app/api"
)

func main() { gendoc.Main(api.Registry()) }
```

A `Base` document can supply defaults (info, vendor extensions like
`x-tagGroups`, ...) that the generated paths and the `Registry`'s own setters
overlay — see `Registry.Base` / `LoadBaseFile`.

## Concepts

### Request parts

A handler's request is `Request[Header, Param, Query, Body]`; each part binds
from a different source, and `struct{}` means "this endpoint does not use it":

| Part     | Source            | Tag        |
| -------- | ----------------- | ---------- |
| `Header` | request headers   | `header:"..."` |
| `Param`  | path parameters   | `uri:"..."`    |
| `Query`  | query string      | `form:"..."`   |
| `Body`   | JSON body         | `json:"..."`   |
| `Body`   | urlencoded / multipart body | `form:"..."` (plus `[]*multipart.FileHeader` for files) |

The `binding` tag carries the validation rules; the same rules become OpenAPI
constraints (`required`, `oneof` → enum, `min`/`max`/`gt` → bounds, `uuid`/
`email`/`url` → formats). `example` tags set the sample values shown in the docs.

### Result & envelope

- `NewRoute(...)` takes a typed `RequestHandler` that returns `*Response`; the value is wrapped by the route's envelope (default `{"data": ...}`). Returning `nil` yields `204 No Content`.
- `NewRichRoute(...)` takes a `RichHandler` that returns a fully built `*Result` for paging, custom headers/status, file downloads, etc. Use `WithResponseType[T]()` to keep the docs accurate.
- The envelope is a `ResponseEnvelope` seam. Defaults to `DataEnvelope`; override per route with `WithEnvelope(oapi.KeyedEnvelope{...})` or `WithRawResponse()` (no wrapper), or process-wide with `SetResponseEnvelope`. The same definition drives both the wire body and the documented schema, so they cannot diverge.
- Result builders: `NewDataResult`, `NewListDataResult` (paging meta), `NewResult` (raw), with `.WithStatus`, `.WithHeader`, `.WithMeta`, `.WithFile`.

### Error model

- `HTTPError` — any error implementing `HTTPStatus() int` controls its own status; optionally implement `ErrorBody` for a custom JSON body. Build one with `oapi.NewError(status, code, message)` or the standard field-level 400 with `oapi.NewValidationError(message, fields)`.
- `ErrorMapper` — a per-route mapper (`WithErrorMapper`) translating domain errors into a full wire body.
- `ErrorParser` — a process-wide, all-in-one error seam (`SetErrorParser`) that maps errors to a status, produces the wire body, **and** describes that body's schema for the docs.
- Resolution order: per-route mapper → global `ErrorParser` → built-in `HTTPError` → aerror-shaped duck typing → generic 500. Unrecognised errors are never leaked to the client; they are recorded on the carrier for logging middleware.

### Validation seam

Validation is a pluggable `Validator` interface installed once at startup with
`SetValidator`. The core depends on no validation library and ships no validator.
`SetValidator(nil)` disables validation explicitly (also silences the startup
warning). The `RuleTag` variable (default `"binding"`) selects which struct tag
both the validator and the schema generator read.

A go-playground/validator-backed reference implementation lives in
`examples/validation`; copy it into your project or wire the small `Validator`
interface yourself, then `oapi.SetValidator(validation.New())`.

## Examples

The `examples/` tree is a runnable "Catalog API" exercising every capability —
typed binding of every request part, JSON/urlencoded/multipart bodies, file
upload/download, paging, security schemes, typed middleware, the full error
model, custom envelopes, and a fully populated `Registry`. The same route set is
mounted on net/http, gin, and Fiber by `examples/cmd/{nethttp,gin,fiber}`.

Two commands demonstrate configuration. `examples/cmd/customized` installs a custom
response envelope and error shape **process-wide** via `oapi.Set*`. `examples/cmd/scoped`
does the same per `oapi.App` instead: it serves two differently configured groups
(`/v1` and `/v2`, distinct envelope + error shape) in one process with **no** global
`Set*` call, scoping both the wire bytes and the generated docs.

## License

[MIT](LICENSE) © 2026 antlss
