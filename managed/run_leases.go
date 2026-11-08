package managed

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

// RunLeaseContext is an opaque service/boot/actor-local routing capability,
// never a credential or a durable Task/provenance envelope.
type RunLeaseContext struct {
	service *RunLeaseService
	token   [32]byte
}

func (RunLeaseContext) MarshalJSON() ([]byte, error) {
	return nil, errors.New("run lease contexts cannot be serialized")
}
func (RunLeaseContext) MarshalText() ([]byte, error) {
	return nil, errors.New("run lease contexts cannot be serialized")
}
func (RunLeaseContext) String() string   { return "<managed run lease>" }
func (RunLeaseContext) GoString() string { return "<managed run lease>" }

type RunLeaseOptions struct {
	MaxRuns int
	MaxTTL  time.Duration
	// Clock returns monotonic elapsed time. Custom clocks require Sweep.
	Clock func() time.Duration
}
type RunCounts struct{ Runs, Pending int }
type localRun struct {
	pin     *ManagedRunPin
	expires time.Duration
	timer   *time.Timer
}
type RunLeaseService struct {
	runtime    *ManagedRuntime
	maxRuns    int
	maxTTL     time.Duration
	clock      func() time.Duration
	automatic  bool
	mu         sync.Mutex
	lastNow    time.Duration
	runs       map[[32]byte]*localRun
	pending    int
	closed     bool
	operations sync.WaitGroup
	shutdown   chan struct{}
}

func NewRunLeaseService(runtime *ManagedRuntime, o RunLeaseOptions) (*RunLeaseService, error) {
	if runtime == nil {
		return nil, errors.New("managed runtime required")
	}
	if o.MaxRuns == 0 {
		o.MaxRuns = 128
	}
	if o.MaxTTL == 0 {
		o.MaxTTL = 5 * time.Minute
	}
	if o.MaxRuns < 1 || o.MaxTTL <= 0 || o.MaxTTL > 2147483647*time.Millisecond {
		return nil, errors.New("positive bounded run capacity and maximum TTL required")
	}
	automatic := o.Clock == nil
	if automatic {
		start := time.Now()
		o.Clock = func() time.Duration { return time.Since(start) }
	}
	return &RunLeaseService{runtime: runtime, maxRuns: o.MaxRuns, maxTTL: o.MaxTTL,
		clock: o.Clock, automatic: automatic, runs: map[[32]byte]*localRun{}}, nil
}
func (s *RunLeaseService) RequiresExplicitSweep() bool { return !s.automatic }

