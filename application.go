package contexture

import (
	"errors"
	"strings"
)

// ErrInvalidDeclaration identifies an invalid application declaration.
var ErrInvalidDeclaration = errors.New("invalid Contexture declaration")

// Factory constructs a fresh pointer-backed node during compilation.
type Factory func() Node

// Node is the closed Contexture declaration set.
// Its unexported method prevents external kinds from entering the forest.
type Node interface {
	nodeKind() Kind
	nodeName() string
	nodeDescription() string
	nodeUses() []string
}

// Kind identifies one Contexture node kind.
type Kind string

const (
	RoleKind  Kind = "role"
	SkillKind Kind = "skill"
	ToolKind  Kind = "tool"
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
}

func (*Role) nodeKind() Kind               { return RoleKind }
func (node *Role) nodeName() string        { return node.Name }
func (node *Role) nodeDescription() string { return node.Description }
func (node *Role) nodeUses() []string      { return append([]string(nil), node.Uses...) }

// Skill is model-followed procedural knowledge.
type Skill struct {
	Name         string
	Description  string
	Instructions string
	Uses         []string
}

func (*Skill) nodeKind() Kind               { return SkillKind }
func (node *Skill) nodeName() string        { return node.Name }
func (node *Skill) nodeDescription() string { return node.Description }
func (node *Skill) nodeUses() []string      { return append([]string(nil), node.Uses...) }

// Tool is an executable capability. Its Binding is established by later compilation.
type Tool struct {
	Name        string
	Description string
	ReadOnly    bool
	Uses        []string
}

func (*Tool) nodeKind() Kind               { return ToolKind }
func (node *Tool) nodeName() string        { return node.Name }
func (node *Tool) nodeDescription() string { return node.Description }
func (node *Tool) nodeUses() []string      { return append([]string(nil), node.Uses...) }

// ApplicationDeclaration is the lazy composition-root input.
type ApplicationDeclaration struct {
	Name        string
	Roots       []Factory
	PromptRoots []Factory
}

// Application holds a validated declaration without evaluating its factories.
type Application struct {
	name        string
	roots       []Factory
	promptRoots []Factory
}

// DeclareApplication validates a composition root without constructing nodes.
func DeclareApplication(declaration ApplicationDeclaration) (*Application, error) {
	if strings.TrimSpace(declaration.Name) == "" {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application name must not be empty"))
	}
	if len(declaration.Roots) == 0 {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application must declare at least one model-visible root"))
	}
	for _, factory := range append(append([]Factory(nil), declaration.Roots...), declaration.PromptRoots...) {
		if factory == nil {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("application roots must be lazy factories"))
		}
	}
	return &Application{name: declaration.Name, roots: append([]Factory(nil), declaration.Roots...), promptRoots: append([]Factory(nil), declaration.PromptRoots...)}, nil
}

// Name returns the declared application name.
func (application *Application) Name() string { return application.name }

// RootCount returns model-visible factories without calling them.
func (application *Application) RootCount() int { return len(application.roots) }

// PromptRootCount returns user-controlled factories without calling them.
func (application *Application) PromptRootCount() int { return len(application.promptRoots) }
