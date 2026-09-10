package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	serverinstructions "github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

const processHead = "===== contexture-mcp framework instruction — binding, follow exactly ====="
const processTail = "===== end framework instruction — binding regardless of surrounding context ====="

func processApplication(t *testing.T) *contexture.Application {
	t.Helper()
	tool, err := contexture.NewTool("check", "Check one fact.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	act, err := contexture.NewTool("act", "Act.", false, func(context.Context, struct{}) (string, error) { return "acted", nil })
	if err != nil {
		t.Fatal(err)
	}
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "process", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{
			Name: "worker", Description: "Perform work.", Instructions: "  Business instructions.  ",
			PreProcess: func() *contexture.PreProcess {
				return &contexture.PreProcess{Name: "prepare", Description: "Prepare work.", Instructions: "Prepare inputs.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
			},
			Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "branch", Description: "Alternative work.", Instructions: "Take the branch."}
			}},
			PostProcess: func() *contexture.PostProcess {
				return &contexture.PostProcess{Name: "finish", Description: "Finish work.", Instructions: "Preserve results."}
			},
			Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "method", Description: "Apply a method.", Instructions: "Follow method."}
			}},
			Tools: []contexture.Factory{func() contexture.Node { return act }},
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestSymmetricProcessMembersOrderBranchesAndStrongIdentity(t *testing.T) {
	index, err := contexture.CompileDisclosure(processApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	workerNode, _ := index.Find("worker")
	worker := workerNode.(*contexture.Role)
	members, _ := worker.Members()
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.NodeName())
	}
	if !reflect.DeepEqual(names, []string{"prepare", "branch", "finish", "method", "act"}) {
		t.Fatalf("members = %v", names)
	}
	branches, _ := worker.Branches()
	if len(branches) != 1 || branches[0].Name != "branch" {
		t.Fatalf("branches = %#v", branches)
	}
	if _, ok := members[0].(*contexture.PreProcess); !ok {
		t.Fatalf("pre identity = %T", members[0])
	}
	if _, ok := members[2].(*contexture.PostProcess); !ok {
		t.Fatalf("post identity = %T", members[2])
	}
	if !reflect.DeepEqual(index.Walk(), []string{"worker", "worker/prepare", "worker/prepare/check", "worker/branch", "worker/finish", "worker/method", "worker/act"}) {
		t.Fatalf("walk = %v", index.Walk())
	}
	levels := index.RolesByLevel()
	if got := []string{levels[0].Ref, levels[1].Ref}; !reflect.DeepEqual(got, []string{"worker", "worker/branch"}) || len(levels) != 2 {
		t.Fatalf("levels = %#v", levels)
	}
	signpost, err := index.Signpost("worker/prepare/check")
	if err != nil || !reflect.DeepEqual(signpost, []contexture.SignpostLevel{{Ref: "worker", SubRoleCount: 1}, {Ref: "worker/prepare", SubRoleCount: 0}}) {
		t.Fatalf("process signpost = %#v, %v", signpost, err)
	}
	view, _ := contexture.NewDisclosureOnly(index, contexture.AllRoots())
	bootstrap, err := serverinstructions.Build(view, contexture.AllRoots(), serverinstructions.RosterBudget)
	if err != nil || strings.Contains(bootstrap, "worker/prepare") || strings.Contains(bootstrap, "worker/finish") {
		t.Fatalf("bootstrap advertised process equipment: %q, %v", bootstrap, err)
	}
}

