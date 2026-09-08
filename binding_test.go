package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
	Count   int32             `json:"count"`
	Ratio   float64           `json:"ratio"`
	Enabled *bool             `json:"enabled,omitempty"`
	Nested  nestedInput       `json:"nested"`
	Values  []string          `json:"values"`
	Labels  map[string]string `json:"labels"`
}

// optionOrderedInput keeps omitempty after another standard encoding/json
// option. The transport contract must not assume optionality is option #1.
type optionOrderedInput struct {
	Value string `json:"value,string,omitempty"`
	Other string `json:"other,string,omitzero"`
}

type strictInput struct {
	Name   string `json:"name"`
	Filter string `json:"filter,omitempty"`
}

type scalarInput struct {
	Count int32 `json:"count"`
}

type nestedStrictInput struct {
	Nested nestedInput `json:"nested"`
}

type collectionInput struct {
	Items  []int32          `json:"items"`
	Labels map[string]int32 `json:"labels"`
}

type defaultedInput struct {
	Previous *bool `json:"previous,omitempty" default:"false"`
}

type numericWidthsInput struct {
	Int     int     `json:"int"`
	Int8    int8    `json:"int8"`
	Int16   int16   `json:"int16"`
	Int32   int32   `json:"int32"`
	Int64   int64   `json:"int64"`
	Uint    uint    `json:"uint"`
	Uint8   uint8   `json:"uint8"`
	Uint16  uint16  `json:"uint16"`
	Uint32  uint32  `json:"uint32"`
	Uint64  uint64  `json:"uint64"`
	Float32 float32 `json:"float32"`
	Float64 float64 `json:"float64"`
}

type customScalar string

func (value *customScalar) UnmarshalJSON(raw []byte) error {
	if string(raw) != `"accepted"` {
		return errors.New("custom rejection")
	}
	*value = "accepted"
	return nil
}

type customDecoderInput struct {
	Value customScalar `json:"value"`
}

func strictInputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"name":   map[string]any{"type": "string"},
			"filter": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
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
	if schema["type"] != "object" || schema["additionalProperties"] != nil {
		t.Fatalf("schema type = %#v", schema["type"])
	}
	if _, ok := schema["properties"].(map[string]any)["service"]; !ok {
		t.Fatalf("schema omits service: %#v", schema)
	}
	result, err := binding.Call(context.Background(), json.RawMessage(`{"service":"api","extra":true}`))
	if err != nil || result.(toolInput).Service != "api" {
		t.Fatalf("unknown argument should follow the derived schema: %#v, %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("missing required argument error = %v, want invalid input", err)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{"service":"api"} {"extra":true}`)); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("trailing JSON value error = %v, want invalid input", err)
	}
	result, err = binding.Call(context.Background(), json.RawMessage(`{"service":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.(toolInput).Service != "api" || calls != 2 {
		t.Fatalf("result = %#v, calls = %d", result, calls)
	}
}

func TestNewToolMatchesTheDerivedSchemaUnknownFieldPolicyRecursively(t *testing.T) {
	calls := 0
	tool, err := contexture.NewTool("nested", "Nested.", true, func(context.Context, nestedStrictInput) (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	schema := binding.Schema()
	if schema["additionalProperties"] != nil {
		t.Fatalf("derived root schema unexpectedly advertises strict decoding: %#v", schema)
	}
	nested := schema["properties"].(map[string]any)["nested"].(map[string]any)
	if nested["additionalProperties"] != nil {
		t.Fatalf("derived nested schema unexpectedly advertises strict decoding: %#v", nested)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{"nested":{"name":"ok","unknown":true}}`)); err != nil {
		t.Fatalf("derived nested unknown field error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("nested unknown field handler calls = %d", calls)
	}
}

func TestExplicitSchemaDefaultsAreAppliedBeforeValidationAndDecoding(t *testing.T) {
	tool, err := contexture.NewToolWithSchema("defaults", "Defaults.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"previous": map[string]any{"type": []any{"boolean", "null"}, "default": false},
		},
		"required": []any{},
	}, func(_ context.Context, input defaultedInput) (any, error) {
		return input.Previous, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := binding.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || *previous.(*bool) {
		t.Fatalf("applied default = %#v, want pointer to false", previous)
	}

	derived, err := contexture.NewTool("derived-default-tag", "Ignore non-standard tags.", true, func(_ context.Context, input defaultedInput) (*bool, error) {
		return input.Previous, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	derivedBinding, err := derived.Binding()
	if err != nil {
		t.Fatal(err)
	}
	property := derivedBinding.Schema()["properties"].(map[string]any)["previous"].(map[string]any)
	if _, advertised := property["default"]; advertised {
		t.Fatalf("derived schema advertised a non-standard struct-tag default: %#v", property)
	}
	value, err := derivedBinding.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil || value.(*bool) != nil {
		t.Fatalf("derived omitted value = %#v, %v; want nil", value, err)
	}

	_, err = contexture.NewToolWithSchema("bad-default", "Bad default.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"previous": map[string]any{"type": []any{"boolean", "null"}, "default": "false"}},
		"required":   []any{},
	}, func(context.Context, defaultedInput) (string, error) { return "unexpected", nil })
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("invalid default declaration error = %v", err)
	}
}

func TestDerivedNumericSchemasMatchGoDecoderWidths(t *testing.T) {
	tool, err := contexture.NewTool("numbers", "Numbers.", true, func(_ context.Context, input numericWidthsInput) (numericWidthsInput, error) {
		return input, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	properties := binding.Schema()["properties"].(map[string]any)
	wantBounds := map[string]numericBoundsForTest{
		"int":     {minimum: -math.Ldexp(1, 63), exclusiveMaximum: math.Ldexp(1, 63)},
		"int8":    {minimum: math.MinInt8, maximum: float64(math.MaxInt8)},
		"int16":   {minimum: math.MinInt16, maximum: float64(math.MaxInt16)},
		"int32":   {minimum: math.MinInt32, maximum: float64(math.MaxInt32)},
		"int64":   {minimum: -math.Ldexp(1, 63), exclusiveMaximum: math.Ldexp(1, 63)},
		"uint":    {minimum: 0, exclusiveMaximum: math.Ldexp(1, 64)},
		"uint8":   {minimum: 0, maximum: float64(math.MaxUint8)},
		"uint16":  {minimum: 0, maximum: float64(math.MaxUint16)},
		"uint32":  {minimum: 0, maximum: float64(math.MaxUint32)},
		"uint64":  {minimum: 0, exclusiveMaximum: math.Ldexp(1, 64)},
		"float32": {minimum: -math.MaxFloat32, maximum: float64(math.MaxFloat32)},
		"float64": {minimum: -math.MaxFloat64, maximum: float64(math.MaxFloat64)},
	}
	for name, want := range wantBounds {
		property := properties[name].(map[string]any)
		if property["minimum"] != want.minimum || property["maximum"] != want.maximum || property["exclusiveMaximum"] != want.exclusiveMaximum {
			t.Errorf("%s bounds = minimum %v, maximum %v, exclusiveMaximum %v; want %#v", name, property["minimum"], property["maximum"], property["exclusiveMaximum"], want)
		}
	}

	valid := map[string]any{"int": 0, "int8": 0, "int16": 0, "int32": 0, "int64": 0, "uint": 0, "uint8": 0, "uint16": 0, "uint32": 0, "uint64": 0, "float32": 0, "float64": 0}
	raw, _ := json.Marshal(valid)
	if _, err := binding.Call(context.Background(), raw); err != nil {
		t.Fatalf("valid numeric input: %v", err)
	}
	exact, err := binding.Call(context.Background(), json.RawMessage(`{"int":9007199254740993,"int8":127,"int16":32767,"int32":2147483647,"int64":9223372036854775807,"uint":18446744073709551615,"uint8":255,"uint16":65535,"uint32":4294967295,"uint64":18446744073709551615,"float32":3.4028234663852886e38,"float64":1.7976931348623157e308}`))
	if err != nil {
		t.Fatalf("exact numeric extremes: %v", err)
	}
	extremes := exact.(numericWidthsInput)
	if extremes.Int != 9007199254740993 || extremes.Int64 != math.MaxInt64 || extremes.Uint != math.MaxUint64 || extremes.Uint64 != math.MaxUint64 {
		t.Fatalf("numeric precision lost: %#v", extremes)
	}
	invalid := map[string]string{
		"int": "9223372036854775808", "int8": "128", "int16": "32768", "int32": "2147483648", "int64": "9223372036854775808",
		"uint": "-1", "uint8": "256", "uint16": "65536", "uint32": "4294967296", "uint64": "18446744073709551616",
		"float32": "3.5e38", "float64": "1e400",
	}
	for name, number := range invalid {
		encoded := make(map[string]json.RawMessage, len(valid))
		for field := range valid {
			encoded[field] = json.RawMessage("0")
		}
		encoded[name] = json.RawMessage(number)
		arguments, _ := json.Marshal(encoded)
		if _, err := binding.Call(context.Background(), arguments); !errors.Is(err, contexture.ErrInvalidInput) {
			t.Errorf("overflowing %s accepted: %v", name, err)
		}
	}
	valid["int8"] = 1.5
	raw, _ = json.Marshal(valid)
	if _, err := binding.Call(context.Background(), raw); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("fractional integer error = %v, want invalid input", err)
	}
}

type numericBoundsForTest struct {
	minimum          float64
	maximum          any
	exclusiveMaximum any
}

func TestDerivedToolRejectsCustomJSONUnmarshalers(t *testing.T) {
	_, err := contexture.NewTool("custom", "Custom.", true, func(context.Context, customDecoderInput) (string, error) {
		return "unexpected", nil
	})
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("custom JSON decoder error = %v, want invalid declaration", err)
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
			"count":   map[string]any{"type": "integer", "minimum": float64(math.MinInt32), "maximum": float64(math.MaxInt32)},
			"ratio":   map[string]any{"type": "number", "minimum": -math.MaxFloat64, "maximum": math.MaxFloat64},
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

func TestNewToolWithSchemaRejectsTransportContractDriftAtDeclaration(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]any
	}{
		{"non-object root", map[string]any{"type": "array", "properties": map[string]any{}, "required": []any{}, "additionalProperties": false}},
		{"missing property", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}, "additionalProperties": false}},
		{"extra property", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "filter": map[string]any{"type": "string"}, "extra": map[string]any{"type": "string"}}, "required": []any{"name"}, "additionalProperties": false}},
		{"optional marked required", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "filter": map[string]any{"type": "string"}}, "required": []any{"name", "filter"}, "additionalProperties": false}},
		{"required omitted", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "filter": map[string]any{"type": "string"}}, "required": []any{}, "additionalProperties": false}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := contexture.NewToolWithSchema("strict", "Strict.", true, test.schema, func(context.Context, strictInput) (string, error) { return "unexpected", nil })
			if !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("NewToolWithSchema error = %v, want invalid declaration", err)
			}
		})
	}
}

