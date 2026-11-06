package graph

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func candidateNode(id, kind string) map[string]any {
	n := map[string]any{"node_id": id, "kind": kind, "name": strings.ReplaceAll(id, "/", "-"), "description": "Description"}
	if kind != "tool" {
		n["instructions"] = ""
	}
	return n
}
func candidateBinding() map[string]any {
	return map[string]any{"kind": "registered", "binding_id": "binding", "version": "1", "portability": map[string]any{"kind": "portable", "contract_id": "contract"}, "config": map[string]any{"nested": []any{map[string]any{"value": "original"}}}, "secret_refs": []any{"secret"}}
}
func candidateGraph(nodes ...map[string]any) map[string]any {
	values, roots := []any{}, []any{}
	for _, n := range nodes {
		values = append(values, n)
		roots = append(roots, map[string]any{"node_id": n["node_id"], "plane": "model"})
	}
	return map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "graph", "application_name": "Application", "mode": "disclosure_only", "nodes": values, "roots": roots, "presentations": map[string]any{"default_locale": "en", "catalogs": []any{}}}
}
func detachedCandidate(t *testing.T, value any) any {
	t.Helper()
	f, err := FreezeJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return f.JSON()
}
func requireModelError(t *testing.T, value any, contracts map[string]PublicationContract) {
	t.Helper()
	definition, err := NewGraphDefinition(value, contracts)
	var typed *GraphError
	if err == nil || !errors.As(err, &typed) || definition != nil {
		t.Fatalf("Expected native GraphError and no definition, got %v, %v", definition, err)
	}
}
func mustDefinition(t *testing.T, value any, contracts map[string]PublicationContract) *GraphDefinition {
	t.Helper()
	d, err := NewGraphDefinition(value, contracts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func mutateJSON(value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			mutateJSON(item)
			v[k] = nil
		}
		v["corrupted"] = true
	case []any:
		for i, item := range v {
			mutateJSON(item)
			v[i] = nil
		}
	}
}

func TestCandidateModelSnapshotIsolation(t *testing.T) {
	n := candidateNode("tool", "tool")
	binding := candidateBinding()
	n["binding"] = binding
	n["input_contract"] = map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}}
	n["extensions"] = map[string]any{"test:key": map[string]any{"list": []any{"original"}}}
	input := candidateGraph(n)
	input["mode"] = "runtime"
	d := mustDefinition(t, input, nil)
	before, err := d.Digest()
	if err != nil {
		t.Fatal(err)
	}
	mutateJSON(input)
	mutateJSON(d.AsJSON())
	roots := d.Roots()
	roots[0].NodeID = "changed"
	nodes := d.Nodes()
	nodes[0] = NodeSpec{}
	node, ok := d.Node("tool")
	if !ok {
		t.Fatal("Missing node")
	}
	mutateJSON(node.AsJSON())
	mutateJSON(node.Extensions())
	schema, ok := node.InputContract()
	if !ok {
		t.Fatal("Missing assertion")
	}
	mutateJSON(schema)
	b, ok := node.Binding()
	if !ok {
		t.Fatal("Missing binding")
	}
	mutateJSON(b.AsJSON())
	secrets := b.SecretRefs()
	secrets[0] = "changed"
	if b.SecretRefs()[0] != "secret" || b.Portability().ContractID != "contract" || b.BindingID() != "binding" || b.Kind() != "registered" || b.Version() != "1" {
		t.Fatal("Binding state escaped")
	}
	after, err := d.Digest()
	if err != nil || before != after {
		t.Fatalf("Snapshot changed: %s %s %v", before, after, err)
	}
	wire, err := CanonicalJSON(d.AsJSON())
	if err != nil {
		t.Fatal(err)
	}
	roundtrip := mustDefinition(t, wire, nil)
	digest, _ := roundtrip.Digest()
	if before != digest {
		t.Fatal("Normalized roundtrip changed")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				mutateJSON(d.AsJSON())
				digest, e := d.Digest()
				if e != nil || digest != before {
					t.Error("Concurrent snapshot mutation")
				}
			}
		}()
	}
	wg.Wait()
}

