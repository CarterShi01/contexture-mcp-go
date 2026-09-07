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
	// The reference-derived schema omits additionalProperties, which means
	// unknown arguments are accepted. Keep the generated card and decoder on
	// that same policy; explicit schemas can deliberately opt into strictness.
	return newToolWithSchema(name, description, readOnly, typeOf, schema, false, false, handler)
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
	return newToolWithSchema(name, description, readOnly, typeOf, &schema, true, true, handler)
}

func newToolWithSchema[I any, O any](name, description string, readOnly bool, typeOf reflect.Type, schema *jsonschema.Schema, preserveAdditionalProperties, disallowUnknownFields bool, handler func(context.Context, I) (O, error)) (*Tool, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal Tool schema: %w", err)
	}
	var rendered map[string]any
	if err := json.Unmarshal(raw, &rendered); err != nil {
		return nil, fmt.Errorf("decode Tool schema: %w", err)
	}
	normalizeSchema(rendered, typeOf, preserveAdditionalProperties)
	validationSchema := schema
	if !disallowUnknownFields {
		// jsonschema-go derives additionalProperties:false for Go structs, but
		// the pinned Python/MCP producer omits it and accepts unknown arguments.
		// Resolve the displayed relaxed schema so validation and decoding agree.
		relaxedRaw, err := json.Marshal(rendered)
		if err != nil {
			return nil, fmt.Errorf("marshal relaxed Tool schema: %w", err)
		}
		validationSchema = new(jsonschema.Schema)
		if err := json.Unmarshal(relaxedRaw, validationSchema); err != nil {
			return nil, fmt.Errorf("decode relaxed Tool schema: %w", err)
		}
	}
	validator, err := validationSchema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("resolve Tool schema: %w", err)
	}
	return &Tool{Name: name, Description: description, ReadOnly: readOnly, binding: &typedBinding[I, O]{schema: rendered, validator: validator, disallowUnknownFields: disallowUnknownFields, handler: handler}}, nil
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
	schema                map[string]any
	validator             *jsonschema.Resolved
	disallowUnknownFields bool
	handler               func(context.Context, I) (O, error)
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
	if binding.disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
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
	name          string
	optional      bool
	typeOf        reflect.Type
	stringEncoded bool
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
		stringEncoded := false
		for _, option := range parts[1:] {
			if option == "omitempty" || option == "omitzero" {
				optional = true
			}
			if option == "string" {
				stringEncoded = true
			}
		}
		fields = append(fields, jsonInputField{name: parts[0], optional: optional, typeOf: field.Type, stringEncoded: stringEncoded})
	}
	return fields, nil
}

// validateExplicitSchema prevents callers from publishing a Tool contract
// whose JSON acceptance set differs from encoding/json. Detailed constraints
// may narrow an accepted scalar, but every schema branch must remain decodable
// by the tagged Go input type.
func validateExplicitSchema(schema map[string]any, input reflect.Type) error {
	return validateObjectSchema(schema, input, false, "explicit Tool schema")
}

