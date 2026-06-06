# Contributing to oapi

Thanks for your interest in improving **oapi**. This guide covers how to build,
test, and submit changes.

## Code of conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/).
By participating you agree to uphold it.

## Repository layout

oapi is a **multi-module** repository. The framework-agnostic core (which
includes the net/http adapter and `tools/gen_doc`) is one module; each other
adapter and the examples are separate modules so consumers pull in only what
they import:

| Path                       | Module                                       |
| -------------------------- | -------------------------------------------- |
| `.` (repo root)            | `github.com/antlss/oapi` (core + net/http adapter + `tools/gen_doc`) |
| `adapter/gin`              | `github.com/antlss/oapi/adapter/gin`         |
| `adapter/fiber`            | `github.com/antlss/oapi/adapter/fiber`       |
| `adapter/chi`              | `github.com/antlss/oapi/adapter/chi`         |
| `adapter/echo`             | `github.com/antlss/oapi/adapter/echo`        |
| `examples`                 | examples module (demo Catalog API + reference validator) |

Each adapter has a dev-only `replace` directive pointing at the local core so
the in-place build works without published tags. Because the modules are
independent, `go build`/`go test` must be run **per module** — a single command
at the root does not cross module boundaries.

## Building and testing

Run these in **each** module directory (root, `adapter/gin`, `adapter/fiber`,
`adapter/chi`, `adapter/echo`, `examples`):

```sh
go build ./...
go test -race ./...
go vet ./...
```

A convenient loop over every module:

```sh
for dir in . adapter/gin adapter/fiber adapter/chi adapter/echo examples; do
  (cd "$dir" && go build ./... && go test -race ./...) || exit 1
done
```

When you change the public API or the schema generation, regenerate and diff the
example OpenAPI document (`go generate ./...` in `examples`) so spec drift is
caught in review.

## Code style

- **Formatting** — all code must be `gofmt`-clean (`gofmt -l .` returns nothing). `goimports` for import grouping is recommended.
- **Linting** — keep the tree `go vet`-clean. We use [`golangci-lint`](https://golangci-lint.run/); run `golangci-lint run` before pushing and fix or explicitly `//nolint`-justify findings.
- **Documentation** — every exported identifier needs a doc comment that starts with the identifier's name (standard godoc convention). The library's design is documented at the seams (`Validator`, `ResponseEnvelope`, `ErrorParser`); keep those contracts accurate when you touch them.
- **No new core dependencies** — the core deliberately depends on no validation framework and no concrete web framework. Framework-specific code belongs in an adapter module; validator-specific code belongs outside the core (see `examples/validation` for the reference implementation). Don't add such an import to the core.
- **Keep docs and wire in sync** — the central invariant is that generated docs cannot drift from binding/validation/response behaviour. Any change to one half must update the other (e.g. an envelope's `Wrap` and `WrapSchema`).

## Tests

- Add table-driven tests for new behaviour and regression tests for fixed bugs.
- Run with `-race`; concurrency-sensitive seams (process-wide validator/envelope/error-parser) are documented as "configure before serving" — tests should respect that.
- Prefer testing the framework-agnostic core directly; adapter tests should cover only the transport translation.

## Commit and PR conventions

- Write focused commits with imperative subject lines (e.g. `fix: don't panic on typed-nil error`). Conventional-commit prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`) are encouraged.
- Reference any related issue in the body.
- Each PR should: build and test cleanly in every affected module, and keep `gofmt`/`golangci-lint` clean.
- Keep PRs scoped. Unrelated refactors should be separate PRs.

## Reporting bugs and proposing features

Open an issue describing the behaviour, the expected result, the Go version, and
which module/adapter is involved. For security issues, **do not** open a public
issue — contact the maintainers privately instead.
