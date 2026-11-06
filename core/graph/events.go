package graph

// Detached graph-local resolved facts. No admission, registry, handlers or I/O.
import (
	"math/big"
	"slices"
	"strings"
)

// RevisionState is a verified detached projection, never a serving lease.
type RevisionState = PureRevisionState

func NewRevisionState(g *GraphDefinition, revision string, tombstones any) (RevisionState, error) {
	return NewPureRevisionState(g, revision, tombstones)
}

type UnseededState struct {
	graphID string
	valid   bool
}

func NewUnseededState(id string) (out UnseededState, err error) {
	defer catchModel(&err)
	ident(id)
	return UnseededState{id, true}, nil
}
func (s UnseededState) GraphID() string          { return s.graphID }
func (s UnseededState) Revision() string         { return "0" }
func (s UnseededState) DefinitionDigest() string { return pureDigest(nil, "contexture.graph/v1") }
func (s UnseededState) Tombstones() any          { return []any{} }

type BootstrapCandidate struct {
	definition GraphDefinition
	valid      bool
}

func OwnBootstrap(seed *GraphDefinition) (out BootstrapCandidate, err error) {
	defer catchModel(&err)
	eventDefinition(seed)
	return BootstrapCandidate{*seed, true}, nil
}
func (b BootstrapCandidate) Definition() GraphDefinition { return b.definition }

type ResolvedChange struct {
	candidate                        GraphDefinition
	facts, tombstones, sourceMapping FrozenJSON
	migrationID                      string
	valid                            bool
}

func (c ResolvedChange) Candidate() GraphDefinition { return c.candidate }
func (c ResolvedChange) Facts() any                 { return c.facts.JSON() }
func (c ResolvedChange) Tombstones() any            { return c.tombstones.JSON() }
func (c ResolvedChange) SourceMapping() any         { return c.sourceMapping.JSON() }
func (c ResolvedChange) MigrationID() string        { return c.migrationID }
func (c ResolvedChange) Noop() bool                 { return c.valid && len(arr(c.facts.value)) == 0 }
func (c ResolvedChange) FactsDigest() string        { return pureDigest(c.facts.value, "contexture.facts/v1") }
func eventFail(message string)                      { pureFail("corrupt_facts", message) }
func eventObject(value any, required, optional string) map[string]any {
	m, ok := value.(map[string]any)
	if !ok {
		eventFail("Expected closed JSON object")
	}
	for _, k := range strings.Fields(required) {
		if _, ok := m[k]; !ok {
			eventFail("Missing required field")
		}
	}
	allowed := strings.Fields(required + " " + optional)
	for _, k := range eventKeys(m) {
		if !slices.Contains(allowed, k) {
			eventFail("Unknown object field")
		}
	}
	return m
}
func eventArray(value any) []any {
	a, ok := value.([]any)
	if !ok {
		eventFail("Expected array")
	}
	return a
}
func eventInput(value any) any {
	if b, ok := value.([]byte); ok {
		v, err := DecodeJSON(b)
		if err != nil {
			panic(err)
		}
		return v
	}
	return mustFreeze(value).JSON()
}
func eventDefinition(g *GraphDefinition) {
	if g == nil || !g.valid {
		eventFail("Expected validated graph definition")
	}
}
func eventState(s RevisionState) {
	if !s.valid || !s.definition.valid || pureCounter(s.revision).Sign() == 0 {
		eventFail("Expected nonzero validated revision state")
	}
}
func eventDigest(value any) string {
	s, ok := value.(string)
	if !ok || len(s) != 71 || !strings.HasPrefix(s, "sha256:") {
		eventFail("Invalid digest")
	}
	for _, c := range s[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			eventFail("Invalid digest")
		}
	}
	return s
}
func eventReason(v any) string {
	s, ok := v.(string)
	if !ok || s == "" {
		eventFail("Event reason must be nonempty text")
	}
	return s
}
func eventNext(revision string) string {
	n := pureCounter(revision)
	n.Add(n, big.NewInt(1))
	if n.Cmp(pureCounter(MaxCounter)) > 0 {
		pureFail("limit_exceeded", "Revision exhausted")
	}
	return n.String()
}
func eventUnwrap(v any) (bool, any) {
	m := eventObject(v, "present", "value")
	present, ok := m["present"].(bool)
	value, has := m["value"]
	if !ok || present != has {
		eventFail("Invalid OptionalValue")
	}
	return present, value
}
func FactsDigest(facts any) (out string, err error) {
	defer catchModel(&err)
	v := eventInput(facts)
	eventArray(v)
	return pureDigest(v, "contexture.facts/v1"), nil
}
func eventRetained(value any, revision string) []any {
	m := map[string]any{}
	for _, raw := range eventArray(value) {
		t := eventObject(raw, "node_id kind mutable deleted_revision", "")
		id := ident(t["node_id"])
		n := pureCounter(t["deleted_revision"])
		if _, ok := m[id]; ok {
			eventFail("Duplicate tombstone identity")
		}
		if n.Sign() == 0 || n.Cmp(pureCounter(revision)) > 0 || !slices.Contains([]any{"role", "skill", "tool"}, t["kind"]) {
			eventFail("Invalid retained identity evidence")
		}
		if _, ok := t["mutable"].(bool); !ok && t["mutable"] != "inherit" {
			eventFail("Invalid retained creation policy")
		}
		m[id] = t
	}
	out := []any{}
	for _, id := range eventKeys(m) {
		out = append(out, m[id])
	}
	return out
}
func ResolveFacts(base RevisionState, candidate *GraphDefinition) (out ResolvedChange, err error) {
	defer catchModel(&err)
	return resolveEventFacts(base, candidate, "", nil), nil
}

