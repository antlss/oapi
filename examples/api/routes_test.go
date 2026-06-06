package api_test

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antlss/oapi/examples/api"
)

// TestDocsValidate asserts the demo document built from the route schemas passes
// OpenAPI 3 validation — the same check the generator runs before writing files.
func TestDocsValidate(t *testing.T) {
	require.NoError(t, api.Registry().Validate(context.Background()))
}

// TestDocsAreLoadable proves the serialized spec is consumable by any standard
// OpenAPI tool: it loads the emitted JSON through kin-openapi's loader and
// re-validates the parsed document.
func TestDocsAreLoadable(t *testing.T) {
	raw, err := api.Registry().JSON()
	require.NoError(t, err)

	doc, err := openapi3.NewLoader().LoadFromData(raw)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(context.Background()))

	for _, path := range []string{
		"/products",
		"/products/{id}",
		"/products/{id}/images",
		"/products/{id}/manual",
		"/subscribe",
		"/reports/sales",
		"/assets/{path}",
	} {
		assert.NotNil(t, doc.Paths.Value(path), "missing path %s", path)
	}
}
