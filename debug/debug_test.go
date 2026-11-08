package debug

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/model"
	"github.com/CarterShi01/contexture-mcp-go/introspection"
)

func fixture(t *testing.T) (*Server, *introspection.NodeCatalog, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	app, err := model.DeclareApplication(model.ApplicationDeclaration{Name: "debug-native", Roots: []model.Factory{func() model.Node {
		return &model.Role{Name: "root", Description: "native root", Instructions: "native\n", Tools: []model.Factory{func() model.Node {
			tool, err := model.NewTool("read😀", "tool", true, func(context.Context, struct{}) (bool, error) {
				calls.Add(1)
				return true, nil
			})
			if err != nil {
				panic(err)
			}
			return tool
		}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := model.Compile(app)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := introspection.New(index, introspection.CatalogOptions{View: introspection.ApplicationView, Detail: introspection.DefinitionDetail})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return server, catalog, calls
}

func get(t *testing.T, address *Address, method, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, address.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

func TestDebugRealHTTPReadonlyExactSnapshotHeadersAndAssets(t *testing.T) {
	server, catalog, calls := fixture(t)
	address, err := server.Start(context.Background(), StartOptions{Host: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := address.Close(); err != nil {
			t.Error(err)
		}
	})
	if address.Host != "127.0.0.1" {
		t.Fatal("localhost resolved outside explicit IPv4 loopback")
	}
	response, body := get(t, address, "GET", "/api/catalog", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(body))
	}
	snapshot, err := introspection.SnapshotFromJSON(string(body))
	if err != nil || snapshot.ContentDigest() != catalog.Snapshot().ContentDigest() {
		t.Fatal("HTTP snapshot changed", err)
	}
	text, _ := catalog.Snapshot().ToJSON(nil)
	if string(body) != text {
		t.Fatal("HTTP recomputed different snapshot bytes")
	}
	for name, value := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
		"Cache-Control": "no-cache", "Content-Security-Policy": contentPolicy,
	} {
		if response.Header.Get(name) != value {
			t.Fatalf("%s = %q", name, response.Header.Get(name))
		}
	}
	tag := `"` + catalog.Snapshot().ContentDigest() + `"`
	if response.Header.Get("ETag") != tag {
		t.Fatal("ETag not Catalog digest")
	}
	for _, row := range []struct{ path, name, digest string }{
		{"/", "index.html", "ce7368f613dcd68cf5e184b5d825aec3786f2d107c497f2408db8713f0de6f57"},
		{"/app.css", "app.css", "fc0137521c8aac27e896ae3e72cc46826f3ab219865361f0c7069a8b2755b8e1"},
		{"/app.js", "app.js", "53d9f565a5e2368169b418a3492019071bd7170a034f56cb884493322d3e0ca6"},
	} {
		response, body := get(t, address, "GET", row.path, nil)
		if response.StatusCode != 200 || fmt.Sprintf("%x", sha256.Sum256(body)) != row.digest {
			t.Fatalf("asset %s differs", row.name)
		}
	}
	_, health := get(t, address, "GET", "/healthz", nil)
	if string(health) != `{"status":"ok"}` {
		t.Fatal(string(health))
	}
	nodeResponse, nodeBody := get(t, address, "GET", "/api/nodes/"+url.PathEscape("root/read😀"), nil)
	node, err := catalog.Get("root/read😀")
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.AsJSON()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := introspection.FormatJSON(value, introspection.JSONOptions{Spaced: true})
	if nodeResponse.StatusCode != 200 || string(nodeBody) != want {
		t.Fatal("encoded ref response mismatch", string(nodeBody))
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
		response, body := get(t, address, method, "/api/catalog", nil)
		if response.StatusCode != 405 {
			t.Fatalf("%s => %d %s", method, response.StatusCode, body)
		}
	}
	for _, path := range []string{"/api/invoke", "/invoke", "/api/nodes/missing", "/favicon.ico", "/wrong"} {
		response, _ := get(t, address, "GET", path, nil)
		if response.StatusCode != 404 {
			t.Fatal("backdoor/unknown endpoint", path, response.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("debug invoked handler")
	}
}

func TestDebugHEADAndExactETag304Only(t *testing.T) {
	server, catalog, _ := fixture(t)
	address, err := server.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer address.Close()
	tag := `"` + catalog.Snapshot().ContentDigest() + `"`
	for _, method := range []string{"GET", "HEAD"} {
		response, body := get(t, address, method, "/api/catalog", map[string]string{"If-None-Match": tag})
		if response.StatusCode != 304 || len(body) != 0 {
			t.Fatal(method, response.StatusCode, string(body))
		}
	}
	response, body := get(t, address, "HEAD", "/api/catalog", nil)
	if response.StatusCode != 200 || len(body) != 0 || response.Header.Get("Content-Length") == "" {
		t.Fatal("HEAD Catalog shape")
	}
	for _, candidate := range []string{"W/" + tag, tag + ", " + tag, "*", tag + " "} {
		response, _ := get(t, address, "GET", "/api/catalog", map[string]string{"If-None-Match": candidate})
		// HTTP trims outer whitespace; the application still compares the
		// exact parsed field, not weak tags or comma-separated alternatives.
		if candidate != tag+" " && response.StatusCode != 200 {
			t.Fatal("non-exact ETag accepted", candidate)
		}
	}
	for _, path := range []string{"/", "/healthz", "/app.css", "/app.js", "/api/nodes/root"} {
		response, body := get(t, address, "HEAD", path, nil)
		if response.StatusCode != 404 || len(body) != 0 {
			t.Fatal("HEAD endpoint widened", path)
		}
	}
	request, _ := http.NewRequest("GET", address.URL+"/api/catalog", nil)
	request.Header.Add("If-None-Match", tag)
	request.Header.Add("If-None-Match", tag)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("duplicate ETag treated as exact")
	}
}

func raw(t *testing.T, address *Address, request string) (int, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(address.Host, fmt.Sprint(address.Port)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func TestDebugHostOriginRebindingUserinfoAndMalformedRequests(t *testing.T) {
	server, _, _ := fixture(t)
	address, err := server.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer address.Close()
	for _, host := range []string{"evil.example", "localhost.evil", "127.0.0.1.evil", "127.1", "2130706433", "0x7f000001", "user@localhost", "localhost:65536", "localhost:-1", "[::ffff:127.0.0.1]", "[::1%eth0]"} {
		status, body := raw(t, address, "GET /api/catalog HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
		if status != 400 {
			t.Fatal(host, status, body)
		}
	}
	for _, request := range []string{
		"GET /api/catalog HTTP/1.1\r\nConnection: close\r\n\r\n",
		"GET /api/catalog HTTP/1.1\r\nHost: localhost\r\nHost: evil.example\r\nConnection: close\r\n\r\n",
		"GET http://127.0.0.1/api/catalog HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n\r\n",
		"GET /api/nodes/%zz HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
	} {
		status, body := raw(t, address, request)
		if status != 400 {
			t.Fatal(status, body)
		}
	}
	authority := net.JoinHostPort(address.Host, fmt.Sprint(address.Port))
	for _, origin := range []string{"https://" + authority, "http://evil.example", "http://user@" + authority, "null", "http://" + authority + "/bad", "http://" + authority + "?x=1"} {
		response, body := get(t, address, "GET", "/api/catalog", map[string]string{"Origin": origin})
		if response.StatusCode != 403 {
			t.Fatal(origin, response.StatusCode, string(body))
		}
	}
	response, _ := get(t, address, "GET", "/api/catalog", map[string]string{"Origin": address.URL})
	if response.StatusCode != 200 {
		t.Fatal("same origin refused")
	}
	status, body := raw(t, address, "GET /api/catalog HTTP/1.1\r\nHost: "+authority+"\r\nOrigin: "+address.URL+"\r\nOrigin: "+address.URL+"\r\nConnection: close\r\n\r\n")
	if status != 403 {
		t.Fatal("duplicate origin accepted", status, body)
	}
}

func TestDebugLoopbackValidationStartupCloseRestartAndCancellation(t *testing.T) {
	server, _, _ := fixture(t)
	if _, err := New(nil); err == nil {
		t.Fatal("nil Catalog accepted")
	}
	if _, err := (&Server{}).Start(context.Background(), StartOptions{}); err == nil {
		t.Fatal("unconstructed server accepted")
	}
	for _, options := range []StartOptions{{Host: "0.0.0.0"}, {Host: "::"}, {Host: "example.com"}, {Host: "127.0.0.1.nip.io"}, {Host: "http://localhost"}, {Host: "127.1"}, {Port: -1}, {Port: 65536}} {
		if _, err := server.Start(context.Background(), options); err == nil {
			t.Fatal("unsafe bind accepted", options)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.Start(ctx, StartOptions{}); err == nil {
		t.Fatal("canceled startup accepted")
	}
	address, err := server.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Start(context.Background(), StartOptions{}); err == nil {
		t.Fatal("duplicate startup accepted")
	}
	response, _ := get(t, address, "GET", "/healthz", nil)
	if response.StatusCode != 200 {
		t.Fatal("not ready")
	}
	occupied := StartOptions{Host: address.Host, Port: address.Port}
	other, _, _ := fixture(t)
	if _, err := other.Start(context.Background(), occupied); err == nil {
		t.Fatal("occupied port accepted")
	}
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Go(func() {
			if err := address.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wait.Wait()
	select {
	case <-address.Done():
	default:
		t.Fatal("Close returned before listener exited")
	}
	ctx, cancel = context.WithCancel(context.Background())
	restarted, err := server.Start(ctx, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	get(t, restarted, "GET", "/healthz", nil)
	cancel()
	select {
	case <-restarted.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation failed to stop listener")
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(restarted.Host, fmt.Sprint(restarted.Port)), time.Second); err == nil {
		conn.Close()
		t.Fatal("stopped listener still accepts")
	}
}

func TestDebugIPv6LoopbackAndConcurrentRequests(t *testing.T) {
	server, _, calls := fixture(t)
	address, err := server.Start(context.Background(), StartOptions{Host: "::1"})
	if err != nil {
		t.Fatal("native test host must support IPv6 loopback", err)
	}
	defer address.Close()
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Go(func() {
			response, body := get(t, address, "GET", "/api/catalog", nil)
			if response.StatusCode != 200 || !bytes.Contains(body, []byte(`"schema": "contexture.node-catalog/v1"`)) {
				t.Error("concurrent response changed")
			}
		})
	}
	wait.Wait()
	if calls.Load() != 0 {
		t.Fatal("concurrent debug invoked handler")
	}
}

func TestDebugLiteralHostValidation(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "127.9.8.7", "::1"} {
		if !loopback(host) {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"::ffff:127.0.0.1", "localhost.", "localhoſt", "[::1]", "127.0.0.1/", "0.0.0.0", "::"} {
		if loopback(host) {
			t.Fatal(host)
		}
	}
	if !trustedAuthority("[::1]:8787") || !trustedAuthority("[::1]") || trustedAuthority("localhost:") || trustedAuthority("localhost:1.5") {
		t.Fatal("authority port grammar")
	}
	if !strings.HasPrefix(contentPolicy, "default-src 'none'") {
		t.Fatal("CSP default changed")
	}
}
