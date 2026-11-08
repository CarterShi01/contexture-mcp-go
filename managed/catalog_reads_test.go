package managed_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

var readCatalogBase = m.CatalogRequest{GraphID: "graph", Source: m.CatalogHead}

func TestReadCatalogCompletenessPrivacyAndRetry(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{width: 430}))
	s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: f})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	first, err := s.Start(identity(t, "actor"), readCatalogBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	nodes := catalogAll(t, s, first, readCatalogBase, m.CatalogNodes)
	edges := catalogAll(t, s, first, readCatalogBase, m.CatalogContainment)
	uses := catalogAll(t, s, first, readCatalogBase, m.CatalogUses)
	if len(nodes) != 435 || len(edges) != 434 || len(uses) != 431 {
		t.Fatal(len(nodes), len(edges), len(uses))
	}
	firstIDs := []string{}
	for _, n := range nodes[:5] {
		firstIDs = append(firstIDs, readText(t, readMap(t, n)["node_id"]))
	}
	if !reflect.DeepEqual(firstIDs, []string{"owner", "pre", "branch", "post", "skill"}) {
		t.Fatal(firstIDs)
	}
	token := readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
	page, err := s.Continuation(identity(t, "actor"), token, readCatalogBase, m.CatalogNodes)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Continuation(identity(t, "actor"), token, readCatalogBase, m.CatalogNodes)
	if err != nil {
		t.Fatal(err)
	}
	readEqual(t, page, retry)
	obj := readJSON(t, page)
	if len(readArray(t, obj["items"])) != 200 || obj["complete"] != false {
		t.Fatal("Default page boundary")
	}
	value, err := m.FreezeJSON(map[string]any{"first": first.JSON(), "nodes": nodes, "edges": edges, "uses": uses})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(readBytes(t, value))
	for _, forbidden := range []string{"SECRET", "instructions", "input_schema", "definition_digest", "manifest_digest", "ordinal", "parent_id", "count", "journal_seq"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatal("Leaked", forbidden)
		}
	}
	if f.calls < 10 {
		t.Fatal("Every continuation must reenter owner fence")
	}
}

func TestReadCatalogHiddenFilteringAndFixedHistory(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{}))
	f.scope = []string{"tool0", "tool1"}
	s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: f})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	history := readCatalogBase
	history.Source, history.Revision = m.CatalogRevision, "1"
	first, err := s.Start(identity(t, "actor"), history, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.advance(t, readTree(t, readTreeOptions{hidden: "CHANGED SECRET", program: "CHANGED PROGRAM", prefixHidden: true}))
	f.update(func() { delete(f.history, "1") })
	nextRevision := history
	nextRevision.Revision = "2"
	second, err := s.Start(identity(t, "actor"), nextRevision, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if readJSON(t, first)["view_digest"] != readJSON(t, second)["view_digest"] {
		t.Fatal("Hidden parent/program/positions affected authorized digest")
	}
	if readJSON(t, first)["revision"] == readJSON(t, second)["revision"] {
		t.Fatal("Missing explicit revision")
	}
	for _, n := range catalogAll(t, s, first, history, m.CatalogNodes) {
		if readMap(t, n)["projected_root"] != true {
			t.Fatal("Hidden parent exposed")
		}
	}
	if len(catalogAll(t, s, first, history, m.CatalogContainment)) != 0 || len(catalogAll(t, s, first, history, m.CatalogUses)) != 0 {
		t.Fatal("Hidden relation endpoint")
	}
	_, err = s.Start(identity(t, "actor"), history, time.Second)
	readCode(t, err, "history_unavailable")
	profile, err := m.NewCatalogProfile(m.PageProfileOptions{Kinds: []string{"role"}})
	if err != nil {
		t.Fatal(err)
	}
	filtered := readCatalogBase
	filtered.Profile = &profile
	empty, err := s.Start(identity(t, "actor"), filtered, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []m.CatalogStream{m.CatalogNodes, m.CatalogContainment, m.CatalogUses} {
		if len(catalogAll(t, s, empty, filtered, stream)) != 0 {
			t.Fatal("Filter leaked relation/card")
		}
	}
	noSelection := readCatalogBase
	noSelection.Selection = []string{}
	none, err := s.Start(identity(t, "actor"), noSelection, time.Second)
	if err != nil || len(catalogAll(t, s, none, noSelection, m.CatalogNodes)) != 0 {
		t.Fatal("Explicit empty selection", err)
	}
	f.advance(t, readTree(t, readTreeOptions{omit: "tool1"}))
	token := readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
	_, err = s.Continuation(identity(t, "actor"), token, history, m.CatalogNodes)
	readCode(t, err, "resync_required")
}

func TestReadCatalogRevokesWholeViewOnEveryCurrentBoundary(t *testing.T) {
	for _, mode := range []string{"actor", "tenant", "authority", "scope", "origin", "deny", "profile", "selection", "source", "stream", "context"} {
		t.Run(mode, func(t *testing.T) {
			f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{}))
			s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: f})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown()
			first, err := s.Start(identity(t, "actor"), readCatalogBase, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			r, stream, ctx := readCatalogBase, m.CatalogNodes, identity(t, "actor")
			f.update(func() {
				switch mode {
				case "actor":
					f.id.ActorID = "other"
				case "tenant":
					f.id.TenantID = "other"
				case "authority":
					f.id.AuthorityRevision = "other"
				case "scope":
					f.scope = []string{"owner"}
				case "origin":
					f.origin = "other"
				case "deny":
					f.err = readDiagnostic{"permission_denied"}
				}
			})
			switch mode {
			case "profile":
				profile, _ := m.NewCatalogProfile(m.PageProfileOptions{Limit: 1})
				r.Profile = &profile
			case "selection":
				r.Selection = []string{"owner"}
			case "source":
				r.Source, r.Revision = m.CatalogRevision, "1"
			case "stream":
				stream = m.CatalogUses
			case "context":
				ctx = context.Background()
			}
			token := readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
			_, err = s.Continuation(ctx, token, r, stream)
			readCode(t, err, "resync_required")
			if s.Counts() != (m.ReadCounts{}) {
				t.Fatal("All inaccessible streams must be discarded", s.Counts())
			}
		})
	}
}

