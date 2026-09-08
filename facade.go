package contexture

import (
	"context"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
	"github.com/CarterShi01/contexture-mcp-go/core/model"
)

type (
	Factory                = model.Factory
	RoleFactory            = model.RoleFactory
	SkillFactory           = model.SkillFactory
	ToolFactory            = model.ToolFactory
	ControllerManager      = model.ControllerManager
	Node                   = model.Node
	Kind                   = model.Kind
	CompileLevel           = model.CompileLevel
	CompiledContext        = model.CompiledContext
	View                   = model.View
	Role                   = model.Role
	Skill                  = model.Skill
	Tool                   = model.Tool
	Application            = model.Application
	ApplicationDeclaration = model.ApplicationDeclaration
	// Prompt is the native data declaration for one person-controlled MCP prompt.
	// PromptDeclaration remains an equivalent explicit spelling for compatibility.
	Prompt            = model.PromptDeclaration
	PromptDeclaration = model.PromptDeclaration
	ModelOpenPolicy   = model.ModelOpenPolicy
	// Resource is the native data declaration for one host-readable MCP resource.
	// ResourceDeclaration remains an equivalent explicit spelling for compatibility.
	Resource                  = model.ResourceDeclaration
	ResourceDeclaration       = model.ResourceDeclaration
	Binding                   = model.Binding
	CleanupRegistrar          = model.CleanupRegistrar
	Channels                  = model.Channels
	Index                     = model.Index
	RootSelection             = model.RootSelection
	RootSelectionError        = model.RootSelectionError
	RootOutsideSelectionError = model.RootOutsideSelectionError
	Disclosure                = model.Disclosure
	DisclosureAPI             = model.DisclosureAPI
	GatewayName               = model.GatewayName
	GatewayTool               = model.GatewayTool
	Gateway                   = model.Gateway
	ExecutionAPI              = model.ExecutionAPI
	RefusedError              = model.RefusedError
	WrongDoorError            = model.WrongDoorError
	Telemetry                 = model.Telemetry
	CallEvent                 = model.CallEvent
	NodeUsage                 = model.NodeUsage
	MemoryTelemetry           = model.MemoryTelemetry
	Runtime                   = model.Runtime
	SelectedGraph             = model.SelectedGraph
	NodeRef                   = model.NodeRef
	SignpostLevel             = model.SignpostLevel
	ReferenceCrossing         = model.ReferenceCrossing
	Principal                 = foundation.Principal
	PrincipalOptions          = foundation.PrincipalOptions
	LookupFailure             = foundation.LookupFailure
	NodeNotFoundError         = foundation.NodeNotFoundError
)