func TestNewToolWithSchemaCanDiscloseAndStripUnknownFields(t *testing.T) {
	for _, additional := range []any{nil, true} {
		schema := map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}, "filter": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		}
		if additional != nil {
			schema["additionalProperties"] = additional
		}
		tool, err := contexture.NewToolWithSchema("permissive", "Permissive.", true, schema, func(_ context.Context, input strictInput) (strictInput, error) {
			return input, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		binding, err := tool.Binding()
		if err != nil {
			t.Fatal(err)
		}
		if binding.Schema()["additionalProperties"] != additional {
			t.Fatalf("additionalProperties = %#v, want %#v", binding.Schema()["additionalProperties"], additional)
		}
		value, err := binding.Call(context.Background(), json.RawMessage(`{"name":"Ada","unknown":true}`))
		if err != nil || value.(strictInput) != (strictInput{Name: "Ada"}) {
			t.Fatalf("permissive invocation = %#v, %v", value, err)
		}
	}
}

func TestNewToolWithSchemaRejectsScalarAndNestedDecodeDriftAtDeclaration(t *testing.T) {
	_, err := contexture.NewToolWithSchema("scalar", "Scalar.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"count": map[string]any{"type": "string"}},
		"required":   []any{"count"},
	}, func(context.Context, scalarInput) (string, error) { return "unexpected", nil })
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("scalar schema drift error = %v", err)
	}

	_, err = contexture.NewToolWithSchema("nested", "Nested.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"nested": map[string]any{
				"type": "object", "additionalProperties": true,
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"required":   []any{"name"},
			},
		},
		"required": []any{"nested"},
	}, func(context.Context, nestedStrictInput) (string, error) { return "unexpected", nil })
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("nested unknown-field drift error = %v", err)
	}
}

