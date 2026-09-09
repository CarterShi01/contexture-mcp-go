// Command contexture provides native project workflows for the Go binding.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// UsageError is a command-line request error with exit status 2.
type UsageError struct{ Message string }

func (err *UsageError) Error() string { return err.Message }

// Names are all stable project identifiers derived from one new-project argument.
type Names struct {
	ProjectName     string
	ModuleName      string
	RoleName        string
	RoleDescription string
	ResourceScheme  string
}

var nonAlphaNumeric = regexp.MustCompile(`[^a-z0-9]+`)

// AvailableTemplates returns the stable scaffold inventory.
func AvailableTemplates() []string { return []string{"project"} }

// DeriveNames turns a display name into native filesystem and Contexture names.
func DeriveNames(raw string) (Names, error) {
	slug := strings.Trim(nonAlphaNumeric.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-"), "-")
	if slug == "" {
		return Names{}, &UsageError{Message: fmt.Sprintf("%q contains no letters or digits to build a name from.", raw)}
	}
	if slug[0] >= '0' && slug[0] <= '9' {
		return Names{}, &UsageError{Message: fmt.Sprintf("%q starts with a digit; choose a leading letter for its root role.", raw)}
	}
	return Names{ProjectName: slug, ModuleName: slug, RoleName: slug + "-assistant", RoleDescription: "Answer requests about " + strings.ReplaceAll(slug, "-", " ") + ".", ResourceScheme: slug}, nil
}

// ProjectTemplate returns every generated starter file with no unresolved variables.
func ProjectTemplate(names Names) map[string]string {
	return map[string]string{
		".gitignore":            "bin/\n.env\n",
		"go.mod":                "module " + names.ModuleName + "\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.12.0\n",
		"README.md":             "# " + names.ProjectName + "\n\nA Contexture MCP application.\n\n`go run ./cmd/assistant check`\n`go run ./cmd/assistant list`\n`go run ./cmd/assistant inspect`\n`go run ./cmd/assistant call " + names.RoleName + "/ping --input '{\"target\":\"local\"}'`\n`go run ./cmd/assistant serve`\n",
		"cmd/assistant/main.go": "package main\n\nimport (\n  \"context\"\n  \"os\"\n  contexture \"github.com/CarterShi01/contexture-mcp-go\"\n  \"github.com/CarterShi01/contexture-mcp-go/cli\"\n)\n\ntype pingInput struct { Target string `json:\"target\"` }\n\nfunc main() {\n  ping, err := contexture.NewTool(\"ping\", \"Check a target without changing it.\", true, func(_ context.Context, input pingInput) (map[string]any, error) { return map[string]any{\"target\": input.Target, \"healthy\": true}, nil })\n  if err != nil { panic(err) }\n  app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: \"" + names.ProjectName + "\", Roots: []contexture.Factory{func() contexture.Node { return &contexture.Role{Name: \"" + names.RoleName + "\", Description: \"" + names.RoleDescription + "\", Instructions: \"Inspect evidence before stating a result.\", Skills: []contexture.Factory{func() contexture.Node { return &contexture.Skill{Name: \"check-target\", Description: \"Check one target and report the evidence.\", Instructions: \"Call ping, then report its returned facts.\", Uses: []string{\"" + names.RoleName + "/ping\"}} }}, Tools: []contexture.Factory{func() contexture.Node { return ping }}} }}})\n  if err != nil { panic(err) }\n  os.Exit(cli.RunApplication(context.Background(), app, os.Args[1:], os.Stdout, os.Stderr))\n}\n",
	}
}

// NewProject writes one starter directory and refuses to overwrite an existing one.
func NewProject(rawName, destination string) (string, error) {
	return NewProjectFromTemplate(rawName, destination, "project")
}

// NewProjectFromTemplate writes one named starter template.
func NewProjectFromTemplate(rawName, destination, template string) (string, error) {
	if template != "project" {
		return "", &UsageError{Message: fmt.Sprintf("Unknown template %q. Available: %s.", template, strings.Join(AvailableTemplates(), ", "))}
	}
	names, err := DeriveNames(rawName)
	if err != nil {
		return "", err
	}
	if destination == "" {
		destination, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	root := filepath.Join(destination, names.ProjectName)
	if _, err := os.Stat(root); err == nil {
		return "", &UsageError{Message: root + " already exists; refusing to write into it."}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	for relative, content := range ProjectTemplate(names) {
		target := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return "", err
		}
	}
	return root, nil
}
