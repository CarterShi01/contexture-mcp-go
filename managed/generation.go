package managed

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

var (
	ErrNotReady   = errors.New("managed generation is not ready")
	ErrDenied     = errors.New("current managed request authority is unavailable")
	ErrClosed     = errors.New("managed scope is closed")
	ErrCapacity   = errors.New("managed capacity exhausted")
	ErrSuperseded = errors.New("managed release was superseded")
)

func recoverFailure(err *error, label string) {
	if value := recover(); value != nil {
		*err = fmt.Errorf("%s panic: %v", label, value)
	}
}

type PreparationContext struct {
	GraphID, Revision, GenerationUUID string
}
type PreparedResource struct {
	Handle  any
	Dispose func(context.Context) error
}
type PreparationAdapterRegistration struct {
	BindingID, Version, ABI, ContractDigest string
	// Acquire is resource-only. On failure it owns unwinding any partial work.
	Acquire func(context.Context, ResolvedBinding, string, PreparationContext) (PreparedResource, error)
}

// ReadyGeneration exclusively owns resources. Cancellation can stop waiting,
// never close a handle while entered work still uses it.
type ReadyGeneration struct {
	plan      *StructuralPlan
	context   PreparationContext
	mu        sync.Mutex
	state     string
	refs      int
	resources map[string]any
	disposers []func(context.Context) error
	started   bool
	done      chan struct{}
	err       error
	owner     *LocalServingCoordinator
}

func PrepareGeneration(ctx context.Context, plan *StructuralPlan, preparation PreparationContext, adapters map[string]PreparationAdapterRegistration) (*ReadyGeneration, error) {
	if !plan.IsCompiled() {
		return nil, ErrNotReady
	}
	definition := plan.Definition()
	if preparation.GraphID != definition.GraphID() {
		return nil, errors.New("preparation graph identity mismatch")
	}
	if _, err := graph.ParseCounter(preparation.Revision); err != nil {
		return nil, err
	}
	if _, err := graph.NewManagedReference(preparation.GraphID, preparation.GenerationUUID, "probe"); err != nil {
		return nil, err
	}
	type acquisition struct {
		id      string
		binding ResolvedBinding
		adapter PreparationAdapterRegistration
	}
	captured := []acquisition{}
	for _, id := range plan.Index().Preorder() {
		binding, ok := plan.Binding(id)
		if !ok {
			continue
		}
		adapter, ok := adapters[id]
		descriptor := binding.Registration().Descriptor()
		if !ok || adapter.Acquire == nil || adapter.BindingID != descriptor.BindingID() ||
			adapter.Version != descriptor.Version() || adapter.ABI != NativeABI ||
			adapter.ContractDigest != binding.Registration().ContractDigest() {
			return nil, errors.New("preparation adapter does not attest exact binding ABI/contract")
		}
		captured = append(captured, acquisition{id, binding, adapter})
	}
	if len(captured) != len(adapters) {
		return nil, errors.New("preparation adapters must match resolved binding nodes exactly")
	}
	g := &ReadyGeneration{plan: plan, context: preparation, state: "prepared", resources: map[string]any{}, done: make(chan struct{})}
	fail := func(original error) (*ReadyGeneration, error) {
		g.BeginRetirement()
		return nil, errors.Join(original, g.Wait(context.Background()))
	}
	for _, entry := range captured {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		resource, err := func() (resource PreparedResource, err error) {
			defer recoverFailure(&err, "resource acquisition")
			return entry.adapter.Acquire(ctx, entry.binding, entry.id, preparation)
		}()
		if err != nil {
			return fail(err)
		}
		if resource.Dispose == nil {
			return fail(errors.New("acquisition must return an explicit owned disposer"))
		}
		g.resources[entry.id] = resource.Handle
		g.disposers = append(g.disposers, resource.Dispose)
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return g, nil
}
func (g *ReadyGeneration) Plan() *StructuralPlan       { return g.plan }
func (g *ReadyGeneration) Context() PreparationContext { return g.context }
func (g *ReadyGeneration) ResourceState() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}
func (g *ReadyGeneration) Lease() (*ResourceLease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != "prepared" {
		return nil, ErrClosed
	}
	g.refs++
	return &ResourceLease{generation: g}, nil
}
func (g *ReadyGeneration) Retain() (*RetainedGeneration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != "prepared" {
		return nil, ErrClosed
	}
	g.refs++
	return &RetainedGeneration{generation: g}, nil
}
func (g *ReadyGeneration) BeginRetirement() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == "prepared" {
		g.state = "draining"
	}
	g.startCleanupLocked()
}
func (g *ReadyGeneration) startCleanupLocked() {
	if g.state != "draining" || g.refs != 0 || g.started {
		return
	}
	g.started = true
	go g.cleanup()
}
func (g *ReadyGeneration) cleanup() {
	var result error
	for i := len(g.disposers) - 1; i >= 0; i-- {
		err := func() (err error) {
			defer func() {
				if value := recover(); value != nil {
					err = fmt.Errorf("resource disposer panic: %v", value)
				}
			}()
			return g.disposers[i](context.Background())
		}()
		result = errors.Join(result, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disposers, g.resources = nil, nil
	g.err = result
	g.state = "retired"
	if result != nil {
		g.state = "cleanup_failed"
	}
	close(g.done)
}
func (g *ReadyGeneration) Wait(ctx context.Context) error {
	select {
	case <-g.done:
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g *ReadyGeneration) Retire(ctx context.Context) error {
	g.BeginRetirement()
	return g.Wait(ctx)
}

type ResourceLease struct {
	generation *ReadyGeneration
	closed     bool
}

func (l *ResourceLease) Handle(nodeID string) (any, error) {
	if l == nil || l.generation == nil {
		return nil, ErrClosed
	}
	g := l.generation
	g.mu.Lock()
	defer g.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	value, ok := g.resources[nodeID]
	if !ok {
		return nil, errors.New("no resource for binding node")
	}
	return value, nil
}
func (l *ResourceLease) Close() {
	if l == nil || l.generation == nil {
		return
	}
	g := l.generation
	g.mu.Lock()
	defer g.mu.Unlock()
	if !l.closed {
		l.closed = true
		g.refs--
		g.startCleanupLocked()
	}
}

type RetainedGeneration struct {
	generation *ReadyGeneration
	closed     bool
}

func (k *RetainedGeneration) Lease() (*ResourceLease, error) {
	if k == nil || k.generation == nil {
		return nil, ErrClosed
	}
	g := k.generation
	g.mu.Lock()
	defer g.mu.Unlock()
	if k.closed || (g.state != "prepared" && g.state != "draining") {
		return nil, ErrClosed
	}
	g.refs++
	return &ResourceLease{generation: g}, nil
}
func (k *RetainedGeneration) Close() {
	if k == nil || k.generation == nil {
		return
	}
	g := k.generation
	g.mu.Lock()
	defer g.mu.Unlock()
	if !k.closed {
		k.closed = true
		g.refs--
		g.startCleanupLocked()
	}
}
