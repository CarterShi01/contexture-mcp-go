// Package mcpinterface declares Contexture's SDK-free MCP primitive facts.
package mcpinterface

// PromptDeclaration publishes person-controlled navigation to one node.
type PromptDeclaration struct {
	Name        string
	Opens       string
	Description string
}
