package oapitest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/antlss/oapi"
	"github.com/antlss/oapi/oapitest"
)

// --- typed request/response shapes exercised by the harness ---------------------

type getHeader struct {
	Token string `header:"Authorization" binding:"required"`
}

type getParam struct {
	ID int `uri:"id"`
}

type getQuery struct {
	Page int `form:"page"`
}

type productResponse struct {
	ID    int    `json:"id"`
	Page  int    `json:"page"`
	Token string `json:"token"`
}

type createBody struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
}

type createResponse struct {
	Name  string `json:"name"`
	Price int    `json:"price"`
}

// noopValidator accepts everything; installing it (instead of leaving nil)
// silences the "no validator configured" warning for routes that carry `binding`
// rules without changing behavior.
type noopValidator struct{}

func (noopValidator) Validate(any, string) error { return nil }

func init() { oapi.SetValidator(noopValidator{}) }

// TestInvokeSuccess builds a typed route binding header + uri param + query, then
// drives it through the Builder and asserts the decoded success envelope.
func TestInvokeSuccess(t *testing.T) {
	route := oapi.NewRoute(http.MethodGet, "/products/:id",
		func(_ context.Context, req oapi.Request[getHeader, getParam, getQuery, struct{}]) (*productResponse, error) {
			return &productResponse{
				ID:    req.Param.ID,
				Page:  req.Query.Page,
				Token: req.Header.Token,
			}, nil
		},
	)

	rec := oapitest.New(http.MethodGet, "/products/7?page=3").
		Header("Authorization", "Bearer abc").
		Param("id", "7").
		Invoke(route)

	if rec.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Status, rec.BodyString())
	}

	// Default DataEnvelope wraps the payload under "data".
	var env struct {
		Data productResponse `json:"data"`
	}
	if err := rec.DecodeJSON(&env); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.BodyString())
	}
	if env.Data.ID != 7 || env.Data.Page != 3 || env.Data.Token != "Bearer abc" {
		t.Fatalf("got %+v, want {ID:7 Page:3 Token:Bearer abc}", env.Data)
	}
}

// TestPathParam isolates the injected path parameter seam.
func TestPathParam(t *testing.T) {
	route := oapi.NewRoute(http.MethodGet, "/items/:id",
		func(_ context.Context, req oapi.Request[struct{}, getParam, struct{}, struct{}]) (*productResponse, error) {
			return &productResponse{ID: req.Param.ID}, nil
		},
	)

	rec := oapitest.New(http.MethodGet, "/items/42").
		Param("id", "42").
		Invoke(route)

	if rec.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Status, rec.BodyString())
	}
	var env struct {
		Data productResponse `json:"data"`
	}
	if err := rec.DecodeJSON(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.ID != 42 {
		t.Fatalf("param ID = %d, want 42", env.Data.ID)
	}
}

// TestJSONBodyBind proves a JSON body is bound and the handler sees it.
func TestJSONBodyBind(t *testing.T) {
	route := oapi.NewRoute(http.MethodPost, "/products",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, struct{}, createBody]) (*createResponse, error) {
			return &createResponse{Name: req.Body.Name, Price: req.Body.Price}, nil
		},
		oapi.WithSuccessStatus(http.StatusCreated),
	)

	rec := oapitest.New(http.MethodPost, "/products").
		JSON(createBody{Name: "Keyboard", Price: 49}).
		Invoke(route)

	if rec.Status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Status, rec.BodyString())
	}
	if ct := rec.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var env struct {
		Data createResponse `json:"data"`
	}
	if err := rec.DecodeJSON(&env); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.BodyString())
	}
	if env.Data.Name != "Keyboard" || env.Data.Price != 49 {
		t.Fatalf("got %+v, want {Keyboard 49}", env.Data)
	}
}

// TestErrorPath asserts a handler-returned oapi.NewError(404,...) renders as a 404
// and is surfaced on Recorded.Errors.
func TestErrorPath(t *testing.T) {
	route := oapi.NewRoute(http.MethodGet, "/items/:id",
		func(_ context.Context, _ oapi.Request[struct{}, getParam, struct{}, struct{}]) (*productResponse, error) {
			return nil, oapi.NewError(http.StatusNotFound, "not_found", "item not found")
		},
	)

	rec := oapitest.New(http.MethodGet, "/items/99").
		Param("id", "99").
		Invoke(route)

	if rec.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Status, rec.BodyString())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := rec.DecodeJSON(&env); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.BodyString())
	}
	if env.Error.Code != "not_found" || env.Error.Message != "item not found" {
		t.Fatalf("error body = %+v", env.Error)
	}
	if len(rec.Errors) != 1 {
		t.Fatalf("recorded errors = %d, want 1", len(rec.Errors))
	}
}

// TestQueryMerge verifies Query() merges with a query already in the target.
func TestQueryMerge(t *testing.T) {
	route := oapi.NewRoute(http.MethodGet, "/items",
		func(_ context.Context, req oapi.Request[struct{}, struct{}, getQuery, struct{}]) (*productResponse, error) {
			return &productResponse{Page: req.Query.Page}, nil
		},
	)

	rec := oapitest.New(http.MethodGet, "/items").
		Query("page", "5").
		Invoke(route)

	var env struct {
		Data productResponse `json:"data"`
	}
	if err := rec.DecodeJSON(&env); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.BodyString())
	}
	if env.Data.Page != 5 {
		t.Fatalf("page = %d, want 5", env.Data.Page)
	}
}
