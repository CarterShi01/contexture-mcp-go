package graph

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func evChange(t *testing.T, s RevisionState, edit func(map[string]any)) ResolvedChange {
	t.Helper()
	wire := s.definition.AsJSON().(map[string]any)
	edit(wire)
	g, e := NewGraphDefinition(wire, nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := ResolveFacts(s, g)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func evOptions() EventOptions {
	return EventOptions{CommandDigest: "sha256:" + strings.Repeat("1", 64), Reason: "native resolved events test"}
}
func evEvent(t *testing.T, s RevisionState, c ResolvedChange) FrozenJSON {
	t.Helper()
	e, err := ChangeEvent(s, c, evOptions())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func evApply(t *testing.T, s any, e any, tombstones any) RevisionState {
	t.Helper()
	next, err := ApplyEvent(s, e, ApplyEventOptions{Tombstones: tombstones})
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func evNode(w map[string]any, id string) map[string]any {
	for _, raw := range arr(w["nodes"]) {
		n := raw.(map[string]any)
		if n["node_id"] == id {
			return n
		}
	}
	panic("test node missing")
}
func evEqual(t *testing.T, a, b any) {
	t.Helper()
	if !pureEqual(a, b) {
		t.Fatalf("got %s\nwant %s", pureEncoding(a), pureEncoding(b))
	}
}

func TestResolvedEventsRoundTripAndImmutability(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) { evNode(w, "left")["name"] = "renamed" })
	e := evEvent(t, s, c)
	next := evApply(t, s, e.JSON(), c.Tombstones())
	if next.revision != "13" {
		t.Fatal(next.revision)
	}
	evEqual(t, next.definition.AsJSON(), c.candidate.AsJSON())
	evEqual(t, c.FactsDigest(), txHash(t, c.Facts(), "contexture.facts/v1"))
	wire := e.JSON().(map[string]any)
	wire["reason"] = "mutated"
	wire["resolved_facts"].([]any)[0].(map[string]any)["after"] = nil
	c.Facts().([]any)[0].(map[string]any)["node_id"] = "mutated"
	copy := c.Candidate()
	copy = GraphDefinition{}
	_ = copy
	evApply(t, s, e.JSON(), c.Tombstones())
	if s.definition.nodes["left"].name != "left" {
		t.Fatal("base mutated")
	}
	if _, err := ApplyFacts(s, c.Facts(), ApplyFactsOptions{Tombstones: c.Tombstones(), Revision: "13", BeforeDigest: pureDigest(s.definition.AsJSON(), "contexture.graph/v1"), AfterDigest: pureDigest(c.candidate.AsJSON(), "contexture.graph/v1"), ResolvedFactDigest: c.FactsDigest()}); err != nil {
		t.Fatal(err)
	}
}
func TestResolvedEventsBootstrapNoopAndRecords(t *testing.T) {
	g := txGraph(t, nil, nil)
	u, err := NewUnseededState("native")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := OwnBootstrap(g)
	if err != nil {
		t.Fatal(err)
	}
	*g = GraphDefinition{}
	seed := owned.Definition()
	e, err := BootstrapEvent(u, &seed, evOptions(), "approval")
	if err != nil {
		t.Fatal(err)
	}
	s := evApply(t, u, e.JSON(), []any{})
	c, err := ResolveFacts(s, &s.definition)
	if err != nil {
		t.Fatal(err)
	}
	noop := evEvent(t, s, c)
	same := evApply(t, s, noop.JSON(), []any{})
	if same.revision != "1" || !c.Noop() {
		t.Fatal("no-op advanced")
	}
	records := []any{pureMap{"journal_seq": "7", "event": e.JSON(), "tombstones": []any{}}, pureMap{"journal_seq": "10", "event": noop.JSON(), "tombstones": []any{}}}
	b, err := CanonicalJSON(records)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyEventRecords(u, b, ApplyRecordsOptions{AfterSeq: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if result.JournalSeq != "10" || result.State.revision != "1" {
		t.Fatal(result)
	}
	decoded, err := DecodeEventRecords(b)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0].Event().(map[string]any)["reason"] = "mutated"
	if decoded[0].Event().(map[string]any)["reason"] != evOptions().Reason {
		t.Fatal("record aliases")
	}
	bad := mustFreeze(records).JSON().([]any)
	bad[1].(map[string]any)["journal_seq"] = "7"
	if _, err := ApplyEventRecords(u, bad, ApplyRecordsOptions{}); err == nil {
		t.Fatal("duplicate sequence accepted")
	}
	if _, err := ApplyEventRecords(u, []any{}, ApplyRecordsOptions{}); err == nil {
		t.Fatal("empty batch accepted")
	}
	if _, err := ApplyEvent(s, e.JSON(), ApplyEventOptions{Tombstones: []any{}}); err == nil {
		t.Fatal("second bootstrap accepted")
	}
	if _, err := ApplyEvent(u, noop.JSON(), ApplyEventOptions{Tombstones: []any{}}); err == nil {
		t.Fatal("unseeded no-op accepted")
	}
	if _, err := ApplyEvent(u, e.JSON(), ApplyEventOptions{Tombstones: []any{pureMap{}}}); err == nil {
		t.Fatal("bootstrap tombstones accepted")
	}
}
func TestResolvedEventsStrictDecodersAndTampering(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) { evNode(w, "left")["description"] = "new" })
	event := evEvent(t, s, c)
	mutators := map[string]func(map[string]any){
		"extra":        func(e map[string]any) { e["unknown"] = true },
		"unknown":      func(e map[string]any) { e["type"] = "FutureRequired" },
		"version":      func(e map[string]any) { e["semantic_version"] = "2" },
		"revision":     func(e map[string]any) { e["result_revision"] = "14" },
		"stale":        func(e map[string]any) { e["before_digest"] = evOptions().CommandDigest },
		"after":        func(e map[string]any) { e["after_digest"] = evOptions().CommandDigest },
		"command":      func(e map[string]any) { e["command_digest"] = "sha256:" + strings.Repeat("A", 64) },
		"empty":        func(e map[string]any) { e["resolved_facts"] = []any{} },
		"reason":       func(e map[string]any) { e["reason"] = "" },
		"graph":        func(e map[string]any) { e["graph_id"] = "other" },
		"extra-fact":   func(e map[string]any) { arr(e["resolved_facts"])[0].(map[string]any)["unknown"] = true },
		"unknown-fact": func(e map[string]any) { arr(e["resolved_facts"])[0].(map[string]any)["type"] = "FutureFact" },
		"before": func(e map[string]any) {
			arr(e["resolved_facts"])[0].(map[string]any)["before"].(map[string]any)["description"] = pureMap{"present": true, "value": "false precondition"}
		},
		"duplicate": func(e map[string]any) {
			e["resolved_facts"] = append(arr(e["resolved_facts"]), arr(e["resolved_facts"])[0])
		},
		"optional": func(e map[string]any) {
			arr(e["resolved_facts"])[0].(map[string]any)["after"].(map[string]any)["description"] = pureMap{"present": false, "value": nil}
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			wire := event.JSON().(map[string]any)
			mutate(wire)
			if _, err := ApplyEvent(s, wire, ApplyEventOptions{Tombstones: c.Tombstones()}); err == nil {
				t.Fatal("corruption accepted")
			}
			evApply(t, s, event.JSON(), c.Tombstones())
		})
	}
	for _, data := range []string{`{"type":"CommandCompleted","type":"CommandCompleted"}`, `[]`, `null`, `{"type":"FutureRequired"}`} {
		if _, err := DecodeGraphEvent([]byte(data)); err == nil {
			t.Fatal("bad bytes accepted", data)
		}
	}
	if _, err := ApplyFacts(s, c.Facts(), ApplyFactsOptions{Tombstones: []any{}, Revision: "13", BeforeDigest: pureDigest(s.definition.AsJSON(), "contexture.graph/v1"), AfterDigest: pureDigest(c.candidate.AsJSON(), "contexture.graph/v1"), ResolvedFactDigest: evOptions().CommandDigest}); err == nil {
		t.Fatal("bad fact digest accepted")
	}
	if _, err := ResolveFacts(RevisionState{}, &s.definition); err == nil {
		t.Fatal("zero state accepted")
	}
	if _, err := DecodeEventRecords([]any{pureMap{"journal_seq": "0", "event": event.JSON(), "tombstones": []any{}}}); err == nil {
		t.Fatal("zero seq accepted")
	}
}
func TestResolvedEventsDeleteRestoreAndMigration(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) {
		evNode(w, "hub")["members"].(map[string]any)["children"] = []any{"right"}
		nodes := []any{}
		for _, n := range arr(w["nodes"]) {
			if n.(map[string]any)["node_id"] != "left" {
				nodes = append(nodes, n)
			}
		}
		w["nodes"] = nodes
	})
	deleted := evApply(t, s, evEvent(t, s, c).JSON(), c.Tombstones())
	restored, err := ResolveFacts(deleted, &s.definition)
	if err != nil {
		t.Fatal(err)
	}
	if arr(restored.Facts())[0].(map[string]any)["type"] != "NodeRestored" {
		t.Fatal("missing restoration evidence")
	}
	live := evApply(t, deleted, evEvent(t, deleted, restored).JSON(), restored.Tombstones())
	evEqual(t, live.Tombstones(), c.Tombstones())
	wire := live.definition.AsJSON().(map[string]any)
	evNode(wire, "left")["mutable"] = true
	candidate, err := NewGraphDefinition(wire, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFacts(live, candidate); err == nil {
		t.Fatal("ordinary policy rewrite accepted")
	}
	d, err := DefinitionDiffBetween(&live.definition, candidate)
	if err != nil {
		t.Fatal(err)
	}
	mapping := pureMap{"retained_ids": []any{"hub", "left", "right"}, "new_ids": []any{}, "deleted_ids": []any{}}
	options := MigrationOptions{"migration", d.AsJSON(), mapping}
	migration, err := ResolveMigration(live, candidate, options)
	if err != nil {
		t.Fatal(err)
	}
	p := pureMap{"kind": "migration", "approval_ref": "approval", "source_digest": pureDigest(live.definition.AsJSON(), "contexture.graph/v1"), "migration_id": "migration", "exact_diff": d.AsJSON()}
	eo := evOptions()
	eo.Migration = p
	e, err := ChangeEvent(live, migration, eo)
	if err != nil {
		t.Fatal(err)
	}
	migrated := evApply(t, live, e.JSON(), migration.Tombstones())
	if arr(migrated.Tombstones())[0].(map[string]any)["mutable"] != true {
		t.Fatal("retained policy not migrated")
	}
	if _, err := ChangeEvent(live, migration, evOptions()); err == nil {
		t.Fatal("missing provenance accepted")
	}
	options.SourceMapping = pureMap{}
	if _, err := ResolveMigration(live, candidate, options); err == nil {
		t.Fatal("wrong mapping accepted")
	}
	options.SourceMapping = mapping
	options.ExactDiff = pureMap{}
	if _, err := ResolveMigration(live, candidate, options); err == nil {
		t.Fatal("wrong diff accepted")
	}
	bad := deleted.definition.AsJSON().(map[string]any)
	bad["nodes"] = append(arr(bad["nodes"]), evNode(wire, "left"))
	evNode(bad, "hub")["members"].(map[string]any)["children"] = []any{"left", "right"}
	g, err := NewGraphDefinition(bad, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFacts(deleted, g); err == nil {
		t.Fatal("restore changed policy accepted")
	}
}
func TestResolvedEventsOrderingPermutationAndLegacy(t *testing.T) {
	g := txGraph(t, nil, pureMap{"publications": []any{pureMap{"publication_id": "p1", "kind": "prompt", "name": "one", "description": "one", "opens_id": "left", "model_may_open": true}, pureMap{"publication_id": "p2", "kind": "prompt", "name": "two", "description": "two", "opens_id": "right", "model_may_open": true}}, "presentations": pureMap{"default_locale": "en", "catalogs": []any{pureMap{"locale": "en", "entries": []any{pureMap{"node_id": "left", "title": "Old"}}}}}})
	s := txState(t, g, "12", nil)
	c := evChange(t, s, func(w map[string]any) {
		p := arr(w["publications"])
		w["publications"] = []any{p[1], p[0]}
		evNode(w, "left")["name"] = "renamed"
		evNode(w, "hub")["uses"] = []any{"left"}
	})
	e := evEvent(t, s, c)
	evApply(t, s, e.JSON(), c.Tombstones())
	bad := e.JSON().(map[string]any)
	facts := arr(bad["resolved_facts"])
	facts[0], facts[1] = facts[1], facts[0]
	if _, err := ApplyEvent(s, bad, ApplyEventOptions{Tombstones: c.Tombstones()}); err == nil {
		t.Fatal("unordered facts accepted")
	}
	bad = e.JSON().(map[string]any)
	for _, f := range arr(bad["resolved_facts"]) {
		if f.(map[string]any)["type"] == "PublicationPut" {
			f.(map[string]any)["position"] = 0
		}
	}
	if _, err := ApplyEvent(s, bad, ApplyEventOptions{Tombstones: c.Tombstones()}); err == nil {
		t.Fatal("duplicate positions accepted")
	}
	c = evChange(t, s, func(w map[string]any) {
		p := w["presentations"].(map[string]any)
		arr(arr(p["catalogs"])[0].(map[string]any)["entries"])[0].(map[string]any)["title"] = "New"
	})
	e = evEvent(t, s, c)
	legacy := e.JSON().(map[string]any)
	legacy["resolved_facts"] = eventLegacyFacts(&s.definition, &c.candidate, arr(c.Facts()))
	evApply(t, s, legacy, c.Tombstones())
	if _, err := ApplyEvent(s, e.JSON(), ApplyEventOptions{Tombstones: []any{pureMap{"node_id": "past", "kind": "role", "mutable": true, "deleted_revision": "1"}}}); err == nil {
		t.Fatal("extra tombstone accepted")
	}
}
func TestResolvedEventsConcurrentDefensiveReaders(t *testing.T) {
	s := txState(t, nil, "12", nil)
	c := evChange(t, s, func(w map[string]any) { evNode(w, "left")["name"] = "new" })
	e := evEvent(t, s, c)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				e.JSON().(map[string]any)["reason"] = "local"
				c.Facts().([]any)[0].(map[string]any)["node_id"] = "local"
				if _, err := ApplyEvent(s, e.JSON(), ApplyEventOptions{Tombstones: c.Tombstones()}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

// Optional external differential evidence: each endpoint is parsed and every
// fact/event/diff is generated by native Go before byte comparison. The normal
// suite above remains self-contained when external evidence is unavailable.
func TestResolvedEventsNativeDifferential(t *testing.T) {
	path := os.Getenv("CONTEXTURE_EVENTS_DIFFERENTIAL")
	if path == "" {
		t.Log("external differential not requested; self-contained native scenarios run separately")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	root := input.(map[string]any)
	seed, err := NewGraphDefinition(root["seed"], nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := NewUnseededState(seed.graphID)
	if err != nil {
		t.Fatal(err)
	}
	expectedBootstrap := root["bootstrap"].(map[string]any)
	p := expectedBootstrap["migration"].(map[string]any)
	bootstrap, err := BootstrapEvent(u, seed, EventOptions{CommandDigest: expectedBootstrap["command_digest"].(string), Reason: expectedBootstrap["reason"].(string)}, p["approval_ref"].(string))
	if err != nil {
		t.Fatal(err)
	}
	evEqual(t, bootstrap.JSON(), root["bootstrap"])
	evApply(t, u, bootstrap.JSON(), []any{})
	outputs := []any{}
	for i, raw := range arr(root["rows"]) {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			row := raw.(map[string]any)
			before, err := NewGraphDefinition(row["before"], nil)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := NewGraphDefinition(row["candidate"], nil)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewRevisionState(before, row["revision"].(string), row["retained"])
			if err != nil {
				t.Fatal(err)
			}
			c, err := ResolveFacts(s, candidate)
			if err != nil {
				t.Fatal(err)
			}
			evEqual(t, c.Facts(), row["facts"])
			evEqual(t, c.FactsDigest(), row["facts_digest"])
			evEqual(t, c.Tombstones(), row["tombstones"])
			wantEvent := row["event"].(map[string]any)
			reason := "no-op"
			if r, ok := wantEvent["reason"]; ok {
				reason = r.(string)
			}
			e, err := ChangeEvent(s, c, EventOptions{CommandDigest: wantEvent["command_digest"].(string), Reason: reason})
			if err != nil {
				t.Fatal(err)
			}
			evEqual(t, e.JSON(), row["event"])
			d, err := DefinitionDiffBetween(before, candidate)
			if err != nil {
				t.Fatal(err)
			}
			evEqual(t, d.AsJSON(), row["diff"])
			after := evApply(t, s, e.JSON(), c.Tombstones())
			evEqual(t, after.revision, row["after_revision"])
			evEqual(t, pureDigest(after.definition.AsJSON(), "contexture.graph/v1"), row["after_digest"])
			outputs = append(outputs, pureMap{"facts": c.Facts(), "facts_digest": c.FactsDigest(), "tombstones": c.Tombstones(), "event": e.JSON(), "diff": d.AsJSON(), "after_revision": after.revision, "after_digest": pureDigest(after.definition.AsJSON(), "contexture.graph/v1")})
		})
	}
	if len(outputs) != 5 {
		t.Fatal("expected five scenarios")
	}
	b, err := CanonicalJSON(pureMap{"bootstrap": bootstrap.JSON(), "rows": outputs})
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("CONTEXTURE_EVENTS_OUTPUT"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "native-execution.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("GO_NATIVE_FIVE_SCENARIOS_BOOTSTRAP_EXACT_PASS input_sha256=%x output_sha256=%x", sha256.Sum256(data), sha256.Sum256(b))
}
