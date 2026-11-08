package managed_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

func runService(t *testing.T, h *harness, options m.RunLeaseOptions) *m.RunLeaseService {
	t.Helper()
	service, err := m.NewRunLeaseService(h.runtime, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return service
}
func TestRunLeaseExplicitIdentityBootAndNonserialization(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	service := runService(t, h, m.RunLeaseOptions{})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{Selection: []string{"tool"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(envelope); err == nil {
		t.Fatal("run envelope serialized")
	}
	if _, err := json.Marshal(*envelope); err == nil {
		t.Fatal("copied run envelope serialized")
	}
	if err := service.Request(identity(t, "bob"), envelope, func(*m.ManagedRequest) error { return nil }); !errors.Is(err, m.ErrDenied) {
		t.Fatal("run crossed actor boundary", err)
	}
	other := runService(t, h, m.RunLeaseOptions{})
	if err := other.Request(ctx, envelope, func(*m.ManagedRequest) error { return nil }); !errors.Is(err, m.ErrDenied) {
		t.Fatal("run crossed service boundary", err)
	}
	if err := service.Request(ctx, &m.RunLeaseContext{}, func(*m.ManagedRequest) error { return nil }); !errors.Is(err, m.ErrDenied) {
		t.Fatal("forged empty run accepted", err)
	}
	if err := service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
		if _, err := request.Reference("write"); err == nil {
			return errors.New("run's subtree ceiling expanded")
		}
		ref, _ := request.Reference("tool")
		_, err := request.Invoke(ref, map[string]any{"x": "word"}, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	if err := service.Request(ctx, envelope, func(*m.ManagedRequest) error { return nil }); !errors.Is(err, m.ErrDenied) {
		t.Fatal("released run remained available", err)
	}
}
func TestRunLeaseRenewAttenuatesAndUpgradeCannotExpand(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	var all atomic.Bool
	all.Store(true)
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if all.Load() {
			return request.RequestedIDs, nil
		}
		return []string{"root", "tool"}, nil
	}
	var now atomic.Int64
	service := runService(t, h, m.RunLeaseOptions{Clock: func() time.Duration { return time.Duration(now.Load()) }})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	all.Store(false)
	if _, err := service.Renew(ctx, envelope, time.Minute); err != nil {
		t.Fatal(err)
	}
	all.Store(true)
	h.authority.set(h.release("2", true))
	second := mustServe(t, h)
	if err := service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
		if request.GenerationUUID() != first.Attempt.GenerationUUID {
			return errors.New("run silently upgraded generations")
		}
		if _, err := request.Reference("write"); err == nil {
			return errors.New("renewal reexpanded revoked authority")
		}
		ref, _ := request.Reference("tool")
		_, err := request.Invoke(ref, map[string]any{"x": "word"}, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upgrade(ctx, envelope, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
		if request.GenerationUUID() != second.Attempt.GenerationUUID {
			return errors.New("explicit upgrade did not repin")
		}
		for _, id := range []string{"write", "added"} {
			if _, err := request.Reference(id); err == nil {
				return errors.New("upgrade expanded original/attenuated ceiling")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Release(ctx, envelope); err != nil {
		t.Fatal(err)
	}
}
func TestRunLeaseExpiryNeverClosesEnteredOldGeneration(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.handler = func(_ context.Context, input map[string]any, _ m.CallContext) (any, error) {
		close(entered)
		<-release
		return input, nil
	}
	var now atomic.Int64
	service := runService(t, h, m.RunLeaseOptions{Clock: func() time.Duration { return time.Duration(now.Load()) }})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Second, m.ManagedRequestOptions{Selection: []string{"tool"}})
	if err != nil {
		t.Fatal(err)
	}
	child := make(chan error, 1)
	go func() {
		child <- service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
			ref, _ := request.Reference("tool")
			_, err := request.Invoke(ref, map[string]any{"x": "word"}, true)
			return err
		})
	}()
	await(t, entered)
	h.authority.set(h.release("2", false))
	mustServe(t, h)
	h.mu.Lock()
	old := h.generations[first.Attempt.GenerationUUID]
	h.mu.Unlock()
	now.Store(int64(time.Second))
	if count, err := service.Sweep(); err != nil || count != 1 || service.Counts().Runs != 0 {
		t.Fatal(count, err)
	}
	if old.ResourceState() != "draining" {
		t.Fatal("expiry closed still-entered old child", old.ResourceState())
	}
	if err := service.Request(ctx, envelope, func(*m.ManagedRequest) error { return nil }); err == nil {
		t.Fatal("expired run admitted future child")
	}
	close(release)
	if err := await(t, child); err != nil {
		t.Fatal(err)
	}
	if err := old.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRunLeaseUpgradeDoesNotMoveEnteredChild(t *testing.T) {
	h := newHarness(t)
	first := mustServe(t, h)
	service := runService(t, h, m.RunLeaseOptions{})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	child := make(chan error, 1)
	go func() {
		child <- service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
			close(entered)
			<-release
			if request.GenerationUUID() != first.Attempt.GenerationUUID {
				return errors.New("entered child moved generation")
			}
			ref, _ := request.Reference("tool")
			_, err := request.Invoke(ref, map[string]any{"x": "word"}, true)
			return err
		})
	}()
	await(t, entered)
	h.authority.set(h.release("2", false))
	mustServe(t, h)
	if _, err := service.Upgrade(ctx, envelope, time.Minute); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := await(t, child); err != nil {
		t.Fatal(err)
	}
}
func TestRunLeasePendingCreatesConsumeCapacityAndShutdownJoins(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if request.Operation == "run.create" {
			close(entered)
			<-release
		}
		return request.RequestedIDs, nil
	}
	service := runService(t, h, m.RunLeaseOptions{MaxRuns: 1})
	ctx := identity(t, "alice")
	done := make(chan error, 1)
	go func() {
		_, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{})
		done <- err
	}()
	await(t, entered)
	if got := service.Counts(); got.Pending != 1 || got.Runs != 0 {
		t.Fatal(got)
	}
	if _, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{}); !errors.Is(err, m.ErrCapacity) {
		t.Fatal("pending creation did not reserve capacity", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown fabricated pending-create completion", err)
	}
	close(release)
	if err := await(t, done); !errors.Is(err, m.ErrClosed) {
		t.Fatal("late creation adopted after shutdown", err)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRunLeaseRenewCannotResurrectExpiryAcrossAuthorization(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	h.policy = func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if request.Operation == "run.renew" {
			close(entered)
			<-release
		}
		return request.RequestedIDs, nil
	}
	var now atomic.Int64
	service := runService(t, h, m.RunLeaseOptions{Clock: func() time.Duration { return time.Duration(now.Load()) }})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Second, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.Renew(ctx, envelope, time.Minute); done <- err }()
	await(t, entered)
	now.Store(int64(time.Second))
	if count, err := service.Sweep(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	close(release)
	if err := await(t, done); !errors.Is(err, m.ErrDenied) || service.Counts().Runs != 0 {
		t.Fatal("renew resurrected expired run", err)
	}
}
func TestRunLeaseTTLClockAndAutomaticExpiry(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	if _, err := m.NewRunLeaseService(h.runtime, m.RunLeaseOptions{MaxTTL: 2147483648 * time.Millisecond}); err == nil {
		t.Fatal("unbounded max TTL accepted")
	}
	var now atomic.Int64
	service := runService(t, h, m.RunLeaseOptions{MaxTTL: time.Second, Clock: func() time.Duration { return time.Duration(now.Load()) }})
	if !service.RequiresExplicitSweep() {
		t.Fatal("custom clock fabricated automatic scheduler")
	}
	for _, ttl := range []time.Duration{0, -1, 2 * time.Second} {
		if _, err := service.Create(identity(t, "alice"), ttl, m.ManagedRequestOptions{}); err == nil {
			t.Fatal("invalid TTL accepted", ttl)
		}
	}
	now.Store(2)
	if _, err := service.Sweep(); err != nil {
		t.Fatal(err)
	}
	now.Store(1)
	if _, err := service.Sweep(); err == nil {
		t.Fatal("regressing monotonic clock accepted")
	}
	automatic := runService(t, h, m.RunLeaseOptions{})
	if automatic.RequiresExplicitSweep() {
		t.Fatal("default clock has no owned expiry timer")
	}
	if _, err := automatic.Create(identity(t, "alice"), time.Millisecond, m.ManagedRequestOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return automatic.Counts().Runs == 0 })
}
func TestRunLeaseConcurrentChildrenAndRenewal(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	service := runService(t, h, m.RunLeaseOptions{})
	ctx := identity(t, "alice")
	envelope, err := service.Create(ctx, time.Minute, m.ManagedRequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				if err := service.Request(ctx, envelope, func(request *m.ManagedRequest) error {
					ref, _ := request.Reference("tool")
					_, err := request.Invoke(ref, map[string]any{"x": "word"}, true)
					return err
				}); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	for range 8 {
		if _, err := service.Renew(ctx, envelope, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if h.calls.Load() != 96 || !reflect.DeepEqual(service.Counts(), m.RunCounts{Runs: 1, Pending: 0}) {
		t.Fatal(h.calls.Load(), service.Counts())
	}
}

type rotatingIdentity struct{ revision atomic.Int64 }

func (p *rotatingIdentity) Current(ctx context.Context) (m.Identity, error) {
	identity, err := m.IdentityFromContext(ctx)
	if err != nil {
		return m.Identity{}, err
	}
	if p.revision.Load() == 0 {
		identity.AuthorityRevision = "old"
	} else {
		identity.AuthorityRevision = "new"
	}
	return identity, nil
}
func TestRuntimeAuthorityRevisionMovementFailsClosed(t *testing.T) {
	h := newHarness(t)
	mustServe(t, h)
	port := &rotatingIdentity{}
	managedRuntime, err := m.NewManagedRuntime(h.coordinator, m.RequestAuthorityFunc(func(_ context.Context, request m.RequestAuthorization) ([]string, error) {
		if request.Operation == "invoke" {
			port.revision.Store(1)
		}
		return request.RequestedIDs, nil
	}), port)
	if err != nil {
		t.Fatal(err)
	}
	err = managedRuntime.Request(identity(t, "alice"), m.ManagedRequestOptions{}, func(request *m.ManagedRequest) error {
		ref, _ := request.Reference("tool")
		_, err := request.Invoke(ref, map[string]any{"x": "word"}, false)
		return err
	})
	if !errors.Is(err, m.ErrDenied) || h.calls.Load() != 0 {
		t.Fatal("authority-revision change crossed native dispatch fence", err)
	}
}
