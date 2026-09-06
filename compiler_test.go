package contexture_test

import (
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestCompileCreatesFreshCanonicalForest(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "operations",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node {
				return &contexture.Tool{Name: "status", Description: "Status.", ReadOnly: true}
			}}}
		}},
		PromptRoots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "command", Description: "Person command.", Instructions: "Wait."}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	second, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(first.Roots()); got != 2 {
		t.Fatalf("Roots len = %d, want 2", got)
	}
	if got := len(first.ModelRoots()); got != 1 {
		t.Fatalf("ModelRoots len = %d, want 1", got)
	}
	if got := len(first.PromptRoots()); got != 1 {
		t.Fatalf("PromptRoots len = %d, want 1", got)
	}
	firstRoot, _ := first.Find("operations")
	secondRoot, _ := second.Find("operations")
	if firstRoot == secondRoot {
		t.Fatal("compilations reused node identity")
	}
	tool, err := first.Find("operations/status")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := first.ParentOf(tool)
	if err != nil || parent == nil || parent.Name != "operations" {
		t.Fatalf("ParentOf = %#v, %v", parent, err)
	}
}

func TestCompileRejectsInvalidForest(t *testing.T) {
	shared := &contexture.Skill{Name: "shared", Description: "Shared.", Instructions: "Read."}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "invalid", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "left", Description: "Left.", Instructions: "Left.", Skills: []contexture.Factory{func() contexture.Node { return shared }}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "right", Description: "Right.", Instructions: "Right.", Skills: []contexture.Factory{func() contexture.Node { return shared }}}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(application); !errors.Is(err, contexture.ErrDuplicate) {
		t.Fatalf("Compile error = %v, want duplicate", err)
	}

	unresolved, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "uses", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Skill{Name: "skill", Description: "Skill.", Instructions: "Read.", Uses: []string{"missing"}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(unresolved); !errors.Is(err, contexture.ErrUnresolvedReference) {
		t.Fatalf("Compile error = %v, want unresolved", err)
	}
}
