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
    contexture "github.com/CarterShi01/contexture-mcp-go"
    "github.com/CarterShi01/contexture-mcp-go/server"
    "github.com/CarterShi01/contexture-mcp-go/web"
)

var _ = contexture.NewPrincipal
var _ = server.NewMCPServer
var _ = web.NewRestRouter

func main() {}
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
