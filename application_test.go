package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestSpecificationIdentity(t *testing.T) {
	t.Parallel()

	if contexture.SpecificationVersion != "0.14" {
		t.Fatalf("SpecificationVersion = %q, want 0.14", contexture.SpecificationVersion)
	}
}

func TestSpecificationIdentityMatchesConformanceLock(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("conformance/specification.json")
	if err != nil {
		t.Fatalf("read conformance lock: %v", err)
	}
	var lock struct {
		Revision             string `json:"revision"`
		SpecificationVersion string `json:"specificationVersion"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("decode conformance lock: %v", err)
	}
	if lock.SpecificationVersion != contexture.SpecificationVersion {
		t.Errorf("specification version lock = %q, public constant = %q", lock.SpecificationVersion, contexture.SpecificationVersion)
	}
	if lock.Revision != contexture.SpecificationRevision {
		t.Errorf("specification revision lock = %q, public constant = %q", lock.Revision, contexture.SpecificationRevision)
	}
}

func TestApplicationDeclarationIsLazy(t *testing.T) {
	t.Parallel()

	constructions := 0
	application, err := contexture.Contexture(contexture.ApplicationDeclaration{
		Name: " operations ",
		Roots: []contexture.Factory{func() contexture.Node {
			constructions++
			return &contexture.Role{
				Name:         "operations",
				Description:  "Handle routine operational questions.",
				Instructions: "Inspect first.",
			}
		}},
	})
	if err != nil {
		t.Fatalf("Contexture() error = %v", err)
	}
	if application.Name() != "operations" {
		t.Fatalf("Name() = %q, want operations", application.Name())
	}
	if application.RootCount() != 1 {
		t.Fatalf("RootCount() = %d, want 1", application.RootCount())
	}
	if constructions != 0 {
		t.Fatalf("root factory called %d times during declaration", constructions)
	}
}

func TestApplicationRequiresModelRoot(t *testing.T) {
	t.Parallel()

	_, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "empty"})
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("DeclareApplication() error = %v, want ErrInvalidDeclaration", err)
	}
}

func TestApplicationRejectsBlankName(t *testing.T) {
	_, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: " \t ",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
		}},
	})
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("DeclareApplication() error = %v, want ErrInvalidDeclaration", err)
	}
}

func TestApplicationRejectsTypedNilChannelsLifecycle(t *testing.T) {
	var channels *noOpChannels
	_, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name:     "typed-nil-channels",
		Channels: channels,
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
		}},
	})
	if !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil application Channels = %v", err)
	}
}

func TestRuntimeAndDisclosureOnlyCompilationStaySeparate(t *testing.T) {
	unbound := &contexture.Tool{Name: "status", Description: "Status.", ReadOnly: true}
	runtimeDeclaration, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name:  "runtime",
		Roots: []contexture.Factory{func() contexture.Node { return unbound }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.Compile(runtimeDeclaration); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("runtime compilation accepted a Tool without Binding: %v", err)
	}

	disclosureDeclaration, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name:  "disclosure",
		Roots: []contexture.Factory{func() contexture.Node { return unbound }},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(disclosureDeclaration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil); err == nil {
		t.Fatal("disclosure-only Index was upgraded into a Runtime")
	}
	if _, err := contexture.NewDisclosure(index, contexture.AllRoots()); err == nil {
		t.Fatal("disclosure-only Index was accepted by runtime Disclosure")
	}
	if _, err := contexture.NewDisclosureOnly(index, contexture.AllRoots()); err != nil {
		t.Fatalf("NewDisclosureOnly rejected unbound Index: %v", err)
	}

	channelsDeclaration, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name:     "channels",
		Channels: noOpChannels{},
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "skill", Description: "Skill.", Instructions: "Read."}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.CompileDisclosure(channelsDeclaration); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("disclosure-only compilation accepted Channels: %v", err)
	}
	resourceDeclaration, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "resources",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "skill", Description: "Skill.", Instructions: "Read."}
		}},
		Resources: []contexture.ResourceDeclaration{{Opens: "skill", URI: "contexture://skill", Description: "Skill."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.CompileDisclosure(resourceDeclaration); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("disclosure-only compilation accepted Resources: %v", err)
	}
}

type noOpChannels struct{ contexture.ChannelsLifecycle }

func (noOpChannels) Open(context.Context, contexture.CleanupRegistrar) error { return nil }
func (noOpChannels) Close(context.Context) error                             { return nil }
