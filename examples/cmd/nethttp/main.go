// Command nethttp mounts the SAME shared demo routes on the net/http standard
// library, proving the typed route set is transport-agnostic.
//
//	go run ./examples/nethttp        # then open http://localhost:8081
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/examples/api"
	"github.com/antlss/oapi/examples/docsui"
	"github.com/antlss/oapi/examples/validation"

	nethttpadapter "github.com/antlss/oapi/adapter/nethttp"
)

// htmlPage serves a static HTML documentation page.
func htmlPage(html string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", docsui.ContentType)
		_, _ = w.Write([]byte(html))
	}
}

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	flag.Parse()

	// Install the default validator so `binding` rules are enforced. The core
	// ships none, so this opt-in is how an app turns validation on.
	oapi.SetValidator(validation.New())

	mux := http.NewServeMux()

	// The same api.Routes() as the gin/fiber examples.
	nethttpadapter.RegisterAll(mux, api.Routes()...)

	reg := api.Registry()
	mux.HandleFunc("GET /openapi.json", nethttpadapter.SpecHandler(reg))
	mux.HandleFunc("GET /{$}", htmlPage(docsui.IndexPage)) // exact "/" only, not a catch-all
	mux.HandleFunc("GET /redoc", htmlPage(docsui.RedocPage))
	mux.HandleFunc("GET /swagger", htmlPage(docsui.SwaggerPage))

	log.Printf("nethttp example: API on http://localhost%s  (docs: / · /swagger · /redoc · /openapi.json)", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("nethttp example: %v", err)
	}
}
