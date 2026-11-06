package graph

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustIndex(t *testing.T, d *GraphDefinition, contracts map[string]PublicationContract) *GraphIndex {
	t.Helper()
	x, err := BuildGraphIndex(d, contracts)
	if err != nil {
		t.Fatal(err)
	}
	return x
}
func nodeIDs(nodes []NodeSpec) []string {
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.NodeID())
	}
	return out
}
func forestCandidate() map[string]any {
	r, pre, child, post, skill, tool, prompt := candidateNode("r", "role"), candidateNode("pre", "role"), candidateNode("id/child", "role"), candidateNode("post", "role"), candidateNode("s", "skill"), candidateNode("t", "tool"), candidateNode("p", "skill")
	r["members"] = map[string]any{"pre_process": "pre", "children": []any{"id/child"}, "post_process": "post", "skills": []any{"s"}, "tools": []any{"t"}}
	r["mutable"] = true
	pre["role_variant"] = "pre_process"
	post["role_variant"] = "post_process"
	child["name"] = "child"
	child["mutable"] = false
	r["uses"] = []any{"s"}
	skill["uses"] = []any{"r"}
	delete(skill, "instructions")
	skill["instruction_template"] = map[string]any{"parts": []any{map[string]any{"kind": "Text", "text": "See "}, map[string]any{"kind": "NodeRef", "target_id": "id/child", "label": ""}, map[string]any{"kind": "NodeRef", "target_id": "id/child"}}}
	g := candidateGraph(tool, skill, child, pre, post, r, prompt)
	g["roots"] = []any{map[string]any{"node_id": "r", "plane": "model"}, map[string]any{"node_id": "p", "plane": "prompt"}}
	return g
}

func TestCandidateIndexTypedForestAndQueries(t *testing.T) {
	d := mustDefinition(t, forestCandidate(), nil)
	x := mustIndex(t, d, nil)
	want := []string{"r", "pre", "id/child", "post", "s", "t", "p"}
	if !reflect.DeepEqual(x.Preorder(), want) {
		t.Fatal(x.Preorder())
	}
	placements := d.nodes["r"].Members().Placements()
	for i, slot := range []Slot{PreProcess, Children, PostProcess, Skills, Tools} {
		if placements[i].Slot != slot || placements[i].Ordinal != 0 {
			t.Fatal(placements)
		}
		loc, ok := x.Location(placements[i].NodeID)
		if !ok || loc.MemberOrdinal != i || !loc.HasParent || !loc.HasSlot || loc.ParentID != "r" || loc.Slot != slot || loc.Depth != 1 || loc.RootOrdinal != 0 || loc.Plane != ModelPlane || loc.Preorder != i+1 || loc.SubtreeEnd != i+2 {
			t.Fatal(loc)
		}
	}
	root, _ := x.Location("r")
	if root.HasParent || root.HasSlot || root.SubtreeEnd != 6 || !root.EffectiveMutable {
		t.Fatal(root)
	}
	blocked, _ := x.Location("id/child")
	if !blocked.FalseBarrier || blocked.EffectiveMutable {
		t.Fatal(blocked)
	}
	if got := x.ReverseUses("r"); !reflect.DeepEqual(got, []string{"s"}) {
		t.Fatal(got)
	}
	if got := x.ReverseUses("s"); !reflect.DeepEqual(got, []string{"r"}) {
		t.Fatal(got)
	}
	if got := x.TemplateReferences("id/child"); !reflect.DeepEqual(got, []TemplateReference{{"s", 1}, {"s", 2}}) {
		t.Fatal(got)
	}
	for _, query := range []struct{ identity, by, expected string }{{"id/child", "id", "id/child"}, {"/r/child", "path", "id/child"}, {"/p", "path", "p"}} {
		n, err := x.Get(query.identity, query.by)
		if err != nil || n.NodeID() != query.expected {
			t.Fatal(n, err)
		}
	}
	for _, query := range [][2]string{{"/r/child", "id"}, {"id/child", "path"}, {"/r/child", "auto"}, {"/r/", "path"}, {"/", "path"}, {"/r/missing", "path"}, {"/r//child", "path"}} {
		if _, err := x.Get(query[0], query[1]); err == nil {
			t.Fatal(query)
		}
	}
	path, err := x.Path("id/child")
	if err != nil || path != "/r/child" {
		t.Fatal(path, err)
	}
	for _, plane := range []Plane{"", ModelPlane, PromptPlane} {
		nodes, err := x.Walk(plane)
		if err != nil {
			t.Fatal(err)
		}
		expected := want
		if plane == ModelPlane {
			expected = want[:6]
		}
		if plane == PromptPlane {
			expected = want[6:]
		}
		if !reflect.DeepEqual(nodeIDs(nodes), expected) {
			t.Fatal(nodeIDs(nodes))
		}
	}
	if _, err := x.Walk("unknown"); err == nil {
		t.Fatal("Invalid plane accepted")
	}
	nodes, err := x.Subtree("r", false)
	if err != nil || !reflect.DeepEqual(nodeIDs(nodes), want[1:6]) {
		t.Fatal(nodes, err)
	}
	nodes, err = x.Subtree("t", false)
	if err != nil || len(nodes) != 0 {
		t.Fatal(nodes, err)
	}
	for id, expected := range map[string]bool{"r": true, "p": false} {
		got, err := x.ModelMayOpen(id)
		if err != nil || got != expected {
			t.Fatal(id, got, err)
		}
	}
	if _, err := x.Path("absent"); err == nil {
		t.Fatal("Missing path target accepted")
	}
	if _, err := x.Subtree("absent", true); err == nil {
		t.Fatal("Missing subtree accepted")
	}
	if _, err := x.ModelMayOpen("absent"); err == nil {
		t.Fatal("Missing open target accepted")
	}
}

