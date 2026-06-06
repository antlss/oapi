package chi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	chiv5 "github.com/go-chi/chi/v5"

	"github.com/antlss/oapi"
	chiadapter "github.com/antlss/oapi/adapter/chi"
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
	r := chiv5.NewRouter()
	chiadapter.Register(r, greetRoute())

	req := httptest.NewRequest(http.MethodGet, "/greet/world", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

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

func TestRegisterAll_AndMiddleware(t *testing.T) {
	var sawMiddleware bool
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			sawMiddleware = true
			next.ServeHTTP(w, req)
		})
	}

	r := chiv5.NewRouter()
	chiadapter.Register(r, greetRoute(), mw)

	req := httptest.NewRequest(http.MethodGet, "/greet/bob", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !sawMiddleware {
		t.Fatalf("native middleware was not invoked")
	}
}

func TestSpecHandler_ReturnsValidJSON(t *testing.T) {
	reg := oapi.NewRegistry("test", "1.0.0").Add(greetRoute())

	r := chiv5.NewRouter()
	r.Get("/openapi.json", chiadapter.SpecHandler(reg))

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

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
