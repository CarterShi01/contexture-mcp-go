package server

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

type GraphOwnerContext struct {
	ActorRef        string
	Identity        managed.Identity
	HostPermissions []string
}
type GraphApproval struct {
	ActorID, TenantID, AuthorityRevision, SubjectDigest, ExpiresAt string
}
type GraphAdmission struct {
	Context                                   GraphOwnerContext
	GraphID, Operation                        string
	Operations                                []string
	BaseRevision, BaseDigest, CandidateDigest string
	Impact, ReferenceTargets                  []string
	ApprovalSubjectDigest                     string
	Approval                                  *GraphApproval
	PayloadRefs                               []managed.FrozenJSON
	Release                                   *ReleaseHostAdmission
}
type GraphAdmissionDecision struct {
	Allowed           bool
	AuthorityRevision string
	ScopeIDs          []string
	ApprovalRequired  bool
}
type GraphOwnerHost interface {
	Current(context.Context) (GraphOwnerContext, error)
	VerifyApproval(context.Context, GraphOwnerContext, string, string, string) (GraphApproval, error)
	Decide(context.Context, GraphAdmission) (GraphAdmissionDecision, error)
	Fence(context.Context, GraphOwnerContext, func() error) error
}
type GraphOwnerSerialization interface {
	Run(func() error) error
}

// SynchronousJournalSerialization grants serialization only, never policy,
// identity or approvals. Concurrent/reentrant operations refuse as busy.
type SynchronousJournalSerialization struct{ mu sync.Mutex }

func (s *SynchronousJournalSerialization) Run(action func() error) error {
	if !s.mu.TryLock() {
		return releaseError("resource_busy", "Shared Journal operation already active")
	}
	defer s.mu.Unlock()
	return action()
}

type GraphOwnerOptions struct {
	Host                 GraphOwnerHost
	Serialization        GraphOwnerSerialization
	Clock                func() string
	PublicationContracts map[string]managed.PublicationContract
	Limits               *managed.TransactionLimits
}
type issuedGraphPreview struct {
	storeID, expiresAt, digest string
	context                    GraphOwnerContext
	request                    managed.FrozenJSON
	definition                 *managed.GraphDefinition
	prepared                   *managed.PurePreparedTransaction
}

// GraphPreview is an owner-issued native capability. Receipt bytes alone cannot
// reconstruct it; retaining it never retains identity or approval authority.
type GraphPreview struct {
	receipt managed.FrozenJSON
	issued  *issuedGraphPreview
}

func (p *GraphPreview) Receipt() managed.FrozenJSON {
	if p == nil {
		return managed.FrozenJSON{}
	}
	return p.receipt
}
func (GraphPreview) MarshalJSON() ([]byte, error) {
	return nil, errors.New("graph preview capabilities cannot be serialized; use Receipt")
}
func (GraphPreview) MarshalText() ([]byte, error) {
	return nil, errors.New("graph preview capabilities cannot be serialized; use Receipt")
}
func (GraphPreview) String() string   { return "<managed graph preview>" }
func (GraphPreview) GoString() string { return "<managed graph preview>" }

type FileJournalGraphOwner struct {
	journal                     *FileJournal
	storeID                     string
	host                        GraphOwnerHost
	serialization               GraphOwnerSerialization
	clock                       func() string
	validation                  managed.GraphJournalProjectionOptions
	limits                      managed.TransactionLimits
	mu                          sync.Mutex
	closed, stopped             atomic.Bool
	confirmedSeq, confirmedHash string
	confirmedProjection         *managed.GraphJournalProjection
}

