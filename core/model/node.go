package model

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
}

// Kind identifies one Contexture node kind.
type Kind string

const (
	RoleKind  Kind = "role"
	SkillKind Kind = "skill"
	ToolKind  Kind = "tool"
)
