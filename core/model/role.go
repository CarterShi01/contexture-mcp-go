package model

import (
	"errors"
	"sort"
)

// Role is a containment and responsibility boundary.
type Role struct {
	Name         string
	Description  string
	Instructions string
	PreProcess   func() *PreProcess
	Children     []Factory
	PostProcess  func() *PostProcess
	Skills       []Factory
	Tools        []Factory
	Uses         []string
	owner        *Index
	ref          string
	preProcess   *Role
	postProcess  *Role
}

func (*Role) nodeKind() Kind               { return RoleKind }
func (node *Role) nodeName() string        { return node.Name }
func (node *Role) nodeDescription() string { return node.Description }
func (node *Role) nodeUses() []string      { return append([]string(nil), node.Uses...) }
func (node *Role) Kind() Kind              { return node.nodeKind() }
func (node *Role) NodeName() string        { return node.nodeName() }
func (node *Role) NodeDescription() string { return node.nodeDescription() }
func (node *Role) NodeUses() []string      { return node.nodeUses() }
func (node *Role) Ref() (string, error)    { return nodeRef(node) }

// Branches returns this compiled Role's direct child Roles in declaration
// order. It is a structural query over the immutable compiled snapshot, so it
// never evaluates declaration factories. Each returned node is a defensive
// copy owned by the same Index.
func (node *Role) Branches() ([]*Role, error) {
	if node == nil || node.owner == nil || node.ref == "" {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("Role branches require a compiled Index snapshot"))
	}
	refs := node.owner.branchRefs[node.ref]
	branches := make([]*Role, 0, len(refs))
	for _, ref := range refs {
		branches = append(branches, cloneNode(node.owner.byRef[ref], node.owner, ref).(*Role))
	}
	return branches, nil
}

// Members returns every direct member of this compiled Role in work order:
// optional PreProcess, child Roles, optional PostProcess, Skills, then Tools. A raw declaration deliberately
// has no members query because its fields contain lazy factories; use a
// compiled Index (or Index.ChildrenOf) instead of evaluating factories during
// declaration inspection.
func (node *Role) Members() ([]Node, error) {
	if node == nil || node.owner == nil || node.ref == "" {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("Role membership requires a compiled Index snapshot"))
	}
	return node.owner.ChildrenOf(node)
}

// PreProcess is a Role specialized in preparing what its owner's work depends
// on. It is an ordinary role on the wire and never an automatic callback.
type PreProcess Role

// PostProcess is a Role specialized in the procedure required to finish its
// owner's work. It is an ordinary role on the wire and never an automatic callback.
type PostProcess Role

func (*PreProcess) nodeKind() Kind                  { return RoleKind }
func (node *PreProcess) nodeName() string           { return node.Name }
func (node *PreProcess) nodeDescription() string    { return node.Description }
func (node *PreProcess) nodeUses() []string         { return append([]string(nil), node.Uses...) }
func (node *PreProcess) Kind() Kind                 { return node.nodeKind() }
func (node *PreProcess) NodeName() string           { return node.nodeName() }
func (node *PreProcess) NodeDescription() string    { return node.nodeDescription() }
func (node *PreProcess) NodeUses() []string         { return node.nodeUses() }
func (node *PreProcess) Ref() (string, error)       { return nodeRef(node) }
func (node *PreProcess) Branches() ([]*Role, error) { return (*Role)(node).Branches() }
func (node *PreProcess) Members() ([]Node, error)   { return (*Role)(node).Members() }
func (node *PreProcess) Member(name string) (Node, error) {
	return (*Role)(node).Member(name)
}

func (*PostProcess) nodeKind() Kind                  { return RoleKind }
func (node *PostProcess) nodeName() string           { return node.Name }
func (node *PostProcess) nodeDescription() string    { return node.Description }
func (node *PostProcess) nodeUses() []string         { return append([]string(nil), node.Uses...) }
func (node *PostProcess) Kind() Kind                 { return node.nodeKind() }
func (node *PostProcess) NodeName() string           { return node.nodeName() }
func (node *PostProcess) NodeDescription() string    { return node.nodeDescription() }
func (node *PostProcess) NodeUses() []string         { return node.nodeUses() }
func (node *PostProcess) Ref() (string, error)       { return nodeRef(node) }
func (node *PostProcess) Branches() ([]*Role, error) { return (*Role)(node).Branches() }
func (node *PostProcess) Members() ([]Node, error)   { return (*Role)(node).Members() }
func (node *PostProcess) Member(name string) (Node, error) {
	return (*Role)(node).Member(name)
}

// Member resolves one direct member by name across Role, Skill, and Tool
// groups. A member name is unique within a compiled Role because it is the
// final segment of that member's canonical reference.
func (node *Role) Member(name string) (Node, error) {
	members, err := node.Members()
	if err != nil {
		return nil, err
	}
	known := make([]string, 0, len(members))
	for _, member := range members {
		if member.NodeName() == name {
			return member, nil
		}
		known = append(known, member.NodeName())
	}
	sort.Strings(known)
	return nil, &NodeNotFoundError{Reason: NoSuchMember, Segment: name, Scope: node.Name, Kind: string(RoleKind), Known: known}
}
