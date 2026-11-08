package managed

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

type CapturedRelease struct {
	GraphID, TargetID, ReleaseEpoch, JournalSeq, ManifestDigest string
	Snapshot                                                    RevisionState
}
type ServingAttempt struct {
	Release                                       CapturedRelease
	InstanceID, BootID, AttemptID, GenerationUUID string
}

// ReleaseAuthority is REQUIRED trusted owner authority. Capture supplies exact
// committed desired intent and retained verified history, not graph head.
// Fence must recheck current admission, approvals, desired intent and independent
// current registry/manifest evidence under the SAME owner boundary as adopt.
// It must invoke adopt synchronously, at most once, and never retain it.
type ReleaseAuthority interface {
	Capture() (*CapturedRelease, error)
	Fence(ServingAttempt, *ReadyGeneration, func() error) (bool, error)
}
type ServingPreparationProvider interface {
	Prepare(context.Context, ServingAttempt) (*ReadyGeneration, error)
}

// ServingObservationPort persists local adoption evidence outside the pointer
// gate. It never grants request authority or reconstructs a serving pointer.
type ServingObservationPort interface {
	Observe(context.Context, ServingAttempt) error
}
type ServingPreparationFunc func(context.Context, ServingAttempt) (*ReadyGeneration, error)

func (f ServingPreparationFunc) Prepare(ctx context.Context, attempt ServingAttempt) (*ReadyGeneration, error) {
	return f(ctx, attempt)
}

type ServingGeneration struct {
	Generation *ReadyGeneration
	Attempt    ServingAttempt
}
type ServingLease struct {
	ServingGeneration
	Resources *ResourceLease
}
type LocalServingResult struct {
	Attempt ServingAttempt
	Status  string
	Error   error
}
type ServingOptions struct {
	GraphID, TargetID, InstanceID   string
	Authority                       ReleaseAuthority
	Provider                        ServingPreparationProvider
	Observation                     ServingObservationPort
	MaxPreparations, MaxRetirements int
}
type ServingCounts struct{ Preparing, Retiring, Active int }

type LocalServingCoordinator struct {
	graphID, targetID, instanceID, bootID string
	authority                             ReleaseAuthority
	provider                              ServingPreparationProvider
	observation                           ServingObservationPort
	maxPreparing, maxRetiring             int
	mu                                    sync.Mutex
	gate                                  sync.Mutex
	closed                                bool
	current                               *ServingGeneration
	highest                               *CapturedRelease
	preparing                             int
	retiring                              map[*ReadyGeneration]bool
	cleanupErrors                         []error
	preparations, retirements             sync.WaitGroup
	observations                          sync.WaitGroup
	pendingObservation                    *ServingAttempt
	observing                             bool
	shutdown                              chan struct{}
}

