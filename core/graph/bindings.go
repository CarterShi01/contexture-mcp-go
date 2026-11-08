package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

const ManagedABI = "go-managed-call/v1"

type ManagedIdentity struct {
	ActorID, TenantID, AuthorityRevision string
}

type ManagedCallContext struct {
	Identity ManagedIdentity
	Resource any
}

// ManagedSchema is an offline, non-mutating Draft 2020-12 validator.
// The zero value is invalid. No loader, defaults, coercion or custom format
// assertion is installed.
type ManagedSchema struct {
	data      FrozenJSON
	validator *jsonschema.Resolved
}

func CompileManagedSchema(value any) (out ManagedSchema, err error) {
	frozen, err := FreezeJSON(value)
	if err != nil {
		return out, err
	}
	var visit func(any) error
	visit = func(value any) error {
		if _, ok := value.(bool); ok {
			return nil
		}
		m, ok := value.(map[string]any)
		if !ok {
			return invalid("Expected a boolean or object JSON Schema")
		}
		allowed := strings.Fields("$schema $comment $ref $defs definitions title description default deprecated readOnly writeOnly examples type enum const multipleOf minimum maximum exclusiveMinimum exclusiveMaximum minLength maxLength pattern prefixItems items minItems maxItems uniqueItems contains minContains maxContains unevaluatedItems minProperties maxProperties required dependentRequired properties patternProperties additionalProperties propertyNames unevaluatedProperties allOf anyOf oneOf not if then else dependentSchemas contentEncoding contentMediaType contentSchema format")
		for key := range m {
			if !slices.Contains(allowed, key) {
				return invalid("Unsupported JSON Schema keyword: " + key)
			}
			if err := managedSchemaSyntax(m); err != nil {
				return err
			}
		}
		if dialect, ok := m["$schema"]; ok && dialect != "https://json-schema.org/draft/2020-12/schema" {
			return invalid("Unsupported schema dialect")
		}
		if ref, ok := m["$ref"]; ok {
			s, ok := ref.(string)
			if !ok || (s != "#" && !strings.HasPrefix(s, "#/")) {
				return invalid("Only local JSON-pointer schema references are supported")
			}
		}
		for _, key := range strings.Fields("$defs definitions properties patternProperties dependentSchemas") {
			if v, ok := m[key]; ok {
				children, ok := v.(map[string]any)
				if !ok {
					return invalid("Expected a schema map")
				}
				for _, child := range children {
					if err := visit(child); err != nil {
						return err
					}
				}
			}
		}
		for _, key := range strings.Fields("items contains unevaluatedItems additionalProperties propertyNames unevaluatedProperties not if then else contentSchema") {
			if child, ok := m[key]; ok {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		for _, key := range strings.Fields("allOf anyOf oneOf prefixItems") {
			if v, ok := m[key]; ok {
				children, ok := v.([]any)
				if !ok {
					return invalid("Expected a schema array")
				}
				for _, child := range children {
					if err := visit(child); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := visit(frozen.value); err != nil {
		return out, err
	}
	if err := managedSchemaReferences(frozen.value); err != nil {
		return out, err
	}
	raw, err := frozen.Canonical()
	if err != nil {
		return out, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return out, fmt.Errorf("invalid managed schema: %w", err)
	}
	validator, err := schema.Resolve(nil)
	if err != nil {
		return out, fmt.Errorf("invalid managed schema: %w", err)
	}
	return ManagedSchema{frozen, validator}, nil
}

// Reject same-instance reference cycles before entering the native evaluator.
// Recursion through object properties or array items remains supported because
// each step consumes a child of the already depth-bounded JSON input.
func managedSchemaReferences(root any) error {
	type item struct {
		path  string
		value any
	}
	pending := []item{{"", root}}
	nodes := map[string]any{}
	edges := map[string][]string{}
	childPath := func(path, key string) string {
		return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	}
	for len(pending) > 0 {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		nodes[entry.path] = entry.value
		m, ok := entry.value.(map[string]any)
		if !ok {
			continue
		}
		add := func(path string, value any, sameInstance bool) {
			pending = append(pending, item{path, value})
			if sameInstance {
				edges[entry.path] = append(edges[entry.path], path)
			}
		}
		for _, key := range strings.Fields("$defs definitions properties patternProperties dependentSchemas") {
			if values, ok := m[key].(map[string]any); ok {
				for name, child := range values {
					add(childPath(childPath(entry.path, key), name), child, key == "dependentSchemas")
				}
			}
		}
		for _, key := range strings.Fields("items contains unevaluatedItems additionalProperties propertyNames unevaluatedProperties not if then else contentSchema") {
			if child, ok := m[key]; ok {
				add(childPath(entry.path, key), child, slices.Contains([]string{"not", "if", "then", "else"}, key))
			}
		}
		for _, key := range strings.Fields("allOf anyOf oneOf prefixItems") {
			if values, ok := m[key].([]any); ok {
				for i, child := range values {
					add(childPath(entry.path, key)+fmt.Sprintf("/%d", i), child, key != "prefixItems")
				}
			}
		}
		if ref, ok := m["$ref"].(string); ok {
			path, err := url.PathUnescape(strings.TrimPrefix(ref, "#"))
			if err != nil {
				return invalid("Invalid schema reference escaping")
			}
			// Paths are stored in canonical JSON-pointer spelling.
			for i := 0; i < len(path); i++ {
				if path[i] == '~' {
					if i+1 == len(path) || path[i+1] != '0' && path[i+1] != '1' {
						return invalid("Invalid schema JSON pointer")
					}
					i++
				}
			}
			edges[entry.path] = append(edges[entry.path], path)
		}
	}
	indegree := map[string]int{}
	for path := range nodes {
		indegree[path] = 0
	}
	for _, targets := range edges {
		for _, path := range targets {
			if _, ok := nodes[path]; !ok {
				return invalid("Schema reference must target a supported local subschema")
			}
			indegree[path]++
		}
	}
	queue := []string{}
	for path, count := range indegree {
		if count == 0 {
			queue = append(queue, path)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, target := range edges[queue[i]] {
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if len(queue) != len(nodes) {
		return invalid("Non-progressing recursive JSON Schema is unsupported")
	}
	return nil
}

// jsonschema-go resolves schemas but deliberately does not validate their own
// keyword syntax. Keep this gate explicit instead of silently ignoring invalid
// native assertions. Schema evaluation itself remains in the existing library.
func managedSchemaSyntax(m map[string]any) error {
	for _, key := range strings.Fields("$schema $comment $ref title description pattern format contentEncoding contentMediaType") {
		if value, ok := m[key]; ok {
			if _, ok := value.(string); !ok {
				return invalid("Schema keyword must be a string: " + key)
			}
		}
	}
	for _, key := range strings.Fields("deprecated readOnly writeOnly uniqueItems") {
		if value, ok := m[key]; ok {
			if _, ok := value.(bool); !ok {
				return invalid("Schema keyword must be boolean: " + key)
			}
		}
	}
	for _, key := range strings.Fields("minimum maximum exclusiveMinimum exclusiveMaximum multipleOf minLength maxLength minItems maxItems minContains maxContains minProperties maxProperties") {
		if value, ok := m[key]; ok {
			n, ok := value.(float64)
			if !ok || (key == "multipleOf" && n <= 0) ||
				(strings.HasPrefix(key, "min") || strings.HasPrefix(key, "max")) &&
					key != "minimum" && key != "maximum" && (n < 0 || math.Trunc(n) != n) {
				return invalid("Invalid numeric schema keyword: " + key)
			}
		}
	}
	uniqueStrings := func(value any, types bool) error {
		items, ok := value.([]any)
		if !ok || (types && len(items) == 0) {
			return invalid("Expected schema string array")
		}
		seen := map[string]bool{}
		for _, raw := range items {
			item, ok := raw.(string)
			if !ok || seen[item] || types && !slices.Contains(strings.Fields("null boolean object array number integer string"), item) {
				return invalid("Invalid or duplicate schema string")
			}
			seen[item] = true
		}
		return nil
	}
	if value, ok := m["type"]; ok {
		if name, ok := value.(string); ok {
			value = []any{name}
		}
		if err := uniqueStrings(value, true); err != nil {
			return err
		}
	}
	if value, ok := m["required"]; ok {
		if err := uniqueStrings(value, false); err != nil {
			return err
		}
	}
	if value, ok := m["dependentRequired"]; ok {
		dependencies, ok := value.(map[string]any)
		if !ok {
			return invalid("Expected dependency map")
		}
		for _, items := range dependencies {
			if err := uniqueStrings(items, false); err != nil {
				return err
			}
		}
	}
	for _, key := range strings.Fields("enum examples allOf anyOf oneOf prefixItems") {
		if value, ok := m[key]; ok {
			items, ok := value.([]any)
			if !ok || (key != "examples" && key != "prefixItems" && len(items) == 0) {
				return invalid("Invalid schema array: " + key)
			}
			if key == "enum" {
				seen := map[string]bool{}
				for _, item := range items {
					raw, _ := CanonicalJSON(item)
					if seen[string(raw)] {
						return invalid("Duplicate schema enum value")
					}
					seen[string(raw)] = true
				}
			}
		}
	}
	return nil
}

func (s ManagedSchema) AsJSON() any { return s.data.JSON() }
func (s ManagedSchema) Validate(value any) error {
	if s.validator == nil {
		return invalid("Uncompiled managed schema")
	}
	copy, err := FreezeJSON(value)
	if err != nil {
		return err
	}
	return s.validator.Validate(copy.JSON())
}

type BindingRegistrationOptions[I any] struct {
	BindingID, Version, Kind, Effect, ABI, ArtifactDigest string
	Portability                                           Portability
	InputSchema, ConfigSchema                             any
	ValidatorContractID, DecoderContractID                string
	DefaultContractID, CoercionContractID                 string
	AllowedConfig, AllowedSecretRefs                      []string
	ResourcePolicy                                        map[string]any
	ArgumentFree                                          bool
	Decoder                                               func(FrozenJSON) (I, error)
	Validator                                             func(I) error
	Handler                                               func(context.Context, I, ManagedCallContext) (any, error)
}

// BindingRegistration captures typed native callables in a single closure.
// Contract IDs attest trusted native semantics; they are not a sandbox.
type BindingRegistration struct {
	descriptor BindingDescriptor
	contract   FrozenJSON
	digest     string
	input      ManagedSchema
	config     ManagedSchema
	call       func(context.Context, FrozenJSON, ManagedCallContext) (FrozenJSON, error)
}

func privateDigest(value any, domain string) (string, error) {
	raw, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(append([]byte(domain), 0), raw...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func jsonEqual(a, b any) bool {
	left, e1 := CanonicalJSON(a)
	right, e2 := CanonicalJSON(b)
	return e1 == nil && e2 == nil && bytes.Equal(left, right)
}
func str(value any) string { return txt(value, false) }
func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, c := range value[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func contractIDs(ids []string) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := ValidateID(id); err != nil || seen[id] {
			failModel("Invalid or duplicate contract identity")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
func NewBindingRegistration[I any](o BindingRegistrationOptions[I]) (out *BindingRegistration, err error) {
	defer catchModel(&err)
	if o.ABI != ManagedABI || o.Decoder == nil || o.Validator == nil || o.Handler == nil {
		return nil, invalid("Explicit native Go ABI, decoder, validator and handler required")
	}
	if !validDigest(o.ArtifactDigest) {
		return nil, invalid("Invalid artifact digest")
	}
	for _, id := range []string{o.ValidatorContractID, o.DecoderContractID, o.DefaultContractID, o.CoercionContractID} {
		if _, err := ValidateID(id); err != nil {
			return nil, err
		}
	}
	port := map[string]any{"kind": o.Portability.Kind}
	switch o.Portability.Kind {
	case "portable":
		port["contract_id"] = o.Portability.ContractID
	case "native":
		if o.Portability.Language != "go" {
			return nil, invalid("Go ABI requires Go native portability")
		}
		port["language"], port["version"], port["factory"] = o.Portability.Language, o.Portability.Version, o.Portability.Factory
	}
	descriptor := parseBinding(map[string]any{"kind": o.Kind, "binding_id": o.BindingID, "version": o.Version, "portability": port})
	effect := enum(o.Effect, "read_only write")
	input, err := CompileManagedSchema(o.InputSchema)
	if err != nil {
		return nil, err
	}
	configValue := o.ConfigSchema
	if configValue == nil {
		configValue = map[string]any{"type": "object", "additionalProperties": false}
	}
	config, err := CompileManagedSchema(configValue)
	if err != nil {
		return nil, err
	}
	if o.ArgumentFree && input.Validate(map[string]any{}) != nil {
		return nil, invalid("Argument-free binding must accept an empty object")
	}
	policy := o.ResourcePolicy
	if policy == nil {
		policy = map[string]any{}
	}
	contract := mustFreeze(map[string]any{
		"binding_id": o.BindingID, "version": o.Version, "kind": o.Kind, "portability": port,
		"effect": effect, "abi": o.ABI, "artifact_digest": o.ArtifactDigest, "input_schema": input.AsJSON(),
		"validator_contract_id": o.ValidatorContractID, "decoder_contract_id": o.DecoderContractID,
		"default_contract_id": o.DefaultContractID, "coercion_contract_id": o.CoercionContractID,
		"allowed_config": contractIDs(o.AllowedConfig), "config_schema": config.AsJSON(),
		"allowed_secret_refs": contractIDs(o.AllowedSecretRefs), "resource_policy": policy,
		"argument_free": o.ArgumentFree, "has_factory": false,
	})
	digest, err := privateDigest(contract.value, "contexture.binding-contract/implementation-v1")
	if err != nil {
		return nil, err
	}
	decoder, validator, handler := o.Decoder, o.Validator, o.Handler
	call := func(ctx context.Context, args FrozenJSON, callContext ManagedCallContext) (output FrozenJSON, err error) {
		defer func() {
			if value := recover(); value != nil {
				err = fmt.Errorf("native managed binding panic: %v", value)
			}
		}()
		if err := ctx.Err(); err != nil {
			return FrozenJSON{}, err
		}
		if err := input.Validate(args.JSON()); err != nil {
			return FrozenJSON{}, err
		}
		decoded, err := decoder(args)
		if err != nil {
			return FrozenJSON{}, err
		}
		if err := validator(decoded); err != nil {
			return FrozenJSON{}, err
		}
		result, err := handler(ctx, decoded, callContext)
		if err != nil {
			return FrozenJSON{}, err
		}
		return FreezeJSON(result)
	}
	return &BindingRegistration{descriptor, contract, digest, input, config, call}, nil
}

func (r *BindingRegistration) Descriptor() BindingDescriptor { return r.descriptor }
func (r *BindingRegistration) Contract() any                 { return r.contract.JSON() }
func (r *BindingRegistration) ContractDigest() string        { return r.digest }
func (r *BindingRegistration) InputSchema() any              { return r.input.AsJSON() }
func (r *BindingRegistration) Call(ctx context.Context, args any, call ManagedCallContext) (FrozenJSON, error) {
	frozen, err := FreezeJSON(args)
	if err != nil {
		return FrozenJSON{}, err
	}
	if r == nil || r.call == nil {
		return FrozenJSON{}, invalid("Trusted native binding required")
	}
	return r.call(ctx, frozen, call)
}

type ResolvedBinding struct {
	entry        FrozenJSON
	registration *BindingRegistration
	publication  PublicationContract
}

// ParseBindingDescriptor validates inert author data, not registry authority.
func ParseBindingDescriptor(value any) (out BindingDescriptor, err error) {
	defer catchModel(&err)
	return parseBinding(mustFreeze(value).JSON()), nil
}

func (r ResolvedBinding) Entry() any                               { return r.entry.JSON() }
func (r ResolvedBinding) Registration() *BindingRegistration       { return r.registration }
func (r ResolvedBinding) PublicationContract() PublicationContract { return r.publication }
func (r ResolvedBinding) Effect() string                           { return r.publication.Effect() }

func (r *BindingRegistration) Resolve(b BindingDescriptor, assertion any) (out ResolvedBinding, err error) {
	defer catchModel(&err)
	if r == nil || r.call == nil || b.id != r.descriptor.id || b.version != r.descriptor.version ||
		b.kind != r.descriptor.kind || b.portability != r.descriptor.portability ||
		(b.digest != "" && b.digest != r.digest) {
		return out, invalid("Exact binding contract mismatch")
	}
	if assertion != nil {
		schema, err := CompileManagedSchema(assertion)
		if err != nil || !jsonEqual(schema.AsJSON(), r.input.AsJSON()) {
			return out, invalid("Input contract assertion mismatch")
		}
	}
	data := obj(b.AsJSON(), "kind binding_id version portability config secret_refs", "expected_contract_digest")
	config, ok := data["config"].(map[string]any)
	if !ok {
		return out, invalid("Expected binding configuration object")
	}
	contract, ok := r.contract.JSON().(map[string]any)
	if !ok {
		return out, invalid("Invalid native contract")
	}
	for key := range config {
		if !slices.Contains(contract["allowed_config"].([]any), any(key)) {
			return out, invalid("Configuration key is not allowed")
		}
	}
	for _, secret := range b.secrets {
		if !slices.Contains(contract["allowed_secret_refs"].([]any), any(secret)) {
			return out, invalid("Secret HANDLE is not allowed")
		}
	}
	if err := r.config.Validate(config); err != nil {
		return out, err
	}
	entry := mustFreeze(map[string]any{"binding": b.AsJSON(), "contract_digest": r.digest,
		"artifact_digest": contract["artifact_digest"], "effect": contract["effect"], "abi": ManagedABI,
		"input_schema": r.input.AsJSON(), "validator_contract_id": contract["validator_contract_id"]})
	publication, err := NewPublicationContract(b.AsJSON(), contract["effect"].(string), contract["argument_free"].(bool))
	return ResolvedBinding{entry, r, publication}, err
}

type RegistryGeneration struct {
	build, digest string
	entries       map[[2]string]*BindingRegistration
}

func NewRegistryGeneration(build string, registrations ...*BindingRegistration) (*RegistryGeneration, error) {
	if build == "" {
		return nil, invalid("Nonempty registry build required")
	}
	entries := map[[2]string]*BindingRegistration{}
	captured := slices.Clone(registrations)
	for _, r := range captured {
		if r == nil || r.call == nil {
			return nil, invalid("Trusted native registration required")
		}
		key := [2]string{r.descriptor.id, r.descriptor.version}
		if entries[key] != nil {
			return nil, invalid("Duplicate binding identity/version")
		}
		entries[key] = r
	}
	slices.SortFunc(captured, func(a, b *BindingRegistration) int {
		if c := strings.Compare(a.descriptor.id, b.descriptor.id); c != 0 {
			return c
		}
		return strings.Compare(a.descriptor.version, b.descriptor.version)
	})
	contracts := []any{}
	for _, r := range captured {
		contracts = append(contracts, r.contract.JSON())
	}
	digest, err := privateDigest(map[string]any{"build": build, "registrations": contracts}, "contexture.registry-contract/implementation-v1")
	if err != nil {
		return nil, err
	}
	return &RegistryGeneration{build, digest, entries}, nil
}
func (r *RegistryGeneration) Digest() string { return r.digest }
func (r *RegistryGeneration) Build() string  { return r.build }
func (r *RegistryGeneration) Resolve(binding BindingDescriptor, assertion any) (ResolvedBinding, error) {
	if r == nil {
		return ResolvedBinding{}, invalid("Exact registry generation required")
	}
	return r.entries[[2]string{binding.id, binding.version}].Resolve(binding, assertion)
}

type ExecutionManifestOptions struct {
	CompilerContractVersion, CompilerBuild, RegistryDigest string
	BindingEntries                                         []any
	DependencyContractID, DependencyBuild                  string
	EnvironmentCompatibility                               []string
}
type ExecutionManifest struct {
	subject FrozenJSON
	digest  string
}

func NewExecutionManifest(o ExecutionManifestOptions) (out *ExecutionManifest, err error) {
	defer catchModel(&err)
	for _, text := range []string{o.CompilerContractVersion, o.CompilerBuild, o.DependencyBuild} {
		if text == "" {
			return nil, invalid("Nonempty compiler/dependency contract required")
		}
	}
	if _, err := ValidateID(o.DependencyContractID); err != nil || !validDigest(o.RegistryDigest) {
		return nil, invalid("Invalid dependency identity or registry digest")
	}
	entries := []any{}
	for _, raw := range o.BindingEntries {
		entry := obj(mustFreeze(raw).JSON(), "binding contract_digest artifact_digest effect abi input_schema validator_contract_id", "")
		b := parseBinding(entry["binding"])
		digest := str(entry["contract_digest"])
		if !validDigest(digest) || !validDigest(str(entry["artifact_digest"])) ||
			(b.digest != "" && b.digest != digest) || str(entry["abi"]) == "" {
			return nil, invalid("Invalid binding manifest evidence")
		}
		enum(entry["effect"], "read_only write")
		if _, err := ValidateID(str(entry["validator_contract_id"])); err != nil {
			return nil, err
		}
		if _, err := CompileManagedSchema(entry["input_schema"]); err != nil {
			return nil, err
		}
		entry["binding"] = b.AsJSON()
		entries = append(entries, entry)
	}
	environment := contractIDs(o.EnvironmentCompatibility)
	subject := mustFreeze(map[string]any{"schema": "contexture.manifest/v1",
		"compiler_contract_version": o.CompilerContractVersion, "compiler_build": o.CompilerBuild,
		"registry_digest": o.RegistryDigest, "binding_entries": entries,
		"app_dependency_contract":   map[string]any{"dependency_contract_id": o.DependencyContractID, "build": o.DependencyBuild},
		"environment_compatibility": environment})
	digest, err := CanonicalDigest(subject.value, "contexture.manifest/v1")
	return &ExecutionManifest{subject, digest}, err
}
func (m *ExecutionManifest) Subject() any   { return m.subject.JSON() }
func (m *ExecutionManifest) Digest() string { return m.digest }
func (m *ExecutionManifest) AsJSON() any {
	out := m.subject.JSON().(map[string]any)
	out["manifest_digest"] = m.digest
	return out
}
func ParseExecutionManifest(value any) (out *ExecutionManifest, err error) {
	defer catchModel(&err)
	if raw, ok := value.([]byte); ok {
		decoded, decodeErr := DecodeJSON(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		value = decoded
	}
	data := obj(mustFreeze(value).JSON(), "schema compiler_contract_version compiler_build registry_digest binding_entries app_dependency_contract environment_compatibility manifest_digest", "")
	if str(data["schema"]) != "contexture.manifest/v1" {
		return nil, invalid("Invalid manifest schema")
	}
	dep := obj(data["app_dependency_contract"], "dependency_contract_id build", "")
	env := []string{}
	for _, id := range arr(data["environment_compatibility"]) {
		env = append(env, str(id))
	}
	out, err = NewExecutionManifest(ExecutionManifestOptions{
		CompilerContractVersion: str(data["compiler_contract_version"]), CompilerBuild: str(data["compiler_build"]),
		RegistryDigest: str(data["registry_digest"]), BindingEntries: arr(data["binding_entries"]),
		DependencyContractID: str(dep["dependency_contract_id"]), DependencyBuild: str(dep["build"]), EnvironmentCompatibility: env,
	})
	if err != nil {
		return nil, err
	}
	if out.digest != str(data["manifest_digest"]) {
		return nil, invalid("Manifest integrity mismatch")
	}
	return out, nil
}
