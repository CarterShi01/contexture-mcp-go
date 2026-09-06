package contexture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// ErrInvalidInput identifies arguments that do not satisfy a Tool Binding.
var ErrInvalidInput = errors.New("invalid Contexture tool arguments")

// Binding owns one Tool's disclosed schema and validated invocation path.
type Binding interface {
	Schema() map[string]any
	Call(context.Context, json.RawMessage) (any, error)
}

// NewTool couples one tagged input struct, its JSON Schema, decoder, and handler.
func NewTool[I any, O any](name, description string, readOnly bool, handler func(context.Context, I) (O, error)) (*Tool, error) {
	if handler == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("tool handler must not be nil"))
	}
	typeOf := reflect.TypeFor[I]()
	if typeOf.Kind() != reflect.Struct {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("tool input must be a struct"))
	}
	if err := requireJSONTags(typeOf); err != nil {
		return nil, err
	}
	schema, err := jsonschema.For[I](nil)
	if err != nil {
		return nil, fmt.Errorf("derive Tool schema: %w", err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal Tool schema: %w", err)
	}
	var rendered map[string]any
	if err := json.Unmarshal(raw, &rendered); err != nil {
		return nil, fmt.Errorf("decode Tool schema: %w", err)
	}
	normalizeSchema(rendered, typeOf)
	return &Tool{Name: name, Description: description, ReadOnly: readOnly, binding: &typedBinding[I, O]{schema: rendered, handler: handler}}, nil
}

func normalizeSchema(schema map[string]any, input reflect.Type) {
	normalizeSchemaValue(schema)
	if schema["type"] == "object" && schema["properties"] == nil {
		schema["properties"] = map[string]any{}
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range reflect.VisibleFields(input) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		property, _ := properties[name].(map[string]any)
		if property == nil {
			continue
		}
		if defaultValue, ok := field.Tag.Lookup("default"); ok {
			var decoded any
			if json.Unmarshal([]byte(defaultValue), &decoded) == nil {
				property["default"] = decoded
			}
		}
	}
}

func normalizeSchemaValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "$schema")
		delete(typed, "title")
		if additional, ok := typed["additionalProperties"].(bool); ok && !additional {
			delete(typed, "additionalProperties")
		}
		for _, child := range typed {
			normalizeSchemaValue(child)
		}
	case []any:
		for _, child := range typed {
			normalizeSchemaValue(child)
		}
	}
}

// Binding returns the compiled Tool Binding, if this Tool is executable.
func (tool *Tool) Binding() (Binding, error) {
	if tool.binding == nil {
		return nil, errors.New("Tool has no execution Binding")
	}
	return tool.binding, nil
}

type typedBinding[I any, O any] struct {
	schema  map[string]any
	handler func(context.Context, I) (O, error)
}

func (binding *typedBinding[I, O]) Schema() map[string]any { return cloneJSON(binding.schema) }

func (binding *typedBinding[I, O]) Call(ctx context.Context, arguments json.RawMessage) (any, error) {
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	var input I
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: multiple JSON values", ErrInvalidInput)
	}
	if err := requireJSONFields(arguments, reflect.TypeFor[I]()); err != nil {
		return nil, err
	}
	return binding.handler(ctx, input)
}

func requireJSONTags(t reflect.Type) error {
	for _, field := range reflect.VisibleFields(t) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "" || strings.Split(tag, ",")[0] == "" || strings.Split(tag, ",")[0] == "-" {
			return fmt.Errorf("%w: input field %s requires an explicit json tag", ErrInvalidDeclaration, field.Name)
		}
	}
	return nil
}

func requireJSONFields(arguments json.RawMessage, t reflect.Type) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &object); err != nil {
		return fmt.Errorf("%w: input must be a JSON object", ErrInvalidInput)
	}
	for _, field := range reflect.VisibleFields(t) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		parts := strings.Split(field.Tag.Get("json"), ",")
		if len(parts) > 1 && (parts[1] == "omitempty" || parts[1] == "omitzero") {
			continue
		}
		if _, ok := object[parts[0]]; !ok {
			return fmt.Errorf("%w: required field %q is missing", ErrInvalidInput, parts[0])
		}
	}
	return nil
}

func cloneJSON(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}
