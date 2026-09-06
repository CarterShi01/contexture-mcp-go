package server_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestNewMCPServerUsesOfficialSDK(t *testing.T) {
	t.Parallel()

	mcpServer := server.NewMCPServer(server.Identity{
		Name:    "contexture-test",
		Version: "0.0.0",
	})
	if mcpServer == nil {
		t.Fatal("NewMCPServer() returned nil")
	}
}

type serverInput struct {
	Value string `json:"value"`
}

func TestNewContextureMCPServerExposesOnlyGatewayTools(t *testing.T) {
	tool, err := contexture.NewTool("business", "Business.", true, func(context.Context, serverInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "server", Roots: []contexture.Factory{func() contexture.Node { return tool }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(server.Identity{Name: "contexture-test", Version: "0.0.0"}, gateway)
	if adapter.Server == nil || len(adapter.GatewayNames) != 4 {
		t.Fatalf("adapter = %#v", adapter)
	}
	want := []contexture.GatewayName{contexture.DiscoverGatewayName, contexture.OpenGatewayName, contexture.InvokeReadOnlyGatewayName, contexture.InvokeGatewayName}
	for position, name := range want {
		if adapter.GatewayNames[position] != name {
			t.Fatalf("GatewayNames = %#v", adapter.GatewayNames)
		}
	}
}
