package server_test

import (
	"errors"
	"testing"

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
