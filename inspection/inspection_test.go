package inspection_test

import (
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/inspection"
)

func fixture(t *testing.T) *contexture.Disclosure {
	t.Helper()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "inspection", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate services.", Instructions: "Inspect first.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose incidents.", Instructions: "Read status."}
		}}}
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

func TestInspectionReplaysDisclosureAndPreservesRecoveryText(t *testing.T) {
	view := fixture(t)
	connected := inspection.ConnectStep(view, "Everything this server offers is behind contexture_open.")
	if connected.Call != inspection.Connect || !connected.Checks[1].OK {
		t.Fatalf("bad connect step: %#v", connected)
	}
	opened := inspection.OpenStep(view, "operations")
	if opened.Refused {
		t.Fatalf("open refused: %#v", opened)
	}
	want, err := view.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened.Body, "Operate services.") || opened.Payload == nil || want == nil {
		t.Fatalf("open did not replay disclosure: %#v", opened)
	}
	refused := inspection.OpenStep(view, "operations/nope")
	if !refused.Refused || !strings.Contains(refused.Body, "holds no member named 'nope'") {
		t.Fatalf("recovery lost: %#v", refused)
	}
	replay := inspection.Replay(t.Context(), view, nil, []string{"operations", "operations/nope"}, true, false, "")
	if len(replay.Steps) != 4 || len(replay.Failures()) != 1 {
		t.Fatalf("unexpected trace: %#v", replay)
	}
	if !strings.Contains(inspection.Render(replay, true), "[refused]") {
		t.Fatal("terminal renderer hid refusal")
	}
}

func TestInspectionCostAndBreadthFirstReferences(t *testing.T) {
	if cost := inspection.CostOf("a中"); cost.Characters != 2 || cost.Bytes != 4 || cost.Tokens != 1 {
		t.Fatalf("wrong CJK cost: %#v", cost)
	}
	if refs := inspection.EveryRef(fixture(t)); len(refs) != 2 || refs[0] != "operations" || refs[1] != "operations/diagnose" {
		t.Fatalf("wrong refs: %#v", refs)
	}
}

func TestConnectStepCountsOnlyRootRosterEntries(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "nested", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "root", Description: "Root.", Instructions: "Start here.", Children: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Continue here."}
		}}}
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
	step := inspection.ConnectStep(view, "contexture_open\n\nCapabilities:\n- root: Root.\n- root/child: Child.")
	if !step.Checks[2].OK {
		t.Fatalf("nested roster should list one root: %#v", step.Checks)
	}
}