func TestCandidateIndexRejectsInvalidForest(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing target", func(g map[string]any) { g["roots"] = []any{map[string]any{"node_id": "absent", "plane": "model"}} }},
		{"duplicate root", func(g map[string]any) { g["roots"] = append(g["roots"].([]any), g["roots"].([]any)[0]) }},
		{"unplaced node", func(g map[string]any) { g["nodes"] = append(g["nodes"].([]any), candidateNode("orphan", "skill")) }},
		{"sibling across slots", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "s" {
					n["name"] = "child"
				}
			}
		}},
		{"root name across planes", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "p" {
					n["name"] = "r"
				}
			}
		}},
		{"typed slot", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "r" {
					n["members"].(map[string]any)["skills"] = []any{"t"}
				}
			}
		}},
		{"pre variant", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "pre" {
					n["role_variant"] = "ordinary"
				}
			}
		}},
		{"post variant", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "post" {
					n["role_variant"] = "pre_process"
				}
			}
		}},
		{"duplicate placement", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "r" {
					n["members"].(map[string]any)["children"] = []any{"id/child", "pre"}
				}
			}
		}},
		{"self uses", func(g map[string]any) { n := g["nodes"].([]any)[0].(map[string]any); n["uses"] = []any{n["node_id"]} }},
		{"missing uses", func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["uses"] = []any{"absent"} }},
		{"missing template target", func(g map[string]any) {
			for _, raw := range g["nodes"].([]any) {
				n := raw.(map[string]any)
				if n["node_id"] == "s" {
					n["instruction_template"].(map[string]any)["parts"] = []any{map[string]any{"kind": "NodeRef", "target_id": "absent"}}
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { g := forestCandidate(); tc.edit(g); requireModelError(t, g, nil) })
	}
	a, b := candidateNode("a", "role"), candidateNode("b", "role")
	a["members"] = map[string]any{"children": []any{"b"}}
	b["members"] = map[string]any{"children": []any{"a"}}
	g := candidateGraph(a, b)
	g["roots"] = []any{}
	requireModelError(t, g, nil)
	g["roots"] = []any{map[string]any{"node_id": "a", "plane": "model"}}
	requireModelError(t, g, nil)
	g["nodes"] = append(g["nodes"].([]any), candidateNode("root", "role"))
	g["roots"] = []any{map[string]any{"node_id": "root", "plane": "model"}}
	requireModelError(t, g, nil) // A detached cycle with otherwise valid placement counts.
	if _, err := BuildGraphIndex(nil, nil); err == nil {
		t.Fatal("Nil definition accepted")
	}
	if _, err := BuildGraphIndex(&GraphDefinition{}, nil); err == nil {
		t.Fatal("Zero definition accepted")
	}
	if _, err := (&GraphIndex{}).Get("r", "id"); err == nil {
		t.Fatal("Zero index accepted")
	}
}

func TestCandidateIndexDeep2000AndStableReverse(t *testing.T) {
	const depth = 2000
	nodes := make([]map[string]any, 0, depth+1)
	for i := 0; i <= depth; i++ {
		n := candidateNode(fmt.Sprintf("n%d", i), "role")
		n["name"] = "N"
		if i < depth {
			n["members"] = map[string]any{"children": []any{fmt.Sprintf("n%d", i+1)}}
		}
		if i == 0 {
			n["mutable"] = true
			n["uses"] = []any{fmt.Sprintf("n%d", depth)}
		} else {
			n["uses"] = []any{"n0"}
		}
		if i == 1000 {
			n["mutable"] = false
		}
		if i == 1001 {
			n["mutable"] = true
		}
		nodes = append(nodes, n)
	}
	// Deliberately reverse node-pool order independently of containment order.
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
	g := candidateGraph(nodes...)
	g["roots"] = []any{map[string]any{"node_id": "n0", "plane": "model"}}
	d := mustDefinition(t, g, nil)
	x := mustIndex(t, d, nil)
	for i, id := range x.Preorder() {
		if id != fmt.Sprintf("n%d", i) {
			t.Fatal(i, id)
		}
	}
	loc, ok := x.Location("n2000")
	if !ok || loc.Depth != depth || loc.SubtreeEnd != depth+1 || loc.EffectiveMutable || !loc.FalseBarrier {
		t.Fatal(loc)
	}
	path, err := x.Path("n2000")
	if err != nil || path != strings.Repeat("/N", depth+1) {
		t.Fatal("Deep path mismatch", err)
	}
	n, err := x.Get(path, "path")
	if err != nil || n.NodeID() != "n2000" {
		t.Fatal(n, err)
	}
	for i, id := range x.ReverseUses("n0") {
		if id != fmt.Sprintf("n%d", i+1) {
			t.Fatal("Unstable reverse order", i, id)
		}
	}
	for range 3 {
		again := mustIndex(t, d, nil)
		if !reflect.DeepEqual(x.Preorder(), again.Preorder()) || !reflect.DeepEqual(x.ReverseUses("n0"), again.ReverseUses("n0")) {
			t.Fatal("Unstable index")
		}
	}
	subtree, err := x.Subtree("n1000", true)
	if err != nil || len(subtree) != 1001 {
		t.Fatal(len(subtree), err)
	}
}

func executableCandidate(publications ...any) (map[string]any, map[string]PublicationContract) {
	n := candidateNode("t", "tool")
	b := candidateBinding()
	n["binding"] = b
	g := candidateGraph(n)
	g["mode"] = "runtime"
	g["publications"] = publications
	c, err := NewPublicationContract(b, "read_only", true)
	if err != nil {
		panic(err)
	}
	return g, map[string]PublicationContract{"t": c}
}
func routeCandidate(method string) map[string]any {
	return map[string]any{"kind": "route", "publication_id": "route", "opens_id": "t", "method": method, "path": "/data"}
}
func resourceCandidate(uri string) map[string]any {
	return map[string]any{"kind": "resource", "publication_id": "resource", "opens_id": "t", "name": "Resource", "uri": uri, "mime_type": "text/plain"}
}

func TestCandidateIndexPublicationContracts(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		g, contracts := executableCandidate(routeCandidate(method))
		effect := "read_only"
		if method != "GET" && method != "HEAD" {
			effect = "write"
		}
		c, err := NewPublicationContract(candidateBinding(), effect, false)
		if err != nil {
			t.Fatal(err)
		}
		contracts["t"] = c
		d := mustDefinition(t, g, contracts)
		mustIndex(t, d, contracts)
		wrong := "write"
		if effect == "write" {
			wrong = "read_only"
		}
		c, _ = NewPublicationContract(candidateBinding(), wrong, true)
		contracts["t"] = c
		requireModelError(t, g, contracts)
	}
	for _, uri := range []string{"urn:test", "https://example.test/x", "https://[::1]:443/x", "https://[v1.a]/x", "https://[::ffff:192.0.2.1]/x", "https://[fe80::1%25zone]/x"} {
		g, c := executableCandidate(resourceCandidate(uri))
		mustDefinition(t, g, c)
	}
	for _, uri := range []string{"relative", "https://a b", "urn:a\u0085b", "https://[no-ip]/x", "https://[::1/x", "https://::1]/x", "https://[::1]junk/x", "https://[vX.a]/x", "https://[1::2::3]/x", "https://[fe80::1%a%b]/x", "https://[fe80::1%]/x", "https://ｅxample.test/x"} {
		g, c := executableCandidate(resourceCandidate(uri))
		requireModelError(t, g, c)
	}
	g, contracts := executableCandidate(resourceCandidate("urn:test"))
	requireModelError(t, g, nil)
	requireModelError(t, g, map[string]PublicationContract{"t": {}})
	for _, changed := range []string{"version", "config", "secret_refs", "portability", "kind", "binding_id", "expected_contract_digest"} {
		b := candidateBinding()
		switch changed {
		case "version":
			b[changed] = "2"
		case "config":
			b[changed] = map[string]any{"different": true}
		case "secret_refs":
			b[changed] = []any{"another"}
		case "portability":
			b[changed] = map[string]any{"kind": "native", "language": "go", "version": "1", "factory": "f"}
		case "kind":
			b[changed] = "remote"
		case "binding_id":
			b[changed] = "another"
		case "expected_contract_digest":
			b[changed] = "sha256:" + strings.Repeat("a", 64)
		}
		c, err := NewPublicationContract(b, "read_only", true)
		if err != nil {
			t.Fatal(err)
		}
		requireModelError(t, g, map[string]PublicationContract{"t": c})
	}
	for _, config := range []struct {
		effect string
		free   bool
	}{{"write", true}, {"read_only", false}} {
		c, _ := NewPublicationContract(candidateBinding(), config.effect, config.free)
		requireModelError(t, g, map[string]PublicationContract{"t": c})
	}
	d := mustDefinition(t, g, contracts)
	if _, err := BuildGraphIndex(d, nil); err == nil {
		t.Fatal("Missing fresh build evidence accepted")
	}
	for _, edit := range []func(map[string]any){
		func(g map[string]any) {
			p := resourceCandidate("urn:other")
			p["publication_id"] = "other"
			g["publications"] = append(g["publications"].([]any), p)
		},
		func(g map[string]any) {
			p := resourceCandidate("urn:test")
			p["publication_id"] = "other"
			p["name"] = "Other"
			g["publications"] = append(g["publications"].([]any), p)
		},
		func(g map[string]any) {
			p := resourceCandidate("urn:other")
			p["name"] = "Other"
			g["publications"] = append(g["publications"].([]any), p)
		},
		func(g map[string]any) { g["publications"].([]any)[0].(map[string]any)["opens_id"] = "absent" },
		func(g map[string]any) { g["publications"].([]any)[0].(map[string]any)["future"] = true },
	} {
		copy := detachedCandidate(t, g).(map[string]any)
		edit(copy)
		requireModelError(t, copy, contracts)
	}
	g, contracts = executableCandidate(routeCandidate("GET"), routeCandidate("GET"))
	g["publications"].([]any)[1].(map[string]any)["publication_id"] = "other"
	requireModelError(t, g, contracts)
	g, contracts = executableCandidate(routeCandidate("GET"), routeCandidate("HEAD"))
	g["publications"].([]any)[1].(map[string]any)["publication_id"] = "head"
	mustDefinition(t, g, contracts)
	for _, edit := range []func(map[string]any){func(p map[string]any) { p["method"] = "get" }, func(p map[string]any) { p["path"] = "relative" }, func(p map[string]any) { p["name"] = "bad" }} {
		p := routeCandidate("GET")
		edit(p)
		g, c := executableCandidate(p)
		requireModelError(t, g, c)
	}
}