// IsFor checks local custody, not request authorization.
func (s *RunLeaseService) IsFor(runtime *ManagedRuntime) bool { return s.runtime == runtime }
func (s *RunLeaseService) Counts() RunCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RunCounts{len(s.runs), s.pending}
}
func (s *RunLeaseService) duration(ttl time.Duration) error {
	if ttl <= 0 || ttl > s.maxTTL {
		return errors.New("run TTL outside configured bounds")
	}
	return nil
}
func (s *RunLeaseService) nowLocked() (time.Duration, error) {
	now := s.clock()
	if now < s.lastNow || now < 0 || now > time.Duration(1<<63-1)-s.maxTTL {
		return 0, errors.New("run clock regressed or exceeded monotonic range")
	}
	s.lastNow = now
	return now, nil
}
func (s *RunLeaseService) dropLocked(token [32]byte) {
	run := s.runs[token]
	if run == nil {
		return
	}
	delete(s.runs, token)
	if run.timer != nil {
		run.timer.Stop()
	}
	run.pin.Close()
}
func (s *RunLeaseService) sweepLocked(now time.Duration) int {
	count := 0
	for token, run := range s.runs {
		if now >= run.expires {
			s.dropLocked(token)
			count++
		}
	}
	return count
}
func (s *RunLeaseService) Sweep() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.nowLocked()
	if err != nil {
		return 0, err
	}
	return s.sweepLocked(now), nil
}
func (s *RunLeaseService) armLocked(token [32]byte, run *localRun, now time.Duration) {
	if run.timer != nil {
		run.timer.Stop()
	}
	if !s.automatic {
		return
	}
	delay := run.expires - now
	if delay <= 0 {
		delay = time.Nanosecond
	}
	run.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.runs[token] != run || s.closed {
			return
		}
		now, err := s.nowLocked()
		if err != nil || now >= run.expires {
			s.dropLocked(token)
		} else {
			s.armLocked(token, run, now)
		}
	})
}
func (s *RunLeaseService) validateLocked(envelope *RunLeaseContext, identity Identity) (*localRun, time.Duration, error) {
	if envelope == nil || envelope.service != s || s.closed || s.runtime.coordinator.Closed() {
		return nil, 0, ErrDenied
	}
	run := s.runs[envelope.token]
	if run == nil || !sameActor(identity, run.pin.identity) ||
		run.pin.serving.Attempt.BootID != s.runtime.coordinator.bootID {
		return nil, 0, ErrDenied
	}
	now, err := s.nowLocked()
	if err != nil {
		return nil, 0, err
	}
	if now >= run.expires {
		s.dropLocked(envelope.token)
		return nil, now, ErrDenied
	}
	return run, now, nil
}
func (s *RunLeaseService) Create(ctx context.Context, ttl time.Duration, options ManagedRequestOptions) (*RunLeaseContext, error) {
	if err := s.duration(ttl); err != nil {
		return nil, err
	}
	s.mu.Lock()
	now, err := s.nowLocked()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.sweepLocked(now)
	if s.closed || len(s.runs)+s.pending >= s.maxRuns {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	s.pending++
	s.operations.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.pending--
		s.mu.Unlock()
		s.operations.Done()
	}()
	pin, err := s.runtime.CreatePin(ctx, options)
	if err != nil {
		return nil, err
	}
	adopted := false
	defer func() {
		if !adopted {
			pin.Close()
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.runtime.coordinator.Closed() {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now, err = s.nowLocked()
	if err != nil {
		return nil, err
	}
	var token [32]byte
	for {
		if _, err := rand.Read(token[:]); err != nil {
			return nil, err
		}
		if s.runs[token] == nil {
			break
		}
	}
	run := &localRun{pin: pin, expires: now + ttl}
	s.runs[token] = run
	s.armLocked(token, run, now)
	adopted = true
	return &RunLeaseContext{s, token}, nil
}
func (s *RunLeaseService) begin(ctx context.Context, envelope *RunLeaseContext) (*localRun, *ManagedRunPin, error) {
	identity, err := s.runtime.CurrentIdentity(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, _, err := s.validateLocked(envelope, identity)
	if err != nil {
		return nil, nil, err
	}
	s.operations.Add(1)
	return run, run.pin, nil
}
func (s *RunLeaseService) Renew(ctx context.Context, envelope *RunLeaseContext, ttl time.Duration) (*RunLeaseContext, error) {
	if err := s.duration(ttl); err != nil {
		return nil, err
	}
	run, original, err := s.begin(ctx, envelope)
	if err != nil {
		return nil, err
	}
	defer s.operations.Done()
	ceiling, identity, err := s.runtime.authorize(ctx, original.identity, "run.renew", original.ceiling)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, now, err := s.validateLocked(envelope, identity)
	if err != nil || current != run || run.pin != original {
		return nil, ErrDenied
	}
	copy := *original
	copy.ceiling = intersect(original.ceiling, ceiling)
	run.pin, run.expires = &copy, now+ttl
	s.armLocked(envelope.token, run, now)
	return envelope, nil
}
func (s *RunLeaseService) Upgrade(ctx context.Context, envelope *RunLeaseContext, ttl time.Duration) (*RunLeaseContext, error) {
	if err := s.duration(ttl); err != nil {
		return nil, err
	}
	run, original, err := s.begin(ctx, envelope)
	if err != nil {
		return nil, err
	}
	defer s.operations.Done()
	candidate, err := s.runtime.CreatePin(ctx, ManagedRequestOptions{Selection: original.anchors,
		PublicationSelection: original.publicationAnchors})
	if err != nil {
		return nil, err
	}
	adopted := false
	defer func() {
		if !adopted {
			candidate.Close()
		}
	}()
	identity, err := s.runtime.CurrentIdentity(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, now, err := s.validateLocked(envelope, identity)
	if err != nil || current != run || run.pin != original || ctx.Err() != nil {
		return nil, ErrDenied
	}
	candidate.ceiling = intersect(original.ceiling, candidate.ceiling)
	run.pin, run.expires = candidate, now+ttl
	s.armLocked(envelope.token, run, now)
	adopted = true
	original.Close()
	return envelope, nil
}
func (s *RunLeaseService) Request(ctx context.Context, envelope *RunLeaseContext, action func(*ManagedRequest) error) error {
	_, pin, err := s.begin(ctx, envelope)
	if err != nil {
		return err
	}
	defer s.operations.Done()
	return s.runtime.PinnedRequest(ctx, pin, action)
}

// CheckCurrent is an opt-in Host response fence. Retention is not a credential;
// release, expiry, service shutdown or a changed actor invalidate future returns.
func (s *RunLeaseService) CheckCurrent(ctx context.Context, envelope *RunLeaseContext) error {
	identity, err := s.runtime.CurrentIdentity(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err = s.validateLocked(envelope, identity)
	return err
}
func (s *RunLeaseService) Release(ctx context.Context, envelope *RunLeaseContext) error {
	identity, err := s.runtime.CurrentIdentity(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, err := s.validateLocked(envelope, identity); err != nil {
		return err
	}
	s.dropLocked(envelope.token)
	return nil
}

// Shutdown stops future children, drops keepers and joins entered service work.
// Call before coordinator.Shutdown. Entered children retain their own leases.
func (s *RunLeaseService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.shutdown == nil {
		s.closed = true
		s.shutdown = make(chan struct{})
		for token := range s.runs {
			s.dropLocked(token)
		}
		go func() {
			s.operations.Wait()
			close(s.shutdown)
		}()
	}
	done := s.shutdown
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
