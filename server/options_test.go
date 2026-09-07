package server_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestContextureOptionsOwnHTTPAuthenticationAndBodyLimit(t *testing.T) {
	identity := &server.Auth{Verifier: tokenVerifier(func(context.Context, string) (*contexture.Principal, error) { return nil, nil }), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp"}
	options, err := server.NewContextureOptions(server.ContextureOptions{
		Transport:           server.StreamableHTTPTransport,
		Host:                "0.0.0.0",
		AllowedHosts:        []string{"mcp.example:*"},
		Auth:                identity,
		MaxRequestBodyBytes: 31,
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Auth == identity || options.Auth == nil || options.MaxRequestBodyBytes != 31 {
		t.Fatalf("options did not defensively retain auth/body policy: %#v", options)
	}
	identity.RequiredScopes = append(identity.RequiredScopes, "changed")
	if len(options.Auth.RequiredScopes) != 0 {
		t.Fatalf("auth scopes were aliased: %#v", options.Auth.RequiredScopes)
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StdioTransport, Auth: identity, MaxRequestBodyBytes: 1}); err == nil {
		t.Fatal("stdio accepted HTTP auth/body policy")
	}
	if _, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, MaxRequestBodyBytes: -1}); err == nil {
		t.Fatal("negative HTTP body limit unexpectedly disabled the safety boundary")
	}
}

func TestServeListenerUsesOptionAuthAndEnforcesConfiguredBodyLimit(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "options-body", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "assistant", Description: "Answer requests.", Instructions: "Read first."}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		t.Fatal(err)
	}
	identity := &server.Auth{Verifier: tokenVerifier(func(_ context.Context, token string) (*contexture.Principal, error) {
		if token == "accepted" {
			return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "operator", Claims: map[string]any{"exp": int64(2_000_000_000)}}), nil
		}
		return nil, nil
	}), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp"}
	options, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Auth: identity, MaxRequestBodyBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- assembly.ServeListener(ctx, listener, options) }()
	request, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/mcp", bytes.NewReader(bytes.Repeat([]byte("x"), 33)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer accepted")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("configured body limit status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("streamable HTTP server did not stop after body-limit test")
	}
}

func TestServeListenerValidatesActualPublicBindAndRejectsTwoAuthSources(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "options-listener", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "assistant", Description: "Answer requests.", Instructions: "Read first."}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		t.Fatal(err)
	}
	loopbackOptions, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport})
	if err != nil {
		t.Fatal(err)
	}
	publicListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer publicListener.Close()
	if err := assembly.ServeListener(context.Background(), publicListener, loopbackOptions); err == nil {
		t.Fatal("a public listener bypassed loopback options without Host/origin/auth policy")
	} else if !errors.As(err, new(*server.ServeError)) {
		t.Fatalf("public listener error = %T %v, want ServeError", err, err)
	}

	identity := &server.Auth{Verifier: tokenVerifier(func(context.Context, string) (*contexture.Principal, error) { return nil, nil }), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp"}
	options, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Auth: identity})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	err = assembly.ServeListenerWithAuth(context.Background(), listener, options, identity)
	if err == nil || !errors.As(err, new(*server.ServeError)) {
		t.Fatalf("options and legacy Auth conflict = %v, want ServeError", err)
	}
}

func TestStartAcceptsLocalhostWhenTheTCPListenerResolvesToLoopback(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "localhost-options", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "assistant", Description: "Answer requests.", Instructions: "Read first."}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		t.Fatal(err)
	}
	options, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport, Host: "localhost", Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := assembly.Start(ctx, options); err != nil {
		t.Fatalf("Start with Host localhost and a resolved loopback listener = %v", err)
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
