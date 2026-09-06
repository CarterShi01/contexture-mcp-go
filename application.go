package contexture

import (
	"errors"
	"strings"
)

// ErrInvalidDeclaration identifies an invalid application declaration.
var ErrInvalidDeclaration = errors.New("invalid Contexture declaration")

// Role is the initial language-native role declaration boundary.
//
// Members, uses, Skills, and Tools will be added with the compiler milestone.
// Keeping the scaffold small avoids promising a public API before its Go
// ergonomics and conformance behavior have been tested together.
type Role struct {
	Name         string
	Description  string
	Instructions string
}

// RoleFactory constructs one fresh Role during compilation.
type RoleFactory func() Role

// ApplicationDeclaration is the lazy composition-root input.
type ApplicationDeclaration struct {
	Name        string
	Roots       []RoleFactory
	PromptRoots []RoleFactory
}

// Application holds a validated declaration without evaluating its factories.
type Application struct {
	name        string
	roots       []RoleFactory
	promptRoots []RoleFactory
}

// DeclareApplication validates a composition root without constructing nodes.
func DeclareApplication(declaration ApplicationDeclaration) (*Application, error) {
	if strings.TrimSpace(declaration.Name) == "" {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application name must not be empty"))
	}
	if len(declaration.Roots) == 0 {
		return nil, errors.Join(
			ErrInvalidDeclaration,
			errors.New("application must declare at least one model-visible root"),
		)
	}

	return &Application{
		name:        declaration.Name,
		roots:       append([]RoleFactory(nil), declaration.Roots...),
		promptRoots: append([]RoleFactory(nil), declaration.PromptRoots...),
	}, nil
}

// Name returns the declared application name.
func (application *Application) Name() string {
	return application.name
}

// RootCount returns the number of model-visible root factories without calling them.
func (application *Application) RootCount() int {
	return len(application.roots)
}

// PromptRootCount returns the number of user-controlled root factories without calling them.
func (application *Application) PromptRootCount() int {
	return len(application.promptRoots)
}
