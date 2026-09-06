package server_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type telemetryWireInput struct{}

func TestCompiledServerSharesTelemetryAcrossGatewayOpenAndInvoke(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	status, err := contexture.NewTool("status", "Read status.", true, func(ctx context.Context, _ telemetryWireInput) (string, error) {
		if contexture.CurrentTelemetry(ctx) != collector {
			return "", context.Canceled
		}
		return "healthy", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "telemetry-wire", Telemetry: collector,
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return status }}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil || compiled.Telemetry != collector || compiled.Runtime.Telemetry() != collector {
		t.Fatalf("compiled telemetry = %#v, %v", compiled, err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(server.Identity{Name: "telemetry-wire", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "telemetry-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := adapter.Server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	opened, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.OpenGatewayName), Arguments: map[string]any{"ref": "operations"}})
	if err != nil || opened.IsError {
		t.Fatalf("gateway open = %#v, %v", opened, err)
	}
	invoked, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.InvokeReadOnlyGatewayName), Arguments: map[string]any{"ref": "operations/status", "arguments": map[string]any{}}})
	if err != nil || invoked.IsError || invoked.StructuredContent != "healthy" {
		t.Fatalf("gateway invocation = %#v, %v", invoked, err)
	}
	for _, ref := range []string{"operations", "operations/status"} {
		usage := collector.Usage(ref)
		if usage.CallCount != 1 || usage.ErrorCount != 0 || usage.LastUsedAt == "" {
			t.Fatalf("server usage %q = %#v", ref, usage)
		}
	}
}
