package graph

import (
	"slices"
	"strconv"
	"strings"
)

// NodeLocation is a detached value. HasParent/HasSlot distinguish root entries.
type NodeLocation struct {
	ParentID                                                         string
	Slot                                                             Slot
	HasParent, HasSlot                                               bool
	Ordinal, MemberOrdinal, RootOrdinal, Depth, Preorder, SubtreeEnd int
	Plane                                                            Plane
	EffectiveMutable, FalseBarrier                                   bool
}
type TemplateReference struct {
	SourceID    string
	PartOrdinal int
}

// GraphIndex is a snapshot-local O(nodes+edges+parts+publications+presentations)
// index. It does not cache full paths or resolve, execute, persist, or authorize.
// Private storage is never exposed. The zero value is invalid.
type GraphIndex struct {
	definition                     *GraphDefinition
	locations                      map[string]NodeLocation
	preorder                       []string
	reverseUses                    map[string][]string
	templateRefs                   map[string][]TemplateReference
	publications, presentationRefs map[string][]string
	childNames                     map[string]map[string]string
	rootNames                      map[string]string
	catalogs                       map[string]map[string]Presentation
	reserved                       map[string]bool
}

func BuildGraphIndex(definition *GraphDefinition, contracts map[string]PublicationContract) (out *GraphIndex, err error) {
	defer catchModel(&err)
	if definition == nil || !definition.valid {
		failModel("Expected a validated graph definition")
	}
	// Copy the value handle: callers replacing their handle cannot affect an index.
	snapshot := *definition
	g := &snapshot
	hasModelRoot := false
	for _, root := range g.roots {
		if root.Plane == ModelPlane {
			hasModelRoot = true
		}
	}
	if len(g.nodes) == 0 || !hasModelRoot {
		failModelAt("Committed forest requires at least one model root", "/roots", "schema_invalid")
	}
	x := &GraphIndex{definition: g, locations: map[string]NodeLocation{}, preorder: []string{}, reverseUses: map[string][]string{}, templateRefs: map[string][]TemplateReference{}, publications: map[string][]string{}, presentationRefs: map[string][]string{}, childNames: map[string]map[string]string{}, rootNames: map[string]string{}, catalogs: map[string]map[string]Presentation{}, reserved: map[string]bool{}}
	target := func(id string) NodeSpec {
		n, ok := g.nodes[id]
		if !ok {
			failModelAt("Reference target does not exist", "", "reference_not_found")
		}
		return n
	}
	parents := map[string]NodeLocation{}
	children := map[string][]Placement{}
	place := func(id, parent string, slot Slot, ordinal, memberOrdinal int) {
		n := target(id)
		if _, ok := parents[id]; ok {
			failModel("Duplicate containment, alias or multiple parent")
		}
		siblings := x.rootNames
		if parent != "" {
			siblings = x.childNames[parent]
		}
		if _, ok := siblings[n.name]; ok {
			failModel("Names must be unique across sibling slots or roots")
		}
		parents[id] = NodeLocation{ParentID: parent, Slot: slot, HasParent: parent != "", HasSlot: slot != "", Ordinal: ordinal, MemberOrdinal: memberOrdinal}
		siblings[n.name] = id
	}
	for i, root := range g.roots {
		place(root.NodeID, "", "", i, i)
	}
	// Reuse the normalized node-pool order without sorting during index build;
	// observable reverse-edge ordering comes from authored forest preorder below.
	for _, raw := range g.data.value.(map[string]any)["nodes"].([]any) {
		n := g.nodes[raw.(map[string]any)["node_id"].(string)]
		x.reverseUses[n.id] = []string{}
		x.templateRefs[n.id] = []TemplateReference{}
		x.publications[n.id] = []string{}
		x.presentationRefs[n.id] = []string{}
		if n.kind == Tool {
			_, hasContract := n.data.value.(map[string]any)["input_contract"]
			if g.mode == "runtime" && n.binding == nil {
				failModel("Runtime Tool requires binding")
			}
			if g.mode == "disclosure_only" && (n.binding != nil || hasContract) {
				failModel("Disclosure-only Tools cannot carry execution contracts")
			}
		}
		if n.kind == Role {
			x.childNames[n.id] = map[string]string{}
			entries := n.members.Placements()
			children[n.id] = entries
			for memberOrdinal, entry := range entries {
				child := target(entry.NodeID)
				kind := Role
				if entry.Slot == Skills {
					kind = Skill
				}
				if entry.Slot == Tools {
					kind = Tool
				}
				if child.kind != kind {
					failModel("Member kind does not match typed slot")
				}
				if (entry.Slot == PreProcess || entry.Slot == PostProcess) && child.variant != string(entry.Slot) {
					failModel("Process slot requires matching Role variant")
				}
				place(entry.NodeID, n.id, entry.Slot, entry.Ordinal, memberOrdinal)
			}
		}
		for _, used := range n.uses {
			target(used)
			if used == n.id {
				failModel("Self uses is forbidden")
			}
		}
		for _, part := range n.instructions.parts {
			if part.Kind == "NodeRef" {
				target(part.TargetID)
			}
		}
	}
	if len(parents) != len(g.nodes) {
		failModel("Every node must have exactly one root or parent placement")
	}
	type frame struct {
		id                       string
		plane                    Plane
		root, depth              int
		inherited, blocked, exit bool
	}
	stack := []frame{}
	for i := len(g.roots) - 1; i >= 0; i-- {
		root := g.roots[i]
		stack = append(stack, frame{id: root.NodeID, plane: root.Plane, root: i})
	}
	entered := map[string]bool{}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.exit {
			loc := x.locations[f.id]
			loc.SubtreeEnd = len(x.preorder)
			x.locations[f.id] = loc
			continue
		}
		if entered[f.id] {
			failModel("Containment cycle or alias")
		}
		entered[f.id] = true
		n := g.nodes[f.id]
		barrier := f.blocked || n.mutable == MutableFalse
		effective := !barrier && (n.mutable == MutableTrue || f.inherited)
		loc := parents[f.id]
		loc.RootOrdinal = f.root
		loc.Plane = f.plane
		loc.Depth = f.depth
		loc.Preorder = len(x.preorder)
		loc.SubtreeEnd = -1
		loc.EffectiveMutable = effective
		loc.FalseBarrier = barrier
		x.locations[f.id] = loc
		x.preorder = append(x.preorder, f.id)
		stack = append(stack, frame{id: f.id, exit: true})
		entries := children[f.id]
		for i := len(entries) - 1; i >= 0; i-- {
			stack = append(stack, frame{id: entries[i].NodeID, plane: f.plane, root: f.root, depth: f.depth + 1, inherited: effective, blocked: barrier})
		}
	}
	if len(entered) != len(g.nodes) {
		failModel("Unreachable containment component or cycle")
	}
	for _, id := range x.preorder {
		n := g.nodes[id]
		for _, used := range n.uses {
			x.reverseUses[used] = append(x.reverseUses[used], id)
		}
		for i, part := range n.instructions.parts {
			if part.Kind == "NodeRef" {
				x.templateRefs[part.TargetID] = append(x.templateRefs[part.TargetID], TemplateReference{id, i})
			}
		}
	}
	publicationIDs, publicNames, uris, routes := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, p := range g.publications {
		location := "/publications/" + strconv.Itoa(i)
		n := target(p.OpensID)
		if publicationIDs[p.PublicationID] {
			failModelAt("Duplicate publication identity", location, "schema_invalid")
		}
		publicationIDs[p.PublicationID] = true
		x.publications[n.id] = append(x.publications[n.id], p.PublicationID)
		if p.Kind != "route" {
			if publicNames[p.Name] {
				failModelAt("Duplicate public name", location, "schema_invalid")
			}
			publicNames[p.Name] = true
		}
		if p.Kind == "prompt" {
			if !p.ModelMayOpen {
				x.reserved[n.id] = true
			}
			continue
		}
		if g.mode != "runtime" || n.kind != Tool || n.binding == nil {
			failModelAt("Executable publication requires a runtime Tool", location, "schema_invalid")
		}
		c, ok := contracts[n.id]
		if !ok || !c.valid {
			failModelAt("Pinned registry contract required", location, "binding_contract_required")
		}
		left, e := c.binding.data.Canonical()
		if e != nil {
			panic(e)
		}
		right, e := n.binding.data.Canonical()
		if e != nil {
			panic(e)
		}
		if string(left) != string(right) {
			failModelAt("Pinned registry contract requires exact binding descriptor", location, "binding_contract_required")
		}
		if p.Kind == "resource" {
			if c.effect != "read_only" || !c.argumentFree {
				failModelAt("Resource requires an argument-free read-only Tool", location, "schema_invalid")
			}
			if uris[p.URI] {
				failModelAt("Duplicate Resource URI", location, "schema_invalid")
			}
			uris[p.URI] = true
		} else {
			effect := "write"
			if p.Method == "GET" || p.Method == "HEAD" {
				effect = "read_only"
			}
			if effect != c.effect {
				failModelAt("Route method and registry effect mismatch", location, "schema_invalid")
			}
			key := p.Method + "\x00" + p.Path
			if routes[key] {
				failModelAt("Duplicate Route method/path", location, "schema_invalid")
			}
			routes[key] = true
		}
	}
	for _, catalog := range g.presentations.catalogs {
		entries := map[string]Presentation{}
		for _, entry := range catalog.entries {
			target(entry.NodeID)
			entries[entry.NodeID] = entry
			x.presentationRefs[entry.NodeID] = append(x.presentationRefs[entry.NodeID], catalog.locale)
		}
		x.catalogs[strings.ToLower(catalog.locale)] = entries
	}
	return x, nil
}

