package api

import (
	"mime/multipart"
	"time"
)

// This file holds every request/response shape the demo uses. The struct tags
// are the single source of truth: `header`/`uri`/`form`/`json` drive binding,
// `binding` drives validation + the schema constraints (required/enum/format/
// bounds), and `example` drives the sample values shown in Swagger UI / Redoc
// (instead of bare "string"/0 type placeholders).

// ---------------- Domain models (also the response bodies) ----------------

// Address is a nested object reused in both requests and responses. Its
// `binding` rules and `example` values surface in the docs even when nested,
// because the schema generator recurses into named nested structs.
type Address struct {
	Line1   string `json:"line1"   binding:"required"        example:"1-2-3 Shibuya"`
	City    string `json:"city"    binding:"required"        example:"Tokyo"`
	Country string `json:"country" binding:"required,len=2"  example:"JP"` // ISO 3166-1 alpha-2
}

// Product is the catalog resource returned by most endpoints.
type Product struct {
	ID        int       `json:"id"         example:"1001"`
	Name      string    `json:"name"       example:"Mechanical Keyboard"`
	SKU       string    `json:"sku"        example:"5f9c2e3a-1b4d-4c8e-9f0a-2b3c4d5e6f70"`
	Price     float64   `json:"price"      example:"49.90"`
	Currency  string    `json:"currency"   example:"USD"`
	Category  string    `json:"category"   example:"electronics"`
	InStock   bool      `json:"in_stock"   example:"true"`
	Tags      []string  `json:"tags,omitempty" example:"new,featured"`
	Warehouse Address   `json:"warehouse"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-02T15:04:05Z"`
}

// User is the authenticated principal that the typed auth middleware injects
// into the request context.
type User struct {
	ID    string
	Roles []string
}

// ---------------- Request parts (bound from header/uri/form/json) ----------------

// ProductURI is the path-parameter struct shared by the /products/{id} routes.
type ProductURI struct {
	ID int `uri:"id" example:"1001"`
}

// Pagination is embedded into list queries to show that embedded structs are
// bound and documented exactly like inline fields.
type Pagination struct {
	Page    int `form:"page"     binding:"omitempty,min=1"         example:"1"`
	PerPage int `form:"per_page" binding:"omitempty,min=1,max=100" example:"20"`
}

// ListQuery demonstrates an embedded struct, an enum (oneof), a string bound, an
// optional boolean and time-range query parameters.
type ListQuery struct {
	Pagination
	Sort     string    `form:"sort"         binding:"omitempty,oneof=name -name price -price created_at" example:"-price"`
	Q        string    `form:"q"            binding:"omitempty,max=100"                                  example:"keyboard"`
	Category string    `form:"category"     binding:"omitempty,oneof=book electronics food toy"          example:"electronics"`
	InStock  *bool     `form:"in_stock"     example:"true"`
	From     time.Time `form:"created_from" example:"2026-01-01T00:00:00Z"`
	To       time.Time `form:"created_to"   example:"2026-12-31T23:59:59Z"`
}

// CreateProductBody is a JSON body showing required fields, string formats
// (uuid/email/uri), an enum, numeric/array bounds and a nested object whose own
// rules and examples also reach the docs.
type CreateProductBody struct {
	Name      string   `json:"name"      binding:"required,min=2,max=120"        example:"Mechanical Keyboard"`
	SKU       string   `json:"sku"       binding:"required,uuid"                 example:"5f9c2e3a-1b4d-4c8e-9f0a-2b3c4d5e6f70"`
	Price     float64  `json:"price"     binding:"required,gt=0"                 example:"49.90"`
	Currency  string   `json:"currency"  binding:"required,oneof=USD EUR JPY VND" example:"USD"`
	Category  string   `json:"category"  binding:"required,oneof=book electronics food toy" example:"electronics"`
	Contact   string   `json:"contact"   binding:"omitempty,email"               example:"sales@example.com"`
	Website   string   `json:"website"   binding:"omitempty,url"                 example:"https://example.com"`
	Tags      []string `json:"tags"      binding:"omitempty,max=10"              example:"new,featured"`
	Warehouse Address  `json:"warehouse"`
}

// UpdateProductBody is a full-replacement (PUT) body.
type UpdateProductBody struct {
	Name     string  `json:"name"     binding:"required,min=2,max=120"          example:"Mechanical Keyboard v2"`
	Price    float64 `json:"price"    binding:"required,gt=0"                   example:"54.90"`
	Currency string  `json:"currency" binding:"required,oneof=USD EUR JPY VND"  example:"USD"`
	InStock  bool    `json:"in_stock" example:"false"`
}

// PatchProductBody is a partial (PATCH) body: pointer fields make every value
// optional while still validating the ones that are present.
type PatchProductBody struct {
	Name    *string  `json:"name,omitempty"     binding:"omitempty,min=2,max=120" example:"Renamed Keyboard"`
	Price   *float64 `json:"price,omitempty"    binding:"omitempty,gt=0"          example:"39.90"`
	InStock *bool    `json:"in_stock,omitempty" example:"true"`
}

// UploadImagesBody is a multipart body: a required slice of files plus a text
// field. The file field makes the binder pick the multipart decoder and the docs
// emit multipart/form-data.
type UploadImagesBody struct {
	Files   []*multipart.FileHeader `form:"files"   binding:"required"`
	Caption string                  `form:"caption" binding:"omitempty,max=200" example:"front and back"`
}

// SubscribeBody has only `form` tags and no file, so it is bound (and documented)
// as application/x-www-form-urlencoded.
type SubscribeBody struct {
	Email string `form:"email" binding:"required,email"                example:"user@example.com"`
	Plan  string `form:"plan"  binding:"required,oneof=free pro enterprise" example:"pro"`
}

// AuthHeader is consumed by the typed auth middleware.
type AuthHeader struct {
	Token string `header:"Authorization" binding:"required" example:"Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig"`
}

// APIKeyHeader secures the asset endpoint with an API-key header.
type APIKeyHeader struct {
	Key string `header:"X-API-Key" binding:"required" example:"sk_live_8f3a1c"`
}

// AssetURI captures a catch-all path segment (`*path`).
type AssetURI struct {
	Path string `uri:"path" example:"images/logo.png"`
}

// ReportQuery shows time.Time query parameters.
type ReportQuery struct {
	From time.Time `form:"from" binding:"required" example:"2026-01-01T00:00:00Z"`
	To   time.Time `form:"to"   example:"2026-02-01T00:00:00Z"`
}

// ---------------- Response bodies for the non-Product endpoints ----------------

// UploadResult is returned after a multipart upload.
type UploadResult struct {
	ProductID  int      `json:"product_id"        example:"1001"`
	Files      []string `json:"files"             example:"front.png,back.png"`
	TotalBytes int64    `json:"total_bytes"       example:"204800"`
	Caption    string   `json:"caption,omitempty" example:"front and back"`
}

// SubscribeResult is returned by the urlencoded subscribe endpoint.
type SubscribeResult struct {
	Email  string `json:"email"  example:"user@example.com"`
	Plan   string `json:"plan"   example:"pro"`
	Status string `json:"status" example:"subscribed"`
}

// ReportResult is returned by the (deprecated) sales report.
type ReportResult struct {
	From  time.Time `json:"from"  example:"2026-01-01T00:00:00Z"`
	To    time.Time `json:"to"    example:"2026-02-01T00:00:00Z"`
	Total int       `json:"total" example:"42"`
}

// AssetResult is returned by the catch-all asset endpoint.
type AssetResult struct {
	Path string `json:"path" example:"images/logo.png"`
}