func capturedGraphOptions(options managed.GraphJournalProjectionOptions) managed.GraphJournalProjectionOptions {
	out := managed.GraphJournalProjectionOptions{}
	if options.PublicationContracts != nil {
		out.PublicationContracts = map[string]managed.PublicationContract{}
		for id, contract := range options.PublicationContracts {
			out.PublicationContracts[id] = contract
		}
	}
	return out
}
func GraphJournalProjector(options managed.GraphJournalProjectionOptions) JournalProjector {
	captured := capturedGraphOptions(options)
	return func(records []JournalObject) (JournalObject, error) {
		checkpoint, err := managed.ProjectGraphJournal(records, captured)
		if err != nil {
			return nil, err
		}
		return journalMap(checkpoint.JSON()), nil
	}
}
func NewFileJournalGraphOwner(journal *FileJournal, options GraphOwnerOptions) (out *FileJournalGraphOwner, err error) {
	defer releaseCatch(&err)
	if journal == nil || options.Host == nil || options.Serialization == nil || options.Clock == nil {
		return nil, releaseError("authority_required", "Physical Journal, current Host, shared serialization and UTC clock required")
	}
	if journal.options.Projector == nil {
		return nil, releaseError("projection_required", "Complete native Journal projection required")
	}
	limits := managed.DefaultTransactionLimits()
	if options.Limits != nil {
		limits = *options.Limits
	}
	if err := graph.ValidateTransactionLimits(limits); err != nil {
		return nil, err
	}
	if err := journal.AssertWritable(); err != nil {
		return nil, err
	}
	out = &FileJournalGraphOwner{journal: journal, storeID: releaseText(journal.Meta()["store_id"]),
		host: options.Host, serialization: options.Serialization, clock: options.Clock, limits: limits,
		validation: capturedGraphOptions(managed.GraphJournalProjectionOptions{PublicationContracts: options.PublicationContracts}), confirmedSeq: "0"}
	if !journal.mu.TryLock() {
		return nil, releaseError("resource_busy", "Journal is busy")
	}
	if journal.graphOwner != nil {
		journal.mu.Unlock()
		return nil, releaseError("resource_busy", "Journal already has a graph owner")
	}
	journal.graphOwner = out
	journal.mu.Unlock()
	if _, err := out.replay(); err != nil {
		journal.mu.Lock()
		if journal.graphOwner == out {
			journal.graphOwner = nil
		}
		journal.mu.Unlock()
		return nil, err
	}
	return out, nil
}
func (o *FileJournalGraphOwner) StoreID() string     { return o.storeID }
func (o *FileJournalGraphOwner) WritesStopped() bool { return o.closed.Load() || o.stopped.Load() }
func (o *FileJournalGraphOwner) Close() error {
	if !o.mu.TryLock() {
		return releaseError("resource_busy", "Cannot detach active graph owner")
	}
	defer o.mu.Unlock()
	if !o.journal.mu.TryLock() {
		return releaseError("resource_busy", "Physical Journal is active")
	}
	defer o.journal.mu.Unlock()
	o.closed.Store(true)
	o.confirmedProjection = nil
	if o.journal.graphOwner == o {
		o.journal.graphOwner = nil
	}
	return nil
}
func (o *FileJournalGraphOwner) ready() error {
	if o.closed.Load() {
		return releaseError("closed", "Graph owner is closed")
	}
	if o.stopped.Load() {
		return releaseError("commit_unknown", "Graph owner requires physical Journal recovery")
	}
	if err := o.journal.AssertWritable(); err != nil {
		var coded *JournalError
		if errors.As(err, &coded) && (coded.Code() == "closed" || coded.Code() == "commit_unknown") {
			o.stopped.Store(true)
		}
		return err
	}
	return nil
}
func (o *FileJournalGraphOwner) replay() (*managed.GraphJournalProjection, error) {
	watermark, err := o.journal.Watermark()
	if err != nil {
		return nil, err
	}
	if o.confirmedProjection != nil && o.confirmedProjection.Watermark() == watermark {
		return o.confirmedProjection, nil
	}
	after := "0"
	if o.confirmedProjection != nil {
		after = o.confirmedProjection.Watermark()
	}
	records, err := o.journal.Records(after)
	if err != nil {
		return nil, err
	}
	var projection *managed.GraphJournalProjection
	if o.confirmedProjection == nil {
		projection, err = managed.NewGraphJournalProjection(records, o.validation)
	} else {
		projection, err = o.confirmedProjection.Extend(records)
	}
	if err != nil {
		return nil, err
	}
	if projection.StoreID() != "" && projection.StoreID() != o.storeID {
		return nil, releaseError("contract_mismatch", "Journal physical store origin differs")
	}
	if projection.Watermark() != watermark {
		return nil, releaseError("commit_unknown", "Accepted suffix watermark differs")
	}
	o.confirmedSeq, o.confirmedHash = projection.Watermark(), ""
	if len(records) != 0 {
		o.confirmedHash = releaseText(records[len(records)-1]["record_hash"])
	}
	o.confirmedProjection = projection
	return projection, nil
}
func cloneGraphContext(value GraphOwnerContext) GraphOwnerContext {
	value.HostPermissions = slices.Clone(value.HostPermissions)
	return value
}
func sameGraphContext(a, b GraphOwnerContext) bool {
	return a.ActorRef == b.ActorRef && a.Identity == b.Identity && slices.Equal(a.HostPermissions, b.HostPermissions)
}
func (o *FileJournalGraphOwner) identity(ctx context.Context) (GraphOwnerContext, error) {
	if err := ctx.Err(); err != nil {
		return GraphOwnerContext{}, err
	}
	explicit, err := managed.IdentityFromContext(ctx)
	if err != nil {
		return GraphOwnerContext{}, err
	}
	current, err := o.host.Current(ctx)
	if err != nil {
		return GraphOwnerContext{}, err
	}
	current = cloneGraphContext(current)
	for _, id := range []string{current.ActorRef, current.Identity.ActorID, current.Identity.TenantID, current.Identity.AuthorityRevision} {
		if _, err := graph.ValidateID(id); err != nil {
			return GraphOwnerContext{}, managed.ErrDenied
		}
	}
	if explicit.ActorID != current.Identity.ActorID || explicit.TenantID != current.Identity.TenantID {
		return GraphOwnerContext{}, managed.ErrDenied
	}
	for _, permission := range current.HostPermissions {
		if _, err := graph.ValidateID(permission); err != nil {
			return GraphOwnerContext{}, managed.ErrDenied
		}
	}
	slices.Sort(current.HostPermissions)
	current.HostPermissions = slices.Compact(current.HostPermissions)
	return current, nil
}

