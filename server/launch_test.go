package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
