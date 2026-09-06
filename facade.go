package contexture

import (
	"context"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
	"github.com/CarterShi01/contexture-mcp-go/core/model"
)

type (
	Factory                = model.Factory
	Node                   = model.Node
	Kind                   = model.Kind
	Role                   = model.Role
	Skill                  = model.Skill
	Tool                   = model.Tool
	Application            = model.Application
	ApplicationDeclaration = model.ApplicationDeclaration
	// Prompt is the native data declaration for one person-controlled MCP prompt.
	// PromptDeclaration remains an equivalent explicit spelling for compatibility.
	Prompt            = model.PromptDeclaration
	PromptDeclaration = model.PromptDeclaration
	// Resource is the native data declaration for one host-readable MCP resource.
	// ResourceDeclaration remains an equivalent explicit spelling for compatibility.
	Resource            = model.ResourceDeclaration
	ResourceDeclaration = model.ResourceDeclaration
	Binding             = model.Binding
	CleanupRegistrar    = model.CleanupRegistrar
	Channels            = model.Channels
	Index               = model.Index
	RootSelection       = model.RootSelection
	Disclosure          = model.Disclosure
	GatewayName         = model.GatewayName
	GatewayTool         = model.GatewayTool
	Gateway             = model.Gateway
	Telemetry           = model.Telemetry
	CallEvent           = model.CallEvent
	MemoryTelemetry     = model.MemoryTelemetry
	Runtime             = model.Runtime
	SelectedGraph       = model.SelectedGraph
	Principal           = foundation.Principal
	PrincipalOptions    = foundation.PrincipalOptions
)

const (
	RoleKind  = model.RoleKind
	SkillKind = model.SkillKind
	ToolKind  = model.ToolKind

	DiscoverGatewayName       = model.DiscoverGatewayName
	OpenGatewayName           = model.OpenGatewayName
	InvokeReadOnlyGatewayName = model.InvokeReadOnlyGatewayName
	InvokeGatewayName         = model.InvokeGatewayName
)

// DeclareApplication validates a lazy Contexture composition root.
func DeclareApplication(declaration ApplicationDeclaration) (*Application, error) {
	return model.DeclareApplication(declaration)
}

// Contexture declares one lazy application through the public concept name
// used by the reference binding. Go callers may continue to use
// DeclareApplication when its error-returning operation is clearer at call sites.
func Contexture(declaration ApplicationDeclaration) (*Application, error) {
	return DeclareApplication(declaration)
}

// Compile builds one fresh canonical forest from a lazy Application.
func Compile(application *Application) (*Index, error) {
	return model.Compile(application)
}

// CompileDisclosure builds an independent navigation-only Index.
func CompileDisclosure(application *Application) (*Index, error) {
	return model.CompileDisclosure(application)
}

// NewDisclosure creates a model navigation view.
func NewDisclosure(index *Index, selection RootSelection) (*Disclosure, error) {
	return model.NewDisclosure(index, selection)
}

// NewDisclosureOnly creates an unbound navigation-only view.
func NewDisclosureOnly(index *Index, selection RootSelection) (*Disclosure, error) {
	return model.NewDisclosureOnly(index, selection)
}

// NewRuntime creates executable Tool dispatch over one bound Index.
func NewRuntime(index *Index, selection, ceiling RootSelection, telemetry Telemetry) (*Runtime, error) {
	return model.NewRuntime(index, selection, ceiling, telemetry)
}

// NewGateway connects progressive disclosure to an optional Runtime.
func NewGateway(disclosure *Disclosure, runtime *Runtime) (*Gateway, error) {
	return model.NewGateway(disclosure, runtime)
}

// AllRoots returns the projection containing every root.
func AllRoots() RootSelection {
	return model.AllRoots()
}

// OnlyRoots constructs an exact non-empty root selection.
func OnlyRoots(names ...string) (RootSelection, error) {
	return model.OnlyRoots(names...)
}

// NewMemoryTelemetry creates a bounded non-blocking telemetry sink.
func NewMemoryTelemetry() *MemoryTelemetry {
	return model.NewMemoryTelemetry()
}

// NewPrincipal snapshots Host-supplied identity facts for one request.
func NewPrincipal(options PrincipalOptions) *Principal {
	return foundation.NewPrincipal(options)
}

// WithPrincipal associates one principal with a request context.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return model.WithPrincipal(ctx, principal)
}

// CurrentPrincipal returns the request principal.
func CurrentPrincipal(ctx context.Context) *Principal {
	return model.CurrentPrincipal(ctx)
}

// CurrentGraph returns the request-local selected graph.
func CurrentGraph(ctx context.Context) *SelectedGraph {
	return model.CurrentGraph(ctx)
}

// CurrentSelection returns the effective request root selection.
func CurrentSelection(ctx context.Context) RootSelection {
	return model.CurrentSelection(ctx)
}

// CurrentTelemetry returns the request-local telemetry exporter.
func CurrentTelemetry(ctx context.Context) Telemetry {
	return model.CurrentTelemetry(ctx)
}

// WithChannels owns dependency lifecycle around one served operation.
func WithChannels[T any](ctx context.Context, channels Channels, serve func(context.Context) (T, error)) (T, error) {
	return model.WithChannels(ctx, channels, serve)
}

// NewTool couples one tagged input struct, schema, decoder, and handler.
func NewTool[I any, O any](name, description string, readOnly bool, handler func(context.Context, I) (O, error)) (*Tool, error) {
	return model.NewTool(name, description, readOnly, handler)
}

// NewToolWithSchema couples one explicit schema to a typed Tool handler.
func NewToolWithSchema[I any, O any](name, description string, readOnly bool, inputSchema map[string]any, handler func(context.Context, I) (O, error)) (*Tool, error) {
	return model.NewToolWithSchema(name, description, readOnly, inputSchema, handler)
}