type MigrationOptions struct {
	MigrationID              string
	ExactDiff, SourceMapping any
}

func ResolveMigration(base RevisionState, candidate *GraphDefinition, options MigrationOptions) (out ResolvedChange, err error) {
	defer catchModel(&err)
	eventState(base)
	eventDefinition(candidate)
	ident(options.MigrationID)
	d, e := DefinitionDiffBetween(&base.definition, candidate)
	if e != nil {
		panic(e)
	}
	if !pureEqual(options.ExactDiff, d.AsJSON()) {
		eventFail("Migration exact diff mismatch")
	}
	a, b := eventNodes(&base.definition), eventNodes(candidate)
	retained, added, deleted := []any{}, []any{}, []any{}
	for _, id := range unionKeys(a, b) {
		_, x := a[id]
		_, y := b[id]
		if x && y {
			retained = append(retained, id)
		} else if y {
			added = append(added, id)
		} else {
			deleted = append(deleted, id)
		}
	}
	mapping := map[string]any{"retained_ids": retained, "new_ids": added, "deleted_ids": deleted}
	if !pureEqual(options.SourceMapping, mapping) {
		eventFail("Migration source mapping mismatch")
	}
	return resolveEventFacts(base, candidate, options.MigrationID, mapping), nil
}
func resolveEventFacts(base RevisionState, candidate *GraphDefinition, migrationID string, mapping any) ResolvedChange {
	eventState(base)
	eventDefinition(candidate)
	a, b := base.definition.AsJSON().(map[string]any), candidate.AsJSON().(map[string]any)
	for _, k := range []string{"schema", "semantic_version", "graph_id"} {
		if !pureEqual(a[k], b[k]) {
			pureFail("unsupported_contract", "Unsupported graph identity/schema transition")
		}
	}
	// Use the shared pure endpoint normalizer after separately enforcing identity
	// invariants. It performs no command admission or policy evaluation.
	for _, n := range candidate.Nodes() {
		old, exists := base.definition.nodes[n.id]
		if exists {
			x, y := old.AsJSON().(map[string]any), n.AsJSON().(map[string]any)
			for _, k := range []string{"kind", "role_variant"} {
				if !pureEqual(eventOptional(x, k), eventOptional(y, k)) {
					eventFail("Cannot rewrite identity kind/variant")
				}
			}
			if !pureEqual(x["mutable"], y["mutable"]) && migrationID == "" {
				eventFail("Ordinary facts cannot change creation policy")
			}
		} else {
			for _, raw := range arr(base.tombstones.value) {
				t := raw.(map[string]any)
				wire := n.AsJSON().(map[string]any)
				if t["node_id"] == n.id && (!pureEqual(t["kind"], wire["kind"]) || !pureEqual(t["mutable"], wire["mutable"])) {
					eventFail("Restoration changes retained policy")
				}
			}
		}
	}
	frozen, retained := resolvePureFacts(base, candidate)
	facts, tombstones := arr(frozen.JSON()), arr(retained.JSON())
	if migrationID != "" {
		// Policy changes precede fields for each common identity.
		policies := map[string]any{}
		for _, n := range candidate.Nodes() {
			if old, ok := base.definition.nodes[n.id]; ok {
				x, y := old.AsJSON().(map[string]any), n.AsJSON().(map[string]any)
				if !pureEqual(x["mutable"], y["mutable"]) {
					policies[n.id] = map[string]any{"type": "CreationPolicyMigrated", "node_id": n.id, "before": x["mutable"], "after": y["mutable"], "migration_id": migrationID}
					for _, raw := range tombstones {
						t := raw.(map[string]any)
						if t["node_id"] == n.id {
							t["mutable"] = y["mutable"]
						}
					}
				}
			}
		}
		prefix, suffix := []any{}, []any{}
		i := 0
		for i < len(facts) {
			tag := facts[i].(map[string]any)["type"]
			if tag != "NodeCreated" && tag != "NodeRestored" {
				break
			}
			prefix = append(prefix, facts[i])
			i++
		}
		fields := map[string]any{}
		for i < len(facts) && facts[i].(map[string]any)["type"] == "NodeFieldsSet" {
			f := facts[i].(map[string]any)
			fields[f["node_id"].(string)] = f
			i++
		}
		for _, id := range unionKeys(policies, fields) {
			if f, ok := policies[id]; ok {
				prefix = append(prefix, f)
			}
			if f, ok := fields[id]; ok {
				prefix = append(prefix, f)
			}
		}
		suffix = append(suffix, facts[i:]...)
		facts = append(prefix, suffix...)
		if len(facts) != 0 {
			facts = append([]any{map[string]any{"type": "SemanticMigrated", "before": a["semantic_version"], "after": b["semantic_version"], "migration_id": migrationID, "source_mapping": mapping}}, facts...)
		}
	}
	return ResolvedChange{*candidate, mustFreeze(facts), mustFreeze(tombstones), mustFreeze(mapping), migrationID, true}
}
func eventProvenance(value any, kind, source string) map[string]any {
	required := "kind approval_ref source_digest"
	if kind == "migration" {
		required += " migration_id exact_diff"
	}
	p := eventObject(value, required, "")
	if p["kind"] != kind || eventDigest(p["source_digest"]) != source {
		eventFail("Provenance kind/source mismatch")
	}
	ident(p["approval_ref"])
	if kind == "migration" {
		ident(p["migration_id"])
	}
	return p
}