func mintUUID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	data[6], data[8] = data[6]&15|64, data[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:]), nil
}
func NewLocalServingCoordinator(o ServingOptions) (*LocalServingCoordinator, error) {
	for _, id := range []string{o.GraphID, o.TargetID, o.InstanceID} {
		if _, err := graph.ValidateID(id); err != nil {
			return nil, err
		}
	}
	if o.Authority == nil || o.Provider == nil {
		return nil, errors.New("trusted release authority and preparation provider required")
	}
	if o.MaxPreparations == 0 {
		o.MaxPreparations = 2
	}
	if o.MaxRetirements == 0 {
		o.MaxRetirements = 8
	}
	if o.MaxPreparations < 1 || o.MaxRetirements < 1 {
		return nil, errors.New("capacities must be positive")
	}
	boot, err := mintUUID()
	if err != nil {
		return nil, err
	}
	return &LocalServingCoordinator{graphID: o.GraphID, targetID: o.TargetID, instanceID: o.InstanceID, bootID: boot,
		authority: o.Authority, provider: o.Provider, maxPreparing: o.MaxPreparations, maxRetiring: o.MaxRetirements,
		observation: o.Observation,
		retiring:    map[*ReadyGeneration]bool{}}, nil
}
func (c *LocalServingCoordinator) GraphID() string    { return c.graphID }
func (c *LocalServingCoordinator) TargetID() string   { return c.targetID }
func (c *LocalServingCoordinator) InstanceID() string { return c.instanceID }
func (c *LocalServingCoordinator) BootID() string     { return c.bootID }
func (c *LocalServingCoordinator) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
func (c *LocalServingCoordinator) Current() (ServingGeneration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.current == nil {
		return ServingGeneration{}, false
	}
	return *c.current, true
}
func (c *LocalServingCoordinator) CleanupErrors() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.cleanupErrors...)
}
func (c *LocalServingCoordinator) Counts() ServingCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	active := 0
	if c.current != nil {
		active = 1
	}
	return ServingCounts{c.preparing, len(c.retiring), active}
}
func compareCounter(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
func sameRelease(a, b CapturedRelease) bool {
	left, e1 := a.Snapshot.DefinitionDigest()
	right, e2 := b.Snapshot.DefinitionDigest()
	return e1 == nil && e2 == nil && a.GraphID == b.GraphID && a.TargetID == b.TargetID &&
		a.ReleaseEpoch == b.ReleaseEpoch && a.JournalSeq == b.JournalSeq && a.ManifestDigest == b.ManifestDigest &&
		a.Snapshot.Revision() == b.Snapshot.Revision() && left == right
}
func (c *LocalServingCoordinator) capture() (CapturedRelease, error) {
	// Serialize captures so an earlier slow owner call cannot rewrite a newer
	// watermark locally. Capture must not reenter this coordinator.
	c.gate.Lock()
	defer c.gate.Unlock()
	value, err := func() (value *CapturedRelease, err error) {
		defer recoverFailure(&err, "release capture")
		return c.authority.Capture()
	}()
	if err != nil {
		return CapturedRelease{}, err
	}
	if value == nil {
		return CapturedRelease{}, ErrNotReady
	}
	release := *value
	definition := release.Snapshot.Definition()
	if release.GraphID != c.graphID || release.TargetID != c.targetID || definition.GraphID() != c.graphID ||
		release.Snapshot.Revision() == "" || release.Snapshot.Revision() == "0" ||
		release.ReleaseEpoch == "0" || release.JournalSeq == "0" {
		return release, errors.New("captured committed release identity mismatch")
	}
	for _, value := range []string{release.ReleaseEpoch, release.JournalSeq, release.Snapshot.Revision()} {
		if _, err := graph.ParseCounter(value); err != nil {
			return release, err
		}
	}
	if len(release.ManifestDigest) != 71 || !strings.HasPrefix(release.ManifestDigest, "sha256:") ||
		strings.Trim(release.ManifestDigest[7:], "0123456789abcdef") != "" {
		return release, errors.New("invalid captured manifest digest")
	}
	if _, err := release.Snapshot.DefinitionDigest(); err != nil {
		return release, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.highest; old != nil {
		epoch, seq := compareCounter(release.ReleaseEpoch, old.ReleaseEpoch), compareCounter(release.JournalSeq, old.JournalSeq)
		if epoch < 0 || seq < 0 || (epoch == 0 && !sameRelease(release, *old)) || (epoch > 0 && seq <= 0) {
			return release, errors.New("desired release epoch/watermark regressed or was rewritten")
		}
	}
	copy := release
	c.highest = &copy
	return release, nil
}
func (c *LocalServingCoordinator) retire(g *ReadyGeneration) {
	c.mu.Lock()
	if c.retiring[g] {
		c.mu.Unlock()
		return
	}
	c.retiring[g] = true
	c.retirements.Add(1)
	c.mu.Unlock()
	g.BeginRetirement()
	go func() {
		defer c.retirements.Done()
		err := g.Wait(context.Background())
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.retiring, g)
		if err != nil {
			c.cleanupErrors = append(c.cleanupErrors, err)
		}
	}()
}
func (c *LocalServingCoordinator) Prepare(ctx context.Context) (result LocalServingResult) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return LocalServingResult{Status: "failed", Error: ErrClosed}
	}
	cleanupFailed := len(c.cleanupErrors) != 0
	for generation := range c.retiring {
		if generation.ResourceState() == "cleanup_failed" {
			cleanupFailed = true
			break
		}
	}
	if c.preparing >= c.maxPreparing || len(c.retiring)+c.preparing >= c.maxRetiring || cleanupFailed || c.pendingObservation != nil {
		c.mu.Unlock()
		return LocalServingResult{Status: "failed", Error: ErrCapacity}
	}
	c.preparing++
	c.preparations.Add(1)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.preparing--
		c.mu.Unlock()
		c.preparations.Done()
	}()
	release, err := c.capture()
	if err != nil {
		return LocalServingResult{Status: "failed", Error: err}
	}
	attemptID, err := mintUUID()
	if err != nil {
		return LocalServingResult{Status: "failed", Error: err}
	}
	generationID, err := mintUUID()
	if err != nil {
		return LocalServingResult{Status: "failed", Error: err}
	}
	attempt := ServingAttempt{release, c.instanceID, c.bootID, attemptID, generationID}
	result = LocalServingResult{Attempt: attempt, Status: "failed"}
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}
	ready, err := func() (ready *ReadyGeneration, err error) {
		defer recoverFailure(&err, "generation provider")
		return c.provider.Prepare(ctx, attempt)
	}()
	if err != nil {
		result.Error = err
		return result
	}
	if ready == nil {
		result.Error = ErrNotReady
		return result
	}
	ready.mu.Lock()
	if ready.owner != nil || ready.state != "prepared" {
		ready.mu.Unlock()
		result.Error = errors.New("preparation must return a fresh owned generation")
		return result
	}
	ready.owner = c
	ready.mu.Unlock()
	adopted := false
	defer func() {
		if !adopted {
			c.retire(ready)
		}
	}()
	expectedDigest, err := release.Snapshot.DefinitionDigest()
	actualDefinition := ready.plan.Definition()
	actualDigest, digestErr := actualDefinition.Digest()
	if err != nil || digestErr != nil || ready.context.GraphID != c.graphID ||
		ready.context.Revision != release.Snapshot.Revision() || ready.context.GenerationUUID != generationID ||
		expectedDigest != actualDigest || ready.plan.Manifest().Digest() != release.ManifestDigest {
		result.Error = errors.New("prepared generation does not match captured release")
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Error = err
		return result
	}
	latest, err := c.capture()
	if err != nil {
		result.Error = err
		return result
	}
	if !sameRelease(latest, release) {
		result.Status, result.Error = "superseded", nil
		return result
	}
	c.gate.Lock()
	var callbackMu sync.Mutex
	gateOpen := true
	called := false
	var old *ReadyGeneration
	adopt := func() error {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		if !gateOpen || called {
			return errors.New("release adoption capability is closed")
		}
		called = true
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed || c.highest == nil || !sameRelease(*c.highest, release) || c.pendingObservation != nil {
			return ErrSuperseded
		}
		if c.current != nil {
			old = c.current.Generation
			old.mu.Lock()
			if old.state == "prepared" {
				old.state = "draining"
			}
			old.mu.Unlock()
		}
		c.current = &ServingGeneration{ready, attempt}
		if c.observation != nil {
			copy := attempt
			c.pendingObservation = &copy
			c.observing = true
			c.observations.Add(1)
		}
		adopted = true
		return nil
	}
	accepted, fenceErr := func() (accepted bool, err error) {
		defer recoverFailure(&err, "release fence")
		return c.authority.Fence(attempt, ready, adopt)
	}()
	callbackMu.Lock()
	gateOpen = false
	wasAdopted := adopted
	callbackMu.Unlock()
	c.gate.Unlock()
	if old != nil {
		c.retire(old)
	}
	if wasAdopted {
		result.Status, result.Error = "serving", fenceErr
		if !accepted {
			result.Error = errors.Join(result.Error, errors.New("release fence violated adoption contract"))
		}
		if c.observation != nil {
			observed := c.observe(ctx, attempt)
			result.Status = observed.Status
			result.Error = errors.Join(result.Error, observed.Error)
		}
		return result
	}
	if fenceErr != nil {
		result.Error = fenceErr
		return result
	}
	result.Status, result.Error = "superseded", nil
	return result
}
func (c *LocalServingCoordinator) observe(ctx context.Context, attempt ServingAttempt) LocalServingResult {
	defer c.observations.Done()
	err := func() (err error) {
		defer recoverFailure(&err, "serving observation")
		return c.observation.Observe(ctx, attempt)
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observing = false
	status := "serving_unobserved"
	if err == nil {
		c.pendingObservation = nil
		status = "local_observed"
	}
	return LocalServingResult{Attempt: attempt, Status: status, Error: err}
}
func (c *LocalServingCoordinator) RetryObservation(ctx context.Context) LocalServingResult {
	c.mu.Lock()
	if c.closed || c.observation == nil || c.pendingObservation == nil || c.current == nil {
		c.mu.Unlock()
		return LocalServingResult{Status: "failed", Error: ErrNotReady}
	}
	if c.observing {
		c.mu.Unlock()
		return LocalServingResult{Status: "serving_unobserved", Error: ErrCapacity}
	}
	attempt := *c.pendingObservation
	c.observing = true
	c.observations.Add(1)
	c.mu.Unlock()
	return c.observe(ctx, attempt)
}
func (c *LocalServingCoordinator) Lease() (ServingLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.current == nil {
		return ServingLease{}, ErrNotReady
	}
	resources, err := c.current.Generation.Lease()
	if err != nil {
		return ServingLease{}, err
	}
	return ServingLease{*c.current, resources}, nil
}

// Shutdown closes admission before waiting. A cancelled wait does not fabricate
// drain completion; subsequent callers can join the same owned shutdown.
func (c *LocalServingCoordinator) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if c.shutdown == nil {
		c.closed = true
		c.shutdown = make(chan struct{})
		active := c.current
		c.current = nil
		c.mu.Unlock()
		if active != nil {
			c.retire(active.Generation)
		}
		go func() {
			c.preparations.Wait()
			c.observations.Wait()
			c.retirements.Wait()
			close(c.shutdown)
		}()
		c.mu.Lock()
	}
	done := c.shutdown
	c.mu.Unlock()
	select {
	case <-done:
		return errors.Join(c.CleanupErrors()...)
	case <-ctx.Done():
		return ctx.Err()
	}
}
