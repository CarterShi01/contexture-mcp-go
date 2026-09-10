package contexture_test

import (
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
	"github.com/CarterShi01/contexture-mcp-go/core/mcpinterface"
)

func TestFoundationVocabularyHasOneCanonicalSpelling(t *testing.T) {
	t.Parallel()

	if contexture.PackageName != "contexture" || contexture.Version != "0.14.0rc1" {
		t.Fatalf("root package vocabulary = (%q, %q), want Contexture 0.14 release facts", contexture.PackageName, contexture.Version)
	}
	if contexture.ReferenceSeparator != foundation.ReferenceSeparator {
		t.Fatalf("ReferenceSeparator = %q, want foundation value %q", contexture.ReferenceSeparator, foundation.ReferenceSeparator)
	}
	if foundation.ReferenceSeparator != "/" {
		t.Fatalf("foundation ReferenceSeparator = %q, want slash", foundation.ReferenceSeparator)
	}
	want := []foundation.GatewayName{
		foundation.DiscoverGatewayName,
		foundation.OpenGatewayName,
		foundation.InvokeReadOnlyGatewayName,
		foundation.InvokeGatewayName,
	}
	got := []contexture.GatewayName{
		contexture.DiscoverGatewayName,
		contexture.OpenGatewayName,
		contexture.InvokeReadOnlyGatewayName,
		contexture.InvokeGatewayName,
	}
	for position := range want {
		if got[position] != want[position] {
			t.Fatalf("root gateway name %d = %q, want foundation %q", position, got[position], want[position])
		}
	}
	if mcpinterface.OpenGatewayName != foundation.OpenGatewayName {
		t.Fatalf("MCP primitive spelling = %q, want foundation %q", mcpinterface.OpenGatewayName, foundation.OpenGatewayName)
	}
	prompt := mcpinterface.PromptDeclaration{Opens: "operations", Description: "Operate.", ModelOpen: mcpinterface.ModelReservedForPerson}
	if prompt.AllowsModelOpen() || foundation.PromptDeclaration(prompt).ModelOpen != foundation.ModelReservedForPerson {
		t.Fatalf("MCP prompt alias lost the shared model-open policy: %#v", prompt)
	}
	resource := mcpinterface.ResourceDeclaration{Opens: "operations/status", URI: "contexture://operations/status", Description: "Status."}
	if foundation.ResourceDeclaration(resource).URI != "contexture://operations/status" {
		t.Fatalf("MCP resource alias lost shared declaration facts: %#v", resource)
	}
}

func TestReferenceSeparatorDrivesCoreReferenceParsing(t *testing.T) {
	t.Parallel()

	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "separator",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{
				Name: "operations", Description: "Operate.", Instructions: "Inspect.",
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read."}
				}},
			}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	ref := "operations" + contexture.ReferenceSeparator + "diagnose"
	if node, err := index.Find(contexture.ReferenceSeparator + ref + contexture.ReferenceSeparator); err != nil || node.NodeName() != "diagnose" {
		t.Fatalf("Find normalized public separator ref = %#v, %v", node, err)
	}
	selection, err := contexture.OnlyRoots(ref)
	if err != nil {
		t.Fatalf("OnlyRoots rejected descendant spelled with ReferenceSeparator: %v", err)
	}
	if got := selection.Selectors(); len(got) != 1 || got[0] != ref {
		t.Fatalf("OnlyRoots selectors = %#v, want [%q]", got, ref)
	}
}
