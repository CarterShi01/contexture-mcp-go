package contexture_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func disclosureFixture(t *testing.T) *contexture.Disclosure {
	t.Helper()
	status, err := contexture.NewTool("status", "Status.", true, func(context.Context, noInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "view", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read.", Uses: []string{"operations/status"}}
		}}, Tools: []contexture.Factory{func() contexture.Node {
			return status
		}}}
	}}, PromptRoots: []contexture.Factory{func() contexture.Node {
		return &contexture.Skill{Name: "command", Description: "Person.", Instructions: "Wait."}
	}}})
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
	return view
}

func TestDisclosureProjectsRootsAndOneSiblingLevel(t *testing.T) {
	view := disclosureFixture(t)
	discovered, err := view.Discover(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered["roles"]) != 1 || len(discovered["tools"]) != 0 {
		t.Fatalf("Discover = %#v", discovered)
	}
	opened, err := view.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if len(opened["skills"].([]map[string]any)) != 1 || len(opened["tools"].([]map[string]any)) != 1 {
		t.Fatalf("Open = %#v", opened)
	}
	if _, err := view.Open("command", contexture.AllRoots()); err == nil {
		t.Fatal("model opened Prompt root")
	}
	if _, err := view.OpenForPerson("command", contexture.AllRoots()); err != nil {
		t.Fatalf("person open: %v", err)
	}
}

func TestRootSelectionIsExactAndMonotonic(t *testing.T) {
	view := disclosureFixture(t)
	selection, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Open("operations/status", selection); err != nil {
		t.Fatalf("selected descendant: %v", err)
	}
	other, _ := contexture.OnlyRoots("command")
	if _, err := selection.Intersect(other); err == nil {
		t.Fatal("empty intersection accepted")
	}
	if _, err := contexture.OnlyRoots("operations/status"); err == nil {
		t.Fatal("descendant selection accepted")
	}
}

func TestDisclosureOmitsPromptRootFromModelVisibleUses(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "prompt-use",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Inspect.", Uses: []string{"command"}}
		}},
		PromptRoots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "command", Description: "Person only.", Instructions: "Wait."}
		}},
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
	opened, err := view.Open("diagnose", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	uses, ok := opened["uses"].([]map[string]any)
	if !ok || len(uses) != 0 {
		t.Fatalf("Prompt root leaked through Uses: %#v", opened)
	}
}
