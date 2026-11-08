package server

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

type JournalHistoryView struct {
	StoreID, Watermark string
	Records            []JournalObject
}
type VerifiedReleaseHistory struct {
	Snapshot                        managed.RevisionState
	StoreID, JournalSeq, RecordHash string
}
type ReleaseHistoryReader interface {
	Read(string, string, JournalHistoryView) (VerifiedReleaseHistory, error)
}
type ReleaseCompilePort interface {
	Verify(managed.ReleaseRequest, VerifiedReleaseHistory, ReleaseHostContext) (*managed.StructuralPlan, error)
}
type ReleaseHostContext struct {
	ActorRef string
	Identity managed.Identity
}
type ReleaseCompilationProof struct {
	Kind           string
	Manifest       *managed.ExecutionManifest
	ManifestDigest string
	Receipt        managed.FrozenJSON
}
type ReleaseHostAdmission struct {
	Context       ReleaseHostContext
	Operation     string
	Request       managed.ReleaseRequest
	RequestDigest string
	History       *VerifiedReleaseHistory
	Compilation   ReleaseCompilationProof
}

// ReleaseHostPort is required current authority, shared with the graph owner.
// Proof callbacks and Fence are synchronous and I/O-free. Fence must invoke its
// action once during its call under the same identity/admission/approval and
// graph-authoring serialization. Non-pointer actions may append Journal facts.
type ReleaseHostPort interface {
	Current() (ReleaseHostContext, error)
	VerifyApproval(ReleaseHostAdmission) (any, error)
	Admit(ReleaseHostAdmission, any) error
	Fence(ReleaseHostAdmission, any, func() error) error
}
type ReleaseGraphProjection struct {
	Graphs, ProjectionVersions, BlobRefs managed.FrozenJSON
}
type ReleaseGraphProjector func([]JournalObject) (ReleaseGraphProjection, error)

func ReleaseJournalProjector(graphs ReleaseGraphProjector) (JournalProjector, error) {
	if graphs == nil {
		return nil, &JournalError{"projection_required", "Independent complete graph projection required"}
	}
	return func(records []JournalObject) (out JournalObject, err error) {
		defer releaseCatch(&err)
		base, err := graphs(journalRecordsCopy(records))
		if err != nil {
			return nil, err
		}
		state, err := managed.ReplayReleases(records)
		if err != nil {
			return nil, err
		}
		out = journalMap(state.AsJSON())
		out["graphs"], out["projection_versions"], out["blob_refs"] = base.Graphs.JSON(), base.ProjectionVersions.JSON(), base.BlobRefs.JSON()
		receipts := []any{}
		for _, record := range records {
			if receipt, ok := record["command_receipt"]; ok {
				receipts = append(receipts, journalCopy(receipt))
			}
		}
		slices.SortFunc(receipts, func(a, b any) int {
			left, right := journalMap(a), journalMap(b)
			return journalTupleCompare([]string{releaseText(left["actor_ref"]), releaseText(left["idempotency_key"])}, []string{releaseText(right["actor_ref"]), releaseText(right["idempotency_key"])})
		})
		out["receipts_index"] = receipts
		return out, nil
	}, nil
}

type FileJournalReleaseOptions struct {
	GraphID, TargetID string
	Host              ReleaseHostPort
	History           ReleaseHistoryReader
	Compilation       ReleaseCompilePort
}
type localReleaseReady struct {
	attempt managed.ServingAttempt
	ready   *managed.ReadyGeneration
	adopted bool
}

// FileJournalReleaseOwner stores actual release/receipt/terminal/observation
// facts. External GraphOwner/Host proof ports never replace those durable facts.
type FileJournalReleaseOwner struct {
	graphID, targetID, storeID  string
	journal                     *FileJournal
	host                        ReleaseHostPort
	history                     ReleaseHistoryReader
	compilation                 ReleaseCompilePort
	mu                          sync.Mutex
	frozen                      atomic.Bool
	confirmedSeq, confirmedHash string
	ready                       map[string]localReleaseReady
}

func releaseCatch(err *error) {
	if value := recover(); value != nil {
		if e, ok := value.(error); ok {
			*err = e
		} else {
			*err = fmt.Errorf("release proof panic: %v", value)
		}
	}
}
func releaseError(code, message string) error { return &JournalError{code, message} }
func releaseText(value any) string {
	s, ok := value.(string)
	if !ok {
		panic(releaseError("schema_invalid", "Expected release text"))
	}
	return s
}
func releaseNext(value string) (string, error) {
	n, err := graph.ParseCounter(value)
	if err != nil {
		return "", err
	}
	n.Add(n, big.NewInt(1))
	text := n.String()
	_, err = graph.ParseCounter(text)
	return text, err
}

