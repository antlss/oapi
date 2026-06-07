# oapi examples

A runnable **"Catalog API"** that exercises every capability of
[`oapi`](../README.md): typed binding of every request part, JSON/urlencoded/
multipart bodies, file upload & download, paging, security schemes, typed
middleware, the full error model, and custom/raw envelopes.

One framework-agnostic route set (`api/`) is mounted on several frameworks by the
commands in `cmd/`, so the same code proves the routes are transport-agnostic — and
the same routes feed the OpenAPI generator, so the docs can't drift from the code.

## Layout

| Path          | What |
| ------------- | ---- |
| `api/`        | The shared route set: handlers, request/response types, errors, typed middleware, the service, and the populated `Registry`. |
| `cmd/`        | Runnable commands (one per framework + two config demos + the doc generator). |
| `validation/` | A go-playground/validator-backed reference `oapi.Validator` (the core ships none). |
| `docsui/`     | Ready-to-serve HTML for Swagger UI / Redoc (loaded from a CDN, no extra deps). |
| `openapi/`    | The generated spec (`openapi.json` / `openapi.yaml`) and a `common.json` base. |

> These examples are their own Go module. Run every command **from this `examples/`
> directory** (a dev-only `replace` points at the local core/adapters). Requires Go 1.25+.

## Run a server

Each command mounts the same routes and serves the docs UI. Pick one:

| Command                  | Framework        | Port | Notes |
| ------------------------ | ---------------- | ---- | ----- |
| `go run ./cmd/gin`       | gin              | 8080 | |
| `go run ./cmd/nethttp`   | net/http         | 8081 | |
| `go run ./cmd/fiber`     | Fiber v2         | 8082 | |
| `go run ./cmd/customized`| net/http         | 8083 | Custom envelope + error parser installed **process-wide** via `Set*`. |
| `go run ./cmd/scoped`    | net/http         | 8084 | Two `App`s in one process: `/v1` (default shape) vs `/v2` (custom), no globals. |

```sh
cd examples
go run ./cmd/nethttp     # then open http://localhost:8081
```

Override the address with `-addr`, e.g. `go run ./cmd/gin -addr :9000`.

## View the API docs

<p align="center">
  <img src="../docs/images/swagger.png" alt="Swagger UI — POST /products" width="49%" />
  <img src="../docs/images/redoc.png" alt="Redoc — Create a product schema" width="49%" />
</p>
<p align="center"><sub>Swagger UI at <code>/swagger</code> (left) and Redoc at <code>/redoc</code> (right), both served by every command.</sub></p>

Every server exposes the same endpoints:

| Path             | What |
| ---------------- | ---- |
| `/`              | Landing page linking to both UIs. |
| `/swagger`       | **Swagger UI** — interactive, with "Try it out". |
| `/redoc`         | **Redoc** — clean read-only reference. |
| `/openapi.json`  | The raw OpenAPI 3 document. |

So with the net/http server running, open <http://localhost:8081/swagger> to call the
API from the browser, or <http://localhost:8081/redoc> to read it.

**Trying protected routes:** write endpoints (create/replace/delete) require an
`Authorization: Bearer <token>` header — any non-empty token works in the demo
(e.g. `Bearer demo`). The asset route (`GET /assets/*`) expects an `X-API-Key`
header. In Swagger UI, click **Authorize** and fill these in.

## Generate the OpenAPI document

`cmd/openapi-gen` writes the spec to disk (it wraps `tools/gen_doc`):

```sh
cd examples
go run ./cmd/openapi-gen -out ./openapi                       # openapi.json + openapi.yaml (validated first)
go run ./cmd/openapi-gen -out ./openapi -format json          # JSON only
go run ./cmd/openapi-gen -out ./openapi -base ./openapi/common.json  # overlay a base/common doc
```

Flags: `-out DIR`, `-format json,yaml`, `-base FILE`, `-no-validate`.

The same command runs via `go generate` (the directive lives in `api/routes.go`):

```sh
cd examples
go generate ./...        # regenerates openapi/openapi.{json,yaml}
```

The generated files under `openapi/` are committed, so regenerate and diff them when
you change the routes or the schema generation — that's how spec drift is caught in
review.
