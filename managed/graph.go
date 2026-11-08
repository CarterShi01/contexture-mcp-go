package managed

import "github.com/CarterShi01/contexture-mcp-go/core/graph"

const NativeABI = graph.ManagedABI

type GraphDefinition = graph.GraphDefinition
type GraphIndex = graph.GraphIndex
type RevisionState = graph.RevisionState
type BindingDescriptor = graph.BindingDescriptor
type Portability = graph.Portability
type PublicationContract = graph.PublicationContract
type FrozenJSON = graph.FrozenJSON
type Identity = graph.ManagedIdentity
type CallContext = graph.ManagedCallContext
type Schema = graph.ManagedSchema
type BindingRegistrationOptions[I any] = graph.BindingRegistrationOptions[I]
type BindingRegistration = graph.BindingRegistration
type ResolvedBinding = graph.ResolvedBinding
type RegistryGeneration = graph.RegistryGeneration
type ExecutionManifest = graph.ExecutionManifest
type ExecutionManifestOptions = graph.ExecutionManifestOptions
type CompilerOptions = graph.ManagedCompilerOptions
type StructuralPlan = graph.StructuralPlan
type ManagedReference = graph.ManagedReference
type CompiledPresentation = graph.CompiledPresentation
type CompiledProcesses = graph.CompiledProcesses
type ResolvedInstruction = graph.ResolvedInstruction
type TemplatePart = graph.TemplatePart
type NodeSpec = graph.NodeSpec
type RootEntry = graph.RootEntry
type GraphError = graph.GraphError
type ReleaseRequest = graph.ReleaseRequest
type ReleaseState = graph.ReleaseState

func ParseReleaseRequest(value any) (ReleaseRequest, error) { return graph.ParseReleaseRequest(value) }
func ParseReleaseReceipt(value any) (FrozenJSON, error)     { return graph.ParseReleaseReceipt(value) }
func EmptyReleaseState() ReleaseState                       { return graph.EmptyReleaseState() }
func ImportReleaseState(value any) (ReleaseState, error)    { return graph.ImportReleaseState(value) }
func ReduceReleaseEvent(state ReleaseState, event, record any) (ReleaseState, error) {
	return graph.ReduceReleaseEvent(state, event, record)
}
func ReplayReleases(records []map[string]any) (ReleaseState, error) {
	return graph.ReplayReleases(records)
}

func NewGraphDefinition(value any, contracts map[string]PublicationContract) (*GraphDefinition, error) {
	return graph.NewGraphDefinition(value, contracts)
}
func NewRevisionState(definition *GraphDefinition, revision string, tombstones any) (RevisionState, error) {
	return graph.NewPureRevisionState(definition, revision, tombstones)
}
func NewRegistryGeneration(build string, registrations ...*BindingRegistration) (*RegistryGeneration, error) {
	return graph.NewRegistryGeneration(build, registrations...)
}
func NewExecutionManifest(options ExecutionManifestOptions) (*ExecutionManifest, error) {
	return graph.NewExecutionManifest(options)
}
func ParseExecutionManifest(value any) (*ExecutionManifest, error) {
	return graph.ParseExecutionManifest(value)
}
func ParseBindingDescriptor(value any) (BindingDescriptor, error) {
	return graph.ParseBindingDescriptor(value)
}
func CompileSchema(value any) (Schema, error) { return graph.CompileManagedSchema(value) }
func Compile(definition *GraphDefinition, options CompilerOptions) (*StructuralPlan, error) {
	return graph.CompileManaged(definition, options)
}
func FreezeJSON(value any) (FrozenJSON, error)              { return graph.FreezeJSON(value) }
func DecodeJSON(value []byte) (any, error)                  { return graph.DecodeJSON(value) }
func ParseReference(value string) (ManagedReference, error) { return graph.ParseReference(value) }
func FormatReference(graphID, generationUUID, nodeID string) (string, error) {
	return graph.FormatReference(graphID, generationUUID, nodeID)
}

func NewBindingRegistration[I any](options BindingRegistrationOptions[I]) (*BindingRegistration, error) {
	return graph.NewBindingRegistration(options)
}
