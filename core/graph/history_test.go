package graph

import "testing"

func TestResolvedHistoryEndpointsNoopAndRetention(t *testing.T) {
	s := txState(t, nil, "12", nil)
	h, err := SeedHistory(s, "20")
	if err != nil {
		t.Fatal(err)
	}
	c := evChange(t, s, func(w map[string]any) { evNode(w, "left")["description"] = "changed" })
	event := evEvent(t, s, c)
	next, err := h.Append(event.JSON(), "25", ApplyEventOptions{Tombstones: c.Tombstones()})
	if err != nil {
		t.Fatal(err)
	}
	if h.Head().revision != "12" || next.Head().revision != "13" || next.RetainedLowerBound() != "12" {
		t.Fatal("history mutated or wrong bounds")
	}
	noop, err := ResolveFacts(next.Head(), &next.records[1].state.definition)
	if err != nil {
		t.Fatal(err)
	}
	nooped, err := next.Append(evEvent(t, next.Head(), noop).JSON(), "27", ApplyEventOptions{Tombstones: []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nooped.Records()) != 3 || nooped.Head().revision != "13" {
		t.Fatal("no-op journal record lost")
	}
	got, err := nooped.At("13")
	if err != nil {
		t.Fatal(err)
	}
	evEqual(t, got.definition.AsJSON(), c.candidate.AsJSON())
	if _, err := nooped.At("11"); err == nil {
		t.Fatal("unretained past fabricated")
	}
	if _, err := nooped.At("14"); err == nil {
		t.Fatal("future fabricated")
	}
	d, err := nooped.Diff("12", "13")
	if err != nil || d.Empty() {
		t.Fatal("missing endpoint diff", err)
	}
	empty, err := nooped.Diff("13", "13")
	if err != nil || !empty.Empty() {
		t.Fatal("same endpoint diff", err)
	}
	records, err := nooped.ForNode("left")
	if err != nil || len(records) != 1 || records[0].journalSeq != "25" {
		t.Fatal(records, err)
	}
	records, err = nooped.ForNode("right")
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	copy := nooped.Records()
	copy[0] = RevisionRecord{}
	nooped.Records()[1].Event().(map[string]any)["reason"] = "mutated"
	if nooped.Records()[1].Event().(map[string]any)["reason"] != evOptions().Reason {
		t.Fatal("history aliases event")
	}
	checkpoint, err := SeedHistory(nooped.Head(), "100")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.RetainedLowerBound() != "13" {
		t.Fatal("wrong checkpoint bound")
	}
	if _, err := checkpoint.At("12"); err == nil {
		t.Fatal("checkpoint invented old history")
	}
	if _, err := next.Append(event.JSON(), "24", ApplyEventOptions{Tombstones: []any{}}); err == nil {
		t.Fatal("regressing sequence accepted")
	}
	if _, err := NewHistory([]RevisionRecord{}); err == nil {
		t.Fatal("empty history accepted")
	}
	if _, err := NewRevisionRecord("0", s, nil); err == nil {
		t.Fatal("zero historical seq")
	}
}
func TestResolvedHistoryBootstrapAndProjections(t *testing.T) {
	g := txGraph(t, nil, pureMap{"publications": []any{pureMap{"publication_id": "p", "kind": "prompt", "name": "prompt", "description": "prompt", "opens_id": "left", "model_may_open": true}}})
	u, err := NewUnseededState("native")
	if err != nil {
		t.Fatal(err)
	}
	e, err := BootstrapEvent(u, g, evOptions(), "approval")
	if err != nil {
		t.Fatal(err)
	}
	h, err := BootstrapHistory(u, e.JSON(), "3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.Records()[0].Event() == nil {
		t.Fatal("bootstrap event lost")
	}
	if _, err := h.At("0"); err == nil {
		t.Fatal("revision-zero serving graph invented")
	}
	s := h.Head()
	c := evChange(t, s, func(w map[string]any) {
		evNode(w, "hub")["members"].(map[string]any)["children"] = []any{"right", "left"}
		arr(w["publications"])[0].(map[string]any)["description"] = "new"
		w["presentations"] = pureMap{"default_locale": "en", "catalogs": []any{pureMap{"locale": "en", "entries": []any{pureMap{"node_id": "left", "title": "Left"}}}}}
	})
	h, err = h.Append(evEvent(t, s, c).JSON(), "8", ApplyEventOptions{Tombstones: c.Tombstones()})
	if err != nil {
		t.Fatal(err)
	}
	n, err := h.ForNode("left")
	if err != nil || len(n) != 1 {
		t.Fatal("node relation/publication/locale projection missing", err)
	}
	p, err := h.ForPublication("p")
	if err != nil || len(p) != 1 {
		t.Fatal("publication projection missing", err)
	}
	s = h.Head()
	c = evChange(t, s, func(w map[string]any) { w["presentations"].(map[string]any)["default_locale"] = "fr" })
	h, err = h.Append(evEvent(t, s, c).JSON(), "9", ApplyEventOptions{Tombstones: c.Tombstones()})
	if err != nil {
		t.Fatal(err)
	}
	n, err = h.ForNode("left")
	if err != nil || len(n) != 2 {
		t.Fatal("default locale projection missing", err)
	}
}
func TestResolvedHistoryRevertCandidatesAndClosure(t *testing.T) {
	past := txState(t, nil, "12", nil)
	c := evChange(t, past, func(w map[string]any) {
		evNode(w, "left")["name"] = "changed"
		evNode(w, "right")["description"] = "unrelated"
	})
	current := evApply(t, past, evEvent(t, past, c).JSON(), c.Tombstones())
	full, err := NewRevertCandidate(current, past, RevertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fullChange := full.Change()
	evEqual(t, fullChange.candidate.AsJSON(), past.definition.AsJSON())
	if full.BaseRevision() != "13" || full.TargetRevision() != "12" {
		t.Fatal("wrong revert endpoints")
	}
	scoped, err := NewRevertCandidate(current, past, RevertOptions{NodeIDs: []string{"left"}})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.change.candidate.nodes["right"].description != "unrelated" || scoped.change.candidate.nodes["left"].name != "left" {
		t.Fatal("scoped revert overwrote unrelated data")
	}
	empty, err := NewRevertCandidate(current, past, RevertOptions{NodeIDs: []string{}})
	if err != nil || !empty.change.Noop() {
		t.Fatal("empty scope became full revert", err)
	}
	for _, scope := range [][]string{{"left", "left"}, {"missing"}} {
		if _, err := NewRevertCandidate(current, past, RevertOptions{NodeIDs: scope}); err == nil {
			t.Fatal("bad revert scope accepted")
		}
	}
	if _, err := NewRevertCandidate(past, current, RevertOptions{}); err == nil {
		t.Fatal("future revert accepted")
	}
	deletedChange := evChange(t, past, func(w map[string]any) {
		evNode(w, "hub")["members"].(map[string]any)["children"] = []any{"right"}
		nodes := []any{}
		for _, n := range arr(w["nodes"]) {
			if n.(map[string]any)["node_id"] != "left" {
				nodes = append(nodes, n)
			}
		}
		w["nodes"] = nodes
	})
	deleted := evApply(t, past, evEvent(t, past, deletedChange).JSON(), deletedChange.Tombstones())
	if _, err := NewRevertCandidate(deleted, past, RevertOptions{NodeIDs: []string{"left"}}); err == nil {
		t.Fatal("incomplete restoration closure accepted")
	}
	restore, err := NewRevertCandidate(deleted, past, RevertOptions{NodeIDs: []string{"left", "hub"}})
	if err != nil {
		t.Fatal(err)
	}
	if arr(restore.change.Facts())[0].(map[string]any)["type"] != "NodeRestored" {
		t.Fatal("restore lost identity evidence")
	}
	without, err := NewRevisionState(&deleted.definition, "13", []any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRevertCandidate(without, past, RevertOptions{}); err == nil {
		t.Fatal("restoration without retained tombstone accepted")
	}
	if current.definition.nodes["left"].name != "changed" {
		t.Fatal("revert rewound accepted state")
	}
}
