package model

import "github.com/CarterShi01/contexture-mcp-go/core/foundation"

var (
	ErrContexture           = foundation.ErrContexture
	ErrModelValidation      = foundation.ErrModelValidation
	ErrDeclaration          = foundation.ErrDeclaration
	ErrDuplicateName        = foundation.ErrDuplicateName
	ErrInvalidDeclaration   = foundation.ErrInvalidDeclaration
	ErrInvalidInput         = foundation.ErrInvalidInput
	ErrDuplicate            = foundation.ErrDuplicate
	ErrContainmentCycle     = foundation.ErrContainmentCycle
	ErrUnresolvedReference  = foundation.ErrUnresolvedReference
	ErrWrongDoor            = foundation.ErrWrongDoor
	ErrInvalidSelection     = foundation.ErrInvalidSelection
	ErrRootOutsideSelection = foundation.ErrRootOutsideSelection
	ErrNodeNotFound         = foundation.ErrNodeNotFound
)

type LookupFailure = foundation.LookupFailure
type NodeNotFoundError = foundation.NodeNotFoundError

const (
	EmptyRef      = foundation.EmptyRef
	NoSuchRoot    = foundation.NoSuchRoot
	NotAContainer = foundation.NotAContainer
	NoSuchMember  = foundation.NoSuchMember
	WrongKind     = foundation.WrongKind
)
