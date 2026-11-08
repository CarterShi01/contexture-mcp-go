package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
	s "github.com/CarterShi01/contexture-mcp-go/server"
)

// This fresh consumer's explicit TEST Host is not production authority.
type testHost struct{ release m.CapturedRelease }

func (h *testHost) Enter(ctx context.Context, _ s.ManagedHostInput, dispatch func(context.Context, s.ManagedHostScope) error) error {
	ctx, err := m.WithIdentity(ctx, m.Identity{ActorID: "consumer", TenantID: "tenant", AuthorityRevision: "1"})
	if err != nil {
		return err
	}
	return dispatch(ctx, s.ManagedHostScope{})
}
func (h *testHost) Fence(ctx context.Context, in s.ManagedHostInput, dispatch func(context.Context, s.ManagedHostScope) error) error {
	return h.Enter(ctx, in, dispatch)
}
func (h *testHost) Verify(ctx context.Context, r m.RequestAuthorization) ([]string, error) {
	id, err := m.IdentityFromContext(ctx)
	if err != nil || id.ActorID != "consumer" || id.TenantID != "tenant" || id != r.Identity {
		return nil, m.ErrDenied
	}
	return r.RequestedIDs, nil
}
func (h *testHost) Capture() (*m.CapturedRelease, error) { r := h.release; return &r, nil }

type testRelease struct{ h *testHost }

func (p testRelease) Capture() (*m.CapturedRelease, error) { return p.h.Capture() }
func (p testRelease) Fence(a m.ServingAttempt, r *m.ReadyGeneration, dispatch func() error) (bool, error) {
	if a.Release.ManifestDigest != p.h.release.ManifestDigest || r.Plan().Manifest().Digest() != p.h.release.ManifestDigest {
		return false, nil
	}
	return true, dispatch()
}

type input struct{ X string }

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	registration, err := m.NewBindingRegistration(m.BindingRegistrationOptions[input]{BindingID: "native", Version: "1", Kind: "registered", Effect: "write", ABI: m.NativeABI,
		ArtifactDigest: "sha256:" + strings.Repeat("a", 64), Portability: m.Portability{Kind: "native", Language: "go", Version: "1.25", Factory: "consumer"},
		InputSchema:       map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []any{"x"}, "additionalProperties": false},
		DecoderContractID: "typed-input", ValidatorContractID: "exact-x", DefaultContractID: "none", CoercionContractID: "none",
		Decoder: func(value m.FrozenJSON) (input, error) {
			return input{X: value.JSON().(map[string]any)["x"].(string)}, nil
		},
		Validator: func(value input) error {
			if value.X != "typed" {
				return fmt.Errorf("unexpected x")
			}
			return nil
		},
		Handler: func(_ context.Context, value input, call m.CallContext) (any, error) {
			if call.Identity.ActorID != "consumer" || call.Resource != "owned-native-resource" {
				return nil, m.ErrDenied
			}
			return map[string]any{"x": value.X, "resource": call.Resource}, nil
		}})
	must(err)
	registry, err := m.NewRegistryGeneration("consumer", registration)
	must(err)
	resolved, err := registry.Resolve(registration.Descriptor(), nil)
	must(err)
	var contract m.PublicationContract = resolved.PublicationContract()
	definition, err := m.NewGraphDefinition(map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "consumer-graph", "application_name": "consumer", "mode": "runtime",
		"nodes":         []any{map[string]any{"node_id": "tool", "kind": "tool", "name": "tool", "description": "native", "binding": registration.Descriptor().AsJSON()}},
		"roots":         []any{map[string]any{"node_id": "tool", "plane": "model"}},
		"publications":  []any{map[string]any{"kind": "route", "publication_id": "route", "method": "POST", "path": "/exact", "opens_id": "tool"}},
		"presentations": map[string]any{"default_locale": "en", "catalogs": []any{}}}, map[string]m.PublicationContract{"tool": contract})
	must(err)
	plan, err := m.Compile(definition, m.CompilerOptions{Registry: registry, Locale: "en", CompilerContractVersion: "1", CompilerBuild: "consumer", DependencyContractID: "consumer", DependencyBuild: "1"})
	must(err)
	state, err := m.NewRevisionState(definition, "1", nil)
	must(err)
	host := &testHost{release: m.CapturedRelease{GraphID: "consumer-graph", TargetID: "target", ReleaseEpoch: "1", JournalSeq: "1", Snapshot: state, ManifestDigest: plan.Manifest().Digest()}}
	coordinator, err := m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "consumer-graph", TargetID: "target", InstanceID: "consumer", Authority: testRelease{host},
		Provider: m.ServingPreparationFunc(func(ctx context.Context, a m.ServingAttempt) (*m.ReadyGeneration, error) {
			return m.PrepareGeneration(ctx, plan, m.PreparationContext{GraphID: "consumer-graph", Revision: "1", GenerationUUID: a.GenerationUUID},
				map[string]m.PreparationAdapterRegistration{"tool": {BindingID: "native", Version: "1", ABI: m.NativeABI, ContractDigest: registration.ContractDigest(),
					Acquire: func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
						return m.PreparedResource{Handle: "owned-native-resource", Dispose: func(context.Context) error { return nil }}, nil
					}}})
		})})
	must(err)
	runtime, err := m.NewManagedRuntime(coordinator, host, nil)
	must(err)
	must(coordinator.Prepare(context.Background()).Error)
	var trusted s.ManagedTransportHost = host
	adapter, err := s.NewManagedHostAdapter(s.ManagedHostOptions{Identity: s.Identity{Name: "consumer", Version: "1"}, Runtime: runtime, Host: trusted,
		Navigation: []s.ManagedNavigationMount{{Path: "/host/routes", Operation: "routes"}}})
	must(err)
	url, err := adapter.Listen(0)
	must(err)
	current, ok := coordinator.Current()
	if !ok {
		panic("no generation")
	}
	ref, err := m.FormatReference("consumer-graph", current.Attempt.GenerationUUID, "tool")
	must(err)
	request, err := http.NewRequest("POST", url+"/exact", bytes.NewBufferString(`{"x":"typed"}`))
	must(err)
	request.Header.Set(s.ManagedProfileHeader, s.ManagedHostProfile)
	request.Header.Set(s.ManagedReferenceHeader, ref)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	must(err)
	raw, err := io.ReadAll(response.Body)
	must(err)
	must(response.Body.Close())
	var envelope map[string]any
	must(json.Unmarshal(raw, &envelope))
	if response.StatusCode != 200 || envelope["generation_uuid"] != current.Attempt.GenerationUUID || envelope["boot_id"] != coordinator.BootID() || envelope["value"].(map[string]any)["resource"] != "owned-native-resource" {
		panic(string(raw))
	}
	runs, err := m.NewRunLeaseService(runtime, m.RunLeaseOptions{})
	must(err)
	if !runs.IsFor(runtime) {
		panic("custody")
	}
	var run *m.RunLeaseContext
	must(host.Enter(context.Background(), s.ManagedHostInput{}, func(ctx context.Context, scope s.ManagedHostScope) error {
		var err error
		run, err = runs.Create(ctx, time.Second, scope)
		if err != nil {
			return err
		}
		return runs.CheckCurrent(ctx, run)
	}))
	must(adapter.Close())
	must(runs.Shutdown(context.Background()))
	must(coordinator.Shutdown(context.Background()))
	fmt.Println("fresh typed native Host consumer passed")
}