func (x *GraphIndex) Definition() GraphDefinition             { return *x.definition }
func (x *GraphIndex) Preorder() []string                      { return slices.Clone(x.preorder) }
func (x *GraphIndex) Location(id string) (NodeLocation, bool) { v, ok := x.locations[id]; return v, ok }
func (x *GraphIndex) ReverseUses(id string) []string          { return slices.Clone(x.reverseUses[id]) }
func (x *GraphIndex) TemplateReferences(id string) []TemplateReference {
	return slices.Clone(x.templateRefs[id])
}
func (x *GraphIndex) PublicationReferences(id string) []string {
	return slices.Clone(x.publications[id])
}
func (x *GraphIndex) PresentationReferences(id string) []string {
	return slices.Clone(x.presentationRefs[id])
}
func (x *GraphIndex) ChildNames(id string) map[string]string {
	out := map[string]string{}
	for k, v := range x.childNames[id] {
		out[k] = v
	}
	return out
}
func (x *GraphIndex) RootNames() map[string]string {
	out := map[string]string{}
	for k, v := range x.rootNames {
		out[k] = v
	}
	return out
}

// Get requires an explicit selector: IDs containing slash never become paths.
func (x *GraphIndex) Get(identity, by string) (NodeSpec, error) {
	if x == nil || x.definition == nil {
		return NodeSpec{}, invalid("Invalid graph index")
	}
	if by == "path" {
		if !strings.HasPrefix(identity, "/") {
			return NodeSpec{}, invalid("Expected absolute author path")
		}
		siblings := x.rootNames
		found := ""
		for _, segment := range strings.Split(identity[1:], "/") {
			var ok bool
			found, ok = siblings[segment]
			if !ok {
				return NodeSpec{}, diagnostic("reference_not_found", "Author path not found", "")
			}
			siblings = x.childNames[found]
		}
		return x.definition.nodes[found], nil
	}
	if by != "id" {
		return NodeSpec{}, invalid("Lookup selector must be id or path")
	}
	n, ok := x.definition.nodes[identity]
	if !ok {
		return NodeSpec{}, diagnostic("reference_not_found", "Node identity not found", "")
	}
	return n, nil
}
func (x *GraphIndex) Path(id string) (string, error) {
	if _, err := x.Get(id, "id"); err != nil {
		return "", err
	}
	segments := []string{}
	for current := id; current != ""; current = x.locations[current].ParentID {
		segments = append(segments, x.definition.nodes[current].name)
	}
	slices.Reverse(segments)
	return "/" + strings.Join(segments, "/"), nil
}
func (x *GraphIndex) Walk(plane Plane) ([]NodeSpec, error) {
	if x == nil || x.definition == nil {
		return nil, invalid("Invalid graph index")
	}
	if plane != "" && plane != ModelPlane && plane != PromptPlane {
		return nil, invalid("Invalid plane")
	}
	out := []NodeSpec{}
	for _, id := range x.preorder {
		if plane == "" || x.locations[id].Plane == plane {
			out = append(out, x.definition.nodes[id])
		}
	}
	return out, nil
}
func (x *GraphIndex) Subtree(id string, includeSelf bool) ([]NodeSpec, error) {
	if _, err := x.Get(id, "id"); err != nil {
		return nil, err
	}
	loc := x.locations[id]
	start := loc.Preorder
	if !includeSelf {
		start++
	}
	out := make([]NodeSpec, 0, loc.SubtreeEnd-start)
	for _, child := range x.preorder[start:loc.SubtreeEnd] {
		out = append(out, x.definition.nodes[child])
	}
	return out, nil
}
func (x *GraphIndex) ModelMayOpen(id string) (bool, error) {
	if _, err := x.Get(id, "id"); err != nil {
		return false, err
	}
	return x.locations[id].Plane == ModelPlane && !x.reserved[id], nil
}
func (x *GraphIndex) Presentation(id, locale string) (Presentation, bool, error) {
	if _, err := x.Get(id, "id"); err != nil {
		return Presentation{}, false, err
	}
	normalized, err := NormalizeLocale(locale)
	if err != nil {
		return Presentation{}, false, err
	}
	candidate := strings.ToLower(normalized)
	truncate := func(s string) string {
		i := strings.LastIndexByte(s, '-')
		if i < 0 {
			return ""
		}
		return s[:i]
	}
	for candidate != "" {
		if p, ok := x.catalogs[candidate][id]; ok {
			return p, true, nil
		}
		candidate = truncate(candidate)
		parts := strings.Split(candidate, "-")
		if len(parts[len(parts)-1]) == 1 {
			candidate = truncate(candidate)
		}
	}
	p, ok := x.catalogs[strings.ToLower(x.definition.presentations.defaultLocale)][id]
	return p, ok, nil
}
