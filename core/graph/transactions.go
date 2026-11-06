package graph

// Bounded ordinary PURE transaction candidate. No admission, clock, store,
// idempotent receipt, durable append, binding resolution or Host authorization.
import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"math/big"
	"slices"
)

func SupportedCommands() []string {
	return []string{"create_node", "update_node", "rename_node", "attach_node", "move_node", "detach_node", "reorder_members", "add_root", "remove_root", "add_use", "remove_use", "delete_nodes", "restore_nodes", "replace_subtree", "import", "reconcile", "put_publication", "remove_publication", "put_presentation", "remove_presentation"}
}
func UnsupportedCommands() []string {
	return []string{}
}

type TransactionLimits struct {
	MaxCommands, MaxTransactionBytes, MaxDefinitionBytes, MaxResolvedBytes int
	MaxNodes, MaxEdges, MaxDepth, MaxNodeBytes                             int
	MaxExpandedOperations                                                  int
}

func DefaultTransactionLimits() TransactionLimits {
	return TransactionLimits{10000, 16 * 1024 * 1024, 256 * 1024 * 1024, 64 * 1024 * 1024, 100000, 1000000, 1024, 1024 * 1024, 2000000}
}
func (l TransactionLimits) validate() {
	for _, n := range []int{l.MaxCommands, l.MaxTransactionBytes, l.MaxDefinitionBytes, l.MaxResolvedBytes, l.MaxNodes, l.MaxEdges, l.MaxDepth, l.MaxNodeBytes, l.MaxExpandedOperations} {
		if n <= 0 || uint64(n) > uint64(MaxSafeInteger) {
			failModel("Limits must be positive safe integers")
		}
	}
}

type PrepareOptions struct {
	Limits               *TransactionLimits
	PublicationContracts map[string]PublicationContract
	// Already acquired snapshots and immutable canonical payload bytes. No I/O
	// callbacks or authority are accepted by the pure kernel. Do not mutate
	// input buffers concurrently with preparation.
	History  []PureRevisionState
	Payloads [][]byte
}

func transactionLimits(options PrepareOptions) TransactionLimits {
	l := DefaultTransactionLimits()
	if options.Limits != nil {
		l = *options.Limits
	}
	l.validate()
	return l
}
func pureSize(value any) int {
	b, err := CanonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return len(b)
}
func pureDigest(value any, domain string) string {
	d, err := CanonicalDigest(value, domain)
	if err != nil {
		panic(err)
	}
	return d
}
func pureCounter(v any) *big.Int {
	n, err := ParseCounter(txt(v, false))
	if err != nil {
		panic(err)
	}
	return n
}
func purePosition(v any, maximum int) int {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(maximum) || n > float64(MaxSafeInteger) || math.Trunc(n) != n {
		failModel("Insertion position is outside resulting sequence")
	}
	return int(n)
}
func pureSlot(v any) string    { return enum(v, "pre_process children post_process skills tools") }
func purePlane(v any) string   { return enum(v, "model prompt") }
func singleSlot(s string) bool { return s == "pre_process" || s == "post_process" }
func transactionSlots() []string {
	return []string{"pre_process", "children", "post_process", "skills", "tools"}
}
func updateFields() []string {
	return []string{"description", "instructions", "instruction_template", "extensions", "binding", "input_contract"}
}

