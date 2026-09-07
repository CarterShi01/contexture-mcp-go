package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternalModuleConsumerCompilesPublicEntrypoints proves that a separate
// Go module can resolve the declaration facade and both Host adapters. Go
// distributes source modules rather than a tarball; a replace directive is the
// native equivalent of installing the candidate module into an external
// consumer before its first public tag exists.
func TestExternalModuleConsumerCompilesPublicEntrypoints(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporaryRoot := t.TempDir()
	goMod := "module contexture-consumer\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.0.0\n\nreplace github.com/CarterShi01/contexture-mcp-go => " + filepath.ToSlash(repositoryRoot) + "\n"
	main := `package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    contexture "github.com/CarterShi01/contexture-mcp-go"
    "github.com/CarterShi01/contexture-mcp-go/inspection"
    "github.com/CarterShi01/contexture-mcp-go/server"
    "github.com/CarterShi01/contexture-mcp-go/web"
)

var _ = contexture.NewPrincipal
var _ = contexture.NewToolWithSchema[struct{}, bool]
var _ = contexture.Contexture
var _ = contexture.DeclareApplication
var _ = contexture.Version
var _ = contexture.PackageName
var _ = contexture.ReferenceSeparator
var _ = contexture.ErrInvalidDeclaration
var _ = contexture.ErrNodeNotFound
var _ = contexture.ErrRootOutsideSelection
var _ = contexture.NewMemoryTelemetry
var _ = contexture.NewDisclosureWithTelemetry
var _ = contexture.ReportTelemetry
var _ = contexture.NewControllerManager
var _ = contexture.NewControllerManagerWithChannels
var _ = contexture.RegisterRoot
var _ = contexture.NewSelectedGraph
var _ = contexture.WithChannels[struct{}]
var _ contexture.NodeUsage
var _ contexture.ControllerManager
var _ contexture.RootSelectionError
var _ contexture.RootOutsideSelectionError
var _ contexture.WrongDoorError
var _ contexture.NodeRef
var _ contexture.SignpostLevel
var _ contexture.ReferenceCrossing
var _ contexture.Prompt = contexture.Prompt{Opens: "operations", ModelOpen: contexture.ModelReservedForPerson}
var _ contexture.Resource = contexture.Resource{Opens: "operations/status", URI: "contexture://operations/status"}
var _ = inspection.Replay
var _ = server.NewMCPServer
var _ = server.Auth{}
var _ = server.HeaderRootSelector{}
var _ = server.Launch{}
var _ = server.ClaudeCodeConfig
var _ = web.NewRestRouter

func main() {
    if contexture.PackageName != "contexture" || contexture.Version != "0.12.0rc1" || contexture.ReferenceSeparator != "/" {
        panic("public Contexture vocabulary has an unexpected spelling")
    }
    if contexture.DiscoverGatewayName != "contexture_discover" || contexture.OpenGatewayName != "contexture_open" || contexture.InvokeReadOnlyGatewayName != "contexture_invoke_read_only" || contexture.InvokeGatewayName != "contexture_invoke" {
        panic("public fixed gateway vocabulary has an unexpected spelling")
    }
    principal := contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "consumer", Claims: map[string]any{"secret": "do-not-log"}})
    copiedPrincipal := *principal
    for _, receiver := range []any{principal, copiedPrincipal} {
        for _, verb := range []string{"%v", "%+v", "%#v"} {
            if rendered := fmt.Sprintf(verb, receiver); strings.Contains(rendered, "do-not-log") || !strings.Contains(rendered, "consumer") {
                panic("public Principal formatting leaked claims or omitted identity")
            }
        }
    }
    manager := contexture.NewControllerManager()
    _, _ = manager.RegisterRole(func() *contexture.Role {
        return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
    })
    application, _ := manager.Application("consumer")
    index, _ := contexture.Compile(application)
    _, _ = contexture.NewSelectedGraph(index, contexture.AllRoots())
    _ = index.Count()
    _ = index.Has("operations")
    _ = index.NodesWithRefs()
    _ = index.RolesByLevel()
    _, _ = index.MatchingRefs("operations", 10)

    type graphInput struct { Name string ` + "`json:\"name\"`" + ` }
    type strictInput struct { Count int32 ` + "`json:\"count\"`" + ` }
    tool, toolErr := contexture.NewTool("graph", "Read the request graph.", true, func(ctx context.Context, _ graphInput) (bool, error) {
        return contexture.CurrentGraph(ctx) != nil, nil
    })
    if toolErr != nil { panic(toolErr) }
    strict, strictErr := contexture.NewToolWithSchema("strict", "Accept one bounded count.", true, map[string]any{
        "type": "object", "additionalProperties": false,
        "properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}},
        "required": []any{"count"},
    }, func(_ context.Context, input strictInput) (int32, error) { return input.Count, nil })
    if strictErr != nil { panic(strictErr) }
    write, writeErr := contexture.NewTool("write", "Change one value.", false, func(_ context.Context, _ graphInput) (string, error) { return "changed", nil })
    if writeErr != nil { panic(writeErr) }
    graphApplication, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "graph-consumer", Roots: []contexture.Factory{func() contexture.Node {
        return &contexture.Role{Name: "graph", Description: "Graph.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node { return &contexture.Skill{Name: "check", Description: "Check.", Instructions: "Read graph facts first."} }}, Tools: []contexture.Factory{func() contexture.Node { return tool }, func() contexture.Node { return strict }, func() contexture.Node { return write }}}
    }}})
    graphIndex, _ := contexture.Compile(graphApplication)
    skillDisclosure, disclosureErr := contexture.NewDisclosure(graphIndex, contexture.AllRoots())
    if disclosureErr != nil { panic(disclosureErr) }
    checkRef := "graph" + contexture.ReferenceSeparator + "check"
    openedSkill, skillOpenErr := skillDisclosure.Open(checkRef, contexture.AllRoots())
    if skillOpenErr != nil || openedSkill["instructions"] != "Read graph facts first." {
        panic("public Skill did not compile and disclose its active procedure")
    }
    openedRole, roleOpenErr := skillDisclosure.Open("graph", contexture.AllRoots())
    if roleOpenErr != nil { panic(roleOpenErr) }
    skillCards, cardsOK := openedRole["skills"].([]map[string]any)
    if !cardsOK || len(skillCards) != 1 || skillCards[0]["ref"] != checkRef || skillCards[0]["instructions"] != nil {
        panic("public Role did not keep Skill as a route card")
    }
    runtime, _ := contexture.NewRuntime(graphIndex, contexture.AllRoots(), contexture.AllRoots(), nil)
    graphValue, graphErr := runtime.InvokeReadOnly(context.Background(), "graph/graph", json.RawMessage("{\"name\":\"Ada\"}"), contexture.AllRoots())
    if graphErr != nil {
        panic(graphErr)
    }
    if present, ok := graphValue.(bool); !ok || !present {
        panic("CurrentGraph was not available inside the external consumer Tool handler")
    }
    _, rejectedErr := runtime.InvokeReadOnly(context.Background(), "graph/graph", json.RawMessage("{}"), contexture.AllRoots())
    if !errors.Is(rejectedErr, contexture.ErrInvalidInput) {
        panic("public tagged Tool did not reject a missing required input field")
    }
    strictValue, strictCallErr := runtime.InvokeReadOnly(context.Background(), "graph/strict", json.RawMessage("{\"count\":7}"), contexture.AllRoots())
    if strictCallErr != nil || strictValue != int32(7) {
        panic("public NewToolWithSchema did not execute accepted input")
    }
    _, strictRejectedErr := runtime.InvokeReadOnly(context.Background(), "graph/strict", json.RawMessage("{\"count\":\"seven\"}"), contexture.AllRoots())
    if !errors.Is(strictRejectedErr, contexture.ErrInvalidInput) {
        panic("public NewToolWithSchema did not reject schema-invalid input")
    }
    _, wrongDoorErr := runtime.InvokeReadOnly(context.Background(), "graph/write", json.RawMessage("{}"), contexture.AllRoots())
    var wrongDoor *contexture.WrongDoorError
    if !errors.As(wrongDoorErr, &wrongDoor) || wrongDoor.Ref != "graph/write" || wrongDoor.ReadOnly || !errors.Is(wrongDoorErr, contexture.ErrWrongDoor) {
        panic("public WrongDoorError did not retain a direct Runtime error's facts")
    }
}

`
	if err := os.WriteFile(filepath.Join(temporaryRoot, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporaryRoot, "main.go"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, arguments := range [][]string{{"mod", "tidy"}, {"run", "."}} {
		command := exec.Command("go", arguments...)
		command.Dir = temporaryRoot
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("external module consumer %q failed: %v\n%s", arguments, err, output)
		}
	}
}

// TestExternalModuleCanDeclareAnInertApplication proves the small authoring
// import works independently of every Host adapter. It deliberately does not
// import server or web, and the factory's counter makes declaration-time
// evaluation observable to a real downstream module.
func TestExternalModuleCanDeclareAnInertApplication(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporaryRoot := t.TempDir()
	goMod := "module contexture-inert-consumer\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.0.0\n\nreplace github.com/CarterShi01/contexture-mcp-go => " + filepath.ToSlash(repositoryRoot) + "\n"
	main := `package main

import (
    "fmt"
    contexture "github.com/CarterShi01/contexture-mcp-go"
)

func main() {
    built := 0
    application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
        Name: " inert ",
        Roots: []contexture.Factory{func() contexture.Node {
            built++
            return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
        }},
    })
    if err != nil || built != 0 || application.Name() != "inert" || application.RootCount() != 1 {
        panic(fmt.Sprintf("inert declaration = application=%#v built=%d err=%v", application, built, err))
    }
}
`
	if err := os.WriteFile(filepath.Join(temporaryRoot, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temporaryRoot, "main.go"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, arguments := range [][]string{{"mod", "tidy"}, {"run", "."}} {
		command := exec.Command("go", arguments...)
		command.Dir = temporaryRoot
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("inert external consumer %q failed: %v\n%s", arguments, err, output)
		}
	}
	command := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	command.Dir = temporaryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list inert external consumer dependencies: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "net/http" ||
			strings.HasPrefix(dependency, "github.com/CarterShi01/contexture-mcp-go/server") ||
			strings.HasPrefix(dependency, "github.com/CarterShi01/contexture-mcp-go/web") ||
			strings.HasPrefix(dependency, "github.com/modelcontextprotocol/go-sdk") {
			t.Fatalf("inert external consumer transitively imports Host dependency %q", dependency)
		}
	}
}
