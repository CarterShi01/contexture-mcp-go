package managed

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

type identityKey struct{}

// WithIdentity carries Host-supplied identity explicitly. Business arguments
// never supply identity, and there is no ambient goroutine/global fallback.
func WithIdentity(ctx context.Context, identity Identity) (context.Context, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, identityKey{}, identity), nil
}
func IdentityFromContext(ctx context.Context) (Identity, error) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	if !ok {
		return Identity{}, ErrDenied
	}
	return identity, validateIdentity(identity)
}
func validateIdentity(identity Identity) error {
	for _, id := range []string{identity.ActorID, identity.TenantID, identity.AuthorityRevision} {
		if _, err := graph.ValidateID(id); err != nil {
			return ErrDenied
		}
	}
	return nil
}
func sameActor(a, b Identity) bool { return a.ActorID == b.ActorID && a.TenantID == b.TenantID }

type RequestAuthorization struct {
	Identity     Identity
	CurrentIndex *GraphIndex
	Operation    string
	RequestedIDs []string
}

// RequestAuthority attests CURRENT policy at return, not a cached grant.
// The supplied index is current serving authority, even for an old run pin.
type RequestAuthority interface {
	Verify(context.Context, RequestAuthorization) ([]string, error)
}
type RequestAuthorityFunc func(context.Context, RequestAuthorization) ([]string, error)

func (f RequestAuthorityFunc) Verify(ctx context.Context, request RequestAuthorization) ([]string, error) {
	return f(ctx, request)
}

// RequestIdentity optionally supports a Host whose authority revision can change
// during a call. It must derive identity from the explicit context, not globals.
type RequestIdentity interface {
	Current(context.Context) (Identity, error)
}
type ContextIdentity struct{}

func (ContextIdentity) Current(ctx context.Context) (Identity, error) {
	return IdentityFromContext(ctx)
}

type ManagedRequestOptions struct {
	Selection            []string
	PublicationSelection []string
}
type ManagedRuntime struct {
	coordinator *LocalServingCoordinator
	authority   RequestAuthority
	identities  RequestIdentity
}

func NewManagedRuntime(coordinator *LocalServingCoordinator, authority RequestAuthority, identities RequestIdentity) (*ManagedRuntime, error) {
	if coordinator == nil || authority == nil {
		return nil, errors.New("serving coordinator and current request authority required")
	}
	if identities == nil {
		identities = ContextIdentity{}
	}
	return &ManagedRuntime{coordinator, authority, identities}, nil
}
func (r *ManagedRuntime) Coordinator() *LocalServingCoordinator { return r.coordinator }
func (r *ManagedRuntime) CurrentIdentity(ctx context.Context) (Identity, error) {
	// Even a custom port must be anchored in explicit Host request identity.
	host, err := IdentityFromContext(ctx)
	if err != nil {
		return Identity{}, err
	}
	identity, err := r.identities.Current(ctx)
	if err != nil || validateIdentity(identity) != nil || !sameActor(host, identity) {
		return Identity{}, ErrDenied
	}
	return identity, nil
}

func intersect(left, right []string) []string {
	allowed := map[string]bool{}
	for _, id := range right {
		allowed[id] = true
	}
	result := []string{}
	seen := map[string]bool{}
	for _, id := range left {
		if allowed[id] && !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result
}
func (r *ManagedRuntime) authorize(ctx context.Context, identity Identity, operation string, requested []string) ([]string, Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, Identity{}, err
	}
	before, err := r.CurrentIdentity(ctx)
	if err != nil || !sameActor(identity, before) {
		return nil, Identity{}, ErrDenied
	}
	active, ok := r.coordinator.Current()
	if !ok {
		return nil, Identity{}, ErrNotReady
	}
	ids := intersect(requested, requested)
	allowed, err := r.authority.Verify(ctx, RequestAuthorization{before, active.Generation.plan.Index(), operation, slices.Clone(ids)})
	if err != nil {
		return nil, Identity{}, err
	}
	after, err := r.CurrentIdentity(ctx)
	if err != nil || after != before {
		return nil, Identity{}, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, Identity{}, err
	}
	// Admission is linearized under the pointer lock. The admitted operation
	// then owns its generation lease; business execution never holds this lock.
	c := r.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.current == nil || c.current.Generation != active.Generation {
		return nil, Identity{}, ErrDenied
	}
	current := []string{}
	for _, id := range intersect(ids, allowed) {
		if _, err := active.Generation.plan.Index().Get(id, "id"); err == nil {
			current = append(current, id)
		}
	}
	return current, before, nil
}
func selection(lease ServingGeneration, anchors []string) ([]string, []string, error) {
	index := lease.Generation.plan.Index()
	if anchors == nil {
		definition := lease.Generation.plan.Definition()
		anchors = []string{}
		for _, root := range definition.Roots() {
			if root.Plane == graph.ModelPlane {
				anchors = append(anchors, root.NodeID)
			}
		}
	} else {
		anchors = slices.Clone(anchors)
	}
	selected := map[string]bool{}
	for _, id := range anchors {
		open, err := index.ModelMayOpen(id)
		if err != nil || !open {
			return nil, nil, ErrDenied
		}
		nodes, err := index.Subtree(id, true)
		if err != nil {
			return nil, nil, err
		}
		for _, node := range nodes {
			if open, err := index.ModelMayOpen(node.NodeID()); err == nil && open {
				selected[node.NodeID()] = true
			}
		}
	}
	ceiling := []string{}
	for _, id := range index.Preorder() {
		if selected[id] {
			ceiling = append(ceiling, id)
		}
	}
	return anchors, ceiling, nil
}

