package server

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/managed"
)

func graphCatalogFirstName(t *testing.T, catalog *managed.ManagedCatalogService, ctx context.Context, request managed.CatalogRequest) string {
	t.Helper()
	start, err := catalog.Start(ctx, request, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := releaseText(journalMap(journalMap(start.JSON())["streams"])["nodes"])
	page, err := catalog.Continuation(ctx, token, request, managed.CatalogNodes)
	if err != nil {
		t.Fatal(err)
	}
	items := journalArray(journalMap(page.JSON())["items"])
	if len(items) != 1 {
		t.Fatal("Current partial owner scope did not project exactly one node", page.JSON())
	}
	data := journalMap(items[0])
	return releaseText(data["name"])
}
func (f *graphOwnerFixture) releaseRequest(revision, epoch, key string) JournalObject {
	f.t.Helper()
	state, err := f.owner.Read(f.context, "graph", revision)
	if err != nil {
		f.t.Fatal(err)
	}
	definition := state.Definition()
	plan, err := f.compile(&definition)
	if err != nil {
		f.t.Fatal(err)
	}
	digest, _ := state.DefinitionDigest()
	return JournalObject{"schema": "contexture.release-request/v1", "graph_id": "graph", "target_id": "target", "revision": revision, "definition_digest": digest,
		"manifest_digest": plan.Manifest().Digest(), "expected_desired_epoch": epoch, "idempotency_key": key, "policy": "exact",
		"reason": "actual graph-owner release", "approval_ref": "approved"}
}
func (f *graphOwnerFixture) releaseOwner() *FileJournalReleaseOwner {
	f.t.Helper()
	owner, err := NewFileJournalReleaseOwner(f.journal, FileJournalReleaseOptions{GraphID: "graph", TargetID: "target",
		Host: f.owner.ReleaseHost(f.context), History: f.owner.ReleaseHistoryReader(), Compilation: f})
	if err != nil {
		f.t.Fatal(err)
	}
	return owner
}
func (f *graphOwnerFixture) serving(owner *FileJournalReleaseOwner) *managed.LocalServingCoordinator {
	f.t.Helper()
	provider, err := owner.ServingProvider(f)
	if err != nil {
		f.t.Fatal(err)
	}
	coordinator, err := managed.NewLocalServingCoordinator(managed.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "native-instance",
		Authority: owner, Provider: provider, Observation: owner})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := coordinator.Shutdown(ctx); err != nil {
			f.t.Error(err)
		}
	})
	return coordinator
}
func TestGraphOwnerActualReleaseHistorySharedFenceCatalogAndWatchBridges(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	release := f.releaseOwner()
	if _, err := release.Request(f.releaseRequest("1", "0", "release")); err != nil {
		t.Fatal(err)
	}
	signer, err := managed.NewCursorSigner([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := managed.NewManagedCatalogService(managed.CatalogOptions{PageStoreOptions: managed.PageStoreOptions{Signer: signer}, Definition: f.owner.DefinitionReads()})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Shutdown()
	watch, err := managed.NewManagedWatchService(managed.WatchOptions{Signer: signer, Reader: f.owner.CommittedReads()})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Shutdown()
	start, err := watch.Start(f.context, managed.WatchRequest{GraphID: "graph"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldCursor := releaseText(journalMap(start.JSON())["cursor"])
	raw := f.transaction("1", "rename-batch", "changed")
	raw["commands"] = []any{JournalObject{"op": "rename_node", "node_id": "root", "new_name": "Next"},
		JournalObject{"op": "update_node", "node_id": "hidden", "set_fields": JournalObject{"description": "CHANGED HIDDEN"}, "unset_fields": []any{}}}
	preview, err := f.owner.Preview(f.context, raw, graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err != nil {
		t.Fatal(err)
	}
	captured, err := release.Capture()
	if err != nil || captured.Snapshot.Revision() != "1" {
		t.Fatal("Release bridge substituted graph head for desired historical revision", captured, err)
	}
	coordinator := f.serving(release)
	if result := coordinator.Prepare(f.context); result.Status != "local_observed" || result.Error != nil {
		t.Fatal("Actual graph/release/compile/provider ports did not adopt", result)
	}
	if name := graphCatalogFirstName(t, catalog, f.context, managed.CatalogRequest{GraphID: "graph", Source: managed.CatalogHead}); name != "Next" {
		t.Fatal(name)
	}
	if name := graphCatalogFirstName(t, catalog, f.context, managed.CatalogRequest{GraphID: "graph", Source: managed.CatalogRevision, Revision: "1"}); name != "Root" {
		t.Fatal("Historical catalog fell back to current head", name)
	}
	polled, err := watch.Poll(f.context, oldCursor, managed.WatchRequest{GraphID: "graph"})
	if err != nil {
		t.Fatal(err)
	}
	batches := journalArray(journalMap(polled.JSON())["batches"])
	if len(batches) != 1 {
		t.Fatal("One accepted graph record did not produce one complete watch batch", polled.JSON())
	}
	payload := journalMap(journalMap(batches[0])["payload"])
	nodes := journalArray(payload["nodes"])
	if len(nodes) != 1 || journalMap(nodes[0])["name"] != "Next" {
		t.Fatal("Watch mixed generation/revision or exposed hidden scope", payload)
	}
	serialized := string(journalBytes(polled.JSON()))
	for _, private := range []string{"HIDDEN", "SECRET", "PRIVATE PROGRAM", "journal_seq", "definition_digest", "manifest_digest"} {
		if strings.Contains(serialized, private) {
			t.Fatal("Committed native bridge leaked", private)
		}
	}
	checkpoint, err := f.journal.Checkpoint()
	if err != nil || len(journalArray(checkpoint["graphs"])) != 1 || len(journalArray(checkpoint["desired_releases"])) != 1 ||
		len(journalArray(checkpoint["prepare_attempt_history"])) != 1 || len(journalArray(checkpoint["observations_history"])) != 1 {
		t.Fatal("Complete actual eight-section checkpoint was not composed", checkpoint, err)
	}
	records, _ := f.journal.Records("0")
	if len(journalArray(records[2]["events"])) != 1 {
		t.Fatal("One native transaction invented multiple same-graph record events")
	}
	if f.handlers.Load() != 0 {
		t.Fatal("Graph/release/catalog/watch replay executed business callbacks")
	}
}
func TestGraphOwnerSharedReleaseUnknownRecoveryKeepsActualServingAndDeduplicates(t *testing.T) {
	var armed atomic.Bool
	f := newGraphOwnerFixture(t, func(stage string) error {
		if stage == "record.renamed" && armed.Swap(false) {
			return errors.New("shared observation publication unknown")
		}
		return nil
	})
	f.boot()
	release := f.releaseOwner()
	if _, err := release.Request(f.releaseRequest("1", "0", "release")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.beforeDecision = func(admission GraphAdmission) {
		if admission.Operation == "release.observe" {
			armed.Store(true)
		}
	}
	f.mu.Unlock()
	coordinator := f.serving(release)
	result := coordinator.Prepare(f.context)
	if result.Status != "serving_unobserved" || result.Error == nil || !f.owner.WritesStopped() || !release.SwapsFrozen() {
		t.Fatal("Shared unknown did not stop graph/release writes without pointer rollback", result, f.owner.WritesStopped(), release.SwapsFrozen())
	}
	serving, ok := coordinator.Current()
	if !ok || serving.Generation.ResourceState() != "prepared" {
		t.Fatal("Actual serving generation was rolled back")
	}
	f.mu.Lock()
	f.beforeDecision = nil
	f.mu.Unlock()
	f.reopen()
	if err := release.Recover(f.journal); err != nil {
		t.Fatal(err)
	}
	before, _ := f.journal.Watermark()
	retry := coordinator.RetryObservation(f.context)
	after, _ := f.journal.Watermark()
	if retry.Status != "local_observed" || retry.Error != nil || before != after {
		t.Fatal("Shared recovery fabricated duplicate observation", retry, before, after)
	}
}
func TestGraphOwnerReadBridgesRejectMissingHistoryEscapedCutsAndCurrentScope(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	f.edit("1", "edit", "changed")
	records, _ := f.journal.Records("0")
	reader := f.owner.ReleaseHistoryReader()
	view := JournalHistoryView{StoreID: f.owner.StoreID(), Watermark: "1", Records: records[:1]}
	exact, err := reader.Read("graph", "1", view)
	if err != nil || exact.JournalSeq != "1" {
		t.Fatal("Actual same-store historical prefix was not verified", exact, err)
	}
	transplanted := journalRecordsCopy(records[:1])
	transplanted[0]["store_id"] = "another-store"
	if _, err := reader.Read("graph", "1", JournalHistoryView{StoreID: f.owner.StoreID(), Watermark: "1", Records: transplanted}); err == nil {
		t.Fatal("Caller-transplanted Journal records became history authority")
	}
	if _, err := f.owner.DefinitionReads().Read(f.context, managed.DefinitionReadRequest{GraphID: "graph", Source: managed.CatalogRevision, Revision: "99"},
		func(managed.DefinitionReadView) (managed.FrozenJSON, error) { return managed.FreezeJSON(true) }); err == nil {
		t.Fatal("Unavailable history silently used head")
	}
	var escaped func(context.Context, string, managed.CommittedBatchBounds) (managed.CommittedBatchWindow, error)
	_, err = f.owner.CommittedReads().Read(f.context, "graph", func(cut managed.CommittedReadCut) (managed.FrozenJSON, error) {
		escaped = cut.ReadBatches
		window, err := cut.ReadBatches(f.context, "1", managed.CommittedBatchBounds{MaxBatches: 4, MaxBytes: 1 << 20})
		if err != nil || window.Before.Revision() != "1" || len(window.Batches) != 1 || window.Through != "2" {
			t.Fatal("Committed positions were not one exact accepted cut", window, err)
		}
		overflow, err := cut.ReadBatches(f.context, "1", managed.CommittedBatchBounds{MaxBatches: 4, MaxBytes: 1})
		if err != nil || !overflow.Overflow || len(overflow.Batches) != 0 {
			t.Fatal("Committed backlog ignored native bounds", overflow, err)
		}
		if _, err := cut.ReadBatches(f.context, "0", managed.CommittedBatchBounds{MaxBatches: 4, MaxBytes: 1000}); err == nil {
			t.Fatal("Unavailable before history was fabricated")
		}
		return managed.FreezeJSON(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped(f.context, "1", managed.CommittedBatchBounds{MaxBatches: 4, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("Committed callback escaped its actual authority cut")
	}
	f.mu.Lock()
	f.allowed = false
	f.mu.Unlock()
	if _, err := f.owner.DefinitionReads().Read(f.context, managed.DefinitionReadRequest{GraphID: "graph", Source: managed.CatalogHead},
		func(managed.DefinitionReadView) (managed.FrozenJSON, error) { return managed.FreezeJSON(true) }); err == nil {
		t.Fatal("Catalog bridge bypassed current authority revocation")
	}
}

type graphFenceOverride struct {
	GraphOwnerHost
	fence func(context.Context, GraphOwnerContext, func() error) error
}

func (h graphFenceOverride) Fence(ctx context.Context, current GraphOwnerContext, action func() error) error {
	return h.fence(ctx, current, action)
}
func TestGraphOwnerRetainedHostFenceCapabilityRefusesAfterReturn(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	if err := f.owner.Close(); err != nil {
		t.Fatal(err)
	}
	var saved func() error
	override := graphFenceOverride{GraphOwnerHost: f, fence: func(_ context.Context, _ GraphOwnerContext, action func() error) error { saved = action; return nil }}
	owner, err := NewFileJournalGraphOwner(f.journal, GraphOwnerOptions{Host: override, Serialization: f.serialization, Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	f.owner = owner
	if _, err := owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry); err == nil {
		t.Fatal("Fence did not synchronously dispatch")
	}
	if saved == nil || saved() == nil {
		t.Fatal("Retained Host fence capability remained valid after return")
	}
	if watermark, _ := f.journal.Watermark(); watermark != "0" {
		t.Fatal("Retained capability fabricated accepted state")
	}
}