type EventOptions struct {
	CommandDigest, Reason string
	Migration             any
}

func BootstrapEvent(base UnseededState, seed *GraphDefinition, options EventOptions, approvalRef string) (out FrozenJSON, err error) {
	defer catchModel(&err)
	eventDefinition(seed)
	if !base.valid || seed.graphID != base.graphID || options.Migration != nil {
		eventFail("Invalid bootstrap endpoint/provenance")
	}
	ident(approvalRef)
	return mustFreeze(map[string]any{"type": "GraphChangeCommitted", "graph_id": base.graphID, "semantic_version": "1", "base_revision": "0", "result_revision": "1", "before_digest": base.DefinitionDigest(), "after_digest": pureDigest(seed.AsJSON(), "contexture.graph/v1"), "resolved_facts": []any{map[string]any{"type": "GraphSeeded", "before": nil, "after": seed.AsJSON()}}, "command_digest": eventDigest(options.CommandDigest), "reason": eventReason(options.Reason), "migration": map[string]any{"kind": "bootstrap", "approval_ref": approvalRef, "source_digest": base.DefinitionDigest()}}), nil
}
func ChangeEvent(base RevisionState, change ResolvedChange, options EventOptions) (out FrozenJSON, err error) {
	defer catchModel(&err)
	eventState(base)
	if !change.valid {
		eventFail("Invalid resolved change")
	}
	eventDigest(options.CommandDigest)
	eventReason(options.Reason)
	var expected ResolvedChange
	if change.migrationID != "" {
		p := eventProvenance(eventInput(options.Migration), "migration", pureDigest(base.definition.AsJSON(), "contexture.graph/v1"))
		if p["migration_id"] != change.migrationID {
			eventFail("Migration ID mismatch")
		}
		expected, err = ResolveMigration(base, &change.candidate, MigrationOptions{change.migrationID, p["exact_diff"], change.SourceMapping()})
	} else {
		if options.Migration != nil {
			eventFail("Ordinary change carries provenance")
		}
		expected, err = ResolveFacts(base, &change.candidate)
	}
	if err != nil {
		return out, err
	}
	if !pureEqual(expected.Facts(), change.Facts()) || !pureEqual(expected.Tombstones(), change.Tombstones()) {
		eventFail("Change does not belong to base")
	}
	if change.Noop() {
		return mustFreeze(map[string]any{"type": "CommandCompleted", "graph_id": base.definition.graphID, "revision": base.revision, "command_digest": options.CommandDigest, "status": "noop"}), nil
	}
	m := map[string]any{"type": "GraphChangeCommitted", "graph_id": base.definition.graphID, "semantic_version": "1", "base_revision": base.revision, "result_revision": eventNext(base.revision), "before_digest": pureDigest(base.definition.AsJSON(), "contexture.graph/v1"), "after_digest": pureDigest(change.candidate.AsJSON(), "contexture.graph/v1"), "resolved_facts": change.Facts(), "command_digest": options.CommandDigest, "reason": options.Reason}
	if options.Migration != nil {
		m["migration"] = options.Migration
	}
	return mustFreeze(m), nil
}

