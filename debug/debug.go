// Package debug is a loopback-only, read-only developer HTTP adapter over one
// immutable Node Catalog. It has no Tool or application lifecycle capability.
package debug

import (
	"context"
	"embed"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/introspection"
)

//go:embed assets/index.html assets/app.css assets/app.js
var assets embed.FS

const contentPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src data:; base-uri 'none'; frame-ancestors 'none'"

type Server struct {
	payload []byte
	nodes   map[string][]byte
	etag    string
	mu      sync.Mutex
	active  bool
}

// New captures the exact immutable snapshot, not a recomputed catalog.
func New(catalog *introspection.NodeCatalog) (*Server, error) {
	if catalog == nil || catalog.Snapshot() == nil {
		return nil, fmt.Errorf("constructed Node Catalog required")
	}
	snapshot := catalog.Snapshot()
	payload, err := snapshot.ToJSON(nil)
	if err != nil {
		return nil, err
	}
	nodes := map[string][]byte{}
	for _, node := range snapshot.Nodes() {
		value, err := node.AsJSON()
		if err != nil {
			return nil, err
		}
		text, err := introspection.FormatJSON(value, introspection.JSONOptions{Spaced: true})
		if err != nil {
			return nil, err
		}
		nodes[node.Ref] = []byte(text)
	}
	return &Server{payload: []byte(payload), nodes: nodes, etag: `"` + snapshot.ContentDigest() + `"`}, nil
}

type StartOptions struct {
	Host string
	Port int
}

// Address is a running TCP loopback listener with idempotent synchronous close.
type Address struct {
	Host   string
	Port   int
	URL    string
	server *http.Server
	done   chan struct{}
	once   sync.Once
	err    error
}

func (address *Address) Done() <-chan struct{} { return address.done }
func (address *Address) Close() error {
	address.once.Do(func() {
		address.err = address.server.Close()
		<-address.done
	})
	return address.err
}

func loopback(host string) bool {
	if strings.ToLower(host) == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && (ip.Is4() && ip.IsLoopback() || ip == netip.IPv6Loopback())
}

func trustedAuthority(authority string) bool {
	if authority == "" || strings.ContainsAny(authority, "@/\\?# \t\r\n") {
		return false
	}
	host, port := authority, "80"
	if strings.Contains(authority, ":") {
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil {
			// Bracketed IPv6 without a port is a valid HTTP authority.
			if strings.HasPrefix(authority, "[") && strings.HasSuffix(authority, "]") {
				host, port = authority[1:len(authority)-1], "80"
			} else {
				return false
			}
		}
	}
	if !loopback(host) || port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 0 && n <= 65535
}

func trustedOrigin(origin, authority string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.User == nil &&
		strings.EqualFold(parsed.Host, authority) && trustedAuthority(parsed.Host) &&
		(parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == ""
}

// Start binds only explicit localhost or literal loopback IPs. localhost is
// translated to a literal loopback address, never resolved through DNS.
func (server *Server) Start(ctx context.Context, options StartOptions) (*Address, error) {
	if server == nil || server.payload == nil {
		return nil, fmt.Errorf("constructed Catalog debug server required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("debug startup requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host := options.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if !loopback(host) {
		return nil, fmt.Errorf("Catalog debug HTTP is loopback-only")
	}
	if options.Port < 0 || options.Port > 65535 {
		return nil, fmt.Errorf("debug port must be between zero and 65535")
	}
	if strings.ToLower(host) == "localhost" {
		host = "127.0.0.1"
	}
	server.mu.Lock()
	if server.active {
		server.mu.Unlock()
		return nil, fmt.Errorf("Catalog debug server is already active")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(options.Port)))
	if err != nil {
		server.mu.Unlock()
		return nil, err
	}
	server.active = true
	server.mu.Unlock()
	httpServer := &http.Server{
		Handler: http.HandlerFunc(server.request), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 * 1024,
	}
	bound := listener.Addr().(*net.TCPAddr)
	address := &Address{
		Host: bound.IP.String(), Port: bound.Port,
		URL:    "http://" + net.JoinHostPort(bound.IP.String(), strconv.Itoa(bound.Port)),
		server: httpServer, done: make(chan struct{}),
	}
	go func() {
		_ = httpServer.Serve(listener)
		server.mu.Lock()
		server.active = false
		server.mu.Unlock()
		close(address.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = address.Close()
		case <-address.done:
		}
	}()
	return address, nil
}

func (server *Server) request(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-Frame-Options", "DENY")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Content-Security-Policy", contentPolicy)
	response.Header().Set("Cache-Control", "no-cache")
	head := request.Method == http.MethodHead
	send := func(status int, payload []byte, contentType string) {
		response.Header().Set("Content-Type", contentType)
		response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		response.WriteHeader(status)
		if !head {
			_, _ = response.Write(payload)
		}
	}
	sendJSON := func(status int, payload []byte) { send(status, payload, "application/json; charset=utf-8") }
	fail := func(status int, message string) {
		text, _ := introspection.FormatJSON(map[string]any{"error": message}, introspection.JSONOptions{})
		sendJSON(status, []byte(text))
	}
	if request.URL.IsAbs() || !trustedAuthority(request.Host) {
		fail(400, "Untrusted Host header.")
		return
	}
	if origins := request.Header.Values("Origin"); len(origins) != 0 &&
		(len(origins) != 1 || !trustedOrigin(origins[0], request.Host)) {
		fail(403, "Cross-origin request refused.")
		return
	}
	if request.Method != http.MethodGet && !head {
		fail(405, "Read-only endpoint.")
		return
	}
	path := request.URL.EscapedPath()
	if head && path != "/api/catalog" {
		fail(404, "Not found.")
		return
	}
	switch path {
	case "/", "/app.css", "/app.js":
		name, contentType := "assets/index.html", "text/html; charset=utf-8"
		if path == "/app.css" {
			name, contentType = "assets/app.css", "text/css; charset=utf-8"
		} else if path == "/app.js" {
			name, contentType = "assets/app.js", "text/javascript; charset=utf-8"
		}
		payload, err := assets.ReadFile(name)
		if err != nil {
			fail(500, "Debug asset unavailable.")
			return
		}
		send(200, payload, contentType)
		return
	case "/healthz":
		sendJSON(200, []byte(`{"status":"ok"}`))
		return
	}
	var payload []byte
	if path == "/api/catalog" {
		payload = server.payload
	} else if strings.HasPrefix(path, "/api/nodes/") {
		ref, err := url.PathUnescape(strings.TrimPrefix(path, "/api/nodes/"))
		if err != nil {
			fail(400, "Invalid catalog ref encoding.")
			return
		}
		payload = server.nodes[ref]
		if payload == nil {
			fail(404, "Unknown catalog ref.")
			return
		}
	}
	if payload == nil {
		fail(404, "Not found.")
		return
	}
	response.Header().Set("ETag", server.etag)
	if tags := request.Header.Values("If-None-Match"); len(tags) == 1 && tags[0] == server.etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	sendJSON(200, payload)
}