func validateObjectSchema(schema map[string]any, input reflect.Type, nullable bool, location string) error {
	types, err := schemaTypes(schema, location)
	if err != nil {
		return err
	}
	if !containsSchemaType(types, "object") || !schemaTypesAreSubset(types, append(nullableTypes(nullable), "object")) {
		return fmt.Errorf("%w: %s must accept only a JSON object%s", ErrInvalidDeclaration, location, nullSuffix(nullable))
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s properties must be an object", ErrInvalidDeclaration, location)
	}
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		return fmt.Errorf("%w: %s must set additionalProperties to false because Tool decoding rejects unknown fields", ErrInvalidDeclaration, location)
	}
	fields, err := inputJSONFields(input)
	if err != nil {
		return err
	}
	if len(properties) != len(fields) {
		return fmt.Errorf("%w: %s properties must exactly match input json fields", ErrInvalidDeclaration, location)
	}
	required, err := requiredSchemaFields(schema["required"])
	if err != nil {
		return err
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		if _, duplicate := requiredSet[name]; duplicate {
			return fmt.Errorf("%w: %s required cannot repeat %q", ErrInvalidDeclaration, location, name)
		}
		requiredSet[name] = struct{}{}
	}
	for _, field := range fields {
		property, exists := properties[field.name]
		if !exists {
			return fmt.Errorf("%w: %s is missing property %q", ErrInvalidDeclaration, location, field.name)
		}
		_, required := requiredSet[field.name]
		if required == field.optional {
			return fmt.Errorf("%w: %s required fields must match input json tags", ErrInvalidDeclaration, location)
		}
		propertySchema, ok := property.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: %s property %q must be a JSON Schema object", ErrInvalidDeclaration, location, field.name)
		}
		if err := validateSchemaForType(propertySchema, field.typeOf, field.stringEncoded, location+" property "+fmt.Sprintf("%q", field.name)); err != nil {
			return err
		}
		delete(requiredSet, field.name)
	}
	if len(requiredSet) != 0 {
		return fmt.Errorf("%w: %s required contains an unknown input field", ErrInvalidDeclaration, location)
	}
	return nil
}

func validateSchemaForType(schema map[string]any, typeOf reflect.Type, stringEncoded bool, location string) error {
	if typeOf == reflect.TypeFor[json.RawMessage]() || typeOf.Kind() == reflect.Interface {
		return nil
	}
	nullable := false
	for typeOf.Kind() == reflect.Pointer {
		nullable = true
		typeOf = typeOf.Elem()
	}
	if implementsJSONUnmarshaler(typeOf) {
		return fmt.Errorf("%w: %s uses a custom JSON unmarshaler and needs a non-reflective Binding", ErrInvalidDeclaration, location)
	}
	if stringEncoded {
		types, err := schemaTypes(schema, location)
		if err != nil {
			return err
		}
		if !schemaTypesAreSubset(types, append(nullableTypes(nullable), "string")) {
			return fmt.Errorf("%w: %s must accept only JSON strings%s for a json string option", ErrInvalidDeclaration, location, nullSuffix(nullable))
		}
		return nil
	}
	switch typeOf.Kind() {
	case reflect.Bool:
		return validateScalarSchema(schema, []string{"boolean"}, nullable, location)
	case reflect.String:
		return validateScalarSchema(schema, []string{"string"}, nullable, location)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return validateScalarSchema(schema, []string{"integer"}, nullable, location)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if err := validateScalarSchema(schema, []string{"integer"}, nullable, location); err != nil {
			return err
		}
		if minimum, ok := schema["minimum"].(float64); !ok || minimum < 0 {
			return fmt.Errorf("%w: %s for an unsigned Go integer must set minimum to 0", ErrInvalidDeclaration, location)
		}
		return nil
	case reflect.Float32, reflect.Float64:
		return validateScalarSchema(schema, []string{"integer", "number"}, nullable, location)
	case reflect.Slice:
		if typeOf.Elem().Kind() == reflect.Uint8 {
			return validateScalarSchema(schema, []string{"string"}, nullable, location)
		}
		return validateArraySchema(schema, typeOf.Elem(), nullable, location)
	case reflect.Array:
		// encoding/json rejects an overlong fixed array, while a general JSON
		// Schema array accepts it unless a coupled maxItems is proved. Keep the
		// public explicit-schema seam conservative rather than publish a wider
		// contract than the decoder can honor.
		return fmt.Errorf("%w: %s uses a fixed Go array; use a slice for an explicit Tool schema", ErrInvalidDeclaration, location)
	case reflect.Map:
		if typeOf.Key().Kind() != reflect.String {
			return fmt.Errorf("%w: %s uses a map with non-string keys; JSON object keys would be wider than its decoder", ErrInvalidDeclaration, location)
		}
		return validateMapSchema(schema, typeOf.Elem(), nullable, location)
	case reflect.Struct:
		return validateObjectSchema(schema, typeOf, nullable, location)
	default:
		return fmt.Errorf("%w: %s uses unsupported Go input type %s", ErrInvalidDeclaration, location, typeOf)
	}
}

