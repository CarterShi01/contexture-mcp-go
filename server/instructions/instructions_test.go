package instructions_test

import (
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

func disclosure(t *testing.T) *contexture.Disclosure {
	t.Helper()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "instructions", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate services.", Instructions: "Inspect first.", Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "incidents", Description: "Diagnose incidents.", Instructions: "Read evidence."}
			}}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "security", Description: "Handle security work.", Instructions: "Verify identity."}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	view, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestBuildPlacesSelfContainedContractBeforeBreadthFirstRoster(t *testing.T) {
	text, err := instructions.Build(disclosure(t), contexture.AllRoots(), instructions.RosterBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "Everything this server offers is behind contexture_open.") {
		t.Fatalf("missing contract prefix: %q", text)
	}
	if strings.Index(text, "- operations: Operate services.") > strings.Index(text, "- operations/incidents:") || strings.Index(text, "- security: Handle security work.") > strings.Index(text, "- operations/incidents:") {
		t.Fatalf("roster is not breadth first: %q", text)
	}
	if !strings.Contains(text, "Every card carries a `ref`.") || len([]byte(text)) > instructions.InstructionsLimit {
		t.Fatalf("invalid bootstrap instructions: %q", text)
	}
}

func TestBuildCutsRootsAndExposesNeutralRequestSelectedText(t *testing.T) {
	text, err := instructions.Build(disclosure(t), contexture.AllRoots(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "more root role(s); call contexture_discover") {
		t.Fatalf("root truncation was not explicit: %q", text)
	}
	if !strings.Contains(instructions.Neutral(), "request-specific set of complete root capabilities") {
		t.Fatal("neutral instructions lost their request-selection contract")
	}
}
