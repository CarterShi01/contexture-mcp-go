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

type nestedInput struct {
	Name string `json:"name"`
}

type corpusInput struct {
	Mode    string            `json:"mode"`
	Count   int               `json:"count"`
	Ratio   float64           `json:"ratio"`
	Enabled *bool             `json:"enabled,omitempty"`
	Nested  nestedInput       `json:"nested"`
	Values  []string          `json:"values"`
	Labels  map[string]string `json:"labels"`
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

func TestToolBindingSchemaCorpusValidatesBeforeTheHandler(t *testing.T) {
	calls := 0
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"mode":    map[string]any{"type": "string", "enum": []any{"safe", "force"}},
			"count":   map[string]any{"type": "integer"},
			"ratio":   map[string]any{"type": "number"},
			"enabled": map[string]any{"type": []any{"boolean", "null"}},
			"nested":  map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}, "additionalProperties": false},
			"values":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"labels":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		},
		"required": []any{"mode", "count", "ratio", "nested", "values", "labels"},
	}
	tool, err := contexture.NewToolWithSchema("corpus", "Corpus.", true, schema, func(context.Context, corpusInput) (string, error) { calls++; return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	invalid := []json.RawMessage{
		json.RawMessage(`{"mode":"unsafe","count":1,"ratio":1.5,"nested":{"name":"n"},"values":["x"],"labels":{"a":"b"}}`),
		json.RawMessage(`{"mode":"safe","count":1.2,"ratio":1.5,"nested":{"name":"n"},"values":["x"],"labels":{"a":"b"}}`),
		json.RawMessage(`{"mode":"safe","count":1,"ratio":1.5,"nested":{"name":4},"values":["x"],"labels":{"a":"b"}}`),
		json.RawMessage(`{"mode":"safe","count":1,"ratio":1.5,"nested":{"name":"n"},"values":[4],"labels":{"a":"b"}}`),
		json.RawMessage(`{"mode":"safe","count":1,"ratio":1.5,"nested":{"name":"n"},"values":["x"],"labels":{"a":4}}`),
	}
	for _, arguments := range invalid {
		if _, err := binding.Call(context.Background(), arguments); !errors.Is(err, contexture.ErrInvalidInput) {
			t.Fatalf("invalid corpus arguments = %s: %v", arguments, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid corpus calls = %d", calls)
	}
	value, err := binding.Call(context.Background(), json.RawMessage(`{"mode":"force","count":1,"ratio":1.5,"enabled":null,"nested":{"name":"n"},"values":["x"],"labels":{"a":"b"}}`))
	if err != nil || value != "ok" || calls != 1 {
		t.Fatalf("valid corpus = %#v, %v, calls %d", value, err, calls)
	}
}
