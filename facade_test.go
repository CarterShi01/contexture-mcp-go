package contexture_test

import (
	"reflect"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

// TestDeclarationFacadeInventory keeps the SDK-neutral authoring surface
// deliberate. Go exposes typed declarations and error sentinels rather than
// Python exception classes or subclass constructors.
func TestDeclarationFacadeInventory(t *testing.T) {
	types := map[string]reflect.Type{
		"Application":            reflect.TypeFor[contexture.Application](),
		"ApplicationDeclaration": reflect.TypeFor[contexture.ApplicationDeclaration](),
		"Channels":               reflect.TypeFor[contexture.Channels](),
		"Factory":                reflect.TypeFor[contexture.Factory](),
		"Node":                   reflect.TypeFor[contexture.Node](),
		"Principal":              reflect.TypeFor[contexture.Principal](),
		"PrincipalOptions":       reflect.TypeFor[contexture.PrincipalOptions](),
		"Prompt":                 reflect.TypeFor[contexture.Prompt](),
		"PromptDeclaration":      reflect.TypeFor[contexture.PromptDeclaration](),
		"Resource":               reflect.TypeFor[contexture.Resource](),
		"ResourceDeclaration":    reflect.TypeFor[contexture.ResourceDeclaration](),
		"Role":                   reflect.TypeFor[contexture.Role](),
		"Skill":                  reflect.TypeFor[contexture.Skill](),
		"Tool":                   reflect.TypeFor[contexture.Tool](),
	}
	for name, value := range types {
		if value == nil {
			t.Fatalf("declaration facade type %s is unavailable", name)
		}
	}
	values := map[string]any{
		"Contexture":             contexture.Contexture,
		"DeclareApplication":     contexture.DeclareApplication,
		"NewPrincipal":           contexture.NewPrincipal,
		"ErrInvalidDeclaration":  contexture.ErrInvalidDeclaration,
		"ErrInvalidInput":        contexture.ErrInvalidInput,
		"ErrDuplicate":           contexture.ErrDuplicate,
		"ErrContainmentCycle":    contexture.ErrContainmentCycle,
		"ErrUnresolvedReference": contexture.ErrUnresolvedReference,
		"ErrWrongDoor":           contexture.ErrWrongDoor,
		"ErrInvalidSelection":    contexture.ErrInvalidSelection,
		"Version":                contexture.Version,
	}
	for name, value := range values {
		if value == nil {
			t.Fatalf("declaration facade value %s is unavailable", name)
		}
	}
}

func TestDeclarationFacadeIsLazyAndOwnsNativePromptAndResourceFacts(t *testing.T) {
	built := 0
	application, err := contexture.Contexture(contexture.ApplicationDeclaration{
		Name: " operations ",
		Roots: []contexture.Factory{func() contexture.Node {
			built++
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
		}},
		Prompts:   []contexture.Prompt{{Name: "open-operations", Opens: "operations", Description: "Open operations."}},
		Resources: []contexture.Resource{{Name: "runbook", Opens: "operations/status", URI: "contexture://operations/runbook", Description: "Runbook.", MIMEType: "text/markdown"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if built != 0 || application.Name() != "operations" || application.RootCount() != 1 {
		t.Fatalf("lazy declaration = built %d, name %q, roots %d", built, application.Name(), application.RootCount())
	}
	prompts, resources := application.Prompts(), application.Resources()
	if len(prompts) != 1 || len(resources) != 1 || prompts[0].Opens != "operations" || resources[0].URI != "contexture://operations/runbook" {
		t.Fatalf("native publication declarations = %#v %#v", prompts, resources)
	}
	prompts[0].Name, resources[0].Name = "changed", "changed"
	if application.Prompts()[0].Name != "open-operations" || application.Resources()[0].Name != "runbook" {
		t.Fatal("publication declarations were not defensively copied")
	}
}
