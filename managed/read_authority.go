package managed

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// ReadError is a bounded native read diagnostic, not a negotiated wire error.
type ReadError struct {
	code    string
	message string
}

func (e *ReadError) Error() string { return e.message }
func (e *ReadError) Code() string  { return e.code }
func readError(code string) error {
	return &ReadError{code, "Native read unavailable: " + code}
}
func readFailure(err error) error {
	var coded interface{ Code() string }
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotReady) || errors.Is(err, ErrClosed) ||
		(errors.As(err, &coded) && (coded.Code() == "permission_denied" || coded.Code() == "not_ready")) {
		return readError("resync_required")
	}
	return err
}

type CatalogSource string

const (
	CatalogHead     CatalogSource = "head"
	CatalogRevision CatalogSource = "revision"
	CatalogServing  CatalogSource = "serving"
)

type DefinitionReadRequest struct {
	GraphID  string
	Source   CatalogSource
	Revision string
}

// DefinitionReadView must be captured under one CURRENT graph.catalog admission
// fence. Index is retained immutable history; CurrentIndex governs ID existence.
type DefinitionReadView struct {
	Identity            Identity
	StoreOrigin         string
	Index, CurrentIndex *GraphIndex
	Revision            string
	ScopeIDs            []string
}

// DefinitionReadPort is required from the durable owner. Dispatch synchronously,
// exactly once, under its shared authoring/admission read fence. Propagate gaps
// and corruption; never substitute today's head for an unavailable revision.
type DefinitionReadPort interface {
	Read(context.Context, DefinitionReadRequest, func(DefinitionReadView) (FrozenJSON, error)) (FrozenJSON, error)
}

type DefinitionReadFunc func(context.Context, DefinitionReadRequest, func(DefinitionReadView) (FrozenJSON, error)) (FrozenJSON, error)

func (f DefinitionReadFunc) Read(ctx context.Context, r DefinitionReadRequest, dispatch func(DefinitionReadView) (FrozenJSON, error)) (FrozenJSON, error) {
	return f(ctx, r, dispatch)
}

// ServingReadPort attests the CURRENT durable store incarnation explicitly.
// Runtime supplies existing current authority, coordinator and generation leases.
type ServingReadPort struct {
	Runtime     *ManagedRuntime
	StoreOrigin func(context.Context) (string, error)
	// Fence is REQUIRED: serialize CURRENT authority/identity/store incarnation
	// through synchronous dispatch with the durable owner's changes. Runtime
	// Verify/CurrentIdentity and StoreOrigin must be safe within this same fence.
	Fence func(context.Context, func() (FrozenJSON, error)) (FrozenJSON, error)
}

