package model

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
	return newToolWithSchema(name, description, readOnly, typeOf, schema, false, handler)
}

// NewToolWithSchema couples an explicit JSON Schema to the same validated,
// typed invocation Binding. It covers constraints that Go reflection cannot
// express directly, such as a string enum.
func NewToolWithSchema[I any, O any](name, description string, readOnly bool, inputSchema map[string]any, handler func(context.Context, I) (O, error)) (*Tool, error) {
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
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal explicit Tool schema: %v", ErrInvalidDeclaration, err)
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		return nil, fmt.Errorf("%w: decode explicit Tool schema: %v", ErrInvalidDeclaration, err)
	}
	if err := validateExplicitSchema(contract, typeOf); err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("%w: decode explicit Tool schema: %v", ErrInvalidDeclaration, err)
	}
	return newToolWithSchema(name, description, readOnly, typeOf, &schema, true, handler)
}

func newToolWithSchema[I any, O any](name, description string, readOnly bool, typeOf reflect.Type, schema *jsonschema.Schema, preserveAdditionalProperties bool, handler func(context.Context, I) (O, error)) (*Tool, error) {
	validator, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("resolve Tool schema: %w", err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal Tool schema: %w", err)
	}
	var rendered map[string]any
	if err := json.Unmarshal(raw, &rendered); err != nil {
		return nil, fmt.Errorf("decode Tool schema: %w", err)
	}
	normalizeSchema(rendered, typeOf, preserveAdditionalProperties)
	return &Tool{Name: name, Description: description, ReadOnly: readOnly, binding: &typedBinding[I, O]{schema: rendered, validator: validator, handler: handler}}, nil
}

func normalizeSchema(schema map[string]any, input reflect.Type, preserveAdditionalProperties bool) {
	normalizeSchemaValue(schema, preserveAdditionalProperties)
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

func normalizeSchemaValue(value any, preserveAdditionalProperties bool) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "$schema")
		delete(typed, "title")
		if additional, ok := typed["additionalProperties"].(bool); ok && !additional && !preserveAdditionalProperties {
			delete(typed, "additionalProperties")
		}
		for _, child := range typed {
			normalizeSchemaValue(child, preserveAdditionalProperties)
		}
	case []any:
		for _, child := range typed {
			normalizeSchemaValue(child, preserveAdditionalProperties)
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
	schema    map[string]any
	validator *jsonschema.Resolved
	handler   func(context.Context, I) (O, error)
}

func (binding *typedBinding[I, O]) Schema() map[string]any { return cloneJSON(binding.schema) }

func (binding *typedBinding[I, O]) Call(ctx context.Context, arguments json.RawMessage) (any, error) {
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage("{}")
	}
	var rawInput any
	if err := json.Unmarshal(arguments, &rawInput); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if binding.validator != nil {
		if err := binding.validator.Validate(&rawInput); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
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
	_, err := inputJSONFields(t)
	return err
}

func requireJSONFields(arguments json.RawMessage, t reflect.Type) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &object); err != nil {
		return fmt.Errorf("%w: input must be a JSON object", ErrInvalidInput)
	}
	fields, err := inputJSONFields(t)
	if err != nil {
		return err
	}
	for _, field := range fields {
		if field.optional {
			continue
		}
		if _, ok := object[field.name]; !ok {
			return fmt.Errorf("%w: required field %q is missing", ErrInvalidInput, field.name)
		}
	}
	return nil
}

type jsonInputField struct {
	name     string
	optional bool
}

// inputJSONFields is the single source of truth for the transport fields that
// encoding/json can decode into a Tool input.  json tags permit arbitrary
// options, so omitempty and omitzero must be found rather than assumed to be
// the first option.
func inputJSONFields(t reflect.Type) ([]jsonInputField, error) {
	fields := make([]jsonInputField, 0, t.NumField())
	seen := make(map[string]struct{}, t.NumField())
	for _, field := range reflect.VisibleFields(t) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		if tag == "" || parts[0] == "" || parts[0] == "-" {
			return nil, fmt.Errorf("%w: input field %s requires an explicit json tag", ErrInvalidDeclaration, field.Name)
		}
		if _, duplicate := seen[parts[0]]; duplicate {
			return nil, fmt.Errorf("%w: input fields cannot share json tag %q", ErrInvalidDeclaration, parts[0])
		}
		seen[parts[0]] = struct{}{}
		optional := false
		for _, option := range parts[1:] {
			if option == "omitempty" || option == "omitzero" {
				optional = true
				break
			}
		}
		fields = append(fields, jsonInputField{name: parts[0], optional: optional})
	}
	return fields, nil
}

// validateExplicitSchema prevents callers from publishing a Tool contract
// whose top-level JSON acceptance set differs from encoding/json.  Detailed
// property constraints remain JSON Schema's responsibility; this check owns
// the object shape, required fields, and unknown-field policy.
func validateExplicitSchema(schema map[string]any, input reflect.Type) error {
	if schema["type"] != "object" {
		return fmt.Errorf("%w: explicit Tool schema root type must be object", ErrInvalidDeclaration)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: explicit Tool schema properties must be an object", ErrInvalidDeclaration)
	}
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		return fmt.Errorf("%w: explicit Tool schema must set additionalProperties to false because Tool decoding rejects unknown fields", ErrInvalidDeclaration)
	}
	fields, err := inputJSONFields(input)
	if err != nil {
		return err
	}
	if len(properties) != len(fields) {
		return fmt.Errorf("%w: explicit Tool schema properties must exactly match input json fields", ErrInvalidDeclaration)
	}
	required, err := requiredSchemaFields(schema["required"])
	if err != nil {
		return err
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		if _, duplicate := requiredSet[name]; duplicate {
			return fmt.Errorf("%w: explicit Tool schema required cannot repeat %q", ErrInvalidDeclaration, name)
		}
		requiredSet[name] = struct{}{}
	}
	for _, field := range fields {
		if _, exists := properties[field.name]; !exists {
			return fmt.Errorf("%w: explicit Tool schema is missing property %q", ErrInvalidDeclaration, field.name)
		}
		_, required := requiredSet[field.name]
		if required == field.optional {
			return fmt.Errorf("%w: explicit Tool schema required fields must match input json tags", ErrInvalidDeclaration)
		}
		delete(requiredSet, field.name)
	}
	if len(requiredSet) != 0 {
		return fmt.Errorf("%w: explicit Tool schema required contains an unknown input field", ErrInvalidDeclaration)
	}
	return nil
}

func requiredSchemaFields(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: explicit Tool schema required must be an array of strings", ErrInvalidDeclaration)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: explicit Tool schema required must be an array of strings", ErrInvalidDeclaration)
		}
		result = append(result, name)
	}
	return result, nil
}

func cloneJSON(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}
