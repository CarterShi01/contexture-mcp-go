package server

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Identity names one Contexture MCP server implementation.
type Identity struct {
	Name    string
	Version string
}

// NewMCPServer constructs the official MCP adapter without registering capabilities.
//
// Gateway registration belongs to the upcoming compilation layer. Keeping the
// SDK dependency in this package enforces an SDK-neutral authoring core.
func NewMCPServer(identity Identity) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    identity.Name,
		Version: identity.Version,
	}, nil)
}