func TestCandidateIndexPromptReservedAndPresentations(t *testing.T) {
	g := forestCandidate()
	g["publications"] = []any{map[string]any{"kind": "prompt", "publication_id": "prompt", "opens_id": "s", "name": "Prompt", "description": "Description", "model_may_open": false}}
	g["presentations"] = map[string]any{"default_locale": "fr", "catalogs": []any{
		map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "s", "title": "English"}}},
		map[string]any{"locale": "en-US", "entries": []any{map[string]any{"node_id": "s", "description": "US"}}},
		map[string]any{"locale": "fr", "entries": []any{map[string]any{"node_id": "s", "title": "Default"}, map[string]any{"node_id": "r", "title": "Root"}}},
	}}
	d := mustDefinition(t, g, nil)
	x := mustIndex(t, d, nil)
	may, err := x.ModelMayOpen("s")
	if err != nil || may {
		t.Fatal(may, err)
	}
	for locale, title := range map[string]string{"EN-au": "English", "de-DE": "Default", "en-u-ca-gregory": "English", "x-private": "Default"} {
		p, ok, err := x.Presentation("s", locale)
		if err != nil || !ok || p.Title != title {
			t.Fatal(locale, p, ok, err)
		}
	}
	p, ok, err := x.Presentation("s", "en-US-x-private")
	if err != nil || !ok || p.Title != "" || p.Description != "US" || p.HasTitle || !p.HasDescription {
		t.Fatal("Fallback must return entry, not merge fields", p, ok, err)
	}
	if _, ok, err := x.Presentation("t", "en"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, _, err := x.Presentation("missing", "en"); err == nil {
		t.Fatal("Missing node accepted")
	}
	if _, _, err := x.Presentation("s", "bad_tag"); err == nil {
		t.Fatal("Invalid locale accepted")
	}
	if !reflect.DeepEqual(x.PublicationReferences("s"), []string{"prompt"}) || !reflect.DeepEqual(x.PresentationReferences("s"), []string{"en", "en-US", "fr"}) {
		t.Fatal("Reverse references lost authored order")
	}
	pubs := d.Publications()
	pubs[0].OpensID = "changed"
	if d.Publications()[0].OpensID != "s" {
		t.Fatal("Publications escaped")
	}
	g["publications"].([]any)[0].(map[string]any)["model_may_open"] = true
	d = mustDefinition(t, g, nil)
	x = mustIndex(t, d, nil)
	may, err = x.ModelMayOpen("s")
	if err != nil || !may {
		t.Fatal(may, err)
	}
}

