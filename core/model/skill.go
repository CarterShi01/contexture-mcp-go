package model

// Skill is model-followed procedural knowledge.
type Skill struct {
	Name         string
	Description  string
	Instructions string
	Uses         []string
	owner        *Index
	ref          string
}

func (*Skill) nodeKind() Kind               { return SkillKind }
func (node *Skill) nodeName() string        { return node.Name }
func (node *Skill) nodeDescription() string { return node.Description }
func (node *Skill) nodeUses() []string      { return append([]string(nil), node.Uses...) }
func (node *Skill) Kind() Kind              { return node.nodeKind() }
func (node *Skill) NodeName() string        { return node.nodeName() }
func (node *Skill) NodeDescription() string { return node.nodeDescription() }
func (node *Skill) NodeUses() []string      { return node.nodeUses() }
func (node *Skill) Ref() (string, error)    { return nodeRef(node) }