func TestReadCatalogOpaqueRotationExpiryCapacityAndUTF8(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{width: 40, description: strings.Repeat("界", 40)}))
	clock, signer := &readClock{}, readSigner(t)
	o := m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: signer, Clock: clock.clock, ResponseBytes: 1300, MaxSnapshots: 1}, Definition: f}
	s, err := m.NewManagedCatalogService(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	first, err := s.Start(identity(t, "actor"), readCatalogBase, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !s.RequiresExplicitSweep() {
		t.Fatal("Custom clock needs sweep")
	}
	_, err = s.Start(identity(t, "actor"), readCatalogBase, time.Second)
	readCode(t, err, "resource_busy")
	token := readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
	if len(token) != 129 || strings.Contains(token, "graph") || strings.Contains(token, "{") {
		t.Fatal("Nonopaque token", token)
	}
	other, err := m.NewManagedCatalogService(o)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Shutdown()
	_, err = other.Continuation(identity(t, "actor"), token, readCatalogBase, m.CatalogNodes)
	readCode(t, err, "resync_required")
	tampered := token[:128] + "x"
	_, err = s.Continuation(identity(t, "actor"), tampered, readCatalogBase, m.CatalogNodes)
	readCode(t, err, "resync_required")
	for _, stream := range []m.CatalogStream{m.CatalogNodes, m.CatalogContainment, m.CatalogUses} {
		cursor := readText(t, readMap(t, readJSON(t, first)["streams"])[string(stream)])
		for i := 0; i < 200; i++ {
			page, err := s.Continuation(identity(t, "actor"), cursor, readCatalogBase, stream)
			if err != nil {
				t.Fatal(err)
			}
			if len(readBytes(t, page)) > 1300 {
				t.Fatal("Complete UTF8 envelope exceeds budget")
			}
			obj := readJSON(t, page)
			if obj["complete"] == true {
				break
			}
			if len(readArray(t, obj["items"])) == 0 || i == 199 {
				t.Fatal("No cursor progress")
			}
			cursor = readText(t, obj["next_cursor"])
		}
	}
	clock.advance(time.Second)
	if s.Sweep() != 1 || s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Retention cleanup")
	}
	_, err = s.Continuation(identity(t, "actor"), token, readCatalogBase, m.CatalogNodes)
	readCode(t, err, "resync_required")
	first, err = s.Start(identity(t, "actor"), readCatalogBase, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Rotate([]byte(strings.Repeat("n", 32))); err != nil {
		t.Fatal(err)
	}
	token = readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
	_, err = s.Continuation(identity(t, "actor"), token, readCatalogBase, m.CatalogNodes)
	readCode(t, err, "resync_required")
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Rotation retained inaccessible bytes")
	}
}

