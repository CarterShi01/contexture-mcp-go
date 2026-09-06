package model

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