// Local NodeSpec parsing only. Final whole-forest validation is separate, so
// transient instruction unions/name collisions may be repaired by later writes.
func transactionNode(value any) map[string]any {
	n := parseNode(value)
	if len(n.uses) != 0 || len(n.members.Placements()) != 0 {
		failModel("Create relations must be empty; use ordered commands")
	}
	return n.AsJSON().(map[string]any)
}
func normalizePureCommand(value any) map[string]any {
	m, ok := value.(map[string]any)
	if !ok {
		failModel("Expected typed command")
	}
	op := txt(m["op"], false)
	if !slices.Contains(SupportedCommands(), op) {
		pureFail("unsupported_contract", "Unsupported pure candidate command: "+op)
	}
	shapes := map[string][2]string{
		"create_node": {"node", ""}, "update_node": {"node_id set_fields unset_fields", ""}, "rename_node": {"node_id new_name", ""},
		"attach_node": {"node_id target", ""}, "move_node": {"node_id target", ""}, "detach_node": {"node_id", ""},
		"reorder_members": {"parent_id slot node_ids", ""}, "add_root": {"node_id plane position", ""}, "remove_root": {"node_id", ""},
		"add_use": {"source_id target_id", "position"}, "remove_use": {"source_id target_id", ""},
		"delete_nodes":    {"targets policy deleted_ids disconnections", ""},
		"restore_nodes":   {"revision node_ids", ""},
		"replace_subtree": {"anchor_id id_mapping payload", ""},
		"import":          {"scope_ids id_mapping payload", ""}, "reconcile": {"scope_ids id_mapping payload", ""},
		"put_publication": {"publication", ""}, "remove_publication": {"publication_id", ""},
		"put_presentation": {"locale presentation", ""}, "remove_presentation": {"locale node_id", ""},
	}
	s := shapes[op]
	obj(m, "op "+s[0], s[1])
	for _, key := range []string{"node_id", "parent_id", "source_id", "target_id", "anchor_id", "publication_id"} {
		if v, ok := m[key]; ok {
			ident(v)
		}
	}
	if v, ok := m["position"]; ok {
		purePosition(v, int(MaxSafeInteger))
	}
	if v, ok := m["plane"]; ok {
		purePlane(v)
	}
	if v, ok := m["slot"]; ok {
		pureSlot(v)
	}
	for _, key := range []string{"node_ids", "scope_ids", "targets", "deleted_ids"} {
		if v, ok := m[key]; ok {
			m[key] = jsonIDs(ids(v))
		}
	}
	if v, ok := m["revision"]; ok && pureCounter(v).Sign() == 0 {
		failModel("Restore requires nonzero revision")
	}
	if v, ok := m["target"]; ok {
		t, isObject := v.(map[string]any)
		if !isObject {
			failModel("Expected placement target")
		}
		if _, member := t["parent_id"]; member {
			obj(t, "parent_id slot position", "")
			ident(t["parent_id"])
			pureSlot(t["slot"])
		} else {
			obj(t, "plane position", "")
			purePlane(t["plane"])
		}
		purePosition(t["position"], int(MaxSafeInteger))
	}
	if op == "create_node" {
		m["node"] = transactionNode(m["node"])
	}
	if op == "rename_node" {
		authorName(m["new_name"])
	}
	if op == "update_node" {
		fields := obj(m["set_fields"], "", "description instructions instruction_template extensions binding input_contract")
		unset := ids(m["unset_fields"])
		for _, key := range unset {
			_, both := fields[key]
			if both || !slices.Contains([]string{"instructions", "instruction_template", "binding", "input_contract"}, key) {
				failModel("Invalid or conflicting update fields")
			}
		}
		// Fixed field order, never Go map iteration order.
		for _, key := range updateFields() {
			raw, present := fields[key]
			if !present {
				continue
			}
			switch key {
			case "description":
				txt(raw, false)
			case "instructions":
				txt(raw, true)
			case "instruction_template":
				_, fields[key] = parseTemplate(raw)
			case "extensions":
				fields[key] = parseExtensions(raw)
			case "binding":
				fields[key] = parseBinding(raw).AsJSON()
			case "input_contract":
				validateInputContract(raw)
			}
		}
		m["unset_fields"] = jsonIDs(unset)
	}
	if op == "put_publication" {
		parsePublication(m["publication"])
	}
	if v, ok := m["locale"]; ok {
		m["locale"] = normalizeLocale(v)
	}
	if op == "put_presentation" {
		p := obj(m["presentation"], "node_id", "title description")
		ident(p["node_id"])
		if len(p) == 1 {
			failModel("Presentation requires human fields")
		}
		for _, key := range []string{"title", "description"} {
			if v, ok := p[key]; ok {
				txt(v, false)
			}
		}
	}
	if v, ok := m["id_mapping"]; ok {
		mapping := obj(v, "retained_ids new_ids deleted_ids", "")
		for _, key := range []string{"retained_ids", "new_ids", "deleted_ids"} {
			mapping[key] = jsonIDs(ids(mapping[key]))
		}
		payload := obj(m["payload"], "", "inline blob")
		if len(payload) != 1 {
			failModel("Exactly one inline/blob payload required")
		}
		if ref, ok := payload["blob"]; ok {
			pureBlobReference(ref)
		}
	}
	if op == "delete_nodes" {
		if len(ids(m["targets"])) == 0 {
			failModel("Delete targets cannot be empty")
		}
		enum(m["policy"], "reject_references explicit_cascade")
		seen := map[string]bool{}
		for _, raw := range arr(m["disconnections"]) {
			d := obj(raw, "source_id target_id relation", "")
			ident(d["source_id"])
			ident(d["target_id"])
			enum(d["relation"], "uses containment instruction_template publication presentation")
			key := pureEncoding(d)
			if seen[key] {
				failModel("Duplicate delete disconnection")
			}
			seen[key] = true
		}
	}
	if op == "restore_nodes" && len(ids(m["node_ids"])) == 0 {
		failModel("Restore scope cannot be empty")
	}
	return m
}

func pureEncoding(value any) string {
	b, err := CanonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func pureBlobReference(value any) map[string]any {
	r := obj(value, "digest size media_type", "")
	pureCounter(r["size"])
	digest := txt(r["digest"], false)
	if r["media_type"] != "application/json" || len(digest) != 71 || digest[:7] != "sha256:" {
		failModel("Invalid canonical definition blob reference")
	}
	for _, c := range digest[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			failModel("Invalid blob digest")
		}
	}
	return r
}

// TransactionEnvelope owns normalized JSON; the zero value is invalid. All
// getters return defensive snapshots. Hash excludes key/correlation/approval.
type TransactionEnvelope struct {
	data  FrozenJSON
	valid bool
}

func (e TransactionEnvelope) AsJSON() any { return e.data.JSON() }
func (e TransactionEnvelope) CommandDigest() (string, error) {
	if !e.valid {
		return "", invalid("Invalid transaction envelope")
	}
	m := e.data.value.(map[string]any)
	subject := map[string]any{}
	for _, key := range []string{"schema", "graph_id", "base_revision", "commands", "reason"} {
		subject[key] = m[key]
	}
	return CanonicalDigest(subject, "contexture.command/v1")
}
func NewTransactionEnvelope(value any, limits TransactionLimits) (out TransactionEnvelope, err error) {
	defer catchModel(&err)
	limits.validate()
	if data, ok := value.([]byte); ok {
		if len(data) > limits.MaxTransactionBytes {
			pureFail("limit_exceeded", "Inline transaction budget exceeded")
		}
		value, err = DecodeJSON(data)
		if err != nil {
			return out, err
		}
	}
	m := obj(mustFreeze(value).JSON(), "schema graph_id base_revision idempotency_key commands reason correlation_id", "approval_ref")
	if m["schema"] != "contexture.transaction/v1" {
		pureFail("unsupported_contract", "Ordinary candidate schema required")
	}
	for _, key := range []string{"graph_id", "idempotency_key", "correlation_id"} {
		ident(m[key])
	}
	if v, ok := m["approval_ref"]; ok {
		ident(v)
	}
	pureCounter(m["base_revision"])
	txt(m["reason"], false)
	commands := arr(m["commands"])
	if len(commands) == 0 || len(commands) > limits.MaxCommands {
		pureFail("limit_exceeded", "Command count outside budget")
	}
	for i, command := range commands {
		commands[i] = normalizePureCommand(command)
	}
	if pureSize(m) > limits.MaxTransactionBytes {
		pureFail("limit_exceeded", "Inline transaction budget exceeded")
	}
	return TransactionEnvelope{mustFreeze(m), true}, nil
}

// PureRevisionState is a caller-supplied snapshot, not an accepted journal head.
// Handles are copied so replacing the caller's GraphDefinition cannot alter it.
type PureRevisionState struct {
	definition GraphDefinition
	revision   string
	tombstones FrozenJSON
	valid      bool
}

