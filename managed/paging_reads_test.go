package managed_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	m "github.com/CarterShi01/contexture-mcp-go/managed"
)

func pagingService(t *testing.T, fixture *servingReadFixture, options m.PageStoreOptions) *m.ManagedPagingService {
	t.Helper()
	if options.Signer == nil {
		options.Signer = readSigner(t)
	}
	service, err := m.NewManagedPagingService(m.PagingOptions{PageStoreOptions: options, Serving: fixture.port()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	return service
}
func pagingRef(t *testing.T, fixture *servingReadFixture, id string) string {
	t.Helper()
	current, ok := fixture.coordinator.Current()
	if !ok {
		t.Fatal("No current generation")
	}
	ref, err := m.FormatReference("graph", current.Attempt.GenerationUUID, id)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func TestReadPagingAtomicProgramsProcessesAndRelations(t *testing.T) {
	f := newServingReadFixture(t, readTree(t, readTreeOptions{width: 12}))
	s := pagingService(t, f, m.PageStoreOptions{})
	profile, err := m.NewPageProfile(m.PageProfileOptions{Limit: 2, Kinds: []string{"tool"}})
	if err != nil {
		t.Fatal(err)
	}
	request := m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner"), Profile: &profile}
	first, err := s.Start(identity(t, "actor"), request, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	obj := readJSON(t, first)
	active := readMap(t, obj["owner_active"])
	processes := readArray(t, obj["process_cards"])
	if active["instructions"] != "ORIGINAL PROGRAM" || len(processes) != 2 {
		t.Fatal("Missing atomic active program/processes")
	}
	for i, designation := range []string{"pre_process", "post_process"} {
		p := readMap(t, processes[i])
		if p["designation"] != designation || readMap(t, p["card"])["ref"] != active[designation] {
			t.Fatal("Designation/card composition drift")
		}
	}
	for _, relation := range []string{"members", "uses"} {
		page := readMap(t, obj[relation+"_page"])
		items := append([]any{}, readArray(t, page["items"])...)
		for i := 0; page["complete"] != true && i < 100; i++ {
			token := readText(t, page["next_cursor"])
			value, err := s.Continuation(identity(t, "actor"), token, request, m.PageRelation(relation))
			if err != nil {
				t.Fatal(err)
			}
			retry, err := s.Continuation(identity(t, "actor"), token, request, m.PageRelation(relation))
			if err != nil {
				t.Fatal(err)
			}
			readEqual(t, value, retry)
			current := readJSON(t, value)
			if !reflect.DeepEqual(current["owner_active"], active) || !reflect.DeepEqual(current["process_cards"], processes) {
				t.Fatal("Independent continuation lost active/process composition")
			}
			page = readMap(t, current[relation+"_page"])
			items = append(items, readArray(t, page["items"])...)
		}
		if len(items) != 12 || page["complete"] != true {
			t.Fatal("Incomplete filtered relation", relation, len(items))
		}
		for _, item := range items {
			card := readMap(t, item)
			if card["kind"] != "tool" || card["instructions"] != nil || card["input_schema"] != nil {
				t.Fatal("Relation card exceeds ROUTE ceiling")
			}
		}
	}
	s.Shutdown()
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Cleanup retained resource pins")
	}
}

func TestReadPagingPlacementOrderRoutesAndCeilings(t *testing.T) {
	f := newServingReadFixture(t, readTree(t, readTreeOptions{}))
	s := pagingService(t, f, m.PageStoreOptions{})
	r := m.PagingRequest{Operation: m.PageMembers, Reference: pagingRef(t, f, "owner")}
	value, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	members := readArray(t, readMap(t, readJSON(t, value)["members_page"])["items"])
	ids := []string{}
	for _, value := range members {
		ref, err := m.ParseReference(readText(t, readMap(t, value)["ref"]))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ref.NodeID())
	}
	if !reflect.DeepEqual(ids, []string{"pre", "branch", "post", "skill", "tool0", "tool1", "tool2"}) {
		t.Fatal("Placement order", ids)
	}
	r = m.PagingRequest{Operation: m.PageRoutes}
	f.update(func() { f.scope = []string{"tool0", "tool1", "hidden"} })
	value, err = s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	routes := readArray(t, readMap(t, readJSON(t, value)["routes_page"])["items"])
	if len(routes) != 2 {
		t.Fatal("Projected model roots", len(routes))
	}
	r = m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "tool0")}
	_, err = s.Start(identity(t, "actor"), r, time.Minute)
	readCode(t, err, "schema_invalid")
	r = m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "hidden")}
	_, err = s.Start(identity(t, "actor"), r, time.Minute)
	readCode(t, err, "permission_denied")
}

