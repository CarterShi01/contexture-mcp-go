package contexture_test

import (
	"errors"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestSkillDeclarationValidationRunsBeforeACompiledSurfaceExists(t *testing.T) {
	cases := []struct {
		name  string
		skill func() *contexture.Skill
	}{
		{"blank name", func() *contexture.Skill {
			return &contexture.Skill{Name: " ", Description: "Describe.", Instructions: "Do."}
		}},
		{"blank description", func() *contexture.Skill {
			return &contexture.Skill{Name: "check", Description: " ", Instructions: "Do."}
		}},
		{"blank instructions", func() *contexture.Skill {
			return &contexture.Skill{Name: "check", Description: "Describe.", Instructions: " "}
		}},
		{"duplicate uses", func() *contexture.Skill {
			return &contexture.Skill{Name: "check", Description: "Describe.", Instructions: "Do.", Uses: []string{"check", "check"}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "invalid-skill", Roots: []contexture.Factory{func() contexture.Node { return test.skill() }}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := contexture.Compile(application); !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("Compile error = %v, want invalid declaration", err)
			}
		})
	}
}

func TestSkillUsesCycleCompilesAsRouteCardsWithoutRecursiveDisclosure(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "skill-cycle", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Choose a procedure.", Skills: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Inspect first.", Uses: []string{"operations/remediate"}}
			},
			func() contexture.Node {
				return &contexture.Skill{Name: "remediate", Description: "Remediate.", Instructions: "Change after diagnosis.", Uses: []string{"operations/diagnose"}}
			},
		}}
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
	diagnose, err := view.Open("operations/diagnose", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	wantRemediate := map[string]any{"kind": "skill", "name": "remediate", "description": "Remediate.", "ref": "operations/remediate"}
	if diagnose["instructions"] != "Inspect first." || !reflect.DeepEqual(diagnose["uses"], []map[string]any{wantRemediate}) {
		t.Fatalf("diagnose disclosure = %#v", diagnose)
	}
	if _, recursive := diagnose["uses"].([]map[string]any)[0]["instructions"]; recursive {
		t.Fatalf("uses card leaked active Skill detail: %#v", diagnose["uses"])
	}
	remediate, err := view.Open("operations/remediate", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	wantDiagnose := map[string]any{"kind": "skill", "name": "diagnose", "description": "Diagnose.", "ref": "operations/diagnose"}
	if remediate["instructions"] != "Change after diagnosis." || !reflect.DeepEqual(remediate["uses"], []map[string]any{wantDiagnose}) {
		t.Fatalf("remediate disclosure = %#v", remediate)
	}
	role, err := view.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	skills := role["skills"].([]map[string]any)
	if len(skills) != 2 || skills[0]["ref"] != "operations/diagnose" || skills[1]["ref"] != "operations/remediate" {
		t.Fatalf("role skill cards = %#v", role["skills"])
	}
	for _, card := range skills {
		if _, active := card["instructions"]; active {
			t.Fatalf("role leaked active Skill detail: %#v", card)
		}
		if _, dependency := card["uses"]; dependency {
			t.Fatalf("role leaked nested Skill dependencies: %#v", card)
		}
	}
}

func TestCompiledSkillIsImmuneToCallerMutation(t *testing.T) {
	declared := &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Inspect first.", Uses: []string{"operations/remediate"}}
	remediate := &contexture.Skill{Name: "remediate", Description: "Remediate.", Instructions: "Change later."}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "skill-snapshot", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Choose.", Skills: []contexture.Factory{func() contexture.Node { return declared }, func() contexture.Node { return remediate }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	declared.Instructions = "Forged."
	declared.Uses[0] = "operations/diagnose"
	node, err := index.Find("operations/diagnose")
	if err != nil {
		t.Fatal(err)
	}
	skill := node.(*contexture.Skill)
	if skill.Instructions != "Inspect first." || !reflect.DeepEqual(skill.Uses, []string{"operations/remediate"}) {
		t.Fatalf("compiled Skill changed after caller mutation: %#v", skill)
	}
	view, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := view.Open("operations/diagnose", contexture.AllRoots())
	if err != nil || opened["instructions"] != "Inspect first." || opened["uses"].([]map[string]any)[0]["ref"] != "operations/remediate" {
		t.Fatalf("compiled Skill disclosure after mutation = %#v, %v", opened, err)
	}
}
