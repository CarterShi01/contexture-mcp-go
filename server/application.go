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
}

// DisclosureApplication is an independently compiled, non-executable Host
// container with one unbound Index and navigation-only MCP surface.
type DisclosureApplication struct {
	Application  *contexture.Application
	Index        *contexture.Index
	Disclosure   *contexture.Disclosure
	Telemetry    contexture.Telemetry
	Publications *surface.Publications
}

// CompileApplication compiles the declaration once for every execution-capable surface.
func CompileApplication(application *contexture.Application) (*RuntimeApplication, error) {
	if application == nil {
		return nil, fmt.Errorf("Contexture application must not be nil")
	}
	index, err := contexture.Compile(application)
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
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), telemetry)
	if err != nil {
		return nil, err
	}
	publications, err := surface.NewPublications(application, disclosure, runtime)
	if err != nil {
		return nil, err
	}
	return &RuntimeApplication{Application: application, Index: index, Disclosure: disclosure, Runtime: runtime, Telemetry: telemetry, Publications: publications}, nil
}

// CompileDisclosureApplication compiles one structural surface. Channels and
// Resources are rejected by the unbound compiler rather than ignored.
func CompileDisclosureApplication(application *contexture.Application) (*DisclosureApplication, error) {
	if application == nil {
		return nil, fmt.Errorf("Contexture application must not be nil")
	}
	index, err := contexture.CompileDisclosure(application)
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
	publications, err := surface.NewPublications(application, disclosure, nil)
	if err != nil {
		return nil, err
	}
	return &DisclosureApplication{Application: application, Index: index, Disclosure: disclosure, Telemetry: telemetry, Publications: publications}, nil
}

// Server builds an official navigation-only MCP adapter over this exact container.
func (application *DisclosureApplication) Server() (*ContextureMCPServer, error) {
	if application == nil || application.Disclosure == nil || application.Publications == nil {
		return nil, fmt.Errorf("Contexture disclosure application is incomplete")
	}
	gateway, err := contexture.NewGateway(application.Disclosure, nil)
	if err != nil {
		return nil, err
	}
	return NewContextureMCPServer(Identity{Name: application.Index.Name(), Version: contexture.Version}, gateway, application.Publications), nil
}

// Gateway returns the fixed Contexture gateway over this application's runtime projections.
func (application *RuntimeApplication) Gateway() (*contexture.Gateway, error) {
	if application == nil || application.Disclosure == nil || application.Runtime == nil {
		return nil, fmt.Errorf("Contexture runtime application is incomplete")
	}
	return contexture.NewGateway(application.Disclosure, application.Runtime)
}
