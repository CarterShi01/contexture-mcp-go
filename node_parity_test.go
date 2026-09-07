package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestNodeFacadePreservesCompiledReferenceAndContainmentAcrossKinds(t *testing.T) {
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "node-facade", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Choose work.",
			Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "incidents", Description: "Handle incidents.", Instructions: "Read evidence."}
			}},
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

	root, err := index.Find("operations")
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := root.Ref(); err != nil || ref != "operations" {
		t.Fatalf("root Ref = %q, %v", ref, err)
	}
	branches, err := contexture.BranchesOf(root)
	if err != nil || !reflect.DeepEqual(nodeNames(branches), []string{"incidents"}) {
		t.Fatalf("BranchesOf(root) = %#v, %v", branches, err)
	}
	members, err := contexture.MembersOf(root)
	if err != nil || !reflect.DeepEqual(nodeNames(members), []string{"incidents", "triage", "status"}) {
		t.Fatalf("MembersOf(root) = %#v, %v", members, err)
	}
	for _, item := range []struct {
		ref  string
		kind contexture.Kind
	}{
		{ref: "operations/incidents", kind: contexture.RoleKind},
		{ref: "operations/triage", kind: contexture.SkillKind},
		{ref: "operations/status", kind: contexture.ToolKind},
	} {
		node, findErr := index.Find(item.ref)
		if findErr != nil || node.Kind() != item.kind {
			t.Fatalf("Find(%q) = %#v, %v", item.ref, node, findErr)
		}
		if ref, refErr := node.Ref(); refErr != nil || ref != item.ref {
			t.Fatalf("Node Ref for %q = %q, %v", item.ref, ref, refErr)
		}
		branches, branchErr := contexture.BranchesOf(node)
		members, memberErr := contexture.MembersOf(node)
		if item.kind != contexture.RoleKind && (branchErr != nil || memberErr != nil || len(branches) != 0 || len(members) != 0) {
			t.Fatalf("leaf Node containment for %q = branches %#v/%v members %#v/%v", item.ref, branches, branchErr, members, memberErr)
		}
	}
}

func TestNodeReferenceIsCanonicalAndSnapshotsRemainImmutable(t *testing.T) {
	tool, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	declaration := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "node-snapshot", Roots: []contexture.Factory{func() contexture.Node { return declaration }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	declaration.Name, declaration.Description = "forged", "Forged."
	tool.Name, tool.Description, tool.ReadOnly = "forged", "Forged.", false

	returned, err := index.Find("operations/status")
	if err != nil {
		t.Fatal(err)
	}
	returned.(*contexture.Tool).Name = "caller-mutation"
	if ref, err := returned.Ref(); err != nil || ref != "operations/status" {
		t.Fatalf("mutated snapshot Ref = %q, %v", ref, err)
	}
	if ref, err := index.RefOf(returned); err != nil || ref != "operations/status" {
		t.Fatalf("Index RefOf mutated snapshot = %q, %v", ref, err)
	}
	again, err := index.Find("operations/status")
	if err != nil || again.NodeName() != "status" || again.NodeDescription() != "Read status." || !again.(*contexture.Tool).ReadOnly {
		t.Fatalf("compiled Tool changed through declaration/node alias: %#v, %v", again, err)
	}
}

func TestNodeFacadeRejectsUncompiledOrTypedNilIdentityWithoutFactoryEvaluation(t *testing.T) {
	built := 0
	uncompiled := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Children: []contexture.Factory{func() contexture.Node {
		built++
		return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Continue."}
	}}}
	if _, err := uncompiled.Ref(); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("uncompiled Ref = %v", err)
	}
	if _, err := contexture.BranchesOf(uncompiled); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("uncompiled BranchesOf = %v, factory builds %d", err, built)
	}
	if _, err := contexture.MembersOf(uncompiled); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("uncompiled MembersOf = %v, factory builds %d", err, built)
	}

	var typedNil *contexture.Skill
	if _, err := typedNil.Ref(); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil Ref = %v", err)
	}
}
