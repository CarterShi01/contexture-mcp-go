package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsVersionAndCreatesProject(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if status := run([]string{"--version"}, stdout, stderr); status != 0 || stdout.String() != cliVersion+"\n" {
		t.Fatalf("version = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"new", "My Context", "--into", t.TempDir()}, stdout, stderr); status != 0 {
		t.Fatalf("new = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Wrote ") {
		t.Fatalf("new output = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"new"}, stdout, stderr); status != 2 || !strings.HasPrefix(stderr.String(), "contexture: ") {
		t.Fatalf("usage = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
}

func TestDeriveNames(t *testing.T) {
	names, err := DeriveNames("My Context")
	if err != nil {
		t.Fatal(err)
	}
	if names.ProjectName != "my-context" || names.RoleName != "my-context-assistant" {
		t.Fatalf("unexpected names: %#v", names)
	}
	if _, err := DeriveNames("9lives"); err == nil {
		t.Fatal("leading digit was accepted")
	}
	if _, err := DeriveNames("..."); err == nil {
		t.Fatal("empty name was accepted")
	}
}

func TestScaffoldTemplateInventoryAndUnknownTemplate(t *testing.T) {
	if got := AvailableTemplates(); len(got) != 1 || got[0] != "project" {
		t.Fatalf("AvailableTemplates = %#v", got)
	}
	if _, err := NewProjectFromTemplate("Example", t.TempDir(), "missing"); err == nil || !strings.Contains(err.Error(), "Available: project") {
		t.Fatalf("unknown template error = %v", err)
	}
}

func TestNewProjectWritesStarterAndRefusesOverwrite(t *testing.T) {
	root, err := NewProject("My Context", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "assistant", "main.go")); err != nil {
		t.Fatal(err)
	}
	for _, content := range ProjectTemplate(Names{ProjectName: "my-context", ModuleName: "my-context", RoleName: "my-context-assistant", RoleDescription: "Answer requests about my context.", ResourceScheme: "my-context"}) {
		if strings.Contains(content, "$") {
			t.Fatalf("template has unresolved variable: %q", content)
		}
	}
	if _, err := NewProject("My Context", filepath.Dir(root)); err == nil {
		t.Fatal("existing project was overwritten")
	}
}

func TestGeneratedProjectRunsItsNativeLocalWorkflow(t *testing.T) {
	root, err := NewProject("Generated Context", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), append(module, []byte("\nreplace github.com/CarterShi01/contexture-mcp-go => "+repository+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	tidy := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidy.Dir = root
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("generated go mod tidy failed: %v\n%s", err, output)
	}
	run := func(arguments ...string) string {
		command := exec.CommandContext(t.Context(), "go", append([]string{"run", "./cmd/assistant"}, arguments...)...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("generated %s failed: %v\n%s", strings.Join(arguments, " "), err, output)
		}
		return string(output)
	}
	if output := run("check"); !strings.Contains(output, "OK generated-context: 1 role(s), 1 skill(s), 1 tool(s)") {
		t.Fatalf("generated check output = %q", output)
	}
	if output := run("list"); !strings.Contains(output, "generated-context-assistant") {
		t.Fatalf("generated list output = %q", output)
	}
	if output := run("inspect", "--all", "--json"); !strings.Contains(output, "contexture_discover") {
		t.Fatalf("generated inspect output = %q", output)
	}
	if output := run("call", "generated-context-assistant/ping", "--input", `{"target":"local"}`); !strings.Contains(output, `"healthy":true`) {
		t.Fatalf("generated call output = %q", output)
	}
}

func TestGlobalCommandRunsGeneratedProjectFromNestedDirectory(t *testing.T) {
	root, err := NewProject("Forwarded Context", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), append(module, []byte("\nreplace github.com/CarterShi01/contexture-mcp-go => "+repository+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	tidy := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	tidy.Dir = root
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("generated go mod tidy failed: %v\n%s", err, output)
	}
	binary := filepath.Join(t.TempDir(), "contexture")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	build.Dir = workingDirectory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building contexture command failed: %v\n%s", err, output)
	}
	nested := filepath.Join(root, "internal", "notes")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), binary, "check")
	command.Dir = nested
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("forwarded check failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "OK forwarded-context: 1 role(s), 1 skill(s), 1 tool(s)") {
		t.Fatalf("forwarded check output = %q", output)
	}
}
