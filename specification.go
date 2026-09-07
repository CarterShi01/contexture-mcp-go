package contexture

import "github.com/CarterShi01/contexture-mcp-go/core/foundation"

const (
	// PackageName is Contexture framework metadata, not an application or MCP
	// server identity.
	PackageName = foundation.PackageName

	// Version is the Go binding package version. It is independent from the
	// specification version because one binding release can target one contract.
	Version = foundation.PackageVersion

	// ReferenceSeparator spells the boundary between Contexture reference
	// segments. It is shared by declaration, lookup, and navigation APIs.
	ReferenceSeparator = foundation.ReferenceSeparator

	// SpecificationVersion is the Contexture contract targeted by this binding.
	SpecificationVersion = foundation.SpecificationVersion

	// SpecificationRevision is the immutable upstream revision used by tests.
	SpecificationRevision = foundation.SpecificationRevision
)

var (
	// ErrContexture is the umbrella category for a framework-domain error.
	ErrContexture = foundation.ErrContexture
	// ErrModelValidation identifies invalid Contexture model facts.
	ErrModelValidation = foundation.ErrModelValidation
	// ErrDeclaration identifies a declaration the model cannot accept.
	ErrDeclaration = foundation.ErrDeclaration
	// ErrDuplicateName identifies a duplicate declaration identity.
	ErrDuplicateName = foundation.ErrDuplicateName
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
	// ErrRootOutsideSelection identifies a ref excluded from a request surface.
	ErrRootOutsideSelection = foundation.ErrRootOutsideSelection
	// ErrNodeNotFound identifies failed canonical node lookup.
	ErrNodeNotFound = foundation.ErrNodeNotFound
)
