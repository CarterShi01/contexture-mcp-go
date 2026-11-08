package managed

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// CommittedDefinitionBatch is the final immutable state after ALL same-graph
// facts in ONE durable record. Position is private, never a consumer coordinate.
type CommittedDefinitionBatch struct {
	Position string
	State    RevisionState
}
type CommittedBatchBounds struct {
	MaxBatches, MaxBytes int
}
type CommittedBatchWindow struct {
	Before   RevisionState
	Batches  []CommittedDefinitionBatch
	Through  string
	Overflow bool
}
type CommittedReadCut struct {
	Identity                 Identity
	StoreOrigin              string
	ScopeIDs                 []string
	Snapshot                 RevisionState
	Watermark, RetainedAfter string
	ReadBatches              func(context.Context, string, CommittedBatchBounds) (CommittedBatchWindow, error)
}

// CommittedBatchReadPort supplies one owner-serialized, verified committed cut.
// Snapshot/watermark/retention/window/current graph.watch admission MUST share
// that fence. Dispatch exactly once synchronously; never fabricate missing
// history, skip batches, expose uncommitted head or load an unbounded backlog.
type CommittedBatchReadPort interface {
	Read(context.Context, string, func(CommittedReadCut) (FrozenJSON, error)) (FrozenJSON, error)
}
type CommittedBatchReadFunc func(context.Context, string, func(CommittedReadCut) (FrozenJSON, error)) (FrozenJSON, error)

func (f CommittedBatchReadFunc) Read(ctx context.Context, id string, dispatch func(CommittedReadCut) (FrozenJSON, error)) (FrozenJSON, error) {
	return f(ctx, id, dispatch)
}

type WatchProfile struct {
	MaxBatches, MaxBytes, InlineBytes int
	MaxDuration                       time.Duration
	Name                              string
}

func NewWatchProfile(p WatchProfile) (WatchProfile, error) {
	if p.MaxBatches == 0 {
		p.MaxBatches = 64
	}
	if p.MaxBytes == 0 {
		p.MaxBytes = 1 << 20
	}
	if p.InlineBytes == 0 {
		p.InlineBytes = min(p.MaxBytes, 256<<10)
	}
	if p.MaxDuration == 0 {
		p.MaxDuration = time.Second
	}
	if p.Name == "" {
		p.Name = "native-definition-metadata-v1"
	}
	if p.MaxBatches < 1 || p.MaxBatches > 1024 || p.MaxBytes < 1 || p.MaxBytes > 16<<20 ||
		p.InlineBytes < 1 || p.InlineBytes > min(p.MaxBytes, 1<<20) ||
		p.MaxDuration < time.Millisecond || p.MaxDuration > 30*time.Second ||
		p.MaxDuration%time.Millisecond != 0 || len(p.Name) > 128 {
		return WatchProfile{}, readError("schema_invalid")
	}
	return p, nil
}
func (p WatchProfile) asJSON() map[string]any {
	return map[string]any{"max_batches": p.MaxBatches, "max_bytes": p.MaxBytes, "inline_bytes": p.InlineBytes,
		"max_milliseconds": int(p.MaxDuration / time.Millisecond), "name": p.Name}
}

type WatchRequest struct {
	GraphID   string
	Selection []string
	Profile   *WatchProfile
}
type WatchOptions struct {
	Reader            CommittedBatchReadPort
	Signer            *CursorSigner
	Clock             func() time.Duration
	MaxSessions       int
	MaxHandles        int
	MaxRetainedBytes  int
	MaxPayloadBytes   int
	MaxItems          int
	MaxBacklogBatches int
	MaxBacklogBytes   int
	MaxTTL            time.Duration
}
type WatchCounts struct {
	Sessions, Handles, Bytes int
}
type watchFetch struct {
	value    FrozenJSON
	position string
	size     int
	ids      []string
}
type watchSession struct {
	id, graphID, origin, binding, version string
	identity                              Identity
	scope, selection                      []string
	profile                               WatchProfile
	expires                               time.Duration
	positions                             map[string]string
	fetches                               map[string]watchFetch
	delivered                             map[string]string
	size                                  int
}
type watchHandle struct {
	session *watchSession
	fetch   bool
}

