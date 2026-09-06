package model

import (
	"errors"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/mcpinterface"
)

// ApplicationDeclaration is the lazy composition-root input.
type ApplicationDeclaration struct {
	Name        string
	Roots       []Factory
	PromptRoots []Factory
	Channels    Channels
	Prompts     []PromptDeclaration
	Resources   []ResourceDeclaration
}

// PromptDeclaration is the SDK-free person-controlled MCP Prompt declaration.
type PromptDeclaration = mcpinterface.PromptDeclaration

// ResourceDeclaration is the SDK-free host-controlled MCP Resource declaration.
type ResourceDeclaration = mcpinterface.ResourceDeclaration

// Application holds a validated declaration without evaluating its factories.
type Application struct {
	name        string
	roots       []Factory
	promptRoots []Factory
	channels    Channels
	prompts     []PromptDeclaration
	resources   []ResourceDeclaration
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
	return &Application{name: strings.TrimSpace(declaration.Name), roots: append([]Factory(nil), declaration.Roots...), promptRoots: append([]Factory(nil), declaration.PromptRoots...), channels: declaration.Channels, prompts: append([]PromptDeclaration(nil), declaration.Prompts...), resources: append([]ResourceDeclaration(nil), declaration.Resources...)}, nil
}

// Name returns the declared application name.
func (application *Application) Name() string { return application.name }

// RootCount returns model-visible factories without calling them.
func (application *Application) RootCount() int { return len(application.roots) }

// PromptRootCount returns user-controlled factories without calling them.
func (application *Application) PromptRootCount() int { return len(application.promptRoots) }

// Prompts returns publication declarations in declaration order.
func (application *Application) Prompts() []PromptDeclaration {
	return append([]PromptDeclaration(nil), application.prompts...)
}

// Resources returns publication declarations in declaration order.
func (application *Application) Resources() []ResourceDeclaration {
	return append([]ResourceDeclaration(nil), application.resources...)
}
