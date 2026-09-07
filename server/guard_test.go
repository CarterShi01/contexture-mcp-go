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

func TestSameBindHostTreatsOnlyCanonicalLoopbackAddressesAsLocalhost(t *testing.T) {
	for _, pair := range [][2]string{{"localhost", "127.0.0.1"}, {"localhost", "::1"}, {"[::1]", "localhost"}} {
		if !sameBindHost(pair[0], pair[1]) {
			t.Fatalf("sameBindHost(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"localhost", "0.0.0.0"}, {"localhost", "192.0.2.1"}, {"mcp.example", "192.0.2.1"}} {
		if sameBindHost(pair[0], pair[1]) {
			t.Fatalf("sameBindHost(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}
