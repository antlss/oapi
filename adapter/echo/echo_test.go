package echo_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	echov4 "github.com/labstack/echo/v4"

	"github.com/antlss/oapi"
	echoadapter "github.com/antlss/oapi/adapter/echo"
)

type greetParam struct {
	Name string `uri:"name"`
}

type greetResponse struct {
	Greeting string `json:"greeting"`
}

func greetRoute() oapi.Route {
	return oapi.NewRoute(
		http.MethodGet,
		"/greet/:name",
		func(_ context.Context, req oapi.Request[struct{}, greetParam, struct{}, struct{}]) (*greetResponse, error) {
			return &greetResponse{Greeting: "hello " + req.Param.Name}, nil
		},
	)
}

func TestRegister_ServesTypedRoute(t *testing.T) {
	e := echov4.New()
	echoadapter.Register(e, greetRoute())

	req := httptest.NewRequest(http.MethodGet, "/greet/world", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q, want application/json; charset=utf-8", ct)
	}

	// Default DataEnvelope wraps the payload under "data".
	var got struct {
		Data greetResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body %q: %v", rec.Body.String(), err)
	}
	if got.Data.Greeting != "hello world" {
		t.Fatalf("greeting = %q, want %q", got.Data.Greeting, "hello world")
	}
}

func TestRegisterAll_AndGroupRouter(t *testing.T) {
	e := echov4.New()
	g := e.Group("/api")
	echoadapter.RegisterAll(g, greetRoute())

	req := httptest.NewRequest(http.MethodGet, "/api/greet/bob", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestSpecHandler_ReturnsValidJSON(t *testing.T) {
	reg := oapi.NewRegistry("test", "1.0.0").Add(greetRoute())

	e := echov4.New()
	e.GET("/openapi.json", echoadapter.SpecHandler(reg))

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q, want application/json; charset=utf-8", ct)
	}

	body, _ := io.ReadAll(rec.Body)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if _, ok := doc["openapi"]; !ok {
		t.Fatalf("spec missing top-level \"openapi\" field: %s", body)
	}
}
