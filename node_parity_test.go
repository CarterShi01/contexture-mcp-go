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

func TestNodeFacadeCompilesRouteAndActivePayloadsThroughView(t *testing.T) {
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "node-lifecycle", Roots: []contexture.Factory{func() contexture.Node {
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
	view, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	var _ contexture.View = view

	root, err := index.Find("operations")
	if err != nil {
		t.Fatal(err)
	}
	route, err := contexture.CompileNode(root, contexture.RouteCompileLevel, nil)
	if err != nil || !reflect.DeepEqual(route, contexture.CompiledContext{"kind": "role", "name": "operations", "description": "Operate."}) {
		t.Fatalf("route CompileNode = %#v, %v", route, err)
	}
	active, err := contexture.CompileNode(root, contexture.ActiveCompileLevel, view)
	if err != nil {
		t.Fatal(err)
	}
	if active["ref"] != "operations" || active["instructions"] != "Choose work." {
		t.Fatalf("active Role facts = %#v", active)
	}
	if !reflect.DeepEqual(cardNames(active["roles"]), []string{"incidents"}) ||
		!reflect.DeepEqual(cardNames(active["skills"]), []string{"triage"}) ||
		!reflect.DeepEqual(cardNames(active["tools"]), []string{"status"}) {
		t.Fatalf("active Role groups = %#v", active)
	}

	tool, err := index.Find("operations/status")
	if err != nil {
		t.Fatal(err)
	}
	standalone, err := contexture.CompileNode(tool, contexture.ActiveCompileLevel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if standalone["ref"] != "operations/status" || standalone["read_only"] != true || !reflect.DeepEqual(standalone["input_schema"], map[string]any{}) {
		t.Fatalf("standalone compiled Tool = %#v", standalone)
	}
	if _, err := contexture.CompileNode(root, contexture.CompileLevel("middle"), view); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("unknown compile level = %v", err)
	}
}

func TestActiveCompilationDoesNotMutateViewOwnedRouteCards(t *testing.T) {
	cached := contexture.CompiledContext{"kind": "skill", "name": "triage", "description": "Triage."}
	view := &cachingNodeView{card: cached}
	skill := &contexture.Skill{Name: "triage", Description: "Triage.", Instructions: "Classify."}
	active, err := contexture.CompileNode(skill, contexture.ActiveCompileLevel, view)
	if err != nil {
		t.Fatal(err)
	}
	if active["instructions"] != "Classify." {
		t.Fatalf("active Skill = %#v", active)
	}
	if _, mutated := cached["instructions"]; mutated {
		t.Fatalf("CompileNode mutated View-owned route card: %#v", cached)
	}
}

func TestActiveRoleCompilationDoesNotAliasViewOwnedGroups(t *testing.T) {
	type labels []string
	cachedStrings := map[string]string{"owner": "original"}
	cachedNumbers := []int{1, 2}
	cachedFlags := []bool{}
	cachedNested := map[string]any{
		"labels":  labels{"original"},
		"strings": cachedStrings,
		"numbers": cachedNumbers,
		"flags":   cachedFlags,
	}
	cachedGroups := contexture.CompiledContext{
		"roles":  []contexture.CompiledContext{{"name": "incidents", "nested": cachedNested}},
		"skills": []contexture.CompiledContext{},
		"tools":  []contexture.CompiledContext{},
	}
	view := &cachingNodeView{card: contexture.CompiledContext{"kind": "role", "name": "operations", "description": "Operate."}, groups: cachedGroups}
	role := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
	active, err := contexture.CompileNode(role, contexture.ActiveCompileLevel, view)
	if err != nil {
		t.Fatal(err)
	}
	roles := active["roles"].([]contexture.CompiledContext)
	roles[0]["name"] = "mutated"
	activeNested := roles[0]["nested"].(map[string]any)
	activeNested["labels"].(labels)[0] = "mutated"
	activeNested["strings"].(map[string]string)["owner"] = "mutated"
	activeNested["numbers"].([]int)[0] = 99
	activeNested["flags"] = append(activeNested["flags"].([]bool), true)
	cachedRoles := cachedGroups["roles"].([]contexture.CompiledContext)
	if cachedRoles[0]["name"] != "incidents" ||
		cachedNested["labels"].(labels)[0] != "original" ||
		cachedStrings["owner"] != "original" ||
		cachedNumbers[0] != 1 ||
		len(cachedFlags) != 0 || cachedFlags == nil {
		t.Fatalf("CompileNode aliased View-owned groups: %#v", cachedGroups)
	}
}

func TestStandaloneRawRoleRequiresNoLazyMemberEvaluation(t *testing.T) {
	raw := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
	active, err := contexture.CompileNode(raw, contexture.ActiveCompileLevel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if active["ref"] != "operations" || !reflect.DeepEqual(cardNames(active["roles"]), []string{}) || !reflect.DeepEqual(cardNames(active["skills"]), []string{}) || !reflect.DeepEqual(cardNames(active["tools"]), []string{}) {
		t.Fatalf("standalone raw Role = %#v", active)
	}

	built := 0
	withMember := &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
		built++
		return &contexture.Skill{Name: "triage", Description: "Triage.", Instructions: "Classify."}
	}}}
	if _, err := contexture.CompileNode(withMember, contexture.ActiveCompileLevel, nil); !errors.Is(err, contexture.ErrInvalidDeclaration) || built != 0 {
		t.Fatalf("raw Role with member = %v, factory builds %d", err, built)
	}
	for _, node := range []contexture.Node{
		&contexture.Role{Name: "operations", Description: "Operate.", Instructions: " \t "},
		&contexture.Skill{Name: "triage", Description: "Triage.", Instructions: " \t "},
	} {
		if _, err := contexture.CompileNode(node, contexture.ActiveCompileLevel, nil); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("blank active instructions = %v", err)
		}
	}
}