func TestReadPagingMissingProcessTemplateAndLargeAtomicMaterial(t *testing.T) {
	for _, mode := range []string{"process", "template", "program", "later", "composition", "entries"} {
		t.Run(mode, func(t *testing.T) {
			options := readTreeOptions{width: 10}
			bounds := m.PageStoreOptions{ResponseBytes: 2300}
			switch mode {
			case "template":
				options.templateHidden = true
			case "program":
				options.program = strings.Repeat("界", 2000)
			case "later":
				options.lateDescription = strings.Repeat("界", 2000)
			case "composition":
				bounds.ResponseBytes = 100
			case "entries":
				bounds.MaxItems = 5
			}
			f := newServingReadFixture(t, readTree(t, options))
			if mode == "process" {
				f.update(func() { f.scope = slices.DeleteFunc(f.scope, func(id string) bool { return id == "pre" }) })
			}
			s := pagingService(t, f, bounds)
			_, err := s.Start(identity(t, "actor"), m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner")}, time.Minute)
			want := "too_large"
			if mode == "process" || mode == "template" {
				want = "permission_denied"
			}
			readCode(t, err, want)
			if s.Counts() != (m.ReadCounts{}) {
				t.Fatal("Rejected capture retained resources", s.Counts())
			}
		})
	}
}

func TestReadServingSnapshotSwapAndCurrentIDRevocation(t *testing.T) {
	f := newServingReadFixture(t, readTree(t, readTreeOptions{width: 4}))
	s := pagingService(t, f, m.PageStoreOptions{})
	profile, _ := m.NewPageProfile(m.PageProfileOptions{Limit: 1})
	r := m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner"), Profile: &profile}
	first, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := f.coordinator.Current()
	old := current.Generation
	token := readText(t, readMap(t, readJSON(t, first)["members_page"])["next_cursor"])
	f.swap(readTree(t, readTreeOptions{width: 4, program: "NEW PROGRAM"}), "2")
	page, err := s.Continuation(identity(t, "actor"), token, r, "members")
	if err != nil {
		t.Fatal(err)
	}
	if readMap(t, readJSON(t, page)["owner_active"])["instructions"] != "ORIGINAL PROGRAM" || readJSON(t, page)["revision"] != "1" {
		t.Fatal("Mixed generation page")
	}
	if old.ResourceState() == "closed" {
		t.Fatal("Retained serving generation closed too soon")
	}
	f.swap(readTree(t, readTreeOptions{width: 4, omit: "tool1"}), "3")
	_, err = s.Continuation(identity(t, "actor"), token, r, "members")
	readCode(t, err, "resync_required")
	if s.Counts() != (m.ReadCounts{}) {
		t.Fatal("Revoked generation pin retained")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := old.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReadServingCatalogRouteVersusReservedOpen(t *testing.T) {
	d := readTree(t, readTreeOptions{})
	raw := d.AsJSON().(map[string]any)
	raw["publications"] = []any{map[string]any{"publication_id": "pub", "kind": "prompt", "opens_id": "branch",
		"name": "Prompt", "description": "Prompt description", "model_may_open": false}}
	d, err := m.NewGraphDefinition(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newServingReadFixture(t, d)
	port := f.port()
	catalog, err := m.NewManagedCatalogService(m.CatalogOptions{PageStoreOptions: m.PageStoreOptions{Signer: readSigner(t)}, Serving: &port})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Shutdown()
	r := m.CatalogRequest{GraphID: "graph", Source: m.CatalogServing}
	first, err := catalog.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	nodes := catalogAll(t, catalog, first, r, m.CatalogNodes)
	found := false
	for _, n := range nodes {
		if readMap(t, n)["node_id"] == "branch" {
			found = true
		}
		if readMap(t, n)["node_id"] == "hidden" {
			t.Fatal("Prompt plane leaked")
		}
	}
	if !found {
		t.Fatal("Routing must not use model-open reserved ceiling")
	}
	firstNode := readMap(t, nodes[0])
	if firstNode["title"] != "Localized" || firstNode["description"] != "Localized description" {
		t.Fatal("Missing pinned localization")
	}
	for _, coordinate := range []string{"generation_uuid", "boot_id", "instance_id", "target_id", "revision", "locale"} {
		if readJSON(t, first)[coordinate] == nil {
			t.Fatal("Missing serving coordinate", coordinate)
		}
	}
	paging := pagingService(t, f, m.PageStoreOptions{})
	_, err = paging.Start(identity(t, "actor"), m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "branch")}, time.Minute)
	readCode(t, err, "permission_denied")
}

func TestReadServingEveryBoundaryAndAwaitRace(t *testing.T) {
	for _, mode := range []string{"scope", "authority", "origin", "actor", "profile", "relation", "rotation", "filter"} {
		t.Run(mode, func(t *testing.T) {
			f := newServingReadFixture(t, readTree(t, readTreeOptions{width: 4}))
			signer := readSigner(t)
			s := pagingService(t, f, m.PageStoreOptions{Signer: signer})
			p, _ := m.NewPageProfile(m.PageProfileOptions{Limit: 1})
			r := m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner"), Profile: &p}
			first, err := s.Start(identity(t, "actor"), r, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			token := readText(t, readMap(t, readJSON(t, first)["members_page"])["next_cursor"])
			f.update(func() {
				switch mode {
				case "scope":
					f.scope = []string{"owner"}
				case "authority":
					f.id.AuthorityRevision = "new"
				case "origin":
					f.origin = "new"
				case "actor":
					f.id.ActorID = "new"
				}
			})
			relation := m.PageRelation("members")
			switch mode {
			case "profile":
				other, _ := m.NewPageProfile(m.PageProfileOptions{Limit: 2})
				r.Profile = &other
			case "filter":
				other, _ := m.NewPageProfile(m.PageProfileOptions{Limit: 1, Kinds: []string{"tool"}})
				r.Profile = &other
			case "relation":
				relation = "uses"
			case "rotation":
				if err := signer.Rotate([]byte(strings.Repeat("v", 32))); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Continuation(identity(t, "actor"), token, r, relation)
			readCode(t, err, "resync_required")
			if s.Counts() != (m.ReadCounts{}) {
				t.Fatal("Current authority boundary did not revoke whole snapshot")
			}
		})
	}
	t.Run("pointer moves during authority", func(t *testing.T) {
		f := newServingReadFixture(t, readTree(t, readTreeOptions{}))
		s := pagingService(t, f, m.PageStoreOptions{})
		f.update(func() {
			f.policy = func(_ context.Context, r m.RequestAuthorization) ([]string, error) {
				f.swap(readTree(t, readTreeOptions{program: "NEW"}), "2")
				return r.RequestedIDs, nil
			}
		})
		_, err := s.Start(identity(t, "actor"), m.PagingRequest{Operation: m.PageRoutes}, time.Minute)
		if err == nil {
			t.Fatal("Pointer movement across authorization admitted")
		}
		if s.Counts() != (m.ReadCounts{}) {
			t.Fatal("Failed admission leaked resources")
		}
	})
}

func TestReadPagingUTF8EnvelopeConcurrentRetention(t *testing.T) {
	f := newServingReadFixture(t, readTree(t, readTreeOptions{width: 30, description: strings.Repeat("界", 50)}))
	s := pagingService(t, f, m.PageStoreOptions{ResponseBytes: 2300})
	r := m.PagingRequest{Operation: m.PageOpen, Reference: pagingRef(t, f, "owner")}
	first, err := s.Start(identity(t, "actor"), r, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(readBytes(t, first)) > 2300 {
		t.Fatal("Initial full composition over budget")
	}
	token := readText(t, readMap(t, readJSON(t, first)["members_page"])["next_cursor"])
	var wg sync.WaitGroup
	ctx := identity(t, "actor")
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := s.Continuation(ctx, token, r, "members")
			if err != nil {
				t.Error(err)
				return
			}
			if len(readBytes(t, value)) > 2300 {
				t.Error("Full continuation exceeds UTF8 envelope limit")
			}
		}()
	}
	wg.Wait()
	total := len(readArray(t, readMap(t, readJSON(t, first)["members_page"])["items"]))
	for i := 0; i < 100; i++ {
		value, err := s.Continuation(ctx, token, r, "members")
		if err != nil {
			t.Fatal(err)
		}
		page := readMap(t, readJSON(t, value)["members_page"])
		total += len(readArray(t, page["items"]))
		if page["complete"] == true {
			break
		}
		if i == 99 || len(readArray(t, page["items"])) == 0 {
			t.Fatal("Infinite cursor")
		}
		token = readText(t, page["next_cursor"])
	}
	if total != 34 {
		t.Fatal("Incomplete member projection", total)
	}
}
