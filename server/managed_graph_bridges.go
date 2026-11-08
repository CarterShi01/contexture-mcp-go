package server

import (
	"context"
	"strconv"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

func (o *FileJournalGraphOwner) ReleaseGraphProjector() ReleaseGraphProjector {
	validation := capturedGraphOptions(o.validation)
	return func(records []JournalObject) (ReleaseGraphProjection, error) {
		projection, err := managed.ProjectGraphJournal(records, validation)
		if err != nil {
			return ReleaseGraphProjection{}, err
		}
		checkpoint := journalMap(projection.JSON())
		graphs, err := managed.FreezeJSON(checkpoint["graphs"])
		if err != nil {
			return ReleaseGraphProjection{}, err
		}
		versions, err := managed.FreezeJSON(checkpoint["projection_versions"])
		if err != nil {
			return ReleaseGraphProjection{}, err
		}
		refs, err := managed.FreezeJSON(checkpoint["blob_refs"])
		return ReleaseGraphProjection{graphs, versions, refs}, err
	}
}

type graphReleaseHistory struct{ owner *FileJournalGraphOwner }

func (o *FileJournalGraphOwner) ReleaseHistoryReader() ReleaseHistoryReader {
	return graphReleaseHistory{o}
}
func (r graphReleaseHistory) Read(graphID, revision string, view JournalHistoryView) (VerifiedReleaseHistory, error) {
	o := r.owner
	if !o.mu.TryLock() {
		return VerifiedReleaseHistory{}, releaseError("resource_busy", "Graph history owner is active")
	}
	defer o.mu.Unlock()
	if err := o.ready(); err != nil {
		return VerifiedReleaseHistory{}, err
	}
	accepted, err := o.replay()
	if err != nil {
		return VerifiedReleaseHistory{}, err
	}
	actual := accepted.Records()
	if view.StoreID != o.storeID || view.Watermark != strconv.Itoa(len(view.Records)) || len(view.Records) > len(actual) {
		return VerifiedReleaseHistory{}, releaseError("contract_mismatch", "Release history is not an accepted same-store prefix")
	}
	for i, record := range view.Records {
		if record["record_hash"] != actual[i]["record_hash"] {
			return VerifiedReleaseHistory{}, releaseError("contract_mismatch", "Release history accepted prefix differs")
		}
	}
	projection, err := managed.NewGraphJournalProjection(view.Records, o.validation)
	if err != nil {
		return VerifiedReleaseHistory{}, err
	}
	history, err := projection.History(graphID)
	if err != nil {
		return VerifiedReleaseHistory{}, err
	}
	snapshot, err := history.At(revision)
	if err != nil {
		return VerifiedReleaseHistory{}, err
	}
	records := history.Records()
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].State().Revision() == revision {
			position, err := strconv.Atoi(records[i].JournalSeq())
			if err != nil || position < 1 || position > len(view.Records) {
				return VerifiedReleaseHistory{}, releaseError("history_unavailable", "Historical accepted anchor absent")
			}
			return VerifiedReleaseHistory{Snapshot: snapshot, StoreID: o.storeID, JournalSeq: records[i].JournalSeq(),
				RecordHash: releaseText(view.Records[position-1]["record_hash"])}, nil
		}
	}
	return VerifiedReleaseHistory{}, releaseError("history_unavailable", "Exact historical revision is not retained")
}

type graphReleaseHost struct {
	owner   *FileJournalGraphOwner
	context context.Context
}

