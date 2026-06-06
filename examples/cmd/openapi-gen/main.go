// Command openapi-gen writes the demo API's OpenAPI document to disk as JSON and
// YAML. It is a thin wrapper around gendoc.Main, which does the flag parsing,
// validation and file writing, so the whole command body is a single line.
//
//	go run ./cmd/openapi-gen -out ./openapi
//	go run ./cmd/openapi-gen -out ./openapi -format json
//
// A base/common document can be overlaid (info, branding, x-tagGroups, …); the
// generated paths and any value set in code via the Registry builders take
// precedence over it:
//
//	go run ./cmd/openapi-gen -out ./openapi -base ./openapi/common.json
package main

import (
	"github.com/antlss/oapi/examples/api"
	gendoc "github.com/antlss/oapi/tools/gen_doc"
)

func main() {
	h := api.NewHandler(api.NewCatalogService())
	gendoc.Main(h.Registry())
}
