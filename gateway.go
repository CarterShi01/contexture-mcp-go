package contexture

import (
	"context"
	"encoding/json"
	"errors"
)

// GatewayName identifies one immutable Contexture system tool.
type GatewayName string

const (
	DiscoverGatewayName       GatewayName = "contexture_discover"
	OpenGatewayName           GatewayName = "contexture_open"
	InvokeReadOnlyGatewayName GatewayName = "contexture_invoke_read_only"
	InvokeGatewayName         GatewayName = "contexture_invoke"
)

// GatewayTool is one fixed model-controlled Contexture system tool.
type GatewayTool struct {
	Name        GatewayName
	Description string
	ReadOnly    bool
}

var gatewayTools = []GatewayTool{
	{Name: DiscoverGatewayName, ReadOnly: true, Description: "List the top-level capabilities this server serves, as short routing cards. Most are roles: open the one that matches the task; its sub-roles arrive with it, one level at a time, so a large tree costs only the branch you enter. A role card is a name, a sentence, and the ref that opens it — instructions and what a role holds arrive on opening, never here."},
	{Name: OpenGatewayName, ReadOnly: true, Description: "Open one role, skill or tool by ref. Opening a role returns its instructions and a card for every skill, tool and sub-role it holds, each with the ref that opens it and each tool with the schema needed to call it. Opening a skill returns its complete procedure, available here and nowhere else. A tool's card is already complete, so run the tool rather than opening it. Pass a ref taken from a card; never assemble one."},
	{Name: InvokeReadOnlyGatewayName, ReadOnly: true, Description: "Run a tool that leaves the world unchanged. Use this for every tool whose card says read_only: true. The ref and arguments come from that card. A tool that is not read-only is refused here."},
	{Name: InvokeGatewayName, ReadOnly: false, Description: "Run a tool that changes something. Use this for every tool whose card says read_only: false. The ref and arguments come from that card. A read-only tool is refused here, so that a host can tell the two apart before a human is asked to approve anything."},
}

// Gateway is the transport-neutral fixed Contexture model plane.
type Gateway struct {
	disclosure *Disclosure
	runtime    *Runtime
}

// NewGateway connects navigation to an optional executable Runtime.
func NewGateway(disclosure *Disclosure, runtime *Runtime) (*Gateway, error) {
	if disclosure == nil {
		return nil, errors.New("Contexture Disclosure must not be nil")
	}
	return &Gateway{disclosure: disclosure, runtime: runtime}, nil
}

// Tools returns the fixed navigation gateway, plus invoke doors when executable.
func (gateway *Gateway) Tools() []GatewayTool {
	limit := 2
	if gateway.runtime != nil {
		limit = len(gatewayTools)
	}
	return append([]GatewayTool(nil), gatewayTools[:limit]...)
}

// Discover lists selected model-controlled root cards.
func (gateway *Gateway) Discover(selection RootSelection) (map[string][]map[string]any, error) {
	return gateway.disclosure.Discover(selection)
}

// Open progressively discloses one selected model-controlled node.
func (gateway *Gateway) Open(ref string, selection RootSelection) (map[string]any, error) {
	return gateway.disclosure.Open(ref, selection)
}

// InvokeReadOnly runs a read-only Tool through the fixed read-only door.
func (gateway *Gateway) InvokeReadOnly(ctx context.Context, ref string, arguments json.RawMessage, selection RootSelection) (any, error) {
	if gateway.runtime == nil {
		return nil, errors.New("This Contexture server is disclosure-only.")
	}
	return gateway.runtime.InvokeReadOnly(ctx, ref, arguments, selection)
}

// Invoke runs a writing Tool through the fixed writing door.
func (gateway *Gateway) Invoke(ctx context.Context, ref string, arguments json.RawMessage, selection RootSelection) (any, error) {
	if gateway.runtime == nil {
		return nil, errors.New("This Contexture server is disclosure-only.")
	}
	return gateway.runtime.Invoke(ctx, ref, arguments, selection)
}
