package graph

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func managedRegistrationOptions(calls *atomic.Int32) BindingRegistrationOptions[map[string]any] {
	return BindingRegistrationOptions[map[string]any]{
		BindingID: "binding", Version: "1", Kind: "registered", Effect: "read_only", ABI: ManagedABI,
		ArtifactDigest:      "sha256:" + strings.Repeat("a", 64),
		Portability:         Portability{Kind: "portable", ContractID: "contract"},
		InputSchema:         map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []any{"x"}, "additionalProperties": false},
		ValidatorContractID: "validator", DecoderContractID: "decoder", DefaultContractID: "defaults.none", CoercionContractID: "coercion.none",
		ConfigSchema:  map[string]any{"type": "object", "properties": map[string]any{"region": map[string]any{"enum": []any{"local"}}}, "additionalProperties": false},
		AllowedConfig: []string{"region"}, AllowedSecretRefs: []string{"secret"},
		Decoder: func(f FrozenJSON) (map[string]any, error) {
			calls.Add(1)
			m, ok := f.JSON().(map[string]any)
			if !ok {
				return nil, errors.New("expected typed map")
			}
			return m, nil
		},
		Validator: func(input map[string]any) error {
			calls.Add(1)
			if input["x"] == "invalid" {
				return errors.New("native validator refused")
			}
			return nil
		},
		Handler: func(_ context.Context, input map[string]any, _ ManagedCallContext) (any, error) {
			calls.Add(1)
			return input, nil
		},
	}
}
func TestManagedBindingPinnedTypedDispatch(t *testing.T) {
	var calls atomic.Int32
	o := managedRegistrationOptions(&calls)
	r, err := NewBindingRegistration(o)
	if err != nil {
		t.Fatal(err)
	}
	o.Handler = nil
	mutateJSON(o.InputSchema)
	if _, err := r.Call(context.Background(), map[string]any{"x": 1}, ManagedCallContext{}); err == nil || calls.Load() != 0 {
		t.Fatal("raw schema must reject before decoder")
	}
	if _, err := r.Call(context.Background(), map[string]any{"x": "invalid"}, ManagedCallContext{}); err == nil || calls.Load() != 2 {
		t.Fatal("native validator must reject before handler")
	}
	args := map[string]any{"x": "original"}
	result, err := r.Call(context.Background(), args, ManagedCallContext{})
	if err != nil || calls.Load() != 5 {
		t.Fatal(result.JSON(), err, calls.Load())
	}
	args["x"] = "changed"
	mutateJSON(r.Contract())
	mutateJSON(r.InputSchema())
	if !reflect.DeepEqual(result.JSON(), map[string]any{"x": "original"}) {
		t.Fatal("result alias escaped")
	}
	binding := candidateBinding()
	binding["config"] = map[string]any{"region": "local"}
	binding["expected_contract_digest"] = r.ContractDigest()
	descriptor := parseBinding(binding)
	resolved, err := r.Resolve(descriptor, nil)
	if err != nil || resolved.Effect() != "read_only" || resolved.Registration() != r {
		t.Fatal(resolved, err)
	}
	mutateJSON(resolved.Entry())
	for name, mutate := range map[string]func(map[string]any){
		"version":       func(b map[string]any) { b["version"] = "latest" },
		"digest":        func(b map[string]any) { b["expected_contract_digest"] = "sha256:" + strings.Repeat("b", 64) },
		"kind":          func(b map[string]any) { b["kind"] = "remote" },
		"config key":    func(b map[string]any) { b["config"] = map[string]any{"secret": "raw"} },
		"config schema": func(b map[string]any) { b["config"] = map[string]any{"region": "remote"} },
		"secret handle": func(b map[string]any) { b["secret_refs"] = []any{"another"} },
		"portability":   func(b map[string]any) { b["portability"] = map[string]any{"kind": "portable", "contract_id": "other"} },
	} {
		t.Run(name, func(t *testing.T) {
			copy := detachedCandidate(t, binding).(map[string]any)
			mutate(copy)
			if _, err := r.Resolve(parseBinding(copy), nil); err == nil {
				t.Fatal("contract mismatch accepted")
			}
		})
	}
	if _, err := r.Resolve(descriptor, false); err == nil {
		t.Fatal("input assertion mismatch accepted")
	}
}
func TestManagedRegistryAndManifestIdentity(t *testing.T) {
	var calls atomic.Int32
	o := managedRegistrationOptions(&calls)
	first, err := NewBindingRegistration(o)
	if err != nil {
		t.Fatal(err)
	}
	o.BindingID = "unused"
	second, err := NewBindingRegistration(o)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewRegistryGeneration("build", first, second)
	b, _ := NewRegistryGeneration("build", second, first)
	c, _ := NewRegistryGeneration("build", first)
	d, _ := NewRegistryGeneration("other", first, second)
	if a.Digest() != b.Digest() || a.Digest() == c.Digest() || a.Digest() == d.Digest() {
		t.Fatal("registry identity is not complete/deterministic")
	}
	if _, err := NewRegistryGeneration("build", first, first); err == nil {
		t.Fatal("duplicate registry registration accepted")
	}
	missing := candidateBinding()
	missing["version"] = "2"
	if _, err := a.Resolve(parseBinding(missing), nil); err == nil {
		t.Fatal("non-exact registry lookup accepted")
	}
	resolved, err := first.Resolve(first.Descriptor(), nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewExecutionManifest(ExecutionManifestOptions{
		CompilerContractVersion: "1", CompilerBuild: "compiler", RegistryDigest: a.Digest(),
		BindingEntries:       []any{resolved.Entry(), resolved.Entry()},
		DependencyContractID: "app", DependencyBuild: "app-build", EnvironmentCompatibility: []string{"z", "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseExecutionManifest(manifest.AsJSON())
	if err != nil || parsed.Digest() != manifest.Digest() {
		t.Fatal(parsed, err)
	}
	subject := manifest.Subject().(map[string]any)
	if len(subject["binding_entries"].([]any)) != 2 || !reflect.DeepEqual(subject["environment_compatibility"], []any{"z", "a"}) {
		t.Fatal("manifest order/repeated entries changed")
	}
	bad := manifest.AsJSON().(map[string]any)
	bad["compiler_build"] = "tampered"
	if _, err := ParseExecutionManifest(bad); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	bad = manifest.AsJSON().(map[string]any)
	bad["unknown"] = true
	if _, err := ParseExecutionManifest(bad); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("registry/manifest ran native code")
	}
}

func TestManagedSchemaOfflineStrictNonmutating(t *testing.T) {
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$defs":   map[string]any{"word": map[string]any{"type": "string", "minLength": 1}},
		"type":    "object", "properties": map[string]any{"x": map[string]any{"$ref": "#/$defs/word", "default": "default"}},
		"required": []any{"x"}, "additionalProperties": false,
	}
	compiled, err := CompileManagedSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	mutateJSON(schema)
	for _, invalid := range []any{map[string]any{}, map[string]any{"x": 12}, map[string]any{"x": ""}, map[string]any{"x": "a", "extra": true}} {
		before, _ := FreezeJSON(invalid)
		if err := compiled.Validate(invalid); err == nil || !jsonEqual(before.JSON(), invalid) {
			t.Fatal("schema mutated/coerced/defaulted input or accepted invalid", invalid)
		}
	}
	if err := compiled.Validate(map[string]any{"x": "word"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{
		nil, []any{}, map[string]any{"$ref": "https://example.invalid/schema"},
		map[string]any{"$ref": "#/$defs/missing"},
		map[string]any{"$id": "local"}, map[string]any{"$anchor": "x"},
		map[string]any{"$dynamicRef": "#"}, map[string]any{"$schema": "http://json-schema.org/draft-07/schema#"},
		map[string]any{"unknownSemanticKeyword": true}, map[string]any{"properties": []any{}},
		map[string]any{"type": "invented"}, map[string]any{"minLength": -1},
		map[string]any{"$ref": "#"}, map[string]any{"not": map[string]any{"$ref": "#"}},
		map[string]any{"$defs": map[string]any{"a": map[string]any{"$ref": "#/$defs/b"}, "b": map[string]any{"$ref": "#/$defs/a"}}},
		map[string]any{"$ref": "#/$defs/bad~2"}, map[string]any{"$ref": "#/%xx"},
	} {
		if _, err := CompileManagedSchema(bad); err == nil {
			t.Fatalf("unsupported/invalid schema accepted: %#v", bad)
		}
		recursive, err := CompileManagedSchema(map[string]any{"type": "object", "properties": map[string]any{"child": map[string]any{"$ref": "#"}}})
		if err != nil {
			t.Fatal("progressing recursive schema should compile", err)
		}
		if err := recursive.Validate(map[string]any{"child": map[string]any{"child": map[string]any{}}}); err != nil {
			t.Fatal(err)
		}
		if err := recursive.Validate(map[string]any{"child": "wrong"}); err == nil {
			t.Fatal("recursive child schema was not evaluated")
		}
	}
}

func TestManagedCompilerPureMetadataAndIsolation(t *testing.T) {
	var calls atomic.Int32
	r, err := NewBindingRegistration(managedRegistrationOptions(&calls))
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := NewRegistryGeneration("registry", r)
	role := candidateNode("role", "role")
	delete(role, "instructions")
	role["instruction_template"] = map[string]any{"parts": []any{map[string]any{"kind": "Text", "text": "Use "}, map[string]any{"kind": "NodeRef", "target_id": "tool", "label": "call"}}}
	role["members"] = map[string]any{"tools": []any{"tool"}, "pre_process": "pre", "post_process": "post"}
	tool := candidateNode("tool", "tool")
	tool["binding"] = r.Descriptor().AsJSON()
	tool["input_contract"] = r.InputSchema()
	pre, post := candidateNode("pre", "role"), candidateNode("post", "role")
	pre["role_variant"], post["role_variant"] = "pre_process", "post_process"
	raw := candidateGraph(role, tool, pre, post)
	raw["mode"] = "runtime"
	raw["roots"] = []any{map[string]any{"node_id": "role", "plane": "model"}}
	raw["presentations"] = map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "tool", "title": "Localized"}}}}}
	definition := mustDefinition(t, raw, nil)
	o := ManagedCompilerOptions{Registry: registry, Locale: "en-US", CompilerContractVersion: "1", CompilerBuild: "compiler", DependencyContractID: "app", DependencyBuild: "app-build"}
	plan, err := CompileManaged(definition, o)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || !plan.IsCompiled() {
		t.Fatal("compiler executed native code")
	}
	processes, _ := plan.Processes("role")
	presentation, _ := plan.Presentation("tool")
	if processes.PreProcess != "pre" || processes.PostProcess != "post" || presentation.Title != "Localized" {
		t.Fatal("metadata missing", processes, presentation)
	}
	parts := plan.Instructions("role")
	parts[0].Text = "corrupt"
	resolved, err := plan.ResolveInstructions("role", "01234567-89ab-cdef-0123-456789abcdef")
	if err != nil || resolved[0].Text != "Use " || resolved[1].Reference.NodeID() != "tool" {
		t.Fatal(resolved, err)
	}
	if _, err := plan.ResolveInstructions("tool", "01234567-89ab-cdef-0123-456789abcdef"); err == nil {
		t.Fatal("tool instructions invented")
	}
	again, err := CompileManaged(definition, o)
	if err != nil || again.Manifest().Digest() != plan.Manifest().Digest() {
		t.Fatal("pure compile not deterministic", err)
	}
	o.ExecutionExtensions = []string{"execute"}
	if _, err := CompileManaged(definition, o); err == nil {
		t.Fatal("executable extensions accepted")
	}
	o.ExecutionExtensions, o.Registry = nil, nil
	if _, err := CompileManaged(definition, o); err == nil {
		t.Fatal("runtime compiled without exact registry")
	}
	disclosure := mustDefinition(t, candidateGraph(candidateNode("root", "skill")), nil)
	if _, err := CompileManaged(disclosure, o); err == nil {
		t.Fatal("disclosure compiled without no-execution identity")
	}
	o.NoExecutionRegistryDigest = "sha256:" + strings.Repeat("0", 64)
	o.Registry = registry
	if _, err := CompileManaged(disclosure, o); err != nil {
		t.Fatal("disclosure should ignore execution registry", err)
	}
}
