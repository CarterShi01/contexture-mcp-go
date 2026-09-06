package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestContextureOptionsPreserveSafeDefaultsAndRejectPublicStartup(t *testing.T) {
	local, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport})
	if err != nil {
		t.Fatal(err)
	}
	if local.URL() != "http://127.0.0.1:8000/mcp" {
		t.Fatalf("URL() = %q", local.URL())
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Host: "127.0.0.1"}); !errors.As(err, new(*server.ServeError)) {
		t.Fatalf("stdio HTTP option error = %v", err)
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Host: "0.0.0.0"}); err == nil {
		t.Fatal("public HTTP bind unexpectedly succeeded")
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Host: "0.0.0.0", AllowedHosts: []string{"localhost"}}); err == nil {
		t.Fatal("unauthenticated public bind unexpectedly succeeded")
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Host: "0.0.0.0", AllowedHosts: []string{"localhost"}, AllowAnonymous: true}); err != nil {
		t.Fatal(err)
	}
}

type tokenVerifier func(context.Context, string) (*contexture.Principal, error)

func (verify tokenVerifier) Verify(ctx context.Context, token string) (*contexture.Principal, error) {
	return verify(ctx, token)
}

func TestAuthMiddlewareRejectsUnauthenticatedRequests(t *testing.T) {
	identity := server.Auth{Verifier: tokenVerifier(func(_ context.Context, token string) (*contexture.Principal, error) {
		if token != "valid" {
			return nil, nil
		}
		return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "person", Scopes: []string{"mcp"}, Claims: map[string]any{"exp": float64(2_000_000_000)}}), nil
	}), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp", RequiredScopes: []string{"mcp"}}
	middleware, err := identity.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "http://server/mcp", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", denied.Code)
	}
	accepted := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://server/mcp", nil)
	request.Header.Set("Authorization", "Bearer valid")
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d", accepted.Code)
	}
}