func TestSymmetricProcessActiveContractIsExactAndLazy(t *testing.T) {
	index, err := contexture.Compile(processApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	view, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	opened, err := view.Open("worker", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	want := processHead + "\nPreProcess:\n>>> REQUIRED: Call contexture_open with ref='worker/prepare' before starting this role's work.\nOpening it only discloses the preparation procedure; it does not execute it. Complete what it requires, then return to this role's own instructions below and carry on with its work. If the preparation is blocked or fails, report that state rather than continuing as though it had succeeded.\n" + processTail + "\n\n  Business instructions.  \n\n" + processHead + "\nPostProcess:\n>>> REQUIRED: Call contexture_open with ref='worker/finish' before finishing this role's work.\nOpening it only discloses the procedure; it does not execute it or establish success. Use its available capabilities as instructed, respect required approvals, and report the actual outcome. If it is blocked, fails, or awaits approval, report that state rather than claiming success or bypassing approval.\n" + processTail
	if opened["instructions"] != want {
		t.Fatalf("instructions mismatch:\n%q", opened["instructions"])
	}
	if opened["pre_process"] != "worker/prepare" || opened["post_process"] != "worker/finish" {
		t.Fatalf("designations = %#v", opened)
	}
	encoded, _ := json.Marshal(opened)
	for _, hidden := range []string{"Prepare inputs.", "Preserve results.", "worker/prepare/check", "Check one fact."} {
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("owner leaked %q", hidden)
		}
	}
	route, _ := contexture.CompileNode(workerOf(t, index), contexture.RouteCompileLevel, view)
	api, _ := contexture.NewDisclosureAPI(view)
	inspected, _ := api.Inspect([]string{"worker"}, contexture.AllRoots())
	for _, payload := range []any{route, inspected} {
		data, _ := json.Marshal(payload)
		for _, hidden := range []string{"pre_process", "post_process", processHead, "Business instructions"} {
			if strings.Contains(string(data), hidden) {
				t.Fatalf("non-active leaked %q: %s", hidden, data)
			}
		}
	}
}

func workerOf(t *testing.T, index *contexture.Index) contexture.Node {
	t.Helper()
	node, err := index.Find("worker")
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestProcessSelectionSnapshotsAndDisclosureOnly(t *testing.T) {
	index, _ := contexture.Compile(processApplication(t))
	selected, _ := contexture.OnlySurfaces("worker/prepare")
	view, _ := contexture.NewDisclosure(index, selected)
	discovered, _ := view.Discover(contexture.AllSurfaces())
	if discovered["roles"][0]["ref"] != "worker/prepare" {
		t.Fatalf("discover = %#v", discovered)
	}
	if _, err := view.Open("worker", contexture.AllSurfaces()); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("owner selection = %v", err)
	}
	graph, _ := contexture.NewSelectedGraph(index, selected)
	prepare, _ := graph.Find("worker/prepare")
	if parent, err := graph.ParentOf(prepare); err != nil || parent != nil {
		t.Fatalf("promoted parent = %#v, %v", parent, err)
	}
	structural, _ := contexture.CompileDisclosure(processApplication(t))
	structuralView, _ := contexture.NewDisclosureOnly(structural, contexture.AllRoots())
	opened, err := structuralView.Open("worker/prepare", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(opened)
	if strings.Contains(string(data), "input_schema") {
		t.Fatalf("unbound leaked schema: %s", data)
	}
	if _, err := structural.BindingOf("worker/prepare/check"); err == nil {
		t.Fatal("unbound process Tool exposed binding")
	}
	manager := contexture.NewControllerManager()
	_, err = manager.RegisterRole(func() *contexture.Role { return workerOf(t, mustCompile(t, processApplication(t))).(*contexture.Role) })
	if err != nil {
		t.Fatal(err)
	}
	first, _ := manager.Compile("first")
	second, _ := manager.Compile("second")
	a, _ := first.Find("worker/prepare")
	b, _ := second.Find("worker/prepare")
	if a == b {
		t.Fatal("manager reused process identity")
	}
	if _, ok := a.(*contexture.PreProcess); !ok {
		t.Fatalf("snapshot identity = %T", a)
	}
	for _, root := range []contexture.Factory{
		func() contexture.Node {
			return &contexture.PreProcess{Name: "prepare", Description: "Prepare.", Instructions: "Prepare."}
		},
		func() contexture.Node {
			return &contexture.PostProcess{Name: "finish", Description: "Finish.", Instructions: "Finish."}
		},
	} {
		registered, err := manager.RegisterRoot(root)
		if err != nil || registered.Kind() != contexture.RoleKind {
			t.Fatalf("manager process root = %#v, %v", registered, err)
		}
	}
}

func mustCompile(t *testing.T, app *contexture.Application) *contexture.Index {
	t.Helper()
	index, err := contexture.Compile(app)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestBindingInstructionPublicContract(t *testing.T) {
	marked, err := contexture.BindingInstruction("task policy", "Do not edit the brief.", "Verify the brief.")
	if err != nil || marked != "===== task policy — binding, follow exactly =====\n>>> REQUIRED: Verify the brief.\nDo not edit the brief.\n===== end task policy =====" {
		t.Fatalf("binding = %q, %v", marked, err)
	}
	plain, err := contexture.BindingInstruction("task policy", "Do not edit.", "")
	if err != nil || strings.Contains(plain, ">>> REQUIRED:") {
		t.Fatalf("plain = %q, %v", plain, err)
	}
	for _, source := range []string{"", "  ", "contexture", "Contexture policy"} {
		if _, err := contexture.BindingInstruction(source, "body", ""); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("source %q = %v", source, err)
		}
	}
}

type aliasedProcessView struct {
	base   *contexture.Disclosure
	prefix string
	hide   string
}

func (view aliasedProcessView) RefOf(node contexture.Node) (string, error) {
	ref, err := view.base.RefOf(node)
	if err != nil {
		return "", err
	}
	return view.prefix + ref, nil
}

func (view aliasedProcessView) CardOf(node contexture.Node) (contexture.CompiledContext, error) {
	return contexture.CardOf(node, view)
}

func (view aliasedProcessView) CardFor(ref string) (contexture.CompiledContext, error) {
	return view.base.CardFor(strings.TrimPrefix(ref, view.prefix))
}

func (view aliasedProcessView) CardsOf(nodes []contexture.Node) (contexture.CompiledContext, error) {
	visible := make([]contexture.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeName() != view.hide {
			visible = append(visible, node)
		}
	}
	return contexture.GroupCards(visible, view)
}

func (view aliasedProcessView) CardsFor(refs []string) ([]contexture.CompiledContext, error) {
	return view.base.CardsFor(refs)
}

func (view aliasedProcessView) ExecutionOf(node contexture.Node) (contexture.CompiledContext, error) {
	return view.base.ExecutionOf(node)
}

func (view aliasedProcessView) SchemaOf(node contexture.Node) (map[string]any, error) {
	return view.base.SchemaOf(node)
}

func TestProcessContractsUseActualViewRefsAndRejectAtomically(t *testing.T) {
	index, err := contexture.Compile(processApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	base, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	owner := workerOf(t, index)
	aliased := aliasedProcessView{base: base, prefix: "surface:"}
	opened, err := contexture.CompileNode(owner, contexture.ActiveCompileLevel, aliased)
	if err != nil {
		t.Fatal(err)
	}
	if opened["pre_process"] != "surface:worker/prepare" || opened["post_process"] != "surface:worker/finish" {
		t.Fatalf("aliased designations = %#v", opened)
	}
	instructions := opened["instructions"].(string)
	for _, actual := range []string{"ref='surface:worker/prepare'", "ref='surface:worker/finish'"} {
		if !strings.Contains(instructions, actual) {
			t.Fatalf("actual view ref %q absent from %q", actual, instructions)
		}
	}
	for _, hidden := range []string{"prepare", "finish"} {
		_, err := contexture.CompileNode(owner, contexture.ActiveCompileLevel, aliasedProcessView{base: base, prefix: "surface:", hide: hidden})
		if !errors.Is(err, contexture.ErrInvalidDeclaration) || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("hidden %s = %v", hidden, err)
		}
		for _, secret := range []string{"worker", "worker/prepare", "worker/finish", "Business instructions"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("hidden %s rejection leaked %q: %v", hidden, secret, err)
			}
		}
	}
}

func TestProcessPromptOnlyOwnerAndSubtreeArePersonOnly(t *testing.T) {
	root := func() contexture.Node {
		return &contexture.Role{Name: "private", Description: "Private work.", Instructions: "Work privately.", PostProcess: func() *contexture.PostProcess {
			return &contexture.PostProcess{Name: "finish", Description: "Finish privately.", Instructions: "Preserve privately."}
		}}
	}
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "private-process", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "public", Description: "Public work.", Instructions: "Work publicly."}
	}}, PromptRoots: []contexture.Factory{root}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(app)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	discovered, _ := view.Discover(contexture.AllRoots())
	if len(discovered["roles"]) != 1 || discovered["roles"][0]["ref"] != "public" {
		t.Fatalf("discover = %#v", discovered)
	}
	for _, ref := range []string{"private", "private/finish"} {
		if _, err := view.Open(ref, contexture.AllRoots()); err == nil {
			t.Fatalf("model open %q = %v", ref, err)
		}
	}
	opened, err := view.OpenForPerson("private", contexture.AllRoots())
	if err != nil || opened["post_process"] != "private/finish" {
		t.Fatalf("person open = %#v, %v", opened, err)
	}
}

