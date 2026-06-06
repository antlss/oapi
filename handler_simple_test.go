package oapi

import (
	"context"
	"net/http"
	"testing"
)

func TestNewBodyRoute_BindsBodyAndDocuments(t *testing.T) {
	route := NewBodyRoute(http.MethodPost, "/things",
		func(_ context.Context, body benchBody) (*benchResp, error) {
			return &benchResp{ID: 1, Name: body.Name}, nil
		})

	c := newBenchCarrier()
	route.Invoke(c)
	if c.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", c.status)
	}

	// The Body type still drives the documented request body.
	if rb := requestBody(route.Schema().Body, nil); rb == nil {
		t.Fatal("NewBodyRoute should document a request body")
	}
}

func TestNewQueryRoute_BindsQuery(t *testing.T) {
	var gotPage int
	route := NewQueryRoute(http.MethodGet, "/things",
		func(_ context.Context, q benchQuery) (*benchResp, error) {
			gotPage = q.Page
			return &benchResp{ID: q.Page}, nil
		})

	c := newBenchCarrier()
	route.Invoke(c)
	if c.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", c.status)
	}
	if gotPage != 2 { // benchCarrier query has page=2
		t.Fatalf("bound page = %d, want 2", gotPage)
	}
	// Query parts are documented as parameters, not a body.
	if rb := requestBody(route.Schema().Body, nil); rb != nil {
		t.Fatal("NewQueryRoute should not document a request body")
	}
}

func TestNewParamRoute_BindsParam(t *testing.T) {
	route := NewParamRoute(http.MethodGet, "/things/:id",
		func(_ context.Context, p widgetURI) (*benchResp, error) {
			return &benchResp{ID: p.ID}, nil
		})
	if route.Schema().Param == nil {
		t.Fatal("NewParamRoute should capture the Param type for docs")
	}
}
