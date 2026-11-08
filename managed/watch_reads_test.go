package managed_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

var readWatchBase = m.WatchRequest{GraphID: "graph"}

func watchService(t *testing.T, fixture *committedReadFixture, o m.WatchOptions) *m.ManagedWatchService {
	t.Helper()
	o.Reader = fixture
	if o.Signer == nil {
		o.Signer = readSigner(t)
	}
	s, err := m.NewManagedWatchService(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s
}
func watchName(t *testing.T, value any) string {
	t.Helper()
	nodes := readArray(t, readMap(t, value)["nodes"])
	if len(nodes) != 1 {
		t.Fatal("Expected complete single authorized node", len(nodes))
	}
	return readText(t, readMap(t, nodes[0])["name"])
}

func TestReadWatchOneCommittedCutWholeBatchAndDuplicates(t *testing.T) {
	f := newCommittedReadFixture(t)
	s := watchService(t, f, m.WatchOptions{})
	first, err := s.Start(identity(t, "actor"), readWatchBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if watchName(t, readJSON(t, first)["snapshot"]) != "Owner" || f.calls != 1 {
		t.Fatal("Snapshot/checkpoint must share one committed cut")
	}
	token := readText(t, readJSON(t, first)["cursor"])
	f.append(t, readTree(t, readTreeOptions{label: "Second", description: "All facts committed together"}))
	result, err := s.Poll(identity(t, "actor"), token, readWatchBase)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Poll(identity(t, "actor"), token, readWatchBase)
	if err != nil {
		t.Fatal(err)
	}
	readEqual(t, result, retry)
	batches := readArray(t, readJSON(t, result)["batches"])
	if len(batches) != 1 {
		t.Fatal("One descriptor for ONE complete durable record", len(batches))
	}
	batch := readMap(t, batches[0])
	if watchName(t, batch["payload"]) != "Second" {
		t.Fatal("No partial event state")
	}
	payload, err := m.FreezeJSON(batch["payload"])
	if err != nil {
		t.Fatal(err)
	}
	if batch["size"] != float64(len(readBytes(t, payload))) {
		t.Fatal("Projected UTF8 size", batch["size"])
	}
	next, err := s.Poll(identity(t, "actor"), readText(t, readJSON(t, result)["cursor"]), readWatchBase)
	if err != nil || len(readArray(t, readJSON(t, next)["batches"])) != 0 {
		t.Fatal("Duplicate batch after accepted checkpoint", err)
	}
	if f.lastBounds != (m.CommittedBatchBounds{MaxBatches: 1024, MaxBytes: 16 << 20}) {
		t.Fatal("Missing bounded trusted backlog reader", f.lastBounds)
	}
}

func TestReadWatchHiddenNoopsOpaqueProgressAndProjectionDigest(t *testing.T) {
	f := newCommittedReadFixture(t)
	s := watchService(t, f, m.WatchOptions{})
	first, err := s.Start(identity(t, "actor"), readWatchBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, readTree(t, readTreeOptions{hidden: "HIDDEN CHANGE", program: "HIDDEN PROGRAM", prefixHidden: true}))
	empty, err := s.Poll(identity(t, "actor"), readText(t, readJSON(t, first)["cursor"]), readWatchBase)
	if err != nil {
		t.Fatal(err)
	}
	if len(readArray(t, readJSON(t, empty)["batches"])) != 0 || readJSON(t, empty)["cursor"] == readJSON(t, first)["cursor"] {
		t.Fatal("Hidden/noop advances only an opaque checkpoint")
	}
	again, err := s.Start(identity(t, "actor"), readWatchBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if readJSON(t, again)["view_digest"] != readJSON(t, first)["view_digest"] {
		t.Fatal("Global hidden changes affected projection digest")
	}
	serialized := string(readBytes(t, first)) + string(readBytes(t, empty))
	for _, forbidden := range []string{"SECRET", "HIDDEN", "journal_seq", "revision", "count", "remaining", "caught_up", "definition_digest", "manifest_digest", "instructions"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatal("Leaked hidden metadata", forbidden)
		}
	}
	r := readWatchBase
	r.Selection = []string{}
	none, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, readTree(t, readTreeOptions{label: "Not selected"}))
	value, err := s.Poll(identity(t, "actor"), readText(t, readJSON(t, none)["cursor"]), r)
	if err != nil || len(readArray(t, readJSON(t, value)["batches"])) != 0 {
		t.Fatal("Exact stable-ID attenuation", err)
	}
}

func TestReadWatchCompleteFetchUTF8BatchBoundaries(t *testing.T) {
	f := newCommittedReadFixture(t)
	s := watchService(t, f, m.WatchOptions{})
	profile, err := m.NewWatchProfile(m.WatchProfile{MaxBatches: 1, MaxBytes: 1000, InlineBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := readWatchBase
	r.Profile = &profile
	first, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cursor := readText(t, readJSON(t, first)["cursor"])
	for _, label := range []string{"Second", "Third", strings.Repeat("界", 1000)} {
		f.append(t, readTree(t, readTreeOptions{label: label}))
	}
	for _, label := range []string{"Second", "Third", strings.Repeat("界", 1000)} {
		page, err := s.Poll(identity(t, "actor"), cursor, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(readBytes(t, page)) > 1000 {
			t.Fatal("Descriptor envelope over UTF8 limit")
		}
		batches := readArray(t, readJSON(t, page)["batches"])
		if len(batches) != 1 {
			t.Fatal("Atomic batch boundary", len(batches))
		}
		batch := readMap(t, batches[0])
		if batch["payload"] != nil {
			t.Fatal("Must use honest complete fetch")
		}
		fetch, err := s.Fetch(identity(t, "actor"), readText(t, batch["authorized_fetch_ref"]), r)
		if err != nil {
			t.Fatal(err)
		}
		if watchName(t, fetch.JSON()) != label || batch["size"] != float64(len(readBytes(t, fetch))) {
			t.Fatal("Fetch was truncated or reconstructed from live head")
		}
		retry, err := s.Fetch(identity(t, "actor"), readText(t, batch["authorized_fetch_ref"]), r)
		if err != nil {
			t.Fatal(err)
		}
		readEqual(t, fetch, retry)
		cursor = readText(t, readJSON(t, page)["cursor"])
	}
}

func TestReadWatchFullEnvelopeThresholdAndLargePayload(t *testing.T) {
	for _, mode := range []string{"snapshot", "descriptor", "payload", "inline_fallback"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommittedReadFixture(t)
			o := m.WatchOptions{}
			profile := m.WatchProfile{MaxBytes: 1000}
			switch mode {
			case "snapshot":
				profile.MaxBytes = 200
			case "descriptor":
				profile.MaxBytes = 500
			case "payload":
				o.MaxPayloadBytes = 1000
			case "inline_fallback":
				profile.InlineBytes = 1000
			}
			p, err := m.NewWatchProfile(profile)
			if err != nil {
				t.Fatal(err)
			}
			s := watchService(t, f, o)
			r := readWatchBase
			r.Profile = &p
			first, err := s.Start(identity(t, "actor"), r, time.Minute)
			if mode == "snapshot" {
				readCode(t, err, "too_large")
				if s.Counts() != (m.WatchCounts{}) {
					t.Fatal("Rejected snapshot leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			label := strings.Repeat("界", 220)
			if mode == "payload" {
				label = strings.Repeat("界", 2000)
			}
			f.append(t, readTree(t, readTreeOptions{label: label}))
			value, err := s.Poll(identity(t, "actor"), readText(t, readJSON(t, first)["cursor"]), r)
			if mode != "inline_fallback" {
				readCode(t, err, "too_large")
				if s.Counts() != (m.WatchCounts{}) {
					t.Fatal("Oversized atomic output leaked handles")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				batch := readMap(t, readArray(t, readJSON(t, value)["batches"])[0])
				if batch["payload"] != nil || batch["authorized_fetch_ref"] == nil || len(readBytes(t, value)) > 1000 {
					t.Fatal("Full UTF8 envelope requires complete fetch")
				}
			}
		})
	}
}

func TestReadWatchReauthorizationRevokesAllFetchesAndCheckpoints(t *testing.T) {
	for _, mode := range []string{"actor", "tenant", "authority", "scope", "origin", "deny", "selection", "profile", "graph", "rotation", "context"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommittedReadFixture(t)
			signer := readSigner(t)
			s := watchService(t, f, m.WatchOptions{Signer: signer})
			p, _ := m.NewWatchProfile(m.WatchProfile{InlineBytes: 1})
			r := readWatchBase
			r.Profile = &p
			first, err := s.Start(identity(t, "actor"), r, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			f.append(t, readTree(t, readTreeOptions{label: "Changed"}))
			cursor := readText(t, readJSON(t, first)["cursor"])
			page, err := s.Poll(identity(t, "actor"), cursor, r)
			if err != nil {
				t.Fatal(err)
			}
			fetch := readText(t, readMap(t, readArray(t, readJSON(t, page)["batches"])[0])["authorized_fetch_ref"])
			ctx, changed := identity(t, "actor"), r
			f.update(func() {
				switch mode {
				case "actor":
					f.id.ActorID = "other"
				case "tenant":
					f.id.TenantID = "other"
				case "authority":
					f.id.AuthorityRevision = "other"
				case "scope":
					f.scope = []string{}
				case "origin":
					f.origin = "other"
				case "deny":
					f.err = readDiagnostic{"permission_denied"}
				}
			})
			switch mode {
			case "selection":
				changed.Selection = []string{}
			case "profile":
				other, _ := m.NewWatchProfile(m.WatchProfile{MaxBatches: 1})
				changed.Profile = &other
			case "graph":
				changed.GraphID = "other"
			case "rotation":
				if err := signer.Rotate([]byte(strings.Repeat("z", 32))); err != nil {
					t.Fatal(err)
				}
			case "context":
				ctx = context.Background()
			}
			_, err = s.Poll(ctx, cursor, changed)
			readCode(t, err, "resync_required")
			_, err = s.Fetch(identity(t, "actor"), fetch, r)
			readCode(t, err, "resync_required")
			if s.Counts() != (m.WatchCounts{}) {
				t.Fatal("Whole-session invalidation required", s.Counts())
			}
		})
	}
}

func TestReadWatchRetentionBackpressureCapacityAndPortFailures(t *testing.T) {
	for _, mode := range []string{"retention", "overflow", "handles", "bytes", "corruption", "duplicate", "foreign", "regressing", "through"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommittedReadFixture(t)
			o := m.WatchOptions{}
			if mode == "handles" {
				o.MaxHandles = 1
			}
			if mode == "bytes" {
				o.MaxRetainedBytes = 1000
			}
			s := watchService(t, f, o)
			p, _ := m.NewWatchProfile(m.WatchProfile{InlineBytes: 1})
			r := readWatchBase
			r.Profile = &p
			first, err := s.Start(identity(t, "actor"), r, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			f.append(t, readTree(t, readTreeOptions{label: strings.Repeat("界", 300)}))
			f.update(func() {
				switch mode {
				case "retention":
					f.retained = "2"
				case "overflow":
					f.overflow = true
				case "corruption":
					f.err = errors.New("corrupt batch history")
				case "duplicate":
					f.windowHook = func(w *m.CommittedBatchWindow) { w.Batches = append(w.Batches, w.Batches[0]) }
				case "foreign":
					f.windowHook = func(w *m.CommittedBatchWindow) { w.Batches[0].State = m.RevisionState{} }
				case "regressing":
					f.windowHook = func(w *m.CommittedBatchWindow) { w.Batches[0].Position = "1" }
				case "through":
					f.windowHook = func(w *m.CommittedBatchWindow) { w.Through = "9" }
				}
			})
			_, err = s.Poll(identity(t, "actor"), readText(t, readJSON(t, first)["cursor"]), r)
			want := "resync_required"
			switch mode {
			case "retention":
				want = "history_unavailable"
			case "duplicate", "foreign", "regressing", "through":
				want = "contract_mismatch"
			case "corruption":
				if err == nil || err.Error() != "corrupt batch history" {
					t.Fatal("Corruption silently masked", err)
				}
			}
			if mode != "corruption" {
				readCode(t, err, want)
			}
			if s.Counts() != (m.WatchCounts{}) {
				t.Fatal("Failure/backpressure did not close session")
			}
		})
	}
}

func TestReadWatchOpacityExpiryRestartAndConcurrentShutdown(t *testing.T) {
	f := newCommittedReadFixture(t)
	clock, signer := &readClock{}, readSigner(t)
	s := watchService(t, f, m.WatchOptions{Clock: clock.clock, Signer: signer})
	first, err := s.Start(identity(t, "actor"), readWatchBase, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	token := readText(t, readJSON(t, first)["cursor"])
	if len(token) != 129 || strings.ContainsAny(token, "{:=") {
		t.Fatal("Global sequence in decodable checkpoint")
	}
	_, err = s.Poll(identity(t, "actor"), token[:128]+"x", readWatchBase)
	readCode(t, err, "resync_required")
	other := watchService(t, f, m.WatchOptions{Clock: clock.clock, Signer: signer})
	_, err = other.Poll(identity(t, "actor"), token, readWatchBase)
	readCode(t, err, "resync_required")
	clock.advance(time.Second)
	if s.Sweep() != 1 || s.Counts() != (m.WatchCounts{}) {
		t.Fatal("Expiry resource retention")
	}
	_, err = s.Poll(identity(t, "actor"), token, readWatchBase)
	readCode(t, err, "resync_required")

	entered, release := make(chan struct{}), make(chan struct{})
	blocked, err := m.NewManagedWatchService(m.WatchOptions{Signer: readSigner(t), Reader: m.CommittedBatchReadFunc(
		func(ctx context.Context, id string, dispatch func(m.CommittedReadCut) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
			close(entered)
			<-release
			return f.Read(ctx, id, dispatch)
		})})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx := identity(t, "actor")
	go func() { _, err := blocked.Start(ctx, readWatchBase, time.Minute); done <- err }()
	await(t, entered)
	_, err = blocked.Start(ctx, readWatchBase, time.Minute)
	readCode(t, err, "resource_busy")
	blocked.Shutdown()
	close(release)
	readCode(t, await(t, done), "resync_required")
	if blocked.Counts() != (m.WatchCounts{}) {
		t.Fatal("Late capture resurrected shutdown")
	}
}

func TestReadWatchCooperativeDeadlineAndFetchCurrentIDs(t *testing.T) {
	f := newCommittedReadFixture(t)
	clock := &readClock{}
	s := watchService(t, f, m.WatchOptions{Clock: clock.clock})
	first, err := s.Start(identity(t, "actor"), readWatchBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, readTree(t, readTreeOptions{label: "Changed"}))
	f.update(func() { f.windowHook = func(*m.CommittedBatchWindow) { clock.advance(2 * time.Second) } })
	_, err = s.Poll(identity(t, "actor"), readText(t, readJSON(t, first)["cursor"]), readWatchBase)
	readCode(t, err, "resync_required")
	if s.Counts() != (m.WatchCounts{}) {
		t.Fatal("Deadline did not discard")
	}

	f = newCommittedReadFixture(t)
	f.scope = []string{"tool1"}
	s = watchService(t, f, m.WatchOptions{})
	p, _ := m.NewWatchProfile(m.WatchProfile{InlineBytes: 1})
	r := readWatchBase
	r.Profile = &p
	first, err = s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, readTree(t, readTreeOptions{description: "Changed tool"}))
	page, err := s.Poll(identity(t, "actor"), readText(t, readJSON(t, first)["cursor"]), r)
	if err != nil {
		t.Fatal(err)
	}
	fetch := readText(t, readMap(t, readArray(t, readJSON(t, page)["batches"])[0])["authorized_fetch_ref"])
	f.append(t, readTree(t, readTreeOptions{omit: "tool1"}))
	_, err = s.Fetch(identity(t, "actor"), fetch, r)
	readCode(t, err, "resync_required")
	if s.Counts() != (m.WatchCounts{}) {
		t.Fatal("Old fetch permission survived stable-ID deletion")
	}
}