const (
	RoleKind  = model.RoleKind
	SkillKind = model.SkillKind
	ToolKind  = model.ToolKind

	RouteCompileLevel  = model.RouteCompileLevel
	ActiveCompileLevel = model.ActiveCompileLevel

	ModelMayOpen           = model.ModelMayOpen
	ModelReservedForPerson = model.ModelReservedForPerson

	EmptyRef      = foundation.EmptyRef
	NoSuchRoot    = foundation.NoSuchRoot
	NotAContainer = foundation.NotAContainer
	NoSuchMember  = foundation.NoSuchMember
	WrongKind     = foundation.WrongKind

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

// NewControllerManager creates the explicit imperative registration owner.
func NewControllerManager() *ControllerManager { return model.NewControllerManager() }

// NewControllerManagerWithChannels creates a manager for future Applications
// that share one lifecycle-scoped Channels implementation.
func NewControllerManagerWithChannels(channels Channels) *ControllerManager {
	return model.NewControllerManagerWithChannels(channels)
}

// RegisterRoot dispatches a generic root Factory by its concrete node kind.
func RegisterRoot(manager *ControllerManager, factory Factory) (Node, error) {
	return manager.RegisterRoot(factory)
}

// BranchesOf returns one compiled Node's direct Role branches. Leaf Nodes have
// no branches and return an empty defensive result.
func BranchesOf(node Node) ([]Node, error) { return model.BranchesOf(node) }

// MembersOf returns one compiled Node's direct containment members. Leaf Nodes
// have no members and return an empty defensive result.
func MembersOf(node Node) ([]Node, error) { return model.MembersOf(node) }

// RouteOf renders one Node's broad-routing facts.
func RouteOf(node Node) (CompiledContext, error) { return model.RouteOf(node) }

// CardOf renders one openable routing card through its owning View.
func CardOf(node Node, view View) (CompiledContext, error) { return model.CardOf(node, view) }

// GroupCards renders one closed Role/Skill/Tool sibling shape.
func GroupCards(nodes []Node, view View) (CompiledContext, error) {
	return model.GroupCards(nodes, view)
}

// CompileNode renders one Node at route or active disclosure level.
func CompileNode(node Node, level CompileLevel, view View) (CompiledContext, error) {
	return model.CompileNode(node, level, view)
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

// NewDisclosureWithTelemetry creates navigation over a shared usage collector.
func NewDisclosureWithTelemetry(index *Index, selection RootSelection, telemetry Telemetry) (*Disclosure, error) {
	return model.NewDisclosureWithTelemetry(index, selection, telemetry)
}

// NewDisclosureOnly creates an unbound navigation-only view.
func NewDisclosureOnly(index *Index, selection RootSelection) (*Disclosure, error) {
	return model.NewDisclosureOnly(index, selection)
}

// NewDisclosureAPI exposes the independently installable navigation half over
// one compiled Disclosure. Optional refs reserve model opening for a person;
// they remain available through OpenForPerson.
func NewDisclosureAPI(disclosure *Disclosure, reserved ...string) (*DisclosureAPI, error) {
	return model.NewDisclosureAPI(disclosure, reserved...)
}

// NewRuntime creates executable Tool dispatch over one bound Index.
func NewRuntime(index *Index, selection, ceiling RootSelection, telemetry Telemetry) (*Runtime, error) {
	return model.NewRuntime(index, selection, ceiling, telemetry)
}

// NewGateway connects progressive disclosure to an optional Runtime.
func NewGateway(disclosure *Disclosure, runtime *Runtime) (*Gateway, error) {
	return model.NewGateway(disclosure, runtime)
}

// NewExecutionAPI exposes the independently installable invocation half over
// a bound Runtime. It owns neither discovery nor a Host transport.
func NewExecutionAPI(runtime *Runtime) (*ExecutionAPI, error) {
	return model.NewExecutionAPI(runtime)
}

// GatewayTools returns the complete fixed system-tool inventory.
func GatewayTools() []GatewayTool { return model.GatewayTools() }

// DisclosureGatewayTools returns only discover and open.
func DisclosureGatewayTools() []GatewayTool { return model.DisclosureGatewayTools() }

// ExecutionGatewayTools returns only the two fixed invocation doors.
func ExecutionGatewayTools() []GatewayTool { return model.ExecutionGatewayTools() }

// UnresolvedMessage is the agent-facing recovery text for one typed lookup failure.
func UnresolvedMessage(failure *NodeNotFoundError) string { return model.UnresolvedMessage(failure) }

// WrongDoorMessage is the agent-facing recovery text for the other invocation door.
func WrongDoorMessage(ref string, isReadOnly bool) string {
	return model.WrongDoorMessage(ref, isReadOnly)
}

// TakenByPersonMessage is the fixed explanation for a person-reserved prompt target.
func TakenByPersonMessage(ref string) string { return model.TakenByPersonMessage(ref) }

// AllRoots returns the projection containing every root.
func AllRoots() RootSelection {
	return model.AllRoots()
}

// OnlyRoots constructs an exact non-empty root selection.
func OnlyRoots(names ...string) (RootSelection, error) {
	return model.OnlyRoots(names...)
}

// NewSelectedGraph creates a validated read-only root projection over Index.
func NewSelectedGraph(index *Index, selection RootSelection) (*SelectedGraph, error) {
	return model.NewSelectedGraph(index, selection)
}

// NewMemoryTelemetry creates a process-local aggregate collector.
func NewMemoryTelemetry() *MemoryTelemetry {
	return model.NewMemoryTelemetry()
}

// ReportTelemetry records one node use without allowing a telemetry exporter
// to change the caller's result. It is useful at a Host integration boundary;
// Runtime and Disclosure report their own framework observations automatically.
func ReportTelemetry(telemetry Telemetry, ref string, failed bool) {
	model.ReportTelemetry(telemetry, ref, failed)
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

// CurrentGraph returns the request-local selected graph, or nil outside a Tool invocation.
func CurrentGraph(ctx context.Context) *SelectedGraph {
	return model.CurrentGraph(ctx)
}

// WithGraph derives a nested request context carrying one selected graph.
// Runtime replaces it with the authoritative graph when invoking a Tool.
func WithGraph(ctx context.Context, graph *SelectedGraph) context.Context {
	return model.WithGraph(ctx, graph)
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
