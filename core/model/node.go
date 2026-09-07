package model

import (
	"errors"
)

// Factory constructs a fresh pointer-backed node during compilation.
type Factory func() Node

// Node is the closed Contexture declaration set.
// Its unexported methods prevent external kinds from entering the forest.
type Node interface {
	nodeKind() Kind
	nodeName() string
	nodeDescription() string
	nodeUses() []string
	Kind() Kind
	NodeName() string
	NodeDescription() string
	NodeUses() []string
	Ref() (string, error)
}

// Kind identifies one Contexture node kind.
type Kind string

const (
	RoleKind  Kind = "role"
	SkillKind Kind = "skill"
	ToolKind  Kind = "tool"
)

// BranchesOf returns a compiled node's direct containment branches. Only a
// Role has branches; Skill and Tool deliberately return an empty result. The
// returned nodes are defensive snapshots in declaration order.
func BranchesOf(node Node) ([]Node, error) {
	if node == nil {
		return nil, uncompiledNodeError()
	}
	role, ok := node.(*Role)
	if !ok {
		return []Node{}, nil
	}
	branches, err := role.Branches()
	if err != nil {
		return nil, err
	}
	result := make([]Node, 0, len(branches))
	for _, branch := range branches {
		result = append(result, branch)
	}
	return result, nil
}

// MembersOf returns a compiled node's direct containment members. Only a Role
// has members; Skill and Tool deliberately return an empty result. Role member
// order is child Roles, Skills, then Tools, matching declaration grouping.
func MembersOf(node Node) ([]Node, error) {
	if node == nil {
		return nil, uncompiledNodeError()
	}
	role, ok := node.(*Role)
	if !ok {
		return []Node{}, nil
	}
	return role.Members()
}

func nodeRef(node Node) (string, error) {
	owner, ref := nodeLocation(node)
	if owner == nil || ref == "" || !owner.Has(ref) {
		return "", uncompiledNodeError()
	}
	return ref, nil
}

func uncompiledNodeError() error {
	return errors.Join(ErrInvalidDeclaration, errors.New("Node reference and containment queries require a compiled Index snapshot"))
}
