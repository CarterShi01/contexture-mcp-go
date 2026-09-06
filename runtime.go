package contexture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrWrongDoor identifies a Tool invoked through the wrong fixed gateway door.
var ErrWrongDoor = errors.New("Contexture Tool invoked through the wrong door")

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

// NewRuntime constructs a transport-neutral runtime over one bound Index.
func NewRuntime(index *Index, selection, ceiling RootSelection, telemetry Telemetry) (*Runtime, error) {
	if index == nil {
		return nil, errors.New("runtime Index must not be nil")
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
	node, err := runtime.index.Find(ref)
	if err != nil {
		return nil, err
	}
	tool, ok := node.(*Tool)
	if !ok {
		return nil, fmt.Errorf("%s names a %s, not a tool. Open it with contexture_open.", ref, node.nodeKind())
	}
	if tool.ReadOnly != readOnly {
		correct := "contexture_invoke"
		stated := "not read-only"
		if tool.ReadOnly {
			correct = "contexture_invoke_read_only"
			stated = "read-only"
		}
		return nil, fmt.Errorf("%w: %s is %s, so it must be run through %s.", ErrWrongDoor, ref, stated, correct)
	}
	binding, err := tool.Binding()
	if err != nil {
		return nil, err
	}
	graph := &SelectedGraph{index: runtime.index, selection: selection}
	ctx = context.WithValue(ctx, graphKey, graph)
	ctx = context.WithValue(ctx, selectionKey, selection)
	ctx = context.WithValue(ctx, telemetryKey, runtime.telemetry)
	value, callErr := binding.Call(ctx, arguments)
	if runtime.telemetry != nil {
		_ = runtime.telemetry.Record(CallEvent{Ref: ref, Failed: callErr != nil})
	}
	return value, callErr
}

// WithPrincipal derives a request context carrying framework identity.
func WithPrincipal(ctx context.Context, principal any) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

// CurrentPrincipal returns framework identity for the exact current invocation.
func CurrentPrincipal(ctx context.Context) any { return ctx.Value(principalKey) }

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
