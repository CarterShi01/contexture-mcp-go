package contexture_test

import (
	"errors"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestErrorSentinelsPreserveGoNativeContextureCategories(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		name    string
		err     error
		parents []error
	}{
		{"invalid declaration", contexture.ErrInvalidDeclaration, []error{contexture.ErrDeclaration, contexture.ErrModelValidation, contexture.ErrContexture}},
		{"duplicate name", contexture.ErrDuplicate, []error{contexture.ErrDuplicateName, contexture.ErrModelValidation, contexture.ErrContexture}},
		{"containment", contexture.ErrContainmentCycle, []error{contexture.ErrModelValidation, contexture.ErrContexture}},
		{"unresolved use", contexture.ErrUnresolvedReference, []error{contexture.ErrModelValidation, contexture.ErrContexture}},
		{"invalid input", contexture.ErrInvalidInput, []error{contexture.ErrContexture}},
		{"wrong door", contexture.ErrWrongDoor, []error{contexture.ErrContexture}},
		{"selection", contexture.ErrInvalidSelection, []error{contexture.ErrContexture}},
		{"outside selection", contexture.ErrRootOutsideSelection, []error{contexture.ErrContexture}},
		{"node missing", contexture.ErrNodeNotFound, []error{contexture.ErrContexture}},
	} {
		for _, parent := range check.parents {
			if !errors.Is(check.err, parent) {
				t.Fatalf("%s %v does not classify as %v", check.name, check.err, parent)
			}
		}
	}

	if errors.Is(contexture.ErrInvalidSelection, contexture.ErrModelValidation) {
		t.Fatal("root selection is a Contexture request error, not a model-validation error")
	}
	if errors.Is(contexture.ErrDuplicate, contexture.ErrDeclaration) {
		t.Fatal("duplicate identity must retain its own ModelValidation branch")
	}

	_, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: " "})
	if !errors.Is(err, contexture.ErrInvalidDeclaration) || !errors.Is(err, contexture.ErrDeclaration) || !errors.Is(err, contexture.ErrModelValidation) || !errors.Is(err, contexture.ErrContexture) {
		t.Fatalf("declaration error lost category chain: %v", err)
	}

	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "duplicate", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "One.", Instructions: "Read."}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Two.", Instructions: "Write."}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = contexture.CompileDisclosure(application)
	if !errors.Is(err, contexture.ErrDuplicate) || !errors.Is(err, contexture.ErrDuplicateName) || !errors.Is(err, contexture.ErrModelValidation) || !errors.Is(err, contexture.ErrContexture) {
		t.Fatalf("duplicate error lost category chain: %v", err)
	}
}

func TestNodeNotFoundFactsWithinAndDeveloperSummary(t *testing.T) {
	t.Parallel()

	failure := &contexture.NodeNotFoundError{
		Reason: contexture.NoSuchMember, Segment: "banana", Scope: "troubleshooter", Kind: "role", Known: []string{"diagnose", "get_pod_logs"},
	}
	within := failure.Within("team/troubleshooter/banana")
	if within == failure || failure.Ref != "" || within.Ref != "team/troubleshooter/banana" || !within.HasRef {
		t.Fatalf("Within did not attach a complete reference without mutating source: source=%#v result=%#v", failure, within)
	}
	within.Known[0] = "changed"
	if failure.Known[0] != "diagnose" || within.Within("other/ref") != within {
		t.Fatalf("Within did not preserve immutable prior facts: source=%#v result=%#v", failure, within)
	}
	known := within.KnownRefs()
	known[0] = "caller-mutation"
	if within.Known[0] != "changed" {
		t.Fatalf("KnownRefs exposed mutable facts: %#v", within.Known)
	}
	summary := within.DeveloperSummary()
	for _, expected := range []string{"no_such_member:", `ref="team/troubleshooter/banana"`, `segment="banana"`, `scope="troubleshooter"`, `kind="role"`, `known=["changed" "get_pod_logs"]`} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("DeveloperSummary %q omitted %q", summary, expected)
		}
	}
	if strings.Contains(summary, "contexture_open") || strings.Contains(summary, "contexture_discover") {
		t.Fatalf("developer summary leaked agent recovery prose: %q", summary)
	}
	empty := &contexture.NodeNotFoundError{Reason: contexture.EmptyRef, Ref: "", HasRef: true}
	if !strings.Contains(empty.Error(), `ref=""`) {
		t.Fatalf("explicit empty reference disappeared from developer summary: %q", empty.Error())
	}
	if !errors.Is(within, contexture.ErrNodeNotFound) || !errors.Is(within, contexture.ErrContexture) {
		t.Fatalf("lookup failure lost Contexture category: %v", within)
	}
}

func TestLookupFailureTaxonomyAndWrongDoorDeveloperError(t *testing.T) {
	t.Parallel()

	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "lookups", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read."}
		}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		ref    string
		reason contexture.LookupFailure
	}{
		{"", contexture.EmptyRef},
		{"missing", contexture.NoSuchRoot},
		{"operations/missing", contexture.NoSuchMember},
		{"operations/diagnose/deeper", contexture.NotAContainer},
	} {
		_, err := index.Find(check.ref)
		var failure *contexture.NodeNotFoundError
		if !errors.As(err, &failure) || failure.Reason != check.reason || !failure.HasRef || !errors.Is(err, contexture.ErrNodeNotFound) || !errors.Is(err, contexture.ErrContexture) {
			t.Fatalf("Find(%q) = %#v, want complete %q lookup facts", check.ref, err, check.reason)
		}
	}
	_, err = index.Tool("operations")
	var wrongKind *contexture.NodeNotFoundError
	if !errors.As(err, &wrongKind) || wrongKind.Reason != contexture.WrongKind || wrongKind.Kind != "role" || wrongKind.Wanted != "tool" {
		t.Fatalf("Tool wrong-kind facts = %#v", err)
	}

	wrongDoor := &contexture.WrongDoorError{Ref: "operations/change", ReadOnly: false}
	if got, want := wrongDoor.Error(), `"operations/change" is a writing Tool`; got != want {
		t.Fatalf("WrongDoorError developer text = %q, want %q", got, want)
	}
	if !errors.Is(wrongDoor, contexture.ErrWrongDoor) || !errors.Is(wrongDoor, contexture.ErrContexture) {
		t.Fatalf("WrongDoorError lost category chain: %v", wrongDoor)
	}
}
