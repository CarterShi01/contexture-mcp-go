package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// ExecutionAPI is the model-facing invocation half of Contexture's fixed
// gateway. It owns no transport and does not discover or open declarations:
// it delegates validated calls to one bound Runtime while turning ordinary
// lookup and wrong-door facts into the agent-facing RefusedError boundary.
//
// Prompt roots are person-controlled entry points. They are deliberately
// refused here after the request's root ceiling has been checked, so a caller
// cannot use invocation to bypass progressive model navigation.
type ExecutionAPI struct {
	runtime     *Runtime
	promptRoots map[string]struct{}
}

// NewExecutionAPI constructs the independently usable execution half over a
// bound Runtime. Runtime is the native Go carrier for the compiled Index,
// selected roots, request context facts, and telemetry exporter.
func NewExecutionAPI(runtime *Runtime) (*ExecutionAPI, error) {
	if runtime == nil || runtime.index == nil || !runtime.index.bound {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("ExecutionAPI requires a bound Runtime"))
	}
	promptRoots := make(map[string]struct{}, len(runtime.index.promptRoots))
	for _, node := range runtime.index.PromptRoots() {
		ref, err := runtime.index.RefOf(node)
		if err != nil {
			return nil, fmt.Errorf("resolve ExecutionAPI prompt root: %w", err)
		}
		promptRoots[ref] = struct{}{}
	}
	return &ExecutionAPI{runtime: runtime, promptRoots: promptRoots}, nil
}

// Tools returns the immutable ordered pair of fixed invocation doors.
func (*ExecutionAPI) Tools() []GatewayTool { return ExecutionGatewayTools() }

// InvokeReadOnly runs a read-only Tool through the only read-only model door.
func (api *ExecutionAPI) InvokeReadOnly(ctx context.Context, ref string, arguments json.RawMessage, requested RootSelection) (any, error) {
	return api.invoke(ctx, ref, arguments, true, requested)
}

// Invoke runs a writing Tool through the only writing model door.
func (api *ExecutionAPI) Invoke(ctx context.Context, ref string, arguments json.RawMessage, requested RootSelection) (any, error) {
	return api.invoke(ctx, ref, arguments, false, requested)
}

// ReadForHost reads a published, no-argument Resource target as its Host. It
// shares Runtime validation, request context, and telemetry with model calls,
// but it does not apply the model-only Prompt-root door.
func (api *ExecutionAPI) ReadForHost(ctx context.Context, ref string, requested RootSelection) (any, error) {
	if api == nil || api.runtime == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("ExecutionAPI must not be nil"))
	}
	value, err := api.runtime.InvokeReadOnly(ctx, ref, json.RawMessage("{}"), requested)
	return value, recoverHostReadError(err)
}

// ReadForAHost is the retained Go compatibility spelling for ReadForHost.
func (api *ExecutionAPI) ReadForAHost(ctx context.Context, ref string, requested RootSelection) (any, error) {
	return api.ReadForHost(ctx, ref, requested)
}

func (api *ExecutionAPI) invoke(ctx context.Context, ref string, arguments json.RawMessage, readOnly bool, requested RootSelection) (any, error) {
	if api == nil || api.runtime == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("ExecutionAPI must not be nil"))
	}
	// Authorization comes first. In particular, an excluded Prompt root must
	// remain a typed RootOutsideSelectionError rather than reveal its person
	// ownership through a refusal sentence.
	selection, err := api.runtime.effectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	root := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
	if _, prompt := api.promptRoots[root]; prompt {
		return nil, &RefusedError{Message: TakenByPersonMessage(ref)}
	}
	if readOnly {
		value, err := api.runtime.InvokeReadOnly(ctx, ref, arguments, requested)
		return value, recoverExecutionError(err)
	}
	value, err := api.runtime.Invoke(ctx, ref, arguments, requested)
	return value, recoverExecutionError(err)
}

// recoverExecutionError converts only facts an agent can correct through the
// fixed execution surface. A root-selection refusal is an authorization fact,
// not a navigation hint, and must remain typed and non-leaking.
func recoverExecutionError(err error) error {
	if err == nil {
		return nil
	}
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

// recoverHostReadError follows the resource-door contract: publication
// validation has already established that a host resource points to a
// no-argument read-only Tool. Lookup failure is still rendered for a Host that
// holds a stale address, while every other Runtime fact remains direct for the
// Host to diagnose.
func recoverHostReadError(err error) error {
	if err == nil {
		return nil
	}
	var failure *NodeNotFoundError
	if errors.As(err, &failure) {
		return &RefusedError{Message: UnresolvedMessage(failure), Cause: err}
	}
	return err
}