func (s PureRevisionState) Definition() GraphDefinition { return s.definition }
func (s PureRevisionState) Revision() string            { return s.revision }
func (s PureRevisionState) Tombstones() any             { return s.tombstones.JSON() }
func (s PureRevisionState) DefinitionDigest() (string, error) {
	if !s.valid {
		return "", invalid("Invalid revision snapshot")
	}
	return s.definition.Digest()
}
func NewPureRevisionState(definition *GraphDefinition, revision string, tombstones any) (out PureRevisionState, err error) {
	defer catchModel(&err)
	if definition == nil || !definition.valid || pureCounter(revision).Sign() == 0 {
		failModel("Snapshot requires validated definition and nonzero revision")
	}
	if tombstones == nil {
		tombstones = []any{}
	}
	seen, retained := map[string]bool{}, map[string]any{}
	for _, raw := range arr(mustFreeze(tombstones).JSON()) {
		t := obj(raw, "node_id kind mutable deleted_revision", "")
		id := ident(t["node_id"])
		deleted := pureCounter(t["deleted_revision"])
		if deleted.Sign() == 0 || deleted.Cmp(pureCounter(revision)) > 0 || seen[id] {
			failModel("Invalid retained identity history")
		}
		seen[id] = true
		enum(t["kind"], "role skill tool")
		if _, ok := t["mutable"].(bool); !ok {
			enum(t["mutable"], "inherit")
		}
		if live, ok := definition.nodes[id]; ok {
			m := live.data.value.(map[string]any)
			if m["kind"] != t["kind"] || m["mutable"] != t["mutable"] {
				pureFail("identity_conflict", "Live identity conflicts with tombstone")
			}
		}
		retained[id] = t
	}
	ordered := []any{}
	for _, id := range sortedSet(seen) {
		ordered = append(ordered, retained[id])
	}
	return PureRevisionState{*definition, revision, mustFreeze(ordered), true}, nil
}

type pureDraft struct {
	base           PureRevisionState
	nodes          map[string]map[string]any
	roots          []any
	wire           map[string]any
	pubs, catalogs []any
	defaultLocale  string
	deleted        map[string]bool
	options        PrepareOptions
	limits         TransactionLimits
	rootOperations bool
}

func newPureDraft(base PureRevisionState) *pureDraft {
	d := &pureDraft{base: base, nodes: map[string]map[string]any{}, roots: base.definition.AsJSON().(map[string]any)["roots"].([]any)}
	for _, n := range base.definition.Nodes() {
		d.nodes[n.id] = n.AsJSON().(map[string]any)
	}
	d.wire = base.definition.AsJSON().(map[string]any)
	d.pubs = arr(d.wire["publications"])
	p := d.wire["presentations"].(map[string]any)
	d.catalogs, d.defaultLocale = arr(p["catalogs"]), p["default_locale"].(string)
	d.deleted = map[string]bool{}
	return d
}
func (d *pureDraft) node(value any) map[string]any {
	n, ok := d.nodes[ident(value)]
	if !ok {
		pureFail("reference_not_found", "Command target must already exist")
	}
	return n
}
func (d *pureDraft) templateTargets(value any) {
	for _, raw := range arr(obj(value, "parts", "")["parts"]) {
		p := raw.(map[string]any)
		if p["kind"] == "NodeRef" {
			d.node(p["target_id"])
		}
	}
}
func (d *pureDraft) members(parent string) map[string]any {
	n := d.node(parent)
	if n["kind"] != "role" {
		failModel("Containment parent must be a Role")
	}
	return n["members"].(map[string]any)
}
func (d *pureDraft) sequence(parent, slot string) []string {
	m := d.members(parent)
	if singleSlot(slot) {
		if v, ok := m[slot]; ok {
			return []string{v.(string)}
		}
		return []string{}
	}
	return ids(m[slot])
}
func (d *pureDraft) assign(parent, slot string, values []string) {
	m := d.members(parent)
	if singleSlot(slot) {
		if len(values) > 1 {
			failModel("Singleton member slot is occupied")
		}
		delete(m, slot)
		if len(values) > 0 {
			m[slot] = values[0]
		}
	} else {
		m[slot] = jsonIDs(values)
	}
}

type pureOwner struct {
	parent, slot string
	position     int
}

