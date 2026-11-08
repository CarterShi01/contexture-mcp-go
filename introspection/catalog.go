package introspection

import (
	"reflect"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/model"
	"github.com/CarterShi01/contexture-mcp-go/core/presentation"
	"golang.org/x/text/cases"
)

// NodeMetadataContributor is explicitly trusted application code. Describe is
// called once per included node, never per lookup. It receives detached native
// declarations and the same immutable Index, not a runtime/lifecycle adapter.
type NodeMetadataContributor interface {
	Namespace() string
	Describe(ref string, node model.Node, index *model.Index) (map[string]any, error)
}

type CatalogOptions struct {
	View             CatalogView
	Detail           CatalogDetail
	Locale           *string
	SurfaceSelection *model.SurfaceSelection
	Contributors     []NodeMetadataContributor
}

// ProjectionOperations reports actual Catalog adapter work, including direct
// native lookup calls (not model-internal lookups), excluding authored JSON/text
// size. It makes linear graph projection bounds directly testable.
type ProjectionOperations struct {
	Walks, Entries, ParentLookups, RefLookups, SelectionLookups, MemberVisits, EdgeVisits int
	NativeLookups                                                                         int
}

type NodeCatalog struct {
	snapshot   *NodeCatalogSnapshot
	byRef      map[string]int
	operations ProjectionOperations
}

type indexedNode struct {
	ref, parent, root string
	node              model.Node
	members           []model.Node
}