func TestCandidateModelStrictFieldsAndTypes(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown graph field", func(g map[string]any) { g["future"] = true }},
		{"missing field", func(g map[string]any) { delete(g, "roots") }},
		{"version", func(g map[string]any) { g["semantic_version"] = "2" }},
		{"mode", func(g map[string]any) { g["mode"] = "future" }},
		{"node unknown", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["future"] = true }},
		{"node name slash", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["name"] = "a/b" }},
		{"node name control", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["name"] = "a\u0085" }},
		{"mutable", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["mutable"] = "true" }},
		{"duplicate node", func(g map[string]any) { g["nodes"] = append(g["nodes"].([]any), g["nodes"].([]any)[0]) }},
		{"instructions absent", func(g map[string]any) { delete(g["nodes"].([]any)[0].(map[string]any), "instructions") }},
		{"instructions both", func(g map[string]any) {
			g["nodes"].([]any)[0].(map[string]any)["instruction_template"] = map[string]any{"parts": []any{}}
		}},
		{"template unknown", func(g map[string]any) {
			n := g["nodes"].([]any)[0].(map[string]any)
			delete(n, "instructions")
			n["instruction_template"] = map[string]any{"parts": []any{map[string]any{"kind": "Future"}}}
		}},
		{"extension", func(g map[string]any) { g["extensions"] = map[string]any{"bare": true} }},
		{"extension newline", func(g map[string]any) { g["extensions"] = map[string]any{"ns:key\n": true} }},
		{"root unknown", func(g map[string]any) { g["roots"].([]any)[0].(map[string]any)["future"] = true }},
		{"plane", func(g map[string]any) { g["roots"].([]any)[0].(map[string]any)["plane"] = "future" }},
		{"skill members", func(g map[string]any) {
			n := g["nodes"].([]any)[0].(map[string]any)
			n["kind"] = "skill"
			n["members"] = map[string]any{}
		}},
		{"role binding", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["binding"] = candidateBinding() }},
		{"duplicate uses", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["uses"] = []any{"x", "x"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := candidateGraph(candidateNode("root", "role"))
			tc.edit(g)
			requireModelError(t, g, nil)
		})
	}
	for _, raw := range []string{
		`{"schema":"contexture.graph/v1","schema":"other"}`,
		`{"value":9007199254740993}`, `{"value":9007199254740993.0}`, `{"value":"\ud800"}`, `{} {}`, `{"value":1e9999}`,
	} {
		requireModelError(t, []byte(raw), nil)
	}
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	requireModelError(t, cyclic, nil)
	_, err := (&GraphDefinition{}).Digest()
	if err == nil {
		t.Fatal("Zero definition accepted")
	}
}

func TestCandidateModelBindingsAndMode(t *testing.T) {
	for _, edit := range []func(map[string]any){
		func(b map[string]any) { b["future"] = true },
		func(b map[string]any) { b["kind"] = "local" },
		func(b map[string]any) { b["version"] = "" },
		func(b map[string]any) { b["expected_contract_digest"] = "sha256:bad" },
		func(b map[string]any) { b["expected_contract_digest"] = "sha256:" + strings.Repeat("a", 64) + "\n" },
		func(b map[string]any) { b["config"] = []any{} },
		func(b map[string]any) { b["secret_refs"] = []any{"s", "s"} },
		func(b map[string]any) {
			b["portability"] = map[string]any{"kind": "portable", "contract_id": "c", "factory": "f"}
		},
		func(b map[string]any) {
			b["portability"] = map[string]any{"kind": "native", "language": "ruby", "version": "1", "factory": "f"}
		},
	} {
		b := candidateBinding()
		edit(b)
		_, err := NewPublicationContract(b, "read_only", true)
		if err == nil {
			t.Fatal("Invalid registry descriptor accepted")
		}
		n := candidateNode("t", "tool")
		n["binding"] = b
		g := candidateGraph(n)
		g["mode"] = "runtime"
		requireModelError(t, g, nil)
	}
	for _, language := range []string{"python", "typescript", "go"} {
		b := candidateBinding()
		b["portability"] = map[string]any{"kind": "native", "language": language, "version": "1", "factory": "factory"}
		b["expected_contract_digest"] = "sha256:" + strings.Repeat("a", 64)
		c, err := NewPublicationContract(b, "write", false)
		if err != nil || c.Binding().Portability().Language != language {
			t.Fatal(c, err)
		}
	}
	n := candidateNode("t", "tool")
	g := candidateGraph(n)
	g["mode"] = "runtime"
	requireModelError(t, g, nil)
	n["binding"] = candidateBinding()
	n["instructions"] = "bad"
	requireModelError(t, g, nil)
	delete(n, "instructions")
	g["mode"] = "disclosure_only"
	requireModelError(t, g, nil)
	delete(n, "binding")
	n["input_contract"] = true
	requireModelError(t, g, nil)
	if _, err := NewPublicationContract(candidateBinding(), "unknown", false); err == nil {
		t.Fatal("Invalid effect accepted")
	}
}