func TestCandidateIndexSnapshotMutationAndRace(t *testing.T) {
	d := mustDefinition(t, forestCandidate(), nil)
	x := mustIndex(t, d, nil)
	original := x.Preorder()
	x.Preorder()[0] = "changed"
	x.RootNames()["r"] = "changed"
	x.ChildNames("r")["child"] = "changed"
	x.ReverseUses("r")[0] = "changed"
	x.TemplateReferences("id/child")[0].SourceID = "changed"
	parts := d.nodes["s"].Instructions().Parts()
	parts[0].Text = "changed"
	uses := d.nodes["s"].Uses()
	uses[0] = "changed"
	placements := d.nodes["r"].Members().Placements()
	placements[0].NodeID = "changed"
	copy := x.Definition()
	copy = GraphDefinition{}
	_ = copy
	*d = GraphDefinition{}
	if !reflect.DeepEqual(x.Preorder(), original) {
		t.Fatal("Traversal escaped")
	}
	node, err := x.Get("id/child", "id")
	if err != nil || node.Name() != "child" {
		t.Fatal(node, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				mutateJSON(x.definition.AsJSON())
				x.Preorder()[0] = "changed"
				x.RootNames()["r"] = "changed"
				n, e := x.Get("/r/child", "path")
				if e != nil || n.NodeID() != "id/child" {
					t.Error("Concurrent copy changed index")
				}
				p, e := x.Path("id/child")
				if e != nil || p != "/r/child" {
					t.Error(p, e)
				}
			}
		}()
	}
	wg.Wait()
	if x.TemplateReferences("id/child")[0].SourceID != "s" || x.ReverseUses("r")[0] != "s" || x.definition.nodes["s"].Instructions().Parts()[0].Text != "See " {
		t.Fatal("Mutable state escaped")
	}
}

