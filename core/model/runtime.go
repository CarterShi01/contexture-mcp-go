package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// NodeUsage is the framework-owned aggregate for one compiled node.
type NodeUsage struct {
	Ref        string `json:"ref"`
	CallCount  int    `json:"call_count"`
	ErrorCount int    `json:"error_count"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// Telemetry observes node usage and never changes business outcomes.
type Telemetry interface {
	Record(CallEvent) error
	Usage(ref string) NodeUsage
}

// CallEvent records one observed node use. Role and Skill opens have Failed
// false; Tool invocations set Failed when their Binding returns an error.
type CallEvent struct {
	Ref        string
	Failed     bool
	OccurredAt string
}

// MemoryTelemetry is a race-safe, non-lossy process-local usage collector.
type MemoryTelemetry struct {
	mu     sync.RWMutex
	usage  map[string]NodeUsage
	events []CallEvent
}

// NewMemoryTelemetry creates a process-local collector. It retains every
// event for diagnostics; applications that need bounded or remote retention
// should provide their own Telemetry implementation.
func NewMemoryTelemetry() *MemoryTelemetry {
	return &MemoryTelemetry{usage: map[string]NodeUsage{}}
}

// Record aggregates a call and retains a non-destructive event snapshot.
func (telemetry *MemoryTelemetry) Record(event CallEvent) error {
	if telemetry == nil {
		return nil
	}
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	telemetry.mu.Lock()
	defer telemetry.mu.Unlock()
	previous := telemetry.usage[event.Ref]
	telemetry.usage[event.Ref] = NodeUsage{
		Ref: event.Ref, CallCount: previous.CallCount + 1,
		ErrorCount: previous.ErrorCount + boolCount(event.Failed), LastUsedAt: event.OccurredAt,
	}
	telemetry.events = append(telemetry.events, event)
	return nil
}

// Usage returns a snapshot, including a zero-count usage for an unseen ref.
func (telemetry *MemoryTelemetry) Usage(ref string) NodeUsage {
	if telemetry == nil {
		return NodeUsage{Ref: ref}
	}
	telemetry.mu.RLock()
	defer telemetry.mu.RUnlock()
	usage, exists := telemetry.usage[ref]
	if !exists {
		return NodeUsage{Ref: ref}
	}
	return usage
}

// Events returns all observed events without consuming or dropping them.
func (telemetry *MemoryTelemetry) Events() []CallEvent {
	if telemetry == nil {
		return nil
	}
	telemetry.mu.RLock()
	defer telemetry.mu.RUnlock()
	return append([]CallEvent(nil), telemetry.events...)
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
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
	if telemetry == nil {
		telemetry = NewMemoryTelemetry()
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
	selection, err := runtime.effectiveSelection(requested)
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
		return nil, &WrongDoorError{Ref: ref, ReadOnly: tool.ReadOnly}
	}
	binding, err := tool.Binding()
	if err != nil {
		return nil, err
	}
	graph, err := NewSelectedGraph(runtime.index, selection)
	if err != nil {
		return nil, err
	}
	ctx = withInvocationFacts(ctx, graph, selection, runtime.telemetry)
	value, callErr := binding.Call(ctx, arguments)
	reportTelemetry(runtime.telemetry, CallEvent{Ref: ref, Failed: callErr != nil})
	return value, callErr
}

// effectiveSelection is the single runtime authorization calculation shared
// by direct Runtime calls and the model-facing ExecutionAPI facade.
func (runtime *Runtime) effectiveSelection(requested RootSelection) (RootSelection, error) {
	selection, err := runtime.ceiling.Intersect(runtime.selection)
	if err != nil {
		return RootSelection{}, err
	}
	selection, err = selection.Intersect(requested)
	if err != nil {
		return RootSelection{}, err
	}
	return selection.Resolve(runtime.index)
}

// Telemetry returns the collector shared by this Runtime's invocation path.
func (runtime *Runtime) Telemetry() Telemetry { return runtime.telemetry }

func reportTelemetry(telemetry Telemetry, event CallEvent) {
	if telemetry == nil {
		return
	}
	// Telemetry is side-channel evidence, never a business dependency.
	defer func() {
		// A third-party exporter must not turn a successful binding into a
		// panic or replace a binding error that has already occurred.
		_ = recover()
	}()
	_ = telemetry.Record(event)
}

// ReportTelemetry publishes one node-use observation without letting a
// telemetry exporter alter the caller's outcome. It is the Go-native
// equivalent of Python's telemetry.report: failed selects the error counter,
// while the collector supplies the observation timestamp when needed.
func ReportTelemetry(telemetry Telemetry, ref string, failed bool) {
	reportTelemetry(telemetry, CallEvent{Ref: ref, Failed: failed})
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

// WrongDoorError reports a direct Runtime call made through the Tool's other
// mutation-semantics door. It retains the canonical Tool facts for a native
// Host while still unwrapping to ErrWrongDoor for category checks.
type WrongDoorError struct {
	Ref      string
	ReadOnly bool
}

func (err *WrongDoorError) Error() string {
	if err == nil {
		return ErrWrongDoor.Error()
	}
	stated := "writing"
	if err.ReadOnly {
		stated = "read-only"
	}
	return fmt.Sprintf("%q is a %s Tool", err.Ref, stated)
}
func (*WrongDoorError) Unwrap() error { return ErrWrongDoor }

// runtimeWrongKindError retains the established agent-facing invocation text
// while unwrapping to the typed lookup facts exposed by Index.Tool.
type runtimeWrongKindError struct{ failure *NodeNotFoundError }

func (err runtimeWrongKindError) Error() string {
	return fmt.Sprintf("%s names a %s, not a tool. Open it with %s.", err.failure.Ref, err.failure.Kind, OpenGatewayName)
}

func (err runtimeWrongKindError) Unwrap() error { return err.failure }
