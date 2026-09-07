package server_test

import (
	"context"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gatewayWireInput struct {
	Service string `json:"service"`
}

type explicitWireInput struct {
	Name   string `json:"name"`
	Filter string `json:"filter,omitempty"`
}

func TestOfficialSDKExposesOnlyFixedGatewayAndPreservesInvocationDoors(t *testing.T) {
	read, err := contexture.NewTool("read-status", "Read status.", true, func(context.Context, struct{}) (string, error) {
		return "healthy", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart a service.", false, func(_ context.Context, input gatewayWireInput) (string, error) {
		return "restarted " + input.Service, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "gateway-wire",
		Roots: []contexture.Factory{
			func() contexture.Node { return read },
			func() contexture.Node { return write },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	// Install Publications too: the open route must still delegate ordinary
	// missing refs to Gateway.Open's recovery layer.
	adapter := server.NewContextureMCPServer(server.Identity{Name: "gateway-test", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-test-client", Version: "0.0.0"}, nil)
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

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("tools = %#v", listed.Tools)
	}
	wantReadOnly := map[string]bool{
		"contexture_discover":         true,
		"contexture_open":             true,
		"contexture_invoke_read_only": true,
		"contexture_invoke":           false,
	}
	for _, tool := range listed.Tools {
		readOnly, known := wantReadOnly[tool.Name]
		if !known || tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Fatalf("tool = %#v", tool)
		}
		delete(wantReadOnly, tool.Name)
	}
	if len(wantReadOnly) != 0 {
		t.Fatalf("gateway tool list misses %#v", wantReadOnly)
	}

	discover, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_discover", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	disclosed, ok := discover.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("discover structured content = %#v", discover.StructuredContent)
	}
	tools, ok := disclosed["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("discover tools = %#v", disclosed["tools"])
	}

	readResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_invoke_read_only", Arguments: map[string]any{"ref": "read-status", "arguments": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if readResult.IsError || readResult.StructuredContent != "healthy" {
		t.Fatalf("read result = %#v", readResult)
	}
	writeResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_invoke", Arguments: map[string]any{"ref": "restart", "arguments": map[string]any{"service": "api"}}})
	if err != nil {
		t.Fatal(err)
	}
	if writeResult.IsError || writeResult.StructuredContent != "restarted api" {
		t.Fatalf("write result = %#v", writeResult)
	}
	wrongDoor, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_invoke", Arguments: map[string]any{"ref": "read-status", "arguments": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !wrongDoor.IsError || len(wrongDoor.Content) != 1 || !strings.Contains(textOf(wrongDoor.Content[0]), "read-only") {
		t.Fatalf("wrong door result = %#v", wrongDoor)
	}
	// The official SDK wire receives the same finished recovery instruction as
	// a direct Gateway caller; business tools never become top-level entries.
	missing, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_open", Arguments: map[string]any{"ref": "missing"}})
	if err != nil || !missing.IsError || len(missing.Content) != 1 || !strings.Contains(textOf(missing.Content[0]), "contexture_discover") {
		t.Fatalf("missing-ref recovery = %#v, %v", missing, err)
	}
}

func TestOfficialSDKCarriesExplicitToolSchemaIntoDisclosureAndRuntime(t *testing.T) {
	calls := 0
	tool, err := contexture.NewToolWithSchema("lookup", "Look up one item.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"name":   map[string]any{"type": "string"},
			"filter": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}, func(_ context.Context, input explicitWireInput) (string, error) {
		calls++
		return input.Name + ":" + input.Filter, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "explicit-wire", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(server.Identity{Name: "explicit-wire", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "explicit-wire-client", Version: "0.0.0"}, nil)
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

	opened, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_open", Arguments: map[string]any{"ref": "operations"}})
	if err != nil || opened.IsError {
		t.Fatalf("open = %#v, %v", opened, err)
	}
	openCard, ok := opened.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("open structured content = %#v", opened.StructuredContent)
	}
	tools, ok := openCard["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("open tools = %#v", openCard["tools"])
	}
	toolCard, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool card = %#v", tools[0])
	}
	schema, ok := toolCard["input_schema"].(map[string]any)
	if !ok || schema["additionalProperties"] != false {
		t.Fatalf("wire input schema = %#v", toolCard["input_schema"])
	}

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_invoke_read_only", Arguments: map[string]any{"ref": "operations/lookup", "arguments": map[string]any{"name": "Ada"}}})
	if err != nil || result.IsError || result.StructuredContent != "Ada:" {
		t.Fatalf("valid invocation = %#v, %v", result, err)
	}
	invalid, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_invoke_read_only", Arguments: map[string]any{"ref": "operations/lookup", "arguments": map[string]any{"name": "Ada", "unknown": true}}})
	if err != nil || !invalid.IsError || calls != 1 {
		t.Fatalf("invalid invocation = %#v, %v; calls = %d", invalid, err, calls)
	}
}

func TestOfficialSDKProjectsRoleUsesCards(t *testing.T) {
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "role-uses-wire", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Coordinate.", Instructions: "Open the declared dependency.", Uses: []string{"status/status"}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "status", Description: "Status.", Instructions: "Read status.", Tools: []contexture.Factory{func() contexture.Node { return status }}}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(server.Identity{Name: "role-uses-wire", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "role-uses-wire-client", Version: "0.0.0"}, nil)
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

	opened, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "contexture_open", Arguments: map[string]any{"ref": "operations"}})
	if err != nil || opened.IsError {
		t.Fatalf("open role = %#v, %v", opened, err)
	}
	payload, ok := opened.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("role structured content = %#v", opened.StructuredContent)
	}
	uses, ok := payload["uses"].([]any)
	if !ok || len(uses) != 1 {
		t.Fatalf("role uses wire payload = %#v", payload["uses"])
	}
	card, ok := uses[0].(map[string]any)
	if !ok || card["ref"] != "status/status" || card["kind"] != "tool" {
		t.Fatalf("role uses wire card = %#v", uses[0])
	}
}

func textOf(content mcp.Content) string {
	if text, ok := content.(*mcp.TextContent); ok {
		return text.Text
	}
	return ""
}
