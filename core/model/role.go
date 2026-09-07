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
	Children     []Factory
	Skills       []Factory
	Tools        []Factory
	Uses         []string
	owner        *Index
	ref          string
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
	members, err := node.Members()
	if err != nil {
		return nil, err
	}
	branches := make([]*Role, 0)
	for _, member := range members {
		if branch, ok := member.(*Role); ok {
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

// Members returns every direct member of this compiled Role in declaration
// group order: child Roles, Skills, then Tools. A raw declaration deliberately
// has no members query because its fields contain lazy factories; use a
// compiled Index (or Index.ChildrenOf) instead of evaluating factories during
// declaration inspection.
func (node *Role) Members() ([]Node, error) {
	if node == nil || node.owner == nil || node.ref == "" {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("Role membership requires a compiled Index snapshot"))
	}
	return node.owner.ChildrenOf(node)
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