func TestCandidateIndexEmptyForest(t *testing.T) {
	requireModelError(t, candidateGraph(), nil)
	g := candidateGraph(candidateNode("prompt", "skill"))
	g["roots"].([]any)[0].(map[string]any)["plane"] = "prompt"
	requireModelError(t, g, nil)
}

func TestCandidateTypedSnapshotsAndPromptNonInheritance(t *testing.T) {
	g := forestCandidate()
	pub := map[string]any{"kind": "prompt", "publication_id": "prompt", "opens_id": "r", "name": "Prompt", "description": "Description", "model_may_open": false}
	g["publications"] = []any{pub}
	d := mustDefinition(t, g, nil)
	x := mustIndex(t, d, nil)
	r, _ := d.Node("r")
	s, _ := d.Node("s")
	if r.Kind() != Role || r.RoleVariant() != "ordinary" || r.Description() != "Description" || r.Mutable() != MutableTrue || r.Instructions().IsTemplate() || r.Instructions().Text() != "" || !s.Instructions().IsTemplate() {
		t.Fatal("Typed model values differ")
	}
	reserved, err := x.ModelMayOpen("r")
	if err != nil || reserved {
		t.Fatal(reserved, err)
	}
	child, err := x.ModelMayOpen("s")
	if err != nil || !child {
		t.Fatal("Prompt reservation must not inherit", child, err)
	}
	x.PublicationReferences("r")[0] = "changed"
	if x.PublicationReferences("r")[0] != "prompt" {
		t.Fatal("Publication reverse references escaped")
	}
	pub["model_may_open"] = "false"
	requireModelError(t, g, nil)
	binding := candidateBinding()
	binding["expected_contract_digest"] = "sha256:" + strings.Repeat("a", 64)
	c, err := NewPublicationContract(binding, "read_only", true)
	if err != nil || c.Effect() != "read_only" || !c.ArgumentFree() || c.Binding().ExpectedContractDigest() != binding["expected_contract_digest"] {
		t.Fatal(c, err)
	}
	mutateJSON(binding)
	if c.Binding().BindingID() != "binding" {
		t.Fatal("Registry input escaped")
	}
	if _, err := (&GraphIndex{}).Walk(""); err == nil {
		t.Fatal("Zero index walk accepted")
	}
	if _, ok := d.Node("missing"); ok {
		t.Fatal("Missing node reported present")
	}
}

