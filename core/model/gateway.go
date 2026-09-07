package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/mcpinterface"
)

// RefusedError is a completed, agent-facing recovery instruction.  It is used
// only at the fixed gateway boundary: lower layers retain typed lookup facts
// for Hosts, while an agent receives the next action it can take.
type RefusedError struct {
	Message string
	Cause   error
}

func (err *RefusedError) Error() string {
	if err == nil {
		return "Contexture request refused"
	}
	return err.Message
}

func (err *RefusedError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// GatewayName identifies one immutable Contexture system Tool.
type GatewayName = mcpinterface.GatewayName

const (
	DiscoverGatewayName       = mcpinterface.DiscoverGatewayName
	OpenGatewayName           = mcpinterface.OpenGatewayName
	InvokeReadOnlyGatewayName = mcpinterface.InvokeReadOnlyGatewayName
	InvokeGatewayName         = mcpinterface.InvokeGatewayName
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

// GatewayTools returns the complete immutable four-tool system surface in
// registration order. Business tools are deliberately never entries here.
func GatewayTools() []GatewayTool { return append([]GatewayTool(nil), gatewayTools...) }

// DisclosureGatewayTools returns the independently installable navigation half.
func DisclosureGatewayTools() []GatewayTool { return append([]GatewayTool(nil), gatewayTools[:2]...) }

// ExecutionGatewayTools returns the two fixed invocation doors.
func ExecutionGatewayTools() []GatewayTool { return append([]GatewayTool(nil), gatewayTools[2:]...) }

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
	result, err := gateway.disclosure.Discover(selection)
	return result, gateway.recover(err)
}

// Open progressively discloses one selected model-controlled node.
func (gateway *Gateway) Open(ref string, selection RootSelection) (map[string]any, error) {
	result, err := gateway.disclosure.Open(ref, selection)
	return result, gateway.recover(err)
}

// InvokeReadOnly runs a read-only Tool through the fixed read-only door.
func (gateway *Gateway) InvokeReadOnly(ctx context.Context, ref string, arguments json.RawMessage, selection RootSelection) (any, error) {
	if gateway.runtime == nil {
		return nil, &RefusedError{Message: "This Contexture server is disclosure-only. Call contexture_discover or contexture_open instead."}
	}
	result, err := gateway.runtime.InvokeReadOnly(ctx, ref, arguments, selection)
	return result, gateway.recover(err)
}

// Invoke runs a writing Tool through the fixed writing door.
func (gateway *Gateway) Invoke(ctx context.Context, ref string, arguments json.RawMessage, selection RootSelection) (any, error) {
	if gateway.runtime == nil {
		return nil, &RefusedError{Message: "This Contexture server is disclosure-only. Call contexture_discover or contexture_open instead."}
	}
	result, err := gateway.runtime.Invoke(ctx, ref, arguments, selection)
	return result, gateway.recover(err)
}

func (gateway *Gateway) recover(err error) error {
	if err == nil {
		return nil
	}
	// A root ceiling is an authorization boundary. It must remain distinguishable
	// and must not disclose an alternative root or recovery route.
	var outside *RootOutsideSelectionError
	if errors.As(err, &outside) {
		return err
	}
	var wrong *WrongDoorError
	if errors.As(err, &wrong) {
		return &RefusedError{Message: WrongDoorMessage(wrong.Ref, wrong.ReadOnly), Cause: err}
	}
	var failure *NodeNotFoundError
	if errors.As(err, &failure) {
		return &RefusedError{Message: UnresolvedMessage(failure), Cause: err}
	}
	return err
}

// UnresolvedMessage renders a typed lookup failure as a fixed gateway next
// action. It intentionally does not see selection failures.
func UnresolvedMessage(failure *NodeNotFoundError) string {
	if failure == nil {
		return "A reference could not be resolved. Call contexture_discover for the roles this server serves."
	}
	known := strings.Join(failure.Known, ", ")
	switch failure.Reason {
	case EmptyRef:
		return "A reference must name at least a root role. Call contexture_discover for the roles this server serves."
	case NoSuchRoot:
		return fmt.Sprintf("No root role named '%s'. This server serves: %s. Call contexture_discover for their cards, then open one to reach what is beneath it.", failure.Scope, known)
	case NotAContainer:
		return fmt.Sprintf("Reference '%s' continues past '%s', which is a %s and holds nothing. Open '%s' itself with contexture_open, or go back to the card the ref came from.", failure.Ref, failure.Scope, failure.Kind, failure.Scope)
	case NoSuchMember:
		holds := "It holds nothing."
		if known != "" {
			holds = "It holds: " + known + "."
		}
		return fmt.Sprintf("Role '%s' holds no member named '%s'. %s Call contexture_open on '%s' to see each member with the ref that opens it.", failure.Scope, failure.Segment, holds, failure.Scope)
	case WrongKind:
		recovery := "Open it with contexture_open."
		if failure.Kind == string(ToolKind) {
			recovery = "Run it with contexture_invoke_read_only or contexture_invoke, whichever its card says."
		}
		found, wanted := failure.Kind, failure.Wanted
		if found == "" {
			found = "node"
		}
		if wanted == "" {
			wanted = "requested kind"
		}
		return fmt.Sprintf("%s names a %s, not a %s. %s", failure.Ref, found, wanted, recovery)
	default:
		return fmt.Sprintf("%q could not be resolved. Call contexture_discover for the roles this server serves.", failure.Ref)
	}
}

// WrongDoorMessage identifies the one safe fixed invocation entry point.
func WrongDoorMessage(ref string, isReadOnly bool) string {
	correct, stated := "contexture_invoke", "not read-only"
	if isReadOnly {
		correct, stated = "contexture_invoke_read_only", "read-only"
	}
	return fmt.Sprintf("%s is %s, so it must be run through %s.", ref, stated, correct)
}

// TakenByPersonMessage explains a model-open reservation without suggesting a
// workaround. Hosts should apply selection authorization before using it.
func TakenByPersonMessage(ref string) string {
	return fmt.Sprintf("%s is opened by a person, not by an agent. It is reachable only as a command in this host's menu. Do not reproduce its steps another way; tell the user which command runs it and let them decide when.", ref)
}