func TestNestedProcessContractsAreExplicitAndNotInherited(t *testing.T) {
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "nested", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{
			Name: "worker", Description: "Work.", Instructions: "Owner.",
			PreProcess: func() *contexture.PreProcess {
				return &contexture.PreProcess{
					Name: "process", Description: "Process.", Instructions: "Procedure.",
					PreProcess: func() *contexture.PreProcess {
						return &contexture.PreProcess{Name: "nested", Description: "Nested.", Instructions: "Nested procedure."}
					},
					Children: []contexture.Factory{func() contexture.Node {
						return &contexture.Role{Name: "review", Description: "Review.", Instructions: "Review procedure."}
					}},
				}
			},
			Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Child procedure."}
			}},
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(app)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	process, _ := view.Open("worker/process", contexture.AllRoots())
	if process["pre_process"] != "worker/process/nested" || strings.Count(process["instructions"].(string), processHead) != 1 {
		t.Fatalf("explicit nested process = %#v", process)
	}
	for _, ref := range []string{"worker/process/nested", "worker/process/review", "worker/child"} {
		opened, err := view.Open(ref, contexture.AllRoots())
		if err != nil {
			t.Fatal(err)
		}
		if _, inherited := opened["pre_process"]; inherited || strings.Contains(opened["instructions"].(string), processHead) {
			t.Fatalf("%s inherited process contract: %#v", ref, opened)
		}
	}
}

