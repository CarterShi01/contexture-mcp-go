// Package foundation contains Contexture facts shared by model and Host layers.
package foundation

import (
	"fmt"
	"strings"
)

// categoryError is a Go-native replacement for Python exception inheritance.
// Its error text stays stable while errors.Is can classify a specific failure
// as one of its broader Contexture domains.
type categoryError struct {
	message string
	parent  *categoryError
}

func (err *categoryError) Error() string { return err.message }

func (err *categoryError) Is(target error) bool {
	wanted, ok := target.(*categoryError)
	if !ok {
		return false
	}
	for current := err; current != nil; current = current.parent {
		if current == wanted {
			return true
		}
	}
	return false
}

// LookupFailure identifies the precise way a canonical Contexture ref failed.
type LookupFailure string

const (
	EmptyRef      LookupFailure = "empty_ref"
	NoSuchRoot    LookupFailure = "no_such_root"
	NotAContainer LookupFailure = "not_a_container"
	NoSuchMember  LookupFailure = "no_such_member"
	WrongKind     LookupFailure = "wrong_kind"
)

var (
	// ErrContexture is the umbrella category for a framework-domain failure.
	// It is the Go-native equivalent of catching Python ContextureError.
	ErrContexture = &categoryError{message: "Contexture error"}

	// ErrModelValidation classifies an invalid Contexture object-model fact.
	// It corresponds to Python ModelValidationError.
	ErrModelValidation = &categoryError{message: "invalid Contexture model", parent: ErrContexture}

	// ErrDeclaration classifies a declaration the model cannot accept. It maps
	// to Python DeclarationError while retaining errors.Is composition.
	ErrDeclaration = &categoryError{message: "invalid Contexture declaration", parent: ErrModelValidation}

	// ErrDuplicateName classifies one identity collision in a declaration. It
	// maps to Python DuplicateNameError.
	ErrDuplicateName = &categoryError{message: "duplicate Contexture identity", parent: ErrModelValidation}

	// ErrInvalidDeclaration identifies an invalid application declaration.
	// It is a more precise declaration-category spelling retained for the Go
	// binding's established API.
	ErrInvalidDeclaration = &categoryError{message: "invalid Contexture declaration", parent: ErrDeclaration}

	// ErrInvalidInput identifies arguments that do not satisfy a Tool Binding.
	ErrInvalidInput = &categoryError{message: "invalid Contexture tool arguments", parent: ErrContexture}

	// ErrDuplicate identifies duplicate names, addresses, or node identity.
	// It remains the established public spelling; ErrDuplicateName is its
	// cross-language semantic category.
	ErrDuplicate = ErrDuplicateName

	// ErrContainmentCycle identifies recursive containment factories.
	ErrContainmentCycle = &categoryError{message: "Contexture containment cycle", parent: ErrModelValidation}

	// ErrUnresolvedReference identifies a uses reference absent from the forest.
	ErrUnresolvedReference = &categoryError{message: "unresolved Contexture reference", parent: ErrModelValidation}

	// ErrWrongDoor identifies a Tool invoked through the wrong fixed gateway door.
	ErrWrongDoor = &categoryError{message: "Contexture Tool invoked through the wrong door", parent: ErrContexture}

	// ErrInvalidSelection identifies an invalid root-level capability projection.
	ErrInvalidSelection = &categoryError{message: "invalid Contexture root selection", parent: ErrContexture}

	// ErrRootOutsideSelection identifies a ref excluded from a request's root
	// projection. It is intentionally distinct from malformed selection input.
	ErrRootOutsideSelection = &categoryError{message: "Contexture ref outside selected roots", parent: ErrContexture}

	// ErrNodeNotFound classifies a failed canonical node lookup with errors.Is.
	ErrNodeNotFound = &categoryError{message: "Contexture node not found", parent: ErrContexture}
)

// NodeNotFoundError carries lookup facts for an application to render for its
// own audience. It deliberately does not bake Host-specific recovery prose
// into the SDK-neutral model layer.
type NodeNotFoundError struct {
	Reason LookupFailure
	Ref    string
	// HasRef preserves Python's distinction between no complete reference yet
	// (a local lookup) and an explicitly empty reference. A non-empty Ref also
	// counts as present for compatibility with existing Go struct literals.
	HasRef  bool
	Segment string
	Scope   string
	Kind    string
	Wanted  string
	Known   []string
}

// Within returns this error unchanged when it already has a complete ref.
// Otherwise it attaches the ref while copying mutable Known facts, matching
// Python NodeNotFoundError.within without requiring every successful lookup to
// carry the full path.
func (err *NodeNotFoundError) Within(ref string) *NodeNotFoundError {
	if err == nil || err.hasRef() {
		return err
	}
	copy := *err
	copy.Ref = ref
	copy.HasRef = true
	copy.Known = append([]string(nil), err.Known...)
	return &copy
}

// KnownRefs returns a defensive copy of the facts available at the failed
// lookup scope. The exported Known field remains for source compatibility.
func (err *NodeNotFoundError) KnownRefs() []string {
	if err == nil {
		return nil
	}
	return append([]string(nil), err.Known...)
}

// DeveloperSummary renders field-shaped lookup facts for a Go caller. It is
// intentionally Host-neutral: agent recovery prose belongs at Gateway.
func (err *NodeNotFoundError) DeveloperSummary() string {
	if err == nil {
		return ErrNodeNotFound.Error()
	}
	parts := []string{string(err.Reason)}
	for _, fact := range []struct {
		name    string
		value   string
		present bool
	}{{"ref", err.Ref, err.hasRef()}, {"segment", err.Segment, err.Segment != ""}, {"scope", err.Scope, err.Scope != ""}, {"kind", err.Kind, err.Kind != ""}, {"wanted", err.Wanted, err.Wanted != ""}} {
		if fact.present {
			parts = append(parts, fmt.Sprintf("%s=%q", fact.name, fact.value))
		}
	}
	if len(err.Known) > 0 {
		parts = append(parts, fmt.Sprintf("known=%q", err.Known))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + ": " + strings.Join(parts[1:], " ")
}

// Error implements error with the developer-facing, Host-neutral summary.
func (err *NodeNotFoundError) Error() string { return err.DeveloperSummary() }

func (err *NodeNotFoundError) hasRef() bool { return err != nil && (err.HasRef || err.Ref != "") }

// Unwrap lets callers classify a lookup failure with errors.Is.
func (err *NodeNotFoundError) Unwrap() error { return ErrNodeNotFound }