func DecodeResolvedFacts(value any) (out FrozenJSON, err error) {
	defer catchModel(&err)
	raw := eventArray(eventInput(value))
	shapes := map[string]string{"GraphSeeded": "type before after", "NodeCreated": "type node", "NodeRestored": "type node tombstone", "NodeFieldsSet": "type node_id before after", "GraphFieldsSet": "type before after", "RootsAssigned": "type before after", "MembersAssigned": "type node_id before after", "UsesAssigned": "type node_id before after", "PublicationPut": "type before after position", "PublicationRemoved": "type before position", "PresentationsAssigned": "type before after", "NodeDeleted": "type node_id before tombstone", "SemanticMigrated": "type before after migration_id source_mapping", "CreationPolicyMigrated": "type node_id before after migration_id", "PresentationPut": "type locale before after", "PresentationRemoved": "type locale before"}
	for _, raw := range raw {
		m, ok := raw.(map[string]any)
		if !ok {
			eventFail("Expected fact object")
		}
		tag, ok := m["type"].(string)
		if !ok || shapes[tag] == "" {
			pureFail("unsupported_contract", "Unknown required fact type")
		}
		f := eventObject(m, shapes[tag], "")
		for _, k := range []string{"node_id", "migration_id"} {
			if v, ok := f[k]; ok {
				ident(v)
			}
		}
		if v, ok := f["position"]; ok {
			purePosition(v, int(MaxSafeInteger))
		}
		if tag == "NodeFieldsSet" {
			for _, side := range []string{"before", "after"} {
				m := eventObject(f[side], "", "name description instructions instruction_template extensions binding input_contract")
				for _, k := range eventKeys(m) {
					eventUnwrap(m[k])
				}
			}
		}
		if tag == "GraphFieldsSet" {
			for _, side := range []string{"before", "after"} {
				eventObject(f[side], "", "application_name mode extensions")
			}
		}
		if tag == "PublicationPut" || tag == "PresentationPut" {
			eventUnwrap(f["before"])
		}
	}
	return mustFreeze(raw), nil
}
func DecodeGraphEvent(value any) (out FrozenJSON, err error) {
	defer catchModel(&err)
	raw := eventInput(value)
	m, ok := raw.(map[string]any)
	if !ok {
		eventFail("Expected event object")
	}
	if m["type"] == "CommandCompleted" {
		e := eventObject(m, "type graph_id revision command_digest status", "")
		ident(e["graph_id"])
		if pureCounter(e["revision"]).Sign() == 0 || e["status"] != "noop" {
			eventFail("Invalid no-op event")
		}
		eventDigest(e["command_digest"])
		return mustFreeze(e), nil
	}
	if m["type"] != "GraphChangeCommitted" {
		pureFail("unsupported_contract", "Unknown required event type")
	}
	e := eventObject(m, "type graph_id semantic_version base_revision result_revision before_digest after_digest resolved_facts command_digest reason", "migration")
	ident(e["graph_id"])
	if e["semantic_version"] != "1" {
		pureFail("unsupported_contract", "Unsupported event version")
	}
	if pureCounter(e["result_revision"]).Cmp(new(big.Int).Add(pureCounter(e["base_revision"]), big.NewInt(1))) != 0 {
		eventFail("Invalid revision sequence")
	}
	for _, k := range []string{"before_digest", "after_digest", "command_digest"} {
		eventDigest(e[k])
	}
	eventReason(e["reason"])
	f, er := DecodeResolvedFacts(e["resolved_facts"])
	if er != nil {
		return out, er
	}
	if len(arr(f.value)) == 0 {
		eventFail("Graph change requires nonempty facts")
	}
	if p, ok := e["migration"]; ok {
		kind := "migration"
		if e["base_revision"] == "0" {
			kind = "bootstrap"
		}
		eventProvenance(p, kind, e["before_digest"].(string))
	}
	return mustFreeze(e), nil
}

