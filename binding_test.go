package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type toolInput struct {
	Service string            `json:"service"`
	Retries *int              `json:"retries,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

func TestToolBindingSharesSchemaValidationAndHandler(t *testing.T) {
	calls := 0
	tool, err := contexture.NewTool("inspect", "Inspect one service.", true, func(_ context.Context, input toolInput) (toolInput, error) {
		calls++
		return input, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	schema := binding.Schema()
	if schema["type"] != "object" {
		t.Fatalf("schema type = %#v", schema["type"])
	}
	if _, ok := schema["properties"].(map[string]any)["service"]; !ok {
		t.Fatalf("schema omits service: %#v", schema)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{"service":"api","extra":true}`)); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("Call error = %v, want invalid input", err)
	}
	if calls != 0 {
		t.Fatalf("handler calls = %d, want 0", calls)
	}
	result, err := binding.Call(context.Background(), json.RawMessage(`{"service":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.(toolInput).Service != "api" || calls != 1 {
		t.Fatalf("result = %#v, calls = %d", result, calls)
	}
}

func TestToolBindingRequiresTaggedStructInput(t *testing.T) {
	_, err := contexture.NewTool("bad", "Bad.", true, func(context.Context, struct{ Value string }) (string, error) { return "", nil })
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("NewTool error = %v", err)
	}
}
