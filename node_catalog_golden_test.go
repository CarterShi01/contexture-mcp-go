package contexture_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/introspection"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestStaticNodeCatalogNativeReferenceDemoGoldenAndSchema(t *testing.T) {
	// Reuse the existing native declaration helper and its authored Role
	// trailing newlines, not values loaded out of the Catalog golden.
	application := goldenApplication(t, "contexture-demo")
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := compiled.Catalog(introspection.CatalogOptions{
		View: introspection.ModelView, Detail: introspection.DefinitionDetail,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("conformance", "node-catalog", "golden", "node-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := introspection.SnapshotFromJSON(string(source))
	if err != nil {
		t.Fatal(err)
	}
	got, err := introspection.FormatJSON(catalog.Snapshot().AsJSON(), introspection.JSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := introspection.FormatJSON(expected.AsJSON(), introspection.JSONOptions{})
	if err != nil || got != want {
		t.Fatalf("native Catalog golden mismatch: %v\ngot %s\nwant %s", err, got, want)
	}
	if !strings.HasSuffix(catalog.Snapshot().Nodes()[0].Role.AuthoredInstructions, "\n") {
		t.Fatal("authored newline lost")
	}
	rawSchema, err := os.ReadFile(filepath.Join("conformance", "node-catalog", "node-catalog.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal([]byte(got), &document); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(document); err != nil {
		t.Fatal(err)
	}
}

func TestStaticNodeCatalogFixtureSelectsMatchingNativeDemoInput(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("conformance", "node-catalog", "fixtures", "reference-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		ApplicationFixture string `json:"application_fixture"`
	}
	if err := json.Unmarshal(fixture, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.ApplicationFixture != "reference-demo-application.json" {
		t.Fatal("Catalog fixture must select the reviewed native demo input")
	}
	source, err := os.ReadFile(filepath.Join("conformance", "node-catalog", "fixtures", metadata.ApplicationFixture))
	if err != nil {
		t.Fatal(err)
	}
	var declaration struct {
		Application struct {
			Name  string   `json:"name"`
			Roots []string `json:"roots"`
		} `json:"application"`
		Roles  []json.RawMessage `json:"roles"`
		Skills []json.RawMessage `json:"skills"`
		Tools  []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(source, &declaration); err != nil {
		t.Fatal(err)
	}
	if declaration.Application.Name != "contexture-demo" ||
		len(declaration.Application.Roots) != 1 ||
		declaration.Application.Roots[0] != "kubernetes-platform" ||
		len(declaration.Roles) != 3 || len(declaration.Skills) != 2 || len(declaration.Tools) != 7 {
		t.Fatal("selected author input does not match the native demo declaration")
	}
	operations, err := os.ReadFile(filepath.Join("conformance", "fixtures", "reference-application.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(operations), `"operations"`) {
		t.Fatal("the separate operations prototype must remain unchanged")
	}
}
