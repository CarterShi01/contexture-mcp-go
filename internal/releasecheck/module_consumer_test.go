package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
    contexture "github.com/CarterShi01/contexture-mcp-go"
    "github.com/CarterShi01/contexture-mcp-go/inspection"
    "github.com/CarterShi01/contexture-mcp-go/server"
    "github.com/CarterShi01/contexture-mcp-go/web"
)

var _ = contexture.NewPrincipal
var _ = contexture.Contexture
var _ = contexture.DeclareApplication
var _ = contexture.Version
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

    tool, _ := contexture.NewTool("graph", "Read the request graph.", true, func(ctx context.Context, _ struct{}) (bool, error) {
        return contexture.CurrentGraph(ctx) != nil, nil
    })
    graphApplication, _ := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "graph-consumer", Roots: []contexture.Factory{func() contexture.Node {
        return &contexture.Role{Name: "graph", Description: "Graph.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
    }}})
    graphIndex, _ := contexture.Compile(graphApplication)
    runtime, _ := contexture.NewRuntime(graphIndex, contexture.AllRoots(), contexture.AllRoots(), nil)
    graphValue, graphErr := runtime.InvokeReadOnly(context.Background(), "graph/graph", json.RawMessage("{}"), contexture.AllRoots())
    if graphErr != nil {
        panic(graphErr)
    }
    if present, ok := graphValue.(bool); !ok || !present {
        panic("CurrentGraph was not available inside the external consumer Tool handler")
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
