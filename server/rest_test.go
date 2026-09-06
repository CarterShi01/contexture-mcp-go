package server_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type restInput struct {
	Value string `json:"value"`
}

func TestRestRouterUsesExplicitAllowlistAndFixedDoors(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "rest", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "ops", Description: "Ops.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	router, err := server.NewRestRouter(runtime, []server.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops/status"}, {Method: http.MethodPost, Path: "/restart", Ref: "ops/restart"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET missing input = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/restart", bytes.NewBufferString(`{"value":"ok"}`))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/status", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/arbitrary/ops/status", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("arbitrary ref = %d", response.Code)
	}
	if _, err := server.NewRestRouter(runtime, []server.RestRoute{{Method: http.MethodPost, Path: "/wrong", Ref: "ops/status"}}); err == nil {
		t.Fatal("writing route accepted read-only Tool")
	}
}
