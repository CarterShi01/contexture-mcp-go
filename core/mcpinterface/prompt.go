// Package mcpinterface declares Contexture's SDK-free MCP primitive facts.
package mcpinterface

import "github.com/CarterShi01/contexture-mcp-go/core/foundation"

// ModelOpenPolicy states whether a model may navigate to a Prompt's target.
// The zero value permits model navigation, matching the ordinary declaration.
type ModelOpenPolicy = foundation.ModelOpenPolicy

const (
	// ModelMayOpen leaves the target reachable through both model and person doors.
	ModelMayOpen = foundation.ModelMayOpen
	// ModelReservedForPerson keeps the target card visible but reserves opening it
	// for the named Prompt or the fixed person-controlled goto entry.
	ModelReservedForPerson = foundation.ModelReservedForPerson
)

// PromptDeclaration publishes person-controlled navigation to one node.
type PromptDeclaration = foundation.PromptDeclaration
