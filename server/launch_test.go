package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
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

type surfaceHeaderTransport struct{ surface string }

func (transport surfaceHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set(server.SelectHeader, transport.surface)
	return http.DefaultTransport.RoundTrip(clone)
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
	if len(listed.Tools) != 5 {
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

func TestApplicationServerHoldsChannelsAcrossRealMCPHTTPRequests(t *testing.T) {
	channels := &launchChannels{}
	tool, err := contexture.NewTool("status", "Read status.", true, func(_ context.Context, input applicationInput) (string, error) {
		if !channels.isLive() {
			return "", errors.New("Tool ran outside the Channels lifetime")
		}
		return "live:" + input.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "launch-lifecycle", Channels: channels, Roots: []contexture.Factory{func() contexture.Node {
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
	client := mcp.NewClient(&mcp.Implementation{Name: "channels-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String() + "/mcp"}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for _, value := range []string{"one", "two"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.InvokeReadOnlyGatewayName), Arguments: map[string]any{"ref": "assistant/status", "arguments": map[string]any{"value": value}}})
		if err != nil || result.IsError || len(result.Content) != 1 {
			_ = session.Close()
			cancel()
			t.Fatalf("MCP Tool call = %#v, %v", result, err)
		}
	}
	if opened, closed, live := channels.snapshot(); opened != 1 || closed != 0 || !live {
		_ = session.Close()
		cancel()
		t.Fatalf("channels while serving = opened %d, closed %d, live %v", opened, closed, live)
	}
	_ = session.Close()
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("streamable HTTP Channels did not close after context cancellation")
	}
	if opened, closed, live := channels.snapshot(); opened != 1 || closed != 1 || live {
		t.Fatalf("channels after serving = opened %d, closed %d, live %v", opened, closed, live)
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
	type rootSurface struct {
		discovery    string
		instructions string
	}
	discover := func(root string) rootSurface {
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
		initialized := session.InitializeResult()
		if initialized == nil {
			t.Fatal("MCP client did not retain initialization instructions")
		}
		return rootSurface{discovery: text.Text, instructions: initialized.Instructions}
	}
	alpha, beta := discover("alpha"), discover("beta")
	if !strings.Contains(alpha.discovery, "alpha") || strings.Contains(alpha.discovery, "beta") {
		t.Fatalf("alpha discover = %s", alpha.discovery)
	}
	if !strings.Contains(beta.discovery, "beta") || strings.Contains(beta.discovery, "alpha") {
		t.Fatalf("beta discover = %s", beta.discovery)
	}
	if !strings.Contains(alpha.instructions, "alpha") || strings.Contains(alpha.instructions, "beta") {
		t.Fatalf("alpha instructions = %s", alpha.instructions)
	}
	if !strings.Contains(beta.instructions, "beta") || strings.Contains(beta.instructions, "alpha") {
		t.Fatalf("beta instructions = %s", beta.instructions)
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

func TestApplicationServerPromotesDeepHTTPSurfaceAndReportsInvalidParams(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "selected-surface", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "team", Description: "Team root.", Instructions: "Route work.", Children: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "editor", Description: "Edit documents.", Instructions: "Edit carefully."}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "reviewer", Description: "Review documents.", Instructions: "Review carefully."}
			},
		}}
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
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		errs <- assembly.ServeListenerWithAuthAndSurfaceSelector(ctx, listener, options, nil, server.HeaderSurfaceSelector{})
	}()
	endpoint := "http://" + listener.Addr().String() + "/mcp"
	client := mcp.NewClient(&mcp.Implementation{Name: "surface-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: surfaceHeaderTransport{surface: "team/editor"}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	initialized := session.InitializeResult()
	if initialized == nil || !strings.Contains(initialized.Instructions, "team/editor: Edit documents.") || strings.Contains(initialized.Instructions, "Team root") || strings.Contains(initialized.Instructions, "team/reviewer") {
		_ = session.Close()
		cancel()
		t.Fatalf("promoted instructions = %#v", initialized)
	}
	_ = session.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":202,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(server.SelectHeader, "missing")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		ID    any `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body.ID != float64(202) || body.Error.Code != -32602 || !strings.Contains(body.Error.Message, "unknown or empty Contexture selector") || strings.Contains(body.Error.Message, "team") {
		cancel()
		t.Fatalf("invalid selector response = status %d body %#v", response.StatusCode, body)
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

func TestAuthenticatedStreamableMCPInvocationCarriesCompletePrincipal(t *testing.T) {
	identityTool, err := contexture.NewTool("whoami", "Return verified identity facts.", true, func(ctx context.Context, _ struct{}) (map[string]any, error) {
		principal := contexture.CurrentPrincipal(ctx)
		if principal == nil {
			return nil, errors.New("CurrentPrincipal was absent")
		}
		claims := principal.Claims()
		return map[string]any{
			"subject": principal.Subject(), "client_id": principal.ClientID(), "issuer": principal.Issuer(),
			"scopes": principal.Scopes(), "tenant": claims["tenant"], "groups": claims["groups"],
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "principal-launch", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "identity", Description: "Identity.", Instructions: "Read identity.", Tools: []contexture.Factory{func() contexture.Node { return identityTool }}}
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
	valid := contexture.NewPrincipal(contexture.PrincipalOptions{
		Subject: "alice", ClientID: "codex", Issuer: "https://issuer.example", Scopes: []string{"mcp", "tools.read"},
		Claims: map[string]any{"exp": int64(2_000_000_000), "tenant": "acme", "groups": []string{"sre", "ops"}},
	})
	machine := contexture.NewPrincipal(contexture.PrincipalOptions{
		ClientID: "machine-client", Issuer: "https://issuer.example", Scopes: []string{"mcp"},
		Claims: map[string]any{"exp": int64(2_000_000_000), "tenant": "automation", "groups": []string{}},
	})
	authentication := &server.Auth{Verifier: tokenVerifier(func(_ context.Context, token string) (*contexture.Principal, error) {
		switch token {
		case "valid":
			return valid, nil
		case "machine":
			return machine, nil
		default:
			return nil, nil
		}
	}), Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp", RequiredScopes: []string{"mcp"}}
	go func() { errs <- assembly.ServeListenerWithAuth(ctx, listener, options, authentication) }()
	endpoint := "http://" + listener.Addr().String() + "/mcp"

	invoke := func(token string) (map[string]any, error) {
		client := mcp.NewClient(&mcp.Implementation{Name: "principal-client-" + token, Version: "0.0.0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: rootHeaderTransport{token: token}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			return nil, err
		}
		defer session.Close()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.InvokeReadOnlyGatewayName), Arguments: map[string]any{"ref": "identity/whoami", "arguments": map[string]any{}}})
		if err != nil {
			return nil, err
		}
		if result.IsError {
			return nil, fmt.Errorf("MCP Tool result is an error: %#v", result.Content)
		}
		payload, ok := result.StructuredContent.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("identity Tool structured content = %#v", result.StructuredContent)
		}
		return payload, nil
	}

	observed, err := invoke("valid")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if observed["subject"] != "alice" || observed["client_id"] != "codex" || observed["issuer"] != "https://issuer.example" || observed["tenant"] != "acme" || !reflect.DeepEqual(observed["scopes"], []any{"mcp", "tools.read"}) || !reflect.DeepEqual(observed["groups"], []any{"sre", "ops"}) {
		cancel()
		t.Fatalf("verified Principal did not survive SDK/gateway/runtime round trip: %#v", observed)
	}
	machineObserved, err := invoke("machine")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if machineObserved["subject"] != "" || machineObserved["client_id"] != "machine-client" || machineObserved["tenant"] != "automation" {
		cancel()
		t.Fatalf("machine Principal lost its native absent-subject facts: %#v", machineObserved)
	}
	if _, err := invoke("rejected"); err == nil {
		cancel()
		t.Fatal("rejected bearer token established an MCP invocation")
	}

	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("authenticated streamable HTTP server did not stop after cancellation")
	}
}

type launchChannels struct {
	contexture.ChannelsLifecycle
	mu             sync.Mutex
	opened, closed int
	live           bool
}

func (channels *launchChannels) Open(context.Context, contexture.CleanupRegistrar) error {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	channels.opened++
	channels.live = true
	return nil
}

func (channels *launchChannels) Close(context.Context) error {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	channels.closed++
	channels.live = false
	return nil
}

func (channels *launchChannels) isLive() bool {
	_, _, live := channels.snapshot()
	return live
}

func (channels *launchChannels) snapshot() (int, int, bool) {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	return channels.opened, channels.closed, channels.live
}