func TestReadCatalogRejectsLaterLargeItemsAndEnvelopeBudgets(t *testing.T) {
	for _, mode := range []string{"later", "entries", "material", "cursor", "envelope"} {
		t.Run(mode, func(t *testing.T) {
			tree := readTreeOptions{width: 10}
			o := m.PageStoreOptions{Signer: readSigner(t)}
			switch mode {
			case "later":
				tree.lateDescription = strings.Repeat("界", 1000)
				o.ResponseBytes = 1500
			case "entries":
				o.MaxItems = 5
			case "material":
				o.MaxBytes = 100
			case "cursor":
				o.MaxCursors = 2
			case "envelope":
				o.ResponseBytes = 100
			}
			f := newDefinitionReadFixture(t, readTree(t, tree))
			s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: o, Definition: f})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown()
			_, err = s.Start(identity(t, "actor"), readCatalogBase, time.Second)
			want := "too_large"
			if mode == "cursor" {
				want = "resource_busy"
			}
			readCode(t, err, want)
			if s.Counts() != (m.ReadCounts{}) {
				t.Fatal("Rejected capture leaked state", s.Counts())
			}
		})
	}
}

func TestReadCatalogPortErrorsAndDispatchContract(t *testing.T) {
	for _, mode := range []string{"missing", "double", "after", "ignored", "corruption"} {
		t.Run(mode, func(t *testing.T) {
			f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{}))
			var saved func(m.DefinitionReadView) (m.FrozenJSON, error)
			port := m.DefinitionReadFunc(func(ctx context.Context, r m.DefinitionReadRequest, dispatch func(m.DefinitionReadView) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
				switch mode {
				case "missing":
					return m.FrozenJSON{}, nil
				case "corruption":
					return m.FrozenJSON{}, errors.New("corrupt committed store")
				case "after":
					saved = dispatch
				}
				value, err := f.Read(ctx, r, dispatch)
				if mode == "double" {
					_, _ = f.Read(ctx, r, dispatch)
				}
				if mode == "ignored" {
					return m.FrozenJSON{}, nil
				}
				return value, err
			})
			s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: port})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown()
			_, err = s.Start(identity(t, "actor"), readCatalogBase, time.Second)
			if mode == "after" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = saved(m.DefinitionReadView{})
				readCode(t, err, "contract_mismatch")
			} else if mode == "corruption" {
				if err == nil || err.Error() != "corrupt committed store" {
					t.Fatal("Failure was masked", err)
				}
			} else {
				readCode(t, err, "contract_mismatch")
				if s.Counts() != (m.ReadCounts{}) {
					t.Fatal("Port failure leaked state")
				}
			}
		})
	}
}

func TestReadCatalogConcurrentPagesAndPendingShutdown(t *testing.T) {
	f := newDefinitionReadFixture(t, readTree(t, readTreeOptions{width: 30}))
	entered, release := make(chan struct{}), make(chan struct{})
	port := m.DefinitionReadFunc(func(ctx context.Context, r m.DefinitionReadRequest, dispatch func(m.DefinitionReadView) (m.FrozenJSON, error)) (m.FrozenJSON, error) {
		close(entered)
		<-release
		return f.Read(ctx, r, dispatch)
	})
	s, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t), MaxSnapshots: 1}, Definition: port})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Start(identity(t, "actor"), readCatalogBase, time.Second); done <- err }()
	await(t, entered)
	_, err = s.Start(identity(t, "actor"), readCatalogBase, time.Second)
	readCode(t, err, "resource_busy")
	s.Shutdown()
	close(release)
	readCode(t, await(t, done), "resync_required")
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Pending shutdown leaked")
	}
	s, err = m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Definition: f})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	first, err := s.Start(identity(t, "actor"), readCatalogBase, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := readText(t, readMap(t, readJSON(t, first)["streams"])["nodes"])
	var wg sync.WaitGroup
	ctx := identity(t, "actor")
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if _, err := s.Continuation(ctx, token, readCatalogBase, m.CatalogNodes); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	s.Shutdown()
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Concurrent cleanup")
	}
}
