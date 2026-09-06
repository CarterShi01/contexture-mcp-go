package contexture

import "github.com/CarterShi01/contexture-mcp-go/core/foundation"

const (
	// Version is the Go binding package version. It is independent from the
	// specification version because one binding release can target one contract.
	Version = "0.12.0rc1"

	// SpecificationVersion is the Contexture contract targeted by this binding.
	SpecificationVersion = foundation.SpecificationVersion

	// SpecificationRevision is the immutable upstream revision used by tests.
	SpecificationRevision = foundation.SpecificationRevision
)

var (
	// ErrInvalidDeclaration identifies an invalid application declaration.
	ErrInvalidDeclaration = foundation.ErrInvalidDeclaration
	// ErrInvalidInput identifies arguments that do not satisfy a Tool Binding.
	ErrInvalidInput = foundation.ErrInvalidInput
	// ErrDuplicate identifies duplicate names, addresses, or node identity.
	ErrDuplicate = foundation.ErrDuplicate
	// ErrContainmentCycle identifies recursive containment factories.
	ErrContainmentCycle = foundation.ErrContainmentCycle
	// ErrUnresolvedReference identifies a uses reference absent from the forest.
	ErrUnresolvedReference = foundation.ErrUnresolvedReference
	// ErrWrongDoor identifies a Tool invoked through the wrong fixed gateway door.
	ErrWrongDoor = foundation.ErrWrongDoor
	// ErrInvalidSelection identifies an invalid root-level capability projection.
	ErrInvalidSelection = foundation.ErrInvalidSelection
)
