package contexture_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func disclosureAPIFixture(t *testing.T, selection contexture.RootSelection, reserved ...string) (*contexture.DisclosureAPI, *contexture.Disclosure) {
	t.Helper()
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "healthy", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "disclosure-api",
		Roots: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read first.", Uses: []string{"operations/status"}}
				}}, Tools: []contexture.Factory{func() contexture.Node { return status }}}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "other", Description: "Other.", Instructions: "Separate."}
			},
		},
		PromptRoots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "command", Description: "Person command.", Instructions: "Ask the person."}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	view, err := contexture.NewDisclosure(index, selection)
	if err != nil {
		t.Fatal(err)
	}
	api, err := contexture.NewDisclosureAPI(view, reserved...)
	if err != nil {
		t.Fatal(err)
	}
	return api, view
}

func TestDisclosureAPIProjectsStatelessCardsAndSelectedGraph(t *testing.T) {
	operations, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	api, _ := disclosureAPIFixture(t, operations)
	tools := api.Tools()
	if len(tools) != 3 || tools[0].Name != contexture.DiscoverGatewayName || tools[1].Name != contexture.InspectGatewayName || tools[2].Name != contexture.OpenGatewayName || !tools[0].ReadOnly || !tools[1].ReadOnly || !tools[2].ReadOnly {
		t.Fatalf("disclosure tools = %#v", tools)
	}
	tools[0].Name = "changed"
	if api.Tools()[0].Name != contexture.DiscoverGatewayName {
		t.Fatal("DisclosureAPI tool inventory was mutable")
	}
	discovered, err := api.Discover(contexture.AllRoots())
	if err != nil || len(discovered["roles"]) != 1 || discovered["roles"][0]["ref"] != "operations" || len(discovered["skills"]) != 0 || len(discovered["tools"]) != 0 {
		t.Fatalf("Discover = %#v, %v", discovered, err)
	}
	opened, err := api.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if opened["instructions"] != "Inspect." || len(opened["skills"].([]map[string]any)) != 1 || len(opened["tools"].([]map[string]any)) != 1 {
		t.Fatalf("Open operations = %#v", opened)
	}
	toolCard := opened["tools"].([]map[string]any)[0]
	if toolCard["ref"] != "operations/status" || toolCard["read_only"] != true || toolCard["input_schema"] == nil {
		t.Fatalf("Open tool card = %#v", toolCard)
	}
	opened["instructions"] = "changed"
	again, err := api.Open("operations", contexture.AllRoots())
	if err != nil || again["instructions"] != "Inspect." {
		t.Fatalf("stateless Open = %#v, %v", again, err)
	}
	graph, err := api.SelectedGraph(contexture.AllRoots())
	if err != nil || len(graph.Walk()) != 3 || graph.Index() != api.Index() {
		t.Fatalf("SelectedGraph = %#v, %v", graph, err)
	}
	if _, err := graph.Find("other"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("SelectedGraph leaked excluded root: %v", err)
	}
}

func TestDisclosureAPISeparatesModelAndPersonDoorsAndLookupBoundary(t *testing.T) {
	api, raw := disclosureAPIFixture(t, contexture.AllRoots(), "operations/diagnose")
	_, err := api.Open("operations/diagnose", contexture.AllRoots())
	var refusal *contexture.RefusedError
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Error(), "opened by a person") {
		t.Fatalf("reserved model open = %#v", err)
	}
	person, err := api.OpenForPerson("operations/diagnose", contexture.AllRoots())
	if err != nil || person["instructions"] != "Read first." {
		t.Fatalf("person open = %#v, %v", person, err)
	}
	person, err = api.OpenForAPerson("command", contexture.AllRoots())
	if err != nil || person["instructions"] != "Ask the person." {
		t.Fatalf("compatibility person open = %#v, %v", person, err)
	}
	_, err = api.Open("command", contexture.AllRoots())
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Error(), "opened by a person") {
		t.Fatalf("prompt-root model open = %#v", err)
	}
	// Raw Disclosure preserves lookup facts for a Host. The public API is the
	// agent-facing boundary and gives the same error a completed next action.
	_, rawErr := raw.Open("operations/missing", contexture.AllRoots())
	var rawMissing *contexture.NodeNotFoundError
	if !errors.As(rawErr, &rawMissing) || !errors.Is(rawErr, contexture.ErrNodeNotFound) {
		t.Fatalf("raw Disclosure missing = %#v", rawErr)
	}
	_, err = api.Open("operations/missing", contexture.AllRoots())
	var missing *contexture.NodeNotFoundError
	if !errors.As(err, &refusal) || !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) || !strings.Contains(refusal.Error(), string(contexture.OpenGatewayName)) {
		t.Fatalf("DisclosureAPI missing = %#v", err)
	}
}

func TestDisclosureAPIKeepsRootCeilingAheadOfPersonPolicy(t *testing.T) {
	operations, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	api, _ := disclosureAPIFixture(t, operations, "command")
	_, err = api.Open("command", contexture.AllRoots())
	var outside *contexture.RootOutsideSelectionError
	var refusal *contexture.RefusedError
	if !errors.As(err, &outside) || errors.As(err, &refusal) || strings.Contains(err.Error(), "person") {
		t.Fatalf("excluded person target = %#v", err)
	}
	_, err = api.OpenForPerson("command", contexture.AllRoots())
	if !errors.As(err, &outside) {
		t.Fatalf("person door widened selection = %#v", err)
	}
	if _, err := contexture.NewDisclosureAPI(nil); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("NewDisclosureAPI(nil) = %v", err)
	}
}

func TestDisclosureAPISupportsUnboundStructuralProjection(t *testing.T) {
	structural := &contexture.Tool{Name: "fact", Description: "Structural fact.", ReadOnly: true}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "disclosure-api-unbound", Roots: []contexture.Factory{func() contexture.Node { return structural }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	view, err := contexture.NewDisclosureOnly(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	api, err := contexture.NewDisclosureAPI(view)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := api.Open("fact", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["read_only"]; exists {
		t.Fatalf("unbound DisclosureAPI leaked execution facts: %#v", payload)
	}
}
