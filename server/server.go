package server

import (
	"context"
	"encoding/json"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/surface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Identity names one Contexture MCP server implementation.
type Identity struct {
	Name    string
	Version string
}

// NewMCPServer constructs the official MCP adapter without registering capabilities.
//
// Keeping the SDK dependency in this package enforces an SDK-neutral authoring
// core.
func NewMCPServer(identity Identity) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    identity.Name,
		Version: identity.Version,
	}, nil)
}

// ContextureMCPServer is an official-SDK adapter with a fixed gateway surface.
type ContextureMCPServer struct {
	Server       *mcp.Server
	Gateway      *contexture.Gateway
	GatewayNames []contexture.GatewayName
}

// NewContextureMCPServer registers only the fixed Contexture gateway.
// Business Tools are payload cards, never MCP top-level tools.
func NewContextureMCPServer(identity Identity, gateway *contexture.Gateway, publications ...*surface.Publications) *ContextureMCPServer {
	return NewContextureMCPServerForRoots(identity, gateway, contexture.AllRoots(), publications...)
}

// NewContextureMCPServerForRoots registers one fixed root projection. It is
// useful for stdio and single-tenant hosts; request selectors resolve a
// projection before constructing their transport-specific adapter.
func NewContextureMCPServerForRoots(identity Identity, gateway *contexture.Gateway, selection contexture.RootSelection, publications ...*surface.Publications) *ContextureMCPServer {
	server := NewMCPServer(identity)
	for _, tool := range gateway.Tools() {
		registerGatewayTool(server, gateway, tool, selection)
	}
	if len(publications) > 0 && publications[0] != nil {
		registerPublications(server, publications[0], selection)
	}
	names := make([]contexture.GatewayName, 0, len(gateway.Tools()))
	for _, tool := range gateway.Tools() {
		names = append(names, tool.Name)
	}
	return &ContextureMCPServer{Server: server, Gateway: gateway, GatewayNames: names}
}

func registerGatewayTool(server *mcp.Server, gateway *contexture.Gateway, tool contexture.GatewayTool, selection contexture.RootSelection) {
	definition := &mcp.Tool{Name: string(tool.Name), Description: tool.Description, InputSchema: gatewaySchema(tool.Name), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: tool.ReadOnly}}
	server.AddTool(definition, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		value, err := callGateway(ctx, gateway, tool.Name, request.Params.Arguments, selection)
		if err != nil {
			return toolFailure(err), nil
		}
		return toolSuccess(value), nil
	})
}

func gatewaySchema(name contexture.GatewayName) map[string]any {
	if name == contexture.DiscoverGatewayName {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	properties := map[string]any{"ref": map[string]any{"type": "string"}}
	if name == contexture.InvokeGatewayName || name == contexture.InvokeReadOnlyGatewayName {
		properties["arguments"] = map[string]any{"anyOf": []any{map[string]any{"type": "object", "additionalProperties": true}, map[string]any{"type": "null"}}, "default": nil}
	}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"ref"}}
}

func callGateway(ctx context.Context, gateway *contexture.Gateway, name contexture.GatewayName, raw json.RawMessage, selection contexture.RootSelection) (any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var input struct {
		Ref       string          `json:"ref"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch name {
	case contexture.DiscoverGatewayName:
		return gateway.Discover(selection)
	case contexture.OpenGatewayName:
		return gateway.Open(input.Ref, selection)
	case contexture.InvokeReadOnlyGatewayName:
		return gateway.InvokeReadOnly(ctx, input.Ref, input.Arguments, selection)
	case contexture.InvokeGatewayName:
		return gateway.Invoke(ctx, input.Ref, input.Arguments, selection)
	}
	return nil, nil
}

func toolSuccess(value any) *mcp.CallToolResult {
	raw, err := json.Marshal(value)
	if err != nil {
		return toolFailure(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: value}
}

func toolFailure(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}
}

func registerPublications(server *mcp.Server, publications *surface.Publications, selection contexture.RootSelection) {
	prompts, err := publications.PromptCards(selection)
	if err == nil {
		for _, card := range prompts {
			card := card
			arguments := make([]*mcp.PromptArgument, 0, len(card.Arguments))
			for _, argument := range card.Arguments {
				arguments = append(arguments, &mcp.PromptArgument{Name: argument.Name, Required: argument.Required})
			}
			server.AddPrompt(&mcp.Prompt{Name: card.Name, Description: card.Description, Arguments: arguments}, func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				var text string
				var callErr error
				if card.Name == "goto" {
					text, callErr = publications.Goto(request.Params.Arguments["ref"], selection)
				} else {
					text, callErr = publications.Command(card.Name, selection)
				}
				if callErr != nil {
					return nil, callErr
				}
				return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
			})
		}
	}
	for _, card := range publications.ResourceCards(selection) {
		card := card
		server.AddResource(&mcp.Resource{Name: card.Name, URI: card.URI, Description: card.Description, MIMEType: card.MIMEType}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			value, callErr := publications.Read(ctx, card.URI, selection)
			if callErr != nil {
				return nil, callErr
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: card.URI, MIMEType: card.MIMEType, Text: stringify(value)}}}, nil
		})
	}
}

func stringify(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}