func requireDiagnostic(t *testing.T, err error, code, location string) {
	t.Helper()
	var diagnostic *GraphError
	if !errors.As(err, &diagnostic) || diagnostic.Code() != code || diagnostic.Location() != location {
		t.Fatalf("diagnostic = %#v, want code=%q location=%q", err, code, location)
	}
}

func TestCandidateDiagnosticJSONPointers(t *testing.T) {
	for name, value := range map[string]any{
		"nonfinite": math.Inf(1),
		"unsafe":    int64(MaxSafeInteger + 1),
		"unicode":   string([]byte{0xff}),
		"nonjson":   make(chan int),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := FreezeJSON(map[string]any{"a/b~c": []any{value}})
			requireDiagnostic(t, err, "schema_invalid", "/a~1b~0c/0")
		})
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	_, err := FreezeJSON(map[string]any{"cycle": cycle})
	requireDiagnostic(t, err, "schema_invalid", "/cycle/self")
	_, err = FreezeJSON(map[string]any{"object": map[string]any{string([]byte{0xff}): true}})
	requireDiagnostic(t, err, "schema_invalid", "/object")
	_, err = FreezeJSON(map[string]any{strings.Repeat("x", 4097): math.NaN()})
	requireDiagnostic(t, err, "schema_invalid", "")
}

