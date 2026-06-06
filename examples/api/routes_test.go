package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antlss/oapi/examples/api"
)

// newHandler wires the real in-memory service — the same composition used by the
// example commands, available here as a one-liner.
func newHandler() *api.Handler {
	return api.NewHandler(api.NewCatalogService())
}

// TestDocsValidate asserts the demo document built from the route schemas passes
// OpenAPI 3 validation — the same check the generator runs before writing files.
func TestDocsValidate(t *testing.T) {
	require.NoError(t, newHandler().Registry().Validate(context.Background()))
}

// TestDocsAreLoadable proves the serialized spec is consumable by any standard
// OpenAPI tool: it loads the emitted JSON through kin-openapi's loader and
// re-validates the parsed document.
func TestDocsAreLoadable(t *testing.T) {
	raw, err := newHandler().Registry().JSON()
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
		"/health",
		"/catalog/summary",
	} {
		assert.NotNil(t, doc.Paths.Value(path), "missing path %s", path)
	}
}

// TestRawAndCustomEnvelopeDocs asserts the per-route envelope overrides are
// reflected in the generated schema: /health is raw (its model, no data wrapper)
// and /catalog/summary uses the custom {result, success} envelope.
func TestRawAndCustomEnvelopeDocs(t *testing.T) {
	props := func(t *testing.T, doc *openapi3.T, path string) openapi3.Schemas {
		t.Helper()
		op := doc.Paths.Value(path).Get
		require.NotNil(t, op, "missing GET %s", path)
		mt := op.Responses.Status(http.StatusOK).Value.Content.Get("application/json")
		require.NotNil(t, mt, "missing 200 application/json for %s", path)
		return mt.Schema.Value.Properties
	}

	doc := newHandler().Registry().OpenAPI()

	health := props(t, doc, "/health")
	assert.NotContains(t, health, "data", "/health should be raw (no data wrapper)")
	assert.Contains(t, health, "status", "/health should document the raw model")

	summary := props(t, doc, "/catalog/summary")
	assert.NotContains(t, summary, "data", "/catalog/summary uses a custom data key")
	assert.Contains(t, summary, "result", "/catalog/summary should use the result key")
	assert.Contains(t, summary, "success", "/catalog/summary should include the success constant")
}
