// Package api is the demo "Catalog API": one framework-agnostic route set that
// exercises every capability of the oapi library. The same routes are mounted on
// gin, net/http and Fiber by the sibling example commands and are the input to
// cmd/openapi-gen, so this package doubles as living, runnable documentation.
//
// Each route below notes the features it demonstrates. Together they cover:
// typed binding of every request part (header/path/query/body); JSON, urlencoded
// and multipart bodies incl. file uploads; validation-driven docs (required,
// enum, format, bounds) including nested structs; binary file downloads; the
// default {data}/{error}/{meta} envelope with paging and custom headers, PLUS the
// pluggable envelope seam — a per-route custom envelope (WithEnvelope +
// KeyedEnvelope) and a raw, un-enveloped response (WithRawResponse); both route
// kinds (NewRoute, NewRichRoute); typed middleware with context injection; every
// route option (summary/description/tags/deprecated/response types/multi-response/
// security/error-mapper/success-status/envelope); the full error model (HTTPError,
// custom ErrorBody, aerror-shaped, per-route ErrorMapper, field-level validation),
// plus a process-wide ErrorParser + custom envelope demonstrated end-to-end in
// cmd/customized; and a Registry with multiple servers and security schemes.
package api

//go:generate go run ../cmd/openapi-gen -out ../openapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/antlss/oapi"
)

// listProducts — GET with a rich query: embedded Pagination, an enum sort and
// category, an optional bool and a time range. NewRichRoute lets the handler
// build the envelope itself (here adding pagination meta); WithResponseType
// keeps the docs accurate for the array payload.
func (h *Handler) listProducts() oapi.Route {
	return oapi.NewRichRoute(
		http.MethodGet, "/products",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, ListQuery, struct{}]) (*oapi.Result, error) {
			page, perPage := req.Query.Page, req.Query.PerPage
			if page == 0 {
				page = 1
			}
			if perPage == 0 {
				perPage = 20
			}
			items := h.catalog.ListProducts()
			return oapi.NewListDataResult(items, int64(len(items)), perPage, page), nil
		},
		oapi.WithSummary("List products"),
		oapi.WithDescription("Search and page the catalog. Shows embedded query structs, enum/bound parameters and pagination meta."),
		oapi.WithTags("catalog"),
		oapi.WithResponseType[[]Product](),
		oapi.WithMetaType[oapi.PagingMeta](),
	)
}

// getProduct — GET one by path id. Uses NewRichRoute to attach response headers
// (ETag, Cache-Control) and custom meta, documents a 404, and returns an
// aerror-shaped error (duck-typed, no library import) when the id is missing.
func (h *Handler) getProduct() oapi.Route {
	return oapi.NewRichRoute(
		http.MethodGet, "/products/:id",
		func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, struct{}]) (*oapi.Result, error) {
			p, ok := h.catalog.GetProduct(req.Param.ID)
			if !ok {
				return nil, notFoundAPIError(req.Param.ID)
			}
			etag := fmt.Sprintf(`"v-%d"`, p.ID)
			return oapi.NewDataResult(p).
				WithHeader("ETag", etag).
				WithHeader("Cache-Control", "private, max-age=60").
				WithMeta(map[string]any{"etag": etag}), nil
		},
		oapi.WithSummary("Get a product"),
		oapi.WithTags("catalog"),
		oapi.WithResponseType[Product](),
		oapi.WithResponse[struct{}](http.StatusNotFound, "Product not found"),
	)
}

// createProduct — POST JSON. Shows a validated nested body (uuid/email/uri
// formats, enum, numeric & array bounds, required nested fields), a 201 Created
// with a Location header, a bearer-scope security requirement, documented 409/422
// responses, and a custom HTTPError (demoError) with its own JSON body.
func (h *Handler) createProduct() oapi.Route {
	return oapi.NewRichRoute(
		http.MethodPost, "/products",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, CreateProductBody]) (*oapi.Result, error) {
			if req.Body.SKU == "00000000-0000-0000-0000-000000000000" {
				return nil, demoError{status: http.StatusConflict, code: "duplicate_sku", message: "a product with this SKU already exists"}
			}
			p := h.catalog.CreateProduct(req.Body)
			return oapi.NewDataResult(p).
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
}

// updateProduct — PUT with a typed auth middleware (validates the bearer header
// and injects the user), a custom ErrorMapper translating domain sentinel errors,
// and bearer security. NewRoute wraps the returned *Product in the {data}
// envelope automatically.
func (h *Handler) updateProduct() oapi.Route {
	return oapi.NewRoute(
		http.MethodPut, "/products/:id",
		func(ctx context.Context, req oapi.Request[AuthHeader, ProductURI, struct{}, UpdateProductBody]) (*Product, error) {
			if _, ok := userFrom(ctx); !ok {
				return nil, oapi.NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
			}
			if req.Param.ID == 409 { // demo trigger for the ErrorMapper
				return nil, errProductConflict
			}
			p := h.catalog.UpdateProduct(req.Param.ID, req.Body)
			return &p, nil
		},
		oapi.WithSummary("Replace a product"),
		oapi.WithTags("catalog"),
		oapi.WithSecurity("bearerAuth", "products:write"),
		oapi.WithErrorMapper(productErrorMapper),
		oapi.WithResponse[struct{}](http.StatusUnauthorized, "Missing or invalid token"),
		oapi.WithResponse[struct{}](http.StatusConflict, "Version conflict"),
		oapi.WithTypedBefore(requireAuth),
	)
}