// graphDispatch checks one-shot synchronous native capabilities and preserves
// action errors even when a faulty trusted port ignores its return value.
func graphDispatch(port func(func() error) error, action func() error) (err error) {
	defer releaseCatch(&err)
	var mu sync.Mutex
	open, entered := true, false
	var outcome error
	err = func() (err error) {
		defer releaseCatch(&err)
		return port(func() (actionErr error) {
			mu.Lock()
			defer mu.Unlock()
			if !open || entered {
				outcome = managed.ErrDenied
				return outcome
			}
			entered = true
			defer func() { outcome = errors.Join(outcome, actionErr) }()
			defer releaseCatch(&actionErr)
			return action()
		})
	}()
	mu.Lock()
	open = false
	called, actionErr := entered, outcome
	mu.Unlock()
	if !called && err == nil {
		err = managed.ErrDenied
	}
	if actionErr != nil && (err == nil || !errors.Is(err, actionErr)) {
		err = errors.Join(err, actionErr)
	}
	return err
}
func (o *FileJournalGraphOwner) check(ctx context.Context, current GraphOwnerContext, watermark string) error {
	if err := o.ready(); err != nil {
		return err
	}
	latest, err := o.identity(ctx)
	if err != nil || !sameGraphContext(latest, current) {
		return releaseError("approval_stale", "Current graph Host identity/authority changed")
	}
	actual, err := o.journal.Watermark()
	if err != nil {
		return err
	}
	if actual != watermark {
		return releaseError("approval_stale", "Accepted Journal cut changed")
	}
	return nil
}
func (o *FileJournalGraphOwner) operate(ctx context.Context, sharedAppend bool, action func(GraphOwnerContext, *managed.GraphJournalProjection) error) (err error) {
	if !o.mu.TryLock() {
		return releaseError("resource_busy", "Graph owner callback/concurrent reentry refused")
	}
	defer o.mu.Unlock()
	defer releaseCatch(&err)
	if err := o.ready(); err != nil {
		return err
	}
	err = graphDispatch(o.serialization.Run, func() error {
		current, err := o.identity(ctx)
		if err != nil {
			return err
		}
		cut, err := o.replay()
		if err != nil {
			return err
		}
		return graphDispatch(func(dispatch func() error) error { return o.host.Fence(ctx, cloneGraphContext(current), dispatch) }, func() error {
			if err := o.check(ctx, current, cut.Watermark()); err != nil {
				return err
			}
			if err := action(current, cut); err != nil {
				return err
			}
			watermark, err := o.journal.Watermark()
			if err != nil {
				return err
			}
			if watermark != o.confirmedSeq {
				if !sharedAppend {
					o.stopped.Store(true)
					return releaseError("commit_unknown", "Unexpected Journal publication; recover physical custody")
				}
				_, err = o.replay()
			}
			return err
		})
	})
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() == "commit_unknown" {
		o.stopped.Store(true)
	}
	return err
}

var graphUTCPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z$`)

func graphUTC(value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Year() < 1 || !graphUTCPattern.MatchString(value) {
		return releaseError("schema_invalid", "Canonical UTC timestamp required")
	}
	return nil
}
func compareGraphUTC(a, b string) (int, error) {
	if err := graphUTC(a); err != nil {
		return 0, err
	}
	if err := graphUTC(b); err != nil {
		return 0, err
	}
	if c := strings.Compare(a[:19], b[:19]); c != 0 {
		return c, nil
	}
	x, y := strings.TrimPrefix(a[19:len(a)-1], "."), strings.TrimPrefix(b[19:len(b)-1], ".")
	width := max(len(x), len(y))
	return strings.Compare(x+strings.Repeat("0", width-len(x)), y+strings.Repeat("0", width-len(y))), nil
}
func (o *FileJournalGraphOwner) approval(ctx context.Context, current GraphOwnerContext, reference, subject, now string) (*GraphApproval, error) {
	if reference == "" {
		return nil, nil
	}
	if _, err := graph.ValidateID(reference); err != nil {
		return nil, err
	}
	if err := graphUTC(now); err != nil {
		return nil, err
	}
	proof, err := o.host.VerifyApproval(ctx, cloneGraphContext(current), reference, subject, now)
	if err != nil {
		return nil, err
	}
	expires, err := compareGraphUTC(proof.ExpiresAt, now)
	if err != nil || expires <= 0 || proof.ActorID != current.Identity.ActorID || proof.TenantID != current.Identity.TenantID ||
		proof.AuthorityRevision != current.Identity.AuthorityRevision || proof.SubjectDigest != subject {
		return nil, releaseError("approval_stale", "Approval does not bind current actor/authority/exact subject/expiry")
	}
	return &proof, nil
}
func detachGraphAdmission(value GraphAdmission) GraphAdmission {
	value.Context = cloneGraphContext(value.Context)
	value.Operations, value.Impact, value.ReferenceTargets = slices.Clone(value.Operations), slices.Clone(value.Impact), slices.Clone(value.ReferenceTargets)
	value.PayloadRefs = slices.Clone(value.PayloadRefs)
	if value.Approval != nil {
		copy := *value.Approval
		value.Approval = &copy
	}
	if value.Release != nil {
		copy := detachedReleaseAdmission(*value.Release)
		value.Release = &copy
	}
	return value
}
func (o *FileJournalGraphOwner) decide(ctx context.Context, admission GraphAdmission, partial bool) ([]string, error) {
	decision, err := o.host.Decide(ctx, detachGraphAdmission(admission))
	if err != nil {
		return nil, err
	}
	scope := slices.Clone(decision.ScopeIDs)
	for _, id := range scope {
		if _, err := graph.ValidateID(id); err != nil {
			return nil, managed.ErrDenied
		}
	}
	slices.Sort(scope)
	scope = slices.Compact(scope)
	if !decision.Allowed || decision.AuthorityRevision != admission.Context.Identity.AuthorityRevision {
		return nil, managed.ErrDenied
	}
	if !partial {
		for _, id := range append(slices.Clone(admission.Impact), admission.ReferenceTargets...) {
			if !slices.Contains(scope, id) {
				return nil, managed.ErrDenied
			}
		}
	}
	if decision.ApprovalRequired && admission.Approval == nil {
		return nil, releaseError("approval_stale", "Current policy requires exact approval")
	}
	return scope, nil
}
func graphStateIDs(snapshot managed.RevisionState) []string {
	definition := snapshot.Definition()
	ids := []string{}
	for _, node := range definition.Nodes() {
		ids = append(ids, node.NodeID())
	}
	return ids
}
func graphAdmission(current GraphOwnerContext, graphID, operation string, snapshot *managed.RevisionState) GraphAdmission {
	digest, _ := graph.CanonicalDigest(nil, "contexture.graph/v1")
	admission := GraphAdmission{Context: cloneGraphContext(current), GraphID: graphID, Operation: operation,
		BaseRevision: "0", BaseDigest: digest, CandidateDigest: digest, Operations: []string{}, Impact: []string{}, ReferenceTargets: []string{}, PayloadRefs: []managed.FrozenJSON{}}
	if snapshot != nil {
		admission.BaseRevision = snapshot.Revision()
		admission.BaseDigest, _ = snapshot.DefinitionDigest()
		admission.CandidateDigest, admission.Impact = admission.BaseDigest, graphStateIDs(*snapshot)
	}
	return admission
}
func (o *FileJournalGraphOwner) readSnapshot(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, graphID, revision string) (managed.RevisionState, error) {
	history, err := cut.History(graphID)
	if err != nil {
		return managed.RevisionState{}, err
	}
	head := history.Head()
	if _, err := o.decide(ctx, graphAdmission(current, graphID, "graph.read", &head), false); err != nil {
		return managed.RevisionState{}, err
	}
	snapshot := head
	if revision != "" {
		snapshot, err = history.At(revision)
		if err != nil {
			return snapshot, err
		}
	}
	_, err = o.decide(ctx, graphAdmission(current, graphID, "graph.read", &snapshot), false)
	return snapshot, err
}
func (o *FileJournalGraphOwner) Read(ctx context.Context, graphID, revision string) (result managed.RevisionState, err error) {
	defer func() {
		if err != nil {
			result = managed.RevisionState{}
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		var err error
		result, err = o.readSnapshot(ctx, current, cut, graphID, revision)
		if err != nil {
			return err
		}
		return o.check(ctx, current, cut.Watermark())
	})
	return result, err
}
func (o *FileJournalGraphOwner) History(ctx context.Context, graphID string) (result managed.History, err error) {
	defer func() {
		if err != nil {
			result = managed.History{}
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		var err error
		result, err = cut.History(graphID)
		if err != nil {
			return err
		}
		for _, record := range result.Records() {
			state := record.State()
			if _, err := o.decide(ctx, graphAdmission(current, graphID, "graph.read", &state), false); err != nil {
				return err
			}
		}
		return o.check(ctx, current, cut.Watermark())
	})
	return result, err
}
func (o *FileJournalGraphOwner) Export(ctx context.Context, graphID, revision string) ([]byte, error) {
	state, err := o.Read(ctx, graphID, revision)
	if err != nil {
		return nil, err
	}
	definition := state.Definition()
	return graph.CanonicalJSON(definition.AsJSON())
}
func (o *FileJournalGraphOwner) Receipt(ctx context.Context, graphID, key string) (result managed.FrozenJSON, err error) {
	defer func() {
		if err != nil {
			result = managed.FrozenJSON{}
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		if _, err := graph.ValidateID(key); err != nil {
			return err
		}
		if _, err := o.readSnapshot(ctx, current, cut, graphID, ""); err != nil {
			return err
		}
		receipt, err := o.journal.GetReceipt(current.ActorRef, key)
		if err != nil {
			return err
		}
		if err := o.check(ctx, current, cut.Watermark()); err != nil {
			return err
		}
		if receipt != nil && receipt["graph_id"] == graphID {
			result, err = managed.FreezeJSON(receipt)
		}
		return err
	})
	return result, err
}
func (o *FileJournalGraphOwner) prior(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, graphID, key, digest string) (JournalObject, error) {
	receipt, err := o.journal.GetReceipt(current.ActorRef, key)
	if err != nil || receipt == nil {
		return nil, err
	}
	if _, err := o.readSnapshot(ctx, current, cut, releaseText(receipt["graph_id"]), ""); err != nil {
		return nil, err
	}
	if receipt["graph_id"] != graphID || receipt["request_digest"] != digest {
		return nil, releaseError("idempotency_conflict", "Actor/key accepted a different command")
	}
	return receipt, o.check(ctx, current, cut.Watermark())
}
func graphObject(value any, required, optional string) (JournalObject, error) {
	frozen, err := managed.FreezeJSON(value)
	if err != nil {
		return nil, err
	}
	m, ok := frozen.JSON().(map[string]any)
	if !ok {
		return nil, releaseError("schema_invalid", "Expected closed graph object")
	}
	allowed := strings.Fields(required + " " + optional)
	for _, key := range strings.Fields(required) {
		if _, ok := m[key]; !ok {
			return nil, releaseError("schema_invalid", "Missing graph field: "+key)
		}
	}
	for key := range m {
		if !slices.Contains(allowed, key) {
			return nil, releaseError("schema_invalid", "Unknown graph field: "+key)
		}
	}
	return m, nil
}
func (o *FileJournalGraphOwner) budget(definition *managed.GraphDefinition) error {
	index, err := graph.BuildGraphIndex(definition, o.validation.PublicationContracts)
	if err != nil {
		return err
	}
	if len(definition.Nodes()) > o.limits.MaxNodes || len(journalBytes(definition.AsJSON())) > o.limits.MaxDefinitionBytes {
		return releaseError("limit_exceeded", "Definition processing budget exceeded")
	}
	edges := len(definition.Roots()) + len(definition.Publications())
	for _, node := range definition.Nodes() {
		edges += len(node.Uses()) + len(node.Members().Placements()) + len(node.Instructions().Parts())
		location, _ := index.Location(node.NodeID())
		if location.Depth > o.limits.MaxDepth || len(journalBytes(node.AsJSON())) > o.limits.MaxNodeBytes {
			return releaseError("limit_exceeded", "Node processing budget exceeded")
		}
	}
	if edges > o.limits.MaxEdges {
		return releaseError("limit_exceeded", "Edge processing budget exceeded")
	}
	return nil
}
func (o *FileJournalGraphOwner) payload(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, graphID string, value any) ([]byte, error) {
	ref, err := graphObject(value, "digest size media_type", "")
	if err != nil {
		return nil, err
	}
	size, err := graph.ParseCounter(releaseText(ref["size"]))
	if err != nil || ref["media_type"] != "application/json" || size.BitLen() > 63 || size.Int64() > int64(o.limits.MaxDefinitionBytes) {
		return nil, releaseError("limit_exceeded", "Canonical definition blob budget exceeded")
	}
	var snapshot *managed.RevisionState
	if history, err := cut.History(graphID); err == nil {
		head := history.Head()
		snapshot = &head
	}
	admission := graphAdmission(current, graphID, "graph.payload", snapshot)
	frozen, err := managed.FreezeJSON(ref)
	if err != nil {
		return nil, err
	}
	admission.PayloadRefs = []managed.FrozenJSON{frozen}
	if _, err := o.decide(ctx, admission, false); err != nil {
		return nil, err
	}
	if err := o.check(ctx, current, cut.Watermark()); err != nil {
		return nil, err
	}
	return o.journal.GetBlob(ref)
}
func (o *FileJournalGraphOwner) bootstrap(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, value any) (request JournalObject, definition *managed.GraphDefinition, digest string, err error) {
	if !slices.Contains(current.HostPermissions, "graph.bootstrap") {
		return nil, nil, "", managed.ErrDenied
	}
	if raw, ok := value.([]byte); ok {
		if len(raw) > o.limits.MaxTransactionBytes {
			return nil, nil, "", releaseError("limit_exceeded", "Bootstrap byte budget exceeded")
		}
		value, err = managed.DecodeJSON(raw)
		if err != nil {
			return
		}
	}
	request, err = graphObject(value, "schema graph_id expected_revision seed approval_ref idempotency_key reason correlation_id", "")
	if err != nil {
		return
	}
	if request["schema"] != "contexture.bootstrap/v1" || request["expected_revision"] != "0" || releaseText(request["reason"]) == "" {
		err = releaseError("schema_invalid", "Closed native bootstrap envelope required")
		return
	}
	for _, field := range []string{"graph_id", "approval_ref", "idempotency_key", "correlation_id"} {
		if _, err = graph.ValidateID(releaseText(request[field])); err != nil {
			return
		}
	}
	if len(journalBytes(request)) > o.limits.MaxTransactionBytes {
		err = releaseError("limit_exceeded", "Bootstrap envelope budget exceeded")
		return
	}
	payload, e := graphObject(request["seed"], "", "inline blob")
	if e != nil || len(payload) != 1 {
		err = releaseError("schema_invalid", "Exactly one bootstrap payload required")
		return
	}
	var raw any
	if value, ok := payload["inline"]; ok {
		raw = value
	} else {
		var data []byte
		data, err = o.payload(ctx, current, cut, releaseText(request["graph_id"]), payload["blob"])
		if err != nil {
			return
		}
		raw, err = managed.DecodeJSON(data)
		if err != nil {
			return
		}
		if !bytes.Equal(data, journalBytes(raw)) {
			err = releaseError("schema_invalid", "Definition blob must contain canonical JSON")
			return
		}
	}
	definition, err = managed.NewGraphDefinition(raw, o.validation.PublicationContracts)
	if err != nil {
		return
	}
	if definition.GraphID() != request["graph_id"] {
		err = releaseError("identity_conflict", "Bootstrap graph identity differs")
		return
	}
	if err = o.budget(definition); err != nil {
		return
	}
	if len(journalBytes([]any{JournalObject{"type": "GraphSeeded", "before": nil, "after": definition.AsJSON()}})) > o.limits.MaxResolvedBytes {
		err = releaseError("limit_exceeded", "Bootstrap fact budget exceeded")
		return
	}
	seedDigest, _ := definition.Digest()
	digest, err = graph.CanonicalDigest(JournalObject{"schema": "contexture.bootstrap/v1", "graph_id": definition.GraphID(),
		"expected_revision": "0", "seed_digest": seedDigest, "reason": request["reason"]}, "contexture.command/v1")
	return
}
func (o *FileJournalGraphOwner) PreviewBootstrap(ctx context.Context, value any, expiresAt string) (result *GraphPreview, err error) {
	defer func() {
		if err != nil {
			result = nil
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		request, definition, digest, err := o.bootstrap(ctx, current, cut, value)
		if err != nil {
			return err
		}
		if _, err := cut.History(definition.GraphID()); err == nil {
			return releaseError("revision_conflict", "Bootstrap requires unseeded graph")
		}
		now := o.clock()
		remaining, err := compareGraphUTC(expiresAt, now)
		if err != nil || remaining <= 0 {
			return releaseError("approval_stale", "Preview expiry must be in the future")
		}
		proof, err := o.approval(ctx, current, releaseText(request["approval_ref"]), digest, now)
		if err != nil {
			return err
		}
		admission := graphAdmission(current, definition.GraphID(), "graph.bootstrap.preview", nil)
		admission.Operations, admission.ApprovalSubjectDigest, admission.Approval = []string{"bootstrap"}, digest, proof
		admission.CandidateDigest, _ = definition.Digest()
		for _, node := range definition.Nodes() {
			admission.Impact = append(admission.Impact, node.NodeID())
		}
		if _, err := o.decide(ctx, admission, false); err != nil {
			return err
		}
		if proof == nil {
			return releaseError("approval_stale", "Bootstrap requires exact approval")
		}
		if err := o.check(ctx, current, cut.Watermark()); err != nil {
			return err
		}
		if remaining, err := compareGraphUTC(expiresAt, o.clock()); err != nil || remaining <= 0 {
			return releaseError("approval_stale", "Preview expired during admission")
		}
		receipt, err := managed.FreezeJSON(JournalObject{"graph_id": definition.GraphID(), "request_digest": digest, "expires_at": expiresAt})
		if err != nil {
			return err
		}
		frozen, err := managed.FreezeJSON(request)
		if err != nil {
			return err
		}
		result = &GraphPreview{receipt, &issuedGraphPreview{o.storeID, expiresAt, digest, cloneGraphContext(current), frozen, definition, nil}}
		return nil
	})
	return result, err
}
func (o *FileJournalGraphOwner) txOptions(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, request managed.TransactionEnvelope) (graph.PrepareOptions, error) {
	options := graph.PrepareOptions{Limits: &o.limits, PublicationContracts: o.validation.PublicationContracts}
	raw := journalMap(request.AsJSON())
	for _, value := range journalArray(raw["commands"]) {
		command := journalMap(value)
		if command["op"] == "restore_nodes" {
			snapshot, err := o.readSnapshot(ctx, current, cut, releaseText(raw["graph_id"]), releaseText(command["revision"]))
			if err != nil {
				return options, err
			}
			options.History = append(options.History, snapshot)
		}
		if payload, ok := command["payload"].(map[string]any); ok {
			if ref, ok := payload["blob"]; ok {
				data, err := o.payload(ctx, current, cut, releaseText(raw["graph_id"]), ref)
				if err != nil {
					return options, err
				}
				options.Payloads = append(options.Payloads, data)
			}
		}
	}
	return options, nil
}
func (o *FileJournalGraphOwner) txAdmission(current GraphOwnerContext, prepared managed.PurePreparedTransaction, operation string, proof *GraphApproval) (GraphAdmission, error) {
	if (prepared.RootOperations() || prepared.Protection().RootsChanged()) && !slices.Contains(current.HostPermissions, "graph.roots") {
		return GraphAdmission{}, managed.ErrDenied
	}
	base := prepared.Base()
	definition := base.Definition()
	admission := graphAdmission(current, definition.GraphID(), operation, &base)
	admission.CandidateDigest, _ = func() (string, error) { candidate := prepared.Candidate(); return candidate.Digest() }()
	admission.Approval, admission.ApprovalSubjectDigest = proof, prepared.ApprovalSubjectDigest()
	admission.Impact, admission.ReferenceTargets = prepared.Protection().Impact(), prepared.Protection().ReferenceTargets()
	for _, value := range journalArray(journalMap(prepared.Envelope().AsJSON())["commands"]) {
		admission.Operations = append(admission.Operations, releaseText(journalMap(value)["op"]))
	}
	return admission, nil
}
func (o *FileJournalGraphOwner) Preview(ctx context.Context, value any, expiresAt string) (result *GraphPreview, err error) {
	defer func() {
		if err != nil {
			result = nil
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		if request, ok := value.(managed.TransactionEnvelope); ok {
			value = request.AsJSON()
		}
		request, err := managed.NewTransactionEnvelope(value, o.limits)
		if err != nil {
			return err
		}
		raw := journalMap(request.AsJSON())
		state, err := o.readSnapshot(ctx, current, cut, releaseText(raw["graph_id"]), "")
		if err != nil {
			return err
		}
		options, err := o.txOptions(ctx, current, cut, request)
		if err != nil {
			return err
		}
		prepared, err := graph.Prepare(state, request, options)
		if err != nil {
			return err
		}
		now := o.clock()
		if remaining, err := compareGraphUTC(expiresAt, now); err != nil || remaining <= 0 {
			return releaseError("approval_stale", "Preview expiry must be in the future")
		}
		ref, _ := raw["approval_ref"].(string)
		proof, err := o.approval(ctx, current, ref, prepared.ApprovalSubjectDigest(), now)
		if err != nil {
			return err
		}
		admission, err := o.txAdmission(current, prepared, "graph.preview", proof)
		if err != nil {
			return err
		}
		if _, err := o.decide(ctx, admission, false); err != nil {
			return err
		}
		if err := o.check(ctx, current, cut.Watermark()); err != nil {
			return err
		}
		if remaining, err := compareGraphUTC(expiresAt, o.clock()); err != nil || remaining <= 0 {
			return releaseError("approval_stale", "Preview expired during admission")
		}
		candidate, base := prepared.Candidate(), state.Definition()
		diff, err := graph.DefinitionDiffBetween(&base, &candidate)
		if err != nil {
			return err
		}
		commandDigest, _ := request.CommandDigest()
		baseDigest, _ := state.DefinitionDigest()
		candidateDigest, _ := candidate.Digest()
		receipt, err := managed.FreezeJSON(JournalObject{"schema": "contexture.preview-receipt/v1", "base_revision": state.Revision(), "base_digest": baseDigest,
			"candidate_digest": candidateDigest, "command_digest": commandDigest, "resolved_fact_digest": prepared.FactsDigest(), "definition_diff": diff.AsJSON(),
			"impact": stringsAny(prepared.Protection().Impact()), "validation_report": []any{}, "approval_subject": prepared.ApprovalSubject(), "expires_at": expiresAt})
		if err != nil {
			return err
		}
		frozen, err := managed.FreezeJSON(request.AsJSON())
		if err != nil {
			return err
		}
		result = &GraphPreview{receipt, &issuedGraphPreview{o.storeID, expiresAt, commandDigest, cloneGraphContext(current), frozen, nil, &prepared}}
		return nil
	})
	return result, err
}
func stringsAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
func (o *FileJournalGraphOwner) persist(ctx context.Context, current GraphOwnerContext, cut *managed.GraphJournalProjection, event managed.FrozenJSON, request JournalObject, digest, revision string, proof *GraphApproval, expiresAt string) (managed.FrozenJSON, error) {
	rawEvent := journalMap(event.JSON())
	resultDigest, _ := rawEvent["after_digest"].(string)
	if resultDigest == "" {
		history, err := cut.History(releaseText(request["graph_id"]))
		if err != nil {
			return managed.FrozenJSON{}, err
		}
		resultDigest, _ = history.Head().DefinitionDigest()
	}
	sequence, err := releaseNext(cut.Watermark())
	if err != nil {
		return managed.FrozenJSON{}, err
	}
	status := "committed"
	if rawEvent["type"] == "CommandCompleted" {
		status = "noop"
	}
	receipt := JournalObject{"schema": "contexture.command-receipt/v1", "graph_id": request["graph_id"], "actor_ref": current.ActorRef,
		"idempotency_key": request["idempotency_key"], "request_digest": digest, "revision": revision, "journal_seq": sequence, "status": status, "durability": o.journal.durability,
		"operation_result": JournalObject{"inline": JournalObject{"graph_id": request["graph_id"], "revision": revision, "definition_digest": resultDigest}}}
	if err := o.check(ctx, current, cut.Watermark()); err != nil {
		return managed.FrozenJSON{}, err
	}
	now := o.clock()
	if err := graphUTC(now); err != nil {
		return managed.FrozenJSON{}, err
	}
	if proof != nil {
		if remaining, err := compareGraphUTC(proof.ExpiresAt, now); err != nil || remaining <= 0 {
			return managed.FrozenJSON{}, releaseError("approval_stale", "Approval expired before append")
		}
	}
	if remaining, err := compareGraphUTC(expiresAt, now); err != nil || remaining <= 0 {
		return managed.FrozenJSON{}, releaseError("approval_stale", "Preview expired before append")
	}
	if err := o.check(ctx, current, cut.Watermark()); err != nil {
		return managed.FrozenJSON{}, err
	}
	accepted, err := o.journal.Append([]any{event.JSON()}, JournalAppendOptions{ActorRef: current.ActorRef, CommandReceipt: receipt,
		CorrelationID: releaseText(request["correlation_id"]), WrittenAt: now})
	if err != nil {
		o.stopped.Store(true)
		return managed.FrozenJSON{}, err
	}
	if accepted["journal_seq"] != sequence {
		o.stopped.Store(true)
		return managed.FrozenJSON{}, releaseError("commit_unknown", "Accepted graph sequence differs")
	}
	after, err := o.replay()
	if err != nil || after.Watermark() != sequence {
		o.stopped.Store(true)
		return managed.FrozenJSON{}, errors.Join(err, releaseError("commit_unknown", "Accepted graph projection differs"))
	}
	return managed.FreezeJSON(receipt)
}

type GraphCommitOptions struct{ ApprovalRef string }

func (o *FileJournalGraphOwner) Commit(ctx context.Context, preview *GraphPreview, options GraphCommitOptions) (result managed.FrozenJSON, err error) {
	defer func() {
		if err != nil {
			result = managed.FrozenJSON{}
		}
	}()
	err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		if preview == nil || preview.issued == nil || preview.issued.storeID != o.storeID {
			return releaseError("schema_invalid", "Same-store owner-issued preview capability required")
		}
		issued := preview.issued
		request := journalMap(issued.request.JSON())
		graphID := releaseText(request["graph_id"])
		prior, err := o.prior(ctx, current, cut, graphID, releaseText(request["idempotency_key"]), issued.digest)
		if err != nil {
			return err
		}
		if prior != nil {
			result, err = managed.FreezeJSON(prior)
			return err
		}
		remaining, err := compareGraphUTC(issued.expiresAt, o.clock())
		if err != nil || remaining <= 0 || !sameGraphContext(issued.context, current) {
			return releaseError("approval_stale", "Preview context/expiry changed; re-preview required")
		}
		if issued.prepared == nil {
			normalized, definition, digest, err := o.bootstrap(ctx, current, cut, request)
			if err != nil {
				return err
			}
			if _, err := cut.History(graphID); err == nil {
				return releaseError("revision_conflict", "Bootstrap no longer matches unseeded graph")
			}
			actualDigest, _ := definition.Digest()
			expectedDigest, _ := issued.definition.Digest()
			if digest != issued.digest || actualDigest != expectedDigest {
				return releaseError("revision_conflict", "Bootstrap proposal changed")
			}
			proof, err := o.approval(ctx, current, releaseText(request["approval_ref"]), digest, o.clock())
			if err != nil {
				return err
			}
			if proof == nil {
				return releaseError("approval_stale", "Bootstrap requires exact approval")
			}
			admission := graphAdmission(current, graphID, "graph.bootstrap", nil)
			admission.Operations, admission.CandidateDigest, admission.ApprovalSubjectDigest, admission.Approval = []string{"bootstrap"}, actualDigest, digest, proof
			for _, node := range definition.Nodes() {
				admission.Impact = append(admission.Impact, node.NodeID())
			}
			if _, err := o.decide(ctx, admission, false); err != nil {
				return err
			}
			unseeded, _ := graph.NewUnseededState(graphID)
			event, err := graph.BootstrapEvent(unseeded, definition, graph.EventOptions{CommandDigest: digest, Reason: releaseText(request["reason"])}, releaseText(request["approval_ref"]))
			if err != nil {
				return err
			}
			result, err = o.persist(ctx, current, cut, event, normalized, digest, "1", proof, issued.expiresAt)
			return err
		}
		envelope, err := managed.NewTransactionEnvelope(request, o.limits)
		if err != nil {
			return err
		}
		state, err := o.readSnapshot(ctx, current, cut, graphID, "")
		if err != nil {
			return err
		}
		txOptions, err := o.txOptions(ctx, current, cut, envelope)
		if err != nil {
			return err
		}
		fresh, err := graph.RecheckPrepared(state, *issued.prepared, txOptions)
		if err != nil {
			return err
		}
		ref := options.ApprovalRef
		if ref == "" {
			ref, _ = request["approval_ref"].(string)
		}
		proof, err := o.approval(ctx, current, ref, fresh.ApprovalSubjectDigest(), o.clock())
		if err != nil {
			return err
		}
		admission, err := o.txAdmission(current, fresh, "graph.transition", proof)
		if err != nil {
			return err
		}
		if _, err := o.decide(ctx, admission, false); err != nil {
			return err
		}
		candidate := fresh.Candidate()
		change, err := graph.ResolveFacts(state, &candidate)
		if err != nil {
			return err
		}
		if !journalEqual(change.Facts(), fresh.Facts()) || !journalEqual(change.Tombstones(), fresh.Tombstones()) {
			return releaseError("unsupported_contract", "Transaction and event kernels disagree on accepted facts")
		}
		commandDigest, _ := envelope.CommandDigest()
		event, err := graph.ChangeEvent(state, change, graph.EventOptions{CommandDigest: commandDigest, Reason: releaseText(request["reason"])})
		if err != nil {
			return err
		}
		result, err = o.persist(ctx, current, cut, event, request, commandDigest, fresh.ResultRevision(), proof, issued.expiresAt)
		return err
	})
	return result, err
}
func (o *FileJournalGraphOwner) Recover(ctx context.Context, journal *FileJournal) (err error) {
	if !o.mu.TryLock() {
		return releaseError("resource_busy", "Graph owner is active")
	}
	defer o.mu.Unlock()
	defer releaseCatch(&err)
	if journal == nil || o.closed.Load() {
		return releaseError("closed", "Cannot rebind absent/closed graph custody")
	}
	return graphDispatch(o.serialization.Run, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := journal.AssertWritable(); err != nil {
			return err
		}
		if journal.Meta()["store_id"] != o.storeID || o.stopped.Load() && journal == o.journal {
			return releaseError("contract_mismatch", "Recovered physical store origin/custody differs")
		}
		records, err := journal.Records("0")
		if err != nil {
			return err
		}
		projection, err := managed.NewGraphJournalProjection(records, o.validation)
		if err != nil {
			return err
		}
		confirmed, err := strconv.Atoi(o.confirmedSeq)
		if err != nil || len(records) < confirmed || confirmed > 0 && records[confirmed-1]["record_hash"] != o.confirmedHash {
			return releaseError("contract_mismatch", "Recovered accepted hash prefix differs")
		}
		if !journal.mu.TryLock() {
			return releaseError("resource_busy", "Recovered Journal is busy")
		}
		if journal.graphOwner != nil && journal.graphOwner != o {
			journal.mu.Unlock()
			return releaseError("resource_busy", "Recovered Journal already has a graph owner")
		}
		journal.graphOwner = o
		journal.mu.Unlock()
		if o.journal != journal {
			o.journal.mu.Lock()
			if o.journal.graphOwner == o {
				o.journal.graphOwner = nil
			}
			o.journal.mu.Unlock()
		}
		o.journal = journal
		o.confirmedProjection = projection
		o.confirmedSeq, o.confirmedHash = projection.Watermark(), ""
		if len(records) != 0 {
			o.confirmedHash = releaseText(records[len(records)-1]["record_hash"])
		}
		o.stopped.Store(false)
		return nil
	})
}