type ApplyFactsOptions struct {
	Tombstones                                              any
	Revision, BeforeDigest, AfterDigest, ResolvedFactDigest string
	PublicationContracts                                    map[string]PublicationContract
	Migration                                               any
}

func ApplyFacts(base RevisionState, facts any, options ApplyFactsOptions) (out RevisionState, err error) {
	defer catchModel(&err)
	eventState(base)
	if options.Revision != eventNext(base.revision) {
		eventFail("Result revision must be next counter")
	}
	if eventDigest(options.BeforeDigest) != pureDigest(base.definition.AsJSON(), "contexture.graph/v1") {
		pureFail("revision_conflict", "Stale definition digest")
	}
	eventDigest(options.AfterDigest)
	decoded, er := DecodeResolvedFacts(facts)
	if er != nil {
		return out, er
	}
	raw := arr(decoded.JSON())
	if len(raw) == 0 {
		eventFail("No-op must not produce revision")
	}
	if options.ResolvedFactDigest != "" && eventDigest(options.ResolvedFactDigest) != pureDigest(raw, "contexture.facts/v1") {
		eventFail("Fact digest mismatch")
	}
	wire := base.definition.AsJSON().(map[string]any)
	nodes := eventNodes(&base.definition)
	pubs := eventByID(arr(wire["publications"]), "publication_id")
	positions := map[string]int{}
	for i, p := range arr(wire["publications"]) {
		positions[p.(map[string]any)["publication_id"].(string)] = i
	}
	presentations := wire["presentations"].(map[string]any)
	locales := eventByID(arr(presentations["catalogs"]), "locale")
	var assigned, mapping any
	migrationID := ""
	var provenance map[string]any
	if options.Migration != nil {
		provenance = eventProvenance(eventInput(options.Migration), "migration", options.BeforeDigest)
		migrationID = ident(provenance["migration_id"])
	}
	deleted := map[string]bool{}
	legacy := false
	for _, value := range raw {
		f := value.(map[string]any)
		tag := f["type"].(string)
		id, _ := f["node_id"].(string)
		node := func() map[string]any {
			v, ok := nodes[id]
			if !ok {
				eventFail("Missing fact target")
			}
			return v.(map[string]any)
		}
		switch tag {
		case "SemanticMigrated":
			if migrationID == "" || f["migration_id"] != migrationID {
				eventFail("Missing migration provenance")
			}
			mapping = f["source_mapping"]
		case "CreationPolicyMigrated":
			if migrationID == "" || f["migration_id"] != migrationID {
				eventFail("Invalid policy migration")
			}
			node()["mutable"] = f["after"]
		case "GraphFieldsSet":
			for _, k := range eventKeys(f["after"].(map[string]any)) {
				wire[k] = f["after"].(map[string]any)[k]
			}
		case "PresentationsAssigned":
			assigned = f["after"]
		case "NodeCreated", "NodeRestored":
			n := parseNode(f["node"])
			if _, ok := nodes[n.id]; ok {
				eventFail("Identity already exists")
			}
			nodes[n.id] = n.AsJSON()
		case "NodeFieldsSet":
			n := node()
			m := f["after"].(map[string]any)
			for _, k := range eventKeys(m) {
				present, value := eventUnwrap(m[k])
				if present {
					n[k] = value
				} else {
					delete(n, k)
				}
			}
		case "MembersAssigned", "UsesAssigned":
			k := "uses"
			if tag == "MembersAssigned" {
				k = "members"
			}
			node()[k] = f["after"]
		case "RootsAssigned":
			wire["roots"] = f["after"]
		case "NodeDeleted":
			node()
			if deleted[id] {
				eventFail("Duplicate deletion")
			}
			deleted[id] = true
		case "PublicationPut", "PublicationRemoved":
			key := "after"
			if tag == "PublicationRemoved" {
				key = "before"
			}
			p := eventObject(f[key], "publication_id", "kind name description opens_id model_may_open uri mime_type method path")
			id := ident(p["publication_id"])
			if tag == "PublicationPut" {
				pubs[id] = p
				positions[id] = purePosition(f["position"], int(MaxSafeInteger))
			} else {
				if _, ok := pubs[id]; !ok {
					eventFail("Missing publication removal")
				}
				delete(pubs, id)
				delete(positions, id)
			}
		case "PresentationPut", "PresentationRemoved":
			legacy = true
			locale, ok := f["locale"].(string)
			if !ok {
				eventFail("Invalid locale")
			}
			catalog, ok := locales[locale]
			if !ok {
				pureFail("unsupported_contract", "Legacy catalog creation unsupported")
			}
			c := catalog.(map[string]any)
			entries := arr(c["entries"])
			key := "after"
			if tag == "PresentationRemoved" {
				key = "before"
			}
			e := eventObject(f[key], "node_id", "title description")
			identity := ident(e["node_id"])
			at := -1
			for i, v := range entries {
				if v.(map[string]any)["node_id"] == identity {
					at = i
					break
				}
			}
			if tag == "PresentationRemoved" {
				if at < 0 {
					eventFail("Missing presentation removal")
				}
				entries = slices.Delete(entries, at, at+1)
			} else if at < 0 {
				entries = append(entries, e)
			} else {
				entries[at] = e
			}
			c["entries"] = entries
		default:
			pureFail("unsupported_contract", "Unsupported required fact type")
		}
	}
	ordered := []any{}
	for _, id := range eventKeys(nodes) {
		if !deleted[id] {
			ordered = append(ordered, nodes[id])
		}
	}
	wire["nodes"] = ordered
	ordered = make([]any, len(pubs))
	used := map[int]bool{}
	for _, id := range eventKeys(pubs) {
		at := positions[id]
		if at < 0 || at >= len(pubs) || used[at] {
			eventFail("Publication positions must form complete permutation")
		}
		used[at] = true
		ordered[at] = pubs[id]
	}
	wire["publications"] = ordered
	if assigned != nil {
		wire["presentations"] = assigned
	} else {
		wire["presentations"] = presentations
	}
	candidate, er := NewGraphDefinition(wire, options.PublicationContracts)
	if er != nil {
		return out, er
	}
	var expected ResolvedChange
	if migrationID == "" {
		expected, er = ResolveFacts(base, candidate)
	} else {
		expected, er = ResolveMigration(base, candidate, MigrationOptions{migrationID, provenance["exact_diff"], mapping})
	}
	if er != nil {
		return out, er
	}
	expectedFacts := arr(expected.Facts())
	if legacy {
		expectedFacts = eventLegacyFacts(&base.definition, candidate, expectedFacts)
	}
	if !pureEqual(raw, expectedFacts) {
		eventFail("Facts are not complete normalized base-to-candidate patch")
	}
	if !pureEqual(expected.Tombstones(), eventRetained(eventInput(options.Tombstones), options.Revision)) {
		eventFail("Retained tombstone mismatch")
	}
	if pureDigest(candidate.AsJSON(), "contexture.graph/v1") != options.AfterDigest {
		eventFail("Final digest mismatch")
	}
	return NewRevisionState(candidate, options.Revision, expected.Tombstones())
}

