package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/inspection"
	serverinstructions "github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

func rolePublicationApplication(t *testing.T) *contexture.Application {
	t.Helper()
	save, err := contexture.NewTool("save", "Save the result.", false, func(_ context.Context, input struct {
		Value string `json:"value"`
	}) (map[string]any, error) {
		return map[string]any{"saved": input.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "publication",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{
				Name: "worker", Description: "Complete work.", Instructions: "  Preserve business whitespace.  ",
				Children: []contexture.Factory{func() contexture.Node {
					return &contexture.Role{Name: "branch", Description: "Alternative work.", Instructions: "Do branch work."}
				}},
				Publication: func() contexture.Node {
					return &contexture.Publication{
						Name: "publish", Description: "Preserve the result.", Instructions: "Review evidence before preserving it.",
						Tools: []contexture.Factory{func() contexture.Node { return save }},
					}
				},
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{Name: "draft", Description: "Draft the result.", Instructions: "Draft carefully."}
				}},
			}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func publicationJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPublicationIsRoleEquipmentAndClosingContract(t *testing.T) {
	index, err := contexture.Compile(rolePublicationApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	workerNode, err := index.Find("worker")
	if err != nil {
		t.Fatal(err)
	}
	worker := workerNode.(*contexture.Role)
	members, err := worker.Members()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.NodeName())
	}
	if !reflect.DeepEqual(names, []string{"branch", "publish", "draft"}) {
		t.Fatalf("members = %v", names)
	}
	branches, err := worker.Branches()
	if err != nil || len(branches) != 1 || branches[0].Name != "branch" {
		t.Fatalf("branches = %#v, %v", branches, err)
	}
	if !reflect.DeepEqual(index.Walk(), []string{"worker", "worker/branch", "worker/publish", "worker/publish/save", "worker/draft"}) {
		t.Fatalf("walk = %v", index.Walk())
	}
	levels := index.RolesByLevel()
	if got := []string{levels[0].Ref, levels[1].Ref}; !reflect.DeepEqual(got, []string{"worker", "worker/branch"}) || len(levels) != 2 {
		t.Fatalf("roles by level = %#v", levels)
	}
	signpost, err := index.Signpost("worker/publish/save")
	if err != nil || !reflect.DeepEqual(signpost, []contexture.SignpostLevel{{Ref: "worker", SubRoleCount: 1}, {Ref: "worker/publish", SubRoleCount: 0}}) {
		t.Fatalf("signpost = %#v, %v", signpost, err)
	}

	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := serverinstructions.Build(disclosure, contexture.AllRoots(), serverinstructions.RosterBudget)
	if err != nil || strings.Contains(bootstrap, "worker/publish") {
		t.Fatalf("bootstrap advertised Publication equipment: %q, %v", bootstrap, err)
	}
	opened, err := disclosure.Open("worker", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if opened["publication"] != "worker/publish" {
		t.Fatalf("publication = %#v", opened["publication"])
	}
	instructions := opened["instructions"].(string)
	if !strings.HasPrefix(instructions, "  Preserve business whitespace.  \n\n") || !strings.Contains(instructions, "call contexture_open") || !strings.Contains(instructions, "blocked, fails, or awaits approval") {
		t.Fatalf("instructions = %q", instructions)
	}
	if strings.Contains(publicationJSON(t, opened), "Review evidence") || strings.Contains(publicationJSON(t, opened), "input_schema") {
		t.Fatalf("owner inlined Publication details: %#v", opened)
	}
	published, err := disclosure.Open("worker/publish", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if published["instructions"] != "Review evidence before preserving it." || published["publication"] != nil {
		t.Fatalf("published = %#v", published)
	}
}

func TestPublicationPromotedSurfaceInspectionAndDisclosureOnly(t *testing.T) {
	index, err := contexture.Compile(rolePublicationApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := contexture.OnlyRoots("worker/publish")
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, selected)
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := disclosure.Discover(contexture.AllRoots())
	if err != nil || len(discovered["roles"]) != 1 || discovered["roles"][0]["ref"] != "worker/publish" {
		t.Fatalf("discover = %#v, %v", discovered, err)
	}
	if _, err := disclosure.Open("worker", contexture.AllRoots()); err == nil {
		t.Fatal("promoted Publication widened to owner")
	}
	graph, err := contexture.NewSelectedGraph(index, selected)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := graph.Find("worker/publish")
	if err != nil {
		t.Fatal(err)
	}
	if parent, err := graph.ParentOf(publication); err != nil || parent != nil {
		t.Fatalf("promoted parent = %#v, %v", parent, err)
	}
	all, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if got := inspection.EveryRef(all); !reflect.DeepEqual(got, []string{"worker", "worker/draft", "worker/branch", "worker/publish", "worker/publish/save"}) {
		t.Fatalf("inspection refs = %v", got)
	}

	structural, err := contexture.CompileDisclosure(rolePublicationApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	structuralView, err := contexture.NewDisclosureOnly(structural, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := structuralView.Open("worker/publish", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(publicationJSON(t, opened), "input_schema") {
		t.Fatalf("disclosure-only payload = %#v", opened)
	}
	if _, err := structural.BindingOf("worker/publish/save"); err == nil {
		t.Fatal("disclosure-only Publication exposed a Binding")
	}
}

func TestPublicationFactoryRejectsOrdinaryRole(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "invalid-publication", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "owner", Description: "Owner.", Instructions: "Work.", Publication: func() contexture.Node {
			return &contexture.Role{Name: "ordinary", Description: "Not designated.", Instructions: "Ordinary."}
		}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = contexture.Compile(application)
	if err == nil || !errors.Is(err, contexture.ErrInvalidDeclaration) || !strings.Contains(err.Error(), "*Publication") {
		t.Fatalf("ordinary publication error = %v", err)
	}
}

func TestControllerManagerPreservesPublicationSnapshots(t *testing.T) {
	manager := contexture.NewControllerManager()
	if _, err := manager.RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "worker", Description: "Work.", Instructions: "Work.", Publication: func() contexture.Node {
			return &contexture.Publication{Name: "publish", Description: "Preserve.", Instructions: "Save evidence."}
		}}
	}); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Compile("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Compile("second")
	if err != nil {
		t.Fatal(err)
	}
	firstPublication, err := first.Find("worker/publish")
	if err != nil {
		t.Fatal(err)
	}
	secondPublication, err := second.Find("worker/publish")
	if err != nil {
		t.Fatal(err)
	}
	if firstPublication.Kind() != contexture.RoleKind || secondPublication.Kind() != contexture.RoleKind || firstPublication == secondPublication {
		t.Fatalf("manager Publication snapshots = %#v %#v", firstPublication, secondPublication)
	}
}

func TestControllerManagerRejectsPublicationAsRoot(t *testing.T) {
	manager := contexture.NewControllerManager()
	_, err := manager.RegisterRoot(func() contexture.Node {
		return &contexture.Publication{Name: "publish", Description: "Preserve.", Instructions: "Save."}
	})
	if err == nil || !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("Publication root error = %v", err)
	}
}
