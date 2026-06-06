package api

import "github.com/antlss/oapi"

// Handler holds the application's injected dependencies and exposes the route
// set and the OpenAPI registry built from those routes.
//
// Construct with NewHandler; the caller (main) provides concrete implementations.
// Swap any dependency for a test double in unit tests without touching main.
type Handler struct {
	catalog CatalogService
}

// NewHandler is the constructor — the one place where dependencies are wired.
// This is the composition seam: real services in main, mocks in tests.
func NewHandler(catalog CatalogService) *Handler {
	return &Handler{catalog: catalog}
}

// Routes returns every demo route in registration order.
func (h *Handler) Routes() []oapi.Route {
	return []oapi.Route{
		h.listProducts(), h.getProduct(), h.createProduct(),
		h.updateProduct(), h.patchProduct(), h.deleteProduct(),
		h.uploadImages(), h.downloadManual(),
		h.subscribe(), h.salesReport(), h.getAsset(),
		h.health(), h.catalogSummary(),
	}
}

// Registry builds the OpenAPI registry from this Handler's routes. Exercises
// every document-level builder: info, servers, security schemes, tags, tag groups.
func (h *Handler) Registry() *oapi.Registry {
	return oapi.NewRegistry("Catalog API", "v1").
		Describe("A demo API exercising every capability of the oapi library: typed binding, validation-driven docs, files, paging, security, typed middleware and the full error model.").
		Contact("Catalog API Team", "https://example.com/support", "api@example.com").
		License("Apache-2.0", "https://www.apache.org/licenses/LICENSE-2.0").
		TermsOfService("https://example.com/terms").
		ExternalDocs("Full developer guides", "https://docs.example.com").
		Logo("https://redocly.github.io/redoc/petstore-logo.png").
		AddServer("http://localhost:8080", "Local example server").
		AddServer("https://api.example.com", "Production").
		AddSecurityScheme("bearerAuth", oapi.BearerAuth()).
		AddSecurityScheme("apiKey", oapi.APIKeyAuth("header", "X-API-Key")).
		AddTag("catalog", "Browse, create and manage products").
		AddTag("account", "Subscriptions and account actions").
		AddTag("reports", "Reporting endpoints (some deprecated)").
		AddTag("assets", "Binary asset retrieval").
		AddTag("system", "Operational endpoints (health probe, raw response)").
		TagGroup("Commerce", "catalog", "account").
		TagGroup("Operations", "reports", "assets", "system").
		Add(h.Routes()...)
}
