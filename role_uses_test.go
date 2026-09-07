package contexture_test

import (
	"context"
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestRoleDisclosureProjectsUsesInOrderWithoutWideningSelection(t *testing.T) {
	status, err := contexture.NewTool("status", "Read status.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	plan, err := contexture.NewTool("plan", "Read plan.", true, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "role-uses", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Coordinate.", Instructions: "Use the stated evidence.", Uses: []string{"beta/status", "gamma/plan"}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Status.", Instructions: "Read status.", Tools: []contexture.Factory{func() contexture.Node { return status }}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "gamma", Description: "Plan.", Instructions: "Read plan.", Tools: []contexture.Factory{func() contexture.Node { return plan }}}
		},
	}})
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
	opened, err := view.Open("alpha", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	uses, ok := opened["uses"].([]map[string]any)
	if !ok {
		t.Fatalf("role uses = %#v", opened["uses"])
	}
	refs := make([]string, 0, len(uses))
	for _, card := range uses {
		refs = append(refs, card["ref"].(string))
		if _, hasSchema := card["input_schema"]; !hasSchema {
			t.Fatalf("bound Tool route card lost its schema: %#v", card)
		}
	}
	if !reflect.DeepEqual(refs, []string{"beta/status", "gamma/plan"}) {
		t.Fatalf("role uses order = %#v", refs)
	}

	alphaOnly, err := contexture.OnlyRoots("alpha")
	if err != nil {
		t.Fatal(err)
	}
	attenuated, err := view.Open("alpha", alphaOnly)
	if err != nil {
		t.Fatal(err)
	}
	if uses, ok := attenuated["uses"].([]map[string]any); !ok || len(uses) != 0 {
		t.Fatalf("cross-root role uses leaked through selection: %#v", attenuated["uses"])
	}
}
