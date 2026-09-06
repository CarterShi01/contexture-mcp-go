// Package foundation contains Contexture facts shared by model and Host layers.
package foundation

import (
	"errors"
	"fmt"
	"strings"
)

// LookupFailure identifies the precise way a canonical Contexture ref failed.
type LookupFailure string

const (
	EmptyRef      LookupFailure = "empty_ref"
	NoSuchRoot    LookupFailure = "no_such_root"
	NotAContainer LookupFailure = "not_a_container"
	NoSuchMember  LookupFailure = "no_such_member"
	WrongKind     LookupFailure = "wrong_kind"
)

// ErrInvalidDeclaration identifies an invalid application declaration.
var ErrInvalidDeclaration = errors.New("invalid Contexture declaration")

// ErrInvalidInput identifies arguments that do not satisfy a Tool Binding.
var ErrInvalidInput = errors.New("invalid Contexture tool arguments")

// ErrDuplicate identifies duplicate names, addresses, or node identity.
var ErrDuplicate = errors.New("duplicate Contexture identity")

// ErrContainmentCycle identifies recursive containment factories.
var ErrContainmentCycle = errors.New("Contexture containment cycle")

// ErrUnresolvedReference identifies a uses reference absent from the forest.
var ErrUnresolvedReference = errors.New("unresolved Contexture reference")

// ErrWrongDoor identifies a Tool invoked through the wrong fixed gateway door.
var ErrWrongDoor = errors.New("Contexture Tool invoked through the wrong door")

// ErrInvalidSelection identifies an invalid root-level capability projection.
var ErrInvalidSelection = errors.New("invalid Contexture root selection")

// ErrRootOutsideSelection identifies a ref excluded from a request's root
// projection. It is intentionally distinct from malformed selection input.
var ErrRootOutsideSelection = errors.New("Contexture ref outside selected roots")

// ErrNodeNotFound classifies a failed canonical node lookup with errors.Is.
var ErrNodeNotFound = errors.New("Contexture node not found")

// NodeNotFoundError carries lookup facts for an application to render for its
// own audience. It deliberately does not bake Host-specific recovery prose
// into the SDK-neutral model layer.
type NodeNotFoundError struct {
	Reason  LookupFailure
	Ref     string
	Segment string
	Scope   string
	Kind    string
	Wanted  string
	Known   []string
}

func (err *NodeNotFoundError) Error() string {
	if err == nil {
		return ErrNodeNotFound.Error()
	}
	parts := []string{string(err.Reason)}
	for _, fact := range []struct{ name, value string }{{"ref", err.Ref}, {"segment", err.Segment}, {"scope", err.Scope}, {"kind", err.Kind}, {"wanted", err.Wanted}} {
		if fact.value != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", fact.name, fact.value))
		}
	}
	if len(err.Known) > 0 {
		parts = append(parts, fmt.Sprintf("known=%q", err.Known))
	}
	return strings.Join(parts, " ")
}

// Unwrap lets callers classify a lookup failure with errors.Is.
func (err *NodeNotFoundError) Unwrap() error { return ErrNodeNotFound }
