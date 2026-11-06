package graph

// Static endpoint protection only. This package grants no Host authority and
// performs no storage, registry resolution, binding acquisition or execution.
import "slices"

type ProtectionReport struct {
	impact, references []string
	rootsChanged       bool
}

func (p ProtectionReport) Impact() []string           { return slices.Clone(p.impact) }
func (p ProtectionReport) ReferenceTargets() []string { return slices.Clone(p.references) }
func (p ProtectionReport) RootsChanged() bool         { return p.rootsChanged }
func (p ProtectionReport) AsJSON() any {
	return map[string]any{"impact": jsonIDs(p.impact), "reference_targets": jsonIDs(p.references), "roots_changed": p.rootsChanged}
}

func pureFail(code, message string) { panic(diagnostic(code, message, "")) }
func pureEqual(a, b any) bool {
	x, e := CanonicalJSON(a)
	if e != nil {
		panic(e)
	}
	y, e := CanonicalJSON(b)
	if e != nil {
		panic(e)
	}
	return string(x) == string(y)
}
func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// reordered returns both endpoints of inversions, ignoring ordinal shifts due
// solely to insertion/deletion. UTF-8 lexical ID order is Unicode scalar order.
func reordered(before, after []string) map[string]bool {
	ranks := map[string]int{}
	for i, id := range after {
		ranks[id] = i
	}
	common := []string{}
	for _, id := range before {
		if _, ok := ranks[id]; ok {
			common = append(common, id)
		}
	}
	out := map[string]bool{}
	maximum := -1
	for _, id := range common {
		rank := ranks[id]
		if rank < maximum {
			out[id] = true
		}
		maximum = max(maximum, rank)
	}
	minimum := len(after)
	for i := len(common) - 1; i >= 0; i-- {
		id := common[i]
		rank := ranks[id]
		if rank > minimum {
			out[id] = true
		}
		minimum = min(minimum, rank)
	}
	return out
}
func policySequence(x *GraphIndex, parent string) []string {
	out := []string{}
	if parent == "" {
		for _, r := range x.definition.roots {
			out = append(out, r.NodeID)
		}
	} else {
		for _, p := range x.definition.nodes[parent].members.Placements() {
			out = append(out, p.NodeID)
		}
	}
	return out
}
func policySurface(x *GraphIndex, id string) any {
	g := x.definition
	n := g.nodes[id]
	refs := n.Uses()
	for _, p := range n.members.Placements() {
		refs = append(refs, p.NodeID)
	}
	for _, p := range n.instructions.parts {
		if p.Kind == "NodeRef" {
			refs = append(refs, p.TargetID)
		}
	}
	cards := []any{}
	for _, target := range refs {
		v := g.nodes[target]
		path, err := x.Path(target)
		if err != nil {
			panic(err)
		}
		cards = append(cards, map[string]any{"node_id": target, "name": v.name, "path": path, "plane": string(x.locations[target].Plane), "description": v.description, "kind": string(v.kind)})
	}
	wire := g.data.value.(map[string]any)
	pubs, presentations := []any{}, []any{}
	for _, p := range arr(wire["publications"]) {
		if p.(map[string]any)["opens_id"] == id {
			pubs = append(pubs, p)
		}
	}
	for _, raw := range arr(wire["presentations"].(map[string]any)["catalogs"]) {
		c := raw.(map[string]any)
		for _, e := range arr(c["entries"]) {
			if e.(map[string]any)["node_id"] == id {
				presentations = append(presentations, map[string]any{"locale": c["locale"], "entry": e})
			}
		}
	}
	path, err := x.Path(id)
	if err != nil {
		panic(err)
	}
	return map[string]any{"node": n.AsJSON(), "path": path, "plane": string(x.locations[id].Plane), "default_locale": g.presentations.defaultLocale, "mode": g.mode, "graph_extensions": wire["extensions"], "cards": cards, "publications": pubs, "presentations": presentations}
}

