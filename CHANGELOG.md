# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is **pre-1.0**, the public API may change between releases;
breaking changes are called out under their version's **Changed**/**Removed**
sections. Once `v1.0.0` is tagged, the usual semantic-versioning guarantees
apply.

## [Unreleased]

### Added

- **Pluggable response envelope** (`ResponseEnvelope` seam). The success-body
  wrapper is now swappable per route (`WithEnvelope`, `WithRawResponse`) or
  process-wide (`SetResponseEnvelope`). The declarative `KeyedEnvelope` (data
  key + meta key + constant fields) covers the common case without importing
  `openapi3`. A single definition drives both the wire body and the documented
  schema, so they cannot diverge. `DataEnvelope` remains the default and is
  byte-identical to the previous behaviour.
- **Process-wide `ErrorParser` seam** (`SetErrorParser`). An all-in-one error
  handler that recognises a project's error types, maps them to a status,
  produces the exact wire body, **and** describes that body's schema for the
  OpenAPI docs (so error responses stay type-driven and cannot drift). It runs
  after any per-route `ErrorMapper` and before the built-in handling.
- **Document-level metadata builders** on `Registry`: `Describe`, `Contact`,
  `License`, `TermsOfService`, `ExternalDocs`, `Logo`/`LogoWith` (Redoc
  `x-logo`), `AddTag`, `TagGroup` (Redoc `x-tagGroups`), and `Base`/
  `LoadBaseFile` for overlaying a common base document with deterministic
  precedence.
- **Built-in OpenAPI generation**: `Registry.Write` (validated JSON/YAML to
  disk, all-or-nothing) and the turnkey `tools/gen_doc` `Main` for a one-line
  `go:generate` generator command.

### Changed

- **Validation is now a pluggable seam.** The core no longer depends on
  `go-playground/validator`; install a `Validator` with `SetValidator`. The
  default, `binding`-tag-reading implementation lives in the examples
  (`examples/playground`) and is slated to move to a dedicated
  `validation/playground` module.
- The repository was split into **multiple modules** (lean core + net/http,
  `adapter/gin`, `adapter/fiber`, examples, `tools/gen_doc`) so consumers pull
  in only the adapters and validator they import.
- `ErrorMapper` now returns `any` for the body (previously a narrower type), and
  result construction returns the raw payload, to support the pluggable
  envelope. **(breaking)**
- `*Result` rendering inherits the route's envelope unless the handler's
  constructor pinned one (`NewResult` pins the raw envelope; `NewDataResult`
  inherits).

### Fixed

A review pass before public release fixed seven verified bugs:

- Nested-struct documentation could drift from the bound type; the schema
  generator now recurses into named nested structs consistently.
- Pointer-bodied requests could panic when binding; an extra pointer level a
  generic `Body` type adds is now collapsed before validation.
- A handler returning a **typed-nil** error (`var e *Error; return nil, e`) no
  longer panics; it is treated as nil and falls through to the safe 500.
- An error type (or `ErrorMapper`) reporting an out-of-range HTTP status
  (`0`, negative, `> 599`) no longer panics the request when the response is
  written; it is sanitised to 500.
- Unrecognised errors never leak internal details to the client: the generic
  `internal server error` 500 body is returned and the real error is recorded on
  the carrier for server-side logging only.
- Request bodies (JSON, urlencoded, multipart) are bounded by
  `DefaultMaxRequestBytes` (10 MiB, configurable) to guard against
  memory/disk exhaustion; multipart temp files spilled to disk are cleaned up.
- JSON output is byte-identical across adapters (`encoding/json`, no trailing
  newline), and `WithHeader` lets rich results set response headers.

## [0.0.0] - Unreleased

Initial pre-release development. No tagged versions yet.

[Unreleased]: https://github.com/antlss/oapi/commits/main
