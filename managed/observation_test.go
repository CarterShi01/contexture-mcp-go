package managed_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

type observationFunc func(context.Context, m.ServingAttempt) error

func (f observationFunc) Observe(ctx context.Context, attempt m.ServingAttempt) error {
	return f(ctx, attempt)
}

func TestOptionalObservationFailurePreservesPointerAndBoundedRetry(t *testing.T) {
	h := newHarness(t)
	var failures atomic.Bool
	failures.Store(true)
	c, err := m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "observed",
		Authority: h.authority, Provider: m.ServingPreparationFunc(h.prepare),
		Observation: observationFunc(func(context.Context, m.ServingAttempt) error {
			if failures.Load() {
				return errors.New("observer unavailable")
			}
			return nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Shutdown(context.Background()) }()
	result := c.Prepare(context.Background())
	if result.Status != "serving_unobserved" || result.Error == nil {
		t.Fatal(result)
	}
	active, ok := c.Current()
	if !ok || active.Generation.ResourceState() != "prepared" {
		t.Fatal("observer failure retired actual pointer")
	}
	if next := c.Prepare(context.Background()); !errors.Is(next.Error, m.ErrCapacity) {
		t.Fatal("unbounded replacement while observation pending", next)
	}
	lease, err := c.Lease()
	if err != nil {
		t.Fatal("unobserved serving value cannot lease", err)
	}
	lease.Resources.Close()
	failures.Store(false)
	retry := c.RetryObservation(context.Background())
	if retry.Status != "local_observed" || retry.Error != nil || retry.Attempt.GenerationUUID != result.Attempt.GenerationUUID {
		t.Fatal(retry)
	}
}
func TestOptionalObservationRunsOutsidePointerGateAndShutdownJoins(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var c *m.LocalServingCoordinator
	var err error
	c, err = m.NewLocalServingCoordinator(m.ServingOptions{GraphID: "graph", TargetID: "target", InstanceID: "observed", Authority: h.authority,
		Provider: m.ServingPreparationFunc(h.prepare), Observation: observationFunc(func(context.Context, m.ServingAttempt) error {
			lease, err := c.Lease()
			if err != nil {
				return err
			}
			lease.Resources.Close()
			close(entered)
			<-release
			return nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan m.LocalServingResult, 1)
	go func() { done <- c.Prepare(context.Background()) }()
	await(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown disowned observer", err)
	}
	close(release)
	if result := await(t, done); result.Status != "local_observed" {
		t.Fatal(result)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if retry := c.RetryObservation(context.Background()); retry.Status != "failed" {
		t.Fatal("closed coordinator retried observation", retry)
	}
}
