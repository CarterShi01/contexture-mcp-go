package managed_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

// These are identified trusted-port CONTRACT fixtures, not durable owners.
type readTreeOptions struct {
	width                        int
	label, hidden, program       string
	description, lateDescription string
	omit                         string
	prefixHidden, templateHidden bool
}

func readTree(t *testing.T, o readTreeOptions) *m.GraphDefinition {
	t.Helper()
	if o.width == 0 {
		o.width = 3
	}
	if o.label == "" {
		o.label = "Owner"
	}
	if o.hidden == "" {
		o.hidden = "SECRET"
	}
	if o.program == "" {
		o.program = "ORIGINAL PROGRAM"
	}
	if o.description == "" {
		o.description = "Tool description"
	}
	owner := node("owner", "role")
	owner["name"], owner["instructions"] = o.label, o.program
	tools, uses := []any{}, []any{}
	nodes := []any{owner}
	for _, id := range []string{"pre", "branch", "post", "skill", "hidden"} {
		kind := "role"
		if id == "skill" {
			kind = "skill"
		}
		n := node(id, kind)
		n["instructions"] = id + " program"
		if id == "pre" {
			n["role_variant"] = "pre_process"
		}
		if id == "post" {
			n["role_variant"] = "post_process"
		}
		if id == "hidden" {
			n["name"], n["description"], n["instructions"] = o.hidden, "SECRET description", "SECRET PROGRAM"
		}
		nodes = append(nodes, n)
	}
	children := []any{"branch"}
	if o.prefixHidden {
		n := node("interleaved", "role")
		n["description"] = "PRIVATE prefix"
		nodes = append(nodes, n)
		children = []any{"interleaved", "branch"}
	}
	for i := range o.width {
		id := fmt.Sprintf("tool%d", i)
		if id == o.omit {
			continue
		}
		n := node(id, "tool")
		n["description"] = o.description
		if i == o.width-1 && o.lateDescription != "" {
			n["description"] = o.lateDescription
		}
		nodes, tools, uses = append(nodes, n), append(tools, id), append(uses, id)
	}
	owner["members"] = map[string]any{"pre_process": "pre", "children": children, "post_process": "post", "skills": []any{"skill"}, "tools": tools}
	owner["uses"] = append(uses, "skill", "hidden")
	if o.templateHidden {
		delete(owner, "instructions")
		owner["instruction_template"] = map[string]any{"parts": []any{map[string]any{"kind": "NodeRef", "target_id": "hidden"}}}
	}
	definition, err := m.NewGraphDefinition(map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1",
		"graph_id": "graph", "application_name": "SECRET application", "mode": "disclosure_only", "nodes": nodes,
		"roots": []any{map[string]any{"node_id": "owner", "plane": "model"}, map[string]any{"node_id": "hidden", "plane": "prompt"}},
		"presentations": map[string]any{"default_locale": "en", "catalogs": []any{map[string]any{"locale": "en",
			"entries": []any{map[string]any{"node_id": "owner", "title": "Localized", "description": "Localized description"}}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
func readIndex(t *testing.T, definition *m.GraphDefinition) *m.GraphIndex {
	t.Helper()
	index, err := graph.BuildGraphIndex(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	return index
}
func readState(t *testing.T, definition *m.GraphDefinition, revision string) m.RevisionState {
	t.Helper()
	state, err := m.NewRevisionState(definition, revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func readSigner(t *testing.T) *m.CursorSigner {
	t.Helper()
	signer, err := m.NewCursorSigner([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

type readClock struct{ now atomic.Int64 }

func (c *readClock) clock() time.Duration    { return time.Duration(c.now.Load()) }
func (c *readClock) advance(d time.Duration) { c.now.Add(int64(d)) }

type readDiagnostic struct{ code string }

func (e readDiagnostic) Error() string { return "Fixture diagnostic" }
func (e readDiagnostic) Code() string  { return e.code }
func readCode(t *testing.T, err error, want string) {
	t.Helper()
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != want {
		t.Fatalf("error = %v; want code %s", err, want)
	}
}
func readJSON(t *testing.T, value m.FrozenJSON) map[string]any {
	t.Helper()
	obj, ok := value.JSON().(map[string]any)
	if !ok {
		t.Fatal("Expected object")
	}
	return obj
}
func readMap(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("Expected object, got %T", value)
	}
	return out
}
func readArray(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	if !ok {
		t.Fatalf("Expected array, got %T", value)
	}
	return out
}
func readText(t *testing.T, value any) string {
	t.Helper()
	out, ok := value.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", value)
	}
	return out
}
func readBytes(t *testing.T, value m.FrozenJSON) []byte {
	t.Helper()
	raw, err := value.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func readEqual(t *testing.T, a, b m.FrozenJSON) {
	t.Helper()
	if string(readBytes(t, a)) != string(readBytes(t, b)) {
		t.Fatal("Expected exact retry-stable bytes")
	}
}

type definitionReadFixture struct {
	mu       sync.Mutex
	current  *m.GraphIndex
	revision string
	history  map[string]*m.GraphIndex
	scope    []string
	id       m.Identity
	origin   string
	err      error
	calls    int
}

func newDefinitionReadFixture(t *testing.T, definition *m.GraphDefinition) *definitionReadFixture {
	index := readIndex(t, definition)
	scope := []string{}
	for _, id := range index.Preorder() {
		if id != "hidden" && id != "interleaved" {
			scope = append(scope, id)
		}
	}
	return &definitionReadFixture{current: index, revision: "1", history: map[string]*m.GraphIndex{"1": index},
		scope: scope, id: m.Identity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "policy-1"}, origin: "store:incarnation:1"}
}
func (f *definitionReadFixture) update(action func()) { f.mu.Lock(); defer f.mu.Unlock(); action() }
func (f *definitionReadFixture) advance(t *testing.T, definition *m.GraphDefinition) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(f.revision)
	f.revision = strconv.Itoa(n + 1)
	f.current = readIndex(t, definition)
	f.history[f.revision] = f.current
}
func (f *definitionReadFixture) Read(_ context.Context, r m.DefinitionReadRequest, dispatch func(m.DefinitionReadView) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return m.FrozenJSON{}, f.err
	}
	revision := f.revision
	if r.Source == m.CatalogRevision {
		revision = r.Revision
	}
	index := f.history[revision]
	if index == nil {
		return m.FrozenJSON{}, readDiagnostic{"history_unavailable"}
	}
	return dispatch(m.DefinitionReadView{Identity: f.id, StoreOrigin: f.origin, Revision: revision, Index: index,
		CurrentIndex: f.current, ScopeIDs: slices.Clone(f.scope)})
}
func catalogAll(t *testing.T, service *m.ManagedCatalogService, first m.FrozenJSON, r m.CatalogRequest, stream m.CatalogStream) []any {
	t.Helper()
	starts := readMap(t, readJSON(t, first)["streams"])
	token := readText(t, starts[string(stream)])
	items := []any{}
	for i := 0; i < 20000; i++ {
		value, err := service.Continuation(identity(t, "actor"), token, r, stream)
		if err != nil {
			t.Fatal(err)
		}
		obj := readJSON(t, value)
		items = append(items, readArray(t, obj["items"])...)
		if obj["complete"] == true {
			return items
		}
		if len(readArray(t, obj["items"])) == 0 {
			t.Fatal("Empty infinite continuation")
		}
		token = readText(t, obj["next_cursor"])
	}
	t.Fatal("Bounded traversal did not finish")
	return nil
}

type servingReadFixture struct {
	t           *testing.T
	fenceMu     sync.Mutex
	mu          sync.Mutex
	authority   *testAuthority
	coordinator *m.LocalServingCoordinator
	runtime     *m.ManagedRuntime
	scope       []string
	origin      string
	id          m.Identity
	policy      func(context.Context, m.RequestAuthorization) ([]string, error)
}

func readCompile(t *testing.T, d *m.GraphDefinition) *m.StructuralPlan {
	t.Helper()
	plan, err := m.Compile(d, m.CompilerOptions{NoExecutionRegistryDigest: "sha256:" + strings.Repeat("a", 64), Locale: "en",
		CompilerContractVersion: "1", CompilerBuild: "go-read-test", DependencyContractID: "app", DependencyBuild: "app-build"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func newServingReadFixture(t *testing.T, d *m.GraphDefinition) *servingReadFixture {
	f := &servingReadFixture{t: t, authority: &testAuthority{allowed: true}, origin: "store:incarnation:1",
		id: m.Identity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "policy-1"}}
	f.scope = readIndex(t, d).Preorder()
	plan := readCompile(t, d)
	f.authority.set(m.CapturedRelease{GraphID: "graph", TargetID: "target", ReleaseEpoch: "1", JournalSeq: "1",
		Snapshot: readState(t, d, "1"), ManifestDigest: plan.Manifest().Digest()})
	var err error
	f.coordinator, err = m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "instance",
		Authority: f.authority, Provider: m.ServingPreparationFunc(func(ctx context.Context, a m.ServingAttempt) (*m.ReadyGeneration, error) {
			definition := a.Release.Snapshot.Definition()
			return m.PrepareGeneration(ctx, readCompile(t, &definition), m.PreparationContext{GraphID: "graph",
				Revision: a.Release.Snapshot.Revision(), GenerationUUID: a.GenerationUUID}, nil)
		})})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime, err = m.NewManagedRuntime(f.coordinator, m.RequestAuthorityFunc(func(ctx context.Context, r m.RequestAuthorization) ([]string, error) {
		f.mu.Lock()
		scope, policy := slices.Clone(f.scope), f.policy
		f.mu.Unlock()
		if policy != nil {
			return policy(ctx, r)
		}
		return scope, nil
	}), readFixtureIdentity{f})
	if err != nil {
		t.Fatal(err)
	}
	result := f.coordinator.Prepare(context.Background())
	if result.Status != "serving" || result.Error != nil {
		t.Fatal(result)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.coordinator.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return f
}

type readFixtureIdentity struct{ fixture *servingReadFixture }

func (p readFixtureIdentity) Current(context.Context) (m.Identity, error) {
	p.fixture.mu.Lock()
	defer p.fixture.mu.Unlock()
	return p.fixture.id, nil
}
func (f *servingReadFixture) port() m.ServingReadPort {
	return m.ServingReadPort{Runtime: f.runtime, StoreOrigin: func(context.Context) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.origin, nil
	}, Fence: func(_ context.Context, dispatch func() (m.FrozenJSON, error)) (m.FrozenJSON, error) {
		f.fenceMu.Lock()
		defer f.fenceMu.Unlock()
		return dispatch()
	}}
}
func (f *servingReadFixture) update(action func()) {
	f.fenceMu.Lock()
	defer f.fenceMu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	action()
}
func (f *servingReadFixture) swap(d *m.GraphDefinition, revision string) {
	plan := readCompile(f.t, d)
	f.authority.set(m.CapturedRelease{GraphID: "graph", TargetID: "target", ReleaseEpoch: revision, JournalSeq: revision,
		Snapshot: readState(f.t, d, revision), ManifestDigest: plan.Manifest().Digest()})
	result := f.coordinator.Prepare(context.Background())
	if result.Status != "serving" || result.Error != nil {
		f.t.Fatal(result)
	}
}

type committedReadFixture struct {
	mu         sync.Mutex
	states     []m.CommittedDefinitionBatch
	scope      []string
	id         m.Identity
	origin     string
	watermark  string
	retained   string
	err        error
	overflow   bool
	calls      int
	lastBounds m.CommittedBatchBounds
	windowHook func(*m.CommittedBatchWindow)
}

func newCommittedReadFixture(t *testing.T) *committedReadFixture {
	return &committedReadFixture{states: []m.CommittedDefinitionBatch{{Position: "1", State: readState(t, readTree(t, readTreeOptions{}), "1")}},
		scope: []string{"owner"}, id: m.Identity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "policy-1"},
		origin: "store:incarnation:1", watermark: "1", retained: "0"}
}
func (f *committedReadFixture) update(action func()) { f.mu.Lock(); defer f.mu.Unlock(); action() }
func (f *committedReadFixture) append(t *testing.T, d *m.GraphDefinition) {
	f.mu.Lock()
	defer f.mu.Unlock()
	position, _ := strconv.Atoi(f.watermark)
	position++
	f.watermark = strconv.Itoa(position)
	f.states = append(f.states, m.CommittedDefinitionBatch{Position: f.watermark, State: readState(t, d, f.watermark)})
}
func (f *committedReadFixture) Read(ctx context.Context, _ string, dispatch func(m.CommittedReadCut) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return m.FrozenJSON{}, f.err
	}
	states := slices.Clone(f.states)
	watermark := f.watermark
	return dispatch(m.CommittedReadCut{Identity: f.id, StoreOrigin: f.origin, ScopeIDs: slices.Clone(f.scope),
		Snapshot: states[len(states)-1].State, Watermark: watermark, RetainedAfter: f.retained,
		ReadBatches: func(_ context.Context, after string, bounds m.CommittedBatchBounds) (m.CommittedBatchWindow, error) {
			f.lastBounds = bounds
			position, _ := strconv.Atoi(after)
			window := m.CommittedBatchWindow{Before: states[0].State, Through: watermark, Overflow: f.overflow}
			rawBytes := 0
			for _, batch := range states {
				seq, _ := strconv.Atoi(batch.Position)
				if seq <= position {
					window.Before = batch.State
				} else {
					window.Batches = append(window.Batches, batch)
					d := batch.State.Definition()
					raw, err := graph.CanonicalJSON(d.AsJSON())
					if err != nil {
						return window, err
					}
					rawBytes += len(raw)
				}
			}
			if len(window.Batches) > bounds.MaxBatches || rawBytes > bounds.MaxBytes {
				window.Overflow = true
			}
			if f.windowHook != nil {
				f.windowHook(&window)
			}
			return window, ctx.Err()
		}})
}
