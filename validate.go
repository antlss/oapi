package oapi

import (
	"log"
	"reflect"
	"sync"
)

// Validator runs project-defined validation on a freshly bound request part. The
// library calls it after decoding each part (header, path, query, body), so a
// single rule source can drive runtime checks while the OpenAPI generator reads
// the same `binding` tags for the docs.
//
// source is the wire location of the part — one of "header", "uri", "form" or
// "json" — so an implementation can report field names the way clients send them
// (see [WireName]). value is a pointer to the bound struct.
//
// Return nil when value is valid. Return an error to abort the request: an error
// implementing [HTTPError] controls its own status and body, anything else is
// rendered as a generic 500. Build the library's standard field-level 400 with
// [NewValidationError].
//
// The core depends on no validation library and ships no Validator. Install one
// once at startup with [SetValidator]; the
// github.com/antlss/oapi/examples/playground sub-package provides the default,
// go-playground/validator-backed implementation that reads the `binding` tag.
type Validator interface {
	Validate(value any, source string) error
}

// RuleTag is the struct tag the library reads for validation rules. It is the
// single name shared by two readers: the OpenAPI generator (which turns the
// rules into schema constraints — required/enum/format/bounds) and the
// configured [Validator] (which enforces them at runtime). It defaults to
// "binding" (gin's convention); set it ONCE at startup to match your project —
// e.g. "validate" (the go-playground/validator default) or "validation" — before
// constructing routes and the validator. Source-location tags (header/uri/form/
// json) are separate and not affected.
var RuleTag = "binding"

// validation is configured once at startup and read on every request. It is not
// guarded by a lock: install the validator before serving (the documented
// contract), exactly like the rest of the library's process-wide configuration.
var (
	validatorImpl       Validator
	validatorConfigured bool
	noValidatorWarning  sync.Once
)

// SetValidator installs the process-wide [Validator] used to check every bound
// request. Call it once during startup, before serving requests. Passing nil
// disables validation explicitly (and silences the "no validator configured"
// warning), which is useful when a service validates elsewhere.
//
// It is not safe to call SetValidator concurrently with in-flight requests.
func SetValidator(v Validator) {
	validatorImpl = v
	validatorConfigured = true
}

// runValidation applies the configured validator to a freshly bound part. dst is
// the pointer the binder filled; validationTarget collapses any extra pointer
// level a generic Body type may add before the value reaches the validator. When
// no validator is configured the request is accepted without validation.
func runValidation(dst any, source string) error {
	if validatorImpl == nil {
		return nil
	}
	return validatorImpl.Validate(validationTarget(dst), source)
}

// warnIfNoValidator logs a single warning the first time a route that declares
// `binding` rules is served while no validator is configured, so the common
// "forgot to call SetValidator" mistake is loud instead of silently skipping
// every rule. SetValidator(nil) opts out explicitly and silences it.
func warnIfNoValidator(route Route) {
	if validatorConfigured || !route.hasRules {
		return
	}
	noValidatorWarning.Do(func() {
		log.Println("[oapi] routes declare `binding` rules but no Validator is configured, " +
			"so those rules are NOT enforced. Call oapi.SetValidator(...) at startup " +
			"(github.com/antlss/oapi/examples/playground provides the default), " +
			"or oapi.SetValidator(nil) to silence this warning.")
	})
}

// routeHasBindingRules reports whether any bound part of the route carries a
// `binding` tag, i.e. whether validation would have anything to enforce.
func routeHasBindingRules(route Route) bool {
	for _, t := range []reflect.Type{
		route.doc.schema.Header, route.doc.schema.Param, route.doc.schema.Query, route.doc.schema.Body,
	} {
		found := false
		rangeFields(t, func(field reflect.StructField) {
			if field.Tag.Get(RuleTag) != "" {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}
