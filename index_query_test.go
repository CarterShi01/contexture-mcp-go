package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type indexChannels struct{}

func (indexChannels) Open(context.Context, contexture.CleanupRegistrar) error { return nil }
func (indexChannels) Close(context.Context) error                             { return nil }

func queryIndex(t *testing.T, channels contexture.Channels) *contexture.Index {
	t.Helper()
	read, err := contexture.NewToolWithSchema("read", "Read.", true, map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("write", "Write.", false, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "index-query", Channels: channels,
		Roots: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Route.", Children: []contexture.Factory{func() contexture.Node {
					return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Route.", Children: []contexture.Factory{func() contexture.Node {
						return &contexture.Role{Name: "grand", Description: "Grand.", Instructions: "Route."}
					}}}
				}}, Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read.", Uses: []string{"beta/read"}}
				}, func() contexture.Node {
					return &contexture.Skill{Name: "inspect", Description: "Inspect.", Instructions: "Read."}
				}}, Tools: []contexture.Factory{func() contexture.Node { return write }}}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Route.", Tools: []contexture.Factory{func() contexture.Node { return read }}}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func refs(items []contexture.NodeRef) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Ref)
	}
	return result
}

func TestIndexQueryFacadeRetainsCanonicalOrderAndDefensiveNodes(t *testing.T) {
	channels := indexChannels{}
	index := queryIndex(t, channels)
	if index.Count() != 8 || !index.Has("alpha/child/grand") || index.Has("alpha/nope") || !index.Bound() || index.Channels() != channels {
		t.Fatalf("index snapshot facts: count=%d bound=%t", index.Count(), index.Bound())
	}
	if got := refs(index.NodesWithRefs()); !reflect.DeepEqual(got, []string{"alpha", "alpha/child", "alpha/child/grand", "alpha/diagnose", "alpha/inspect", "alpha/write", "beta", "beta/read"}) {
		t.Fatalf("nodes = %#v", got)
	}
	if got := refs(index.RolesWithRefs()); !reflect.DeepEqual(got, []string{"alpha", "alpha/child", "alpha/child/grand", "beta"}) {
		t.Fatalf("roles dfs = %#v", got)
	}
	if got := refs(index.RolesByLevel()); !reflect.DeepEqual(got, []string{"alpha", "beta", "alpha/child", "alpha/child/grand"}) {
		t.Fatalf("roles bfs = %#v", got)
	}
	if got := refs(index.Skills()); !reflect.DeepEqual(got, []string{"alpha/diagnose", "alpha/inspect"}) {
		t.Fatalf("skills = %#v", got)
	}
	if got := index.OfKind(contexture.ToolKind); len(got) != 2 || got[0].NodeName() != "write" || got[1].NodeName() != "read" || len(index.OfKind(contexture.Kind("missing"))) != 0 {
		t.Fatalf("of kind = %#v", got)
	}
	// Returned values cannot mutate the compiled graph.
	role := index.OfKind(contexture.RoleKind)[0].(*contexture.Role)
	role.Name = "forged"
	if node, err := index.Find("alpha"); err != nil || node.NodeName() != "alpha" {
		t.Fatalf("defensive node = %#v, %v", node, err)
	}
}

func TestIndexQueryFacadeBindingMatchingSignpostAndCrossings(t *testing.T) {
	index := queryIndex(t, nil)
	binding, err := index.BindingOf("beta/read")
	if err != nil {
		t.Fatal(err)
	}
	schema := binding.Schema()
	schema["forged"] = true
	tool, err := index.Find("beta/read")
	if err != nil {
		t.Fatal(err)
	}
	again, err := index.SchemaOf(tool)
	if err != nil || again["forged"] != nil {
		t.Fatalf("schema defensive copy = %#v, %v", again, err)
	}
	if _, err := index.BindingOf("alpha"); !errors.Is(err, contexture.ErrNodeNotFound) {
		t.Fatalf("non-tool binding = %v", err)
	}
	if matches, total := index.MatchingRefs("ins", 1); total != 1 || !reflect.DeepEqual(matches, []string{"alpha/inspect"}) {
		t.Fatalf("matches = %#v, %d", matches, total)
	}
	if matches, total := index.MatchingRefs("", -1); total != index.Count() || len(matches) != 0 {
		t.Fatalf("negative limit = %#v, %d", matches, total)
	}
	if levels, err := index.Signpost("alpha/child/grand"); err != nil || !reflect.DeepEqual(levels, []contexture.SignpostLevel{{Ref: "alpha", SubRoleCount: 1}, {Ref: "alpha/child", SubRoleCount: 1}}) {
		t.Fatalf("signpost = %#v, %v", levels, err)
	}
	if _, err := index.Signpost("missing/child"); !errors.Is(err, contexture.ErrNodeNotFound) {
		t.Fatalf("unknown signpost = %v", err)
	}
	if got := index.Crossings(); !reflect.DeepEqual(got, []contexture.ReferenceCrossing{{SourceRef: "alpha/diagnose", TargetRef: "beta/read", TargetRoot: "beta"}}) {
		t.Fatalf("crossings = %#v", got)
	}
}

func TestIndexMatchingRefsRanksByRunesNotUTF8Bytes(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "unicode-matching", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "root", Description: "Root.", Instructions: "Route.", Children: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "a中", Description: "Chinese.", Instructions: "Route."}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "abcd", Description: "ASCII.", Instructions: "Route."}
			},
		}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	if matches, total := index.MatchingRefs("a", 1); total != 2 || !reflect.DeepEqual(matches, []string{"root/a中"}) {
		t.Fatalf("rune-length matching = %#v, %d", matches, total)
	}
}

func TestIndexFindAndSignpostCanonicalizeEmptySlashSegments(t *testing.T) {
	index := queryIndex(t, nil)
	node, err := index.Find("/alpha//child/")
	if err != nil || node.NodeName() != "child" {
		t.Fatalf("normalized Find = %#v, %v", node, err)
	}
	if ref, err := index.RefOf(node); err != nil || ref != "alpha/child" {
		t.Fatalf("normalized RefOf = %q, %v", ref, err)
	}
	levels, err := index.Signpost("/alpha//child//grand/")
	if err != nil || !reflect.DeepEqual(levels, []contexture.SignpostLevel{{Ref: "alpha", SubRoleCount: 1}, {Ref: "alpha/child", SubRoleCount: 1}}) {
		t.Fatalf("normalized Signpost = %#v, %v", levels, err)
	}
}

func TestDisclosureOnlyIndexQueryFacadeRefusesBindings(t *testing.T) {
	tool, err := contexture.NewTool("status", "Status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "unbound-index", Roots: []contexture.Factory{func() contexture.Node { return tool }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	if index.Bound() {
		t.Fatal("disclosure-only Index is bound")
	}
	if _, err := index.BindingOf("status"); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("unbound BindingOf = %v", err)
	}
}
