package graph

import "testing"

func TestPurePolicyDefaultRootAndFalseAncestry(t *testing.T) {
	s := txState(t, txGraph(t, []any{txRole("hub", nil)}, nil), "12", nil)
	txReject(t, s, "mutation_protected", txRename("hub", "new"))
	p := txPrepare(t, s, txRename("hub", "temporary"), txRename("hub", "hub"))
	if !p.Noop() {
		t.Fatal("endpoint noop rejected")
	}
	s = txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"barrier"}}}), txRole("barrier", pureMap{"mutable": false, "members": pureMap{"children": []any{"child"}}}), txRole("child", pureMap{"mutable": true})}, nil), "12", nil)
	txReject(t, s, "mutation_protected", txRename("child", "new"))
	txReject(t, s, "mutation_protected", txRename("hub", "new"))
	txReject(t, s, "mutation_protected", txMove("child", txMember("hub", "children", 1)))
}

func TestPurePolicyImmutableCreationIdentity(t *testing.T) {
	before := txGraph(t, []any{txRole("hub", pureMap{"mutable": true})}, nil)
	for _, fields := range []pureMap{{"mutable": false}, {"mutable": true, "role_variant": "post_process"}, {"mutable": true, "kind": "skill"}} {
		after := txGraph(t, []any{txRole("hub", fields)}, nil)
		_, err := ProtectChange(before, after, nil)
		txCode(t, err, "identity_conflict")
	}
	after := txGraph(t, []any{txRole("hub", pureMap{"mutable": true})}, pureMap{"graph_id": "other"})
	_, err := ProtectChange(before, after, nil)
	txCode(t, err, "identity_conflict")
}

func TestPurePolicyLockedOrderVersusInsertionShift(t *testing.T) {
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"free", "locked"}}}), txRole("free", nil), txRole("locked", pureMap{"mutable": false})}, nil)
	s := txState(t, g, "12", nil)
	txReject(t, s, "mutation_protected", pureMap{"op": "reorder_members", "parent_id": "hub", "slot": "children", "node_ids": []any{"locked", "free"}})
	p := txPrepare(t, s, pureMap{"op": "create_node", "node": txRole("inserted", nil)}, pureMap{"op": "attach_node", "node_id": "inserted", "target": txMember("hub", "children", 0)})
	txEqual(t, p.Protection().Impact(), []string{"hub", "inserted"})
	s = txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "right", "tail"}}}), txRole("left", nil), txRole("right", nil), txRole("tail", nil)}, nil), "12", nil)
	p = txPrepare(t, s, pureMap{"op": "reorder_members", "parent_id": "hub", "slot": "children", "node_ids": []any{"right", "left", "tail"}})
	txEqual(t, p.Protection().Impact(), []string{"hub", "left", "right"})
}

func TestPurePolicyParentSlotAndNewBarrier(t *testing.T) {
	s := txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true})}, nil), "12", nil)
	p := txPrepare(t, s, pureMap{"op": "create_node", "node": txRole("barrier", pureMap{"mutable": false})}, pureMap{"op": "attach_node", "node_id": "barrier", "target": txMember("hub", "children", 0)})
	txEqual(t, p.candidate.nodes["barrier"].mutable, MutableFalse)
	txReject(t, s, "mutation_protected", pureMap{"op": "create_node", "node": txRole("barrier", pureMap{"mutable": false})}, pureMap{"op": "create_node", "node": txRole("child", pureMap{"mutable": true})}, pureMap{"op": "attach_node", "node_id": "barrier", "target": txMember("hub", "children", 0)}, pureMap{"op": "attach_node", "node_id": "child", "target": txMember("barrier", "children", 0)})
	// An explicitly mutable child below a default-inherit locked parent cannot
	// escape through a different parent or through a new root placement.
	g := txGraph(t, []any{txRole("hub", pureMap{"members": pureMap{"children": []any{"free"}}}), txRole("free", pureMap{"mutable": true})}, nil)
	txReject(t, txState(t, g, "12", nil), "mutation_protected", txMove("free", pureMap{"plane": "prompt", "position": 1}))
	// Same-parent slot mutation is ownership change even with identical paths.
	g = txGraph(t, []any{txRole("hub", pureMap{"members": pureMap{"children": []any{"free"}}}), txRole("free", pureMap{"mutable": true, "role_variant": "pre_process"})}, nil)
	txReject(t, txState(t, g, "12", nil), "mutation_protected", txMove("free", txMember("hub", "pre_process", 0)))
}