func (d *pureDraft) owners(id string) []pureOwner {
	out := []pureOwner{}
	for i, raw := range d.roots {
		if raw.(map[string]any)["node_id"] == id {
			out = append(out, pureOwner{position: i})
		}
	}
	keys := map[string]bool{}
	for parent := range d.nodes {
		keys[parent] = true
	}
	for _, parent := range sortedSet(keys) {
		if d.nodes[parent]["kind"] == "role" {
			for _, slot := range transactionSlots() {
				for i, target := range d.sequence(parent, slot) {
					if target == id {
						out = append(out, pureOwner{parent, slot, i})
					}
				}
			}
		}
	}
	if len(out) > 1 {
		failModel("Draft has multiple containment owners")
	}
	return out
}
func (d *pureDraft) detach(id string, rootOnly bool) {
	d.node(id)
	owners := d.owners(id)
	if len(owners) == 0 {
		failModel("Node has no placement")
	}
	o := owners[0]
	if o.parent == "" {
		d.roots = slices.Delete(d.roots, o.position, o.position+1)
		d.rootOperations = true
	} else {
		if rootOnly {
			failModel("remove_root target is not a root")
		}
		values := d.sequence(o.parent, o.slot)
		d.assign(o.parent, o.slot, slices.Delete(values, o.position, o.position+1))
	}
}
func (d *pureDraft) attach(id string, t map[string]any) {
	d.node(id)
	if len(d.owners(id)) != 0 {
		failModel("attach requires an unowned draft object")
	}
	if parent, member := t["parent_id"]; member {
		p := ident(parent)
		slot := pureSlot(t["slot"])
		values := d.sequence(p, slot)
		at := purePosition(t["position"], len(values))
		d.assign(p, slot, slices.Insert(values, at, id))
	} else {
		at := purePosition(t["position"], len(d.roots))
		d.roots = slices.Insert(d.roots, at, any(map[string]any{"node_id": id, "plane": purePlane(t["plane"])}))
		d.rootOperations = true
	}
}
func (d *pureDraft) apply(c map[string]any) {
	op := c["op"].(string)
	id, _ := c["node_id"].(string)
	switch op {
	case "create_node":
		n := mustFreeze(c["node"]).JSON().(map[string]any)
		identity := n["node_id"].(string)
		if _, ok := d.nodes[identity]; ok || d.deleted[identity] {
			pureFail("identity_conflict", "Identity reuse requires explicit historical restore")
		}
		for _, raw := range d.base.tombstones.value.([]any) {
			if raw.(map[string]any)["node_id"] == identity {
				pureFail("identity_conflict", "Identity reuse requires explicit historical restore")
			}
		}
		if t, ok := n["instruction_template"]; ok {
			d.templateTargets(t)
		}
		d.nodes[identity] = n
	case "rename_node":
		d.node(id)["name"] = c["new_name"]
	case "update_node":
		n := d.node(id)
		fields := c["set_fields"].(map[string]any)
		if t, ok := fields["instruction_template"]; ok {
			d.templateTargets(t)
		}
		for _, key := range ids(c["unset_fields"]) {
			delete(n, key)
		}
		for _, key := range updateFields() {
			if value, ok := fields[key]; ok {
				n[key] = mustFreeze(value).JSON()
			}
		}
	case "attach_node", "move_node", "add_root":
		if op == "move_node" {
			d.detach(id, false)
		}
		if op == "add_root" {
			d.attach(id, map[string]any{"plane": c["plane"], "position": c["position"]})
		} else {
			d.attach(id, c["target"].(map[string]any))
		}
	case "detach_node", "remove_root":
		d.detach(id, op == "remove_root")
	case "reorder_members":
		parent := ident(c["parent_id"])
		slot := pureSlot(c["slot"])
		current, values := d.sequence(parent, slot), ids(c["node_ids"])
		if len(current) != len(values) {
			failModel("Reorder requires exactly existing member set")
		}
		for _, v := range values {
			if !slices.Contains(current, v) {
				failModel("Reorder requires exactly existing member set")
			}
		}
		d.assign(parent, slot, values)
	case "add_use", "remove_use":
		source := d.node(c["source_id"])
		d.node(c["target_id"])
		uses := ids(source["uses"])
		target := ident(c["target_id"])
		at := slices.Index(uses, target)
		if op == "add_use" {
			if at >= 0 || c["source_id"] == target {
				failModel("Duplicate or self use")
			}
			pos := len(uses)
			if v, ok := c["position"]; ok {
				pos = purePosition(v, len(uses))
			}
			uses = slices.Insert(uses, pos, target)
		} else {
			if at < 0 {
				failModel("Use does not exist")
			}
			uses = slices.Delete(uses, at, at+1)
		}
		source["uses"] = jsonIDs(uses)
	case "delete_nodes":
		d.deleteNodes(c)
	case "restore_nodes":
		d.restoreNodes(c)
	case "replace_subtree", "import", "reconcile":
		d.replace(c)
	case "put_publication", "remove_publication":
		publicationID := c["publication_id"]
		if op == "put_publication" {
			p := c["publication"].(map[string]any)
			d.node(p["opens_id"])
			publicationID = p["publication_id"]
		}
		at := -1
		for i, p := range d.pubs {
			if p.(map[string]any)["publication_id"] == publicationID {
				at = i
				break
			}
		}
		if op == "remove_publication" {
			if at < 0 {
				failModel("Publication does not exist")
			}
			d.pubs = slices.Delete(d.pubs, at, at+1)
		} else {
			p := mustFreeze(c["publication"]).JSON()
			if at < 0 {
				d.pubs = append(d.pubs, p)
			} else {
				d.pubs[at] = p
			}
		}
	case "put_presentation", "remove_presentation":
		at := -1
		for i, raw := range d.catalogs {
			if raw.(map[string]any)["locale"] == c["locale"] {
				at = i
				break
			}
		}
		if at < 0 {
			if op == "remove_presentation" {
				failModel("Presentation does not exist")
			}
			d.catalogs = append(d.catalogs, map[string]any{"locale": c["locale"], "entries": []any{}})
			at = len(d.catalogs) - 1
		}
		catalog := d.catalogs[at].(map[string]any)
		entry := map[string]any{"node_id": id}
		if op == "put_presentation" {
			entry = c["presentation"].(map[string]any)
		}
		d.node(entry["node_id"])
		entries, found := arr(catalog["entries"]), -1
		for i, raw := range entries {
			if raw.(map[string]any)["node_id"] == entry["node_id"] {
				found = i
				break
			}
		}
		if op == "remove_presentation" {
			if found < 0 {
				failModel("Presentation does not exist")
			}
			entries = slices.Delete(entries, found, found+1)
		} else if found < 0 {
			entries = append(entries, mustFreeze(entry).JSON())
		} else {
			entries[found] = mustFreeze(entry).JSON()
		}
		catalog["entries"] = entries
	default:
		pureFail("unsupported_contract", "Unsupported pure command")
	}
}
func (d *pureDraft) definition(contracts map[string]PublicationContract, l TransactionLimits) *GraphDefinition {
	wire := mustFreeze(d.wire).JSON().(map[string]any)
	nodes := []any{}
	keys := map[string]bool{}
	for id := range d.nodes {
		keys[id] = true
	}
	for _, id := range sortedSet(keys) {
		nodes = append(nodes, d.nodes[id])
	}
	wire["nodes"], wire["roots"] = nodes, d.roots
	wire["publications"] = d.pubs
	wire["presentations"] = map[string]any{"default_locale": d.defaultLocale, "catalogs": d.catalogs}
	if len(nodes) > l.MaxNodes || pureSize(wire) > l.MaxDefinitionBytes {
		pureFail("limit_exceeded", "Candidate exceeds graph budget")
	}
	g, err := NewGraphDefinition(wire, contracts)
	if err != nil {
		panic(err)
	}
	x, err := BuildGraphIndex(g, contracts)
	if err != nil {
		panic(err)
	}
	edges := len(g.roots) + len(g.publications)
	for _, n := range g.Nodes() {
		edges += len(n.uses) + len(n.members.Placements()) + len(n.instructions.parts)
		if x.locations[n.id].Depth > l.MaxDepth || pureSize(n.AsJSON()) > l.MaxNodeBytes {
			pureFail("limit_exceeded", "Node size/depth exceeds transaction validation budget")
		}
	}
	if edges > l.MaxEdges {
		pureFail("limit_exceeded", "Graph relation budget exceeded")
	}
	return g
}

func pureSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range values {
		out[id] = true
	}
	return out
}
func pureSameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}
func pureClosure(g *GraphDefinition, seeds []string) map[string]bool {
	out := map[string]bool{}
	stack := slices.Clone(seeds)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n, exists := g.nodes[id]
		if !exists || out[id] {
			continue
		}
		out[id] = true
		for _, p := range n.members.Placements() {
			stack = append(stack, p.NodeID)
		}
	}
	return out
}

func (d *pureDraft) deleteNodes(c map[string]any) {
	deleted, closure := pureSet(ids(c["deleted_ids"])), map[string]bool{}
	stack := ids(c["targets"])
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if closure[id] {
			continue
		}
		n := d.node(id)
		closure[id] = true
		if n["kind"] == "role" {
			for _, slot := range transactionSlots() {
				stack = append(stack, d.sequence(id, slot)...)
			}
		}
	}
	if !pureSameSet(deleted, closure) {
		failModel("deleted_ids must exactly equal target subtree closure")
	}
	expected := map[string]bool{}
	relation := func(source, target, kind string) {
		expected[pureEncoding(map[string]any{"source_id": source, "target_id": target, "relation": kind})] = true
	}
	for _, id := range sortedSet(deleted) {
		for _, owner := range d.owners(id) {
			if owner.parent == "" {
				relation(d.base.definition.graphID, id, "containment")
			} else if !deleted[owner.parent] {
				relation(owner.parent, id, "containment")
			}
		}
	}
	for source, n := range d.nodes {
		if deleted[source] {
			continue
		}
		for _, target := range ids(n["uses"]) {
			if deleted[target] {
				relation(source, target, "uses")
			}
		}
		if template, ok := n["instruction_template"]; ok {
			for _, raw := range arr(template.(map[string]any)["parts"]) {
				p := raw.(map[string]any)
				if p["kind"] == "NodeRef" && deleted[p["target_id"].(string)] {
					failModel("Repair external NodeRef programs explicitly before delete")
				}
			}
		}
	}
	for _, raw := range d.pubs {
		p := raw.(map[string]any)
		if deleted[p["opens_id"].(string)] {
			relation(p["publication_id"].(string), p["opens_id"].(string), "publication")
		}
	}
	for _, raw := range d.catalogs {
		for _, entry := range arr(raw.(map[string]any)["entries"]) {
			if deleted[entry.(map[string]any)["node_id"].(string)] {
				failModel("Remove target presentations explicitly before delete")
			}
		}
	}
	supplied := map[string]bool{}
	for _, raw := range arr(c["disconnections"]) {
		supplied[pureEncoding(raw)] = true
		if c["policy"] == "reject_references" && raw.(map[string]any)["relation"] != "containment" {
			failModel("Delete target has external references")
		}
	}
	if !pureSameSet(expected, supplied) {
		failModel("Disconnections must exactly match all boundary references")
	}
	for _, id := range sortedSet(deleted) {
		if len(d.owners(id)) > 0 {
			d.detach(id, false)
		}
	}
	for _, n := range d.nodes {
		values := []string{}
		for _, id := range ids(n["uses"]) {
			if !deleted[id] {
				values = append(values, id)
			}
		}
		n["uses"] = jsonIDs(values)
	}
	pubs := []any{}
	for _, raw := range d.pubs {
		if !deleted[raw.(map[string]any)["opens_id"].(string)] {
			pubs = append(pubs, raw)
		}
	}
	d.pubs = pubs
	for id := range deleted {
		delete(d.nodes, id)
		d.deleted[id] = true
	}
}

func (d *pureDraft) restoreNodes(c map[string]any) {
	revision := c["revision"].(string)
	if pureCounter(revision).Cmp(pureCounter(d.base.revision)) > 0 {
		pureFail("history_unavailable", "Restore requires a past accepted revision")
	}
	var past *PureRevisionState
	for i := range d.options.History {
		s := &d.options.History[i]
		if s.valid && s.revision == revision && s.definition.graphID == d.base.definition.graphID {
			past = s
			break
		}
	}
	if past == nil {
		pureFail("history_unavailable", "Retained history snapshot required")
	}
	for _, id := range ids(c["node_ids"]) {
		historical, ok := past.definition.nodes[id]
		if _, live := d.nodes[id]; live || !ok {
			pureFail("identity_conflict", "Restore requires absent current and present historical identity")
		}
		var evidence map[string]any
		for _, raw := range arr(d.base.tombstones.value) {
			t := raw.(map[string]any)
			if t["node_id"] == id {
				evidence = t
				break
			}
		}
		if d.deleted[id] {
			if n, ok := d.base.definition.nodes[id]; ok {
				evidence = n.AsJSON().(map[string]any)
			}
		}
		if evidence == nil {
			pureFail("history_unavailable", "Restore identity has no retained tombstone")
		}
		n := historical.AsJSON().(map[string]any)
		if n["kind"] != evidence["kind"] || n["mutable"] != evidence["mutable"] {
			pureFail("identity_conflict", "Historical identity creation policy mismatch")
		}
		d.nodes[id] = n
	}
}

