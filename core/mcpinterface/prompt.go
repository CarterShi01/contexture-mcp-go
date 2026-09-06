// Package mcpinterface declares Contexture's SDK-free MCP primitive facts.
package mcpinterface

// ModelOpenPolicy states whether a model may navigate to a Prompt's target.
// The zero value permits model navigation, matching the ordinary declaration.
type ModelOpenPolicy uint8

const (
	// ModelMayOpen leaves the target reachable through both model and person doors.
	ModelMayOpen ModelOpenPolicy = iota
	// ModelReservedForPerson keeps the target card visible but reserves opening it
	// for the named Prompt or the fixed person-controlled goto entry.
	ModelReservedForPerson
)

// PromptDeclaration publishes person-controlled navigation to one node.
type PromptDeclaration struct {
	Name        string
	Opens       string
	Description string
	ModelOpen   ModelOpenPolicy
}

// AllowsModelOpen reports whether model navigation may open this target.
func (declaration PromptDeclaration) AllowsModelOpen() bool {
	return declaration.ModelOpen == ModelMayOpen
}