func TestNewToolWithSchemaRejectsUnsafeNumericArrayAndMapContractsAtDeclaration(t *testing.T) {
	base := func(items, labels map[string]any) map[string]any {
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"items": items, "labels": labels},
			"required":   []any{"items", "labels"},
		}
	}
	validInteger := map[string]any{"type": "integer", "minimum": float64(math.MinInt32), "maximum": float64(math.MaxInt32)}
	cases := []struct {
		name       string
		schema     map[string]any
		collection bool
	}{
		{"unbounded integer", map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"count": map[string]any{"type": "integer"}}, "required": []any{"count"}}, false},
		{"wider integer", map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": float64(math.MinInt32) - 1, "maximum": float64(math.MaxInt32)}}, "required": []any{"count"}}, false},
		{"array item scalar mismatch", base(map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, map[string]any{"type": "object", "additionalProperties": validInteger}), true},
		{"array missing items", base(map[string]any{"type": "array"}, map[string]any{"type": "object", "additionalProperties": validInteger}), true},
		{"map untyped extras", base(map[string]any{"type": "array", "items": validInteger}, map[string]any{"type": "object", "additionalProperties": true}), true},
		{"map pattern scalar mismatch", base(map[string]any{"type": "array", "items": validInteger}, map[string]any{"type": "object", "additionalProperties": false, "patternProperties": map[string]any{".*": map[string]any{"type": "string"}}}), true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.collection {
				_, err := contexture.NewToolWithSchema("collection", "Collection.", true, test.schema, func(context.Context, collectionInput) (string, error) { return "unexpected", nil })
				if !errors.Is(err, contexture.ErrInvalidDeclaration) {
					t.Fatalf("collection schema error = %v", err)
				}
				return
			}
			_, err := contexture.NewToolWithSchema("scalar", "Scalar.", true, test.schema, func(context.Context, scalarInput) (scalarInput, error) { return scalarInput{}, nil })
			if !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("numeric schema error = %v", err)
			}
		})
	}
}

func TestNewToolWithSchemaValidatesCollectionAcceptanceAtInvocation(t *testing.T) {
	integer := map[string]any{"type": "integer", "minimum": float64(math.MinInt32), "maximum": float64(math.MaxInt32)}
	tool, err := contexture.NewToolWithSchema("collection", "Collection.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"items":  map[string]any{"type": "array", "items": integer},
			"labels": map[string]any{"type": "object", "additionalProperties": integer, "patternProperties": map[string]any{"^safe-": integer}},
		},
		"required": []any{"items", "labels"},
	}, func(_ context.Context, input collectionInput) (int, error) {
		return len(input.Items) + len(input.Labels), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	value, err := binding.Call(context.Background(), json.RawMessage(`{"items":[1],"labels":{"safe-count":2}}`))
	if err != nil || value != 2 {
		t.Fatalf("valid collection invocation = %#v, %v", value, err)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{"items":["one"],"labels":{"safe-count":"two"}}`)); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("invalid collection invocation error = %v", err)
	}
}

func TestNewToolWithSchemaRejectsNestedUnknownFieldsBeforeTheHandler(t *testing.T) {
	calls := 0
	tool, err := contexture.NewToolWithSchema("nested", "Nested.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"nested": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"required":   []any{"name"},
			},
		},
		"required": []any{"nested"},
	}, func(context.Context, nestedStrictInput) (string, error) {
		calls++
		return "unexpected", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Call(context.Background(), json.RawMessage(`{"nested":{"name":"Ada","forged":true}}`)); !errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("nested unknown field error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("nested unknown field reached handler %d times", calls)
	}
}

func TestNewToolWithSchemaKeepsDisclosureAndInvocationAcceptanceSetsAligned(t *testing.T) {
	calls := 0
	tool, err := contexture.NewToolWithSchema("strict", "Strict.", true, strictInputSchema(), func(_ context.Context, input strictInput) (string, error) {
		calls++
		return input.Name + ":" + input.Filter, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "strict-binding", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
	}}})
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
	opened, err := disclosure.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	card := opened["tools"].([]map[string]any)[0]
	schema := card["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false || len(schema["properties"].(map[string]any)) != 2 || !containsString(schema["required"].([]any), "name") {
		t.Fatalf("disclosed schema = %#v", schema)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.InvokeReadOnly(context.Background(), "operations/strict", json.RawMessage(`{"name":"Ada"}`), contexture.AllRoots())
	if err != nil || result != "Ada:" || calls != 1 {
		t.Fatalf("valid invocation = %#v, %v; calls = %d", result, err, calls)
	}
	for _, arguments := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"name":"Ada","unknown":true}`)} {
		if _, err := runtime.InvokeReadOnly(context.Background(), "operations/strict", arguments, contexture.AllRoots()); !errors.Is(err, contexture.ErrInvalidInput) {
			t.Fatalf("invalid invocation %s = %v", arguments, err)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid input reached handler %d times", calls)
	}
}

func TestJSONTagOptionalityScansEveryOption(t *testing.T) {
	tool, err := contexture.NewToolWithSchema("ordered", "Ordered.", true, map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
			"other": map[string]any{"type": "string"},
		},
		"required": []any{},
	}, func(_ context.Context, input optionOrderedInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	binding, err := tool.Binding()
	if err != nil {
		t.Fatal(err)
	}
	value, err := binding.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil || value != "" {
		t.Fatalf("optional field invocation = %#v, %v", value, err)
	}
}

func containsString(items []any, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
