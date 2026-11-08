package managed_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

type testAuthority struct {
	mu      sync.Mutex
	release m.CapturedRelease
	allowed bool
	fence   func(m.ServingAttempt, *m.ReadyGeneration, func() error) (bool, error)
}

func (a *testAuthority) Capture() (*m.CapturedRelease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	copy := a.release
	return &copy, nil
}
func (a *testAuthority) Fence(attempt m.ServingAttempt, ready *m.ReadyGeneration, adopt func() error) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fence != nil {
		return a.fence(attempt, ready, adopt)
	}
	if !a.allowed || a.release.ReleaseEpoch != attempt.Release.ReleaseEpoch ||
		a.release.JournalSeq != attempt.Release.JournalSeq || a.release.ManifestDigest != ready.Plan().Manifest().Digest() {
		return false, nil
	}
	if err := adopt(); err != nil {
		return false, err
	}
	return true, nil
}
func (a *testAuthority) set(release m.CapturedRelease) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.release = release
}

type harness struct {
	t                         *testing.T
	registry                  *m.RegistryGeneration
	read, write               *m.BindingRegistration
	authority                 *testAuthority
	coordinator               *m.LocalServingCoordinator
	runtime                   *m.ManagedRuntime
	mu                        sync.Mutex
	events                    []string
	generations               map[string]*m.ReadyGeneration
	providerHook              func(context.Context, m.ServingAttempt) error
	handler                   func(context.Context, map[string]any, m.CallContext) (any, error)
	policy                    func(context.Context, m.RequestAuthorization) ([]string, error)
	decodes, validates, calls atomic.Int32
}