// Scope compares complete candidates against the current ordered draft.
func pureCheckScope(before, after *GraphDefinition, seeds []string) (map[string]bool, map[string]bool) {
	if len(seeds) == 0 {
		pureFail("scope_escape", "Scope anchors required")
	}
	for _, id := range seeds {
		_, a := before.nodes[id]
		_, b := after.nodes[id]
		if !a && !b {
			pureFail("scope_escape", "Scope anchor does not exist")
		}
	}
	a, b := pureClosure(before, seeds), pureClosure(after, seeds)
	for id := range b {
		if _, exists := before.nodes[id]; exists && !a[id] {
			pureFail("scope_escape", "Existing identity crosses scope boundary")
		}
	}
	for id := range a {
		if _, exists := after.nodes[id]; exists && !b[id] {
			pureFail("scope_escape", "Existing identity crosses scope boundary")
		}
	}
	whole := len(a) == len(before.nodes) && len(b) == len(after.nodes)
	x, y := before.AsJSON().(map[string]any), after.AsJSON().(map[string]any)
	if before.applicationName != after.applicationName {
		pureFail("identity_conflict", "Ordinary payload cannot rename application identity")
	}
	for _, key := range []string{"schema", "semantic_version", "graph_id", "mode", "extensions"} {
		if !pureEqual(x[key], y[key]) && (!whole || key != "mode" && key != "extensions") {
			pureFail("scope_escape", "Graph metadata outside scope")
		}
	}
	outside := func(g *GraphDefinition, scope map[string]bool) map[string]bool {
		out := map[string]bool{}
		for id := range g.nodes {
			if !scope[id] {
				out[id] = true
			}
		}
		return out
	}
	if !pureSameSet(outside(before, a), outside(after, b)) {
		pureFail("scope_escape", "Candidate changes identities outside scope")
	}
	projection := func(g *GraphDefinition, id string, scope map[string]bool) any {
		n := g.nodes[id].AsJSON().(map[string]any)
		if n["kind"] == "role" {
			m := n["members"].(map[string]any)
			for _, slot := range transactionSlots() {
				if singleSlot(slot) {
					if v, ok := m[slot]; ok && scope[v.(string)] {
						delete(m, slot)
					}
				} else {
					values := []string{}
					for _, v := range ids(m[slot]) {
						if !scope[v] {
							values = append(values, v)
						}
					}
					m[slot] = jsonIDs(values)
				}
			}
		}
		return n
	}
	for id := range outside(before, a) {
		if !pureEqual(projection(before, id, a), projection(after, id, b)) {
			pureFail("scope_escape", "Candidate changes outside node or boundary reference")
		}
	}
	filter := func(values any, key string, scope map[string]bool) []any {
		out := []any{}
		for _, raw := range arr(values) {
			if !scope[raw.(map[string]any)[key].(string)] {
				out = append(out, raw)
			}
		}
		return out
	}
	for _, pair := range [][2]string{{"roots", "node_id"}, {"publications", "opens_id"}} {
		if !pureEqual(filter(x[pair[0]], pair[1], a), filter(y[pair[0]], pair[1], b)) {
			pureFail("scope_escape", "Candidate changes outside placement/publication")
		}
	}
	for _, np := range after.publications {
		for _, op := range before.publications {
			if np.PublicationID == op.PublicationID && a[op.OpensID] != b[np.OpensID] {
				pureFail("scope_escape", "Publication identity crosses scope")
			}
		}
	}
	if !whole {
		if before.presentations.defaultLocale != after.presentations.defaultLocale {
			pureFail("scope_escape", "Default locale outside partial scope")
		}
		existing := map[string]bool{}
		for _, c := range before.presentations.catalogs {
			existing[c.locale] = true
		}
		project := func(g map[string]any, scope map[string]bool) []any {
			out := []any{}
			for _, raw := range arr(g["presentations"].(map[string]any)["catalogs"]) {
				c := raw.(map[string]any)
				if existing[c["locale"].(string)] {
					out = append(out, map[string]any{"locale": c["locale"], "entries": filter(c["entries"], "node_id", scope)})
				}
			}
			return out
		}
		if !pureEqual(project(x, a), project(y, b)) {
			pureFail("scope_escape", "Candidate changes outside presentation catalogs")
		}
		for _, raw := range arr(y["presentations"].(map[string]any)["catalogs"]) {
			c := raw.(map[string]any)
			if !existing[c["locale"].(string)] && (len(arr(c["entries"])) == 0 || len(filter(c["entries"], "node_id", b)) > 0) {
				pureFail("scope_escape", "New catalog lies outside scope")
			}
		}
	}
	return a, b
}

func (d *pureDraft) replace(c map[string]any) {
	payload := c["payload"].(map[string]any)
	raw := payload["inline"]
	if value, ok := payload["blob"]; ok {
		ref := pureBlobReference(value)
		size := pureCounter(ref["size"])
		if size.Cmp(big.NewInt(int64(d.limits.MaxDefinitionBytes))) > 0 {
			pureFail("limit_exceeded", "Staged definition exceeds budget")
		}
		if d.options.Payloads == nil {
			pureFail("unsupported_contract", "Immutable payload bytes required")
		}
		var data []byte
		for _, supplied := range d.options.Payloads {
			if big.NewInt(int64(len(supplied))).Cmp(size) == 0 && fmt.Sprintf("sha256:%x", sha256.Sum256(supplied)) == ref["digest"] {
				data = bytes.Clone(supplied)
				break
			}
		}
		if data == nil {
			failModel("Payload bytes disagree with immutable reference")
		}
		var err error
		raw, err = DecodeJSON(data)
		if err != nil {
			panic(err)
		}
		if pureEncoding(raw) != string(data) {
			failModel("Definition blob must contain exact canonical bytes")
		}
	}
	if pureSize(raw) > d.limits.MaxDefinitionBytes {
		pureFail("limit_exceeded", "Definition payload exceeds budget")
	}
	candidate, err := NewGraphDefinition(raw, d.options.PublicationContracts)
	if err != nil {
		panic(err)
	}
	if candidate.graphID != d.base.definition.graphID {
		failModel("Cross-graph replacement is not an ordinary transaction")
	}
	before := d.definition(d.options.PublicationContracts, d.limits)
	var seeds []string
	if c["op"] == "replace_subtree" {
		seeds = []string{ident(c["anchor_id"])}
		d.node(seeds[0])
	} else {
		seeds = ids(c["scope_ids"])
	}
	a, b := pureCheckScope(before, candidate, seeds)
	expected := map[string]map[string]bool{"retained_ids": {}, "new_ids": {}, "deleted_ids": {}}
	union := map[string]bool{}
	for id := range a {
		union[id] = true
		if b[id] {
			expected["retained_ids"][id] = true
		} else {
			expected["deleted_ids"][id] = true
		}
	}
	for id := range b {
		union[id] = true
		if !a[id] {
			expected["new_ids"][id] = true
		}
	}
	mapping := c["id_mapping"].(map[string]any)
	for _, key := range []string{"retained_ids", "new_ids", "deleted_ids"} {
		if !pureSameSet(expected[key], pureSet(ids(mapping[key]))) {
			failModel("ID mapping is not exact retained/new/deleted partition")
		}
	}
	if len(union) > d.limits.MaxExpandedOperations {
		pureFail("limit_exceeded", "Replacement expansion budget exceeded")
	}
	for id, n := range candidate.nodes {
		old, exists := d.nodes[id]
		if !exists {
			if d.deleted[id] {
				pureFail("identity_conflict", "Replacement cannot resurrect deleted identities")
			}
			for _, raw := range arr(d.base.tombstones.value) {
				if raw.(map[string]any)["node_id"] == id {
					pureFail("identity_conflict", "Replacement cannot resurrect deleted identities")
				}
			}
		} else {
			v := n.AsJSON().(map[string]any)
			for _, key := range []string{"kind", "mutable", "role_variant"} {
				if !pureEqual(old[key], v[key]) {
					pureFail("identity_conflict", "Replacement cannot reset creation identity")
				}
			}
		}
	}
	for id := range d.nodes {
		if _, exists := candidate.nodes[id]; !exists {
			d.deleted[id] = true
		}
	}
	next := newPureDraft(PureRevisionState{definition: *candidate, revision: d.base.revision, tombstones: d.base.tombstones, valid: true})
	d.rootOperations = d.rootOperations || !pureEqual(d.roots, next.roots)
	d.nodes, d.roots, d.wire, d.pubs, d.catalogs, d.defaultLocale = next.nodes, next.roots, next.wire, next.pubs, next.catalogs, next.defaultLocale
}

