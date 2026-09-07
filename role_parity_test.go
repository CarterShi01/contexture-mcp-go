package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestCompiledRoleMembershipQueriesAreOrderedDefensiveAndTyped(t *testing.T) {
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, noInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "role-members", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Choose the applicable branch.",
			Children: []contexture.Factory{
				func() contexture.Node {
					return &contexture.Role{Name: "incidents", Description: "Handle incidents.", Instructions: "Read evidence."}
				},
				func() contexture.Node {
					return &contexture.Role{Name: "changes", Description: "Handle changes.", Instructions: "Plan first."}
				},
			},
			Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "triage", Description: "Triage.", Instructions: "Classify."}
			}},
			Tools: []contexture.Factory{func() contexture.Node { return status }},
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	node, err := index.Find("operations")
	if err != nil {
		t.Fatal(err)
	}
	role := node.(*contexture.Role)

	branches, err := role.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if got := roleNames(branches); !reflect.DeepEqual(got, []string{"incidents", "changes"}) {
		t.Fatalf("Branches = %#v", got)
	}
	members, err := role.Members()
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeNames(members); !reflect.DeepEqual(got, []string{"incidents", "changes", "triage", "status"}) {
		t.Fatalf("Members = %#v", got)
	}
	// Returned nodes belong to a defensive snapshot, not the immutable Index.
	members[0].(*contexture.Role).Name = "forged"
	again, err := role.Members()
	if err != nil || again[0].NodeName() != "incidents" {
		t.Fatalf("Members defensive snapshot = %#v, %v", again, err)
	}

	member, err := role.Member("triage")
	if err != nil || member.Kind() != contexture.SkillKind || member.NodeName() != "triage" {
		t.Fatalf("Member(triage) = %#v, %v", member, err)
	}
	_, err = role.Member("absent")
	var missing *contexture.NodeNotFoundError
	if !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) || missing.Reason != contexture.NoSuchMember || missing.Ref != "" || missing.HasRef || missing.Scope != "operations" || !reflect.DeepEqual(missing.KnownRefs(), []string{"changes", "incidents", "status", "triage"}) {
		t.Fatalf("Member(absent) error = %#v", err)
	}
}

func TestRoleMembershipDoesNotEvaluateUncompiledFactories(t *testing.T) {
	built := 0
	role := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Children: []contexture.Factory{func() contexture.Node {
		built++
		return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Continue."}
	}}}
	if _, err := role.Members(); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("uncompiled Members = %v, factories built = %d", err, built)
	}
	if _, err := role.Branches(); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("uncompiled Branches = %v, factories built = %d", err, built)
	}
	if _, err := role.Member("child"); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("uncompiled Member = %v, factories built = %d", err, built)
	}
}

func TestCompileRejectsRoleSelfUsesAfterForestAssembly(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "self-use", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Uses: []string{"operations"}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(application); !errors.Is(err, contexture.ErrInvalidDeclaration) || errors.Is(err, contexture.ErrUnresolvedReference) {
		t.Fatalf("self Uses compile error = %v", err)
	}
}

func roleNames(nodes []*contexture.Role) []string {
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node.Name)
	}
	return result
}

func nodeNames(nodes []contexture.Node) []string {
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node.NodeName())
	}
	return result
}
