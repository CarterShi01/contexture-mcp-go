package graph

import "testing"

func TestResolvedDiffExactGroupsStableOrderAndCopies(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) {
		evNode(w, "left")["name"] = "renamed"
		evNode(w, "hub")["members"].(map[string]any)["children"] = []any{"right", "left"}
		evNode(w, "hub")["uses"] = []any{"left"}
		w["application_name"] = "new-app"
		w["presentations"] = pureMap{"default_locale": "fr", "catalogs": []any{pureMap{"locale": "fr", "entries": []any{}}}}
	})
	d, err := Diff(&s.definition, &c.candidate)
	if err != nil {
		t.Fatal(err)
	}
	wire := d.AsJSON().(map[string]any)
	if len(wire) != 5 || len(arr(wire["nodes"])) != 2 || len(arr(wire["membership"])) != 1 || len(arr(wire["uses"])) != 1 || len(arr(wire["locales"])) != 1 || d.Empty() {
		t.Fatal("incorrect five-group diff", d.AsJSON())
	}
	nodes := arr(wire["nodes"])
	if nodes[0].(map[string]any)["id"] != "left" || nodes[1].(map[string]any)["id"] != "native" {
		t.Fatal("unstable ID sort")
	}
	nodes[0].(map[string]any)["id"] = "mutated"
	if arr(d.AsJSON().(map[string]any)["nodes"])[0].(map[string]any)["id"] != "left" {
		t.Fatal("diff aliases")
	}
	for i := 0; i < 30; i++ {
		again, err := Diff(&s.definition, &c.candidate)
		if err != nil {
			t.Fatal(err)
		}
		evEqual(t, again.AsJSON(), d.AsJSON())
	}
	if _, err := NewDefinitionDiff(pureMap{}); err == nil {
		t.Fatal("missing groups accepted")
	}
	other := s.definition.AsJSON().(map[string]any)
	other["graph_id"] = "other"
	g, err := NewGraphDefinition(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(&s.definition, g); err == nil {
		t.Fatal("different graph aligned")
	}
}
func TestResolvedDiffAbsencePublicationOrderAndProjectionEvidence(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) {
		evNode(w, "left")["instruction_template"] = pureMap{"parts": []any{pureMap{"kind": "Text", "text": "new"}}}
		delete(evNode(w, "left"), "instructions")
	})
	d, err := Diff(&s.definition, &c.candidate)
	if err != nil {
		t.Fatal(err)
	}
	entries := arr(d.AsJSON().(map[string]any)["nodes"])
	if len(entries) != 2 {
		t.Fatal("absence collapsed")
	}
	for _, raw := range entries {
		e := raw.(map[string]any)
		before, after := e["before"].(map[string]any), e["after"].(map[string]any)
		if before["present"] == after["present"] {
			t.Fatal("missing optional transition")
		}
	}
	for _, project := range []func(any, any) (FrozenJSON, error){BehaviorDiff, SurfaceDiff, ExecutionDiff} {
		input := pureMap{"nested": []any{pureMap{"value": "a"}}}
		result, err := project(input, input)
		if err != nil {
			t.Fatal(err)
		}
		input["nested"] = nil
		m := result.JSON().(map[string]any)
		if m["equal"] != true || m["before"].(map[string]any)["nested"] == nil {
			t.Fatal("projection aliases")
		}
		if _, err := project("graph text", pureMap{}); err == nil {
			t.Fatal("text inferred as behavior")
		}
	}
}