func resolvePureFacts(base PureRevisionState, candidate *GraphDefinition) (FrozenJSON, FrozenJSON) {
	before := &base.definition
	facts := []any{}
	tombstones := map[string]any{}
	for _, raw := range arr(base.tombstones.value) {
		tombstones[raw.(map[string]any)["node_id"].(string)] = raw
	}
	retained := []string{}
	for _, n := range candidate.Nodes() {
		if _, ok := before.nodes[n.id]; !ok {
			if tombstone, ok := tombstones[n.id]; ok {
				facts = append(facts, map[string]any{"type": "NodeRestored", "node": n.AsJSON(), "tombstone": tombstone})
			} else {
				facts = append(facts, map[string]any{"type": "NodeCreated", "node": n.AsJSON()})
			}
		} else {
			retained = append(retained, n.id)
		}
	}
	optional := func(n map[string]any, key string) any {
		if v, ok := n[key]; ok {
			return map[string]any{"present": true, "value": v}
		}
		return map[string]any{"present": false}
	}
	fields := append([]string{"name"}, updateFields()...)
	slices.Sort(fields)
	for _, id := range retained {
		a, b := before.nodes[id].AsJSON().(map[string]any), candidate.nodes[id].AsJSON().(map[string]any)
		left, right := map[string]any{}, map[string]any{}
		for _, key := range fields {
			x, y := optional(a, key), optional(b, key)
			if !pureEqual(x, y) {
				left[key], right[key] = x, y
			}
		}
		if len(left) > 0 {
			facts = append(facts, map[string]any{"type": "NodeFieldsSet", "node_id": id, "before": left, "after": right})
		}
	}
	a, b := before.data.value.(map[string]any), candidate.data.value.(map[string]any)
	left, right := map[string]any{}, map[string]any{}
	for _, key := range []string{"application_name", "extensions", "mode"} {
		if !pureEqual(a[key], b[key]) {
			left[key], right[key] = a[key], b[key]
		}
	}
	if len(left) > 0 {
		facts = append(facts, map[string]any{"type": "GraphFieldsSet", "before": left, "after": right})
	}
	if !pureEqual(a["roots"], b["roots"]) {
		facts = append(facts, map[string]any{"type": "RootsAssigned", "before": a["roots"], "after": b["roots"]})
	}
	for _, pair := range [][2]string{{"members", "MembersAssigned"}, {"uses", "UsesAssigned"}} {
		for _, id := range retained {
			x, y := before.nodes[id].data.value.(map[string]any), candidate.nodes[id].data.value.(map[string]any)
			if v, ok := x[pair[0]]; ok && !pureEqual(v, y[pair[0]]) {
				facts = append(facts, map[string]any{"type": pair[1], "node_id": id, "before": v, "after": y[pair[0]]})
			}
		}
	}
	pubs := func(value any) (map[string]any, map[string]int) {
		entries, positions := map[string]any{}, map[string]int{}
		for i, raw := range arr(value) {
			id := raw.(map[string]any)["publication_id"].(string)
			entries[id], positions[id] = raw, i
		}
		return entries, positions
	}
	ap, ai := pubs(a["publications"])
	bp, bi := pubs(b["publications"])
	keys := map[string]bool{}
	for id := range ap {
		keys[id] = true
	}
	for id := range bp {
		keys[id] = true
	}
	for _, id := range sortedSet(keys) {
		x, xok := ap[id]
		y, yok := bp[id]
		if !yok {
			facts = append(facts, map[string]any{"type": "PublicationRemoved", "before": x, "position": ai[id]})
		} else if !xok || ai[id] != bi[id] || !pureEqual(x, y) {
			previous := map[string]any{"present": false}
			if xok {
				previous = map[string]any{"present": true, "value": x}
			}
			facts = append(facts, map[string]any{"type": "PublicationPut", "before": previous, "after": y, "position": bi[id]})
		}
	}
	if !pureEqual(a["presentations"], b["presentations"]) {
		facts = append(facts, map[string]any{"type": "PresentationsAssigned", "before": a["presentations"], "after": b["presentations"]})
	}
	for _, n := range before.Nodes() {
		if _, exists := candidate.nodes[n.id]; !exists {
			revision := pureCounter(base.revision)
			revision.Add(revision, big.NewInt(1))
			if revision.Cmp(pureCounter(MaxCounter)) > 0 {
				pureFail("limit_exceeded", "Revision counter exhausted")
			}
			wire := n.AsJSON().(map[string]any)
			tombstone := map[string]any{"node_id": n.id, "kind": wire["kind"], "mutable": wire["mutable"], "deleted_revision": revision.String()}
			tombstones[n.id] = tombstone
			facts = append(facts, map[string]any{"type": "NodeDeleted", "node_id": n.id, "before": wire, "tombstone": tombstone})
		}
	}
	ordered := []any{}
	keys = map[string]bool{}
	for id := range tombstones {
		keys[id] = true
	}
	for _, id := range sortedSet(keys) {
		ordered = append(ordered, tombstones[id])
	}
	return mustFreeze(facts), mustFreeze(ordered)
}

