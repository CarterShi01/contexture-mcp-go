package contexture_test

import (
	"context"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestDisclosureOnlyToolCardsAreStructuralAtRootAndNestedLevels(t *testing.T) {
	root, err := contexture.NewTool("root-status", "Read root status.", true, func(context.Context, struct{}) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := contexture.NewTool("nested-status", "Read nested status.", false, func(context.Context, struct{}) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "structural-tool-cards",
		Roots: []contexture.Factory{
			func() contexture.Node { return root },
			func() contexture.Node {
				return &contexture.Role{
					Name: "operations", Description: "Operate.", Instructions: "Inspect.",
					Tools: []contexture.Factory{func() contexture.Node { return nested }},
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	view, err := contexture.NewDisclosureOnly(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}

	discovered, err := view.Discover(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	assertStructuralToolCard(t, discovered["tools"][0])
	openedRoot, err := view.Open("root-status", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	assertStructuralToolCard(t, openedRoot)

	openedRole, err := view.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	nestedCards := openedRole["tools"].([]map[string]any)
	if len(nestedCards) != 1 {
		t.Fatalf("nested tool cards = %#v", openedRole["tools"])
	}
	assertStructuralToolCard(t, nestedCards[0])

	openedNested, err := view.Open("operations/nested-status", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	assertStructuralToolCard(t, openedNested)
}

func TestBoundToolDisclosureProjectsUsesInOrderWithoutCyclesOrLeaks(t *testing.T) {
	left, err := contexture.NewTool("alpha", "Read alpha.", true, func(context.Context, struct{}) (string, error) { return "alpha", nil })
	if err != nil {
		t.Fatal(err)
	}
	right, err := contexture.NewTool("beta", "Read beta.", true, func(context.Context, struct{}) (string, error) { return "beta", nil })
	if err != nil {
		t.Fatal(err)
	}
	third, err := contexture.NewTool("gamma", "Read gamma.", false, func(context.Context, struct{}) (string, error) { return "gamma", nil })
	if err != nil {
		t.Fatal(err)
	}
	left.Uses = []string{"beta", "gamma"}
	right.Uses = []string{"alpha"}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "tool-uses",
		Roots: []contexture.Factory{
			func() contexture.Node { return left },
			func() contexture.Node { return right },
			func() contexture.Node { return third },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	view, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}

	opened, err := view.Open("alpha", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opened["input_schema"].(map[string]any); !ok || opened["read_only"] != true {
		t.Fatalf("bound Tool lost execution facts: %#v", opened)
	}
	uses, ok := opened["uses"].([]map[string]any)
	if !ok || len(uses) != 2 {
		t.Fatalf("Tool uses = %#v", opened["uses"])
	}
	refs := []string{uses[0]["ref"].(string), uses[1]["ref"].(string)}
	if want := []string{"beta", "gamma"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("Tool uses order = %#v, want %#v", refs, want)
	}
	if _, hasNestedUses := uses[0]["uses"]; hasNestedUses {
		t.Fatalf("Tool use recursively disclosed a cycle: %#v", uses[0])
	}

	alphaOnly, err := contexture.OnlyRoots("alpha")
	if err != nil {
		t.Fatal(err)
	}
	attenuated, err := view.Open("alpha", alphaOnly)
	if err != nil {
		t.Fatal(err)
	}
	if uses, ok := attenuated["uses"].([]map[string]any); !ok || len(uses) != 0 {
		t.Fatalf("cross-root Tool uses leaked through selection: %#v", attenuated["uses"])
	}
}

func assertStructuralToolCard(t *testing.T, card map[string]any) {
	t.Helper()
	if card["kind"] != "tool" {
		t.Fatalf("card kind = %#v", card)
	}
	if _, exists := card["read_only"]; exists {
		t.Fatalf("disclosure-only Tool leaked read_only: %#v", card)
	}
	if _, exists := card["input_schema"]; exists {
		t.Fatalf("disclosure-only Tool leaked input_schema: %#v", card)
	}
}
