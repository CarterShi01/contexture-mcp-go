package contexture_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestSpecificationIdentity(t *testing.T) {
	t.Parallel()

	if contexture.SpecificationVersion != "0.12" {
		t.Fatalf("SpecificationVersion = %q, want 0.12", contexture.SpecificationVersion)
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
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "operations",
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
		t.Fatalf("DeclareApplication() error = %v", err)
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
