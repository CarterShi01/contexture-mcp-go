// Package foundation contains Contexture facts shared by model and Host layers.
package foundation

import "errors"

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
