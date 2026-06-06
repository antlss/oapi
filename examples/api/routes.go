// Package api is the demo "Catalog API": one framework-agnostic route set that
// exercises every capability of the oapi library. The same routes are mounted on
// gin, net/http and Fiber by the sibling example commands and are the input to
// cmd/openapi-gen, so this package doubles as living, runnable documentation.
//
// Each route below notes the features it demonstrates. Together they cover:
// typed binding of every request part (header/path/query/body); JSON, urlencoded
// and multipart bodies incl. file uploads; validation-driven docs (required,
// enum, format, bounds) including nested structs; binary file downloads; the
// {data}/{error}/{meta} envelope with paging and custom headers; both route
// kinds (NewRoute, NewRichRoute); typed middleware with context injection; every
// route option (summary/description/tags/deprecated/response types/multi-response/
// security/error-mapper/success-status); the full error model (HTTPError,
// custom ErrorBody, aerror-shaped, ErrorMapper, field-level validation); and a
// Registry with multiple servers and security schemes.
package api

//go:generate go run ../cmd/openapi-gen -out ../openapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/antlss/oapi"
)

// ListProducts — GET with a rich query: embedded Pagination, an enum sort and
// category, an optional bool and a time range. NewRichRoute lets the handler
// build the envelope itself (here adding pagination meta); WithResponseType
// keeps the docs accurate for the array payload.
var ListProducts = oapi.NewRichRoute(
	http.MethodGet, "/products",
	func(_ context.Context, req oapi.Request[struct{}, struct{}, ListQuery, struct{}]) (*oapi.Result, error) {
		page, perPage := req.Query.Page, req.Query.PerPage
		if page == 0 {
			page = 1
		}
		if perPage == 0 {
			perPage = 20
		}
		items := sampleProducts()
		return oapi.NewResult(items).WithPaging(int64(len(items)), perPage, page), nil
	},
	oapi.WithSummary("List products"),
	oapi.WithDescription("Search and page the catalog. Shows embedded query structs, enum/bound parameters and pagination meta."),
	oapi.WithTags("catalog"),
	oapi.WithResponseType[[]Product](),
	oapi.WithMetaType[oapi.PagingMeta](), // documents the {data, meta} success envelope
)

// GetProduct — GET one by path id. Uses NewRichRoute to attach response headers
// (ETag, Cache-Control) and custom meta, documents a 404, and returns an
// aerror-shaped error (duck-typed, no library import) when the id is missing.
var GetProduct = oapi.NewRichRoute(
	http.MethodGet, "/products/:id",
	func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, struct{}]) (*oapi.Result, error) {
		if req.Param.ID == 0 {
			return nil, notFoundAPIError(req.Param.ID)
		}
		p := sampleProduct(req.Param.ID)
		etag := fmt.Sprintf(`"v-%d"`, p.ID)
		return oapi.NewResult(p).
			WithHeader("ETag", etag).
			WithHeader("Cache-Control", "private, max-age=60").
			WithMeta(map[string]any{"etag": etag}), nil
	},
	oapi.WithSummary("Get a product"),
	oapi.WithTags("catalog"),
	oapi.WithResponseType[Product](),
	oapi.WithResponse[struct{}](http.StatusNotFound, "Product not found"),
)

