package server

import (
	"fmt"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/surface"
)

// RuntimeApplication is every runtime projection derived from one lazy declaration.
// Its members share one bound immutable Index and are safe to use together.
type RuntimeApplication struct {
	Application  *contexture.Application
	Index        *contexture.Index
	Disclosure   *contexture.Disclosure
	Runtime      *contexture.Runtime
	Telemetry    contexture.Telemetry
	Publications *surface.Publications
	Presentation *contexture.Presentation
	Locale       string
}

// DisclosureApplication is an independently compiled, non-executable Host
// container with one unbound Index and navigation-only MCP surface.
type DisclosureApplication struct {
	Application  *contexture.Application
	Index        *contexture.Index
	Disclosure   *contexture.Disclosure
	Telemetry    contexture.Telemetry
	Publications *surface.Publications
	Presentation *contexture.Presentation
	Locale       string
}

// CompilationOptions fixes optional presentation locale independently of identity.
type CompilationOptions struct{ Locale *string }

// CompileApplication compiles the declaration once for every execution-capable surface.
func CompileApplication(application *contexture.Application) (*RuntimeApplication, error) {
	return CompileApplicationWithOptions(application, CompilationOptions{})
}

// CompileApplicationWithOptions resolves immutable copy on the same bound Index.
func CompileApplicationWithOptions(application *contexture.Application, options CompilationOptions) (*RuntimeApplication, error) {
	if application == nil {
		return nil, fmt.Errorf("Contexture application must not be nil")
	}
	index, err := contexture.Compile(application)
	if err != nil {
		return nil, err
	}
	locale, copy, err := contexture.ResolvePresentation(application.Presentation(), options.Locale)
	if err != nil {
		return nil, err
	}
	telemetry := application.Telemetry()
	if telemetry == nil {
		telemetry = contexture.NewMemoryTelemetry()
	}
	disclosure, err := contexture.NewDisclosureWithTelemetry(index, contexture.AllRoots(), telemetry)
	if err != nil {
		return nil, err
	}
	disclosure, err = disclosure.WithLocale(options.Locale)
	if err != nil {
		return nil, err
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), telemetry)
	if err != nil {
		return nil, err
	}
	publications, err := surface.NewPublications(application, disclosure, runtime)
	if err != nil {
		return nil, err
	}
	return &RuntimeApplication{Application: application, Index: index, Disclosure: disclosure, Runtime: runtime, Telemetry: telemetry, Publications: publications, Presentation: copy, Locale: locale}, nil
}

// CompileDisclosureApplication compiles one structural surface. Channels and
// Resources are rejected by the unbound compiler rather than ignored.
func CompileDisclosureApplication(application *contexture.Application) (*DisclosureApplication, error) {
	return CompileDisclosureApplicationWithOptions(application, CompilationOptions{})
}

// CompileDisclosureApplicationWithOptions resolves copy without binding execution.
func CompileDisclosureApplicationWithOptions(application *contexture.Application, options CompilationOptions) (*DisclosureApplication, error) {
	if application == nil {
		return nil, fmt.Errorf("Contexture application must not be nil")
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		return nil, err
	}
	locale, copy, err := contexture.ResolvePresentation(application.Presentation(), options.Locale)
	if err != nil {
		return nil, err
	}
	telemetry := application.Telemetry()
	if telemetry == nil {
		telemetry = contexture.NewMemoryTelemetry()
	}
	disclosure, err := contexture.NewDisclosureOnlyWithTelemetry(index, contexture.AllRoots(), telemetry)
	if err != nil {
		return nil, err
	}
	disclosure, err = disclosure.WithLocale(options.Locale)
	if err != nil {
		return nil, err
	}
	publications, err := surface.NewPublications(application, disclosure, nil)
	if err != nil {
		return nil, err
	}
	return &DisclosureApplication{Application: application, Index: index, Disclosure: disclosure, Telemetry: telemetry, Publications: publications, Presentation: copy, Locale: locale}, nil
}

// Server builds an official navigation-only MCP adapter over this exact container.
func (application *DisclosureApplication) Server() (*ContextureMCPServer, error) {
	if application == nil || application.Disclosure == nil || application.Publications == nil {
		return nil, fmt.Errorf("Contexture disclosure application is incomplete")
	}
	gateway, err := contexture.NewGateway(application.Disclosure, nil, reservedPromptRefs(application.Application)...)
	if err != nil {
		return nil, err
	}
	identity := Identity{Name: application.Index.Name(), Version: contexture.Version}
	var titles *contexture.GatewayPresentation
	if application.Presentation != nil {
		identity.Title = application.Presentation.Title()
		identity.Description = application.Presentation.Description()
		titles = application.Presentation.Gateway()
	}
	return NewContextureMCPServerWithPresentation(identity, gateway, contexture.AllRoots(), titles, application.Publications)
}

// Gateway returns the fixed Contexture gateway over this application's runtime projections.
func (application *RuntimeApplication) Gateway() (*contexture.Gateway, error) {
	if application == nil || application.Disclosure == nil || application.Runtime == nil {
		return nil, fmt.Errorf("Contexture runtime application is incomplete")
	}
	return contexture.NewGateway(application.Disclosure, application.Runtime, reservedPromptRefs(application.Application)...)
}

func reservedPromptRefs(application *contexture.Application) []string {
	if application == nil {
		return nil
	}
	refs := []string{}
	for _, prompt := range application.Prompts() {
		if !prompt.AllowsModelOpen() {
			refs = append(refs, prompt.Opens)
		}
	}
	return refs
}
