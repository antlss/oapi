// Package oapi turns typed Go handlers into HTTP endpoints whose request and
// response structs are the single source of truth for binding, validation and
// OpenAPI 3 documentation.
//
// The core is framework-agnostic: handlers, middleware, the response envelope
// and the OpenAPI generator all operate over the small [Carrier] seam, never a
// concrete web framework. It depends only on what every route needs (binding +
// OpenAPI generation) and on no validation library; each transport lives in its
// own module so a consumer pulls in only the one it imports:
//
//   - github.com/antlss/oapi/adapter/nethttp — net/http (ships with the core, no
//     extra dependencies)
//   - github.com/antlss/oapi/adapter/gin — gin
//   - github.com/antlss/oapi/adapter/fiber — Fiber v2
//
// Validation is a pluggable seam ([Validator] + [SetValidator]); a ready
// go-playground/validator implementation lives in its own lean module
// (github.com/antlss/oapi/validation/playground) as a reference, so the library
// ships no validator.
package oapi
