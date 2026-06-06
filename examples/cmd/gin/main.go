// Command gin is the consolidated example: it mounts the shared demo routes on
// gin AND serves their OpenAPI document with both Redoc and Swagger UI, so the
// same routes that cmd/openapi-gen writes to disk can be exercised and browsed
// locally.
//
//	go run ./examples/gin            # then open http://localhost:8080
//	go run ./examples/gin -addr :9000
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/examples/api"
	"github.com/antlss/oapi/examples/docsui"
	"github.com/antlss/oapi/examples/playground"

	ginadapter "github.com/antlss/oapi/adapter/gin"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	// Install the default validator so `binding` rules are enforced. The core
	// ships none, so this opt-in is how an app turns validation on.
	oapi.SetValidator(playground.New())

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())

	// Mount the demo API.
	ginadapter.RegisterAll(engine, api.Routes()...)

	// Serve the docs for the same routes: the raw spec plus two browser UIs.
	reg := api.Registry()
	engine.GET("/openapi.json", ginadapter.SpecHandler(reg))
	page := func(html string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Data(http.StatusOK, docsui.ContentType, []byte(html)) }
	}
	engine.GET("/", page(docsui.IndexPage))
	engine.GET("/redoc", page(docsui.RedocPage))
	engine.GET("/swagger", page(docsui.SwaggerPage))

	log.Printf("gin example: API on http://localhost%s  (docs: / · /swagger · /redoc · /openapi.json)", *addr)
	if err := engine.Run(*addr); err != nil {
		log.Fatalf("gin example: %v", err)
	}
}
