package server

import (
	"errors"
	"net"
	"testing"
)

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
	spellings := []string{"localhost", "LOCALHOST", "127.0.0.1", "::1", "[::1]"}
	for _, declared := range spellings {
		for _, actual := range spellings {
			if !sameBindHost(declared, actual) {
				t.Fatalf("sameBindHost(%q, %q) = false, want true", declared, actual)
			}
		}
	}
	for _, pair := range [][2]string{{"localhost", "0.0.0.0"}, {"localhost", "192.0.2.1"}, {"mcp.example", "192.0.2.1"}} {
		if sameBindHost(pair[0], pair[1]) {
			t.Fatalf("sameBindHost(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}

func TestValidateListenerOptionsAcceptsEveryLoopbackPair(t *testing.T) {
	for _, declared := range []string{"localhost", "127.0.0.1", "::1", "[::1]"} {
		for _, actual := range []string{"127.0.0.1", "::1"} {
			options := &ContextureOptions{Transport: StreamableHTTPTransport, Host: declared}
			listener := fakeListener{address: &net.TCPAddr{IP: net.ParseIP(actual), Port: 8000}}
			if _, err := validateListenerOptions(options, listener); err != nil {
				t.Fatalf("declared host %q with actual host %q: %v", declared, actual, err)
			}
		}
	}
}

type fakeListener struct{ address net.Addr }

func (listener fakeListener) Accept() (net.Conn, error) { return nil, errors.New("fake listener") }
func (listener fakeListener) Close() error              { return nil }
func (listener fakeListener) Addr() net.Addr            { return listener.address }

var _ net.Listener = fakeListener{}
