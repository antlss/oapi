// Package playground provides a go-playground/validator-backed [oapi.Validator].
//
// It lives in its own lean module on purpose: the go-playground dependency is a
// project choice, not part of the library. The core ships only the
// oapi.Validator seam (and no validation library), so this package doubles as a
// reference for wiring your own validator. Install it once at startup:
//
//	oapi.SetValidator(playground.New())
package playground

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/go-playground/validator/v10"

	"github.com/antlss/oapi"
)

// bindingSources are the wire locations the library validates. Each gets its own
// engine so reported field names match how clients send that part (a header by
// its header name, a JSON field by its json name, ...).
var bindingSources = []string{"header", "uri", "form", "json"}

// Validator is the go-playground-backed implementation of [oapi.Validator]. It
// is safe for concurrent use and is meant to be built once via [New].
type Validator struct {
	bySource map[string]*validator.Validate
}

// compile-time assurance the package satisfies the core seam.
var _ oapi.Validator = (*Validator)(nil)

// New builds the validator: one go-playground engine per binding source, each
// configured to read rules from the tag named by [oapi.RuleTag] (default
// "binding") and to report the source wire name (via [oapi.WireName]) for
// field-level errors. Set oapi.RuleTag before calling New if you use a different
// tag (e.g. "validate").
func New() *Validator {
	v := &Validator{bySource: make(map[string]*validator.Validate, len(bindingSources))}
	for _, source := range bindingSources {
		engine := validator.New(validator.WithRequiredStructEnabled())
		engine.SetTagName(oapi.RuleTag)
		engine.RegisterTagNameFunc(func(field reflect.StructField) string {
			return oapi.WireName(field, source)
		})
		v.bySource[source] = engine
	}
	return v
}

// Validate checks value (a pointer to a bound request part) against its rules,
// reporting field names using the wire names of the given source.
func (v *Validator) Validate(value any, source string) error {
	engine := v.bySource[source]
	if engine == nil {
		engine = v.bySource["json"]
	}
	if err := engine.Struct(value); err != nil {
		return translate(err)
	}
	return nil
}

// translate converts go-playground failures into the library's field-level 400.
// A non-ValidationErrors failure (e.g. an unsupported top-level body type such
// as a JSON array) is rendered as a generic 400 rather than leaking the internal
// validator message to the client.
func translate(err error) error {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return oapi.NewValidationError("request validation failed", nil)
	}
	fields := make([]oapi.FieldError, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, oapi.FieldError{
			Field:   fe.Field(),
			Rule:    fe.Tag(),
			Message: message(fe),
		})
	}
	return oapi.NewValidationError("request validation failed", fields)
}

// message renders a human-readable explanation for a single field failure.
func message(fe validator.FieldError) string {
	switch {
	case fe.Tag() == "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case fe.Param() != "":
		return fmt.Sprintf("%s failed the %q rule (%s)", fe.Field(), fe.Tag(), fe.Param())
	default:
		return fmt.Sprintf("%s failed the %q rule", fe.Field(), fe.Tag())
	}
}
