// Command scoped demonstrates oapi.App — per-App scoped configuration installed
// WITHOUT any process-wide Set* call. It mounts two independently configured route
// groups in ONE process:
//
//   - /v1 uses the library's standard {"data": ...} envelope and built-in error shape;
//   - /v2 uses a {"success": true, "data": ...} envelope and the project's AppError
//     error shape (see api.AppErrorParser).
//
// Each route is bound to its App via oapi.WithApp. The SAME scoping drives the served
// OpenAPI document, so /v1 and /v2 are documented with their own success and error
// shapes — proving config scopes for both the wire bytes and the docs, and that two
// Apps coexist in one process without racing on globals.
//
//	go run ./examples/cmd/scoped   # then open http://localhost:8084
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/examples/api"
	"github.com/antlss/oapi/examples/docsui"
	"github.com/antlss/oapi/examples/validation"

	nethttpadapter "github.com/antlss/oapi/adapter/nethttp"
)

// emptyReq is the request shape for every demo route: it binds nothing. (A type
// alias is transparent, so NewRoute still infers all four parts as struct{}.)
type emptyReq = oapi.Request[struct{}, struct{}, struct{}, struct{}]

// pingResult is the tiny success body each /ping route returns, so the per-App
// envelope difference is visible both on the wire and in the docs.
type pingResult struct {
	App  string `json:"app"  example:"v1"`
	Pong bool   `json:"pong" example:"true"`
}

// pingRoute returns a success body wrapped by the App's configured envelope.
func pingRoute(app *oapi.App, path, label string) oapi.Route {
	return oapi.NewRoute(http.MethodGet, path,
		func(_ context.Context, _ emptyReq) (*pingResult, error) {
			return &pingResult{App: label, Pong: true}, nil
		},
		oapi.WithApp(app),
		oapi.WithSummary("Ping ("+label+" envelope)"),
		oapi.WithTags(label),
		oapi.WithResponseType[pingResult](),
	)
}

// boomRoute always returns the same oapi.Error; how it renders (and is documented)
// depends purely on the App's error parser.
func boomRoute(app *oapi.App, path, label string) oapi.Route {
	return oapi.NewRoute(http.MethodGet, path,
		func(_ context.Context, _ emptyReq) (*struct{}, error) {
			return nil, oapi.NewError(http.StatusConflict, "conflict", "demo conflict from "+label)
		},
		oapi.WithApp(app),
		oapi.WithSummary("Trigger an error ("+label+" error shape)"),
		oapi.WithTags(label),
		oapi.WithResponse[struct{}](http.StatusConflict, "Conflict, rendered in this App's error shape"),
	)
}

func htmlPage(html string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", docsui.ContentType)
		_, _ = w.Write([]byte(html))
	}
}

func main() {
	addr := flag.String("addr", ":8084", "address to listen on")
	flag.Parse()

	// One validator, shared by both Apps (it is concurrency-safe). Note there is NO
	// oapi.Set* call anywhere in this program — all configuration lives on the Apps.
	v := validation.New()

	// v1: the library defaults — standard {"data": ...} envelope, built-in errors.
	v1 := oapi.New(oapi.WithValidator(v))

	// v2: a project-specific success envelope AND error shape, scoped to this App.
	v2 := oapi.New(
		oapi.WithValidator(v),
		oapi.WithResponseEnvelope(oapi.KeyedEnvelope{
			DataKey:   "data",
			Constants: map[string]any{"success": true},
		}),
		oapi.WithErrorParser(api.AppErrorParser{}),
	)

	routes := []oapi.Route{
		pingRoute(v1, "/v1/ping", "v1"), boomRoute(v1, "/v1/boom", "v1"),
		pingRoute(v2, "/v2/ping", "v2"), boomRoute(v2, "/v2/boom", "v2"),
	}

	mux := http.NewServeMux()
	nethttpadapter.RegisterAll(mux, routes...)

	reg := oapi.NewRegistry("Scoped Config API", "v1").
		Describe("Two oapi.App configurations (different success envelope + error parser) "+
			"coexisting in one process via WithApp, with no process-wide Set* call.").
		AddServer("http://localhost:8084", "Local example server").
		AddTag("v1", "Standard {data} envelope + built-in error shape").
		AddTag("v2", "{success, data} envelope + AppError error shape").
		Add(routes...)

	mux.HandleFunc("GET /openapi.json", nethttpadapter.SpecHandler(reg))
	mux.HandleFunc("GET /{$}", htmlPage(docsui.IndexPage))
	mux.HandleFunc("GET /redoc", htmlPage(docsui.RedocPage))
	mux.HandleFunc("GET /swagger", htmlPage(docsui.SwaggerPage))

	log.Printf("scoped example: API on http://localhost%s  (two Apps via WithApp; "+
		"try /v1/ping vs /v2/ping and /v1/boom vs /v2/boom; docs: / · /swagger · /redoc)", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("scoped example: %v", err)
	}
}