func node(id, kind string) map[string]any {
	n := map[string]any{"node_id": id, "kind": kind, "name": id, "description": "Description"}
	if kind != "tool" {
		n["instructions"] = ""
	}
	return n
}
func (h *harness) definition(revision string, extra bool) *m.GraphDefinition {
	h.t.Helper()
	root := node("root", "role")
	root["instructions"] = "revision " + revision
	tool, write, prompt := node("tool", "tool"), node("write", "tool"), node("prompt", "tool")
	tool["binding"], write["binding"], prompt["binding"] = h.read.Descriptor().AsJSON(), h.write.Descriptor().AsJSON(), h.read.Descriptor().AsJSON()
	toolIDs := []any{"tool", "write"}
	nodes := []any{root, tool, write, prompt}
	if extra {
		added := node("added", "tool")
		added["binding"] = h.read.Descriptor().AsJSON()
		nodes = append(nodes, added)
		toolIDs = append(toolIDs, "added")
	}
	root["members"] = map[string]any{"tools": toolIDs}
	raw := map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "graph",
		"application_name": "Application", "mode": "runtime", "nodes": nodes,
		"roots":         []any{map[string]any{"node_id": "root", "plane": "model"}, map[string]any{"node_id": "prompt", "plane": "prompt"}},
		"presentations": map[string]any{"default_locale": "en", "catalogs": []any{}}}
	definition, err := m.NewGraphDefinition(raw, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return definition
}
func (h *harness) compile(definition *m.GraphDefinition) *m.StructuralPlan {
	h.t.Helper()
	plan, err := m.Compile(definition, m.CompilerOptions{Registry: h.registry, Locale: "en", CompilerContractVersion: "1",
		CompilerBuild: "go-test", DependencyContractID: "app", DependencyBuild: "app-build"})
	if err != nil {
		h.t.Fatal(err)
	}
	return plan
}
func (h *harness) release(epoch string, extra bool) m.CapturedRelease {
	h.t.Helper()
	definition := h.definition(epoch, extra)
	state, err := m.NewRevisionState(definition, epoch, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return m.CapturedRelease{GraphID: "graph", TargetID: "target", ReleaseEpoch: epoch, JournalSeq: epoch,
		Snapshot: state, ManifestDigest: h.compile(definition).Manifest().Digest()}
}
func (h *harness) event(value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, value)
}
func (h *harness) prepare(ctx context.Context, attempt m.ServingAttempt) (*m.ReadyGeneration, error) {
	if h.providerHook != nil {
		if err := h.providerHook(ctx, attempt); err != nil {
			return nil, err
		}
	}
	definition := attempt.Release.Snapshot.Definition()
	plan := h.compile(&definition)
	generation, err := m.PrepareGeneration(ctx, plan, m.PreparationContext{GraphID: "graph", Revision: attempt.Release.Snapshot.Revision(), GenerationUUID: attempt.GenerationUUID}, h.adapters(plan, attempt.GenerationUUID))
	if err == nil {
		h.mu.Lock()
		h.generations[attempt.GenerationUUID] = generation
		h.mu.Unlock()
	}
	return generation, err
}
func (h *harness) adapters(plan *m.StructuralPlan, generation string) map[string]m.PreparationAdapterRegistration {
	adapters := map[string]m.PreparationAdapterRegistration{}
	for _, id := range plan.Index().Preorder() {
		resolved, ok := plan.Binding(id)
		if !ok {
			continue
		}
		registration := resolved.Registration()
		descriptor := registration.Descriptor()
		adapters[id] = m.PreparationAdapterRegistration{BindingID: descriptor.BindingID(), Version: descriptor.Version(), ABI: m.NativeABI, ContractDigest: registration.ContractDigest(),
			Acquire: func(_ context.Context, _ m.ResolvedBinding, id string, _ m.PreparationContext) (m.PreparedResource, error) {
				h.event("acquire:" + generation + ":" + id)
				return m.PreparedResource{Handle: generation + ":" + id, Dispose: func(context.Context) error {
					h.event("dispose:" + generation + ":" + id)
					return nil
				}}, nil
			}}
	}
	return adapters
}
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, generations: map[string]*m.ReadyGeneration{}}
	options := func(id, effect string) m.BindingRegistrationOptions[map[string]any] {
		return m.BindingRegistrationOptions[map[string]any]{BindingID: id, Version: "1", Kind: "registered", Effect: effect,
			ABI: m.NativeABI, ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
			Portability:         m.Portability{Kind: "native", Language: "go", Version: "1.25", Factory: "test"},
			InputSchema:         map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []any{"x"}, "additionalProperties": false},
			ValidatorContractID: "validator", DecoderContractID: "decoder", DefaultContractID: "defaults.none", CoercionContractID: "coercion.none",
			Decoder: func(args m.FrozenJSON) (map[string]any, error) {
				h.decodes.Add(1)
				value, ok := args.JSON().(map[string]any)
				if !ok {
					return nil, errors.New("expected typed map")
				}
				return value, nil
			},
			Validator: func(input map[string]any) error {
				h.validates.Add(1)
				if input["x"] == "invalid" {
					return errors.New("invalid native input")
				}
				return nil
			},
			Handler: func(ctx context.Context, input map[string]any, call m.CallContext) (any, error) {
				h.calls.Add(1)
				if h.handler != nil {
					return h.handler(ctx, input, call)
				}
				return map[string]any{"x": input["x"], "resource": call.Resource, "actor": call.Identity.ActorID}, nil
			}}
	}
	var err error
	h.read, err = m.NewBindingRegistration(options("read", "read_only"))
	if err != nil {
		t.Fatal(err)
	}
	h.write, err = m.NewBindingRegistration(options("write", "write"))
	if err != nil {
		t.Fatal(err)
	}
	h.registry, err = m.NewRegistryGeneration("registry", h.read, h.write)
	if err != nil {
		t.Fatal(err)
	}
	h.authority = &testAuthority{allowed: true}
	h.authority.set(h.release("1", false))
	h.coordinator, err = m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "instance", Authority: h.authority, Provider: m.ServingPreparationFunc(h.prepare)})
	if err != nil {
		t.Fatal(err)
	}
	h.runtime, err = m.NewManagedRuntime(h.coordinator, m.RequestAuthorityFunc(func(ctx context.Context, request m.RequestAuthorization) ([]string, error) {
		if h.policy != nil {
			return h.policy(ctx, request)
		}
		return request.RequestedIDs, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.coordinator.Shutdown(ctx); err != nil {
			t.Error("shutdown:", err)
		}
	})
	return h
}
func identity(t *testing.T, actor string) context.Context {
	t.Helper()
	ctx, err := m.WithIdentity(context.Background(), m.Identity{ActorID: actor, TenantID: "tenant", AuthorityRevision: "policy-1"})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
func mustServe(t *testing.T, h *harness) m.LocalServingResult {
	t.Helper()
	result := h.coordinator.Prepare(context.Background())
	if result.Status != "serving" || result.Error != nil {
		t.Fatal(result.Status, result.Error)
	}
	return result
}
func await[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("barrier timed out")
		var zero T
		return zero
	}
}
func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		runtime.Gosched()
	}
}