// ProtectChange compares complete validated endpoints under static creation
// policy. Its impact/reference set is evidence for a Host, never authorization.
func ProtectChange(before, candidate *GraphDefinition, contracts map[string]PublicationContract) (out ProtectionReport, err error) {
	defer catchModel(&err)
	old, err := BuildGraphIndex(before, contracts)
	if err != nil {
		return out, err
	}
	next, err := BuildGraphIndex(candidate, contracts)
	if err != nil {
		return out, err
	}
	if before.graphID != candidate.graphID {
		pureFail("identity_conflict", "Ordinary change cannot cross graphs")
	}
	a, b := before.nodes, candidate.nodes
	locked, impact, refs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for id, loc := range old.locations {
		if !loc.EffectiveMutable {
			locked[id] = true
		}
	}
	all := map[string]bool{}
	for id := range a {
		all[id] = true
	}
	for id := range b {
		all[id] = true
	}
	for _, id := range sortedSet(all) {
		x, hasA := a[id]
		y, hasB := b[id]
		if !hasA || !hasB {
			impact[id] = true
			if !hasB && locked[id] {
				pureFail("mutation_protected", "Deletion closure contains locked objects")
			}
			if !hasA {
				p := next.locations[id]
				if p.HasParent && !next.locations[p.ParentID].EffectiveMutable {
					pureFail("mutation_protected", "Creation requires a mutable parent")
				}
			}
			continue
		}
		if x.kind != y.kind || x.mutable != y.mutable || x.variant != y.variant {
			pureFail("identity_conflict", "Identity kind, variant and creation policy are immutable")
		}
		p, q := old.locations[id], next.locations[id]
		moved := p.ParentID != q.ParentID || p.Slot != q.Slot
		changed := !pureEqual(x.AsJSON(), y.AsJSON()) || moved || p.Plane != q.Plane || !pureEqual(policySurface(old, id), policySurface(next, id))
		if changed {
			impact[id] = true
		}
		if changed && locked[id] {
			pureFail("mutation_protected", "Change alters a locked object, path, plane or surface")
		}
		if moved {
			if (p.HasParent && !old.locations[p.ParentID].EffectiveMutable) || (q.HasParent && !next.locations[q.ParentID].EffectiveMutable) {
				pureFail("mutation_protected", "Move requires mutable source and destination parents")
			}
		}
		if !slices.Equal(x.uses, y.uses) {
			for _, target := range append(x.Uses(), y.uses...) {
				refs[target] = true
			}
		}
	}
	checkOrder := func(left, right []string, barriers map[string]bool) map[string]bool {
		changes := reordered(left, right)
		for id := range changes {
			if barriers[id] {
				pureFail("mutation_protected", "Existing member crossed a locked member")
			}
		}
		return changes
	}
	parents := []string{""}
	for _, n := range before.Nodes() {
		parents = append(parents, n.id)
	}
	for _, parent := range parents {
		if _, ok := b[parent]; parent != "" && !ok {
			continue
		}
		for id := range checkOrder(policySequence(old, parent), policySequence(next, parent), locked) {
			impact[id] = true
		}
	}
	pseq := func(g *GraphDefinition) ([]string, map[string]string) {
		seq, targets := []string{}, map[string]string{}
		for _, p := range g.publications {
			seq = append(seq, p.PublicationID)
			targets[p.PublicationID] = p.OpensID
		}
		return seq, targets
	}
	left, ap := pseq(before)
	right, bp := pseq(candidate)
	barriers := map[string]bool{}
	for id, target := range ap {
		if locked[target] {
			barriers[id] = true
		}
	}
	for id := range checkOrder(left, right, barriers) {
		impact[ap[id]] = true
		impact[bp[id]] = true
	}
	cseq := func(g *GraphDefinition) ([]string, map[string][]string) {
		seq, entries := []string{}, map[string][]string{}
		for _, c := range g.presentations.catalogs {
			seq = append(seq, c.locale)
			entries[c.locale] = []string{}
			for _, e := range c.entries {
				entries[c.locale] = append(entries[c.locale], e.NodeID)
			}
		}
		return seq, entries
	}
	left, ac := cseq(before)
	right, bc := cseq(candidate)
	barriers = map[string]bool{}
	for _, locale := range left {
		if after, ok := bc[locale]; ok {
			for id := range checkOrder(ac[locale], after, locked) {
				impact[id] = true
			}
		}
		for _, id := range ac[locale] {
			if locked[id] {
				barriers[locale] = true
			}
		}
	}
	for locale := range checkOrder(left, right, barriers) {
		for _, id := range append(slices.Clone(ac[locale]), bc[locale]...) {
			impact[id] = true
		}
	}
	for _, g := range []*GraphDefinition{before, candidate} {
		for _, n := range g.Nodes() {
			if impact[n.id] {
				for _, id := range n.uses {
					refs[id] = true
				}
				for _, p := range n.instructions.parts {
					if p.Kind == "NodeRef" {
						refs[p.TargetID] = true
					}
				}
			}
		}
		for _, p := range g.publications {
			if impact[p.OpensID] {
				refs[p.OpensID] = true
			}
		}
	}
	out = ProtectionReport{sortedSet(impact), sortedSet(refs), !pureEqual(before.data.value.(map[string]any)["roots"], candidate.data.value.(map[string]any)["roots"])}
	return out, nil
}