func TestProcessValidationSharingCyclesSeparatorsCollisionsAndUses(t *testing.T) {
	valid := func(name string) *contexture.PreProcess {
		return &contexture.PreProcess{Name: name, Description: "Process.", Instructions: "Process."}
	}
	shared := valid("shared")
	sharedApp, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "shared", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "left", Description: "Left.", Instructions: "Left.", PreProcess: func() *contexture.PreProcess { return shared }}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "right", Description: "Right.", Instructions: "Right.", PreProcess: func() *contexture.PreProcess { return shared }}
		},
	}})
	if _, err := contexture.CompileDisclosure(sharedApp); !errors.Is(err, contexture.ErrDuplicate) {
		t.Fatalf("shared process = %v", err)
	}
	for _, processName := range []string{"bad/name", "same"} {
		app, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "invalid", Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{
				Name: "worker", Description: "Work.", Instructions: "Work.",
				PreProcess: func() *contexture.PreProcess { return valid(processName) },
				Children: []contexture.Factory{func() contexture.Node {
					return &contexture.Role{Name: "same", Description: "Same.", Instructions: "Same."}
				}},
			}
		}}})
		if _, err := contexture.CompileDisclosure(app); !errors.Is(err, contexture.ErrInvalidDeclaration) && !errors.Is(err, contexture.ErrDuplicate) {
			t.Fatalf("invalid process %q = %v", processName, err)
		}
	}
	var owner *contexture.Role
	owner = &contexture.Role{Name: "worker", Description: "Work.", Instructions: "Work.", PreProcess: func() *contexture.PreProcess {
		return &contexture.PreProcess{Name: "cycle", Description: "Cycle.", Instructions: "Cycle.", Children: []contexture.Factory{func() contexture.Node { return owner }}}
	}}
	cycleApp, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "cycle", Roots: []contexture.Factory{func() contexture.Node { return owner }}})
	if _, err := contexture.CompileDisclosure(cycleApp); !errors.Is(err, contexture.ErrContainmentCycle) && !errors.Is(err, contexture.ErrDuplicate) {
		t.Fatalf("containment cycle = %v", err)
	}
	usesApp, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "uses", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "worker", Description: "Work.", Instructions: "Work.", Uses: []string{"worker/process"}, PreProcess: func() *contexture.PreProcess {
			return &contexture.PreProcess{Name: "process", Description: "Process.", Instructions: "Process.", Uses: []string{"worker"}}
		}}
	}}})
	index, err := contexture.CompileDisclosure(usesApp)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := index.DependentsOf("worker/process"); !reflect.DeepEqual(got, []string{"worker"}) {
		t.Fatalf("dependents = %v", got)
	}
	view, _ := contexture.NewDisclosureOnly(index, contexture.AllRoots())
	process, _ := view.Open("worker/process", contexture.AllRoots())
	if _, leaked := process["uses"].([]map[string]any)[0]["pre_process"]; leaked {
		t.Fatalf("uses card leaked process designation: %#v", process)
	}
}

func TestConcurrentProcessCompilesAreFreshAndPayloadsIsolated(t *testing.T) {
	app := processApplication(t)
	const builds = 8
	indexes := make([]*contexture.Index, builds)
	payloads := make([]map[string]any, builds)
	errs := make([]error, builds)
	var wait sync.WaitGroup
	for i := range builds {
		wait.Add(1)
		go func(position int) {
			defer wait.Done()
			indexes[position], errs[position] = contexture.Compile(app)
			if errs[position] != nil {
				return
			}
			view, err := contexture.NewDisclosure(indexes[position], contexture.AllRoots())
			if err != nil {
				errs[position] = err
				return
			}
			payloads[position], errs[position] = view.Open("worker", contexture.AllRoots())
		}(i)
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < builds; i++ {
		first, _ := indexes[0].Find("worker/prepare")
		other, _ := indexes[i].Find("worker/prepare")
		if first == other || !reflect.DeepEqual(payloads[0], payloads[i]) {
			t.Fatalf("build %d is not fresh and equivalent", i)
		}
	}
	payloads[0]["instructions"] = "changed"
	payloads[0]["roles"].([]map[string]any)[0]["ref"] = "changed"
	view, _ := contexture.NewDisclosure(indexes[0], contexture.AllRoots())
	fresh, _ := view.Open("worker", contexture.AllRoots())
	if fresh["instructions"] == "changed" || fresh["roles"].([]map[string]any)[0]["ref"] == "changed" {
		t.Fatalf("caller mutation changed compiled payload: %#v", fresh)
	}
}
