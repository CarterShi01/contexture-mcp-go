package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
	s "github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This fixture owns real native bindings/resources/adoption, not credentials or
// FileJournal durability. Existing durable-owner tests qualify that other slice.
type nativeHostFixture struct {
	t                                   *testing.T
	mu, gate                            sync.Mutex
	identity                            m.Identity
	scope                               s.ManagedHostScope
	denied                              map[string]bool
	release                             m.CapturedRelease
	registry                            *m.RegistryGeneration
	registrations                       map[string]*m.BindingRegistration
	runtime                             *m.ManagedRuntime
	coordinator                         *m.LocalServingCoordinator
	adapter                             *s.ManagedHostAdapter
	url                                 string
	handler                             func(context.Context, map[string]any, m.CallContext) (any, error)
	decodes, validates, calls, disposes atomic.Int32
}

func (f *nativeHostFixture) Current(ctx context.Context) (m.Identity, error) {
	anchor, err := m.IdentityFromContext(ctx)
	if err != nil {
		return m.Identity{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if anchor.ActorID != f.identity.ActorID || anchor.TenantID != f.identity.TenantID {
		return m.Identity{}, m.ErrDenied
	}
	return f.identity, nil
}
func (f *nativeHostFixture) Enter(ctx context.Context, _ s.ManagedHostInput, call func(context.Context, s.ManagedHostScope) error) error {
	f.mu.Lock()
	identity, scope := f.identity, f.scope
	f.mu.Unlock()
	ctx, err := m.WithIdentity(ctx, identity)
	if err != nil {
		return err
	}
	return call(ctx, scope)
}
func (f *nativeHostFixture) Fence(ctx context.Context, _ s.ManagedHostInput, call func(context.Context, s.ManagedHostScope) error) error {
	f.gate.Lock()
	defer f.gate.Unlock()
	f.mu.Lock()
	identity, scope := f.identity, f.scope
	f.mu.Unlock()
	ctx, err := m.WithIdentity(ctx, identity)
	if err != nil {
		return err
	}
	return call(ctx, scope)
}
func (f *nativeHostFixture) Verify(ctx context.Context, request m.RequestAuthorization) ([]string, error) {
	current, err := f.Current(ctx)
	if err != nil || current != request.Identity {
		return nil, m.ErrDenied
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	allowed := []string{}
	for _, id := range request.RequestedIDs {
		if !f.denied[id] {
			allowed = append(allowed, id)
		}
	}
	return allowed, nil
}
func (f *nativeHostFixture) Capture() (*m.CapturedRelease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy := f.release
	return &copy, nil
}
func (f *nativeHostFixture) FenceRelease(a m.ServingAttempt, ready *m.ReadyGeneration, adopt func() error) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.Release.ReleaseEpoch != f.release.ReleaseEpoch || a.Release.ManifestDigest != ready.Plan().Manifest().Digest() {
		return false, nil
	}
	return true, adopt()
}

type nativeReleasePort struct{ f *nativeHostFixture }

func (p nativeReleasePort) Capture() (*m.CapturedRelease, error) { return p.f.Capture() }
func (p nativeReleasePort) Fence(a m.ServingAttempt, r *m.ReadyGeneration, adopt func() error) (bool, error) {
	return p.f.FenceRelease(a, r, adopt)
}

func (f *nativeHostFixture) compile(definition *m.GraphDefinition) *m.StructuralPlan {
	f.t.Helper()
	plan, err := m.Compile(definition, m.CompilerOptions{Registry: f.registry, Locale: "en",
		CompilerContractVersion: "1", CompilerBuild: "native-host-test", DependencyContractID: "fixture", DependencyBuild: "1"})
	if err != nil {
		f.t.Fatal(err)
	}
	return plan
}
func (f *nativeHostFixture) setRelease(epoch string) {
	f.t.Helper()
	nodes := []any{}
	contracts := map[string]m.PublicationContract{}
	role := func(id string) map[string]any {
		return map[string]any{"node_id": id, "name": id, "kind": "role", "description": id, "instructions": "actual native " + epoch}
	}
	root, prompt, pre, post := role("root"), role("prompt"), role("pre"), role("post")
	pre["role_variant"], post["role_variant"] = "pre_process", "post_process"
	root["members"] = map[string]any{"pre_process": "pre", "post_process": "post", "tools": []any{"read", "write", "resource"}}
	nodes = append(nodes, root, prompt, pre, post)
	for _, id := range []string{"read", "write", "resource"} {
		reg, ok := f.registrations[id]
		if !ok {
			f.t.Fatal("missing fixture binding")
		}
		binding, err := f.registry.Resolve(reg.Descriptor(), nil)
		if err != nil {
			f.t.Fatal(err)
		}
		contracts[id] = binding.PublicationContract()
		nodes = append(nodes, map[string]any{"node_id": id, "kind": "tool", "name": id, "description": id, "binding": reg.Descriptor().AsJSON()})
	}
	definition, err := m.NewGraphDefinition(map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "graph",
		"application_name": "native", "mode": "runtime", "nodes": nodes,
		"roots": []any{map[string]any{"node_id": "root", "plane": "model"}, map[string]any{"node_id": "prompt", "plane": "prompt"}},
		"publications": []any{
			map[string]any{"kind": "prompt", "publication_id": "prompt-pub", "name": "declared-prompt", "description": "prompt description", "opens_id": "prompt", "model_may_open": false},
			map[string]any{"kind": "resource", "publication_id": "resource-pub", "name": "declared-resource", "uri": "fixture://exact/text", "mime_type": "text/plain", "opens_id": "resource"},
			map[string]any{"kind": "route", "publication_id": "get-pub", "method": "GET", "path": "/declared/text", "opens_id": "resource"},
			map[string]any{"kind": "route", "publication_id": "head-pub", "method": "HEAD", "path": "/declared/text", "opens_id": "resource"},
			map[string]any{"kind": "route", "publication_id": "write-pub", "method": "POST", "path": "/declared/write", "opens_id": "write"}},
		"presentations": map[string]any{"default_locale": "en", "catalogs": []any{}}}, contracts)
	if err != nil {
		f.t.Fatal(err)
	}
	state, err := m.NewRevisionState(definition, epoch, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	release := m.CapturedRelease{GraphID: "graph", TargetID: "target", ReleaseEpoch: epoch, JournalSeq: epoch,
		Snapshot: state, ManifestDigest: f.compile(definition).Manifest().Digest()}
	f.mu.Lock()
	f.release = release
	f.mu.Unlock()
}
func newNativeHostFixture(t *testing.T) *nativeHostFixture {
	t.Helper()
	f := &nativeHostFixture{t: t, identity: m.Identity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "authority"}, denied: map[string]bool{},
		registrations: map[string]*m.BindingRegistration{}}
	registrations := []*m.BindingRegistration{}
	for _, id := range []string{"read", "write", "resource"} {
		effect := "read_only"
		if id == "write" {
			effect = "write"
		}
		schema := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []any{"x"}, "additionalProperties": false}
		if id == "resource" {
			schema = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		}
		id := id
		reg, err := m.NewBindingRegistration(m.BindingRegistrationOptions[map[string]any]{BindingID: id, Version: "1", Kind: "registered", Effect: effect, ABI: m.NativeABI,
			ArgumentFree:   id == "resource",
			ArtifactDigest: "sha256:" + strings.Repeat("a", 64), Portability: m.Portability{Kind: "native", Language: "go", Version: "1.25", Factory: "fixture"},
			InputSchema: schema, DecoderContractID: "typed-map", ValidatorContractID: "validator", DefaultContractID: "none", CoercionContractID: "none",
			Decoder: func(raw m.FrozenJSON) (map[string]any, error) {
				f.decodes.Add(1)
				value, ok := raw.JSON().(map[string]any)
				if !ok {
					return nil, errors.New("typed object required")
				}
				return value, nil
			},
			Validator: func(value map[string]any) error {
				f.validates.Add(1)
				if value["x"] == "invalid" {
					return errors.New("invalid")
				}
				return nil
			},
			Handler: func(ctx context.Context, input map[string]any, call m.CallContext) (any, error) {
				f.calls.Add(1)
				if f.handler != nil {
					return f.handler(ctx, input, call)
				}
				if id == "resource" {
					return "native resource bytes\n", nil
				}
				return map[string]any{"x": input["x"], "actor": call.Identity.ActorID, "resource": call.Resource}, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		registrations = append(registrations, reg)
		f.registrations[id] = reg
	}
	var err error
	f.registry, err = m.NewRegistryGeneration("native-fixture", registrations...)
	if err != nil {
		t.Fatal(err)
	}
	f.setRelease("1")
	f.coordinator, err = m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "instance", Authority: nativeReleasePort{f},
		Provider: m.ServingPreparationFunc(func(ctx context.Context, a m.ServingAttempt) (*m.ReadyGeneration, error) {
			definition := a.Release.Snapshot.Definition()
			plan := f.compile(&definition)
			adapters := map[string]m.PreparationAdapterRegistration{}
			for _, id := range plan.Index().Preorder() {
				binding, ok := plan.Binding(id)
				if !ok {
					continue
				}
				reg := binding.Registration()
				adapters[id] = m.PreparationAdapterRegistration{BindingID: reg.Descriptor().BindingID(), Version: "1", ABI: m.NativeABI, ContractDigest: reg.ContractDigest(),
					Acquire: func(_ context.Context, _ m.ResolvedBinding, id string, _ m.PreparationContext) (m.PreparedResource, error) {
						return m.PreparedResource{Handle: a.GenerationUUID + ":" + id, Dispose: func(context.Context) error { f.disposes.Add(1); return nil }}, nil
					}}
			}
			return m.PrepareGeneration(ctx, plan, m.PreparationContext{GraphID: "graph", Revision: a.Release.Snapshot.Revision(), GenerationUUID: a.GenerationUUID}, adapters)
		})})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime, err = m.NewManagedRuntime(f.coordinator, f, f)
	if err != nil {
		t.Fatal(err)
	}
	result := f.coordinator.Prepare(context.Background())
	if result.Error != nil {
		t.Fatalf("prepare %v", result)
	}
	t.Cleanup(func() {
		if f.adapter != nil {
			if err := f.adapter.Close(); err != nil {
				t.Error(err)
			}
		}
		if err := f.coordinator.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *nativeHostFixture) build(options s.ManagedHostOptions) *s.ManagedHostAdapter {
	f.t.Helper()
	options.Identity = s.Identity{Name: "native", Version: "1"}
	options.Runtime = f.runtime
	options.Host = f
	adapter, err := s.NewManagedHostAdapter(options)
	if err != nil {
		f.t.Fatal(err)
	}
	f.adapter = adapter
	return adapter
}
func (f *nativeHostFixture) ref(id string) string {
	current, ok := f.coordinator.Current()
	if !ok {
		f.t.Fatal("no generation")
	}
	ref, err := m.FormatReference("graph", current.Attempt.GenerationUUID, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}
func hostMeta(ref string) mcp.Meta {
	meta := mcp.Meta{s.ManagedProfileMeta: s.ManagedHostProfile}
	if ref != "" {
		meta[s.ManagedReferenceMeta] = ref
	}
	return meta
}
func (f *nativeHostFixture) rest(method, path, ref string, body any, headers http.Header) *httptest.ResponseRecorder {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	if body == nil {
		raw = nil
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Host = "127.0.0.1"
	if f.url != "" {
		request.Host = strings.TrimPrefix(f.url, "http://")
	}
	request.Header.Set(s.ManagedProfileHeader, s.ManagedHostProfile)
	if ref != "" {
		request.Header.Set(s.ManagedReferenceHeader, ref)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		request.Header[key] = values
	}
	response := httptest.NewRecorder()
	f.adapter.Handler().ServeHTTP(response, request)
	return response
}
func callNative(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args, Meta: hostMeta("")})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type profileRoundTripper struct{ run string }

func (profile profileRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set(s.ManagedProfileHeader, s.ManagedHostProfile)
	if profile.run != "" {
		request.Header.Set(s.ManagedRunHeader, profile.run)
	}
	return http.DefaultTransport.RoundTrip(request)
}

func TestManagedHostNativeSDKLoopbackAndMountableREST(t *testing.T) {
	f := newNativeHostFixture(t)
	adapter := f.build(s.ManagedHostOptions{Navigation: []s.ManagedNavigationMount{{Path: "/host/routes", Operation: "routes"}, {Path: "/host/open", Operation: "open"}, {Path: "/host/members", Operation: "members"}}})
	url, err := adapter.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	f.url = url
	client := mcp.NewClient(&mcp.Implementation{Name: "real-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: profileRoundTripper{}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), &mcp.ListToolsParams{Meta: hostMeta("")})
	if err != nil || len(tools.Tools) != 5 {
		t.Fatalf("fixed tools %v %v", tools, err)
	}
	for _, name := range []string{"contexture_discover", "contexture_inspect", "contexture_open", "contexture_invoke_read_only", "contexture_invoke"} {
		args := map[string]any{}
		switch name {
		case "contexture_inspect":
			args["refs"] = []string{f.ref("root"), f.ref("read")}
		case "contexture_open":
			args["ref"] = f.ref("root")
		case "contexture_invoke_read_only":
			args["ref"], args["arguments"] = f.ref("read"), map[string]any{"x": "native"}
		case "contexture_invoke":
			args["ref"], args["arguments"] = f.ref("write"), map[string]any{"x": "native"}
		}
		result := callNative(t, session, name, args)
		if result.IsError {
			t.Fatalf("%s %v", name, result)
		}
		envelope, ok := result.StructuredContent.(map[string]any)
		if !ok || envelope["generation_uuid"] == "" || envelope["boot_id"] != f.coordinator.BootID() || envelope["revision"] != "1" || envelope["target_id"] != "target" || envelope["instance_id"] != "instance" {
			t.Fatalf("actual coordinates %v", envelope)
		}
	}
	prompts, err := session.ListPrompts(context.Background(), &mcp.ListPromptsParams{Meta: hostMeta("")})
	if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "declared-prompt" {
		t.Fatalf("prompts %v %v", prompts, err)
	}
	prompt, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "declared-prompt", Meta: hostMeta(f.ref("prompt"))})
	if err != nil || len(prompt.Messages) != 1 || !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "actual native 1") {
		t.Fatalf("native prompt %v %v", prompt, err)
	}
	resources, err := session.ListResources(context.Background(), &mcp.ListResourcesParams{Meta: hostMeta("")})
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != "fixture://exact/text" {
		t.Fatalf("resources %v %v", resources, err)
	}
	resource, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "fixture://exact/text", Meta: hostMeta(f.ref("resource"))})
	if err != nil || resource.Contents[0].Text != "native resource bytes\n" {
		t.Fatalf("native resource %v %v", resource, err)
	}
	if f.decodes.Load() != 3 || f.validates.Load() != 3 || f.calls.Load() != 3 {
		t.Fatalf("native binding observations %d/%d/%d", f.decodes.Load(), f.validates.Load(), f.calls.Load())
	}
	for _, method := range []string{"GET", "HEAD"} {
		response := f.rest(method, "/declared/text", f.ref("resource"), nil, nil)
		if response.Code != 200 || response.Header().Get("Contexture-Generation") == "" {
			t.Fatalf("exact route %s %s", method, response.Body.String())
		}
		if method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD bytes")
		}
	}
	response := f.rest("POST", "/declared/write", f.ref("write"), map[string]any{"x": "rest"}, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"x":"rest"`) {
		t.Fatalf("write %s", response.Body.String())
	}
	if f.rest("GET", "/host/routes", "", nil, nil).Code != 200 || f.rest("POST", "/host/open", "", map[string]any{"ref": f.ref("root")}, nil).Code != 200 {
		t.Fatal("native mounts")
	}
	if f.rest("POST", "/host/members", "", map[string]any{"ref": f.ref("root")}, nil).Code != 200 {
		t.Fatal("members")
	}
	for _, test := range []struct {
		method, path, ref string
		body              any
	}{
		{"GET", "/declared/text/", "", nil}, {"POST", "/declared/text", f.ref("resource"), map[string]any{}}, {"GET", "/declared/text", "", nil}, {"GET", "/declared/text?alias=1", f.ref("resource"), nil},
		{"POST", "/host/open", "", map[string]any{"ref": f.ref("root"), "actor": "forged"}}, {"GET", "/host/open", "", nil},
	} {
		if f.rest(test.method, test.path, test.ref, test.body, nil).Code == 200 {
			t.Fatalf("refused exact route %v", test)
		}
	}
	if _, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "alias", Meta: hostMeta(f.ref("prompt"))}); err == nil {
		t.Fatal("prompt alias")
	}
	if _, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "fixture://exact/text/", Meta: hostMeta(f.ref("resource"))}); err == nil {
		t.Fatal("URI alias")
	}
	if !callNative(t, session, "read", map[string]any{"x": "wrong"}).IsError {
		t.Fatal("business top-level tool")
	}
	for _, name := range []string{"invoke", "contexture_members", "discover"} {
		args := map[string]any{}
		if name == "invoke" {
			args["ref"], args["arguments"] = f.ref("read"), map[string]any{"x": "alias"}
		}
		if name == "contexture_members" {
			args["ref"] = f.ref("root")
		}
		if !callNative(t, session, name, args).IsError {
			t.Fatalf("undeclared gateway alias %s", name)
		}
	}
	if !callNative(t, session, "contexture_invoke_read_only", map[string]any{"ref": f.ref("write"), "arguments": map[string]any{"x": "wrong"}}).IsError {
		t.Fatal("actual binding effect")
	}
	if !callNative(t, session, "contexture_invoke", map[string]any{"ref": f.ref("read"), "arguments": map[string]any{"x": "invalid"}}).IsError {
		t.Fatal("native validator")
	}
	if !callNative(t, session, "contexture_invoke", map[string]any{"ref": f.ref("read"), "arguments": map[string]any{"x": 1}}).IsError {
		t.Fatal("native schema")
	}
}

func TestManagedHostNativeRevokeAfterBusinessAndDrain(t *testing.T) {
	f := newNativeHostFixture(t)
	adapter := f.build(s.ManagedHostOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	f.handler = func(_ context.Context, _ map[string]any, call m.CallContext) (any, error) {
		close(entered)
		<-release
		if call.Resource == nil {
			t.Error("missing entered resource")
		}
		return "business finished", nil
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.rest("POST", "/declared/write", f.ref("write"), map[string]any{"x": "block"}, nil) }()
	<-entered
	f.gate.Lock()
	f.mu.Lock()
	f.denied["write"] = true
	f.identity.AuthorityRevision = "revoked"
	f.mu.Unlock()
	f.gate.Unlock()
	close(release)
	if response := <-done; response.Code == 200 {
		t.Fatalf("revoked successful effect leaked %s", response.Body.String())
	}
	if f.calls.Load() != 1 {
		t.Fatal("effect observation")
	}
	// Separate non-cooperative child proves close cannot release resources early.
	entered, release = make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.denied["write"] = false
	f.mu.Unlock()
	go func() { done <- f.rest("POST", "/declared/write", f.ref("write"), map[string]any{"x": "block"}, nil) }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- adapter.Close() }()
	select {
	case <-closed:
		t.Fatal("close preceded non-cooperative settle")
	case <-time.After(30 * time.Millisecond):
	}
	if f.disposes.Load() != 0 {
		t.Fatal("resources closed during handler")
	}
	close(release)
	if (<-done).Code == 200 {
		t.Fatal("canceled success")
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code == 200 {
		t.Fatal("closed admission")
	}
}

func TestManagedHostNativeRetainedRunAndProtectedCarriers(t *testing.T) {
	f := newNativeHostFixture(t)
	now := time.Duration(0)
	var clockMu sync.Mutex
	runs, err := m.NewRunLeaseService(f.runtime, m.RunLeaseOptions{Clock: func() time.Duration { clockMu.Lock(); defer clockMu.Unlock(); return now }, MaxTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runs.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, err := m.WithIdentity(context.Background(), f.identity)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.Create(ctx, time.Second, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	old := f.ref("write")
	oldResource := f.ref("resource")
	approvals := atomic.Int32{}
	adapter := f.build(s.ManagedHostOptions{Runs: runs, Carrier: s.ManagedHeaderCarrier{Approve: func(_ context.Context, token string, _ s.ManagedHostInput) (*m.RunLeaseContext, error) {
		approvals.Add(1)
		if token != "approved_token_1234" {
			return nil, m.ErrDenied
		}
		return run, nil
	}}})
	f.setRelease("2")
	result := f.coordinator.Prepare(context.Background())
	if result.Error != nil {
		t.Fatalf("swap %v", result)
	}
	if f.rest("POST", "/declared/write", old, map[string]any{"x": "ordinary"}, nil).Code == 200 {
		t.Fatal("ordinary old ref")
	}
	headers := http.Header{s.ManagedRunHeader: []string{"approved_token_1234"}}
	response := f.rest("POST", "/declared/write", old, map[string]any{"x": "retained"}, headers)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"revision":"1"`) {
		t.Fatalf("retained actual generation %s", response.Body.String())
	}
	if f.rest("GET", "/declared/text", oldResource, nil, headers).Code != 200 || approvals.Load() != 2 {
		t.Fatal("approval per request/publication pin")
	}
	url, err := adapter.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	f.url = url
	client := mcp.NewClient(&mcp.Implementation{Name: "retained-run-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: profileRoundTripper{run: "approved_token_1234"}},
		DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	invoked := callNative(t, session, "contexture_invoke", map[string]any{"ref": old, "arguments": map[string]any{"x": "native-SDK-retained"}})
	if invoked.IsError || invoked.StructuredContent.(map[string]any)["revision"] != "1" || approvals.Load() != 3 {
		t.Fatalf("native SDK retained run %v approvals %d", invoked, approvals.Load())
	}
	for _, header := range []string{"bad", "unapproved_token_1"} {
		if f.rest("GET", "/declared/text", oldResource, nil, http.Header{s.ManagedRunHeader: []string{header}}).Code == 200 {
			t.Fatal("bad carrier fallback")
		}
	}
	f.mu.Lock()
	f.scope = m.ManagedRequestOptions{Selection: []string{}, PublicationSelection: []string{}}
	f.mu.Unlock()
	if f.rest("POST", "/declared/write", old, map[string]any{"x": "current-cut"}, headers).Code == 200 {
		t.Fatal("current scope retained run")
	}
	f.mu.Lock()
	f.scope = m.ManagedRequestOptions{}
	f.identity.ActorID = "other"
	f.mu.Unlock()
	if f.rest("POST", "/declared/write", old, map[string]any{"x": "wrong-actor"}, headers).Code == 200 {
		t.Fatal("wrong actor run")
	}
	f.mu.Lock()
	f.identity.ActorID = "actor"
	f.identity.TenantID = "other-tenant"
	f.mu.Unlock()
	if f.rest("POST", "/declared/write", old, map[string]any{"x": "wrong-tenant"}, headers).Code == 200 {
		t.Fatal("wrong tenant run")
	}
	f.mu.Lock()
	f.identity.TenantID = "tenant"
	f.mu.Unlock()
	if _, err := runs.Renew(ctx, run, time.Second); err != nil {
		t.Fatal(err)
	}
	clockMu.Lock()
	now = 2 * time.Second
	clockMu.Unlock()
	if f.rest("POST", "/declared/write", old, map[string]any{"x": "expired"}, headers).Code == 200 {
		t.Fatal("expired")
	}
	if _, err := runs.Renew(ctx, run, time.Second); err == nil {
		t.Fatal("expired renewal")
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	f.url = ""
	f.build(s.ManagedHostOptions{Runs: runs, Carrier: s.ManagedChannelCarrier{Current: func(context.Context, s.ManagedHostInput) (*m.RunLeaseContext, error) { return run, nil }}})
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, headers).Code == 200 {
		t.Fatal("channel/header exclusive")
	}
}

func TestManagedHostNativeStdioOSPipeSubprocess(t *testing.T) {
	if os.Getenv("CONTEXTURE_NATIVE_HOST_SUBPROCESS") == "1" {
		f := newNativeHostFixture(t)
		adapter := f.build(s.ManagedHostOptions{})
		if err := adapter.ServeStdio(context.Background()); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestManagedHostNativeStdioOSPipeSubprocess$")
	command.Env = append(os.Environ(), "CONTEXTURE_NATIVE_HOST_SUBPROCESS=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	write := func(id int, method string, params any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := stdin.Write(append(body, '\n')); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatalf("OS pipe response %v %s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["error"] != nil {
			t.Fatalf("RPC error %v", response)
		}
		return response["result"].(map[string]any)
	}
	write(1, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "os-pipe", "version": "1"}})
	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`+"\n")
	discover := write(2, "tools/call", map[string]any{"name": "contexture_discover", "arguments": map[string]any{}, "_meta": hostMeta("")})
	envelope, ok := discover["structuredContent"].(map[string]any)
	if !ok || discover["isError"] == true {
		t.Fatalf("native stdio discover %v", discover)
	}
	if envelope["boot_id"] == "" {
		t.Fatal("stdio boot")
	}
	generation := envelope["generation_uuid"].(string)
	ref, err := m.FormatReference("graph", generation, "read")
	if err != nil {
		t.Fatal(err)
	}
	invoke := write(3, "tools/call", map[string]any{"name": "contexture_invoke_read_only", "arguments": map[string]any{"ref": ref, "arguments": map[string]any{"x": "OS-pipe"}}, "_meta": hostMeta("")})
	if invoke["isError"] == true || !strings.Contains(string(mustHostJSON(t, invoke)), generation+":read") {
		t.Fatalf("actual OS-pipe resource %v", invoke)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
		if string(scanner.Bytes()) != "PASS" {
			t.Fatalf("stdio pollution %s", scanner.Text())
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("native EOF drain %v %s", err, stderr.String())
	}
}
func mustHostJSON(t *testing.T, value any) []byte {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func TestManagedHostNativeStrictNegotiationAndWrongCoordinates(t *testing.T) {
	f := newNativeHostFixture(t)
	f.build(s.ManagedHostOptions{Navigation: []s.ManagedNavigationMount{
		{Path: "/host/open", Operation: "open"}, {Path: "/host/resource", Operation: "resource.read"},
	}})
	for _, headers := range []http.Header{
		{s.ManagedProfileHeader: []string{""}}, {s.ManagedProfileHeader: []string{"unaccepted"}},
		{s.ManagedProfileHeader: []string{s.ManagedHostProfile, s.ManagedHostProfile}},
		{s.ManagedRunHeader: []string{"forged_token_1234"}}, {s.ManagedReferenceHeader: []string{f.ref("resource"), f.ref("resource")}},
		{"Origin": []string{"https://evil.example"}},
	} {
		if f.rest("GET", "/declared/text", f.ref("resource"), nil, headers).Code == 200 {
			t.Fatal("negotiation/header refusal")
		}

	}
	current, ok := f.coordinator.Current()
	if !ok {
		t.Fatal("current")
	}
	ref, err := m.FormatReference("other-graph", current.Attempt.GenerationUUID, "root")
	if err != nil {
		t.Fatal(err)
	}
	if f.rest("POST", "/host/open", "", map[string]any{"ref": ref}, nil).Code == 200 {
		t.Fatal("wrong graph")
	}
	ref, err = m.FormatReference("graph", current.Attempt.GenerationUUID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if f.rest("POST", "/host/open", "", map[string]any{"ref": ref}, nil).Code == 200 {
		t.Fatal("wrong target")
	}
	f.handler = func(context.Context, map[string]any, m.CallContext) (any, error) {
		return map[string]any{"not": "text"}, nil
	}
	if f.rest("POST", "/host/resource", "", map[string]any{"uri": "fixture://exact/text", "ref": f.ref("resource")}, nil).Code == 200 {
		t.Fatal("nontext resource")
	}
}

func TestManagedHostNativeModelAndPublicationScopeAreIndependent(t *testing.T) {
	f := newNativeHostFixture(t)
	f.build(s.ManagedHostOptions{Navigation: []s.ManagedNavigationMount{{Path: "/host/invoke", Operation: "invoke"}}})
	f.mu.Lock()
	f.scope = m.ManagedRequestOptions{Selection: []string{}, PublicationSelection: []string{"resource"}}
	f.mu.Unlock()
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code != 200 {
		t.Fatal("explicit publication scope")
	}
	if f.rest("POST", "/host/invoke", "", map[string]any{"ref": f.ref("resource")}, nil).Code == 200 {
		t.Fatal("publication restored model scope")
	}
	f.mu.Lock()
	f.scope = m.ManagedRequestOptions{PublicationSelection: []string{}}
	f.mu.Unlock()
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code == 200 {
		t.Fatal("model restored publication scope")
	}
	if f.rest("POST", "/host/invoke", "", map[string]any{"ref": f.ref("resource")}, nil).Code != 200 {
		t.Fatal("model scope remains independent")
	}
}

func TestManagedHostNativeRunExpiryAfterWorkAndWrongBootService(t *testing.T) {
	f := newNativeHostFixture(t)
	var clockMu sync.Mutex
	now := time.Duration(0)
	runs, err := m.NewRunLeaseService(f.runtime, m.RunLeaseOptions{Clock: func() time.Duration { clockMu.Lock(); defer clockMu.Unlock(); return now }, MaxTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runs.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, err := m.WithIdentity(context.Background(), f.identity)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.Create(ctx, time.Second, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.build(s.ManagedHostOptions{Runs: runs, Carrier: s.ManagedChannelCarrier{Current: func(context.Context, s.ManagedHostInput) (*m.RunLeaseContext, error) { return run, nil }}})
	oldRef := f.ref("write")
	old, ok := f.coordinator.Current()
	if !ok {
		t.Fatal("current")
	}
	f.setRelease("2")
	if result := f.coordinator.Prepare(context.Background()); result.Error != nil {
		t.Fatal(result.Error)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.handler = func(context.Context, map[string]any, m.CallContext) (any, error) {
		close(entered)
		<-release
		return "finished", nil
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.rest("POST", "/declared/write", oldRef, map[string]any{"x": "expired-after-work"}, nil)
	}()
	<-entered
	clockMu.Lock()
	now = 2 * time.Second
	clockMu.Unlock()
	if count, err := runs.Sweep(); err != nil || count != 1 {
		t.Fatalf("sweep %d %v", count, err)
	}
	if f.disposes.Load() != 0 {
		t.Fatal("expired keeper closed active resources")
	}
	close(release)
	if (<-done).Code == 200 {
		t.Fatal("expired successful effect leaked")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.Generation.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	if f.disposes.Load() != 3 {
		t.Fatalf("real old generation drain %d", f.disposes.Load())
	}
	other := newNativeHostFixture(t)
	foreignService, err := m.NewRunLeaseService(other.runtime, m.RunLeaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := foreignService.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	foreignCtx, err := m.WithIdentity(context.Background(), other.identity)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := foreignService.Create(foreignCtx, time.Second, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if other.coordinator.BootID() == f.coordinator.BootID() {
		t.Fatal("boot identities")
	}
	if err := f.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	f.build(s.ManagedHostOptions{Runs: runs, Carrier: s.ManagedChannelCarrier{Current: func(context.Context, s.ManagedHostInput) (*m.RunLeaseContext, error) { return foreign, nil }}})
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code == 200 {
		t.Fatal("wrong boot/service")
	}
	if _, err := s.NewManagedHostAdapter(s.ManagedHostOptions{Identity: s.Identity{Name: "wrong", Version: "1"}, Runtime: f.runtime, Host: f, Runs: foreignService,
		Carrier: s.ManagedChannelCarrier{Current: func(context.Context, s.ManagedHostInput) (*m.RunLeaseContext, error) { return foreign, nil }}}); err == nil {
		t.Fatal("wrong runtime custody")
	}
}

func TestManagedHostNativeResponseBoundsAndMountConflicts(t *testing.T) {
	f := newNativeHostFixture(t)
	f.build(s.ManagedHostOptions{MaxBodyBytes: 16, Navigation: []s.ManagedNavigationMount{{Path: "/host/open", Operation: "open"}}})
	if f.rest("POST", "/host/open", "", map[string]any{"ref": f.ref("root")}, nil).Code != http.StatusRequestEntityTooLarge {
		t.Fatal("bounded body")
	}
	if err := f.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	f.build(s.ManagedHostOptions{MaxResponseBytes: 16})
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code == 200 {
		t.Fatal("atomic result bound")
	}
	if err := f.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	f.build(s.ManagedHostOptions{Navigation: []s.ManagedNavigationMount{{Path: "/declared/text", Operation: "routes"}}})
	if f.rest("GET", "/declared/text", f.ref("resource"), nil, nil).Code == 200 {
		t.Fatal("mount shadowed publication")
	}
}

type nativeTrackedWriter struct {
	*os.File
	once   sync.Once
	closed chan struct{}
}

func (writer *nativeTrackedWriter) Close() error {
	writer.once.Do(func() { close(writer.closed) })
	return writer.File.Close()
}

func TestManagedHostNativeOfficialStdioCancellationDrainsBeforePipeClose(t *testing.T) {
	f := newNativeHostFixture(t)
	adapter := f.build(s.ManagedHostOptions{})
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inputRead.Close(); _ = inputWrite.Close(); _ = outputRead.Close(); _ = outputWrite.Close() })
	writer := &nativeTrackedWriter{File: outputWrite, closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = adapter.ConnectStdio(ctx, &mcp.IOTransport{Reader: inputRead, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "native-pipe-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.IOTransport{Reader: outputRead, Writer: inputWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	f.handler = func(context.Context, map[string]any, m.CallContext) (any, error) {
		close(entered)
		<-release
		return "finished", nil
	}
	done := make(chan error, 1)
	ref := f.ref("write")
	go func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "contexture_invoke", Arguments: map[string]any{"ref": ref, "arguments": map[string]any{"x": "cancel"}}, Meta: hostMeta("")})
		if err == nil && !result.IsError {
			err = errors.New("canceled native pipe returned success")
		}
		done <- err
	}()
	<-entered
	cancel()
	select {
	case <-writer.closed:
		t.Fatal("official SDK pipe closed before native work settled")
	case <-time.After(30 * time.Millisecond):
	}
	if f.disposes.Load() != 0 {
		t.Fatal("native resources closed before settle")
	}
	close(release)
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	<-writer.closed
	if err := <-done; err != nil && err.Error() == "canceled native pipe returned success" {
		t.Fatal(err)
	}
}

var _ s.ManagedTransportHost = (*nativeHostFixture)(nil)
var _ m.RequestAuthority = (*nativeHostFixture)(nil)
var _ m.RequestIdentity = (*nativeHostFixture)(nil)