// CreateProduct — POST JSON. Shows a validated nested body (uuid/email/uri
// formats, enum, numeric & array bounds, required nested fields), a 201 Created
// with a Location header, a bearer-scope security requirement, documented 409/422
// responses, and a custom HTTPError (demoError) with its own JSON body.
var CreateProduct = oapi.NewRichRoute(
	http.MethodPost, "/products",
	func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, CreateProductBody]) (*oapi.Result, error) {
		if req.Body.SKU == "00000000-0000-0000-0000-000000000000" {
			return nil, demoError{status: http.StatusConflict, code: "duplicate_sku", message: "a product with this SKU already exists"}
		}
		p := Product{
			ID: 1001, Name: req.Body.Name, SKU: req.Body.SKU, Price: req.Body.Price,
			Currency: req.Body.Currency, Category: req.Body.Category, Tags: req.Body.Tags,
			Warehouse: req.Body.Warehouse, InStock: true, CreatedAt: sampleTime(),
		}
		return oapi.NewResult(p).
			WithStatus(http.StatusCreated).
			WithHeader("Location", fmt.Sprintf("/products/%d", p.ID)), nil
	},
	oapi.WithSummary("Create a product"),
	oapi.WithTags("catalog"),
	oapi.WithSuccessStatus(http.StatusCreated),
	oapi.WithResponseType[Product](),
	oapi.WithSecurity("bearerAuth", "products:write"),
	oapi.WithResponse[struct{}](http.StatusConflict, "Duplicate SKU"),
	oapi.WithResponse[struct{}](http.StatusUnprocessableEntity, "Validation failed"),
)

// UpdateProduct — PUT with a typed auth middleware (validates the bearer header
// and injects the user), a custom ErrorMapper translating domain sentinel errors,
// and bearer security. NewRoute wraps the returned *Product in the {data}
// envelope automatically.
var UpdateProduct = oapi.NewRoute(
	http.MethodPut, "/products/:id",
	func(ctx context.Context, req oapi.Request[AuthHeader, ProductURI, struct{}, UpdateProductBody]) (*Product, error) {
		if _, ok := userFrom(ctx); !ok {
			return nil, oapi.NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
		}
		if req.Param.ID == 409 { // demo trigger for the ErrorMapper
			return nil, errProductConflict
		}
		return &Product{
			ID: req.Param.ID, Name: req.Body.Name, Price: req.Body.Price,
			Currency: req.Body.Currency, InStock: req.Body.InStock, CreatedAt: sampleTime(),
		}, nil
	},
	oapi.WithSummary("Replace a product"),
	oapi.WithTags("catalog"),
	oapi.WithSecurity("bearerAuth", "products:write"),
	oapi.WithErrorMapper(productErrorMapper),
	oapi.WithResponse[struct{}](http.StatusUnauthorized, "Missing or invalid token"),
	oapi.WithResponse[struct{}](http.StatusConflict, "Version conflict"),
	oapi.WithTypedBefore(requireAuth),
)

// PatchProduct — PATCH with a partial (pointer-field) body: every field is
// optional, and only the ones present are validated and applied.
var PatchProduct = oapi.NewRoute(
	http.MethodPatch, "/products/:id",
	func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, PatchProductBody]) (*Product, error) {
		p := sampleProduct(req.Param.ID)
		if req.Body.Name != nil {
			p.Name = *req.Body.Name
		}
		if req.Body.Price != nil {
			p.Price = *req.Body.Price
		}
		if req.Body.InStock != nil {
			p.InStock = *req.Body.InStock
		}
		return &p, nil
	},
	oapi.WithSummary("Partially update a product"),
	oapi.WithTags("catalog"),
)

// DeleteProduct — DELETE behind the typed auth middleware. Returning nil yields
// 204 No Content (no response body).
var DeleteProduct = oapi.NewRoute(
	http.MethodDelete, "/products/:id",
	func(_ context.Context, _ oapi.Request[AuthHeader, ProductURI, struct{}, struct{}]) (*struct{}, error) {
		return nil, nil
	},
	oapi.WithSummary("Delete a product"),
	oapi.WithTags("catalog"),
	oapi.WithSecurity("bearerAuth", "products:write"),
	oapi.WithResponse[struct{}](http.StatusUnauthorized, "Missing or invalid token"),
	oapi.WithTypedBefore(requireAuth),
)

