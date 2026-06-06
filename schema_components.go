package oapi

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
)

// componentRefPath is the JSON-pointer prefix every component schema $ref uses.
const componentRefPath = "#/components/schemas/"

// schemaSet collects reusable component schemas during OpenAPI generation. When a
// Registry opts in (Registry.UseComponents), a named struct used as a response,
// data, meta or documented-error type is registered here once and referenced by
// $ref everywhere it appears, so a type shared across endpoints is described a
// single time instead of being inlined repeatedly.
//
// A nil *schemaSet means "inline everything" — the default. Every method is
// nil-safe so the schema generators can pass the set through unconditionally and
// fall back to inlining when it is nil, keeping the default output byte-identical.
type schemaSet struct {
	schemas openapi3.Schemas        // component name -> schema
	names   map[reflect.Type]string // type -> chosen component name (memo)
	byName  map[string]reflect.Type // component name -> owning type (collision guard)
}

// newSchemaSet builds an empty set ready to collect components.
func newSchemaSet() *schemaSet {
	return &schemaSet{
		schemas: openapi3.Schemas{},
		names:   map[reflect.Type]string{},
		byName:  map[string]reflect.Type{},
	}
}

// componentRef returns a $ref pointing at t's component schema, registering that
// schema (built once) on first sight. It returns nil — telling the caller to
// inline instead — when the set is nil, or t is not a nameable struct (an
// anonymous struct, a non-struct, or time.Time, which is documented as a
// date-time string rather than an object).
func (s *schemaSet) componentRef(t reflect.Type) *openapi3.SchemaRef {
	if s == nil {
		return nil
	}
	dt := deref(t)
	if dt == nil || dt.Kind() != reflect.Struct || dt == timeType || dt.Name() == "" {
		return nil
	}
	name := s.nameFor(dt)
	if _, ok := s.schemas[name]; !ok {
		// Register a placeholder under the name first so a self-referential type
		// resolves to this $ref instead of recursing while its schema is built.
		s.schemas[name] = openapi3.NewSchemaRef("", openapi3.NewObjectSchema())
		s.schemas[name] = openapi3.NewSchemaRef("", buildTypeSchema(dt))
	}
	return openapi3.NewSchemaRef(componentRefPath+name, nil)
}

// nameFor picks a stable, collision-free component name for t: its bare type name,
// qualified with the package's last path segment if a different type already owns
// that name, and finally numbered if even that collides.
func (s *schemaSet) nameFor(t reflect.Type) string {
	if n, ok := s.names[t]; ok {
		return n
	}
	name := sanitizeComponentName(t.Name())
	if owner, taken := s.byName[name]; taken && owner != t {
		qualified := sanitizeComponentName(pkgPrefix(t) + t.Name())
		name = qualified
		for i := 2; ; i++ {
			owner, taken := s.byName[name]
			if !taken || owner == t {
				break
			}
			name = qualified + strconv.Itoa(i)
		}
	}
	s.names[t] = name
	s.byName[name] = t
	return name
}

// empty reports whether the set collected no components (so the document needs no
// components/schemas section).
func (s *schemaSet) empty() bool { return s == nil || len(s.schemas) == 0 }

// pkgPrefix returns the last segment of t's import path followed by "_", used to
// disambiguate two same-named types from different packages. It is "" for types
// with no package path.
func pkgPrefix(t reflect.Type) string {
	p := t.PkgPath()
	if p == "" {
		return ""
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p + "_"
}

// sanitizeComponentName keeps only the characters OpenAPI allows in a component
// key (^[a-zA-Z0-9._-]+$), replacing anything else with '_'. An empty result
// falls back to "Schema".
func sanitizeComponentName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "Schema"
	}
	return b.String()
}

// buildTypeSchema builds the inline (component-body) schema for a single Go type:
// openapi3gen for the structure, de-aliased so shared *Schema instances do not
// bleed, then enriched with example/description metadata (no binding overlay — a
// response/component carries no input contract). Nested named structs stay inline
// within the component; only the top-level type passed to typeSchemaRef becomes a
// component.
func buildTypeSchema(dt reflect.Type) *openapi3.Schema {
	if dt == nil {
		return openapi3.NewObjectSchema()
	}
	ref, err := openapi3gen.NewSchemaRefForValue(reflect.New(dt).Elem().Interface(), nil)
	if err != nil || ref.Value == nil {
		return openapi3.NewObjectSchema()
	}
	s := deAliasSchema(ref.Value)
	enrichNestedSchema(s, dt, false)
	return s
}
