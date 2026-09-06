package contexture_test

import (
	"errors"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
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
		"ControllerManager":      reflect.TypeFor[contexture.ControllerManager](),
		"Factory":                reflect.TypeFor[contexture.Factory](),
		"RoleFactory":            reflect.TypeFor[contexture.RoleFactory](),
		"SkillFactory":           reflect.TypeFor[contexture.SkillFactory](),
		"ToolFactory":            reflect.TypeFor[contexture.ToolFactory](),
		"Node":                   reflect.TypeFor[contexture.Node](),
		"Principal":              reflect.TypeFor[contexture.Principal](),
		"PrincipalOptions":       reflect.TypeFor[contexture.PrincipalOptions](),
		"ModelOpenPolicy":        reflect.TypeFor[contexture.ModelOpenPolicy](),
		"NodeNotFoundError":      reflect.TypeFor[contexture.NodeNotFoundError](),
		"NodeUsage":              reflect.TypeFor[contexture.NodeUsage](),
		"Telemetry":              reflect.TypeFor[contexture.Telemetry](),
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
		"Contexture":                       contexture.Contexture,
		"DeclareApplication":               contexture.DeclareApplication,
		"NewPrincipal":                     contexture.NewPrincipal,
		"ErrInvalidDeclaration":            contexture.ErrInvalidDeclaration,
		"ErrInvalidInput":                  contexture.ErrInvalidInput,
		"ErrDuplicate":                     contexture.ErrDuplicate,
		"ErrContainmentCycle":              contexture.ErrContainmentCycle,
		"ErrUnresolvedReference":           contexture.ErrUnresolvedReference,
		"ErrWrongDoor":                     contexture.ErrWrongDoor,
		"ErrInvalidSelection":              contexture.ErrInvalidSelection,
		"ErrNodeNotFound":                  contexture.ErrNodeNotFound,
		"ModelMayOpen":                     contexture.ModelMayOpen,
		"ModelReservedForPerson":           contexture.ModelReservedForPerson,
		"Version":                          contexture.Version,
		"NewMemoryTelemetry":               contexture.NewMemoryTelemetry,
		"NewDisclosureWithTelemetry":       contexture.NewDisclosureWithTelemetry,
		"ReportTelemetry":                  contexture.ReportTelemetry,
		"NewControllerManager":             contexture.NewControllerManager,
		"NewControllerManagerWithChannels": contexture.NewControllerManagerWithChannels,
		"RegisterRoot":                     contexture.RegisterRoot,
	}
	for name, value := range values {
		if value == nil {
			t.Fatalf("declaration facade value %s is unavailable", name)
		}
	}
}

func TestDeclarationFacadeRejectsInvalidPublicationFactsEarly(t *testing.T) {
	root := func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
	}
	for _, declaration := range []contexture.ApplicationDeclaration{
		{Name: "invalid-prompt-opens", Roots: []contexture.Factory{root}, Prompts: []contexture.Prompt{{Opens: " ", Description: "Open."}}},
		{Name: "invalid-prompt-description", Roots: []contexture.Factory{root}, Prompts: []contexture.Prompt{{Opens: "operations", Description: " "}}},
		{Name: "invalid-prompt-name", Roots: []contexture.Factory{root}, Prompts: []contexture.Prompt{{Name: " ", Opens: "operations", Description: "Open."}}},
		{Name: "invalid-prompt-policy", Roots: []contexture.Factory{root}, Prompts: []contexture.Prompt{{Opens: "operations", Description: "Open.", ModelOpen: 99}}},
		{Name: "invalid-resource-opens", Roots: []contexture.Factory{root}, Resources: []contexture.Resource{{Opens: " ", URI: "contexture://runbook", Description: "Read."}}},
		{Name: "invalid-resource-uri", Roots: []contexture.Factory{root}, Resources: []contexture.Resource{{Opens: "operations", URI: " ", Description: "Read."}}},
		{Name: "invalid-resource-description", Roots: []contexture.Factory{root}, Resources: []contexture.Resource{{Opens: "operations", URI: "contexture://runbook", Description: " "}}},
		{Name: "invalid-resource-name", Roots: []contexture.Factory{root}, Resources: []contexture.Resource{{Name: " ", Opens: "operations", URI: "contexture://runbook", Description: "Read."}}},
	} {
		if _, err := contexture.DeclareApplication(declaration); !errors.Is(err, contexture.ErrInvalidDeclaration) {
			t.Fatalf("invalid publication declaration %q = %v", declaration.Name, err)
		}
	}
}

func TestNodeNotFoundErrorCarriesClassifiableLookupFacts(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "lookups", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read."}
		}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		ref    string
		reason contexture.LookupFailure
	}{
		{ref: "", reason: contexture.EmptyRef},
		{ref: "missing", reason: contexture.NoSuchRoot},
		{ref: "operations/unknown", reason: contexture.NoSuchMember},
		{ref: "operations/diagnose/deeper", reason: contexture.NotAContainer},
	} {
		_, err := index.Find(expected.ref)
		var failure *contexture.NodeNotFoundError
		if !errors.Is(err, contexture.ErrNodeNotFound) || !errors.As(err, &failure) || failure.Ref != expected.ref || failure.Reason != expected.reason {
			t.Fatalf("Find(%q) = %#v, want reason %q", expected.ref, err, expected.reason)
		}
	}
	_, err = index.Tool("operations/diagnose")
	var wrongKind *contexture.NodeNotFoundError
	if !errors.Is(err, contexture.ErrNodeNotFound) || !errors.As(err, &wrongKind) || wrongKind.Reason != contexture.WrongKind || wrongKind.Kind != "skill" || wrongKind.Wanted != "tool" {
		t.Fatalf("Tool wrong kind = %#v", err)
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
	if len(prompts) != 1 || len(resources) != 1 || !prompts[0].AllowsModelOpen() || prompts[0].Opens != "operations" || resources[0].URI != "contexture://operations/runbook" {
		t.Fatalf("native publication declarations = %#v %#v", prompts, resources)
	}
	prompts[0].Name, resources[0].Name = "changed", "changed"
	if application.Prompts()[0].Name != "open-operations" || application.Resources()[0].Name != "runbook" {
		t.Fatal("publication declarations were not defensively copied")
	}
}

func TestDeclarationFacadeDoesNotImportHostLayers(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "facade.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(path, "/server") || strings.Contains(path, "/web") || strings.Contains(path, "modelcontextprotocol") || path == "net/http" {
			t.Fatalf("declaration facade imports Host layer %q", path)
		}
	}
}
