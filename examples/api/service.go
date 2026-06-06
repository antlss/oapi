package api

import (
	"fmt"
	"time"
)

// CatalogService is the business-layer contract the Handler depends on.
// The interface is defined here (consumer side) — the Go idiom: accept interfaces
// where consumed, not where implemented. Swap the implementation in tests.
type CatalogService interface {
	ListProducts() []Product
	GetProduct(id int) (Product, bool)
	CreateProduct(body CreateProductBody) Product
	UpdateProduct(id int, body UpdateProductBody) Product
	PatchProduct(id int, body PatchProductBody) Product
	ManualPDF(id int) []byte
	Summary() CatalogSummary
}

// NewCatalogService returns the deterministic in-memory implementation used by
// the example commands. In a real application replace this with a struct backed
// by a database (injected via its own constructor).
func NewCatalogService() CatalogService { return &catalogService{} }

type catalogService struct{}

func (s *catalogService) ListProducts() []Product {
	return []Product{s.build(1), s.build(2)}
}

func (s *catalogService) GetProduct(id int) (Product, bool) {
	if id == 0 {
		return Product{}, false
	}
	return s.build(id), true
}

func (s *catalogService) CreateProduct(body CreateProductBody) Product {
	return Product{
		ID: 1001, Name: body.Name, SKU: body.SKU, Price: body.Price,
		Currency: body.Currency, Category: body.Category, Tags: body.Tags,
		Warehouse: body.Warehouse, InStock: true, CreatedAt: s.now(),
	}
}

func (s *catalogService) UpdateProduct(id int, body UpdateProductBody) Product {
	return Product{
		ID: id, Name: body.Name, Price: body.Price,
		Currency: body.Currency, InStock: body.InStock, CreatedAt: s.now(),
	}
}

func (s *catalogService) PatchProduct(id int, body PatchProductBody) Product {
	p := s.build(id)
	if body.Name != nil {
		p.Name = *body.Name
	}
	if body.Price != nil {
		p.Price = *body.Price
	}
	if body.InStock != nil {
		p.InStock = *body.InStock
	}
	return p
}

func (s *catalogService) ManualPDF(id int) []byte {
	return fmt.Appendf(nil, "%%PDF-1.4\n%% demo manual for product %d\n", id)
}

func (s *catalogService) Summary() CatalogSummary {
	return CatalogSummary{Products: 128, Categories: 7}
}

func (s *catalogService) now() time.Time {
	return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
}

func (s *catalogService) build(id int) Product {
	return Product{
		ID:        id,
		Name:      "Demo Widget",
		SKU:       "5f9c2e3a-1b4d-4c8e-9f0a-2b3c4d5e6f70",
		Price:     19.99,
		Currency:  "USD",
		Category:  "electronics",
		InStock:   true,
		Tags:      []string{"new", "featured"},
		Warehouse: Address{Line1: "1 Main St", City: "Tokyo", Country: "JP"},
		CreatedAt: s.now(),
	}
}
