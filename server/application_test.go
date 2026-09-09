package server_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type applicationInput struct {
	Value string `json:"value"`
}

func TestCompileApplicationSharesOneBoundRuntimeSurface(t *testing.T) {
	tool, err := contexture.NewTool("read", "Read one value.", true, func(_ context.Context, input applicationInput) (string, error) {
		return "read:" + input.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "application",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "assistant", Description: "Answer requests.", Instructions: "Read first.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Index == nil || compiled.Disclosure == nil || compiled.Runtime == nil || compiled.Publications == nil {
		t.Fatalf("compiled application = %#v", compiled)
	}
	value, err := compiled.Runtime.InvokeReadOnly(context.Background(), "assistant/read", []byte(`{"value":"ok"}`), contexture.AllRoots())
	if err != nil || value != "read:ok" {
		t.Fatalf("InvokeReadOnly() = %#v, %v", value, err)
	}
	if _, err := compiled.Gateway(); err != nil {
		t.Fatal(err)
	}
	container, err := server.BuildServer(declaration)
	if err != nil {
		t.Fatal(err)
	}
	first, err := container.Build()
	if err != nil {
		t.Fatal(err)
	}
	second, err := container.Build()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("Build() constructed more than one default MCP adapter")
	}
}

func TestCompileDisclosureApplicationBuildsNavigationOnlyContainer(t *testing.T) {
	tool, err := contexture.NewTool("provider", "Read provider.", true, func(context.Context, applicationInput) (string, error) { return "never bound", nil })
	if err != nil {
		t.Fatal(err)
	}
	collector := contexture.NewMemoryTelemetry()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "architecture", Telemetry: collector,
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "architecture", Description: "Architecture.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
		}},
		Prompts: []contexture.Prompt{{Opens: "architecture", Description: "Review architecture."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileDisclosureApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Index.Bound() || compiled.Telemetry != collector || compiled.Disclosure.Index() != compiled.Index || len(compiled.Publications.ResourceCards(contexture.AllRoots())) != 0 {
		t.Fatalf("structural container = %#v", compiled)
	}
	opened, err := compiled.Disclosure.Open("architecture", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	card := opened["tools"].([]map[string]any)[0]
	if card["read_only"] != nil || card["input_schema"] != nil {
		t.Fatalf("structural Tool card leaked execution facts: %#v", card)
	}
	adapter, err := compiled.Server()
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.GatewayNames) != 2 || adapter.GatewayNames[0] != contexture.DiscoverGatewayName || adapter.GatewayNames[1] != contexture.OpenGatewayName {
		t.Fatalf("structural gateway = %#v", adapter.GatewayNames)
	}
	if _, err := contexture.NewRuntime(compiled.Index, contexture.AllRoots(), contexture.AllRoots(), nil); err == nil {
		t.Fatal("unbound Index upgraded into Runtime")
	}
}
