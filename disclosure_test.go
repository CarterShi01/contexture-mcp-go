package contexture_test

import (
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func disclosureFixture(t *testing.T) *contexture.Disclosure {
	t.Helper()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "view", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read.", Uses: []string{"operations/status"}}
		}}, Tools: []contexture.Factory{func() contexture.Node {
			return &contexture.Tool{Name: "status", Description: "Status.", ReadOnly: true}
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
