package managed_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

func TestGenerationAcquisitionPanicStillCleansSuccessfulResources(t *testing.T) {
	h := newHarness(t)
	plan := h.compile(h.definition("1", false))
	adapters := h.adapters(plan, "panic")
	adapter := adapters["write"]
	adapter.Acquire = func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
		panic("native acquisition failed")
	}
	adapters["write"] = adapter
	_, err := m.PrepareGeneration(context.Background(), plan, m.PreparationContext{GraphID: "graph", Revision: "1", GenerationUUID: "01234567-89ab-cdef-0123-456789abcdef"}, adapters)
	if err == nil || !strings.Contains(err.Error(), "native acquisition failed") || len(h.events) != 2 || h.events[1] != "dispose:panic:tool" {
		t.Fatal("acquisition panic leaked successful resource", err, h.events)
	}
}
func TestServingCleanupFailureObservableAndBlocksNewPreparations(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	h.authority.set(h.release("2", false))
	readyChannel := make(chan *m.ReadyGeneration, 1)
	other, err := m.NewLocalServingCoordinator(m.ServingOptions{
		GraphID: "graph", TargetID: "target", InstanceID: "failure", Authority: h.authority,
		Provider: m.ServingPreparationFunc(func(ctx context.Context, attempt m.ServingAttempt) (*m.ReadyGeneration, error) {
			definition := attempt.Release.Snapshot.Definition()
			plan := h.compile(&definition)
			adapters := h.adapters(plan, "failure")
			entry := adapters["write"]
			entry.Acquire = func(context.Context, m.ResolvedBinding, string, m.PreparationContext) (m.PreparedResource, error) {
				return m.PreparedResource{Dispose: func(context.Context) error { return errors.New("owned cleanup failed") }}, nil
			}
			adapters["write"] = entry
			ready, err := m.PrepareGeneration(ctx, plan, m.PreparationContext{GraphID: "graph", Revision: "2", GenerationUUID: attempt.GenerationUUID}, adapters)
			if err == nil {
				readyChannel <- ready
			}
			return ready, err
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.authority.mu.Lock()
	h.authority.allowed = false
	h.authority.mu.Unlock()
	result := other.Prepare(context.Background())
	if result.Status != "superseded" {
		t.Fatal(result)
	}
	candidate := await(t, readyChannel)
	if err := candidate.Wait(context.Background()); err == nil || candidate.ResourceState() != "cleanup_failed" {
		t.Fatal("cleanup failure was hidden", err)
	}
	if result := other.Prepare(context.Background()); !errors.Is(result.Error, m.ErrCapacity) {
		t.Fatal("cleanup failure did not block new preparations", result)
	}
	eventually(t, func() bool { return len(other.CleanupErrors()) == 1 })
	if err := other.Shutdown(context.Background()); err == nil {
		t.Fatal("shutdown swallowed cleanup failure")
	}
	current, ok := h.coordinator.Current()
	if !ok || current.Attempt.GenerationUUID != first.Attempt.GenerationUUID || current.Generation.ResourceState() != "prepared" {
		t.Fatal("unrelated failed candidate harmed LKG")
	}
}
func TestServingFencePanicClosesCapabilityWithoutLosingLKG(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	h.authority.set(h.release("2", false))
	var saved func() error
	h.authority.mu.Lock()
	h.authority.fence = func(_ m.ServingAttempt, _ *m.ReadyGeneration, adopt func() error) (bool, error) {
		saved = adopt
		panic("owner fence failed")
	}
	h.authority.mu.Unlock()
	result := h.coordinator.Prepare(context.Background())
	if result.Status != "failed" || result.Error == nil || saved() == nil {
		t.Fatal("fence panic/capability was not contained", result)
	}
	current, _ := h.coordinator.Current()
	if current.Attempt.GenerationUUID != first.Attempt.GenerationUUID {
		t.Fatal("fence panic lost LKG")
	}
}
func TestServingSaturationRefusesRatherThanQueueing(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	h.providerHook = func(context.Context, m.ServingAttempt) error { entered <- struct{}{}; <-release; return nil }
	results := make(chan m.LocalServingResult, 2)
	for range 2 {
		go func() { results <- h.coordinator.Prepare(context.Background()) }()
	}
	await(t, entered)
	await(t, entered)
	if got := h.coordinator.Counts(); got.Preparing != 2 {
		t.Fatal(got)
	}
	if result := h.coordinator.Prepare(context.Background()); !errors.Is(result.Error, m.ErrCapacity) {
		t.Fatal("unbounded serving preparation queue", result)
	}
	close(release)
	for range 2 {
		if result := await(t, results); result.Status != "serving" {
			t.Fatal(result)
		}
	}
}