func eventLegacyFacts(base, candidate *GraphDefinition, facts []any) []any {
	a := base.AsJSON().(map[string]any)["presentations"].(map[string]any)
	b := candidate.AsJSON().(map[string]any)["presentations"].(map[string]any)
	ac, bc := arr(a["catalogs"]), arr(b["catalogs"])
	if a["default_locale"] != b["default_locale"] || len(ac) != len(bc) {
		eventFail("Ambiguous legacy catalog order/default")
	}
	legacy := []any{}
	for i, raw := range ac {
		x, y := raw.(map[string]any), bc[i].(map[string]any)
		if x["locale"] != y["locale"] {
			eventFail("Ambiguous legacy catalog order")
		}
		ae, be := eventByID(arr(x["entries"]), "node_id"), eventByID(arr(y["entries"]), "node_id")
		order, actual := []any{}, []any{}
		for _, e := range arr(x["entries"]) {
			id := e.(map[string]any)["node_id"].(string)
			if _, ok := be[id]; ok {
				order = append(order, id)
			}
		}
		for _, id := range eventKeys(be) {
			if _, ok := ae[id]; !ok {
				order = append(order, id)
			}
		}
		for _, e := range arr(y["entries"]) {
			actual = append(actual, e.(map[string]any)["node_id"])
		}
		if !pureEqual(order, actual) {
			eventFail("Ambiguous legacy entry order")
		}
		for _, id := range unionKeys(ae, be) {
			v, ok := be[id]
			if !ok {
				legacy = append(legacy, map[string]any{"type": "PresentationRemoved", "locale": x["locale"], "before": ae[id]})
			} else if old, exists := ae[id]; !exists || !pureEqual(old, v) {
				legacy = append(legacy, map[string]any{"type": "PresentationPut", "locale": x["locale"], "before": eventOptional(ae, id), "after": v})
			}
		}
	}
	out := []any{}
	for _, f := range facts {
		if f.(map[string]any)["type"] == "PresentationsAssigned" {
			out = append(out, legacy...)
		} else {
			out = append(out, f)
		}
	}
	return out
}