// ManagedWatchService is synchronous bounded pull, definition metadata ONLY.
// Complete payloads replace the authorized projection atomically. Persist a
// checkpoint only after applying all its complete inline/fetch payloads.
type ManagedWatchService struct {
	mu       sync.Mutex
	busy     atomic.Bool
	options  WatchOptions
	sessions map[string]*watchSession
	handles  map[string]watchHandle
	bytes    int
	closed   bool
}

func NewManagedWatchService(o WatchOptions) (*ManagedWatchService, error) {
	if o.Reader == nil || o.Signer == nil || o.Signer.Version() == "" {
		return nil, errors.New("explicit committed reader and CursorSigner required")
	}
	for _, item := range []struct {
		value    *int
		fallback int
	}{{&o.MaxSessions, 64}, {&o.MaxHandles, 4096}, {&o.MaxRetainedBytes, 16 << 20},
		{&o.MaxPayloadBytes, 8 << 20}, {&o.MaxItems, 10000}, {&o.MaxBacklogBatches, 1024}, {&o.MaxBacklogBytes, 16 << 20}} {
		v, err := readBound(*item.value, item.fallback)
		if err != nil {
			return nil, err
		}
		*item.value = v
	}
	if o.MaxTTL == 0 {
		o.MaxTTL = time.Minute
	}
	if o.MaxTTL < 0 || o.MaxTTL > time.Hour {
		return nil, readError("schema_invalid")
	}
	if o.Clock == nil {
		began := time.Now()
		o.Clock = func() time.Duration { return time.Since(began) }
	}
	return &ManagedWatchService{options: o, sessions: map[string]*watchSession{}, handles: map[string]watchHandle{}}, nil
}
func (s *ManagedWatchService) Counts() WatchCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WatchCounts{len(s.sessions), len(s.handles), s.bytes}
}
func (s *ManagedWatchService) drop(session *watchSession) {
	if s.sessions[session.id] != session {
		return
	}
	delete(s.sessions, session.id)
	for token := range session.positions {
		delete(s.handles, token)
	}
	for token := range session.fetches {
		delete(s.handles, token)
	}
	s.bytes -= session.size
	session.positions, session.fetches, session.delivered, session.size = nil, nil, nil, 0
}
func (s *ManagedWatchService) discard(session *watchSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session != nil {
		s.drop(session)
	}
}
func (s *ManagedWatchService) sweepLocked() int {
	removed := 0
	for _, session := range s.sessions {
		if s.closed || session.version != s.options.Signer.Version() || s.options.Clock() >= session.expires {
			s.drop(session)
			removed++
		}
	}
	return removed
}
func (s *ManagedWatchService) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sweepLocked()
}
func (s *ManagedWatchService) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.sweepLocked()
}
func watchReadRequest(r WatchRequest) (string, WatchProfile, []string, error) {
	if _, err := graph.ValidateID(r.GraphID); err != nil {
		return "", WatchProfile{}, nil, readError("schema_invalid")
	}
	p := WatchProfile{}
	if r.Profile != nil {
		p = *r.Profile
	}
	p, err := NewWatchProfile(p)
	if err != nil {
		return "", WatchProfile{}, nil, err
	}
	selected, err := readSelection(r.Selection)
	if err != nil {
		return "", WatchProfile{}, nil, err
	}
	binding, err := readEncode(map[string]any{"graph_id": r.GraphID, "selection": nullableReadArray(selected), "profile": p.asJSON()})
	return binding, p, selected, err
}
func validWatchState(state RevisionState, graphID string) error {
	definition := state.Definition()
	if definition.GraphID() != graphID || state.Revision() == "" {
		return readError("contract_mismatch")
	}
	if _, err := graph.ParseCounter(state.Revision()); err != nil {
		return readError("contract_mismatch")
	}
	return nil
}
func validWatchCut(ctx context.Context, cut CommittedReadCut, graphID string) error {
	host, err := IdentityFromContext(ctx)
	if err != nil {
		return err
	}
	if !sameActor(host, cut.Identity) {
		return ErrDenied
	}
	if validateIdentity(cut.Identity) != nil ||
		readOrigin(cut.StoreOrigin) != nil || cut.ReadBatches == nil {
		return readError("contract_mismatch")
	}
	if err := validWatchState(cut.Snapshot, graphID); err != nil {
		return err
	}
	if _, err := readIDs(cut.ScopeIDs); err != nil {
		return err
	}
	retained, e1 := graph.ParseCounter(cut.RetainedAfter)
	watermark, e2 := graph.ParseCounter(cut.Watermark)
	if e1 != nil || e2 != nil || retained.Cmp(watermark) > 0 {
		return readError("contract_mismatch")
	}
	return nil
}
func (s *ManagedWatchService) fresh(ctx context.Context, session *watchSession, cut CommittedReadCut) error {
	if err := validWatchCut(ctx, cut, session.graphID); err != nil {
		return err
	}
	scope, err := readSelection(cut.ScopeIDs)
	if err != nil {
		return readError("contract_mismatch")
	}
	if s.closed || s.sessions[session.id] != session || s.options.Clock() >= session.expires ||
		session.version != s.options.Signer.Version() || session.identity != cut.Identity ||
		session.origin != cut.StoreOrigin || !slices.Equal(session.scope, scope) {
		return readError("resync_required")
	}
	for _, position := range session.positions {
		if compareCounter(position, cut.RetainedAfter) < 0 {
			return readError("history_unavailable")
		}
	}
	return ctx.Err()
}
func (s *ManagedWatchService) deadline(ctx context.Context, session *watchSession, deadline time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.options.Clock() >= min(deadline, session.expires) {
		return readError("resync_required")
	}
	return nil
}
func (s *ManagedWatchService) payload(state RevisionState, scope, selected []string, current *GraphDefinition) (FrozenJSON, int, []string, error) {
	allowed, err := readIDs(scope)
	if err != nil {
		return FrozenJSON{}, 0, nil, err
	}
	attenuation, err := readIDs(selected)
	if err != nil {
		return FrozenJSON{}, 0, nil, err
	}
	existing := map[string]bool{}
	if current != nil {
		for _, n := range current.Nodes() {
			existing[n.NodeID()] = true
		}
	}
	visible := func(id string) bool {
		return allowed[id] && (selected == nil || attenuation[id]) && (current == nil || existing[id])
	}
	definition := state.Definition()
	nodes, roots, ids := []any{}, []any{}, []string{}
	budget := readMaterialBudget{maxItems: s.options.MaxItems, maxBytes: s.options.MaxPayloadBytes}
	for _, n := range definition.Nodes() {
		if !visible(n.NodeID()) {
			continue
		}
		value := map[string]any{"node_id": n.NodeID(), "kind": string(n.Kind()), "name": n.Name(), "description": n.Description()}
		if _, err := budget.add(value, true); err != nil {
			return FrozenJSON{}, 0, nil, err
		}
		nodes, ids = append(nodes, value), append(ids, n.NodeID())
	}
	for _, root := range definition.Roots() {
		if visible(root.NodeID) {
			value := map[string]any{"node_id": root.NodeID, "plane": string(root.Plane)}
			if _, err := budget.add(value, true); err != nil {
				return FrozenJSON{}, 0, nil, err
			}
			roots = append(roots, value)
		}
	}
	value := map[string]any{"nodes": nodes, "roots": roots}
	raw, err := readEncode(value)
	if err != nil {
		return FrozenJSON{}, 0, nil, err
	}
	if len(raw) > s.options.MaxPayloadBytes {
		return FrozenJSON{}, 0, nil, readError("too_large")
	}
	frozen, err := graph.FreezeJSON(value)
	return frozen, len(raw), ids, err
}
func (s *ManagedWatchService) issue(session *watchSession, kind, position string) (string, error) {
	subject, err := readEncode([]any{session.id, kind, position})
	if err != nil {
		return "", err
	}
	return s.options.Signer.issue(subject)
}
func (s *ManagedWatchService) cursor(session *watchSession, position string) (string, error) {
	token, err := s.issue(session, "cursor", position)
	if err != nil {
		return "", err
	}
	if _, ok := session.positions[token]; !ok {
		if len(s.handles) >= s.options.MaxHandles {
			return "", readError("resync_required")
		}
		session.positions[token], s.handles[token] = position, watchHandle{session, false}
	}
	return token, nil
}
func (s *ManagedWatchService) read(ctx context.Context, graphID string, action func(CommittedReadCut) (FrozenJSON, error)) (FrozenJSON, error) {
	return checkedReadFence(ctx, func(dispatch func(CommittedReadCut) (FrozenJSON, error)) (FrozenJSON, error) {
		return s.options.Reader.Read(ctx, graphID, dispatch)
	}, action)
}
func (s *ManagedWatchService) Start(ctx context.Context, r WatchRequest, ttl time.Duration) (result FrozenJSON, err error) {
	if !s.busy.CompareAndSwap(false, true) {
		return result, readError("resource_busy")
	}
	defer s.busy.Store(false)
	binding, profile, selected, err := watchReadRequest(r)
	if err != nil {
		return result, err
	}
	if ttl <= 0 || ttl > s.options.MaxTTL {
		return result, readError("schema_invalid")
	}
	began, version := s.options.Clock(), s.options.Signer.Version()
	var session *watchSession
	defer func() {
		if err != nil {
			s.discard(session)
			result = FrozenJSON{}
		}
	}()
	return s.read(ctx, r.GraphID, func(cut CommittedReadCut) (FrozenJSON, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sweepLocked()
		if err := validWatchCut(ctx, cut, r.GraphID); err != nil {
			return FrozenJSON{}, err
		}
		if s.closed || len(s.sessions) >= s.options.MaxSessions {
			return FrozenJSON{}, readError("resync_required")
		}
		scope, err := readSelection(cut.ScopeIDs)
		if err != nil {
			return FrozenJSON{}, err
		}
		id, err := randomReadID()
		if err != nil {
			return FrozenJSON{}, err
		}
		session = &watchSession{id: id, graphID: r.GraphID, origin: cut.StoreOrigin, binding: binding, version: version,
			identity: cut.Identity, scope: scope, selection: selected, profile: profile, expires: began + ttl,
			positions: map[string]string{}, fetches: map[string]watchFetch{}, delivered: map[string]string{}}
		retained, err := readEncode(map[string]any{"binding": binding, "scope": stringReadArray(scope),
			"actor": cut.Identity.ActorID, "tenant": cut.Identity.TenantID, "authority": cut.Identity.AuthorityRevision, "origin": cut.StoreOrigin})
		if err != nil {
			return FrozenJSON{}, err
		}
		if len(retained) > s.options.MaxRetainedBytes-s.bytes {
			return FrozenJSON{}, readError("resync_required")
		}
		value, _, _, err := s.payload(cut.Snapshot, scope, selected, nil)
		if err != nil {
			return FrozenJSON{}, err
		}
		session.size = len(retained)
		s.sessions[id], s.bytes = session, s.bytes+session.size
		cursor, err := s.cursor(session, cut.Watermark)
		if err != nil {
			return FrozenJSON{}, err
		}
		digest, err := readDigest(value.JSON())
		if err != nil {
			return FrozenJSON{}, err
		}
		out := map[string]any{"snapshot": value.JSON(), "view_digest": digest, "cursor": cursor}
		raw, err := readEncode(out)
		if err != nil {
			return FrozenJSON{}, err
		}
		if len(raw) > profile.MaxBytes {
			return FrozenJSON{}, readError("too_large")
		}
		if err := s.fresh(ctx, session, cut); err != nil {
			return FrozenJSON{}, err
		}
		if err := s.deadline(ctx, session, began+profile.MaxDuration); err != nil {
			return FrozenJSON{}, err
		}
		return graph.FreezeJSON(out)
	})
}
func (s *ManagedWatchService) find(token string, r WatchRequest, fetch bool) (*watchSession, error) {
	binding, _, _, err := watchReadRequest(r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	found, ok := s.handles[token]
	if !ok || !s.options.Signer.valid(token) {
		return nil, readError("resync_required")
	}
	if found.fetch != fetch || found.session.binding != binding {
		s.drop(found.session)
		return nil, readError("resync_required")
	}
	return found.session, nil
}
func validWatchWindow(window CommittedBatchWindow, after string, cut CommittedReadCut, graphID string) error {
	if err := validWatchState(window.Before, graphID); err != nil {
		return err
	}
	through, e1 := graph.ParseCounter(window.Through)
	watermark, e2 := graph.ParseCounter(cut.Watermark)
	position, e3 := graph.ParseCounter(after)
	if e1 != nil || e2 != nil || e3 != nil || through.Cmp(position) < 0 || through.Cmp(watermark) > 0 {
		return readError("contract_mismatch")
	}
	previousRevision := window.Before.Revision()
	for _, batch := range window.Batches {
		seq, err := graph.ParseCounter(batch.Position)
		if err != nil || seq.Cmp(position) <= 0 || seq.Cmp(through) > 0 {
			return readError("contract_mismatch")
		}
		if err := validWatchState(batch.State, graphID); err != nil {
			return err
		}
		if compareCounter(batch.State.Revision(), previousRevision) < 0 {
			return readError("contract_mismatch")
		}
		position, previousRevision = seq, batch.State.Revision()
	}
	return nil
}
func (s *ManagedWatchService) Poll(ctx context.Context, token string, r WatchRequest) (result FrozenJSON, err error) {
	if !s.busy.CompareAndSwap(false, true) {
		return result, readError("resource_busy")
	}
	defer s.busy.Store(false)
	session, err := s.find(token, r, false)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			s.discard(session)
			err = readFailure(err)
			result = FrozenJSON{}
		}
	}()
	deadline := s.options.Clock() + session.profile.MaxDuration
	return s.read(ctx, session.graphID, func(cut CommittedReadCut) (FrozenJSON, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.fresh(ctx, session, cut); err != nil {
			return FrozenJSON{}, err
		}
		position := session.positions[token]
		if compareCounter(position, cut.Watermark) > 0 {
			return FrozenJSON{}, readError("resync_required")
		}
		window, err := cut.ReadBatches(ctx, position, CommittedBatchBounds{s.options.MaxBacklogBatches, s.options.MaxBacklogBytes})
		if err != nil {
			return FrozenJSON{}, err
		}
		if window.Overflow || len(window.Batches) > s.options.MaxBacklogBatches {
			return FrozenJSON{}, readError("resync_required")
		}
		if err := validWatchWindow(window, position, cut, session.graphID); err != nil {
			return FrozenJSON{}, err
		}
		previous, _, _, err := s.payload(window.Before, session.scope, session.selection, nil)
		if err != nil {
			return FrozenJSON{}, err
		}
		current := cut.Snapshot.Definition()
		batches, examined := []any{}, 0
		for _, batch := range window.Batches {
			if err := s.deadline(ctx, session, deadline); err != nil {
				return FrozenJSON{}, err
			}
			if examined >= session.profile.MaxBatches {
				break
			}
			value, size, ids, err := s.payload(batch.State, session.scope, session.selection, &current)
			if err != nil {
				return FrozenJSON{}, err
			}
			if !sameReadJSON(previous, value) {
				seqcursor, err := s.issue(session, "cursor", batch.Position)
				if err != nil {
					return FrozenJSON{}, err
				}
				fetch, err := s.issue(session, "fetch", batch.Position)
				if err != nil {
					return FrozenJSON{}, err
				}
				batchID, err := s.issue(session, "batch", batch.Position)
				if err != nil {
					return FrozenJSON{}, err
				}
				digest, err := readDigest(value.JSON())
				if err != nil {
					return FrozenJSON{}, err
				}
				if prior, delivered := session.delivered[batch.Position]; delivered && prior != digest {
					return FrozenJSON{}, readError("resync_required")
				}
				descriptor := map[string]any{"batch_id": batchID, "facts_digest": digest, "size": size, "seqcursor": seqcursor}
				if size <= session.profile.InlineBytes {
					descriptor["payload"] = value.JSON()
				} else {
					descriptor["authorized_fetch_ref"] = fetch
				}
				responseSize := func() (int, error) {
					entries := append(slices.Clone(batches), descriptor)
					raw, err := readEncode(map[string]any{"batches": entries, "cursor": seqcursor})
					return len(raw), err
				}
				responseBytes, err := responseSize()
				if err != nil {
					return FrozenJSON{}, err
				}
				if responseBytes > session.profile.MaxBytes {
					delete(descriptor, "payload")
					descriptor["authorized_fetch_ref"] = fetch
					responseBytes, err = responseSize()
					if err != nil {
						return FrozenJSON{}, err
					}
				}
				if responseBytes > session.profile.MaxBytes {
					if len(batches) == 0 {
						return FrozenJSON{}, readError("too_large")
					}
					break
				}
				if _, err := s.cursor(session, batch.Position); err != nil {
					return FrozenJSON{}, err
				}
				if _, delivered := session.delivered[batch.Position]; !delivered {
					added := len(batch.Position) + len(digest)
					if added > s.options.MaxRetainedBytes-s.bytes {
						return FrozenJSON{}, readError("resync_required")
					}
					session.delivered[batch.Position] = digest
					session.size, s.bytes = session.size+added, s.bytes+added
				}
				if _, needed := descriptor["authorized_fetch_ref"]; needed {
					retained, exists := session.fetches[fetch]
					if exists && !sameReadJSON(retained.value, value) {
						return FrozenJSON{}, readError("contract_mismatch")
					}
					if !exists {
						if len(s.handles) >= s.options.MaxHandles || size > s.options.MaxRetainedBytes-s.bytes {
							return FrozenJSON{}, readError("resync_required")
						}
						session.fetches[fetch], s.handles[fetch] = watchFetch{value, batch.Position, size, ids}, watchHandle{session, true}
						session.size, s.bytes = session.size+size, s.bytes+size
					}
				}
				batches = append(batches, descriptor)
			}
			position, previous, examined = batch.Position, value, examined+1
		}
		if examined == len(window.Batches) {
			position = window.Through
		}
		cursor, err := s.cursor(session, position)
		if err != nil {
			return FrozenJSON{}, err
		}
		out := map[string]any{"batches": batches, "cursor": cursor}
		raw, err := readEncode(out)
		if err != nil {
			return FrozenJSON{}, err
		}
		if len(raw) > session.profile.MaxBytes {
			return FrozenJSON{}, readError("too_large")
		}
		if err := s.fresh(ctx, session, cut); err != nil {
			return FrozenJSON{}, err
		}
		if err := s.deadline(ctx, session, deadline); err != nil {
			return FrozenJSON{}, err
		}
		return graph.FreezeJSON(out)
	})
}

