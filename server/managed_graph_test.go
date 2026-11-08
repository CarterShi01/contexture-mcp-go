package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/CarterShi01/contexture-mcp-go/managed"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const graphPreviewExpiry = "2026-11-07T04:30:00Z"

type graphOwnerFixture struct {
	t                *testing.T
	root             string
	journal          *FileJournal
	owner            *FileJournalGraphOwner
	journalOptions   JournalOptions
	serialization    *SynchronousJournalSerialization
	context          context.Context
	definition       *managed.GraphDefinition
	registry         *managed.RegistryGeneration
	registration     *managed.BindingRegistration
	mu, authority    sync.Mutex
	version, now     string
	approvalExpiry   string
	allowed, approve bool
	payloadAllowed   bool
	permissions      []string
	scope, visible   []string
	badProof         string
	beforeDecision   func(GraphAdmission)
	calls, handlers  atomic.Int32
}

func graphTestContext(t *testing.T, actor string) context.Context {
	t.Helper()
	ctx, err := managed.WithIdentity(context.Background(), managed.Identity{ActorID: actor, TenantID: "tenant", AuthorityRevision: "1"})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
func (f *graphOwnerFixture) Current(ctx context.Context) (GraphOwnerContext, error) {
	f.calls.Add(1)
	identity, err := managed.IdentityFromContext(ctx)
	if err != nil {
		return GraphOwnerContext{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	identity.AuthorityRevision = f.version
	return GraphOwnerContext{ActorRef: identity.TenantID + "-" + identity.ActorID, Identity: identity, HostPermissions: slices.Clone(f.permissions)}, nil
}
func (f *graphOwnerFixture) VerifyApproval(_ context.Context, current GraphOwnerContext, reference, subject, _ string) (GraphApproval, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.approve || reference != "approved" {
		return GraphApproval{}, managed.ErrDenied
	}
	proof := GraphApproval{ActorID: current.Identity.ActorID, TenantID: current.Identity.TenantID, AuthorityRevision: current.Identity.AuthorityRevision,
		SubjectDigest: subject, ExpiresAt: f.approvalExpiry}
	switch f.badProof {
	case "actor":
		proof.ActorID = "other"
	case "subject":
		proof.SubjectDigest = "sha256:" + strings.Repeat("0", 64)
	case "expiry":
		proof.ExpiresAt = f.now
	}
	return proof, nil
}
func (f *graphOwnerFixture) Decide(_ context.Context, admission GraphAdmission) (GraphAdmissionDecision, error) {
	f.calls.Add(1)
	f.mu.Lock()
	hook := f.beforeDecision
	f.mu.Unlock()
	if hook != nil {
		hook(admission)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	scope := slices.Clone(f.scope)
	if admission.Operation == "graph.catalog" || admission.Operation == "graph.watch" {
		scope = slices.Clone(f.visible)
	}
	allowed := f.allowed && (admission.Operation != "graph.payload" || f.payloadAllowed)
	approval := admission.Operation == "graph.bootstrap" || admission.Operation == "graph.bootstrap.preview" ||
		admission.Operation == "graph.preview" || admission.Operation == "graph.transition" ||
		admission.Release != nil && admission.Release.Compilation.Kind != "receipt_read"
	return GraphAdmissionDecision{Allowed: allowed, AuthorityRevision: f.version, ScopeIDs: scope, ApprovalRequired: approval}, nil
}
func (f *graphOwnerFixture) Fence(ctx context.Context, current GraphOwnerContext, action func() error) error {
	f.authority.Lock()
	defer f.authority.Unlock()
	latest, err := f.Current(ctx)
	if err != nil || !sameGraphContext(current, latest) {
		return managed.ErrDenied
	}
	return action()
}
func (f *graphOwnerFixture) clock() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}
func (f *graphOwnerFixture) compile(definition *managed.GraphDefinition) (*managed.StructuralPlan, error) {
	return managed.Compile(definition, managed.CompilerOptions{Registry: f.registry, Locale: "en", CompilerContractVersion: "candidate",
		CompilerBuild: "go-native", DependencyContractID: "app", DependencyBuild: "app-build"})
}
func (f *graphOwnerFixture) Verify(_ managed.ReleaseRequest, history VerifiedReleaseHistory, _ ReleaseHostContext) (*managed.StructuralPlan, error) {
	definition := history.Snapshot.Definition()
	return f.compile(&definition)
}
func (f *graphOwnerFixture) Prepare(ctx context.Context, attempt managed.ServingAttempt) (*managed.ReadyGeneration, error) {
	definition := attempt.Release.Snapshot.Definition()
	plan, err := f.compile(&definition)
	if err != nil {
		return nil, err
	}
	return managed.PrepareGeneration(ctx, plan, managed.PreparationContext{GraphID: "graph", Revision: attempt.Release.Snapshot.Revision(), GenerationUUID: attempt.GenerationUUID},
		map[string]managed.PreparationAdapterRegistration{"tool": {BindingID: "reader", Version: "1", ABI: managed.NativeABI, ContractDigest: f.registration.ContractDigest(),
			Acquire: func(context.Context, managed.ResolvedBinding, string, managed.PreparationContext) (managed.PreparedResource, error) {
				return managed.PreparedResource{Handle: attempt.GenerationUUID, Dispose: func(context.Context) error { return nil }}, nil
			}}})
}
func newGraphOwnerFixture(t *testing.T, fault func(string) error) *graphOwnerFixture {
	t.Helper()
	f := &graphOwnerFixture{t: t, root: journalTestRoot(t), context: graphTestContext(t, "actor"), serialization: &SynchronousJournalSerialization{},
		version: "1", now: "2026-11-07T04:00:00Z", approvalExpiry: "2027-01-01T00:00:00Z", allowed: true, approve: true, payloadAllowed: true,
		permissions: []string{"graph.bootstrap", "graph.roots"}, scope: []string{"root", "tool", "hidden", "added"}, visible: []string{"root"}}
	var err error
	f.registration, err = managed.NewBindingRegistration(managed.BindingRegistrationOptions[struct{}]{BindingID: "reader", Version: "1", Kind: "registered", Effect: "read_only",
		ABI: managed.NativeABI, ArtifactDigest: "sha256:" + strings.Repeat("a", 64), Portability: managed.Portability{Kind: "portable", ContractID: "read-contract"},
		InputSchema: JournalObject{"type": "object", "additionalProperties": false}, ValidatorContractID: "validate", DecoderContractID: "decode",
		DefaultContractID: "defaults.none", CoercionContractID: "coercion.none",
		Decoder:   func(managed.FrozenJSON) (struct{}, error) { f.handlers.Add(1); return struct{}{}, nil },
		Validator: func(struct{}) error { f.handlers.Add(1); return nil },
		Handler:   func(context.Context, struct{}, managed.CallContext) (any, error) { f.handlers.Add(1); return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.registry, err = managed.NewRegistryGeneration("registry", f.registration)
	if err != nil {
		t.Fatal(err)
	}
	f.definition, err = managed.NewGraphDefinition(JournalObject{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "graph", "application_name": "native-authoring",
		"mode": "runtime", "nodes": []any{
			JournalObject{"node_id": "root", "kind": "role", "name": "Root", "description": "original", "instructions": "PRIVATE PROGRAM", "mutable": true,
				"members": JournalObject{"tools": []any{"tool"}, "skills": []any{"hidden"}}},
			JournalObject{"node_id": "tool", "kind": "tool", "name": "Tool", "description": "tool", "binding": f.registration.Descriptor().AsJSON()},
			JournalObject{"node_id": "hidden", "kind": "skill", "name": "SECRET", "description": "HIDDEN", "instructions": "SECRET PROGRAM"}},
		"roots": []any{JournalObject{"node_id": "root", "plane": "model"}}, "presentations": JournalObject{"default_locale": "en", "catalogs": []any{}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.journalOptions = JournalOptions{Schema: journalTestSchema(t), Projector: GraphJournalProjector(managed.GraphJournalProjectionOptions{}), Fault: fault}
	f.journal, err = OpenFileJournal(f.root, f.journalOptions)
	if err != nil {
		t.Fatal(err)
	}
	f.owner, err = NewFileJournalGraphOwner(f.journal, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.owner.Close(); _ = f.journal.Close() })
	return f
}
func (f *graphOwnerFixture) bootstrap() JournalObject {
	return JournalObject{"schema": "contexture.bootstrap/v1", "graph_id": "graph", "expected_revision": "0", "seed": JournalObject{"inline": f.definition.AsJSON()},
		"approval_ref": "approved", "idempotency_key": "seed", "reason": "native approved seed", "correlation_id": "seed-correlation"}
}
func (f *graphOwnerFixture) boot() (*GraphPreview, managed.FrozenJSON) {
	f.t.Helper()
	preview, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry)
	if err != nil {
		f.t.Fatal(err)
	}
	receipt, err := f.owner.Commit(f.context, preview, GraphCommitOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	return preview, receipt
}
func (f *graphOwnerFixture) transaction(base, key, description string) JournalObject {
	return JournalObject{"schema": "contexture.transaction/v1", "graph_id": "graph", "base_revision": base, "idempotency_key": key, "reason": "native transition",
		"correlation_id": "transition-" + key, "approval_ref": "approved",
		"commands": []any{JournalObject{"op": "update_node", "node_id": "root", "set_fields": JournalObject{"description": description}, "unset_fields": []any{}}}}
}
func (f *graphOwnerFixture) edit(base, key, description string) managed.FrozenJSON {
	f.t.Helper()
	preview, err := f.owner.Preview(f.context, f.transaction(base, key, description), graphPreviewExpiry)
	if err != nil {
		f.t.Fatal(err)
	}
	receipt, err := f.owner.Commit(f.context, preview, GraphCommitOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	return receipt
}
func (f *graphOwnerFixture) reopen() {
	f.t.Helper()
	if err := f.journal.Close(); err != nil {
		f.t.Fatal(err)
	}
	journal, err := OpenFileJournal(f.root, f.journalOptions)
	if err != nil {
		f.t.Fatal(err)
	}
	f.journal = journal
	if err := f.owner.Recover(f.context, journal); err != nil {
		f.t.Fatal(err)
	}
}
func TestGraphOwnerUnchangedCutReusesProjectionWithoutCachingAuthority(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	cached := f.owner.confirmedProjection
	calls := f.calls.Load()
	for range 10 {
		if _, err := f.owner.Read(f.context, "graph", ""); err != nil {
			t.Fatal(err)
		}
		if f.owner.confirmedProjection != cached {
			t.Fatal("Unchanged cut repeated semantic replay")
		}
	}
	if f.calls.Load() <= calls {
		t.Fatal("Projection cache suppressed current admission")
	}
	f.mu.Lock()
	f.allowed = false
	f.mu.Unlock()
	if _, err := f.owner.Read(f.context, "graph", ""); err == nil {
		t.Fatal("Cached projection bypassed revocation")
	}
	f.mu.Lock()
	f.allowed = true
	f.mu.Unlock()
	preview, err := f.owner.Preview(f.context, f.transaction("1", "cache-change", "updated"), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{ApprovalRef: "approved"}); err != nil {
		t.Fatal(err)
	}
	if f.owner.confirmedProjection == cached {
		t.Fatal("New accepted suffix retained stale projection")
	}
	latest := f.owner.confirmedProjection
	if err := f.owner.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewFileJournalGraphOwner(f.journal, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if fresh.confirmedProjection == latest {
		t.Fatal("Fresh owner reused a prior owner's replay evidence")
	}
}

func TestGraphOwnerApprovedBootstrapOpaqueCapabilityAndRealReopen(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	if f.calls.Load() != 0 {
		t.Fatal("Constructor/replay called external Host")
	}
	preview, receipt := f.boot()
	if journalMap(receipt.JSON())["actor_ref"] != "tenant-actor" || journalMap(receipt.JSON())["status"] != "committed" {
		t.Fatal(receipt.JSON())
	}
	if _, err := f.owner.Commit(f.context, &GraphPreview{}, GraphCommitOptions{}); err == nil {
		t.Fatal("Preview receipt/empty capability was accepted")
	}
	if _, err := json.Marshal(preview); err == nil {
		t.Fatal("Opaque capability serialized")
	}
	if _, err := NewFileJournalGraphOwner(f.journal, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock}); err == nil {
		t.Fatal("Duplicate owner bound physical Journal")
	}
	exported, err := f.owner.Export(f.context, "graph", "1")
	if err != nil || !bytes.Equal(exported, journalBytes(f.definition.AsJSON())) {
		t.Fatal(err)
	}
	checkpoint, err := f.journal.Checkpoint()
	if err != nil || len(journalArray(checkpoint["graphs"])) != 1 {
		t.Fatal(checkpoint, err)
	}
	if err := f.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.journal.AssertWritable(); err != nil {
		t.Fatal("Logical close took physical custody", err)
	}
	if err := f.journal.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenFileJournal(f.root, f.journalOptions)
	if err != nil {
		t.Fatal(err)
	}
	f.journal = journal
	before := f.calls.Load()
	f.owner, err = NewFileJournalGraphOwner(journal, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock})
	if err != nil || f.calls.Load() != before {
		t.Fatal("Reopen/replay invoked external authority", err)
	}
	f.mu.Lock()
	f.now, f.approve, f.version = "2027-02-01T00:00:00Z", false, "2"
	f.mu.Unlock()
	retry, err := f.owner.Commit(f.context, preview, GraphCommitOptions{})
	if err != nil || !journalEqual(receipt.JSON(), retry.JSON()) {
		t.Fatal("Original receipt retry repeated expired preview/approval/CAS", retry.JSON(), err)
	}
	loaded, err := journal.ReadCheckpoint()
	if err != nil || !journalEqual(checkpoint, loaded) || f.handlers.Load() != 0 {
		t.Fatal("Reopened checkpoint differs or business code ran", err)
	}
}
func TestGraphOwnerPreviewCASConflictActorRetryNoopAndHistory(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	original, err := f.owner.Read(f.context, "graph", "1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.owner.Preview(f.context, f.transaction("1", "edit", "changed"), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := f.owner.Preview(f.context, f.transaction("1", "stale", "changed"), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.owner.Preview(f.context, f.transaction("1", "edit", "different"), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.owner.Commit(f.context, first, GraphCommitOptions{})
	if err != nil || journalMap(result.JSON())["revision"] != "2" {
		t.Fatal(result.JSON(), err)
	}
	if _, err := f.owner.Commit(f.context, stale, GraphCommitOptions{}); err == nil {
		t.Fatal("Stale CAS overwrote head")
	}
	if _, err := f.owner.Commit(f.context, conflict, GraphCommitOptions{}); err == nil {
		t.Fatal("Actor/key accepted conflicting subject")
	}
	if retry, err := f.owner.Commit(f.context, first, GraphCommitOptions{}); err != nil || !journalEqual(result.JSON(), retry.JSON()) {
		t.Fatal(err)
	}
	other := graphTestContext(t, "other")
	if receipt, err := f.owner.Receipt(other, "graph", "edit"); err != nil || receipt.JSON() != nil {
		t.Fatal("Actor receipt leaked", receipt.JSON(), err)
	}
	if _, err := f.owner.Commit(other, first, GraphCommitOptions{}); err == nil {
		t.Fatal("Another actor consumed preview")
	}
	noop := f.edit("2", "noop", "changed")
	if journalMap(noop.JSON())["status"] != "noop" || journalMap(noop.JSON())["revision"] != "2" {
		t.Fatal(noop.JSON())
	}
	history, err := f.owner.History(f.context, "graph")
	if err != nil || len(history.Records()) != 3 {
		t.Fatal(history, err)
	}
	definition := original.Definition()
	root, _ := definition.Node("root")
	if root.Description() != "original" {
		t.Fatal("Immutable historical snapshot mutated")
	}
	if _, err := f.journal.Checkpoint(); err != nil {
		t.Fatal(err)
	}
}
func TestGraphOwnerIndependentPermissionsExactApprovalExpiryAndCurrentFences(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	if _, err := f.owner.PreviewBootstrap(context.Background(), f.bootstrap(), graphPreviewExpiry); err == nil {
		t.Fatal("Implicit request identity fallback")
	}
	f.mu.Lock()
	f.permissions = []string{"graph.roots"}
	f.mu.Unlock()
	if _, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry); err == nil {
		t.Fatal("Independent bootstrap permission bypassed")
	}
	f.mu.Lock()
	f.permissions = []string{"graph.bootstrap", "graph.roots"}
	f.mu.Unlock()
	for _, mode := range []string{"actor", "subject", "expiry"} {
		f.mu.Lock()
		f.badProof = mode
		f.mu.Unlock()
		if _, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry); err == nil {
			t.Fatal("Wrong approval proof accepted", mode)
		}
	}
	f.mu.Lock()
	f.badProof = ""
	f.mu.Unlock()
	stale, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.version = "2"
	f.mu.Unlock()
	if _, err := f.owner.Commit(f.context, stale, GraphCommitOptions{}); err == nil {
		t.Fatal("Changed current authority consumed preview")
	}
	fresh, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.beforeDecision = func(admission GraphAdmission) {
		if admission.Operation == "graph.bootstrap" {
			if _, err := f.owner.Read(f.context, "graph", ""); err == nil {
				t.Error("Callback graph reentry accepted")
			}
			f.mu.Lock()
			f.now = graphPreviewExpiry
			f.mu.Unlock()
		}
	}
	f.mu.Unlock()
	if _, err := f.owner.Commit(f.context, fresh, GraphCommitOptions{}); err == nil {
		t.Fatal("Preview expiry across final decision ignored")
	}
	if watermark, _ := f.journal.Watermark(); watermark != "0" {
		t.Fatal("Failed bootstrap published")
	}
	f.mu.Lock()
	f.beforeDecision, f.now = nil, "2026-11-07T04:00:00Z"
	f.mu.Unlock()
	f.boot()
	f.mu.Lock()
	f.permissions = []string{"graph.bootstrap"}
	f.mu.Unlock()
	raw := f.transaction("1", "root-noop", "original")
	raw["commands"] = []any{JournalObject{"op": "remove_root", "node_id": "root"},
		JournalObject{"op": "add_root", "node_id": "root", "plane": "model", "position": 0}}
	if _, err := f.owner.Preview(f.context, raw, graphPreviewExpiry); err == nil {
		t.Fatal("Noop root intent bypassed independent roots permission")
	}
}
func TestGraphOwnerFullReadGatesReceiptsHistoricalSnapshotsAndPayload(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	seed, _ := f.boot()
	f.mu.Lock()
	f.scope = []string{"root"}
	f.mu.Unlock()
	if _, err := f.owner.Read(f.context, "graph", "1"); err == nil {
		t.Fatal("Partial read scope leaked complete definition")
	}
	if _, err := f.owner.History(f.context, "graph"); err == nil {
		t.Fatal("Partial scope read complete history")
	}
	if _, err := f.owner.Receipt(f.context, "graph", "seed"); err == nil {
		t.Fatal("Receipt bypassed current full-read admission")
	}
	if _, err := f.owner.Commit(f.context, seed, GraphCommitOptions{}); err == nil {
		t.Fatal("Idempotent receipt retry bypassed current full read")
	}
	empty := newGraphOwnerFixture(t, nil)
	ref, err := empty.journal.PutBlob(journalBytes(empty.definition.AsJSON()), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	raw := empty.bootstrap()
	raw["seed"] = JournalObject{"blob": ref}
	empty.mu.Lock()
	empty.payloadAllowed = false
	empty.mu.Unlock()
	if _, err := empty.owner.PreviewBootstrap(empty.context, raw, graphPreviewExpiry); err == nil {
		t.Fatal("Canonical payload read bypassed current payload authority")
	}
	empty.mu.Lock()
	empty.payloadAllowed = true
	empty.mu.Unlock()
	preview, err := empty.owner.PreviewBootstrap(empty.context, raw, graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.owner.Commit(empty.context, preview, GraphCommitOptions{}); err != nil {
		t.Fatal("Real same-Journal canonical payload did not commit", err)
	}
}
func TestGraphOwnerRealAppendFailureReopenRecoveryAndOriginalCapability(t *testing.T) {
	for _, stage := range []string{"record.temp_written", "record.renamed"} {
		t.Run(stage, func(t *testing.T) {
			var armed atomic.Bool
			f := newGraphOwnerFixture(t, func(actual string) error {
				if actual == stage && armed.Swap(false) {
					return errors.New("native graph publication fault")
				}
				return nil
			})
			preview, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry)
			if err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			if receipt, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err == nil || receipt.JSON() != nil || !f.owner.WritesStopped() {
				t.Fatal("Uncertain/failed append invented head/receipt", receipt.JSON(), err)
			}
			if _, err := f.owner.Read(f.context, "graph", ""); err == nil {
				t.Fatal("Stopped owner continued reads/writes")
			}
			if err := f.owner.Recover(f.context, f.journal); err == nil {
				t.Fatal("Recovery skipped physical close/reopen")
			}
			f.reopen()
			retry, err := f.owner.Commit(f.context, preview, GraphCommitOptions{})
			if err != nil || journalMap(retry.JSON())["journal_seq"] != "1" {
				t.Fatal("Recovered actual facts/capability retry failed", retry.JSON(), err)
			}
			if watermark, _ := f.journal.Watermark(); watermark != "1" {
				t.Fatal("Recovery duplicated publication")
			}
		})
	}
}
func TestGraphOwnerApprovalFractionalPrecisionAndKernelBudgets(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	f.mu.Lock()
	f.now, f.approvalExpiry = "2026-11-07T04:00:00.000000Z", "2026-11-07T04:00:00.000001Z"
	f.mu.Unlock()
	preview, err := f.owner.Preview(f.context, f.transaction("1", "microsecond", "changed"), "2026-11-07T04:00:00.000010Z")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.beforeDecision = func(admission GraphAdmission) {
		if admission.Operation == "graph.transition" {
			f.mu.Lock()
			f.now = "2026-11-07T04:00:00.000001Z"
			f.mu.Unlock()
		}
	}
	f.mu.Unlock()
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err == nil {
		t.Fatal("Approval expiry collapsed microseconds to milliseconds")
	}
	if watermark, _ := f.journal.Watermark(); watermark != "1" {
		t.Fatal("Expired proof appended")
	}
	if err := f.owner.Close(); err != nil {
		t.Fatal(err)
	}
	limits := managed.DefaultTransactionLimits()
	limits.MaxNodes = 1
	owner, err := NewFileJournalGraphOwner(f.journal, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock, Limits: &limits})
	if err != nil {
		t.Fatal(err)
	}
	f.owner = owner
	f.mu.Lock()
	f.now, f.approvalExpiry, f.beforeDecision = "2026-11-07T04:00:00Z", "2027-01-01T00:00:00Z", nil
	f.mu.Unlock()
	if _, err := owner.Preview(f.context, f.transaction("1", "budget", "changed"), graphPreviewExpiry); err == nil {
		t.Fatal("Existing native node processing limit was bypassed")
	}
	other := newGraphOwnerFixture(t, nil)
	if err := owner.Recover(f.context, other.journal); err == nil {
		t.Fatal("Other physical store rebound as same origin")
	}
}
func TestGraphOwnerTombstoneHistoryReconstructionAndRestore(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	raw := f.transaction("1", "delete", "original")
	raw["commands"] = []any{JournalObject{"op": "delete_nodes", "targets": []any{"hidden"}, "deleted_ids": []any{"hidden"}, "policy": "reject_references",
		"disconnections": []any{JournalObject{"source_id": "root", "target_id": "hidden", "relation": "containment"}}}}
	preview, err := f.owner.Preview(f.context, raw, graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err != nil {
		t.Fatal(err)
	}
	f.reopen()
	state, err := f.owner.Read(f.context, "graph", "")
	if err != nil || len(journalArray(state.Tombstones())) != 1 {
		t.Fatal("Real replay lost retained creation identity", state.Tombstones(), err)
	}
	restore := f.transaction("2", "restore", "original")
	restore["commands"] = []any{JournalObject{"op": "restore_nodes", "revision": "1", "node_ids": []any{"hidden"}},
		JournalObject{"op": "attach_node", "node_id": "hidden", "target": JournalObject{"parent_id": "root", "slot": "skills", "position": 0}}}
	preview, err = f.owner.Preview(f.context, restore, graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := f.owner.Read(f.context, "graph", "")
	definition := restored.Definition()
	if _, ok := definition.Node("hidden"); err != nil || !ok {
		t.Fatal("Exact authorized historical restoration failed", err)
	}
}
