package contexture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRootPackageDoesNotImportHostSDKs(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob root Go files: %v", err)
	}
	for _, file := range files {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		for _, imported := range parsed.Imports {
			path, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", file, unquoteErr)
			}
			if strings.HasPrefix(path, "github.com/modelcontextprotocol/") || path == "net/http" {
				t.Errorf("SDK-neutral package imports Host SDK in %s: %s", file, path)
			}
		}
	}
}

func TestCorePackagesDoNotImportHostSDKs(t *testing.T) {
	t.Parallel()

	for _, directory := range []string{"core/foundation", "core/mcpinterface", "core/model"} {
		err := filepath.WalkDir(directory, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
				return nil
			}
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			for _, imported := range parsed.Imports {
				importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
				if unquoteErr != nil {
					return unquoteErr
				}
				if strings.HasPrefix(importPath, "github.com/modelcontextprotocol/") || importPath == "net/http" {
					t.Errorf("SDK-neutral core imports Host SDK in %s: %s", file, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", directory, err)
		}
	}
}

// Foundation is the shared floor for model and MCP primitive declarations. It
// cannot import either sibling (or a Host layer) without recreating the
// dependency cycle this vocabulary slice removes.
func TestFoundationDoesNotImportHigherLayers(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("core/foundation/*.go")
	if err != nil {
		t.Fatalf("glob foundation files: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", file, unquoteErr)
			}
			if strings.Contains(importPath, "/core/model") || strings.Contains(importPath, "/core/mcpinterface") || strings.Contains(importPath, "/server") || strings.Contains(importPath, "/web") {
				t.Fatalf("foundation imports a higher layer in %s: %s", file, importPath)
			}
		}
	}
}

func TestMCPInterfaceDoesNotImportModelOrHostLayers(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("core/mcpinterface/*.go")
	if err != nil {
		t.Fatalf("glob MCP interface files: %v", err)
	}
	for _, file := range files {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", file, unquoteErr)
			}
			if strings.Contains(importPath, "/core/model") || strings.Contains(importPath, "/server") || strings.Contains(importPath, "/web") {
				t.Errorf("MCP interface imports a higher layer in %s: %s", file, importPath)
			}
		}
	}
}

// Model owns declarations and reference semantics, while mcpinterface owns
// only its SDK-free primitive projection. Shared declaration vocabulary must
// live below both layers, so no model file may import mcpinterface.
func TestModelDoesNotImportMCPInterface(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("core/model/*.go")
	if err != nil {
		t.Fatalf("glob model files: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", file, unquoteErr)
			}
			if importPath == "github.com/CarterShi01/contexture-mcp-go/core/mcpinterface" {
				t.Fatalf("model imports MCP primitive vocabulary in %s", file)
			}
		}
	}
}

func TestRootFacadeDoesNotImportHostLayers(t *testing.T) {
	t.Parallel()

	parsed, err := parser.ParseFile(token.NewFileSet(), "facade.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse facade: %v", err)
	}
	for _, imported := range parsed.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("unquote facade import: %v", unquoteErr)
		}
		if strings.Contains(importPath, "/server") || strings.Contains(importPath, "/web") || path.Base(importPath) == "mcp" {
			t.Errorf("declaration facade imports Host layer: %s", importPath)
		}
	}
}

func TestServerDoesNotImportWebAdapter(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("server/*.go")
	if err != nil {
		t.Fatalf("glob server Go files: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import in %s: %v", file, unquoteErr)
			}
			if strings.Contains(importPath, "/web") {
				t.Errorf("MCP server imports independent web adapter in %s: %s", file, importPath)
			}
		}
	}
}
