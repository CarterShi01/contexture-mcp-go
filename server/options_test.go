package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestConfigureLoggingWritesContextureRecordsToStderr(t *testing.T) {
	t.Cleanup(func() {
		if err := server.ConfigureLogging(server.InfoLogLevel); err != nil {
			t.Fatal(err)
		}
	})
	if err := server.ConfigureLogging(server.WarnLogLevel); err != nil {
		t.Fatal(err)
	}
	if slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info record remained enabled at warn level")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("warn record was disabled")
	}
	if err := server.ConfigureLogging(server.LogLevel("verbose")); !errors.As(err, new(*server.ServeError)) {
		t.Fatalf("invalid logging level error = %v", err)
	}
}

func TestContextureOptionsPreserveSafeDefaultsAndRejectPublicStartup(t *testing.T) {
	local, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport})
	if err != nil {
		t.Fatal(err)
	}
	if local.URL() != "http://127.0.0.1:8000/mcp" {
		t.Fatalf("URL() = %q", local.URL())
	}
	if local.LogLevel != server.InfoLogLevel {
		t.Fatalf("LogLevel = %q", local.LogLevel)
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{LogLevel: server.LogLevel("verbose")}); !errors.As(err, new(*server.ServeError)) {
		t.Fatalf("invalid log level error = %v", err)
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

func TestAuthMiddlewarePreservesVerifiedPrincipalWithNativeNumericExpiry(t *testing.T) {
	principal := contexture.NewPrincipal(contexture.PrincipalOptions{
		Subject: "person", ClientID: "client", Issuer: "https://issuer.example",
		Scopes: []string{"mcp"}, Claims: map[string]any{"exp": int64(2_000_000_000), "tenant": "example"},
	})
	identity := server.Auth{Verifier: tokenVerifier(func(_ context.Context, token string) (*contexture.Principal, error) {
		if token == "valid" {
			return principal, nil
		}
		return nil, nil
	}), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp", RequiredScopes: []string{"mcp"}}
	middleware, err := identity.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		got := server.PrincipalOf(request.Context())
		if got == nil || got.Subject() != "person" || got.ClientID() != "client" || got.Issuer() != "https://issuer.example" || !got.HasScope("mcp") || got.Claims()["tenant"] != "example" {
			t.Fatalf("principal round trip = %#v", got)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://server/mcp", nil)
	request.Header.Set("Authorization", "Bearer valid")
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, request)
	if recorded.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d", recorded.Code)
	}
}
