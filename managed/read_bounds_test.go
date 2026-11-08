package managed_test

import (
	"context"
	"strings"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

func TestReadCatalogFullUTF8EnvelopeExactThreshold(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{width: 20, description: strings.Repeat("界", 100)}))
	p, _ := m.NewCatalogProfile(m.PageProfileOptions{Limit: 1})
	r := readCatalogBase
	r.Profile = &p
	signer := readSigner(t)
	seed, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: signer}, Definition: f})
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Shutdown()
	first, err := seed.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	threshold := len(readBytes(t, first))
	for _, stream := range []m.CatalogStream{m.CatalogNodes, m.CatalogContainment, m.CatalogUses} {
		token := readText(t, readMap(t, readJSON(t, first)["streams"])[string(stream)])
		for i := 0; i < 100; i++ {
			page, err := seed.Continuation(identity(t, "actor"), token, r, stream)
			if err != nil {
				t.Fatal(err)
			}
			threshold = max(threshold, len(readBytes(t, page)))
			obj := readJSON(t, page)
			if obj["complete"] == true {
				break
			}
			token = readText(t, obj["next_cursor"])
		}
	}
	for _, budget := range []int{threshold, threshold - 1} {
		s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: signer, ResponseBytes: budget}, Definition: f})
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.Start(identity(t, "actor"), r, time.Minute)
		if budget == threshold-1 {
			readCode(t, err, "too_large")
			if s.Counts() != (m.ReadCounts{}) {
				t.Fatal("Threshold rejection leaked snapshot")
			}
		} else {
			if err != nil {
				t.Fatal("Exact full UTF8 threshold must fit", err)
			}
			if len(catalogAll(t, s, first, r, m.CatalogNodes)) != 25 {
				t.Fatal("Exact budget lost items")
			}
		}
		s.Shutdown()
	}
}

func TestReadServingRequiresRealSharedCurrentFence(t *testing.T) {
	f := newServingReadFixture(t, readTree(t, readTreeOptions{}))
	port := f.port()
	port.Fence = nil
	if _, err := m.NewManagedPagingService(m.PagingOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Serving: port}); err == nil {
		t.Fatal("Must not fabricate current admission fence")
	}
	if _, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Serving: &port}); err == nil {
		t.Fatal("Must not fabricate Catalog admission fence")
	}
	s := pagingService(t, f, m.PageStoreOptions{})
	p, _ := m.NewPageProfile(m.PageProfileOptions{Limit: 1})
	r := m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner"), Profile: &p}
	first, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := readText(t, readMap(t, readJSON(t, first)["members_page"])["next_cursor"])
	entered, proceed := make(chan struct{}), make(chan struct{})
	f.update(func() {
		f.policy = func(_ context.Context, r m.RequestAuthorization) ([]string, error) {
			close(entered)
			<-proceed
			return r.RequestedIDs, nil
		}
	})
	ctx := identity(t, "actor")
	readDone := make(chan error, 1)
	go func() { _, err := s.Continuation(ctx, token, r, "members"); readDone <- err }()
	await(t, entered)
	mutatorStarted, mutated := make(chan struct{}), make(chan struct{})
	go func() {
		close(mutatorStarted)
		f.update(func() { f.scope, f.policy = []string{"owner"}, nil })
		close(mutated)
	}()
	await(t, mutatorStarted)
	select {
	case <-mutated:
		t.Fatal("Current admission moved while read return fence held")
	default:
	}
	close(proceed)
	if err := await(t, readDone); err != nil {
		t.Fatal(err)
	}
	await(t, mutated)
	_, err = s.Continuation(ctx, token, r, "members")
	readCode(t, err, "resync_required")
}

func TestReadWatchInlineRetryCannotRegrantDeletedCurrentIDs(t *testing.T) {
	f := newCommittedReadFixture(t)
	f.scope = []string{"tool1"}
	s := watchService(t, f, m.WatchOptions{})
	first, err := s.Start(identity(t, "actor"), readWatchBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := readText(t, readJSON(t, first)["cursor"])
	f.append(t, readTree(t, readTreeOptions{description: "Updated"}))
	delivered, err := s.Poll(identity(t, "actor"), token, readWatchBase)
	if err != nil || len(readArray(t, readJSON(t, delivered)["batches"])) != 1 {
		t.Fatal("Missing inline batch", err)
	}
	f.append(t, readTree(t, readTreeOptions{omit: "tool1"}))
	retried, err := s.Poll(identity(t, "actor"), token, readWatchBase)
	readCode(t, err, "resync_required")
	if retried.JSON() != nil || s.Counts() != (m.WatchCounts{}) {
		t.Fatal("Stable retry changed payload or exposed deleted metadata")
	}
}

func TestReadFailedFenceCannotRetainCallbacksOrPublishMetadata(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{}))
	var saved func(m.DefinitionReadView) (m.FrozenJSON, error)
	port := m.DefinitionReadFunc(func(_ context.Context, _ m.DefinitionReadRequest, dispatch func(m.DefinitionReadView) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
		saved = dispatch
		panic("trusted port failed before dispatch")
	})
	s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: port})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	result, err := s.Start(identity(t, "actor"), readCatalogBase, time.Minute)
	if err == nil || result.JSON() != nil {
		t.Fatal("Panic became successful/default metadata")
	}
	_, err = saved(m.DefinitionReadView{Identity: f.id, StoreOrigin: f.origin, Index: f.current,
		CurrentIndex: f.current, Revision: "1", ScopeIDs: f.scope})
	readCode(t, err, "contract_mismatch")
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Retained callback resurrected failed capture")
	}
}
