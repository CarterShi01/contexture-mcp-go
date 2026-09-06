package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type rootHeaderTransport struct {
	root  string
	token string
}

func (transport rootHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set(server.RootsHeader, transport.root)
	if transport.token != "" {
		clone.Header.Set("Authorization", "Bearer "+transport.token)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func TestApplicationServerServesGatewayOverStreamableHTTP(t *testing.T) {
	tool, err := contexture.NewTool("status", "Read status.", true, func(context.Context, applicationInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "launch", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "assistant", Description: "Answer requests.", Instructions: "Read first.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		t.Fatal(err)
	}
	options, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- assembly.ServeListener(ctx, listener, options) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "contexture-launch-test", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String() + "/mcp"}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		cancel()
		t.Fatalf("MCP tools = %#v", listed.Tools)
	}
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("streamable HTTP server did not stop after context cancellation")
	}
}

func TestApplicationServerSelectsIndependentRootsPerAuthenticatedHTTPClient(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "selected-launch", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Alpha."}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Beta."}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		t.Fatal(err)
	}
	options, err := server.NewContextureOptions(server.ContextureOptions{Transport: server.StreamableHTTPTransport})
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
	identity := &server.Auth{Verifier: tokenVerifier(func(_ context.Context, token string) (*contexture.Principal, error) {
		if token != "valid" {
			return nil, nil
		}
		return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "operator", Scopes: []string{"roots"}, Claims: map[string]any{"exp": float64(2_000_000_000)}}), nil
	}), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp", RequiredScopes: []string{"roots"}}
	selector := server.HeaderRootSelector{Ceiling: func(principal *contexture.Principal) (contexture.RootSelection, error) {
		if principal == nil || !principal.HasScope("roots") {
			return contexture.RootSelection{}, errors.New("selector did not receive authenticated principal")
		}
		return contexture.AllRoots(), nil
	}}
	go func() {
		errs <- assembly.ServeListenerWithAuthAndRootSelector(ctx, listener, options, identity, selector)
	}()
	endpoint := "http://" + listener.Addr().String() + "/mcp"
	discover := func(root string) string {
		client := mcp.NewClient(&mcp.Implementation{Name: "root-client-" + root, Version: "0.0.0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: rootHeaderTransport{root: root, token: "valid"}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.DiscoverGatewayName)})
		if err != nil {
			t.Fatal(err)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("discover content = %#v", result.Content)
		}
		return text.Text
	}
	alpha, beta := discover("alpha"), discover("beta")
	if !strings.Contains(alpha, "alpha") || strings.Contains(alpha, "beta") {
		t.Fatalf("alpha discover = %s", alpha)
	}
	if !strings.Contains(beta, "beta") || strings.Contains(beta, "alpha") {
		t.Fatalf("beta discover = %s", beta)
	}
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("streamable HTTP server did not stop after context cancellation")
	}
}
