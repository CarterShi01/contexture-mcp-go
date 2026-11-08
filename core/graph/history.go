package graph

// Persistent detached historical values. Retention and journal integrity are
// external; a retained checkpoint never invents earlier revisions.
import "github.com/benbjohnson/immutable"

type RevisionRecord struct {
	journalSeq string
	state      RevisionState
	event      FrozenJSON
	valid      bool
}

func (r RevisionRecord) JournalSeq() string   { return r.journalSeq }
func (r RevisionRecord) State() RevisionState { return r.state }
func (r RevisionRecord) Event() any           { return r.event.JSON() }
func NewRevisionRecord(seq string, state RevisionState, event any) (out RevisionRecord, err error) {
	defer catchModel(&err)
	eventState(state)
	if pureCounter(seq).Sign() == 0 {
		failModel("Historical sequence must be positive")
	}
	frozen := mustFreeze(nil)
	if event != nil {
		e, er := DecodeGraphEvent(event)
		if er != nil {
			return out, er
		}
		frozen = e
	}
	return RevisionRecord{seq, state, frozen, true}, nil
}

type History struct {
	records *immutable.List[RevisionRecord]
}

func (h History) Records() []RevisionRecord {
	if h.records == nil {
		return nil
	}
	out := make([]RevisionRecord, h.records.Len())
	for iterator := h.records.Iterator(); !iterator.Done(); {
		index, value := iterator.Next()
		out[index] = value
	}
	return out
}
func (h History) Head() RevisionState {
	if h.records == nil || h.records.Len() == 0 {
		return RevisionState{}
	}
	return h.records.Get(h.records.Len() - 1).state
}
func (h History) RetainedLowerBound() string {
	if h.records == nil || h.records.Len() == 0 {
		return ""
	}
	return h.records.Get(0).state.revision
}
func NewHistory(records []RevisionRecord) (out History, err error) {
	defer catchModel(&err)
	if len(records) == 0 {
		failModel("History requires accepted seed")
	}
	for i, r := range records {
		if !r.valid {
			failModel("History requires valid revision records")
		}
		eventState(r.state)
		if i > 0 {
			before := records[i-1]
			if pureCounter(r.journalSeq).Cmp(pureCounter(before.journalSeq)) <= 0 || r.state.definition.graphID != before.state.definition.graphID || r.event.value == nil {
				eventFail("Historical sequence/graph/event mismatch")
			}
		}
	}
	return History{immutable.NewList(records...)}, nil
}
func SeedHistory(state RevisionState, seq string) (History, error) {
	r, e := NewRevisionRecord(seq, state, nil)
	if e != nil {
		return History{}, e
	}
	return NewHistory([]RevisionRecord{r})
}
func BootstrapHistory(base UnseededState, event any, seq string, contracts map[string]PublicationContract) (History, error) {
	s, e := ApplyEvent(base, event, ApplyEventOptions{[]any{}, contracts})
	if e != nil {
		return History{}, e
	}
	r, e := NewRevisionRecord(seq, s, event)
	if e != nil {
		return History{}, e
	}
	return NewHistory([]RevisionRecord{r})
}
func (h History) Append(event any, seq string, options ApplyEventOptions) (out History, err error) {
	defer catchModel(&err)
	if h.records == nil || h.records.Len() == 0 {
		failModel("Invalid history")
	}
	if pureCounter(seq).Cmp(pureCounter(h.records.Get(h.records.Len()-1).journalSeq)) <= 0 {
		eventFail("Historical sequence must increase")
	}
	s, e := ApplyEvent(h.Head(), event, options)
	if e != nil {
		return out, e
	}
	r, e := NewRevisionRecord(seq, s, event)
	if e != nil {
		return out, e
	}
	if s.definition.graphID != h.Head().definition.graphID {
		eventFail("Historical graph mismatch")
	}
	return History{h.records.Append(r)}, nil
}
func (h History) At(revision string) (out RevisionState, err error) {
	defer catchModel(&err)
	pureCounter(revision)
	if h.records != nil {
		iterator := h.records.Iterator()
		iterator.Last()
		for !iterator.Done() {
			_, record := iterator.Prev()
			if record.state.revision == revision {
				return record.state, nil
			}
		}
	}
	pureFail("history_unavailable", "Revision outside retained verified history")
	return
}
func (h History) Diff(before, after string) (DefinitionDiff, error) {
	a, e := h.At(before)
	if e != nil {
		return DefinitionDiff{}, e
	}
	b, e := h.At(after)
	if e != nil {
		return DefinitionDiff{}, e
	}
	return DefinitionDiffBetween(&a.definition, &b.definition)
}
func (h History) matching(projection func(RevisionState) any) []RevisionRecord {
	out := []RevisionRecord{}
	records := h.Records()
	for i := 1; i < len(records); i++ {
		if !pureEqual(projection(records[i-1].state), projection(records[i].state)) {
			out = append(out, records[i])
		}
	}
	return out
}
func (h History) ForNode(id string) (out []RevisionRecord, err error) {
	defer catchModel(&err)
	ident(id)
	return h.matching(func(s RevisionState) any { return historyNodeProjection(&s.definition, id) }), nil
}
func (h History) ForPublication(id string) (out []RevisionRecord, err error) {
	defer catchModel(&err)
	ident(id)
	return h.matching(func(s RevisionState) any {
		pubs := arr(s.definition.AsJSON().(map[string]any)["publications"])
		out := []any{}
		for i, p := range pubs {
			if p.(map[string]any)["publication_id"] == id {
				out = append(out, []any{i, p})
			}
		}
		return out
	}), nil
}
func historyNodeProjection(g *GraphDefinition, id string) any {
	nodes := eventNodes(g)
	locations, pubs, entries := []any{}, []any{}, []any{}
	for i, r := range g.roots {
		if r.NodeID == id {
			locations = append(locations, []any{nil, i, string(r.Plane)})
		}
	}
	for _, n := range g.Nodes() {
		for _, p := range n.members.Placements() {
			if p.NodeID == id {
				locations = append(locations, []any{n.id, string(p.Slot), p.Ordinal})
			}
		}
	}
	wire := g.AsJSON().(map[string]any)
	for i, p := range arr(wire["publications"]) {
		if p.(map[string]any)["opens_id"] == id {
			pubs = append(pubs, []any{i, p})
		}
	}
	presentations := wire["presentations"].(map[string]any)
	for ci, c := range arr(presentations["catalogs"]) {
		catalog := c.(map[string]any)
		for ei, e := range arr(catalog["entries"]) {
			if e.(map[string]any)["node_id"] == id {
				entries = append(entries, []any{ci, catalog["locale"], ei, e})
			}
		}
	}
	return []any{nodes[id], locations, pubs, entries, presentations["default_locale"]}
}

