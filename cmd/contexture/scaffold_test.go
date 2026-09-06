package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