func TestPurePolicyUsesAndNodeRefClosure(t *testing.T) {
	for _, template := range []bool{false, true} {
		locked := txRole("watcher", pureMap{"mutable": false, "uses": []any{"left"}})
		if template {
			delete(locked, "uses")
			delete(locked, "instructions")
			locked["instruction_template"] = pureMap{"parts": []any{pureMap{"kind": "NodeRef", "target_id": "left"}}}
		}
		g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "watcher"}}}), txRole("left", nil), locked}, nil)
		txReject(t, txState(t, g, "12", nil), "mutation_protected", txRename("left", "new"))
	}
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"branch", "external"}}}), txRole("branch", pureMap{"members": pureMap{"children": []any{"leaf"}}}), txRole("leaf", nil), txRole("external", pureMap{"uses": []any{"leaf"}})}, nil)
	p := txPrepare(t, txState(t, g, "12", nil), txRename("branch", "new"))
	txEqual(t, p.Protection().Impact(), []string{"branch", "external", "hub", "leaf"})
	txEqual(t, p.Protection().ReferenceTargets(), []string{"leaf"})
}

func TestPurePolicyRootReorderAndPlane(t *testing.T) {
	for _, locked := range []bool{false, true} {
		nodes := []any{txRole("hub", pureMap{"mutable": true}), txRole("other", pureMap{"mutable": !locked})}
		g := txGraph(t, nodes, pureMap{"roots": []any{pureMap{"node_id": "hub", "plane": "model"}, pureMap{"node_id": "other", "plane": "prompt"}}})
		s := txState(t, g, "12", nil)
		if locked {
			txReject(t, s, "mutation_protected", txMove("hub", pureMap{"plane": "model", "position": 1}))
			txReject(t, s, "mutation_protected", txMove("other", pureMap{"plane": "model", "position": 1}))
			continue
		}
		p := txPrepare(t, s, txMove("hub", pureMap{"plane": "model", "position": 1}))
		txEqual(t, p.Protection().Impact(), []string{"hub", "other"})
		if !p.RootOperations() || !p.Protection().RootsChanged() {
			t.Fatal("root order not reported")
		}
	}
}

func TestPurePolicyPublicationPresentationAndMetadata(t *testing.T) {
	nodes := []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"locked", "free"}}}), txRole("locked", pureMap{"mutable": false}), txRole("free", nil)}
	prompt := func(id string) any {
		return pureMap{"kind": "prompt", "publication_id": id, "name": id, "description": "prompt", "opens_id": id, "model_may_open": true}
	}
	before := txGraph(t, nodes, pureMap{"publications": []any{prompt("locked"), prompt("free")}})
	after := txGraph(t, nodes, pureMap{"publications": []any{prompt("free"), prompt("locked")}})
	_, err := ProtectChange(before, after, nil)
	txCode(t, err, "mutation_protected")
	catalog := func(locale string, entries ...any) any { return pureMap{"locale": locale, "entries": entries} }
	entry := func(id string) any { return pureMap{"node_id": id, "title": id} }
	for _, catalogOrder := range []bool{false, true} {
		var left, right []any
		if catalogOrder {
			left = []any{catalog("en", entry("locked")), catalog("fr", entry("free"))}
			right = []any{left[1], left[0]}
		} else {
			left = []any{catalog("en", entry("locked"), entry("free"))}
			right = []any{catalog("en", entry("free"), entry("locked"))}
		}
		before = txGraph(t, nodes, pureMap{"presentations": pureMap{"default_locale": "en", "catalogs": left}})
		after = txGraph(t, nodes, pureMap{"presentations": pureMap{"default_locale": "en", "catalogs": right}})
		_, err = ProtectChange(before, after, nil)
		txCode(t, err, "mutation_protected")
	}
	before = txGraph(t, nodes, nil)
	for _, extra := range []pureMap{{"extensions": pureMap{"test:meta": 1}}, {"presentations": pureMap{"default_locale": "fr", "catalogs": []any{}}}} {
		_, err = ProtectChange(before, txGraph(t, nodes, extra), nil)
		txCode(t, err, "mutation_protected")
	}
	_, err = ProtectChange(before, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"free"}}}), txRole("free", nil)}, nil), nil)
	txCode(t, err, "mutation_protected")
}

func TestPurePolicyOrderIndependentInversionOracle(t *testing.T) {
	// Exhaustively compare the linear production scan against a quadratic test
	// oracle, including inserted/removed identities and all 4! permutations.
	var permute func([]string, int)
	permute = func(values []string, at int) {
		if at < len(values) {
			for i := at; i < len(values); i++ {
				values[at], values[i] = values[i], values[at]
				permute(values, at+1)
				values[at], values[i] = values[i], values[at]
			}
			return
		}
		before := []string{"a", "b", "c", "d", "removed"}
		after := append([]string{"inserted"}, values...)
		expected := map[string]bool{}
		for i, x := range before {
			for _, y := range before[i+1:] {
				px, py := -1, -1
				for j, id := range after {
					if id == x {
						px = j
					}
					if id == y {
						py = j
					}
				}
				if px >= 0 && py >= 0 && px > py {
					expected[x] = true
					expected[y] = true
				}
			}
		}
		txEqual(t, reordered(before, after), expected)
	}
	permute([]string{"a", "b", "c", "d"}, 0)
}