// ManagedRunPin is an opaque local retention capability, not authorization.
type ManagedRunPin struct {
	runtime            *ManagedRuntime
	serving            ServingGeneration
	identity           Identity
	anchors            []string
	publicationAnchors []string
	ceiling            []string
	keeper             *RetainedGeneration
}

func (p *ManagedRunPin) GenerationUUID() string { return p.serving.Attempt.GenerationUUID }
func (p *ManagedRunPin) Close() {
	if p != nil && p.keeper != nil {
		p.keeper.Close()
	}
}
func (r *ManagedRuntime) CreatePin(ctx context.Context, options ManagedRequestOptions) (*ManagedRunPin, error) {
	lease, err := r.coordinator.Lease()
	if err != nil {
		return nil, err
	}
	defer lease.Resources.Close()
	identity, err := r.CurrentIdentity(ctx)
	if err != nil {
		return nil, err
	}
	anchors, publicationAnchors, selected, err := requestSelection(lease.ServingGeneration, options)
	if err != nil {
		return nil, err
	}
	keeper, err := lease.Generation.Retain()
	if err != nil {
		return nil, err
	}
	ceiling, _, err := r.authorize(ctx, identity, "run.create", selected)
	if err != nil {
		keeper.Close()
		return nil, err
	}
	return &ManagedRunPin{runtime: r, serving: lease.ServingGeneration, identity: identity,
		anchors: anchors, publicationAnchors: publicationAnchors, ceiling: ceiling, keeper: keeper}, nil
}
func (r *ManagedRuntime) Request(ctx context.Context, options ManagedRequestOptions, action func(*ManagedRequest) error) error {
	if action == nil {
		return errors.New("managed request action required")
	}
	lease, err := r.coordinator.Lease()
	if err != nil {
		return err
	}
	defer lease.Resources.Close()
	identity, err := r.CurrentIdentity(ctx)
	if err != nil {
		return err
	}
	_, _, selected, err := requestSelection(lease.ServingGeneration, options)
	if err != nil {
		return err
	}
	ceiling, _, err := r.authorize(ctx, identity, "request", selected)
	if err != nil {
		return err
	}
	return r.execute(ctx, lease, identity, ceiling, options, action)
}
func (r *ManagedRuntime) PinnedRequest(ctx context.Context, pin *ManagedRunPin, action func(*ManagedRequest) error) error {
	if pin == nil || pin.runtime != r || action == nil || pin.serving.Attempt.BootID != r.coordinator.bootID {
		return ErrDenied
	}
	resources, err := pin.keeper.Lease()
	if err != nil {
		return err
	}
	defer resources.Close()
	ceiling, _, err := r.authorize(ctx, pin.identity, "request", pin.ceiling)
	if err != nil {
		return err
	}
	return r.execute(ctx, ServingLease{pin.serving, resources}, pin.identity, ceiling,
		ManagedRequestOptions{Selection: pin.anchors, PublicationSelection: pin.publicationAnchors}, action)
}
func (r *ManagedRuntime) execute(ctx context.Context, lease ServingLease, identity Identity, ceiling []string,
	options ManagedRequestOptions, action func(*ManagedRequest) error) (err error) {
	_, modelIDs, err := selection(lease.ServingGeneration, options.Selection)
	if err != nil {
		return err
	}
	_, _, publicationIDs, err := requestSelection(lease.ServingGeneration,
		ManagedRequestOptions{Selection: []string{}, PublicationSelection: options.PublicationSelection})
	if err != nil {
		return err
	}
	request := &ManagedRequest{runtime: r, ctx: ctx, lease: lease, identity: identity, ceiling: slices.Clone(ceiling),
		modelCeiling: intersect(ceiling, modelIDs), publicationCeiling: intersect(ceiling, publicationIDs),
		open: true, pending: map[*requestOperation]bool{}}
	defer func() { err = errors.Join(err, request.Finish()) }()
	return action(request)
}

type requestOperation struct {
	done chan struct{}
	err  error
}
type ManagedRequest struct {
	runtime             *ManagedRuntime
	ctx                 context.Context
	lease               ServingLease
	identity            Identity
	authMu              sync.Mutex
	mu                  sync.Mutex
	open                bool
	ceiling             []string
	modelCeiling        []string
	publicationCeiling  []string
	pending             map[*requestOperation]bool
	finished            chan struct{}
	finishErr           error
	observed            map[string]bool
	modelObserved       map[string]bool
	publicationObserved map[string]bool
}