// New constructs an explicit read model, ignoring all request-local graph and
// selection facts. View is required; Detail defaults to summary.
func New(index *model.Index, options CatalogOptions) (*NodeCatalog, error) {
	if index == nil || !validView(options.View) {
		return nil, invalid("a native Index and explicit Catalog view are required")
	}
	if options.Detail == "" {
		options.Detail = SummaryDetail
	}
	if !validDetail(options.Detail) || (options.View == SurfaceView) != (options.SurfaceSelection != nil) {
		return nil, invalid("valid detail and explicit surface-only selection required")
	}
	contributors := append([]NodeMetadataContributor{}, options.Contributors...)
	namespaces := make([]string, len(contributors))
	seen := map[string]bool{}
	for i, contributor := range contributors {
		if contributor == nil || reflect.ValueOf(contributor).Kind() == reflect.Pointer && reflect.ValueOf(contributor).IsNil() {
			return nil, invalid("nil Catalog contributor")
		}
		namespace := contributor.Namespace()
		if !validNamespace(namespace) || seen[namespace] {
			return nil, invalid("invalid or duplicate Catalog contributor namespace")
		}
		seen[namespace], namespaces[i] = true, namespace
	}
	locale, copy, err := presentation.ResolvePresentation(index.Presentation(), options.Locale)
	if err != nil {
		return nil, err
	}
	var disclosure *model.Disclosure
	if index.Bound() {
		disclosure, err = model.NewDisclosure(index, model.AllSurfaces())
	} else {
		disclosure, err = model.NewDisclosureOnly(index, model.AllSurfaces())
	}
	if err != nil {
		return nil, err
	}
	disclosure, err = disclosure.WithLocale(options.Locale)
	if err != nil {
		return nil, err
	}
	disclosure = disclosure.Unrestricted()
	stats := ProjectionOperations{Walks: 1}
	entries := index.NodesWithRefs()
	rows := make([]*indexedNode, len(entries))
	byRef := map[string]*indexedNode{}
	for i, entry := range entries {
		stats.Entries++
		stats.ParentLookups++
		stats.NativeLookups++
		parent, err := index.ParentOf(entry.Node)
		if err != nil {
			return nil, err
		}
		row := &indexedNode{ref: entry.Ref, node: entry.Node, root: entry.Ref, members: []model.Node{}}
		if parent != nil {
			stats.RefLookups++
			stats.NativeLookups++
			row.parent, err = index.RefOf(parent)
			if err != nil {
				return nil, err
			}
			owner := byRef[row.parent]
			if owner == nil {
				return nil, invalid("native Index must be in containment preorder")
			}
			row.root = owner.root
			owner.members = append(owner.members, entry.Node)
			stats.MemberVisits++
		}
		rows[i], byRef[entry.Ref] = row, row
	}
	promptRoots := map[string]bool{}
	for _, root := range index.PromptRoots() {
		stats.RefLookups++
		stats.NativeLookups++
		ref, err := index.RefOf(root)
		if err != nil {
			return nil, err
		}
		promptRoots[ref] = true
	}
	selected, err := resolveSelection(options.SurfaceSelection, rows, byRef, &stats)
	if err != nil {
		return nil, err
	}
	included, visible := map[string]bool{}, map[string]bool{}
	roots := []string{}
	ordinals := map[string]int{}
	// Preorder parent propagation canonicalizes selected root prefixes into an
	// antichain in one cut; there is no per-anchor sibling/ancestor scan.
	for _, row := range rows {
		stats.Entries++
		stats.SelectionLookups += 2
		active := selected == nil || selected[row.ref] || visible[row.parent]
		visible[row.ref] = active
		if !active || options.View != ApplicationView && promptRoots[row.root] {
			continue
		}
		included[row.ref] = true
		if row.parent == "" || !included[row.parent] {
			ordinals[row.ref] = len(roots)
			roots = append(roots, row.ref)
		}
	}
	active := &catalogView{base: disclosure, index: index, included: included, schemas: map[string]map[string]any{}, stats: &stats}
	nodes := []NodeDescriptor{}
	counts := Counts{}
	for _, row := range rows {
		stats.Entries++
		if !included[row.ref] {
			continue
		}
		node := NodeDescriptor{
			Ref: row.ref, Kind: row.node.Kind(), Name: row.node.NodeName(), Description: row.node.NodeDescription(),
			Plane: "model", Ordinal: ordinals[row.ref], Members: []MemberDescriptor{}, Uses: []string{},
			Dependents: []string{}, Extensions: map[string]any{},
		}
		if promptRoots[row.root] {
			node.Plane = "prompt"
		}
		if included[row.parent] {
			node.ParentRef = ptrCopy(&row.parent)
		}
		if label, exists := copy.Node(row.ref); exists {
			node.Presentation = &PresentationDescriptor{Title: label.Title()}
			if description := label.Description(); description != "" {
				node.Presentation.Description = &description
			}
		}
		for position, member := range row.members {
			stats.MemberVisits++
			stats.RefLookups++
			stats.NativeLookups++
			ref, err := index.RefOf(member)
			if err != nil {
				return nil, err
			}
			if !included[ref] {
				continue
			}
			slot := memberSlot(row.node, member, position)
			ordinals[ref] = len(node.Members)
			node.Members = append(node.Members, MemberDescriptor{Ref: ref, Kind: member.Kind(), Slot: slot})
		}
		for _, ref := range row.node.NodeUses() {
			stats.EdgeVisits++
			if included[ref] {
				node.Uses = append(node.Uses, ref)
			}
		}
		stats.NativeLookups++
		dependents, err := index.DependentsOf(row.ref)
		if err != nil {
			return nil, err
		}
		for _, ref := range dependents {
			stats.EdgeVisits++
			if included[ref] {
				node.Dependents = append(node.Dependents, ref)
			}
		}
		for i, contributor := range contributors {
			// Fetch a fresh native snapshot so a trusted contributor cannot
			// mutate another contributor's input or alter the authored program.
			stats.NativeLookups++
			native, err := index.Find(row.ref)
			if err != nil {
				return nil, err
			}
			metadata, err := contributor.Describe(row.ref, native, index)
			if err != nil {
				return nil, err
			}
			if metadata != nil {
				frozen, err := detachedObject(metadata)
				if err != nil {
					return nil, err
				}
				node.Extensions[namespaces[i]] = frozen
			}
		}
		if options.Detail == DefinitionDetail {
			switch node.Kind {
			case model.RoleKind, model.SkillKind:
				opened, err := model.CompileNodeWithMembers(row.node, model.ActiveCompileLevel, active, row.members)
				if err != nil {
					return nil, err
				}
				effective, ok := opened["instructions"].(string)
				if !ok {
					return nil, invalid("native ACTIVE program must be text")
				}
				if node.Kind == model.RoleKind {
					node.Role = &RoleDefinition{AuthoredInstructions: authored(row.node), EffectiveInstructions: effective}
					for _, member := range node.Members {
						if member.Slot == PreProcessSlot {
							node.Role.PreProcessRef = ptrCopy(&member.Ref)
						}
						if member.Slot == PostProcessSlot {
							node.Role.PostProcessRef = ptrCopy(&member.Ref)
						}
					}
				} else {
					node.Skill = &SkillDefinition{AuthoredInstructions: authored(row.node), EffectiveInstructions: effective}
				}
			case model.ToolKind:
				node.Tool = &ToolDefinition{ExecutionAvailable: index.Bound()}
				if index.Bound() {
					readOnly := row.node.(*model.Tool).ReadOnly
					node.Tool.ReadOnly = &readOnly
					node.Tool.InputSchema, err = active.SchemaOf(row.node)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		switch node.Kind {
		case model.RoleKind:
			counts.Role++
		case model.SkillKind:
			counts.Skill++
		case model.ToolKind:
			counts.Tool++
		}
		nodes = append(nodes, node)
	}
	mode := "disclosure-only"
	if index.Bound() {
		mode = "runtime"
	}
	application := ApplicationDescriptor{Name: index.Name(), Mode: mode, View: options.View, Detail: options.Detail, ExecutionBound: index.Bound()}
	if locale != "" {
		application.Locale = &locale
	}
	snapshot, err := NewSnapshot(SnapshotData{Application: application, Roots: roots, Counts: counts, Nodes: nodes})
	if err != nil {
		return nil, err
	}
	positions := map[string]int{}
	for i, node := range snapshot.data.Nodes {
		positions[node.Ref] = i
	}
	return &NodeCatalog{snapshot: snapshot, byRef: positions, operations: stats}, nil
}

func resolveSelection(selection *model.SurfaceSelection, rows []*indexedNode, byRef map[string]*indexedNode, stats *ProjectionOperations) (map[string]bool, error) {
	if selection == nil || selection.IsAll() {
		return nil, nil
	}
	result := map[string]bool{}
	unknown := []string{}
	for _, selector := range selection.Selectors() {
		stats.SelectionLookups++
		switch {
		case selector == "*":
			for _, row := range rows {
				stats.SelectionLookups++
				if row.parent == "" {
					result[row.ref] = true
				}
			}
		case strings.HasSuffix(selector, "/*"):
			parent := byRef[strings.TrimSuffix(selector, "/*")]
			if parent == nil || len(parent.members) == 0 {
				unknown = append(unknown, selector)
				continue
			}
			for _, node := range parent.members {
				stats.SelectionLookups++
				stats.NativeLookups++
				ref, err := node.Ref()
				if err != nil {
					return nil, err
				}
				result[ref] = true
			}
		default:
			if byRef[selector] == nil {
				unknown = append(unknown, selector)
			} else {
				result[selector] = true
			}
		}
	}
	if len(unknown) != 0 {
		return nil, &model.SurfaceSelectionError{Message: "unknown or empty Contexture selector: " + strings.Join(unknown, ", ")}
	}
	return result, nil
}

func asRole(node model.Node) *model.Role {
	switch value := node.(type) {
	case *model.Role:
		return value
	case *model.PreProcess:
		return (*model.Role)(value)
	case *model.PostProcess:
		return (*model.Role)(value)
	default:
		return nil
	}
}
func authored(node model.Node) string {
	if role := asRole(node); role != nil {
		return role.Instructions
	}
	return node.(*model.Skill).Instructions
}
func memberSlot(parent, member model.Node, ordinal int) MemberSlot {
	role := asRole(parent)
	if role.PreProcess != nil && ordinal == 0 {
		return PreProcessSlot
	}
	postOrdinal := len(role.Children)
	if role.PreProcess != nil {
		postOrdinal++
	}
	if role.PostProcess != nil && ordinal == postOrdinal {
		return PostProcessSlot
	}
	switch member.Kind() {
	case model.SkillKind:
		return SkillSlot
	case model.ToolKind:
		return ToolSlot
	default:
		return ChildSlot
	}
}

type catalogView struct {
	base     *model.Disclosure
	index    *model.Index
	included map[string]bool
	schemas  map[string]map[string]any
	stats    *ProjectionOperations
}

func (view *catalogView) RefOf(node model.Node) (string, error) {
	view.stats.NativeLookups++
	return view.index.RefOf(node)
}
func (view *catalogView) CardOf(node model.Node) (model.CompiledContext, error) {
	return model.CardOf(node, view)
}
func (view *catalogView) PresentCard(node model.Node, card model.CompiledContext) (model.CompiledContext, error) {
	view.stats.NativeLookups++
	return view.base.PresentCard(node, card)
}
func (view *catalogView) CardFor(ref string) (model.CompiledContext, error) {
	if !view.included[ref] {
		return nil, invalid("reference is outside this Catalog view")
	}
	view.stats.NativeLookups++
	node, err := view.index.Find(ref)
	if err != nil {
		return nil, err
	}
	return view.CardOf(node)
}
func (view *catalogView) CardsOf(nodes []model.Node) (model.CompiledContext, error) {
	included := []model.Node{}
	for _, node := range nodes {
		ref, err := view.RefOf(node)
		if err != nil {
			return nil, err
		}
		if view.included[ref] {
			included = append(included, node)
		}
	}
	return model.GroupCards(included, view)
}
func (view *catalogView) CardsFor(refs []string) ([]model.CompiledContext, error) {
	result := []model.CompiledContext{}
	for _, ref := range refs {
		if !view.included[ref] {
			continue
		}
		card, err := view.CardFor(ref)
		if err != nil {
			return nil, err
		}
		result = append(result, card)
	}
	return result, nil
}
func (view *catalogView) ExecutionOf(node model.Node) (model.CompiledContext, error) {
	if !view.index.Bound() {
		return model.CompiledContext{}, nil
	}
	schema, err := view.SchemaOf(node)
	if err != nil {
		return nil, err
	}
	return model.CompiledContext{"read_only": node.(*model.Tool).ReadOnly, "input_schema": schema}, nil
}
func (view *catalogView) SchemaOf(node model.Node) (map[string]any, error) {
	ref, err := view.RefOf(node)
	if err != nil {
		return nil, err
	}
	if schema, exists := view.schemas[ref]; exists {
		return detachedObject(schema)
	}
	view.stats.NativeLookups++
	schema, err := view.index.SchemaOf(node)
	if err != nil {
		return nil, err
	}
	view.schemas[ref], err = detachedObject(schema)
	if err != nil {
		return nil, err
	}
	return detachedObject(schema)
}

func (catalog *NodeCatalog) Snapshot() *NodeCatalogSnapshot   { return catalog.snapshot }
func (catalog *NodeCatalog) Roots() []string                  { return catalog.snapshot.Roots() }
func (catalog *NodeCatalog) Operations() ProjectionOperations { return catalog.operations }
func (catalog *NodeCatalog) Get(ref string) (NodeDescriptor, error) {
	position, exists := catalog.byRef[ref]
	if !exists {
		return NodeDescriptor{}, invalid("reference is not present in this Node Catalog view")
	}
	return cloneDescriptor(catalog.snapshot.data.Nodes[position])
}
func (catalog *NodeCatalog) Children(ref string) ([]NodeDescriptor, error) {
	node, err := catalog.Get(ref)
	if err != nil {
		return nil, err
	}
	result := make([]NodeDescriptor, len(node.Members))
	for i, member := range node.Members {
		result[i], err = catalog.Get(member.Ref)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

type ListOptions struct {
	Kind  model.Kind
	Query string
}

func (catalog *NodeCatalog) List(options ListOptions) ([]NodeDescriptor, error) {
	if options.Kind != "" && !validKind(options.Kind) {
		return nil, invalid("unknown Catalog node kind")
	}
	if !utf8Valid(options.Query) {
		return nil, invalid("Catalog query requires Unicode scalar text")
	}
	fold := cases.Fold()
	wanted := foldText(fold, strip(options.Query))
	result := []NodeDescriptor{}
	for _, node := range catalog.snapshot.data.Nodes {
		if options.Kind != "" && node.Kind != options.Kind {
			continue
		}
		if wanted != "" {
			text, err := searchText(node)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(foldText(fold, text), wanted) {
				continue
			}

		}
		copy, err := cloneDescriptor(node)
		if err != nil {
			return nil, err
		}
		result = append(result, copy)
	}
	return result, nil
}
func foldText(fold cases.Caser, text string) string {
	// Unicode default case folding preserves Cherokee capitals. x/text 0.34's
	// Fold also lowercases those 86 scalars; retain them while delegating every
	// other scalar/expansion to the maintained Unicode tables.
	var result strings.Builder
	start := 0
	for offset, point := range text {
		if point >= 0x13a0 && point <= 0x13f5 {
			result.WriteString(fold.String(text[start:offset]))
			result.WriteRune(point)
			start = offset + len(string(point))
		}
	}
	if start == 0 {
		return fold.String(text)
	}
	result.WriteString(fold.String(text[start:]))
	return result.String()
}

func utf8Valid(text string) bool { return validText(text) || text == "" }
func searchText(node NodeDescriptor) (string, error) {
	fields := []string{node.Ref, node.Name, node.Description}
	fields = append(fields, node.Uses...)
	fields = append(fields, node.Dependents...)
	if node.Presentation != nil {
		fields = append(fields, node.Presentation.Title)
		if node.Presentation.Description != nil {
			fields = append(fields, *node.Presentation.Description)
		}
	}
	if node.Role != nil {
		fields = append(fields, node.Role.AuthoredInstructions, node.Role.EffectiveInstructions)
	}
	if node.Skill != nil {
		fields = append(fields, node.Skill.AuthoredInstructions, node.Skill.EffectiveInstructions)
	}
	value, err := node.AsJSON()
	if err != nil {
		return "", err
	}
	for _, key := range []string{"tool", "extensions"} {
		if key == "tool" && node.Tool == nil {
			continue
		}
		text, err := FormatJSON(value[key], JSONOptions{Spaced: true})
		if err != nil {
			return "", err
		}
		fields = append(fields, text)
	}
	return strings.Join(fields, " "), nil
}