func NewFileJournalReleaseOwner(journal *FileJournal, options FileJournalReleaseOptions) (out *FileJournalReleaseOwner, err error) {
	defer releaseCatch(&err)
	if journal == nil || options.Host == nil || options.History == nil || options.Compilation == nil {
		return nil, releaseError("authority_required", "Journal and all trusted Host/history/compile ports required")
	}
	if journal.options.Projector == nil {
		return nil, releaseError("projection_required", "Complete independent Journal projection required")
	}
	for _, id := range []string{options.GraphID, options.TargetID} {
		if _, err := graph.ValidateID(id); err != nil {
			return nil, err
		}
	}
	if err := journal.AssertWritable(); err != nil {
		return nil, err
	}
	store := releaseText(journal.Meta()["store_id"])
	if _, err := graph.ValidateID(store); err != nil {
		return nil, err
	}
	out = &FileJournalReleaseOwner{graphID: options.GraphID, targetID: options.TargetID, storeID: store, journal: journal,
		host: options.Host, history: options.History, compilation: options.Compilation, ready: map[string]localReleaseReady{}, confirmedSeq: "0"}
	_, _, err = out.read()
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (o *FileJournalReleaseOwner) GraphID() string   { return o.graphID }
func (o *FileJournalReleaseOwner) TargetID() string  { return o.targetID }
func (o *FileJournalReleaseOwner) SwapsFrozen() bool { return o.frozen.Load() }
func (o *FileJournalReleaseOwner) freezeReadiness(err error) {
	var journalErr *JournalError
	if errors.As(err, &journalErr) && (journalErr.Code() == "commit_unknown" || journalErr.Code() == "closed") {
		o.frozen.Store(true)
	}
}
func (o *FileJournalReleaseOwner) boundary(recovery bool, action func() error) (err error) {
	if !o.mu.TryLock() {
		return releaseError("resource_busy", "Release owner is busy or reentered")
	}
	defer o.mu.Unlock()
	defer releaseCatch(&err)
	if !recovery {
		if o.frozen.Load() {
			return releaseError("resource_busy", "Release owner requires Journal recovery")
		}
		if err := o.journal.AssertWritable(); err != nil {
			o.freezeReadiness(err)
			return err
		}
	}
	return action()
}
func (o *FileJournalReleaseOwner) read() (managed.ReleaseState, JournalHistoryView, error) {
	records, err := o.journal.Records("0")
	if err != nil {
		return managed.ReleaseState{}, JournalHistoryView{}, err
	}
	state, err := managed.ReplayReleases(records)
	if err != nil {
		return managed.ReleaseState{}, JournalHistoryView{}, err
	}
	seq := strconv.Itoa(len(records))
	hash := ""
	if len(records) != 0 {
		hash = releaseText(records[len(records)-1]["record_hash"])
	}
	o.confirmedSeq, o.confirmedHash = seq, hash
	for key, local := range o.ready {
		if local.ready.ResourceState() != "prepared" {
			delete(o.ready, key)
		}
	}
	return state, JournalHistoryView{o.storeID, seq, records}, nil
}
func (o *FileJournalReleaseOwner) scope(request managed.ReleaseRequest) error {
	if request.GraphID() != o.graphID || request.TargetID() != o.targetID {
		return releaseError("contract_mismatch", "Release graph/target differs")
	}
	return nil
}
func (o *FileJournalReleaseOwner) current() (ReleaseHostContext, error) {
	current, err := o.host.Current()
	if err != nil {
		return current, err
	}
	for _, id := range []string{current.ActorRef, current.Identity.ActorID, current.Identity.TenantID, current.Identity.AuthorityRevision} {
		if _, err := graph.ValidateID(id); err != nil {
			return current, managed.ErrDenied
		}
	}
	return current, nil
}
func (o *FileJournalReleaseOwner) checkedCompile(request managed.ReleaseRequest, history VerifiedReleaseHistory, current ReleaseHostContext) (*managed.StructuralPlan, error) {
	plan, err := o.compilation.Verify(request, history, current)
	if err != nil {
		return nil, err
	}
	if !plan.IsCompiled() {
		return nil, releaseError("contract_mismatch", "Current independent compile evidence absent")
	}
	definition := plan.Definition()
	digest, err := definition.Digest()
	if err != nil || definition.GraphID() != request.GraphID() || digest != request.DefinitionDigest() || plan.Manifest().Digest() != request.ManifestDigest() {
		return nil, releaseError("contract_mismatch", "Current independent compile evidence differs")
	}
	return plan, nil
}
func detachedReleaseAdmission(admission ReleaseHostAdmission) ReleaseHostAdmission {
	if admission.History != nil {
		copy := *admission.History
		admission.History = &copy
	}
	return admission
}
func (o *FileJournalReleaseOwner) hostFence(admission ReleaseHostAdmission, view JournalHistoryView, check func() error, action func() error) error {
	proof, err := o.host.VerifyApproval(detachedReleaseAdmission(admission))
	if err != nil {
		return err
	}
	if err := o.host.Admit(detachedReleaseAdmission(admission), proof); err != nil {
		return err
	}
	var gate sync.Mutex
	open, entered := true, false
	var outcome error
	callback := func() (callbackErr error) {
		gate.Lock()
		defer gate.Unlock()
		if !open || entered {
			return managed.ErrDenied
		}
		entered = true
		defer func() { outcome = callbackErr }()
		current, err := o.current()
		if err != nil || current != admission.Context {
			return managed.ErrDenied
		}
		watermark, err := o.journal.Watermark()
		if err != nil || watermark != view.Watermark {
			return releaseError("permission_denied", "Accepted Journal watermark changed during proof")
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		current, err = o.current()
		if err != nil || current != admission.Context {
			return managed.ErrDenied
		}
		watermark, err = o.journal.Watermark()
		if err != nil || watermark != view.Watermark {
			return releaseError("permission_denied", "Final Journal watermark changed")
		}
		if err := o.journal.AssertWritable(); err != nil {
			o.freezeReadiness(err)
			return err
		}
		return action()
	}
	err = func() (err error) {
		defer releaseCatch(&err)
		return o.host.Fence(detachedReleaseAdmission(admission), proof, callback)
	}()
	gate.Lock()
	open = false
	wasEntered := entered
	actionErr := outcome
	gate.Unlock()
	if err == nil && !wasEntered {
		return managed.ErrDenied
	}
	return errors.Join(err, actionErr)
}
func (o *FileJournalReleaseOwner) authorize(operation string, request managed.ReleaseRequest, view JournalHistoryView, terminalFailure bool, action func(ReleaseHostAdmission) error) error {
	if err := o.scope(request); err != nil {
		return err
	}
	current, err := o.current()
	if err != nil {
		return err
	}
	history, err := o.history.Read(request.GraphID(), request.Revision(), JournalHistoryView{view.StoreID, view.Watermark, journalRecordsCopy(view.Records)})
	if err != nil {
		return err
	}
	n, err := graph.ParseCounter(history.JournalSeq)
	if err != nil || n.Sign() <= 0 || history.StoreID != o.storeID {
		return releaseError("contract_mismatch", "Historical accepted anchor differs")
	}
	found := false
	for _, record := range view.Records {
		if record["journal_seq"] == history.JournalSeq && record["record_hash"] == history.RecordHash {
			found = true
		}
	}
	definition := history.Snapshot.Definition()
	digest, err := history.Snapshot.DefinitionDigest()
	if err != nil || !found || history.Snapshot.Revision() != request.Revision() || definition.GraphID() != request.GraphID() || digest != request.DefinitionDigest() {
		return releaseError("contract_mismatch", "Verified immutable historical provenance differs")
	}
	compilation := ReleaseCompilationProof{Kind: "terminal_failure", ManifestDigest: request.ManifestDigest()}
	if !terminalFailure {
		plan, err := o.checkedCompile(request, history, current)
		if err != nil {
			return err
		}
		compilation = ReleaseCompilationProof{Kind: "verified", Manifest: plan.Manifest()}
	}
	requestDigest, err := request.Digest()
	if err != nil {
		return err
	}
	admission := ReleaseHostAdmission{current, operation, request, requestDigest, &history, compilation}
	check := func() error {
		if terminalFailure {
			return nil
		}
		_, err := o.checkedCompile(request, history, current)
		return err
	}
	return o.hostFence(admission, view, check, func() error { return action(admission) })
}
func (o *FileJournalReleaseOwner) append(state managed.ReleaseState, view JournalHistoryView, events []any, actor string, receipt JournalObject) error {
	next, err := releaseNext(view.Watermark)
	if err != nil {
		return err
	}
	record := JournalObject{"journal_seq": next, "actor_ref": actor, "events": events}
	if receipt != nil {
		record["command_receipt"] = receipt
	}
	for _, event := range events {
		state, err = managed.ReduceReleaseEvent(state, event, record)
		if err != nil {
			return err
		}
	}
	accepted, err := o.journal.Append(events, JournalAppendOptions{ActorRef: actor, CommandReceipt: receipt})
	if err != nil {
		o.frozen.Store(true)
		return err
	}
	if accepted["journal_seq"] != next {
		o.frozen.Store(true)
		return releaseError("commit_unknown", "Accepted release sequence differs; recover custody")
	}
	_, _, err = o.read()
	if err != nil {
		o.frozen.Store(true)
	}
	return err
}
func (o *FileJournalReleaseOwner) desired(state managed.ReleaseState) JournalObject {
	for _, value := range state.Items("desired_releases") {
		item := journalMap(value)
		request, err := managed.ParseReleaseRequest(item["request"])
		if err != nil {
			panic(err)
		}
		if request.GraphID() == o.graphID && request.TargetID() == o.targetID {
			return item
		}
	}
	return nil
}
func releaseID(request managed.ReleaseRequest, epoch string) JournalObject {
	return JournalObject{"graph_id": request.GraphID(), "target_id": request.TargetID(), "release_epoch": epoch}
}
func operationFor(state managed.ReleaseState, id any) JournalObject {
	for _, value := range state.Items("release_operations") {
		op := journalMap(value)
		if op["status"] == "requested" && journalEqual(op["release"], id) {
			return op
		}
	}
	return nil
}
func (o *FileJournalReleaseOwner) originalReceipt(request managed.ReleaseRequest, receipt JournalObject, state managed.ReleaseState) (managed.FrozenJSON, error) {
	digest, _ := request.Digest()
	if receipt["request_digest"] != digest || receipt["graph_id"] != request.GraphID() {
		return managed.FrozenJSON{}, releaseError("idempotency_conflict", "Actor/key accepted another operation")
	}
	result, err := managed.ParseReleaseReceipt(journalMap(receipt["operation_result"])["inline"])
	if err != nil {
		return result, err
	}
	r := journalMap(result.JSON())
	id := journalMap(r["release"])
	if id["graph_id"] != request.GraphID() || id["target_id"] != request.TargetID() || r["request_digest"] != digest {
		return result, releaseError("idempotency_conflict", "Actor/key belongs to another operation")
	}
	op := operationFor(state, id)
	if receipt["status"] != "committed" || receipt["journal_seq"] != r["journal_seq"] || op == nil || op["journal_seq"] != r["journal_seq"] || op["request_digest"] != digest {
		return result, releaseError("journal_corrupt", "Receipt has no matching actual release fact")
	}
	return result, nil
}
func (o *FileJournalReleaseOwner) Request(value any) (result managed.FrozenJSON, err error) {
	err = o.boundary(false, func() error {
		request, err := managed.ParseReleaseRequest(value)
		if err != nil {
			return err
		}
		if err := o.scope(request); err != nil {
			return err
		}
		current, err := o.current()
		if err != nil {
			return err
		}
		state, view, err := o.read()
		if err != nil {
			return err
		}
		receipt, err := o.journal.GetReceipt(current.ActorRef, request.IdempotencyKey())
		if err != nil {
			return err
		}
		digest, _ := request.Digest()
		if receipt != nil {
			original, err := o.originalReceipt(request, receipt, state)
			if err != nil {
				return err
			}
			admission := ReleaseHostAdmission{Context: current, Operation: "release.status", Request: request, RequestDigest: digest,
				Compilation: ReleaseCompilationProof{Kind: "receipt_read", Receipt: original}}
			return o.hostFence(admission, view, nil, func() error { result = original; return nil })
		}
		previous, expected := o.desired(state), "0"
		if previous != nil {
			expected = releaseText(previous["release_epoch"])
		}
		if request.ExpectedDesiredEpoch() != expected {
			return releaseError("revision_conflict", "Desired release epoch changed")
		}
		epoch, err := releaseNext(expected)
		if err != nil {
			return err
		}
		return o.authorize("release.request", request, view, false, func(admission ReleaseHostAdmission) error {
			if current != admission.Context {
				return managed.ErrDenied
			}
			seq, err := releaseNext(view.Watermark)
			if err != nil {
				return err
			}
			result, err = managed.ParseReleaseReceipt(JournalObject{"schema": "contexture.release-receipt/v1", "release": releaseID(request, epoch),
				"journal_seq": seq, "request_digest": digest, "status": "requested"})
			if err != nil {
				return err
			}
			events := []any{}
			if previous != nil {
				events = append(events, JournalObject{"type": "ReleaseSuperseded", "release": releaseID(request, expected), "superseded_by": epoch})
			}
			events = append(events, JournalObject{"type": "ReleaseRequested", "request": request.AsJSON(), "release_epoch": epoch})
			receipt := JournalObject{"schema": "contexture.command-receipt/v1", "graph_id": o.graphID, "actor_ref": current.ActorRef,
				"idempotency_key": request.IdempotencyKey(), "request_digest": digest, "revision": request.Revision(), "journal_seq": seq,
				"status": "committed", "durability": o.journal.durability, "operation_result": JournalObject{"inline": result.JSON()}}
			return o.append(state, view, events, current.ActorRef, receipt)
		})
	})
	if err != nil {
		return managed.FrozenJSON{}, err
	}
	return result, nil
}
func (o *FileJournalReleaseOwner) Capture() (result *managed.CapturedRelease, err error) {
	err = o.boundary(false, func() error {
		state, view, err := o.read()
		if err != nil {
			return err
		}
		desired := o.desired(state)
		if desired == nil {
			return nil
		}
		request, err := managed.ParseReleaseRequest(desired["request"])
		if err != nil {
			return err
		}
		return o.authorize("release.status", request, view, false, func(admission ReleaseHostAdmission) error {
			op := operationFor(state, releaseID(request, releaseText(desired["release_epoch"])))
			if op == nil {
				return releaseError("journal_corrupt", "Desired release operation absent")
			}
			result = &managed.CapturedRelease{GraphID: o.graphID, TargetID: o.targetID, ReleaseEpoch: releaseText(desired["release_epoch"]),
				JournalSeq: releaseText(op["journal_seq"]), Snapshot: admission.History.Snapshot, ManifestDigest: request.ManifestDigest()}
			_, err := o.requestFor(managed.ServingAttempt{Release: *result}, state, view)
			return err
		})
	})
	return result, err
}
func attemptIdentity(attempt managed.ServingAttempt) JournalObject {
	return JournalObject{"release": JournalObject{"graph_id": attempt.Release.GraphID, "target_id": attempt.Release.TargetID, "release_epoch": attempt.Release.ReleaseEpoch},
		"instance_id": attempt.InstanceID, "boot_id": attempt.BootID, "attempt_id": attempt.AttemptID}
}
func sameAttempt(a, b managed.ServingAttempt) bool {
	left, e1 := a.Release.Snapshot.DefinitionDigest()
	right, e2 := b.Release.Snapshot.DefinitionDigest()
	return e1 == nil && e2 == nil && a.InstanceID == b.InstanceID && a.BootID == b.BootID && a.AttemptID == b.AttemptID && a.GenerationUUID == b.GenerationUUID &&
		a.Release.GraphID == b.Release.GraphID && a.Release.TargetID == b.Release.TargetID && a.Release.ReleaseEpoch == b.Release.ReleaseEpoch &&
		a.Release.JournalSeq == b.Release.JournalSeq && a.Release.ManifestDigest == b.Release.ManifestDigest &&
		a.Release.Snapshot.Revision() == b.Release.Snapshot.Revision() && left == right
}
func terminalFor(state managed.ReleaseState, attempt managed.ServingAttempt) JournalObject {
	for _, value := range state.Items("prepare_attempt_history") {
		item := journalMap(value)
		if journalEqual(item["identity"], attemptIdentity(attempt)) {
			return item
		}
	}
	return nil
}
func (o *FileJournalReleaseOwner) requestFor(attempt managed.ServingAttempt, state managed.ReleaseState, view JournalHistoryView) (managed.ReleaseRequest, error) {
	for _, record := range view.Records {
		for _, value := range journalArray(record["events"]) {
			event := journalMap(value)
			if event["type"] != "ReleaseRequested" || event["release_epoch"] != attempt.Release.ReleaseEpoch {
				continue
			}
			request, err := managed.ParseReleaseRequest(event["request"])
			if err != nil {
				return request, err
			}
			if request.GraphID() != attempt.Release.GraphID || request.TargetID() != attempt.Release.TargetID {
				continue
			}
			if err := o.scope(request); err != nil {
				return request, err
			}
			digest, err := attempt.Release.Snapshot.DefinitionDigest()
			if err != nil || record["journal_seq"] != attempt.Release.JournalSeq || request.Revision() != attempt.Release.Snapshot.Revision() ||
				request.DefinitionDigest() != digest || request.ManifestDigest() != attempt.Release.ManifestDigest {
				return request, releaseError("contract_mismatch", "Captured release differs from actual Journal")
			}
			receipt, ok := record["command_receipt"]
			if !ok {
				return request, releaseError("journal_corrupt", "Release command receipt absent")
			}
			if _, err := o.originalReceipt(request, journalMap(receipt), state); err != nil {
				return request, err
			}
			return request, nil
		}
	}
	return managed.ReleaseRequest{}, releaseError("contract_mismatch", "Captured release is not journaled")
}
func (o *FileJournalReleaseOwner) ServingProvider(provider managed.ServingPreparationProvider) (managed.ServingPreparationProvider, error) {
	if provider == nil {
		return nil, releaseError("provider_required", "Resource preparation provider required")
	}
	return managed.ServingPreparationFunc(func(ctx context.Context, attempt managed.ServingAttempt) (ready *managed.ReadyGeneration, err error) {
		err = o.boundary(false, func() error {
			state, view, err := o.read()
			if err != nil {
				return err
			}
			request, err := o.requestFor(attempt, state, view)
			if err != nil {
				return err
			}
			return o.authorize("release.prepare", request, view, false, func(ReleaseHostAdmission) error { return nil })
		})
		if err == nil {
			ready, err = func() (ready *managed.ReadyGeneration, err error) {
				defer releaseCatch(&err)
				return provider.Prepare(ctx, attempt)
			}()
			if ready != nil && (!managed.IsUnclaimedGeneration(ready) || ready.Context().GenerationUUID != attempt.GenerationUUID) || err == nil && ready == nil {
				ready = nil
				err = errors.Join(err, releaseError("contract_mismatch", "Provider returned a generation it does not own"))
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			candidate := ready
			err = o.boundary(false, func() error {
				state, view, err := o.read()
				if err != nil {
					return err
				}
				request, err := o.requestFor(attempt, state, view)
				if err != nil {
					return err
				}
				err = o.authorize("release.prepare", request, view, false, func(admission ReleaseHostAdmission) error {
					preparation, definition := candidate.Context(), candidate.Plan().Definition()
					digest, err := definition.Digest()
					if err != nil || preparation.GraphID != o.graphID || preparation.Revision != request.Revision() || digest != request.DefinitionDigest() ||
						candidate.Plan().Manifest().Digest() != request.ManifestDigest() || candidate.ResourceState() != "prepared" {
						return releaseError("contract_mismatch", "Prepared generation differs from accepted intent")
					}
					return o.append(state, view, []any{JournalObject{"type": "ReleasePreparationSucceeded", "attempt": attemptIdentity(attempt),
						"generation_id": attempt.GenerationUUID, "manifest_digest": request.ManifestDigest()}}, admission.Context.ActorRef, nil)
				})
				if err == nil {
					o.ready[attempt.AttemptID] = localReleaseReady{attempt: attempt, ready: candidate}
				}
				return err
			})
		}
		if err == nil {
			return ready, nil
		}
		original := err
		if !o.frozen.Load() {
			factErr := o.boundary(false, func() error {
				state, view, err := o.read()
				if err != nil {
					return err
				}
				if terminalFor(state, attempt) != nil {
					return nil
				}
				request, err := o.requestFor(attempt, state, view)
				if err != nil {
					return err
				}
				return o.authorize("release.prepare", request, view, true, func(admission ReleaseHostAdmission) error {
					return o.append(state, view, []any{JournalObject{"type": "ReleasePreparationFailed", "attempt": attemptIdentity(attempt),
						"diagnostics": []any{JournalObject{"code": "preparation_failed", "location": "", "message": "Local preparation did not publish"}}}}, admission.Context.ActorRef, nil)
				})
			})
			original = errors.Join(original, factErr)
		}
		if ready != nil {
			original = errors.Join(original, ready.Retire(context.Background()))
		}
		return nil, original
	}), nil
}
func (o *FileJournalReleaseOwner) Fence(attempt managed.ServingAttempt, ready *managed.ReadyGeneration, adopt func() error) (accepted bool, err error) {
	err = o.boundary(false, func() error {
		state, view, err := o.read()
		if err != nil {
			return err
		}
		request, err := o.requestFor(attempt, state, view)
		if err != nil {
			return err
		}
		local, ok := o.ready[attempt.AttemptID]
		terminal := terminalFor(state, attempt)
		if !ok || local.ready != ready || !sameAttempt(local.attempt, attempt) || ready.ResourceState() != "prepared" ||
			terminal == nil || terminal["status"] != "ready" || terminal["generation_id"] != attempt.GenerationUUID {
			return releaseError("contract_mismatch", "Actual local and durable ready provenance absent")
		}
		desired := o.desired(state)
		if desired == nil || desired["release_epoch"] != attempt.Release.ReleaseEpoch || !journalEqual(desired["request"], request.AsJSON()) {
			delete(o.ready, attempt.AttemptID)
			return nil
		}
		return o.authorize("release.swap", request, view, false, func(ReleaseHostAdmission) error {
			if adopt == nil {
				return managed.ErrDenied
			}
			if err := adopt(); err != nil {
				return err
			}
			local.adopted = true
			o.ready[attempt.AttemptID] = local
			accepted = true
			return nil
		})
	})
	return accepted, err
}
func (o *FileJournalReleaseOwner) Observe(ctx context.Context, attempt managed.ServingAttempt) error {
	return o.boundary(false, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		local, ok := o.ready[attempt.AttemptID]
		if !ok || !local.adopted || !sameAttempt(local.attempt, attempt) || local.ready.ResourceState() != "prepared" {
			return releaseError("permission_denied", "No actual in-process adoption for observation")
		}
		state, view, err := o.read()
		if err != nil {
			return err
		}
		request, err := o.requestFor(attempt, state, view)
		if err != nil {
			return err
		}
		terminal := terminalFor(state, attempt)
		if terminal == nil || terminal["status"] != "ready" || terminal["generation_id"] != attempt.GenerationUUID {
			return releaseError("contract_mismatch", "Observation durable ready provenance absent")
		}
		return o.authorize("release.observe", request, view, false, func(admission ReleaseHostAdmission) error {
			if local.ready.ResourceState() != "prepared" {
				return managed.ErrDenied
			}
			observation := JournalObject{"release": releaseID(request, attempt.Release.ReleaseEpoch), "instance_id": attempt.InstanceID,
				"boot_id": attempt.BootID, "generation_id": attempt.GenerationUUID, "revision": request.Revision(),
				"definition_digest": request.DefinitionDigest(), "manifest_digest": request.ManifestDigest()}
			for _, value := range state.Items("observations_history") {
				item := journalMap(value)
				copy := journalMap(journalCopy(item))
				delete(copy, "observed_at")
				if journalEqual(copy, observation) {
					return nil
				}
			}
			observation["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			return o.append(state, view, []any{JournalObject{"type": "ReplicaObserved", "observation": observation}}, admission.Context.ActorRef, nil)
		})
	})
}
func (o *FileJournalReleaseOwner) Recover(journal *FileJournal) error {
	return o.boundary(true, func() error {
		if err := journal.AssertWritable(); err != nil {
			return err
		}
		if journal.Meta()["store_id"] != o.storeID {
			return releaseError("contract_mismatch", "Recovered store identity differs")
		}
		records, err := journal.Records("0")
		if err != nil {
			return err
		}
		confirmed, err := strconv.Atoi(o.confirmedSeq)
		if err != nil || len(records) < confirmed || confirmed > 0 && records[confirmed-1]["record_hash"] != o.confirmedHash {
			return releaseError("contract_mismatch", "Recovered confirmed accepted prefix differs")
		}
		if _, err := managed.ReplayReleases(records); err != nil {
			return err
		}
		o.journal = journal
		if _, _, err := o.read(); err != nil {
			return err
		}
		o.frozen.Store(false)
		return nil
	})
}

var _ managed.ReleaseAuthority = (*FileJournalReleaseOwner)(nil)
var _ managed.ServingObservationPort = (*FileJournalReleaseOwner)(nil)
