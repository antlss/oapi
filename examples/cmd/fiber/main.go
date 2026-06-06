// Command fiber mounts the SAME shared demo routes on Fiber v2, proving the
// typed route set is transport-agnostic.
//
//	go run ./examples/fiber          # then open http://localhost:8082
package main

import (
	"flag"
	"log"

	"github.com/gofiber/fiber/v2"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/examples/api"
	"github.com/antlss/oapi/examples/docsui"
	"github.com/antlss/oapi/examples/validation"

	fiberadapter "github.com/antlss/oapi/adapter/fiber"
)

// htmlPage serves a static HTML documentation page.
func htmlPage(html string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", docsui.ContentType)
		return c.SendString(html)
	}
}

func main() {
	addr := flag.String("addr", ":8082", "address to listen on")
	flag.Parse()

	// Install the default validator so `binding` rules are enforced. The core
	// ships none, so this opt-in is how an app turns validation on.
	oapi.SetValidator(validation.New())

	app := fiber.New()

	// The same api.Routes() as the gin/nethttp examples.
	fiberadapter.RegisterAll(app, api.Routes()...)

	reg := api.Registry()
	app.Get("/openapi.json", fiberadapter.SpecHandler(reg))
	app.Get("/", htmlPage(docsui.IndexPage))
	app.Get("/redoc", htmlPage(docsui.RedocPage))
	app.Get("/swagger", htmlPage(docsui.SwaggerPage))

	log.Printf("fiber example: API on http://localhost%s  (docs: / · /swagger · /redoc · /openapi.json)", *addr)
	if err := app.Listen(*addr); err != nil {
		log.Fatalf("fiber example: %v", err)
	}
}
