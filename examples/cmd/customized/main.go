// Command customized mounts the SAME shared demo routes as the gin/nethttp/fiber
// examples, but with a project-wide response shape installed once at startup:
//
//   - oapi.SetResponseEnvelope — every success body is wrapped in a custom
//     {"success": true, "data": ..., "meta": ...} envelope instead of the default
//     {"data": ...}.
//   - oapi.SetErrorParser — every error renders in the project's own AppError shape
//     ({"success": false, "error": {code, message, fields}}) instead of the
//     built-in {"error": {...}} envelope.
//
// Both seams drive the generated OpenAPI document as well as the wire bytes, so the
// docs served here describe the customized shapes — no drift. Per-route overrides
// still win: /health stays raw (WithRawResponse) and /catalog/summary keeps its own
// {result, success} envelope (WithEnvelope), demonstrating precedence.
//
//	go run ./examples/cmd/customized   # then open http://localhost:8083
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/examples/api"
	"github.com/antlss/oapi/examples/docsui"
	"github.com/antlss/oapi/examples/playground"

	nethttpadapter "github.com/antlss/oapi/adapter/nethttp"
)

func htmlPage(html string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", docsui.ContentType)
		_, _ = w.Write([]byte(html))
	}
}

func main() {
	addr := flag.String("addr", ":8083", "address to listen on")
	flag.Parse()

	// Process-wide configuration, installed once before serving.
	oapi.SetValidator(playground.New())
	// A custom success envelope for the whole API: {"success": true, "data": ...}.
	oapi.SetResponseEnvelope(oapi.KeyedEnvelope{
		DataKey:   "data",
		MetaKey:   "meta",
		Constants: map[string]any{"success": true},
	})
	// A custom, uniform error shape for the whole API (see api.AppError).
	oapi.SetErrorParser(api.AppErrorParser{})

	mux := http.NewServeMux()
	nethttpadapter.RegisterAll(mux, api.Routes()...)

	reg := api.Registry()
	mux.HandleFunc("GET /openapi.json", nethttpadapter.SpecHandler(reg))
	mux.HandleFunc("GET /{$}", htmlPage(docsui.IndexPage))
	mux.HandleFunc("GET /redoc", htmlPage(docsui.RedocPage))
	mux.HandleFunc("GET /swagger", htmlPage(docsui.SwaggerPage))

	log.Printf("customized example: API on http://localhost%s  (custom envelope + error parser; docs: / · /swagger · /redoc)", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("customized example: %v", err)
	}
}
