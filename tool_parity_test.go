package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type toolParityInput struct {
	Value string `json:"value"`
}

func TestToolConstructorsRejectInvalidIdentityFactsImmediately(t *testing.T) {
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"value": map[string]any{"type": "string"}},
		"required":   []any{"value"},
	}
	for _, test := range []struct {
		name, toolName, description string
	}{
		{name: "blank name", toolName: " ", description: "Describe."},
		{name: "separator name", toolName: "bad/name", description: "Describe."},
		{name: "blank description", toolName: "valid", description: " \t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := contexture.NewTool(test.toolName, test.description, true, func(context.Context, toolParityInput) (string, error) { return "unexpected", nil }); !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("NewTool error = %v", err)
			}
			if _, err := contexture.NewToolWithSchema(test.toolName, test.description, true, schema, func(context.Context, toolParityInput) (string, error) { return "unexpected", nil }); !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("NewToolWithSchema error = %v", err)
			}
		})
	}
}

func TestToolBindingAbsenceIsAClassifiedDeclarationFailure(t *testing.T) {
	structural := &contexture.Tool{Name: "structural", Description: "Structural card."}
	if structural.ReadOnly {
		t.Fatal("a raw Tool must retain Go's false (writing) ReadOnly default")
	}
	for _, tool := range []*contexture.Tool{nil, structural} {
		if _, err := tool.Binding(); !errors.Is(err, contexture.ErrInvalidDeclaration) || !errors.Is(err, contexture.ErrDeclaration) {
			t.Fatalf("Binding(%#v) = %v", tool, err)
		}
	}
}

func TestCompiledToolPreservesIdentityUsesReadOnlyAndSchemaSnapshots(t *testing.T) {
	entry, err := contexture.NewTool("entry", "Read the entry.", true, func(context.Context, toolParityInput) (string, error) { return "entry", nil })
	if err != nil {
		t.Fatal(err)
	}
	target, err := contexture.NewTool("target", "Read the target.", true, func(context.Context, toolParityInput) (string, error) { return "target", nil })
	if err != nil {
		t.Fatal(err)
	}
	entry.Uses = []string{"target"}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "tool-snapshot", Roots: []contexture.Factory{
		func() contexture.Node { return entry },
		func() contexture.Node { return target },
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	entry.Name, entry.Description, entry.ReadOnly, entry.Uses[0] = "forged", "Forged.", false, "forged"

	compiled, err := index.Tool("entry")
	if err != nil || compiled.Name != "entry" || compiled.Description != "Read the entry." || !compiled.ReadOnly || !reflect.DeepEqual(compiled.NodeUses(), []string{"target"}) {
		t.Fatalf("compiled Tool snapshot = %#v, %v", compiled, err)
	}
	usesSnapshot := compiled.NodeUses()
	usesSnapshot[0] = "forged"
	if got := compiled.NodeUses(); !reflect.DeepEqual(got, []string{"target"}) {
		t.Fatalf("compiled Tool Uses exposed a mutable snapshot: %#v", got)
	}
	binding, err := compiled.Binding()
	if err != nil {
		t.Fatal(err)
	}
	firstSchema := binding.Schema()
	firstSchema["properties"] = map[string]any{"forged": map[string]any{"type": "boolean"}}
	secondSchema := binding.Schema()
	if _, present := secondSchema["properties"].(map[string]any)["value"]; !present {
		t.Fatalf("Binding Schema exposed mutable snapshot: %#v", secondSchema)
	}

	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := disclosure.Open("entry", contexture.AllRoots())
	if err != nil || opened["name"] != "entry" || opened["description"] != "Read the entry." || opened["read_only"] != true {
		t.Fatalf("compiled Tool disclosure = %#v, %v", opened, err)
	}
	uses, ok := opened["uses"].([]map[string]any)
	if !ok || len(uses) != 1 || uses[0]["ref"] != "target" {
		t.Fatalf("compiled Tool uses = %#v", opened["uses"])
	}
}

func TestToolReadOnlyClassificationAndSelfUsesAreEnforcedAtRuntimeAndCompile(t *testing.T) {
	write, err := contexture.NewTool("change", "Change one value.", false, func(_ context.Context, input toolParityInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "tool-door", Roots: []contexture.Factory{func() contexture.Node { return write }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InvokeReadOnly(context.Background(), "change", json.RawMessage(`{"value":"ok"}`), contexture.AllRoots()); !errors.Is(err, contexture.ErrWrongDoor) {
		t.Fatalf("writing Tool read-only invocation = %v", err)
	}
	if value, err := runtime.Invoke(context.Background(), "change", json.RawMessage(`{"value":"ok"}`), contexture.AllRoots()); err != nil || value != "ok" {
		t.Fatalf("writing Tool invocation = %#v, %v", value, err)
	}

	self, err := contexture.NewTool("self", "Try to use itself.", true, func(context.Context, toolParityInput) (string, error) { return "unexpected", nil })
	if err != nil {
		t.Fatal(err)
	}
	self.Uses = []string{"self"}
	selfApplication, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "tool-self-use", Roots: []contexture.Factory{func() contexture.Node { return self }}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(selfApplication); !errors.Is(err, contexture.ErrInvalidDeclaration) || errors.Is(err, contexture.ErrUnresolvedReference) {
		t.Fatalf("Tool self Uses compile = %v", err)
	}
}
