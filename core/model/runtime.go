package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

type contextKey int

const (
	principalKey contextKey = iota
	graphKey
	selectionKey
	telemetryKey
)

// Telemetry observes calls and never changes their outcome.
type Telemetry interface{ Record(CallEvent) error }

// CallEvent records one attempted Tool invocation.
type CallEvent struct {
	Ref    string
	Failed bool
}

// MemoryTelemetry is a race-safe test and inspection exporter.
type MemoryTelemetry struct{ events chan CallEvent }

// NewMemoryTelemetry creates a bounded non-blocking telemetry sink.
func NewMemoryTelemetry() *MemoryTelemetry {
	return &MemoryTelemetry{events: make(chan CallEvent, 1024)}
}

// Record records a call without blocking an invocation.
func (telemetry *MemoryTelemetry) Record(event CallEvent) error {
	select {
	case telemetry.events <- event:
	default:
	}
	return nil
}

// Events returns all events observed so far.
func (telemetry *MemoryTelemetry) Events() []CallEvent {
	result := []CallEvent{}
	for {
		select {
		case event := <-telemetry.events:
			result = append(result, event)
		default:
			return result
		}
	}
}

// Runtime invokes bound Tools under explicit request context.
type Runtime struct {
	index     *Index
	selection RootSelection
	ceiling   RootSelection
	telemetry Telemetry
}

// Tool resolves an executable Tool in this Runtime's immutable Index.
func (runtime *Runtime) Tool(ref string) (*Tool, error) {
	return runtime.index.Tool(ref)
}

// NewRuntime constructs a transport-neutral runtime over one bound Index.
func NewRuntime(index *Index, selection, ceiling RootSelection, telemetry Telemetry) (*Runtime, error) {
	if index == nil {
		return nil, errors.New("runtime Index must not be nil")
	}
	if !index.bound {
		return nil, errors.New("a disclosure-only Index cannot be upgraded into a Runtime")
	}
	selection, err := selection.Resolve(index)
	if err != nil {
		return nil, err
	}
	ceiling, err = ceiling.Resolve(index)
	if err != nil {
		return nil, err
	}
	return &Runtime{index: index, selection: selection, ceiling: ceiling, telemetry: telemetry}, nil
}

// InvokeReadOnly runs a read-only Tool through its required door.
func (runtime *Runtime) InvokeReadOnly(ctx context.Context, ref string, arguments json.RawMessage, requested RootSelection) (any, error) {
	return runtime.invoke(ctx, ref, arguments, true, requested)
}

// Invoke runs a writing Tool through its required door.
func (runtime *Runtime) Invoke(ctx context.Context, ref string, arguments json.RawMessage, requested RootSelection) (any, error) {
	return runtime.invoke(ctx, ref, arguments, false, requested)
}

func (runtime *Runtime) invoke(ctx context.Context, ref string, arguments json.RawMessage, readOnly bool, requested RootSelection) (any, error) {
	selection, err := runtime.ceiling.Intersect(runtime.selection)
	if err != nil {
		return nil, err
	}
	selection, err = selection.Intersect(requested)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	tool, err := runtime.index.Tool(ref)
	if err != nil {
		var failure *NodeNotFoundError
		if errors.As(err, &failure) && failure.Reason == WrongKind {
			return nil, runtimeWrongKindError{failure: failure}
		}
		return nil, err
	}
	if tool.ReadOnly != readOnly {
		correct := "contexture_invoke"
		stated := "not read-only"
		if tool.ReadOnly {
			correct = "contexture_invoke_read_only"
			stated = "read-only"
		}
		return nil, wrongDoorError(fmt.Sprintf("%s is %s, so it must be run through %s.", ref, stated, correct))
	}
	binding, err := tool.Binding()
	if err != nil {
		return nil, err
	}
	graph := &SelectedGraph{index: runtime.index, selection: selection}
	principal := CurrentPrincipal(ctx)
	ctx = context.WithValue(ctx, graphKey, graph)
	ctx = context.WithValue(ctx, selectionKey, selection)
	ctx = context.WithValue(ctx, telemetryKey, runtime.telemetry)
	ctx = context.WithValue(ctx, principalKey, principal)
	value, callErr := binding.Call(ctx, arguments)
	if runtime.telemetry != nil {
		_ = runtime.telemetry.Record(CallEvent{Ref: ref, Failed: callErr != nil})
	}
	return value, callErr
}

// Serve holds the Application's Channels open around one serving lifetime.
func (runtime *Runtime) Serve(ctx context.Context, serve func(context.Context) error) error {
	if serve == nil {
		return errors.New("Contexture serving function must not be nil")
	}
	_, err := WithChannels(ctx, runtime.index.channels, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, serve(ctx)
	})
	return err
}

type wrongDoorError string

func (err wrongDoorError) Error() string { return string(err) }
func (err wrongDoorError) Unwrap() error { return ErrWrongDoor }

// runtimeWrongKindError retains the established agent-facing invocation text
// while unwrapping to the typed lookup facts exposed by Index.Tool.
type runtimeWrongKindError struct{ failure *NodeNotFoundError }

func (err runtimeWrongKindError) Error() string {
	return fmt.Sprintf("%s names a %s, not a tool. Open it with contexture_open.", err.failure.Ref, err.failure.Kind)
}

func (err runtimeWrongKindError) Unwrap() error { return err.failure }

// WithPrincipal derives a request context carrying framework identity.
func WithPrincipal(ctx context.Context, principal *foundation.Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

// CurrentPrincipal returns framework identity for the exact current invocation.
func CurrentPrincipal(ctx context.Context) *foundation.Principal {
	principal, _ := ctx.Value(principalKey).(*foundation.Principal)
	return principal
}

// CurrentGraph returns the graph constrained to the exact current selection.
func CurrentGraph(ctx context.Context) *SelectedGraph {
	graph, _ := ctx.Value(graphKey).(*SelectedGraph)
	return graph
}

// CurrentSelection returns the current effective root selection.
func CurrentSelection(ctx context.Context) RootSelection {
	selection, _ := ctx.Value(selectionKey).(RootSelection)
	return selection
}

// CurrentTelemetry returns the current request telemetry exporter.
func CurrentTelemetry(ctx context.Context) Telemetry {
	telemetry, _ := ctx.Value(telemetryKey).(Telemetry)
	return telemetry
}

// SelectedGraph exposes only selected roots and descendants.
type SelectedGraph struct {
	index     *Index
	selection RootSelection
}

// Walk returns selected addresses in canonical order.
func (graph *SelectedGraph) Walk() []string {
	result := []string{}
	for _, ref := range graph.index.Walk() {
		if graph.selection.ContainsRef(ref) {
			result = append(result, ref)
		}
	}
	return result
}

// Find resolves an address only when selected.
func (graph *SelectedGraph) Find(ref string) (Node, error) {
	if err := graph.selection.RequireRef(ref); err != nil {
		return nil, err
	}
	return graph.index.Find(ref)
}