type RevertOptions struct {
	NodeIDs              []string
	PublicationContracts map[string]PublicationContract
}
type RevertCandidate struct {
	baseRevision, targetRevision string
	change                       ResolvedChange
}

func (c RevertCandidate) BaseRevision() string   { return c.baseRevision }
func (c RevertCandidate) TargetRevision() string { return c.targetRevision }
func (c RevertCandidate) Change() ResolvedChange { return c.change }
func NewRevertCandidate(base, target RevisionState, options RevertOptions) (out RevertCandidate, err error) {
	defer catchModel(&err)
	eventState(base)
	eventState(target)
	if base.definition.graphID != target.definition.graphID {
		failModel("Revert target belongs to another graph")
	}
	if pureCounter(target.revision).Cmp(pureCounter(base.revision)) > 0 {
		pureFail("history_unavailable", "Revert requires retained past revision")
	}
	wire := target.definition.AsJSON().(map[string]any)
	if options.NodeIDs != nil {
		wire = base.definition.AsJSON().(map[string]any)
		nodes := eventNodes(&base.definition)
		historical := eventNodes(&target.definition)
		seen := map[string]bool{}
		for _, id := range options.NodeIDs {
			ident(id)
			if seen[id] {
				failModel("Duplicate revert scope")
			}
			seen[id] = true
			if n, ok := historical[id]; ok {
				nodes[id] = n
			} else if _, ok := nodes[id]; ok {
				delete(nodes, id)
			} else {
				pureFail("reference_not_found", "Revert identity absent in both revisions")
			}
		}
		ordered := []any{}
		for _, id := range eventKeys(nodes) {
			ordered = append(ordered, nodes[id])
		}
		wire["nodes"] = ordered
	}
	g, e := NewGraphDefinition(wire, options.PublicationContracts)
	if e != nil {
		return out, e
	}
	retained := map[string]bool{}
	for _, raw := range arr(base.tombstones.value) {
		retained[raw.(map[string]any)["node_id"].(string)] = true
	}
	for _, n := range g.Nodes() {
		if _, ok := base.definition.nodes[n.id]; !ok && !retained[n.id] {
			pureFail("history_unavailable", "Historical restoration requires retained tombstones")
		}
	}
	c, e := ResolveFacts(base, g)
	if e != nil {
		return out, e
	}
	return RevertCandidate{base.revision, target.revision, c}, nil
}