// ReleaseHost binds an explicit control-plane context to the existing release
// port. The application owns that context's lifetime and principal routing; no
// ambient or goroutine-local identity is introduced.
func (o *FileJournalGraphOwner) ReleaseHost(ctx context.Context) ReleaseHostPort {
	return graphReleaseHost{o, ctx}
}
func (h graphReleaseHost) Current() (ReleaseHostContext, error) {
	current, err := h.owner.identity(h.context)
	return ReleaseHostContext{ActorRef: current.ActorRef, Identity: current.Identity}, err
}
func (o *FileJournalGraphOwner) releaseAdmission(value ReleaseHostAdmission, current GraphOwnerContext, cut *managed.GraphJournalProjection, proof *GraphApproval) (GraphAdmission, error) {
	if value.Context.ActorRef != current.ActorRef || value.Context.Identity != current.Identity {
		return GraphAdmission{}, releaseError("approval_stale", "Release context differs from current graph Host")
	}
	history, err := cut.History(value.Request.GraphID())
	if err != nil {
		return GraphAdmission{}, err
	}
	state := history.Head()
	if value.History != nil {
		state, err = history.At(value.Request.Revision())
		if err != nil {
			return GraphAdmission{}, err
		}
		digest, _ := state.DefinitionDigest()
		supplied, err := value.History.Snapshot.DefinitionDigest()
		if err != nil || supplied != digest || value.History.Snapshot.Revision() != state.Revision() || value.History.StoreID != o.storeID {
			return GraphAdmission{}, releaseError("contract_mismatch", "Release historical proof differs")
		}
		found := false
		for _, record := range cut.Records() {
			found = found || record["journal_seq"] == value.History.JournalSeq && record["record_hash"] == value.History.RecordHash
		}
		if !found {
			return GraphAdmission{}, releaseError("contract_mismatch", "Release accepted historical anchor differs")
		}
	}
	admission := graphAdmission(current, value.Request.GraphID(), value.Operation, &state)
	admission.Operations = []string{value.Operation}
	admission.CandidateDigest, admission.ApprovalSubjectDigest, admission.Approval = value.Request.ManifestDigest(), value.RequestDigest, proof
	copy := detachedReleaseAdmission(value)
	admission.Release = &copy
	return admission, nil
}
func (h graphReleaseHost) proofView() (*managed.GraphJournalProjection, GraphOwnerContext, error) {
	o := h.owner
	if !o.mu.TryLock() {
		return nil, GraphOwnerContext{}, releaseError("resource_busy", "Graph owner proof is active")
	}
	defer o.mu.Unlock()
	if err := o.ready(); err != nil {
		return nil, GraphOwnerContext{}, err
	}
	current, err := o.identity(h.context)
	if err != nil {
		return nil, GraphOwnerContext{}, err
	}
	cut, err := o.replay()
	return cut, current, err
}
func (h graphReleaseHost) VerifyApproval(value ReleaseHostAdmission) (any, error) {
	cut, current, err := h.proofView()
	if err != nil {
		return nil, err
	}
	if _, err := h.owner.releaseAdmission(value, current, cut, nil); err != nil {
		return nil, err
	}
	if value.Compilation.Kind == "receipt_read" {
		return nil, nil
	}
	proof, err := h.owner.approval(h.context, current, value.Request.ApprovalRef(), value.RequestDigest, h.owner.clock())
	if proof == nil || err != nil {
		return nil, err
	}
	return *proof, nil
}
func graphReleaseProof(value any) (*GraphApproval, error) {
	if value == nil {
		return nil, nil
	}
	proof, ok := value.(GraphApproval)
	if !ok {
		return nil, managed.ErrDenied
	}
	return &proof, nil
}
func (h graphReleaseHost) Admit(value ReleaseHostAdmission, raw any) error {
	cut, current, err := h.proofView()
	if err != nil {
		return err
	}
	proof, err := graphReleaseProof(raw)
	if err != nil {
		return err
	}
	admission, err := h.owner.releaseAdmission(value, current, cut, proof)
	if err != nil {
		return err
	}
	_, err = h.owner.decide(h.context, admission, false)
	return err
}
func (h graphReleaseHost) Fence(value ReleaseHostAdmission, raw any, action func() error) error {
	return h.owner.operate(h.context, true, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
		proof, err := graphReleaseProof(raw)
		if err != nil {
			return err
		}
		if proof != nil {
			remaining, err := compareGraphUTC(proof.ExpiresAt, h.owner.clock())
			if err != nil || remaining <= 0 || proof.SubjectDigest != value.RequestDigest || proof.ActorID != current.Identity.ActorID ||
				proof.TenantID != current.Identity.TenantID || proof.AuthorityRevision != current.Identity.AuthorityRevision {
				return releaseError("approval_stale", "Release approval expired/changed at shared graph fence")
			}
		}
		admission, err := h.owner.releaseAdmission(value, current, cut, proof)
		if err != nil {
			return err
		}
		if _, err := h.owner.decide(h.context, admission, false); err != nil {
			return err
		}
		if err := h.owner.check(h.context, current, cut.Watermark()); err != nil {
			return err
		}
		if action == nil {
			return managed.ErrDenied
		}
		return action()
	})
}
func (o *FileJournalGraphOwner) DefinitionReads() managed.DefinitionReadPort {
	return managed.DefinitionReadFunc(func(ctx context.Context, request managed.DefinitionReadRequest, dispatch func(managed.DefinitionReadView) (managed.FrozenJSON, error)) (result managed.FrozenJSON, err error) {
		err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
			if dispatch == nil || (request.Source != managed.CatalogHead && request.Source != managed.CatalogRevision) ||
				(request.Source == managed.CatalogRevision) != (request.Revision != "") {
				return releaseError("schema_invalid", "Explicit definition source/revision required")
			}
			history, err := cut.History(request.GraphID)
			if err != nil {
				return err
			}
			head, snapshot := history.Head(), history.Head()
			if request.Source == managed.CatalogRevision {
				snapshot, err = history.At(request.Revision)
				if err != nil {
					return err
				}
			}
			admission := graphAdmission(current, request.GraphID, "graph.catalog", &head)
			admission.Impact = []string{}
			scope, err := o.decide(ctx, admission, true)
			if err != nil {
				return err
			}
			definition, currentDefinition := snapshot.Definition(), head.Definition()
			index, err := graph.BuildGraphIndex(&definition, o.validation.PublicationContracts)
			if err != nil {
				return err
			}
			currentIndex, err := graph.BuildGraphIndex(&currentDefinition, o.validation.PublicationContracts)
			if err != nil {
				return err
			}
			if err := o.check(ctx, current, cut.Watermark()); err != nil {
				return err
			}
			result, err = dispatch(managed.DefinitionReadView{Identity: current.Identity, StoreOrigin: o.storeID,
				Index: index, CurrentIndex: currentIndex, Revision: snapshot.Revision(), ScopeIDs: scope})
			if err != nil {
				return err
			}
			return o.check(ctx, current, cut.Watermark())
		})
		if err != nil {
			return managed.FrozenJSON{}, err
		}
		return result, nil
	})
}
func (o *FileJournalGraphOwner) CommittedReads() managed.CommittedBatchReadPort {
	return managed.CommittedBatchReadFunc(func(ctx context.Context, graphID string, dispatch func(managed.CommittedReadCut) (managed.FrozenJSON, error)) (result managed.FrozenJSON, err error) {
		err = o.operate(ctx, false, func(current GraphOwnerContext, cut *managed.GraphJournalProjection) error {
			if dispatch == nil {
				return managed.ErrDenied
			}
			history, err := cut.History(graphID)
			if err != nil {
				return err
			}
			head := history.Head()
			admission := graphAdmission(current, graphID, "graph.watch", &head)
			admission.Impact = []string{}
			scope, err := o.decide(ctx, admission, true)
			if err != nil {
				return err
			}
			if err := o.check(ctx, current, cut.Watermark()); err != nil {
				return err
			}
			records, accepted := history.Records(), cut.Records()
			var gate sync.Mutex
			open := true
			defer func() { gate.Lock(); open = false; gate.Unlock() }()
			window := func(nextCtx context.Context, after string, bounds managed.CommittedBatchBounds) (managed.CommittedBatchWindow, error) {
				gate.Lock()
				defer gate.Unlock()
				if !open {
					return managed.CommittedBatchWindow{}, managed.ErrDenied
				}
				if err := nextCtx.Err(); err != nil {
					return managed.CommittedBatchWindow{}, err
				}
				if err := o.check(ctx, current, cut.Watermark()); err != nil {
					return managed.CommittedBatchWindow{}, err
				}
				position, err := graph.ParseCounter(after)
				through, _ := graph.ParseCounter(cut.Watermark())
				retained, _ := graph.ParseCounter(records[0].JournalSeq())
				if err != nil || position.Cmp(retained) < 0 || position.Cmp(through) > 0 {
					return managed.CommittedBatchWindow{}, releaseError("history_unavailable", "Checkpoint outside exact retained history")
				}
				if bounds.MaxBatches <= 0 || bounds.MaxBytes <= 0 {
					return managed.CommittedBatchWindow{}, releaseError("schema_invalid", "Positive committed window bounds required")
				}
				var before managed.RevisionState
				batches := []managed.CommittedDefinitionBatch{}
				bytes := 0
				overflow := false
				for _, record := range records {
					seq, _ := graph.ParseCounter(record.JournalSeq())
					if seq.Cmp(position) <= 0 {
						before = record.State()
						continue
					}
					ordinal, err := strconv.Atoi(record.JournalSeq())
					if err != nil || ordinal < 1 || ordinal > len(accepted) {
						return managed.CommittedBatchWindow{}, releaseError("history_unavailable", "Exact committed record absent")
					}
					bytes += len(journalBytes(accepted[ordinal-1]))
					if len(batches) >= bounds.MaxBatches || bytes > bounds.MaxBytes {
						overflow = true
						break
					}
					batches = append(batches, managed.CommittedDefinitionBatch{Position: record.JournalSeq(), State: record.State()})
				}
				if overflow {
					batches = []managed.CommittedDefinitionBatch{}
				}
				return managed.CommittedBatchWindow{Before: before, Batches: batches, Through: cut.Watermark(), Overflow: overflow}, nil
			}
			result, err = dispatch(managed.CommittedReadCut{Identity: current.Identity, StoreOrigin: o.storeID, ScopeIDs: scope, Snapshot: head,
				Watermark: cut.Watermark(), RetainedAfter: records[0].JournalSeq(), ReadBatches: window})
			if err != nil {
				return err
			}
			return o.check(ctx, current, cut.Watermark())
		})
		if err != nil {
			return managed.FrozenJSON{}, err
		}
		return result, nil
	})
}

var _ ReleaseHistoryReader = graphReleaseHistory{}
var _ ReleaseHostPort = graphReleaseHost{}