type ApplyEventOptions struct {
	Tombstones           any
	PublicationContracts map[string]PublicationContract
}

func ApplyEvent(base any, event any, options ApplyEventOptions) (out RevisionState, err error) {
	defer catchModel(&err)
	decoded, er := DecodeGraphEvent(event)
	if er != nil {
		return out, er
	}
	e := decoded.JSON().(map[string]any)
	if unseeded, ok := base.(UnseededState); ok {
		if !unseeded.valid {
			eventFail("Invalid unseeded identity")
		}
		eventObject(e, "type graph_id semantic_version base_revision result_revision before_digest after_digest resolved_facts command_digest reason migration", "")
		p := eventProvenance(e["migration"], "bootstrap", unseeded.DefinitionDigest())
		facts := arr(e["resolved_facts"])
		if len(facts) != 1 || !pureEqual(options.Tombstones, []any{}) {
			eventFail("Bootstrap requires one fact and no tombstones")
		}
		f := eventObject(facts[0], "type before after", "")
		if f["type"] != "GraphSeeded" || f["before"] != nil {
			eventFail("Invalid bootstrap fact")
		}
		g, er := NewGraphDefinition(f["after"], options.PublicationContracts)
		if er != nil {
			return out, er
		}
		expected, er := BootstrapEvent(unseeded, g, EventOptions{CommandDigest: e["command_digest"].(string), Reason: e["reason"].(string)}, ident(p["approval_ref"]))
		if er != nil {
			return out, er
		}
		if !pureEqual(expected.JSON(), e) {
			eventFail("Bootstrap not exact normalized transition")
		}
		return NewRevisionState(g, "1", []any{})
	}
	s, ok := base.(RevisionState)
	if !ok {
		eventFail("Expected accepted graph state")
	}
	eventState(s)
	if e["type"] == "CommandCompleted" {
		if e["graph_id"] != s.definition.graphID || e["revision"] != s.revision || !pureEqual(s.Tombstones(), options.Tombstones) {
			eventFail("No-op disagrees with base")
		}
		return s, nil
	}
	if e["graph_id"] != s.definition.graphID || e["base_revision"] != s.revision {
		eventFail("Event identity/revision mismatch")
	}
	return ApplyFacts(s, e["resolved_facts"], ApplyFactsOptions{Tombstones: options.Tombstones, Revision: e["result_revision"].(string), BeforeDigest: e["before_digest"].(string), AfterDigest: e["after_digest"].(string), PublicationContracts: options.PublicationContracts, Migration: e["migration"]})
}