func (q *ManagedRequest) GenerationUUID() string { return q.lease.Attempt.GenerationUUID }
func (q *ManagedRequest) Reference(nodeID string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.open || !slices.Contains(q.ceiling, nodeID) {
		return "", ErrDenied
	}
	if err := q.ctx.Err(); err != nil {
		return "", err
	}
	return graph.FormatReference(q.lease.Generation.context.GraphID, q.GenerationUUID(), nodeID)
}
func (q *ManagedRequest) enter() (*requestOperation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.open {
		return nil, ErrClosed
	}
	if err := q.ctx.Err(); err != nil {
		return nil, err
	}
	op := &requestOperation{done: make(chan struct{})}
	q.pending[op] = true
	return op, nil
}
func (q *ManagedRequest) leave(op *requestOperation, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	op.err = err
	delete(q.pending, op)
	close(op.done)
}
func (q *ManagedRequest) node(reference string) (string, error) {
	ref, err := graph.ParseReference(reference)
	if err != nil {
		return "", err
	}
	if ref.GraphID() != q.lease.Generation.context.GraphID || ref.GenerationUUID() != q.GenerationUUID() {
		return "", ErrDenied
	}
	q.mu.Lock()
	selected := slices.Contains(q.modelCeiling, ref.NodeID())
	q.mu.Unlock()
	if !selected {
		return "", ErrDenied
	}
	if open, err := q.lease.Generation.plan.Index().ModelMayOpen(ref.NodeID()); err != nil || !open {
		return "", ErrDenied
	}
	q.mu.Lock()
	if q.modelObserved == nil {
		q.modelObserved = map[string]bool{}
	}
	q.modelObserved[ref.NodeID()] = true
	q.mu.Unlock()
	return ref.NodeID(), nil
}
func (q *ManagedRequest) authorized(operation, id string) (Identity, error) {
	q.authMu.Lock()
	defer q.authMu.Unlock()
	q.mu.Lock()
	ceiling := slices.Clone(q.ceiling)
	q.mu.Unlock()
	if !slices.Contains(ceiling, id) {
		return Identity{}, ErrDenied
	}
	current, identity, err := q.runtime.authorize(q.ctx, q.identity, operation, ceiling)
	if err != nil {
		return Identity{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ceiling = intersect(q.ceiling, current)
	if !slices.Contains(q.ceiling, id) {
		return Identity{}, ErrDenied
	}
	if q.observed == nil {
		q.observed = map[string]bool{}
	}
	q.observed[id] = true
	return identity, nil
}
func (q *ManagedRequest) Inspect(reference string) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	id, err := q.node(reference)
	if err != nil {
		return result, err
	}
	if _, err := q.authorized("inspect", id); err != nil {
		return result, err
	}
	plan := q.lease.Generation.plan
	node, err := plan.Index().Get(id, "id")
	if err != nil {
		return result, err
	}
	presentation, _ := plan.Presentation(id)
	data := map[string]any{"reference": reference, "kind": string(node.Kind()), "name": node.Name(),
		"title": presentation.Title, "description": presentation.Description}
	if binding, ok := plan.Binding(id); ok {
		data["effect"], data["input_schema"] = binding.Effect(), binding.Registration().InputSchema()
	}
	return graph.FreezeJSON(data)
}
func (q *ManagedRequest) Invoke(reference string, arguments any, readOnly bool) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	argumentsCopy, err := graph.FreezeJSON(arguments)
	if err != nil {
		return result, err
	}
	id, err := q.node(reference)
	if err != nil {
		return result, err
	}
	binding, ok := q.lease.Generation.plan.Binding(id)
	if !ok || (readOnly && binding.Effect() != "read_only") {
		return result, ErrDenied
	}
	operation := "invoke"
	if readOnly {
		operation = "invoke_read_only"
	}
	identity, err := q.authorized(operation, id)
	if err != nil {
		return result, err
	}
	resource, err := q.lease.Resources.Handle(id)
	if err != nil {
		return result, err
	}
	result, err = binding.Registration().Call(q.ctx, argumentsCopy.JSON(), CallContext{Identity: identity, Resource: resource})
	if err != nil {
		return result, err
	}
	return result, q.ctx.Err()
}

// Finish stops escaped/new calls, then joins entered operations, including a
// non-cooperative native handler. It never uses cancellation to fake drain.
func (q *ManagedRequest) Finish() error {
	q.mu.Lock()
	if q.finished != nil {
		done := q.finished
		q.mu.Unlock()
		<-done
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.finishErr
	}
	q.open = false
	q.finished = make(chan struct{})
	pending := make([]*requestOperation, 0, len(q.pending))
	for op := range q.pending {
		pending = append(pending, op)
	}
	q.mu.Unlock()
	var err error
	for _, op := range pending {
		<-op.done
		err = errors.Join(err, op.err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.finishErr = err
	close(q.finished)
	return err
}
