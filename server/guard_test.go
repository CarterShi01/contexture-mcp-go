package server

import "testing"

func TestAllowedHostAcceptsExactAndExplicitPortWildcardOnly(t *testing.T) {
	values := []string{"mcp.example:*", "fixed.example:8443"}
	for _, requestHost := range []string{"mcp.example:443", "mcp.example", "fixed.example:8443"} {
		if !allowedHost(values, requestHost) {
			t.Fatalf("allowedHost(%q) rejected %q", values, requestHost)
		}
	}
	for _, requestHost := range []string{"mcp.example.evil:443", "fixed.example:443"} {
		if allowedHost(values, requestHost) {
			t.Fatalf("allowedHost(%q) accepted %q", values, requestHost)
		}
	}
}
