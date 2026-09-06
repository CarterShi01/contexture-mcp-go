package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestCompileCreatesFreshCanonicalForest(t *testing.T) {
	status, err := contexture.NewTool("status", "Status.", true, func(context.Context, noInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "operations",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node {
				return status
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

func TestCompiledIndexIsAnImmutableSnapshot(t *testing.T) {
	declaredSkill := &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read.", Uses: []string{"operations/status"}}
	status, err := contexture.NewTool("status", "Status.", true, func(context.Context, noInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	declaredRole := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node { return declaredSkill }}, Tools: []contexture.Factory{func() contexture.Node { return status }}}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "immutable", Roots: []contexture.Factory{func() contexture.Node { return declaredRole }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}

	declaredRole.Name = "changed"
	declaredRole.Description = "Changed."
	declaredSkill.Uses[0] = "changed"
	status.ReadOnly = false
	root, _ := index.Find("operations")
	root.(*contexture.Role).Name = "also-changed"
	compiledStatus, _ := index.Find("operations/status")
	compiledStatus.(*contexture.Tool).ReadOnly = false

	again, err := index.Find("operations")
	if err != nil || again.(*contexture.Role).Name != "operations" || again.(*contexture.Role).Description != "Operate." {
		t.Fatalf("compiled root changed through an external alias: %#v, %v", again, err)
	}
	againStatus, err := index.Find("operations/status")
	if err != nil || !againStatus.(*contexture.Tool).ReadOnly {
		t.Fatalf("compiled Tool changed through an external alias: %#v, %v", againStatus, err)
	}
	dependents, err := index.DependentsOf("operations/status")
	if err != nil || !reflect.DeepEqual(dependents, []string{"operations/diagnose"}) {
		t.Fatalf("compiled uses graph changed through declaration alias: %#v, %v", dependents, err)
	}
	if ref, err := index.RefOf(againStatus); err != nil || ref != "operations/status" {
		t.Fatalf("RefOf defensive snapshot = %q, %v", ref, err)
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

func TestCompileRejectsCyclesNamesAndCrossKindDuplicates(t *testing.T) {
	tests := []struct {
		name    string
		factory contexture.Factory
		want    error
	}{
		{
			name: "cycle",
			factory: func() contexture.Node {
				var recursive contexture.Factory
				recursive = func() contexture.Node {
					return &contexture.Role{Name: "loop", Description: "Loop.", Instructions: "Loop.", Children: []contexture.Factory{recursive}}
				}
				return recursive()
			},
			want: contexture.ErrContainmentCycle,
		},
		{
			name: "cross kind duplicate",
			factory: func() contexture.Node {
				return &contexture.Role{Name: "root", Description: "Root.", Instructions: "Root.", Skills: []contexture.Factory{
					func() contexture.Node {
						return &contexture.Skill{Name: "same", Description: "Skill.", Instructions: "Do."}
					},
				}, Tools: []contexture.Factory{
					func() contexture.Node { return &contexture.Tool{Name: "same", Description: "Tool."} },
				}}
			},
			want: contexture.ErrDuplicate,
		},
		{
			name: "separator ambiguity",
			factory: func() contexture.Node {
				return &contexture.Skill{Name: "not/a-name", Description: "Skill.", Instructions: "Do."}
			},
			want: contexture.ErrInvalidDeclaration,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "negative", Roots: []contexture.Factory{test.factory}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = contexture.Compile(application)
			if !errors.Is(err, test.want) {
				t.Fatalf("Compile error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "empty"}); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("empty application = %v", err)
	}
}