type EventRecord struct {
	journalSeq        string
	event, tombstones FrozenJSON
}

func (r EventRecord) JournalSeq() string { return r.journalSeq }
func (r EventRecord) Event() any         { return r.event.JSON() }
func (r EventRecord) Tombstones() any    { return r.tombstones.JSON() }
func DecodeEventRecords(value any) (out []EventRecord, err error) {
	defer catchModel(&err)
	out = []EventRecord{}
	for _, raw := range eventArray(eventInput(value)) {
		r := eventObject(raw, "journal_seq event tombstones", "")
		if pureCounter(r["journal_seq"]).Sign() == 0 {
			eventFail("Record sequence must be positive")
		}
		e, er := DecodeGraphEvent(r["event"])
		if er != nil {
			return nil, er
		}
		m := e.value.(map[string]any)
		revision, ok := m["result_revision"]
		if !ok {
			revision = m["revision"]
		}
		ts := eventRetained(r["tombstones"], revision.(string))
		out = append(out, EventRecord{r["journal_seq"].(string), e, mustFreeze(ts)})
	}
	return out, nil
}

type ApplyRecordsOptions struct {
	AfterSeq             string
	PublicationContracts map[string]PublicationContract
}
type ReplayResult struct {
	State      RevisionState
	JournalSeq string
}

func ApplyEventRecords(base any, records any, options ApplyRecordsOptions) (out ReplayResult, err error) {
	defer catchModel(&err)
	seq := options.AfterSeq
	if seq == "" {
		seq = "0"
	}
	pureCounter(seq)
	decoded, er := DecodeEventRecords(records)
	if er != nil {
		return out, er
	}
	if len(decoded) == 0 {
		eventFail("Expected nonempty graph record batch")
	}
	state := base
	var next RevisionState
	for _, r := range decoded {
		if pureCounter(r.journalSeq).Cmp(pureCounter(seq)) <= 0 {
			eventFail("Record sequences must increase")
		}
		next, er = ApplyEvent(state, r.Event(), ApplyEventOptions{r.Tombstones(), options.PublicationContracts})
		if er != nil {
			return out, er
		}
		state, seq = next, r.journalSeq
	}
	return ReplayResult{next, seq}, nil
}