func TestGenerationPreflightAndReverseCleanup(t *testing.T) {
	h := newHarness(t)
	plan := h.compile(h.definition("1", false))
	preparation := m.PreparationContext{GraphID: "graph", Revision: "1", GenerationUUID: "01234567-89ab-cdef-0123-456789abcdef"}
	adapters := h.adapters(plan, "test")
	bad := adapters["prompt"]
	bad.ContractDigest = "sha256:" + strings.Repeat("0", 64)
	adapters["prompt"] = bad
	if _, err := m.PrepareGeneration(context.Background(), plan, preparation, adapters); err == nil || len(h.events) != 0 {
		t.Fatal("all adapter attestations must preflight before acquisition")
	}
	adapters = h.adapters(plan, "test")
	failing := adapters["prompt"]
	failing.Acquire = func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
		return m.PreparedResource{}, errors.New("acquisition failed")
	}
	adapters["prompt"] = failing
	tool := adapters["tool"]
	tool.Acquire = func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
		h.event("acquire:test:tool")
		return m.PreparedResource{Dispose: func(context.Context) error {
			h.event("dispose:test:tool")
			return errors.New("cleanup failed")
		}}, nil
	}
	adapters["tool"] = tool
	if _, err := m.PrepareGeneration(context.Background(), plan, preparation, adapters); err == nil || !strings.Contains(err.Error(), "acquisition failed") || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatal("preparation must join acquisition and cleanup failures", err)
	}
	expected := []string{"acquire:test:tool", "acquire:test:write", "dispose:test:write", "dispose:test:tool"}
	if !reflect.DeepEqual(h.events, expected) {
		t.Fatal(h.events)
	}
}
func TestGenerationRetentionCancellationAndRealDrain(t *testing.T) {
	h := newHarness(t)
	served := mustServe(t, h)
	generation, _ := h.coordinator.Current()
	keeper, err := generation.Generation.Retain()
	if err != nil {
		t.Fatal(err)
	}
	entered, err := generation.Generation.Lease()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := generation.Generation.Retire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := generation.Generation.Lease(); err == nil {
		t.Fatal("ordinary admission remained open")
	}
	child, err := keeper.Lease()
	if err != nil {
		t.Fatal("preexisting retention must admit draining children", err)
	}
	keeper.Close()
	if _, err := keeper.Lease(); err == nil {
		t.Fatal("closed keeper admitted child")
	}
	entered.Close()
	if _, err := child.Handle("tool"); err != nil || generation.Generation.ResourceState() != "draining" {
		t.Fatal("keeper expiry prematurely cleaned entered child", err)
	}
	child.Close()
	if err := generation.Generation.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if generation.Generation.ResourceState() != "retired" {
		t.Fatal("retirement did not finish")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.events)
	want := []string{"dispose:" + served.Attempt.GenerationUUID + ":prompt", "dispose:" + served.Attempt.GenerationUUID + ":write", "dispose:" + served.Attempt.GenerationUUID + ":tool"}
	if !reflect.DeepEqual(h.events[n-3:], want) {
		t.Fatal(h.events)
	}
}
func TestGenerationCancellationJoinsUncooperativeAcquisition(t *testing.T) {
	h := newHarness(t)
	plan := h.compile(h.definition("1", false))
	adapters := h.adapters(plan, "cancel")
	entered, release := make(chan struct{}), make(chan struct{})
	adapter := adapters["tool"]
	adapter.Acquire = func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
		close(entered)
		<-release
		return m.PreparedResource{Dispose: func(context.Context) error { h.event("disposed"); return nil }}, nil
	}
	adapters["tool"] = adapter
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.PrepareGeneration(ctx, plan, m.PreparationContext{GraphID: "graph", Revision: "1", GenerationUUID: "01234567-89ab-cdef-0123-456789abcdef"}, adapters)
		done <- err
	}()
	await(t, entered)
	cancel()
	select {
	case <-done:
		t.Fatal("cancellation disowned acquisition")
	default:
	}
	close(release)
	if err := await(t, done); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(h.events, []string{"disposed"}) {
		t.Fatal(err, h.events)
	}
}
func TestServingLatePreparationCannotReplaceNewerIntent(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.providerHook = func(_ context.Context, attempt m.ServingAttempt) error {
		if attempt.Release.ReleaseEpoch == "2" {
			close(entered)
			<-release
		}
		return nil
	}
	h.authority.set(h.release("2", false))
	late := make(chan m.LocalServingResult, 1)
	go func() { late <- h.coordinator.Prepare(context.Background()) }()
	await(t, entered)
	h.authority.set(h.release("3", false))
	newest := mustServe(t, h)
	close(release)
	if result := await(t, late); result.Status != "superseded" || result.Error != nil {
		t.Fatal(result.Status, result.Error)
	}
	active, ok := h.coordinator.Current()
	if !ok || active.Attempt.GenerationUUID != newest.Attempt.GenerationUUID {
		t.Fatal("late candidate replaced desired generation")
	}
}
func TestServingAuthorityFenceLKGAndOneShotCapability(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	h.authority.set(h.release("2", false))
	var saved func() error
	h.authority.mu.Lock()
	h.authority.fence = func(_ m.ServingAttempt, _ *m.ReadyGeneration, adopt func() error) (bool, error) {
		saved = adopt
		return false, nil
	}
	h.authority.mu.Unlock()
	if result := h.coordinator.Prepare(context.Background()); result.Status != "superseded" {
		t.Fatal(result)
	}
	if err := saved(); err == nil {
		t.Fatal("late saved adoption capability remained open")
	}
	current, _ := h.coordinator.Current()
	if current.Attempt.GenerationUUID != first.Attempt.GenerationUUID {
		t.Fatal("fence rejection lost LKG")
	}
	h.authority.mu.Lock()
	h.authority.fence = func(_ m.ServingAttempt, _ *m.ReadyGeneration, adopt func() error) (bool, error) {
		if err := adopt(); err != nil {
			return false, err
		}
		if err := adopt(); err == nil {
			return false, errors.New("duplicate adoption accepted")
		}
		return true, nil
	}
	h.authority.mu.Unlock()
	mustServe(t, h)
}
func TestServingWatermarksAndFailurePreserveLKG(t *testing.T) {
	for _, mutation := range []string{"epoch", "sequence", "same epoch", "manifest"} {
		t.Run(mutation, func(t *testing.T) {
			h := newHarness(t)
			first := mustServe(t, h)
			release := h.release("2", false)
			switch mutation {
			case "epoch":
				release.ReleaseEpoch = "0"
			case "sequence":
				release.JournalSeq = "1"
			case "same epoch":
				release.ReleaseEpoch = "1"
			case "manifest":
				release.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
			}
			h.authority.set(release)
			result := h.coordinator.Prepare(context.Background())
			if result.Status != "failed" || result.Error == nil {
				t.Fatal("invalid release accepted", result)
			}
			active, _ := h.coordinator.Current()
			if active.Attempt.GenerationUUID != first.Attempt.GenerationUUID {
				t.Fatal("failed candidate replaced LKG")
			}
		})
	}
}
func TestServingExclusiveOwnershipAndRequiredAuthority(t *testing.T) {
	if _, err := m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "instance", Provider: m.ServingPreparationFunc(func(context.Context, m.ServingAttempt) (*m.ReadyGeneration, error) { return nil, nil })}); err == nil {
		t.Fatal("missing trusted release authority accepted")
	}
	h := newHarness(t)
	mustServe(t, h)
	active, _ := h.coordinator.Current()
	other, err := m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "other", Authority: h.authority,
		Provider: m.ServingPreparationFunc(func(context.Context, m.ServingAttempt) (*m.ReadyGeneration, error) { return active.Generation, nil })})
	if err != nil {
		t.Fatal(err)
	}
	if result := other.Prepare(context.Background()); result.Status != "failed" || active.Generation.ResourceState() != "prepared" {
		t.Fatal("generation reuse harmed original serving owner", result)
	}
	if err := other.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestServingShutdownJoinsLateUncooperativePreparation(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	h.authority.set(h.release("2", false))
	entered, release := make(chan struct{}), make(chan struct{})
	h.providerHook = func(context.Context, m.ServingAttempt) error { close(entered); <-release; return nil }
	done := make(chan m.LocalServingResult, 1)
	go func() { done <- h.coordinator.Prepare(context.Background()) }()
	await(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.coordinator.Shutdown(ctx); !errors.Is(err, context.Canceled) || !h.coordinator.Closed() {
		t.Fatal("shutdown did not close admission or fabricated drain", err)
	}
	if _, err := h.coordinator.Lease(); err == nil {
		t.Fatal("shutdown admission stayed open")
	}
	close(release)
	if result := await(t, done); result.Status == "serving" {
		t.Fatal("late preparation adopted after shutdown")
	}
	if err := h.coordinator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeContextReferencesSchemaAndReadOnlyDoor(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	if err := h.runtime.Request(context.Background(), m.ManagedRequestOptions{}, func(*m.ManagedRequest) error { return nil }); !errors.Is(err, m.ErrDenied) {
		t.Fatal("ambient identity fallback", err)
	}
	var escaped *m.ManagedRequest
	err := h.runtime.Request(identity(t, "alice"), m.ManagedRequestOptions{}, func(request *m.ManagedRequest) error {
		escaped = request
		valid, err := request.Reference("tool")
		if err != nil {
			return err
		}
		for _, bad := range []string{"wrong", mustReference(t, "other", request.GenerationUUID(), "tool"), mustReference(t, "graph", "00000000-0000-0000-0000-000000000000", "tool"), mustReference(t, "graph", request.GenerationUUID(), "prompt")} {
			if _, err := request.Invoke(bad, map[string]any{"x": "word"}, false); err == nil {
				return fmt.Errorf("invalid reference accepted: %s", bad)
			}
		}
		write, _ := request.Reference("write")
		if _, err := request.Invoke(write, map[string]any{"x": "word"}, true); err == nil {
			return errors.New("write binding entered read-only door")
		}
		for _, args := range []any{map[string]any{}, map[string]any{"x": 1}, map[string]any{"x": "word", "extra": true}} {
			if _, err := request.Invoke(valid, args, false); err == nil {
				return errors.New("invalid schema entered native decoder")
			}
		}
		if h.decodes.Load() != 0 || h.calls.Load() != 0 {
			return errors.New("native code ran before reference/effect/schema gates")
		}
		info, err := request.Inspect(valid)
		if err != nil {
			return err
		}
		if data, ok := info.JSON().(map[string]any); !ok || data["effect"] != "read_only" {
			return errors.New("inspect missing captured binding")
		}
		result, err := request.Invoke(valid, map[string]any{"x": "word"}, true)
		if err != nil {
			return err
		}
		data, ok := result.JSON().(map[string]any)
		if !ok || data["actor"] != "alice" || data["resource"] != request.GenerationUUID()+":tool" {
			return errors.New("native context/resource mixed generations")
		}
		return nil
	})
	if err != nil || h.calls.Load() != 1 {
		t.Fatal(err, h.calls.Load())
	}
	if _, err := escaped.Reference("tool"); err == nil {
		t.Fatal("escaped reference remained live")
	}
	if _, err := escaped.Invoke("wrong", map[string]any{}, false); err == nil {
		t.Fatal("escaped invocation remained live")
	}
}
func mustReference(t *testing.T, graph, generation, node string) string {
	t.Helper()
	ref, err := m.FormatReference(graph, generation, node)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func TestRuntimeAttenuationCannotReexpand(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	var granted atomic.Bool
	granted.Store(true)
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if granted.Load() {
			return append(request.RequestedIDs, "prompt", "added"), nil
		}
		return []string{"root"}, nil
	}
	err := h.runtime.Request(identity(t, "alice"), m.ManagedRequestOptions{Selection: []string{"tool"}}, func(request *m.ManagedRequest) error {
		if _, err := request.Reference("write"); err == nil {
			return errors.New("initial subtree ceiling expanded")
		}
		ref, _ := request.Reference("tool")
		granted.Store(false)
		if _, err := request.Invoke(ref, map[string]any{"x": "word"}, false); err == nil {
			return errors.New("revocation not enforced")
		}
		granted.Store(true)
		if _, err := request.Invoke(ref, map[string]any{"x": "word"}, false); err == nil {
			return errors.New("attenuated request regained authority")
		}
		return nil
	})
	if err != nil || h.calls.Load() != 0 {
		t.Fatal(err, h.calls.Load())
	}
}
func TestRuntimePointerMovementAcrossAuthorizationFailsClosed(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if request.Operation == "invoke" {
			close(entered)
			<-release
		}
		return request.RequestedIDs, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- h.runtime.Request(identity(t, "alice"), m.ManagedRequestOptions{}, func(request *m.ManagedRequest) error {
			ref, _ := request.Reference("tool")
			_, err := request.Invoke(ref, map[string]any{"x": "word"}, false)
			return err
		})
	}()
	await(t, entered)
	h.authority.set(h.release("2", false))
	mustServe(t, h)
	close(release)
	if err := await(t, done); !errors.Is(err, m.ErrDenied) || h.calls.Load() != 0 {
		t.Fatal("authority await mixed serving generations", err)
	}
}
func TestRuntimeCopiesArgumentsBeforeAuthorizationAwait(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if request.Operation == "invoke" {
			close(entered)
			<-release
		}
		return request.RequestedIDs, nil
	}
	args := map[string]any{"x": "original"}
	var output m.FrozenJSON
	done := make(chan error, 1)
	go func() {
		done <- h.runtime.Request(identity(t, "alice"), m.ManagedRequestOptions{}, func(request *m.ManagedRequest) error {
			ref, _ := request.Reference("tool")
			var err error
			output, err = request.Invoke(ref, args, false)
			return err
		})
	}()
	await(t, entered)
	args["x"] = "mutated"
	close(release)
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	value, ok := output.JSON().(map[string]any)
	if !ok || value["x"] != "original" {
		t.Fatal("caller arguments alias crossed authority await", output.JSON())
	}
}
func TestRuntimeScopeJoinsUnawaitedCancelledHandler(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.handler = func(_ context.Context, input map[string]any, _ m.CallContext) (any, error) {
		close(entered)
		<-release
		return input, nil
	}
	ctx, cancel := context.WithCancel(identity(t, "alice"))
	actionReturned := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.runtime.Request(ctx, m.ManagedRequestOptions{}, func(request *m.ManagedRequest) error {
			ref, _ := request.Reference("tool")
			go func() { _, _ = request.Invoke(ref, map[string]any{"x": "word"}, false) }()
			await(t, entered)
			close(actionReturned)
			return nil
		})
	}()
	await(t, actionReturned)
	cancel()
	h.authority.set(h.release("2", false))
	mustServe(t, h)
	select {
	case <-done:
		t.Fatal("scope disowned unawaited handler")
	default:
	}
	close(release)
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
