package model

import (
	"context"
	"fmt"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// contextKey is private framework vocabulary for one request's immutable
// Contexture facts. Values are always derived from a caller's context, never
// stored globally, so concurrent calls and nested scopes cannot leak facts.
type contextKey int

const (
	principalKey contextKey = iota
	graphKey
	selectionKey
	telemetryKey
	channelsKey
)

// WithGraph derives a context carrying one request-selected immutable graph.
// It is the Go-native equivalent of Python's bound_graph: deriving another
// context nests the graph scope, while retaining the parent context restores
// the outer graph without a mutable reset operation.
//
// Runtime installs the authoritative graph for every Tool invocation. Hosts
// and tests may use WithGraph for framework-aware local scopes, but a Tool
// cannot use a caller-supplied graph to widen Runtime selection.
func WithGraph(ctx context.Context, graph *SelectedGraph) context.Context {
	return context.WithValue(ctx, graphKey, graph)
}

func withSelection(ctx context.Context, selection RootSelection) context.Context {
	return context.WithValue(ctx, selectionKey, selection)
}

// WithTelemetry derives a context carrying one task-local collector. Nested
// contexts retain their parent and therefore restore the outer collector.
func WithTelemetry(ctx context.Context, telemetry Telemetry) context.Context {
	return context.WithValue(ctx, telemetryKey, telemetry)
}

func withInvocationFacts(ctx context.Context, graph *SelectedGraph, selection RootSelection, telemetry Telemetry, channels ChannelHandle) context.Context {
	ctx = WithGraph(ctx, graph)
	ctx = withSelection(ctx, selection)
	ctx = WithTelemetry(ctx, telemetry)
	ctx = context.WithValue(ctx, channelsKey, channels)
	// Keep the request identity from the Host context explicit in the derived
	// invocation scope. Principal is immutable; this does not grant a Tool an
	// authority-changing handle.
	return context.WithValue(ctx, principalKey, CurrentPrincipal(ctx))
}

// CurrentChannels returns the exact deployment dependency captured by the
// compiled Application, or nil outside a Tool invocation.
func CurrentChannels(ctx context.Context) ChannelHandle { return ctx.Value(channelsKey) }

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
// It panics outside a derived graph scope so missing framework context cannot
// be mistaken for an all-roots graph, matching Python's fail-fast accessor.
func CurrentGraph(ctx context.Context) *SelectedGraph {
	graph, _ := ctx.Value(graphKey).(*SelectedGraph)
	if graph == nil {
		panic(fmt.Errorf("no compiled Contexture graph is active; use CurrentGraph only inside a Tool invocation or WithGraph scope"))
	}
	return graph
}

// CurrentSelection returns the current effective root selection.
func CurrentSelection(ctx context.Context) RootSelection {
	selection, ok := ctx.Value(selectionKey).(RootSelection)
	if !ok {
		return AllRoots()
	}
	return selection
}

// CurrentTelemetry returns the current request telemetry exporter.
func CurrentTelemetry(ctx context.Context) Telemetry {
	telemetry, _ := ctx.Value(telemetryKey).(Telemetry)
	if telemetry == nil {
		panic(fmt.Errorf("no Contexture telemetry is active; use CurrentTelemetry only inside a Tool invocation"))
	}
	return telemetry
}
