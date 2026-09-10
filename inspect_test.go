package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func inspectApplication(t *testing.T, collector contexture.Telemetry, calls *int) *contexture.Application {
	t.Helper()
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) {
		*calls++
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "inspect", Telemetry: collector,
		Roots: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{
					Name: "team", Description: "Coordinate work.", Instructions: "Adopt the team procedure.",
					Children: []contexture.Factory{func() contexture.Node {
						return &contexture.Role{Name: "candidate", Description: "Candidate role.", Instructions: "Act as candidate.", Uses: []string{"services/status"}}
					}},
					PostProcess: func() *contexture.PostProcess {
						return &contexture.PostProcess{Name: "publish", Description: "Preserve results.", Instructions: "Publish evidence."}
					},
					Skills: []contexture.Factory{func() contexture.Node {
						return &contexture.Skill{Name: "diagnose", Description: "Diagnose incidents.", Instructions: "Follow diagnosis."}
					}},
				}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "services", Description: "Service operations.", Instructions: "Operate services.", Tools: []contexture.Factory{func() contexture.Node { return status }}}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func TestInspectReturnsOnlyDirectPureRoutingCardsWithoutEffects(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	calls := 0
	index, err := contexture.Compile(inspectApplication(t, collector, &calls))
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosureWithTelemetry(index, contexture.AllRoots(), collector)
	if err != nil {
		t.Fatal(err)
	}
	api, err := contexture.NewDisclosureAPI(disclosure)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := api.Inspect([]string{" team/candidate ", "services/status"}, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	items := payload["items"].([]contexture.CompiledContext)
	if len(items) != 2 || items[0]["node"].(contexture.CompiledContext)["ref"] != "team/candidate" || items[1]["node"].(contexture.CompiledContext)["ref"] != "services/status" {
		t.Fatalf("inspect request order = %#v", items)
	}
	uses := items[0]["uses"].([]contexture.CompiledContext)
	if len(uses) != 1 || uses[0]["ref"] != "services/status" || !reflect.DeepEqual(sortedKeys(uses[0]), []string{"description", "kind", "name", "ref"}) {
		t.Fatalf("pure uses = %#v", uses)
	}
	if calls != 0 {
		t.Fatalf("inspect invoked Tool %d times", calls)
	}
	itemsJSON, _ := json.Marshal(payload["items"])
	for _, forbidden := range []string{"instructions", "input_schema", "read_only", "publication", "framework contract", "result", "Act as candidate", "Publish evidence"} {
		if strings.Contains(string(itemsJSON), forbidden) {
			t.Fatalf("inspect leaked %q: %s", forbidden, itemsJSON)
		}
	}
	if usage := collector.Usage("team/candidate"); usage.CallCount != 0 {
		t.Fatalf("inspection counted as activation: %#v", usage)
	}
	if usage := collector.InspectionUsage("team/candidate"); usage.CallCount != 1 || usage.LastInspectedAt == "" {
		t.Fatalf("inspection telemetry = %#v", usage)
	}
}

func TestInspectRolePreservesDeclarationGroupsAndPostProcessIsPlain(t *testing.T) {
	calls := 0
	index, err := contexture.Compile(inspectApplication(t, nil, &calls))
	if err != nil {
		t.Fatal(err)
	}
	disclosure, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	api, _ := contexture.NewDisclosureAPI(disclosure)
	payload, err := api.Inspect([]string{"team"}, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	item := payload["items"].([]contexture.CompiledContext)[0]
	members := item["members"].(contexture.CompiledContext)
	roles := members["roles"].([]contexture.CompiledContext)
	if got := []any{roles[0]["ref"], roles[1]["ref"]}; !reflect.DeepEqual(got, []any{"team/candidate", "team/publish"}) {
		t.Fatalf("Role/PostProcess declaration order = %#v", roles)
	}
	if members["skills"].([]contexture.CompiledContext)[0]["ref"] != "team/diagnose" || len(members["tools"].([]contexture.CompiledContext)) != 0 {
		t.Fatalf("member groups = %#v", members)
	}
}

func TestInspectBatchPrevalidationIsAtomicAndOpenEquivalent(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	calls := 0
	index, _ := contexture.Compile(inspectApplication(t, collector, &calls))
	disclosure, _ := contexture.NewDisclosureWithTelemetry(index, contexture.AllRoots(), collector)
	api, _ := contexture.NewDisclosureAPI(disclosure, "team/candidate")
	for _, refs := range [][]string{nil, {}, {" "}, {"team", " team "}, append(make([]string, 32), "team")} {
		if _, err := api.Inspect(refs, contexture.AllRoots()); err == nil {
			t.Fatalf("Inspect(%#v) succeeded", refs)
		}
	}
	for _, refs := range [][]string{{"team", "missing"}, {"team", "team/candidate"}} {
		if _, err := api.Inspect(refs, contexture.AllRoots()); err == nil {
			t.Fatalf("Inspect(%#v) succeeded", refs)
		}
		if usage := collector.InspectionUsage("team"); usage.CallCount != 0 {
			t.Fatalf("failed batch emitted partial telemetry: %#v", usage)
		}
	}
	selected, _ := contexture.OnlySurfaces("team/candidate")
	narrow, _ := contexture.NewDisclosure(index, selected)
	narrowAPI, _ := contexture.NewDisclosureAPI(narrow)
	payload, err := narrowAPI.Inspect([]string{"team/candidate"}, contexture.AllSurfaces())
	if err != nil || payload["items"].([]contexture.CompiledContext)[0]["node"].(contexture.CompiledContext)["ref"] != "team/candidate" {
		t.Fatalf("promoted descendant inspect = %#v, %v", payload, err)
	}
	if _, err := narrowAPI.Inspect([]string{"team"}, contexture.AllSurfaces()); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("hidden ancestor error = %v", err)
	}
}

func sortedKeys(card contexture.CompiledContext) []string {
	keys := make([]string, 0, len(card))
	for key := range card {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