func TestCandidateModelInputContractSubset(t *testing.T) {
	valid := []any{true, false, map[string]any{
		"$id": "urn:schema", "$schema": "schema", "$ref": "#/x", "$anchor": "anchor", "$comment": "", "title": "T", "description": "D", "format": "unknown", "contentEncoding": "base64", "contentMediaType": "text/plain",
		"type": []any{"object", "null"}, "required": []any{"x"}, "properties": map[string]any{"x": map[string]any{"type": "string", "minLength": 0, "maxLength": 9}}, "$defs": map[string]any{"x": true}, "dependentSchemas": map[string]any{"x": false}, "dependentRequired": map[string]any{"x": []any{"y"}},
		"minimum": -2, "maximum": 2, "exclusiveMinimum": -3, "exclusiveMaximum": 3, "multipleOf": 0.5,
		"minItems": 0, "maxItems": 1, "minContains": 0, "maxContains": 1, "minProperties": 0, "maxProperties": 1,
		"readOnly": true, "writeOnly": false, "deprecated": false, "uniqueItems": true,
		"items": true, "contains": false, "additionalProperties": true, "propertyNames": true, "not": false, "if": true, "then": true, "else": false, "unevaluatedItems": true, "unevaluatedProperties": false, "contentSchema": true,
		"allOf": []any{true}, "anyOf": []any{true}, "oneOf": []any{false}, "prefixItems": []any{}, "enum": []any{1, "1"}, "const": nil, "default": map[string]any{}, "examples": []any{},
	}}
	for _, schema := range valid {
		n := candidateNode("t", "tool")
		n["binding"] = candidateBinding()
		n["input_contract"] = schema
		g := candidateGraph(n)
		g["mode"] = "runtime"
		mustDefinition(t, g, nil)
	}
	invalidSchemas := []any{nil, "schema", []any{}, map[string]any{"pattern": ".*"}, map[string]any{"patternProperties": map[string]any{}}, map[string]any{"$dynamicRef": "#x"}, map[string]any{"$vocabulary": map[string]any{}}, map[string]any{"type": []any{}}, map[string]any{"type": []any{"string", "string"}}, map[string]any{"type": "future"}, map[string]any{"required": []any{"x", "x"}}, map[string]any{"dependentRequired": map[string]any{"x": []any{"a", "a"}}}, map[string]any{"enum": []any{}}, map[string]any{"enum": []any{1, 1.0}}, map[string]any{"allOf": []any{}}, map[string]any{"items": 4}, map[string]any{"properties": map[string]any{"x": nil}}, map[string]any{"multipleOf": 0}, map[string]any{"minimum": "1"}, map[string]any{"minLength": -1}, map[string]any{"minItems": 0.5}, map[string]any{"readOnly": "true"}, map[string]any{"examples": "x"}, map[string]any{"$ref": 1}}
	for _, schema := range invalidSchemas {
		n := candidateNode("t", "tool")
		n["binding"] = candidateBinding()
		n["input_contract"] = schema
		g := candidateGraph(n)
		g["mode"] = "runtime"
		requireModelError(t, g, nil)
	}
}

func TestCandidateModelLocales(t *testing.T) {
	for input, expected := range map[string]string{"EN-latn-us": "en-Latn-US", "zh-cmn-hans-cn": "zh-cmn-Hans-CN", "de-CH-1901": "de-CH-1901", "X-PRIVATE": "x-private", "I-KLINGON": "i-klingon", "SGN-be-fr": "sgn-BE-FR", "en-u-CA-gregory-x-TEST": "en-u-ca-gregory-x-test"} {
		got, err := NormalizeLocale(input)
		if err != nil || got != expected {
			t.Fatalf("%s: %s %v", input, got, err)
		}
	}
	for _, input := range []string{"", "e", "en_US", "en--US", "x", "en-u", "en-u-ca-u-nu", "de-1901-1901", "é", "en-abcdefghi", "en\n"} {
		if _, err := NormalizeLocale(input); err == nil {
			t.Fatalf("Invalid locale %q accepted", input)
		}
	}
	g := candidateGraph(candidateNode("r", "role"))
	g["presentations"] = map[string]any{"default_locale": "EN", "catalogs": []any{map[string]any{"locale": "en-us", "entries": []any{map[string]any{"node_id": "r", "title": "Title"}}}}}
	d := mustDefinition(t, g, nil)
	p := d.Presentations()
	catalogs := p.Catalogs()
	entries := catalogs[0].Entries()
	entries[0].Title = "Changed"
	catalogs[0] = PresentationCatalog{}
	if p.DefaultLocale() != "en" || p.Catalogs()[0].Locale() != "en-US" || p.Catalogs()[0].Entries()[0].Title != "Title" {
		t.Fatal("Presentation state escaped")
	}
	for _, presentations := range []any{
		map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "EN-us", "entries": []any{}}, map[string]any{"locale": "en-US", "entries": []any{}}}},
		map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "r"}}}}},
		map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "r", "title": "X"}, map[string]any{"node_id": "r", "description": "Y"}}}}},
		map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "absent", "title": "X"}}}}},
	} {
		copy := detachedCandidate(t, g).(map[string]any)
		copy["presentations"] = presentations
		requireModelError(t, copy, nil)
	}
}

func TestCandidateModelSortedNodePool(t *testing.T) {
	a := candidateNode("\U00010000", "skill")
	b := candidateNode("\ue000", "skill")
	g := candidateGraph(a, b)
	d := mustDefinition(t, g, nil)
	nodes := d.AsJSON().(map[string]any)["nodes"].([]any)
	if !reflect.DeepEqual([]any{nodes[0].(map[string]any)["node_id"], nodes[1].(map[string]any)["node_id"]}, []any{"\ue000", "\U00010000"}) {
		t.Fatal("Node order must use Unicode scalars, not UTF16")
	}
	if d.Roots()[0].NodeID != "\U00010000" {
		t.Fatal("Authored root order changed")
	}
}