func validateScalarSchema(schema map[string]any, allowed []string, nullable bool, location string) error {
	types, err := schemaTypes(schema, location)
	if err != nil {
		return err
	}
	if !schemaTypesAreSubset(types, append(nullableTypes(nullable), allowed...)) {
		return fmt.Errorf("%w: %s schema type is not decodable by its Go field", ErrInvalidDeclaration, location)
	}
	return nil
}

func validateArraySchema(schema map[string]any, element reflect.Type, nullable bool, location string) error {
	types, err := schemaTypes(schema, location)
	if err != nil {
		return err
	}
	if !containsSchemaType(types, "array") || !schemaTypesAreSubset(types, append(nullableTypes(nullable), "array")) {
		return fmt.Errorf("%w: %s must accept only a JSON array%s", ErrInvalidDeclaration, location, nullSuffix(nullable))
	}
	items, ok := schema["items"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s array schema must declare one item schema", ErrInvalidDeclaration, location)
	}
	return validateSchemaForType(items, element, false, location+" item")
}

func validateMapSchema(schema map[string]any, element reflect.Type, nullable bool, location string) error {
	types, err := schemaTypes(schema, location)
	if err != nil {
		return err
	}
	if !containsSchemaType(types, "object") || !schemaTypesAreSubset(types, append(nullableTypes(nullable), "object")) {
		return fmt.Errorf("%w: %s must accept only a JSON object%s", ErrInvalidDeclaration, location, nullSuffix(nullable))
	}
	if properties, exists := schema["properties"]; exists {
		items, ok := properties.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: %s properties must be an object", ErrInvalidDeclaration, location)
		}
		for name, property := range items {
			propertySchema, ok := property.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: %s property %q must be a JSON Schema object", ErrInvalidDeclaration, location, name)
			}
			if err := validateSchemaForType(propertySchema, element, false, location+" property "+fmt.Sprintf("%q", name)); err != nil {
				return err
			}
		}
	}
	additional, exists := schema["additionalProperties"]
	if !exists || additional == true {
		if acceptsAnyJSON(element) {
			return nil
		}
		return fmt.Errorf("%w: %s must constrain additionalProperties to its map value type", ErrInvalidDeclaration, location)
	}
	if additional == false {
		return nil
	}
	additionalSchema, ok := additional.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s additionalProperties must be false or a JSON Schema object", ErrInvalidDeclaration, location)
	}
	return validateSchemaForType(additionalSchema, element, false, location+" additional property")
}

func schemaTypes(schema map[string]any, location string) ([]string, error) {
	value, exists := schema["type"]
	if !exists {
		return nil, fmt.Errorf("%w: %s must declare a JSON Schema type", ErrInvalidDeclaration, location)
	}
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %s type must contain only strings", ErrInvalidDeclaration, location)
			}
			result = append(result, name)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%w: %s type must be a string or an array of strings", ErrInvalidDeclaration, location)
	}
}

func schemaTypesAreSubset(types, allowed []string) bool {
	for _, typeName := range types {
		if !containsSchemaType(allowed, typeName) {
			return false
		}
	}
	return len(types) != 0
}

func containsSchemaType(types []string, want string) bool {
	for _, typeName := range types {
		if typeName == want {
			return true
		}
	}
	return false
}

func nullableTypes(nullable bool) []string {
	if nullable {
		return []string{"null"}
	}
	return nil
}

func nullSuffix(nullable bool) string {
	if nullable {
		return " or null"
	}
	return ""
}

func acceptsAnyJSON(typeOf reflect.Type) bool {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return typeOf == reflect.TypeFor[json.RawMessage]() || typeOf.Kind() == reflect.Interface
}

func implementsJSONUnmarshaler(typeOf reflect.Type) bool {
	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	return typeOf.Implements(unmarshaler) || reflect.PointerTo(typeOf).Implements(unmarshaler)
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
