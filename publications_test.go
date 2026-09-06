package contexture_test

import (
	"context"
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type emptyInput struct{}
type requiredInput struct {
	Value string `json:"value"`
}

func publicationApplication(t *testing.T) (*contexture.Application, *contexture.Disclosure, *contexture.Runtime) {
	t.Helper()
	runbook, err := contexture.NewTool("runbook", "Read the runbook.", true, func(context.Context, emptyInput) (string, error) { return "# Runbook\n", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "publications",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate services.", Instructions: "Inspect first.", Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "diagnose", Description: "Diagnose failures.", Instructions: "Read the runbook."}
			}}, Tools: []contexture.Factory{func() contexture.Node { return runbook }}}
		}},
		PromptRoots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "command", Description: "A person command.", Instructions: "Only on request."}
		}},
		Prompts:   []contexture.PromptDeclaration{{Name: "show-command", Opens: "command", Description: "Show the protected command."}},
		Resources: []contexture.ResourceDeclaration{{Opens: "operations/runbook", URI: "contexture://runbooks/operations", Description: "Read the runbook.", MIMEType: "text/markdown"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return application, disclosure, runtime
}

func TestPublicationsProjectPromptResourceCompletionAndInstructions(t *testing.T) {
	application, disclosure, runtime := publicationApplication(t)
	publications, err := contexture.NewPublications(application, disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	prompts, err := publications.PromptCards(contexture.AllRoots())
	if err != nil || len(prompts) != 2 || prompts[0].Name != "show-command" || prompts[1].Name != "goto" {
		t.Fatalf("PromptCards = %#v, %v", prompts, err)
	}
	resources := publications.ResourceCards()
	if len(resources) != 1 || resources[0].URI != "contexture://runbooks/operations" {
		t.Fatalf("ResourceCards = %#v", resources)
	}
	command, err := publications.Command("show-command", contexture.AllRoots())
	if err != nil || len(command) == 0 {
		t.Fatalf("Command = %q, %v", command, err)
	}
	values, total, err := publications.Complete("operations", contexture.AllRoots(), 100)
	if err != nil || total != 3 || len(values) != 3 {
		t.Fatalf("Complete = %#v, %d, %v", values, total, err)
	}
	value, err := publications.Read(context.Background(), "contexture://runbooks/operations", contexture.AllRoots())
	if err != nil || value != "# Runbook\n" {
		t.Fatalf("Read = %#v, %v", value, err)
	}
	instructions, err := publications.Instructions(contexture.AllRoots())
	if err != nil || len(instructions) == 0 {
		t.Fatalf("Instructions = %q, %v", instructions, err)
	}
	selected, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	prompts, err = publications.PromptCards(selected)
	if err != nil || len(prompts) != 1 || prompts[0].Name != "goto" {
		t.Fatalf("attenuated PromptCards = %#v, %v", prompts, err)
	}
}

func TestPublicationsRejectInvalidResourceTargetsAndDisclosureResources(t *testing.T) {
	for _, target := range []string{"skill", "writing", "arguments"} {
		t.Run(target, func(t *testing.T) {
			var root contexture.Factory
			switch target {
			case "skill":
				root = func() contexture.Node {
					return &contexture.Skill{Name: "target", Description: "Target.", Instructions: "Target."}
				}
			case "writing":
				tool, err := contexture.NewTool("target", "Target.", false, func(context.Context, emptyInput) (string, error) { return "", nil })
				if err != nil {
					t.Fatal(err)
				}
				root = func() contexture.Node { return tool }
			case "arguments":
				tool, err := contexture.NewTool("target", "Target.", true, func(context.Context, requiredInput) (string, error) { return "", nil })
				if err != nil {
					t.Fatal(err)
				}
				root = func() contexture.Node { return tool }
			}
			application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: target, Roots: []contexture.Factory{root}, Resources: []contexture.ResourceDeclaration{{Opens: "target", URI: "contexture://" + target, Description: "Target."}}})
			if err != nil {
				t.Fatal(err)
			}
			index, err := contexture.Compile(application)
			if err != nil {
				t.Fatal(err)
			}
			disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := contexture.NewPublications(application, disclosure, runtime); err == nil {
				t.Fatal("NewPublications accepted invalid Resource")
			}
		})
	}
	application, disclosure, _ := publicationApplication(t)
	if _, err := contexture.NewPublications(application, disclosure, nil); err == nil || errors.Is(err, contexture.ErrInvalidInput) {
		t.Fatalf("disclosure Resources = %v", err)
	}
}
