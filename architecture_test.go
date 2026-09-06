package contexture_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRootPackageDoesNotImportMCPSDK(t *testing.T) {
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
			if strings.HasPrefix(path, "github.com/modelcontextprotocol/") {
				t.Errorf("SDK-neutral package imports MCP SDK in %s: %s", file, path)
			}
		}
	}
}
