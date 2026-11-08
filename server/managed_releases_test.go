package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

type releaseFixture struct {
	t                                                *testing.T
	root                                             string
	journal                                          *FileJournal
	options                                          JournalOptions
	definition                                       *graph.GraphDefinition
	registry                                         *managed.RegistryGeneration
	registration                                     *managed.BindingRegistration
	mu, serial                                       sync.Mutex
	current                                          ReleaseHostContext
	permit, approval                                 bool
	beforeFence                                      func(ReleaseHostAdmission)
	activeOperation                                  atomic.Value
	compileFail                                      atomic.Bool
	compiles, decodes, validates, handlers, disposes atomic.Int32
	providerHook                                     func(context.Context, managed.ServingAttempt) error
}
type releaseProof struct {
	digest  string
	context ReleaseHostContext
}
type releaseHistoryFunc func(string, string, JournalHistoryView) (VerifiedReleaseHistory, error)

func (f releaseHistoryFunc) Read(id, revision string, view JournalHistoryView) (VerifiedReleaseHistory, error) {
	return f(id, revision, view)
}

func (f *releaseFixture) Current() (ReleaseHostContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current, nil
}
func (f *releaseFixture) VerifyApproval(admission ReleaseHostAdmission) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if admission.Compilation.Kind != "receipt_read" && (!f.approval || admission.Request.ApprovalRef() != "approved") {
		return nil, managed.ErrDenied
	}
	return releaseProof{admission.RequestDigest, f.current}, nil
}
func (f *releaseFixture) Admit(ReleaseHostAdmission, any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.permit {
		return managed.ErrDenied
	}
	return nil
}
func (f *releaseFixture) Fence(admission ReleaseHostAdmission, proof any, action func() error) error {
	f.serial.Lock()
	defer f.serial.Unlock()
	f.mu.Lock()
	before := f.beforeFence
	f.mu.Unlock()
	if before != nil {
		before(admission)
	}
	f.mu.Lock()
	valid := f.permit && (admission.Compilation.Kind == "receipt_read" || f.approval)
	expected := releaseProof{admission.RequestDigest, f.current}
	f.mu.Unlock()
	supplied, ok := proof.(releaseProof)
	if !valid || !ok || supplied != expected {
		return managed.ErrDenied
	}
	f.activeOperation.Store(admission.Operation)
	defer f.activeOperation.Store("")
	return action()
}
func (f *releaseFixture) histories(records []JournalObject) ([]VerifiedReleaseHistory, error) {
	unseeded, err := graph.NewUnseededState("graph")
	if err != nil {
		return nil, err
	}
	var current any = unseeded
	out := []VerifiedReleaseHistory{}
	for _, record := range records {
		for _, raw := range journalArray(record["events"]) {
			event := journalMap(raw)
			if event["type"] != "GraphChangeCommitted" {
				continue
			}
			next, err := graph.ApplyEvent(current, event, graph.ApplyEventOptions{Tombstones: []any{}})
			if err != nil {
				return nil, err
			}
			current = next
			out = append(out, VerifiedReleaseHistory{Snapshot: next, JournalSeq: releaseText(record["journal_seq"]), RecordHash: releaseText(record["record_hash"])})
		}
	}
	return out, nil
}
func (f *releaseFixture) Read(id, revision string, view JournalHistoryView) (VerifiedReleaseHistory, error) {
	items, err := f.histories(view.Records)
	if err != nil {
		return VerifiedReleaseHistory{}, err
	}
	for _, item := range items {
		if id == "graph" && item.Snapshot.Revision() == revision {
			item.StoreID = view.StoreID
			return item, nil
		}
	}
	return VerifiedReleaseHistory{}, releaseError("reference_not_found", "Historical revision absent")
}
func (f *releaseFixture) compile(definition *graph.GraphDefinition) (*managed.StructuralPlan, error) {
	return managed.Compile(definition, managed.CompilerOptions{Registry: f.registry, Locale: "en", CompilerContractVersion: "candidate-1",
		CompilerBuild: "go-build", DependencyContractID: "app", DependencyBuild: "app-build", EnvironmentCompatibility: []string{"go-1.25"}})
}
func (f *releaseFixture) Verify(_ managed.ReleaseRequest, history VerifiedReleaseHistory, _ ReleaseHostContext) (*managed.StructuralPlan, error) {
	f.compiles.Add(1)
	if f.compileFail.Load() {
		return nil, releaseError("contract_mismatch", "Current compiler unavailable")
	}
	definition := history.Snapshot.Definition()
	return f.compile(&definition)
}
func (f *releaseFixture) graphProjection(records []JournalObject) (ReleaseGraphProjection, error) {
	items, err := f.histories(records)
	if err != nil {
		return ReleaseGraphProjection{}, err
	}
	graphs := []any{}
	if len(items) != 0 {
		head := items[len(items)-1].Snapshot
		definition := head.Definition()
		digest, _ := head.DefinitionDigest()
		graphs = append(graphs, JournalObject{"definition": definition.AsJSON(), "head": head.Revision(), "definition_digest": digest, "tombstones": head.Tombstones()})
	}
	g, _ := managed.FreezeJSON(graphs)
	versions, _ := managed.FreezeJSON(JournalObject{"graphs": "1", "releases": "1"})
	refs, _ := managed.FreezeJSON([]any{})
	return ReleaseGraphProjection{g, versions, refs}, nil
}
func (f *releaseFixture) Prepare(ctx context.Context, attempt managed.ServingAttempt) (*managed.ReadyGeneration, error) {
	if f.providerHook != nil {
		if err := f.providerHook(ctx, attempt); err != nil {
			return nil, err
		}
	}
	definition := attempt.Release.Snapshot.Definition()
	plan, err := f.compile(&definition)
	if err != nil {
		return nil, err
	}
	return managed.PrepareGeneration(ctx, plan, managed.PreparationContext{GraphID: "graph", Revision: attempt.Release.Snapshot.Revision(), GenerationUUID: attempt.GenerationUUID},
		map[string]managed.PreparationAdapterRegistration{"tool": {BindingID: "reader", Version: "1", ABI: managed.NativeABI, ContractDigest: f.registration.ContractDigest(),
			Acquire: func(context.Context, managed.ResolvedBinding, string, managed.PreparationContext) (managed.PreparedResource, error) {
				return managed.PreparedResource{Handle: attempt.GenerationUUID, Dispose: func(context.Context) error { f.disposes.Add(1); return nil }}, nil
			}}})
}
func newReleaseFixture(t *testing.T, fault func(string) error) *releaseFixture {
	t.Helper()
	f := &releaseFixture{t: t, root: journalTestRoot(t), permit: true, approval: true,
		current: ReleaseHostContext{ActorRef: "actor", Identity: managed.Identity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "policy-1"}}}
	var err error
	f.registration, err = managed.NewBindingRegistration(managed.BindingRegistrationOptions[struct{}]{BindingID: "reader", Version: "1", Kind: "registered", Effect: "read_only",
		ABI: managed.NativeABI, ArtifactDigest: "sha256:" + strings.Repeat("a", 64), Portability: managed.Portability{Kind: "portable", ContractID: "reader-contract"},
		InputSchema: JournalObject{"type": "object", "additionalProperties": false}, ValidatorContractID: "validator", DecoderContractID: "decoder",
		DefaultContractID: "defaults.none", CoercionContractID: "coercion.none",
		Decoder:   func(managed.FrozenJSON) (struct{}, error) { f.decodes.Add(1); return struct{}{}, nil },
		Validator: func(struct{}) error { f.validates.Add(1); return nil },
		Handler: func(_ context.Context, _ struct{}, call managed.CallContext) (any, error) {
			f.handlers.Add(1)
			return JournalObject{"resource": call.Resource}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	f.registry, err = managed.NewRegistryGeneration("registry", f.registration)
	if err != nil {
		t.Fatal(err)
	}
	f.definition, err = managed.NewGraphDefinition(JournalObject{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "graph", "application_name": "native-release",
		"mode": "runtime", "nodes": []any{
			JournalObject{"node_id": "root", "kind": "role", "name": "Root", "description": "root", "instructions": "", "mutable": true, "members": JournalObject{"tools": []any{"tool"}}},
			JournalObject{"node_id": "tool", "kind": "tool", "name": "Tool", "description": "tool", "binding": f.registration.Descriptor().AsJSON()}},
		"roots": []any{JournalObject{"node_id": "root", "plane": "model"}}, "presentations": JournalObject{"default_locale": "en", "catalogs": []any{}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := ReleaseJournalProjector(f.graphProjection)
	if err != nil {
		t.Fatal(err)
	}
	f.options = JournalOptions{Schema: journalTestSchema(t), Projector: projector, Fault: fault}
	f.journal, err = OpenFileJournal(f.root, f.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.journal.Close() })
	unseeded, _ := graph.NewUnseededState("graph")
	digest, _ := graph.CanonicalDigest(JournalObject{"seed": true}, "contexture.command/v1")
	seed, err := graph.BootstrapEvent(unseeded, f.definition, graph.EventOptions{CommandDigest: digest, Reason: "native seed"}, "approved")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.Append([]any{seed.JSON()}, JournalAppendOptions{ActorRef: "actor"}); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *releaseFixture) owner() *FileJournalReleaseOwner {
	f.t.Helper()
	owner, err := NewFileJournalReleaseOwner(f.journal, FileJournalReleaseOptions{GraphID: "graph", TargetID: "target", Host: f, History: f, Compilation: f})
	if err != nil {
		f.t.Fatal(err)
	}
	return owner
}
func (f *releaseFixture) request(fields JournalObject) JournalObject {
	f.t.Helper()
	plan, err := f.compile(f.definition)
	if err != nil {
		f.t.Fatal(err)
	}
	digest, _ := f.definition.Digest()
	raw := JournalObject{"schema": "contexture.release-request/v1", "graph_id": "graph", "target_id": "target", "revision": "1",
		"definition_digest": digest, "manifest_digest": plan.Manifest().Digest(), "expected_desired_epoch": "0", "idempotency_key": "release-key",
		"policy": "exact", "reason": "native release", "approval_ref": "approved"}
	for key, value := range fields {
		raw[key] = value
	}
	return raw
}
func (f *releaseFixture) serving(owner *FileJournalReleaseOwner) *managed.LocalServingCoordinator {
	f.t.Helper()
	provider, err := owner.ServingProvider(f)
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := managed.NewLocalServingCoordinator(managed.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "instance", Authority: owner, Provider: provider, Observation: owner})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Shutdown(ctx); err != nil {
			f.t.Error(err)
		}
	})
	return c
}
func (f *releaseFixture) reopen(owner *FileJournalReleaseOwner) {
	f.t.Helper()
	if err := f.journal.Close(); err != nil {
		f.t.Fatal(err)
	}
	journal, err := OpenFileJournal(f.root, f.options)
	if err != nil {
		f.t.Fatal(err)
	}
	f.journal = journal
	if err := owner.Recover(journal); err != nil {
		f.t.Fatal(err)
	}
}

func TestDurableReleaseActorReceiptsCASAndRealReopen(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	first, err := owner.Request(f.request(nil))
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := f.journal.Watermark()
	f.mu.Lock()
	f.approval = false
	f.mu.Unlock()
	f.compileFail.Store(true)
	compiles := f.compiles.Load()
	retry, err := owner.Request(f.request(nil))
	if err != nil || !journalEqual(first.JSON(), retry.JSON()) || f.compiles.Load() != compiles {
		t.Fatal("receipt retry repeated approval/compile/CAS", err)
	}
	if watermark, _ := f.journal.Watermark(); watermark != seq {
		t.Fatal("receipt retry appended")
	}
	if _, err := owner.Request(f.request(JournalObject{"reason": "other"})); err == nil {
		t.Fatal("actor/key subject conflict accepted")
	}
	f.mu.Lock()
	f.approval = true
	f.current.ActorRef, f.current.Identity.ActorID = "other-actor", "other-actor"
	f.mu.Unlock()
	f.compileFail.Store(false)
	if _, err := owner.Request(f.request(JournalObject{"expected_desired_epoch": "1", "policy": "latest"})); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.current.ActorRef, f.current.Identity.ActorID = "actor", "actor"
	f.mu.Unlock()
	if _, err := owner.Request(f.request(JournalObject{"idempotency_key": "conflict"})); err == nil {
		t.Fatal("stale desired CAS accepted")
	}
	checkpoint, err := f.journal.Checkpoint()
	if err != nil || len(journalArray(checkpoint["release_operations"])) != 3 || len(journalArray(checkpoint["graphs"])) != 1 {
		t.Fatal(checkpoint, err)
	}
	f.reopen(owner)
	newOwner := f.owner()
	retry, err = newOwner.Request(f.request(nil))
	if err != nil || !journalEqual(first.JSON(), retry.JSON()) {
		t.Fatal("original actor receipt changed after supersession/reopen", err)
	}
	captured, err := newOwner.Capture()
	if err != nil || captured.ReleaseEpoch != "2" || f.handlers.Load()+f.decodes.Load()+f.validates.Load() != 0 {
		t.Fatal(captured, err)
	}
}
func TestDurableReleaseCurrentAdmissionReentryAndProofMovement(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	seq, _ := f.journal.Watermark()
	f.mu.Lock()
	f.permit = false
	f.mu.Unlock()
	if _, err := owner.Request(f.request(nil)); !errors.Is(err, managed.ErrDenied) {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.permit = true
	f.beforeFence = func(ReleaseHostAdmission) {
		if _, err := owner.Capture(); err == nil {
			t.Error("release owner callback reentry accepted")
		}
		f.mu.Lock()
		f.current.Identity.AuthorityRevision = "policy-2"
		f.mu.Unlock()
	}
	f.mu.Unlock()
	if _, err := owner.Request(f.request(nil)); err == nil {
		t.Fatal("authority revision movement accepted")
	}
	if watermark, _ := f.journal.Watermark(); watermark != seq {
		t.Fatal("failed proof appended release")
	}
	f.mu.Lock()
	f.beforeFence = nil
	f.mu.Unlock()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.permit = false
	f.mu.Unlock()
	if _, err := owner.Request(f.request(nil)); !errors.Is(err, managed.ErrDenied) {
		t.Fatal("receipt retry bypassed current read revocation", err)
	}
}
func TestDurableReleaseHistoricalRevisionIsNotCurrentHead(t *testing.T) {
	f := newReleaseFixture(t, nil)
	records, _ := f.journal.Records("0")
	base, err := f.Read("graph", "1", JournalHistoryView{StoreID: releaseText(f.journal.Meta()["store_id"]), Records: records})
	if err != nil {
		t.Fatal(err)
	}
	raw := journalMap(f.definition.AsJSON())
	for _, value := range journalArray(raw["nodes"]) {
		if journalMap(value)["node_id"] == "root" {
			journalMap(value)["description"] = "new head"
		}
	}
	changed, err := graph.NewGraphDefinition(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	change, err := graph.ResolveFacts(base.Snapshot, changed)
	if err != nil {
		t.Fatal(err)
	}
	event, err := graph.ChangeEvent(base.Snapshot, change, graph.EventOptions{CommandDigest: "sha256:" + strings.Repeat("1", 64), Reason: "head advances"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.Append([]any{event.JSON()}, JournalAppendOptions{ActorRef: "actor"}); err != nil {
		t.Fatal(err)
	}
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	captured, err := owner.Capture()
	want, _ := f.definition.Digest()
	got, _ := captured.Snapshot.DefinitionDigest()
	if err != nil || captured.Snapshot.Revision() != "1" || got != want {
		t.Fatal("historical request substituted head", captured, err)
	}
}
func TestDurableReleaseActualReadyAndObservationNotReplayAuthority(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	c := f.serving(owner)
	result := c.Prepare(context.Background())
	if result.Status != "local_observed" || result.Error != nil {
		t.Fatal(result.Status, result.Error)
	}
	records, _ := f.journal.Records("0")
	state, err := managed.ReplayReleases(records)
	if err != nil || len(state.Items("prepare_attempt_history")) != 1 || len(state.Items("observations_history")) != 1 {
		t.Fatal(state.AsJSON(), err)
	}
	if _, err := f.journal.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	fresh := f.owner()
	if err := fresh.Observe(context.Background(), result.Attempt); err == nil {
		t.Fatal("replayed durable observation reconstructed local adoption")
	}
	forged := result.Attempt
	forged.GenerationUUID = "00000000-0000-0000-0000-000000000000"
	if err := owner.Observe(context.Background(), forged); err == nil {
		t.Fatal("forged observation accepted")
	}
	if f.handlers.Load()+f.decodes.Load()+f.validates.Load() != 0 {
		t.Fatal("compiler/replay invoked business code")
	}
}
func TestDurableReleaseObservationUnknownKeepsPointerAndDeduplicatesAfterRecovery(t *testing.T) {
	var armed atomic.Bool
	f := newReleaseFixture(t, func(stage string) error {
		if stage == "record.renamed" && armed.Swap(false) {
			return errors.New("unknown observed publication")
		}
		return nil
	})
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.beforeFence = func(admission ReleaseHostAdmission) {
		if admission.Operation == "release.observe" {
			armed.Store(true)
		}
	}
	f.mu.Unlock()
	c := f.serving(owner)
	result := c.Prepare(context.Background())
	if result.Status != "serving_unobserved" || result.Error == nil || !owner.SwapsFrozen() {
		t.Fatal(result.Status, result.Error)
	}
	active, ok := c.Current()
	if !ok || active.Generation.ResourceState() != "prepared" || f.disposes.Load() != 0 {
		t.Fatal("uncertain observation rolled back real pointer")
	}
	if next := c.Prepare(context.Background()); !errors.Is(next.Error, managed.ErrCapacity) {
		t.Fatal("pending observation did not freeze replacement swaps", next)
	}
	f.mu.Lock()
	f.beforeFence = nil
	f.mu.Unlock()
	f.reopen(owner)
	before, _ := f.journal.Watermark()
	retry := c.RetryObservation(context.Background())
	after, _ := f.journal.Watermark()
	if retry.Status != "local_observed" || retry.Error != nil || before != after {
		t.Fatal("published uncertain observation was not deduplicated", retry, before, after)
	}
	records, _ := f.journal.Records("0")
	state, _ := managed.ReplayReleases(records)
	if len(state.Items("observations_history")) != 1 {
		t.Fatal(state.AsJSON())
	}
}
func TestDurableReleaseGlobalJournalUnknownFreezesOtherOwner(t *testing.T) {
	var armed atomic.Bool
	f := newReleaseFixture(t, func(stage string) error {
		if stage == "record.renamed" && armed.Swap(false) {
			return errors.New("external owner unknown")
		}
		return nil
	})
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	if _, err := f.journal.Append(journalTestEvent("external"), JournalAppendOptions{ActorRef: "actor"}); err == nil {
		t.Fatal("fault did not produce commit_unknown")
	}
	if _, err := owner.Capture(); err == nil || !owner.SwapsFrozen() {
		t.Fatal("other authoring owner's unknown allowed swaps", err)
	}
	f.reopen(owner)
	if captured, err := owner.Capture(); err != nil || captured == nil {
		t.Fatal(captured, err)
	}
}
func TestDurableReleasePreparationFailureRecordsOnlySupportedTerminal(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	f.providerHook = func(context.Context, managed.ServingAttempt) error {
		f.compileFail.Store(true)
		return errors.New("native acquisition failed")
	}
	c := f.serving(owner)
	result := c.Prepare(context.Background())
	if result.Status != "failed" || result.Error == nil {
		t.Fatal(result)
	}
	records, _ := f.journal.Records("0")
	state, err := managed.ReplayReleases(records)
	if err != nil || len(state.Items("prepare_attempt_history")) != 1 || journalMap(state.Items("prepare_attempt_history")[0])["status"] != "failed" {
		t.Fatal(state.AsJSON(), err)
	}
	for _, record := range records {
		for _, raw := range journalArray(record["events"]) {
			if tag := journalMap(raw)["type"]; tag == "ReleasePreparationStarted" || tag == "ReleasePending" {
				t.Fatal("absent-schema durable start/pending fact invented")
			}
		}
	}
}
func TestDurableReleaseRequiresIndependentProjectionAndAllProofPorts(t *testing.T) {
	if _, err := ReleaseJournalProjector(nil); err == nil {
		t.Fatal("empty graph projection default accepted")
	}
	f := newReleaseFixture(t, nil)
	if _, err := NewFileJournalReleaseOwner(f.journal, FileJournalReleaseOptions{GraphID: "graph", TargetID: "target", Host: f, History: f}); err == nil {
		t.Fatal("missing independent compile authority accepted")
	}
	owner := f.owner()
	other := newReleaseFixture(t, nil)
	if err := owner.Recover(other.journal); err == nil {
		t.Fatal("different physical store accepted as recovery")
	}
}
func TestDurableReleaseRequestUnknownRecoversActualOriginalReceipt(t *testing.T) {
	var armed atomic.Bool
	f := newReleaseFixture(t, func(stage string) error {
		if stage == "record.renamed" && armed.Swap(false) {
			return errors.New("uncertain release request")
		}
		return nil
	})
	owner := f.owner()
	armed.Store(true)
	if result, err := owner.Request(f.request(nil)); err == nil || result.JSON() != nil || !owner.SwapsFrozen() {
		t.Fatal("uncertain request fabricated receipt/authority", result.JSON(), err)
	}
	f.reopen(owner)
	f.mu.Lock()
	f.approval = false
	f.mu.Unlock()
	f.compileFail.Store(true)
	before, _ := f.journal.Watermark()
	result, err := owner.Request(f.request(nil))
	after, _ := f.journal.Watermark()
	if err != nil || before != after || journalMap(result.JSON())["journal_seq"] != "2" {
		t.Fatal("unknown retry did not use actual recovered receipt", result.JSON(), err)
	}
}
func TestDurableReleaseCurrentCompileAndAcceptedWatermarkFences(t *testing.T) {
	for _, mode := range []string{"compile", "watermark", "approval"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t, nil)
			owner := f.owner()
			f.mu.Lock()
			f.beforeFence = func(ReleaseHostAdmission) {
				switch mode {
				case "compile":
					f.compileFail.Store(true)
				case "approval":
					f.mu.Lock()
					f.approval = false
					f.mu.Unlock()
				case "watermark":
					if _, err := f.journal.Append(journalTestEvent("proof-change"), JournalAppendOptions{ActorRef: "actor"}); err != nil {
						t.Error(err)
					}
				}
			}
			f.mu.Unlock()
			if _, err := owner.Request(f.request(nil)); err == nil {
				t.Fatal("changed current proof accepted", mode)
			}
			records, _ := f.journal.Records("0")
			state, err := managed.ReplayReleases(records)
			if err != nil || len(state.Items("desired_releases")) != 0 {
				t.Fatal("failed proof appended release", state.AsJSON(), err)
			}
		})
	}
}
func TestDurableReleaseHostCannotMutateOwnedHistoricalAdmission(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	f.mu.Lock()
	f.beforeFence = func(admission ReleaseHostAdmission) {
		if admission.History != nil {
			fake, err := graph.NewRevisionState(f.definition, "99", nil)
			if err != nil {
				t.Error(err)
			}
			admission.History.Snapshot = fake
			admission.History.StoreID = "forged"
		}
	}
	f.mu.Unlock()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	captured, err := owner.Capture()
	if err != nil || captured.Snapshot.Revision() != "1" {
		t.Fatal("Host mutated owned immutable proof", captured, err)
	}
}
func TestDurableReleaseLateReadyHistoryCannotAdoptNewerDesired(t *testing.T) {
	f := newReleaseFixture(t, nil)
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.providerHook = func(_ context.Context, attempt managed.ServingAttempt) error {
		if attempt.Release.ReleaseEpoch == "1" {
			close(entered)
			<-release
		}
		return nil
	}
	c := f.serving(owner)
	done := make(chan managed.LocalServingResult, 1)
	go func() { done <- c.Prepare(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation barrier timed out")
	}
	if _, err := owner.Request(f.request(JournalObject{"idempotency_key": "new", "expected_desired_epoch": "1", "policy": "latest"})); err != nil {
		t.Fatal(err)
	}
	newer := c.Prepare(context.Background())
	if newer.Status != "local_observed" || newer.Error != nil {
		t.Fatal(newer)
	}
	close(release)
	select {
	case late := <-done:
		if late.Status != "superseded" || late.Error != nil {
			t.Fatal(late)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late preparation did not settle")
	}
	active, ok := c.Current()
	if !ok || active.Attempt.Release.ReleaseEpoch != "2" {
		t.Fatal("late prepared value replaced desired")
	}
	records, _ := f.journal.Records("0")
	state, err := managed.ReplayReleases(records)
	if err != nil || len(state.Items("prepare_attempt_history")) != 2 || len(state.Items("observations_history")) != 1 {
		t.Fatal(state.AsJSON(), err)
	}
}
func TestDurableReleasePointerFenceDoesNotAppendAndJournalReadinessRefusesReentry(t *testing.T) {
	var fixture *releaseFixture
	var checked atomic.Int32
	fixture = newReleaseFixture(t, func(stage string) error {
		if fixture != nil && strings.HasPrefix(stage, "record.") {
			if operation, _ := fixture.activeOperation.Load().(string); operation == "release.swap" {
				t.Error("physical Journal I/O occurred in pointer fence")
			}
			if err := fixture.journal.AssertWritable(); err == nil {
				t.Error("Journal callback reentry readiness allowed")
			}
			checked.Add(1)
		}
		return nil
	})
	owner := fixture.owner()
	if _, err := owner.Request(fixture.request(nil)); err != nil {
		t.Fatal(err)
	}
	c := fixture.serving(owner)
	if result := c.Prepare(context.Background()); result.Status != "local_observed" || result.Error != nil {
		t.Fatal(result)
	}
	if checked.Load() == 0 {
		t.Fatal("native physical publication hooks were not exercised")
	}
}

func TestDurableReleaseRejectsWrongStoreAnchorHashAndSnapshot(t *testing.T) {
	for _, mode := range []string{"store", "anchor", "hash", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t, nil)
			reader := releaseHistoryFunc(func(id, revision string, view JournalHistoryView) (VerifiedReleaseHistory, error) {
				history, err := f.Read(id, revision, view)
				if err != nil {
					return history, err
				}
				switch mode {
				case "store":
					history.StoreID = "another-store"
				case "anchor":
					history.JournalSeq = "2"
				case "hash":
					history.RecordHash = "sha256:" + strings.Repeat("0", 64)
				case "snapshot":
					history.Snapshot, err = graph.NewRevisionState(f.definition, "2", nil)
				}
				return history, err
			})
			owner, err := NewFileJournalReleaseOwner(f.journal, FileJournalReleaseOptions{GraphID: "graph", TargetID: "target", Host: f, History: reader, Compilation: f})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Request(f.request(nil)); err == nil {
				t.Fatal("invalid historical provenance accepted", mode)
			}
			if watermark, _ := f.journal.Watermark(); watermark != "1" {
				t.Fatal("failed historical proof appended")
			}
		})
	}
}