// PurePreparedTransaction is an immutable proposed batch, not PreviewReceipt or
// permission to append. Root intent survives no-op; no durable result is claimed.
type PurePreparedTransaction struct {
	base                                               PureRevisionState
	envelope                                           TransactionEnvelope
	candidate                                          GraphDefinition
	facts, tombstones, approvalSubject                 FrozenJSON
	factsDigest, approvalSubjectDigest, resultRevision string
	protection                                         ProtectionReport
	rootOperations, noop, valid                        bool
}

func (p PurePreparedTransaction) Status() string {
	if !p.valid {
		return ""
	}
	return "prepared-pure-candidate"
}
func (p PurePreparedTransaction) Base() PureRevisionState       { return p.base }
func (p PurePreparedTransaction) Envelope() TransactionEnvelope { return p.envelope }
func (p PurePreparedTransaction) Candidate() GraphDefinition    { return p.candidate }
func (p PurePreparedTransaction) Facts() any                    { return p.facts.JSON() }
func (p PurePreparedTransaction) Tombstones() any               { return p.tombstones.JSON() }
func (p PurePreparedTransaction) FactsDigest() string           { return p.factsDigest }
func (p PurePreparedTransaction) Protection() ProtectionReport  { return p.protection }
func (p PurePreparedTransaction) RootOperations() bool          { return p.rootOperations }
func (p PurePreparedTransaction) Noop() bool                    { return p.noop }
func (p PurePreparedTransaction) ResultRevision() string        { return p.resultRevision }
func (p PurePreparedTransaction) ApprovalSubject() any          { return p.approvalSubject.JSON() }
func (p PurePreparedTransaction) ApprovalSubjectDigest() string { return p.approvalSubjectDigest }

func Prepare(base PureRevisionState, envelope any, options PrepareOptions) (out PurePreparedTransaction, err error) {
	defer catchModel(&err)
	if !base.valid {
		failModel("Expected validated pure revision snapshot")
	}
	l := transactionLimits(options)
	if e, ok := envelope.(TransactionEnvelope); ok {
		if !e.valid {
			failModel("Invalid envelope")
		}
		envelope = e.AsJSON()
	}
	request, err := NewTransactionEnvelope(envelope, l)
	if err != nil {
		return out, err
	}
	m := request.data.value.(map[string]any)
	if m["graph_id"] != base.definition.graphID || m["base_revision"] != base.revision {
		pureFail("revision_conflict", "Transaction does not match accepted base")
	}
	draft := newPureDraft(base)
	draft.options, draft.limits = options, l
	for _, command := range arr(m["commands"]) {
		draft.apply(command.(map[string]any))
		if len(draft.nodes) > l.MaxNodes {
			pureFail("limit_exceeded", "Node budget exceeded")
		}
	}
	candidate := draft.definition(options.PublicationContracts, l)
	protection, err := ProtectChange(&base.definition, candidate, options.PublicationContracts)
	if err != nil {
		return out, err
	}
	facts, tombstones := resolvePureFacts(base, candidate)
	if pureSize(facts.value) > l.MaxResolvedBytes {
		pureFail("limit_exceeded", "Resolved facts exceed atomic batch budget")
	}
	noop := len(facts.value.([]any)) == 0
	number := pureCounter(base.revision)
	if !noop {
		number.Add(number, big.NewInt(1))
	}
	if number.Cmp(pureCounter(MaxCounter)) > 0 {
		pureFail("limit_exceeded", "Revision counter exhausted")
	}
	factsDigest := pureDigest(facts.value, "contexture.facts/v1")
	commandDigest, err := request.CommandDigest()
	if err != nil {
		return out, err
	}
	subject := mustFreeze(map[string]any{"schema": "contexture.preview/v1", "graph_id": base.definition.graphID, "base_revision": base.revision, "base_digest": pureDigest(base.definition.data.value, "contexture.graph/v1"), "candidate_digest": pureDigest(candidate.data.value, "contexture.graph/v1"), "command_digest": commandDigest, "resolved_fact_digest": factsDigest})
	return PurePreparedTransaction{base: base, envelope: request, candidate: *candidate, facts: facts, tombstones: tombstones, approvalSubject: subject, factsDigest: factsDigest, approvalSubjectDigest: pureDigest(subject.value, "contexture.preview/v1"), resultRevision: number.String(), protection: protection, rootOperations: draft.rootOperations, noop: noop, valid: true}, nil
}

// RecheckPrepared checks CAS and reconstructs the complete proposal. A Host must
// independently serialize, authorize, fence and durably append facts+receipt.
func RecheckPrepared(current PureRevisionState, prepared PurePreparedTransaction, options PrepareOptions) (out PurePreparedTransaction, err error) {
	defer catchModel(&err)
	if !current.valid || !prepared.valid || !prepared.base.valid {
		failModel("Expected validated snapshots and proposal")
	}
	if current.revision != prepared.base.revision || !pureEqual(current.definition.data.value, prepared.base.definition.data.value) || !pureEqual(current.tombstones.value, prepared.base.tombstones.value) {
		pureFail("revision_conflict", "Current head or identity history changed")
	}
	fresh, err := Prepare(current, prepared.envelope, options)
	if err != nil {
		return out, err
	}
	if !pureEqual(fresh.tombstones.value, prepared.tombstones.value) || !pureEqual(fresh.approvalSubject.value, prepared.approvalSubject.value) || fresh.approvalSubjectDigest != prepared.approvalSubjectDigest || !pureEqual(fresh.facts.value, prepared.facts.value) || !pureEqual(fresh.protection.AsJSON(), prepared.protection.AsJSON()) || !pureEqual(fresh.candidate.data.value, prepared.candidate.data.value) || fresh.factsDigest != prepared.factsDigest || fresh.rootOperations != prepared.rootOperations || fresh.noop != prepared.noop || fresh.resultRevision != prepared.resultRevision || fresh.Status() != prepared.Status() {
		pureFail("revision_conflict", "Prepared proposal no longer describes the complete transaction")
	}
	return fresh, nil
}
