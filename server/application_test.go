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
}
