package model

import (
	"errors"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// ApplicationDeclaration is the lazy composition-root input.
type ApplicationDeclaration struct {
	Name        string
	Roots       []Factory
	PromptRoots []Factory
	Channels    Channels
	Telemetry   Telemetry
	Prompts     []PromptDeclaration
	Resources   []ResourceDeclaration
}

// PromptDeclaration is the SDK-free person-controlled MCP Prompt declaration.
type PromptDeclaration = foundation.PromptDeclaration

// ModelOpenPolicy controls whether a prompt target also admits model navigation.
type ModelOpenPolicy = foundation.ModelOpenPolicy

const (
	ModelMayOpen           = foundation.ModelMayOpen
	ModelReservedForPerson = foundation.ModelReservedForPerson
)

// ResourceDeclaration is the SDK-free host-controlled MCP Resource declaration.
type ResourceDeclaration = foundation.ResourceDeclaration

// Application holds a validated declaration without evaluating its factories.
type Application struct {
	name        string
	roots       []Factory
	promptRoots []Factory
	channels    ChannelHandle
	telemetry   Telemetry
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
	if nilChannels(declaration.Channels) {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application Channels lifecycle must not be typed nil"))
	}
	for _, factory := range append(append([]Factory(nil), declaration.Roots...), declaration.PromptRoots...) {
		if factory == nil {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("application roots must be lazy factories"))
		}
	}
	for _, prompt := range declaration.Prompts {
		if strings.TrimSpace(prompt.Opens) == "" || strings.TrimSpace(prompt.Description) == "" {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("a Prompt requires a non-empty opens ref and description"))
		}
		if prompt.Name != "" && strings.TrimSpace(prompt.Name) == "" {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("a Prompt name must be non-empty when supplied"))
		}
		if prompt.ModelOpen != foundation.ModelMayOpen && prompt.ModelOpen != foundation.ModelReservedForPerson {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("a Prompt model open policy is not supported"))
		}
	}
	for _, resource := range declaration.Resources {
		if strings.TrimSpace(resource.Opens) == "" || strings.TrimSpace(resource.URI) == "" || strings.TrimSpace(resource.Description) == "" {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("a Resource requires a non-empty opens ref, URI, and description"))
		}
		if resource.Name != "" && strings.TrimSpace(resource.Name) == "" {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("a Resource name must be non-empty when supplied"))
		}
	}
	return &Application{name: strings.TrimSpace(declaration.Name), roots: append([]Factory(nil), declaration.Roots...), promptRoots: append([]Factory(nil), declaration.PromptRoots...), channels: declaration.Channels, telemetry: declaration.Telemetry, prompts: append([]PromptDeclaration(nil), declaration.Prompts...), resources: append([]ResourceDeclaration(nil), declaration.Resources...)}, nil
}

// Name returns the declared application name.
func (application *Application) Name() string { return application.name }

// RootCount returns model-visible factories without calling them.
func (application *Application) RootCount() int { return len(application.roots) }

// PromptRootCount returns user-controlled factories without calling them.
func (application *Application) PromptRootCount() int { return len(application.promptRoots) }

// Telemetry returns the optional collector declared for compiled Host surfaces.
func (application *Application) Telemetry() Telemetry { return application.telemetry }

// Prompts returns publication declarations in declaration order.
func (application *Application) Prompts() []PromptDeclaration {
	return append([]PromptDeclaration(nil), application.prompts...)
}

// Resources returns publication declarations in declaration order.
func (application *Application) Resources() []ResourceDeclaration {
	return append([]ResourceDeclaration(nil), application.resources...)
}