func TestCandidateDiagnosticModelLocations(t *testing.T) {
	bad := candidateNode("r", "role")
	bad["name"] = ""
	_, err := NewGraphDefinition(candidateGraph(bad), nil)
	requireDiagnostic(t, err, "schema_invalid", "/nodes/0")
	_, err = NewGraphDefinition(candidateGraph(candidateNode("r", "role"), candidateNode("r", "role")), nil)
	requireDiagnostic(t, err, "schema_invalid", "/nodes/1/node_id")
	for _, identity := range []any{"", strings.Repeat("x", 257), true} {
		g := candidateGraph(candidateNode("r", "role"))
		g["graph_id"] = identity
		_, err := NewGraphDefinition(g, nil)
		requireDiagnostic(t, err, "schema_invalid", "/graph_id")
	}
	_, err = NewGraphDefinition(candidateGraph(), nil)
	requireDiagnostic(t, err, "schema_invalid", "/roots")
}

func TestCandidateDiagnosticMissingReferences(t *testing.T) {
	for _, edit := range []func(map[string]any){
		func(g map[string]any) { g["roots"] = []any{map[string]any{"node_id": "missing", "plane": "model"}} },
		func(g map[string]any) { g["nodes"].([]any)[0].(map[string]any)["uses"] = []any{"missing"} },
		func(g map[string]any) {
			n := g["nodes"].([]any)[0].(map[string]any)
			delete(n, "instructions")
			n["instruction_template"] = map[string]any{"parts": []any{map[string]any{"kind": "NodeRef", "target_id": "missing"}}}
		},
		func(g map[string]any) {
			g["nodes"].([]any)[0].(map[string]any)["members"] = map[string]any{"children": []any{"missing"}}
		},
		func(g map[string]any) {
			g["publications"] = []any{map[string]any{"kind": "prompt", "publication_id": "p", "name": "P", "description": "P", "opens_id": "missing", "model_may_open": false}}
		},
		func(g map[string]any) {
			g["presentations"] = map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en", "entries": []any{map[string]any{"node_id": "missing", "title": "Missing"}}}}}
		},
	} {
		g := candidateGraph(candidateNode("r", "role"))
		edit(g)
		_, err := NewGraphDefinition(g, nil)
		requireDiagnostic(t, err, "reference_not_found", "")
	}
	x := mustIndex(t, mustDefinition(t, forestCandidate(), nil), nil)
	for _, query := range [][2]string{{"missing", "id"}, {"/missing", "path"}, {"/r/missing", "path"}, {"/", "path"}} {
		_, err := x.Get(query[0], query[1])
		requireDiagnostic(t, err, "reference_not_found", "")
	}
	_, err := x.Get("relative", "path")
	requireDiagnostic(t, err, "schema_invalid", "")
}

func TestCandidateDiagnosticPublicationEvidence(t *testing.T) {
	g, contracts := executableCandidate(resourceCandidate("urn:test"))
	changed := candidateBinding()
	changed["version"] = "2"
	mismatch, err := NewPublicationContract(changed, "read_only", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []map[string]PublicationContract{nil, {"t": {}}, {"t": mismatch}} {
		_, err := NewGraphDefinition(g, evidence)
		requireDiagnostic(t, err, "binding_contract_required", "/publications/0")
	}
	d := mustDefinition(t, g, contracts)
	_, err = BuildGraphIndex(d, nil)
	requireDiagnostic(t, err, "binding_contract_required", "/publications/0")
	wrongEffect, err := NewPublicationContract(candidateBinding(), "write", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewGraphDefinition(g, map[string]PublicationContract{"t": wrongEffect})
	requireDiagnostic(t, err, "schema_invalid", "/publications/0")
	second := resourceCandidate("urn:test")
	second["publication_id"], second["name"] = "second", "Second"
	g["publications"] = append(g["publications"].([]any), second)
	_, err = NewGraphDefinition(g, contracts)
	requireDiagnostic(t, err, "schema_invalid", "/publications/1")
}
