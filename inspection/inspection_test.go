package inspection_test

import (
	"context"
	"encoding/json"
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
	if cost := inspection.CostOf("😀"); cost.Characters != 1 || cost.Bytes != 4 || cost.Tokens != 0 {
		t.Fatalf("wrong astral cost: %#v", cost)
	}
	if refs := inspection.EveryRef(fixture(t)); len(refs) != 2 || refs[0] != "operations" || refs[1] != "operations/diagnose" {
		t.Fatalf("wrong refs: %#v", refs)
	}
}

func TestConnectStepCountsEveryVisibleRoleInTheRoster(t *testing.T) {
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
		t.Fatalf("nested roster should list every visible role: %#v", step.Checks)
	}
}

func TestInspectionReportsRoutingDuplicationAndKeepsAbsentFieldsNull(t *testing.T) {
	type noInput struct{}
	logs, err := contexture.NewTool("get_logs", "Read the diagnose output.", true, func(context.Context, noInput) (string, error) {
		return "logs", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.Contexture(contexture.ApplicationDeclaration{Name: "detail", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{
			Name: "root", Description: "Route work.", Instructions: "Start here.",
			Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "diagnose", Description: "Diagnose the service.", Instructions: "Inspect first."}
			}},
			Tools: []contexture.Factory{func() contexture.Node { return logs }},
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
	opened := inspection.OpenStep(view, "root")
	if len(opened.Checks) < 2 || opened.Checks[1].OK || !strings.Contains(opened.Checks[1].Note, "get_logs") {
		t.Fatalf("routing duplication was not reported: %#v", opened.Checks)
	}

	trace := inspection.Trace{Steps: []inspection.Step{{Call: "synthetic", Body: "body", Cost: inspection.CostOf("body")}}, Total: inspection.CostOf("body")}
	raw, err := inspection.AsJSON(trace)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Steps []struct {
			Ref   *string `json:"ref"`
			Aside *string `json:"aside"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Steps[0].Ref != nil || parsed.Steps[0].Aside != nil {
		t.Fatalf("absent optional fields must encode as null: %s", raw)
	}
	if !strings.Contains(inspection.Render(trace, false), "running") {
		t.Fatal("terminal renderer omitted the running cost summary")
	}
}