// UploadImages — multipart upload of several files plus a caption. The file
// field makes the binder pick the multipart decoder and the docs emit
// multipart/form-data.
var UploadImages = oapi.NewRoute(
	http.MethodPost, "/products/:id/images",
	func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, UploadImagesBody]) (*UploadResult, error) {
		names := make([]string, 0, len(req.Body.Files))
		var total int64
		for _, f := range req.Body.Files {
			names = append(names, f.Filename)
			total += f.Size
		}
		return &UploadResult{ProductID: req.Param.ID, Files: names, TotalBytes: total, Caption: req.Body.Caption}, nil
	},
	oapi.WithSummary("Upload product images"),
	oapi.WithTags("catalog"),
)

// DownloadManual — a binary file download. NewResult([]byte).WithFile sets a safe
// Content-Disposition and streams the bytes (no {data} envelope).
var DownloadManual = oapi.NewRichRoute(
	http.MethodGet, "/products/:id/manual",
	func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, struct{}]) (*oapi.Result, error) {
		pdf := []byte("%PDF-1.4\n% demo manual for product " + fmt.Sprint(req.Param.ID) + "\n")
		return oapi.NewResult(pdf).WithFile(fmt.Sprintf("manual-%d.pdf", req.Param.ID)), nil
	},
	oapi.WithSummary("Download the product manual (binary)"),
	oapi.WithTags("catalog"),
)

// Subscribe — an application/x-www-form-urlencoded body (only `form` tags, no
// file or json), validated with email + enum rules.
var Subscribe = oapi.NewRoute(
	http.MethodPost, "/subscribe",
	func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, SubscribeBody]) (*SubscribeResult, error) {
		return &SubscribeResult{Email: req.Body.Email, Plan: req.Body.Plan, Status: "subscribed"}, nil
	},
	oapi.WithSummary("Subscribe to a plan (urlencoded form)"),
	oapi.WithTags("account"),
)

// SalesReport — a deprecated endpoint with time.Time query parameters.
var SalesReport = oapi.NewRoute(
	http.MethodGet, "/reports/sales",
	func(_ context.Context, req oapi.Request[struct{}, struct{}, ReportQuery, struct{}]) (*ReportResult, error) {
		return &ReportResult{From: req.Query.From, To: req.Query.To, Total: 42}, nil
	},
	oapi.WithSummary("Sales report"),
	oapi.WithDescription("Superseded by the analytics service; kept for backward compatibility."),
	oapi.WithTags("reports"),
	oapi.WithDeprecated(),
)

// GetAsset — a catch-all path (`*path`) secured by an API-key header, showing the
// second security scheme and required header binding.
var GetAsset = oapi.NewRoute(
	http.MethodGet, "/assets/*path",
	func(_ context.Context, req oapi.Request[APIKeyHeader, AssetURI, struct{}, struct{}]) (*AssetResult, error) {
		return &AssetResult{Path: req.Param.Path}, nil
	},
	oapi.WithSummary("Fetch an asset by path"),
	oapi.WithTags("assets"),
	oapi.WithSecurity("apiKey"),
)

// Routes returns every demo route in registration order.
func Routes() []oapi.Route {
	return []oapi.Route{
		ListProducts, GetProduct, CreateProduct, UpdateProduct, PatchProduct,
		DeleteProduct, UploadImages, DownloadManual, Subscribe, SalesReport, GetAsset,
	}
}

// Registry collects the demo routes into an oapi.Registry with full
// document-level metadata: API info (description, contact, license, terms,
// external docs, a Redoc logo), two servers, two security schemes (bearer + API
// key), described/ordered tags and Redoc tag groups. It is ready to emit an
// OpenAPI document and exercises every document-level builder.
//
// All of the above can alternatively be supplied (or augmented) by a base
// document — see examples/openapi/common.json and cmd/openapi-gen's -base flag.
func Registry() *oapi.Registry {
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
		TagGroup("Commerce", "catalog", "account").
		TagGroup("Operations", "reports", "assets").
		Add(Routes()...)
}