// patchProduct — PATCH with a partial (pointer-field) body: every field is
// optional, and only the ones present are validated and applied.
func (h *Handler) patchProduct() oapi.Route {
	return oapi.NewRoute(
		http.MethodPatch, "/products/:id",
		func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, PatchProductBody]) (*Product, error) {
			p := h.catalog.PatchProduct(req.Param.ID, req.Body)
			return &p, nil
		},
		oapi.WithSummary("Partially update a product"),
		oapi.WithTags("catalog"),
	)
}

// deleteProduct — DELETE behind the typed auth middleware. Returning nil yields
// 204 No Content (no response body).
func (h *Handler) deleteProduct() oapi.Route {
	return oapi.NewRoute(
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
}

// uploadImages — multipart upload of several files plus a caption. The file
// field makes the binder pick the multipart decoder and the docs emit
// multipart/form-data.
func (h *Handler) uploadImages() oapi.Route {
	return oapi.NewRoute(
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
}

// downloadManual — a binary file download. NewResult([]byte).WithFile sets a safe
// Content-Disposition and streams the bytes (no {data} envelope). WithBinaryResponse
// documents the 200 response as an application/pdf stream so the docs match the
// bytes on the wire instead of defaulting to 204 No Content.
func (h *Handler) downloadManual() oapi.Route {
	return oapi.NewRichRoute(
		http.MethodGet, "/products/:id/manual",
		func(_ context.Context, req oapi.Request[struct{}, ProductURI, struct{}, struct{}]) (*oapi.Result, error) {
			pdf := h.catalog.ManualPDF(req.Param.ID)
			return oapi.NewResult(pdf).WithFile(fmt.Sprintf("manual-%d.pdf", req.Param.ID)), nil
		},
		oapi.WithSummary("Download the product manual (binary)"),
		oapi.WithTags("catalog"),
		oapi.WithBinaryResponse("application/pdf", "The product manual as a PDF file"),
	)
}

// subscribe — an application/x-www-form-urlencoded body (only `form` tags, no
// file or json), validated with email + enum rules.
func (h *Handler) subscribe() oapi.Route {
	return oapi.NewRoute(
		http.MethodPost, "/subscribe",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, SubscribeBody]) (*SubscribeResult, error) {
			return &SubscribeResult{Email: req.Body.Email, Plan: req.Body.Plan, Status: "subscribed"}, nil
		},
		oapi.WithSummary("Subscribe to a plan (urlencoded form)"),
		oapi.WithTags("account"),
	)
}

// salesReport — a deprecated endpoint with time.Time query parameters.
func (h *Handler) salesReport() oapi.Route {
	return oapi.NewRoute(
		http.MethodGet, "/reports/sales",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, ReportQuery, struct{}]) (*ReportResult, error) {
			return &ReportResult{From: req.Query.From, To: req.Query.To, Total: 42}, nil
		},
		oapi.WithSummary("Sales report"),
		oapi.WithDescription("Superseded by the analytics service; kept for backward compatibility."),
		oapi.WithTags("reports"),
		oapi.WithDeprecated(),
	)
}

// getAsset — a catch-all path (`*path`) secured by an API-key header, showing the
// second security scheme and required header binding.
func (h *Handler) getAsset() oapi.Route {
	return oapi.NewRoute(
		http.MethodGet, "/assets/*path",
		func(_ context.Context, req oapi.Request[APIKeyHeader, AssetURI, struct{}, struct{}]) (*AssetResult, error) {
			return &AssetResult{Path: req.Param.Path}, nil
		},
		oapi.WithSummary("Fetch an asset by path"),
		oapi.WithTags("assets"),
		oapi.WithSecurity("apiKey"),
	)
}

// health — a raw (un-enveloped) response. WithRawResponse drops the {data}
// wrapper so the body IS the HealthStatus model, and the docs describe it the same
// way. Useful for probes and any endpoint a project wants returned verbatim.
func (h *Handler) health() oapi.Route {
	return oapi.NewRoute(
		http.MethodGet, "/health",
		func(_ context.Context, _ oapi.Request[struct{}, struct{}, struct{}, struct{}]) (*HealthStatus, error) {
			return &HealthStatus{Status: "ok", Version: "v1"}, nil
		},
		oapi.WithSummary("Health check (raw response)"),
		oapi.WithDescription("Returns the HealthStatus model with no envelope, via WithRawResponse()."),
		oapi.WithTags("system"),
		oapi.WithRawResponse(),
	)
}

// catalogSummary — a per-route custom envelope. WithEnvelope wraps the
// payload in a project-specific {"result": ..., "success": true} shape; the
// KeyedEnvelope drives both the wire body and the generated schema, so they cannot
// drift. Other routes keep the default {data} envelope.
func (h *Handler) catalogSummary() oapi.Route {
	return oapi.NewRoute(
		http.MethodGet, "/catalog/summary",
		func(_ context.Context, _ oapi.Request[struct{}, struct{}, struct{}, struct{}]) (*CatalogSummary, error) {
			s := h.catalog.Summary()
			return &s, nil
		},
		oapi.WithSummary("Catalog summary (custom envelope)"),
		oapi.WithDescription("Wraps the payload in a {result, success} envelope via WithEnvelope(KeyedEnvelope{...})."),
		oapi.WithTags("catalog"),
		oapi.WithEnvelope(oapi.KeyedEnvelope{
			DataKey:   "result",
			Constants: map[string]any{"success": true},
		}),
	)
}
