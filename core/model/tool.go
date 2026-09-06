package model

// Tool is an executable capability. Its Binding is established by later compilation.
type Tool struct {
	Name        string
	Description string
	ReadOnly    bool
	Uses        []string
	binding     Binding
	owner       *Index
	ref         string
}

func (*Tool) nodeKind() Kind               { return ToolKind }
func (node *Tool) nodeName() string        { return node.Name }
func (node *Tool) nodeDescription() string { return node.Description }
func (node *Tool) nodeUses() []string      { return append([]string(nil), node.Uses...) }
func (node *Tool) Kind() Kind              { return node.nodeKind() }
func (node *Tool) NodeName() string        { return node.nodeName() }
func (node *Tool) NodeDescription() string { return node.nodeDescription() }
func (node *Tool) NodeUses() []string      { return node.nodeUses() }