func readOrigin(value string) error {
	if len(value) == 0 || len(value) > 4096 {
		return readError("contract_mismatch")
	}
	return nil
}
func readSelection(ids []string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	out := slices.Clone(ids)
	for _, id := range out {
		if _, err := graph.ValidateID(id); err != nil {
			return nil, readError("schema_invalid")
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
func readIDs(ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		if _, err := graph.ValidateID(id); err != nil {
			return nil, readError("contract_mismatch")
		}
		out[id] = true
	}
	return out, nil
}
func selectedReadIDs(index *GraphIndex, anchors []string) ([]string, error) {
	if index == nil {
		return nil, readError("contract_mismatch")
	}
	selected := map[string]bool{}
	if anchors == nil {
		return index.Preorder(), nil
	}
	for _, id := range anchors {
		nodes, err := index.Subtree(id, true)
		if err != nil {
			return nil, readError("resync_required")
		}
		for _, n := range nodes {
			selected[n.NodeID()] = true
		}
	}
	out := []string{}
	for _, id := range index.Preorder() {
		if selected[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
func definitionReadView(ctx context.Context, view DefinitionReadView, graphID string) error {
	host, err := IdentityFromContext(ctx)
	if err != nil {
		return err
	}
	if !sameActor(host, view.Identity) {
		return ErrDenied
	}
	if validateIdentity(view.Identity) != nil ||
		readOrigin(view.StoreOrigin) != nil || view.Index == nil || view.CurrentIndex == nil {
		return readError("contract_mismatch")
	}
	if _, err := view.Index.Get("", "id"); err != nil {
		var coded interface{ Code() string }
		if !errors.As(err, &coded) || coded.Code() != "reference_not_found" {
			return readError("contract_mismatch")
		}
	}
	if _, err := view.CurrentIndex.Get("", "id"); err != nil {
		var coded interface{ Code() string }
		if !errors.As(err, &coded) || coded.Code() != "reference_not_found" {
			return readError("contract_mismatch")
		}
	}
	if _, err := graph.ParseCounter(view.Revision); err != nil {
		return readError("contract_mismatch")
	}
	g, current := view.Index.Definition(), view.CurrentIndex.Definition()
	if g.GraphID() != graphID || current.GraphID() != graphID {
		return readError("contract_mismatch")
	}
	_, err = readIDs(view.ScopeIDs)
	return err
}
func visibleDefinitionRead(view DefinitionReadView, pinned *GraphIndex, anchors []string) ([]string, error) {
	ids, err := selectedReadIDs(pinned, anchors)
	if err != nil {
		return nil, err
	}
	allowed, err := readIDs(view.ScopeIDs)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range ids {
		if _, ok := view.CurrentIndex.Location(id); allowed[id] && ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// checkedReadFence detects missing/repeated/retained dispatch and ignored action
// errors. The trusted port still owns synchronous dispatch and current admission.
func checkedReadFence[V any](ctx context.Context, read func(func(V) (FrozenJSON, error)) (FrozenJSON, error), action func(V) (FrozenJSON, error)) (result FrozenJSON, err error) {
	defer recoverFailure(&err, "native read fence")
	var mu sync.Mutex
	called, closed := false, false
	defer func() {
		mu.Lock()
		closed = true
		mu.Unlock()
	}()
	var actual FrozenJSON
	var actionErr error
	result, err = read(func(view V) (FrozenJSON, error) {
		mu.Lock()
		defer mu.Unlock()
		if called || closed {
			actionErr = readError("contract_mismatch")
			return FrozenJSON{}, actionErr
		}
		called = true
		if actionErr = ctx.Err(); actionErr == nil {
			actual, actionErr = action(view)
		}
		return actual, actionErr
	})
	mu.Lock()
	defer mu.Unlock()
	closed = true
	if actionErr != nil {
		return FrozenJSON{}, actionErr
	}
	if err != nil {
		return FrozenJSON{}, err
	}
	if !called || !sameReadJSON(actual, result) {
		return FrozenJSON{}, readError("contract_mismatch")
	}
	if err := ctx.Err(); err != nil {
		return FrozenJSON{}, err
	}
	return result, nil
}

// WithReadAuthorization adapts the existing native RequestAuthority and pointer
// fence for synchronous immutable read rendering. It does not cache admission or
// change existing Request/Invoke semantics. The Host's shared admission fence
// must span this call. Dispatch must not perform I/O or reenter serving.
func (r *ManagedRuntime) WithReadAuthorization(ctx context.Context, identity Identity, operation string, requested []string, dispatch func([]string) (FrozenJSON, error)) (result FrozenJSON, err error) {
	defer recoverFailure(&err, "native read authorization")
	if dispatch == nil || (operation != "route" && operation != "open") {
		return FrozenJSON{}, readError("schema_invalid")
	}
	before, err := r.CurrentIdentity(ctx)
	if err != nil || identity != before {
		return FrozenJSON{}, ErrDenied
	}
	active, ok := r.coordinator.Current()
	if !ok {
		return FrozenJSON{}, ErrNotReady
	}
	ids := intersect(requested, requested)
	allowed, err := r.authority.Verify(ctx, RequestAuthorization{before, active.Generation.Plan().Index(), operation, slices.Clone(ids)})
	if err != nil {
		return FrozenJSON{}, err
	}
	after, err := r.CurrentIdentity(ctx)
	if err != nil || before != after {
		return FrozenJSON{}, ErrDenied
	}
	c := r.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return FrozenJSON{}, err
	}
	if c.closed || c.current == nil || c.current.Generation != active.Generation {
		return FrozenJSON{}, ErrDenied
	}
	current := []string{}
	index := active.Generation.Plan().Index()
	for _, id := range intersect(ids, allowed) {
		loc, exists := index.Location(id)
		if !exists || loc.Plane != graph.ModelPlane {
			continue
		}
		if operation == "open" {
			if open, err := index.ModelMayOpen(id); err != nil || !open {
				continue
			}
		}
		current = append(current, id)
	}
	result, err = dispatch(current)
	if err != nil {
		return FrozenJSON{}, err
	}
	return result, ctx.Err()
}

type servingReadSnapshot struct {
	serving   ServingGeneration
	plan      *StructuralPlan
	visible   []string
	identity  Identity
	dispose   func()
	authorize func(context.Context, func() (FrozenJSON, error)) (FrozenJSON, error)
}

func servingReadFence(ctx context.Context, port ServingReadPort, action func() (FrozenJSON, error)) (FrozenJSON, error) {
	return checkedReadFence(ctx, func(dispatch func(struct{}) (FrozenJSON, error)) (FrozenJSON, error) {
		return port.Fence(ctx, func() (FrozenJSON, error) { return dispatch(struct{}{}) })
	}, func(struct{}) (FrozenJSON, error) { return action() })
}
func captureServingRead(ctx context.Context, port ServingReadPort, anchors []string, openable bool, dispatch func(servingReadSnapshot) (FrozenJSON, error)) (FrozenJSON, error) {
	return servingReadFence(ctx, port, func() (FrozenJSON, error) {
		return captureServingReadAtFence(ctx, port, anchors, openable, dispatch)
	})
}
func captureServingReadAtFence(ctx context.Context, port ServingReadPort, anchors []string, openable bool, dispatch func(servingReadSnapshot) (FrozenJSON, error)) (result FrozenJSON, err error) {
	defer recoverFailure(&err, "native serving read")
	r, c := port.Runtime, port.Runtime.Coordinator()
	origin, err := port.StoreOrigin(ctx)
	if err != nil {
		return FrozenJSON{}, err
	}
	if err := readOrigin(origin); err != nil {
		return FrozenJSON{}, err
	}
	identity, err := r.CurrentIdentity(ctx)
	if err != nil {
		return FrozenJSON{}, err
	}
	lease, err := c.Lease()
	if err != nil {
		return FrozenJSON{}, err
	}
	defer lease.Resources.Close()
	keeper, err := lease.Generation.Retain()
	if err != nil {
		return FrozenJSON{}, err
	}
	adopted := false
	defer func() {
		if !adopted {
			keeper.Close()
		}
	}()
	plan := lease.Generation.Plan()
	selected, err := selectedReadIDs(plan.Index(), anchors)
	if err != nil {
		return FrozenJSON{}, err
	}
	requested := []string{}
	for _, id := range selected {
		loc, _ := plan.Index().Location(id)
		mayOpen, _ := plan.Index().ModelMayOpen(id)
		if loc.Plane == graph.ModelPlane && (!openable || mayOpen) {
			requested = append(requested, id)
		}
	}
	operation := "route"
	if openable {
		operation = "open"
	}
	fresh := func(ctx context.Context) error {
		now, err := port.StoreOrigin(ctx)
		if err != nil {
			return err
		}
		id, err := r.CurrentIdentity(ctx)
		if err != nil {
			return err
		}
		if now != origin || id != identity {
			return readError("resync_required")
		}
		return nil
	}
	result, err = r.WithReadAuthorization(ctx, identity, operation, requested, func(allowed []string) (FrozenJSON, error) {
		if err := fresh(ctx); err != nil {
			return FrozenJSON{}, err
		}
		ceiling := slices.Clone(allowed)
		authorize := func(ctx context.Context, render func() (FrozenJSON, error)) (FrozenJSON, error) {
			return servingReadFence(ctx, port, func() (FrozenJSON, error) {
				if err := fresh(ctx); err != nil {
					return FrozenJSON{}, err
				}
				resources, err := keeper.Lease()
				if err != nil {
					return FrozenJSON{}, err
				}
				defer resources.Close()
				return r.WithReadAuthorization(ctx, identity, operation, requested, func(current []string) (FrozenJSON, error) {
					if err := fresh(ctx); err != nil {
						return FrozenJSON{}, err
					}
					if !slices.Equal(ceiling, current) {
						return FrozenJSON{}, readError("resync_required")
					}
					return render()
				})
			})
		}
		value, err := dispatch(servingReadSnapshot{lease.ServingGeneration, plan, ceiling, identity, keeper.Close, authorize})
		adopted = err == nil
		return value, err
	})
	return result, err
}
