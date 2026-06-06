package api

import "time"

// Fake business layer. Real handlers would call a service or repository; these
// deterministic stubs keep the example self-contained and the generated docs
// byte-stable across runs.

func sampleTime() time.Time { return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC) }

func sampleProduct(id int) Product {
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
		CreatedAt: sampleTime(),
	}
}

func sampleProducts() []Product {
	return []Product{sampleProduct(1), sampleProduct(2)}
}