// Fetch returns one exact COMPLETE authorized replacement, never chunks or a
// reconstruction from live head. CURRENT admission and stable-ID existence gate it.
func (s *ManagedWatchService) Fetch(ctx context.Context, token string, r WatchRequest) (result FrozenJSON, err error) {
	if !s.busy.CompareAndSwap(false, true) {
		return result, readError("resource_busy")
	}
	defer s.busy.Store(false)
	session, err := s.find(token, r, true)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			s.discard(session)
			err = readFailure(err)
			result = FrozenJSON{}
		}
	}()
	deadline := s.options.Clock() + session.profile.MaxDuration
	return s.read(ctx, session.graphID, func(cut CommittedReadCut) (FrozenJSON, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.fresh(ctx, session, cut); err != nil {
			return FrozenJSON{}, err
		}
		fetch, ok := session.fetches[token]
		if !ok {
			return FrozenJSON{}, readError("resync_required")
		}
		current := cut.Snapshot.Definition()
		existing := map[string]bool{}
		for _, n := range current.Nodes() {
			existing[n.NodeID()] = true
		}
		for _, id := range fetch.ids {
			if !existing[id] {
				return FrozenJSON{}, readError("resync_required")
			}
		}
		if compareCounter(fetch.position, cut.RetainedAfter) < 0 {
			return FrozenJSON{}, readError("history_unavailable")
		}
		if err := s.deadline(ctx, session, deadline); err != nil {
			return FrozenJSON{}, err
		}
		return fetch.value, nil
	})
}