func TestNodeLifecycleRejectsTypedNilBeforeCallingAView(t *testing.T) {
	var role *contexture.Role
	var skill *contexture.Skill
	var tool *contexture.Tool
	view := &countingNodeView{}
	for _, node := range []contexture.Node{role, skill, tool} {
		if _, err := contexture.BranchesOf(node); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("BranchesOf typed nil = %v", err)
		}
		if _, err := contexture.MembersOf(node); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("MembersOf typed nil = %v", err)
		}
		if _, err := contexture.CardOf(node, view); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("CardOf typed nil = %v", err)
		}
		if _, err := contexture.CompileNode(node, contexture.ActiveCompileLevel, view); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("CompileNode typed nil = %v", err)
		}
	}
	if view.calls != 0 {
		t.Fatalf("typed-nil lifecycle called View %d times", view.calls)
	}

	var disclosure *contexture.Disclosure
	var nilView contexture.View = disclosure
	valid := &contexture.Skill{Name: "triage", Description: "Triage.", Instructions: "Classify."}
	if _, err := contexture.CardOf(valid, nilView); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil View CardOf = %v", err)
	}
	if _, err := contexture.GroupCards([]contexture.Node{valid}, nilView); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil View GroupCards = %v", err)
	}
	if _, err := contexture.CompileNode(valid, contexture.ActiveCompileLevel, nilView); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil View CompileNode = %v", err)
	}
}

func TestGroupCardsReturnsEveryClosedKindBucketInDeclarationOrder(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "group-cards", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Choose.",
			Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "incidents", Description: "Incidents.", Instructions: "Inspect."}
			}},
			Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "triage", Description: "Triage.", Instructions: "Classify."}
			}},
		}
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
	root, _ := index.Find("operations")
	members, _ := contexture.MembersOf(root)
	grouped, err := contexture.GroupCards(members, view)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cardNames(grouped["roles"]), []string{"incidents"}) || !reflect.DeepEqual(cardNames(grouped["skills"]), []string{"triage"}) || !reflect.DeepEqual(cardNames(grouped["tools"]), []string{}) {
		t.Fatalf("GroupCards = %#v", grouped)
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

func cardNames(value any) []string {
	cards := value.([]contexture.CompiledContext)
	names := make([]string, 0, len(cards))
	for _, card := range cards {
		names = append(names, card["name"].(string))
	}
	return names
}

type cachingNodeView struct {
	card   contexture.CompiledContext
	groups contexture.CompiledContext
}

func (view *cachingNodeView) RefOf(contexture.Node) (string, error) { return "triage", nil }
func (view *cachingNodeView) CardOf(contexture.Node) (contexture.CompiledContext, error) {
	return view.card, nil
}
func (view *cachingNodeView) CardFor(string) (contexture.CompiledContext, error) {
	return nil, errors.New("unexpected CardFor")
}
func (view *cachingNodeView) CardsOf([]contexture.Node) (contexture.CompiledContext, error) {
	if view.groups != nil {
		return view.groups, nil
	}
	return contexture.CompiledContext{"roles": []contexture.CompiledContext{}, "skills": []contexture.CompiledContext{}, "tools": []contexture.CompiledContext{}}, nil
}
func (view *cachingNodeView) CardsFor([]string) ([]contexture.CompiledContext, error) {
	return []contexture.CompiledContext{}, nil
}
func (view *cachingNodeView) ExecutionOf(contexture.Node) (contexture.CompiledContext, error) {
	return contexture.CompiledContext{}, nil
}
func (view *cachingNodeView) SchemaOf(contexture.Node) (map[string]any, error) {
	return map[string]any{}, nil
}

type countingNodeView struct{ calls int }

func (view *countingNodeView) RefOf(contexture.Node) (string, error) {
	view.calls++
	return "", nil
}
func (view *countingNodeView) CardOf(contexture.Node) (contexture.CompiledContext, error) {
	view.calls++
	return contexture.CompiledContext{}, nil
}
func (view *countingNodeView) CardFor(string) (contexture.CompiledContext, error) {
	view.calls++
	return contexture.CompiledContext{}, nil
}
func (view *countingNodeView) CardsOf([]contexture.Node) (contexture.CompiledContext, error) {
	view.calls++
	return contexture.CompiledContext{}, nil
}
func (view *countingNodeView) CardsFor([]string) ([]contexture.CompiledContext, error) {
	view.calls++
	return nil, nil
}
func (view *countingNodeView) ExecutionOf(contexture.Node) (contexture.CompiledContext, error) {
	view.calls++
	return contexture.CompiledContext{}, nil
}
func (view *countingNodeView) SchemaOf(contexture.Node) (map[string]any, error) {
	view.calls++
	return map[string]any{}, nil
}
