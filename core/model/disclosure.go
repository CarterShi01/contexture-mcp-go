package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// Disclosure is a pure progressive navigation projection over one Index.
type Disclosure struct {
	index       *Index
	selection   RootSelection
	promptRoots map[string]struct{}
	bound       bool
	telemetry   Telemetry
}

// NewDisclosure creates a model navigation view. Prompt roots remain person-reachable only.
func NewDisclosure(index *Index, selection RootSelection) (*Disclosure, error) {
	if index == nil || !index.bound {
		return nil, errors.New("runtime Disclosure requires a bound Index")
	}
	return newDisclosure(index, selection, true, NewMemoryTelemetry())
}

// NewDisclosureWithTelemetry creates model navigation sharing one collector
// with an execution Runtime compiled for the same application.
func NewDisclosureWithTelemetry(index *Index, selection RootSelection, telemetry Telemetry) (*Disclosure, error) {
	if index == nil || !index.bound {
		return nil, errors.New("runtime Disclosure requires a bound Index")
	}
	if telemetry == nil {
		telemetry = NewMemoryTelemetry()
	}
	return newDisclosure(index, selection, true, telemetry)
}

// NewDisclosureOnly creates navigation that intentionally exposes no Tool schemas.
// It is used by a disclosure-only Host with no execution bindings or Resources.
func NewDisclosureOnly(index *Index, selection RootSelection) (*Disclosure, error) {
	return NewDisclosureOnlyWithTelemetry(index, selection, nil)
}

// NewDisclosureOnlyWithTelemetry creates structural navigation sharing one
// collector with its compiled Host container.
func NewDisclosureOnlyWithTelemetry(index *Index, selection RootSelection, telemetry Telemetry) (*Disclosure, error) {
	if index == nil || index.bound {
		return nil, errors.New("disclosure-only navigation requires an unbound Index")
	}
	if telemetry == nil {
		telemetry = NewMemoryTelemetry()
	}
	return newDisclosure(index, selection, false, telemetry)
}

func newDisclosure(index *Index, selection RootSelection, bound bool, telemetry Telemetry) (*Disclosure, error) {
	selection, err := selection.Resolve(index)
	if err != nil {
		return nil, err
	}
	prompts := map[string]struct{}{}
	for _, node := range index.PromptRoots() {
		ref, _ := index.RefOf(node)
		prompts[ref] = struct{}{}
	}
	return &Disclosure{index: index, selection: selection, promptRoots: prompts, bound: bound, telemetry: telemetry}, nil
}

// Index returns the immutable canonical graph projected by this Disclosure.
func (view *Disclosure) Index() *Index { return view.index }

// Unrestricted removes Prompt-only model ownership while retaining the exact
// selected surface and all other immutable disclosure facts.
func (view *Disclosure) Unrestricted() *Disclosure {
	if view == nil {
		return nil
	}
	return &Disclosure{
		index:       view.index,
		selection:   view.selection,
		promptRoots: map[string]struct{}{},
		bound:       view.bound,
		telemetry:   view.telemetry,
	}
}

// RefOf returns the canonical address for a Node held by this View.
func (view *Disclosure) RefOf(node Node) (string, error) { return view.index.RefOf(node) }

// CardOf renders one policy-aware route card for a held Node.
func (view *Disclosure) CardOf(node Node) (CompiledContext, error) {
	return CardOf(node, view)
}

// RoutingCardOf renders one pure openable card without execution facts.
func (view *Disclosure) RoutingCardOf(node Node) (CompiledContext, error) {
	return RoutingCardOf(node, view)
}

// CardFor renders one selected, model-visible dependency by canonical ref.
func (view *Disclosure) CardFor(ref string) (CompiledContext, error) {
	if !view.selection.ContainsRef(ref) {
		return nil, &RootOutsideSelectionError{Ref: ref}
	}
	root := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
	if _, prompt := view.promptRoots[root]; prompt {
		return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("%q belongs to a Prompt-only root and has no model routing card", ref))
	}
	node, err := view.index.Find(ref)
	if err != nil {
		return nil, err
	}
	return view.CardOf(node)
}

// CardsOf renders a grouped sibling set after applying this View's policy.
func (view *Disclosure) CardsOf(nodes []Node) (CompiledContext, error) {
	visible := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		ref, err := view.index.RefOf(node)
		if err != nil {
			return nil, err
		}
		root := strings.Split(ref, foundation.ReferenceSeparator)[0]
		if view.selection.ContainsRef(ref) {
			if _, prompt := view.promptRoots[root]; !prompt {
				visible = append(visible, node)
			}
		}
	}
	return GroupCards(visible, view)
}

// RoutingCardsOf renders selected model-visible siblings without execution facts.
func (view *Disclosure) RoutingCardsOf(nodes []Node) (CompiledContext, error) {
	visible := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		ref, err := view.index.RefOf(node)
		if err != nil {
			return nil, err
		}
		root := strings.Split(ref, foundation.ReferenceSeparator)[0]
		if view.selection.ContainsRef(ref) {
			if _, prompt := view.promptRoots[root]; !prompt {
				visible = append(visible, node)
			}
		}
	}
	return GroupRoutingCards(visible, view)
}

// CardsFor renders selected model-visible dependency cards in declaration order.
func (view *Disclosure) CardsFor(refs []string) ([]CompiledContext, error) {
	cards := make([]CompiledContext, 0, len(refs))
	for _, ref := range refs {
		if !view.selection.ContainsRef(ref) {
			continue
		}
		root := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
		if _, prompt := view.promptRoots[root]; prompt {
			continue
		}
		card, err := view.CardFor(ref)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// RoutingCardsFor renders selected uses as pure, non-executable cards.
func (view *Disclosure) RoutingCardsFor(refs []string) ([]CompiledContext, error) {
	cards := make([]CompiledContext, 0, len(refs))
	for _, ref := range refs {
		if !view.selection.ContainsRef(ref) {
			continue
		}
		root := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
		if _, prompt := view.promptRoots[root]; prompt {
			continue
		}
		node, err := view.index.Find(ref)
		if err != nil {
			return nil, err
		}
		card, err := view.RoutingCardOf(node)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// Inspect compares an already validated shortlist without activating nodes.
func (view *Disclosure) Inspect(refs []string, requested RootSelection) (CompiledContext, error) {
	selection, err := view.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	projection := *view
	projection.selection = selection
	items := make([]CompiledContext, 0, len(refs))
	for _, ref := range refs {
		if err := selection.RequireRef(ref); err != nil {
			return nil, err
		}
		node, err := view.resolve(ref, selection, view.index.ModelRoots())
		if err != nil {
			return nil, err
		}
		item, err := CompileNode(node, InspectCompileLevel, &projection)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return CompiledContext{
		"notice": "These are routing cards for candidate evaluation. No Role or Skill has been activated, no instructions or execution facets are disclosed, and no Tool has been invoked.",
		"items":  items,
	}, nil
}

// ExecutionOf exposes callable Tool facts only for a bound Index.
func (view *Disclosure) ExecutionOf(node Node) (CompiledContext, error) {
	tool, ok := node.(*Tool)
	if !ok {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("only a Tool has an executable disclosure facet"))
	}
	if !view.bound {
		return CompiledContext{}, nil
	}
	schema, err := view.SchemaOf(tool)
	if err != nil {
		return nil, err
	}
	return CompiledContext{"read_only": tool.ReadOnly, "input_schema": schema}, nil
}

// SchemaOf returns one bound Tool's defensive input schema.
func (view *Disclosure) SchemaOf(node Node) (map[string]any, error) {
	return view.index.SchemaOf(node)
}

// EffectiveSelection applies the view's ceiling to one requested root selection.
func (view *Disclosure) EffectiveSelection(requested RootSelection) (RootSelection, error) {
	requested, err := requested.Resolve(view.index)
	if err != nil {
		return RootSelection{}, err
	}
	selection, err := view.selection.Intersect(requested)
	if err != nil {
		return RootSelection{}, err
	}
	return selection.Resolve(view.index)
}

// Discover returns routing cards for model-visible selected roots only.
func (view *Disclosure) Discover(requested RootSelection) (map[string][]map[string]any, error) {
	selection, err := view.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	result := map[string][]map[string]any{"roles": {}, "skills": {}, "tools": {}}
	graph, err := NewSelectedGraph(view.index, selection)
	if err != nil {
		return nil, err
	}
	for _, root := range graph.Roots() {
		ref, _ := view.index.RefOf(root)
		rootRef := strings.Split(ref, foundation.ReferenceSeparator)[0]
		if _, prompt := view.promptRoots[rootRef]; prompt {
			continue
		}
		key := string(root.nodeKind()) + "s"
		result[key] = append(result[key], view.card(root, true))
	}
	return result, nil
}

// Open discloses one model-reachable node and one sibling level.
func (view *Disclosure) Open(ref string, requested RootSelection) (map[string]any, error) {
	selection, err := view.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	if _, prompt := view.promptRoots[strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]]; prompt {
		return nil, &RefusedError{Message: TakenByPersonMessage(ref)}
	}
	roots := view.index.ModelRoots()
	if len(view.promptRoots) == 0 {
		roots = view.index.Roots()
	}
	node, err := view.resolve(ref, selection, roots)
	if err != nil {
		return nil, err
	}
	result, err := view.active(node, selection)
	if err != nil {
		return nil, err
	}
	view.reportOpen(ref, node)
	return result, nil
}

// OpenForPerson resolves through both ordinary and Prompt roots.
func (view *Disclosure) OpenForPerson(ref string, requested RootSelection) (map[string]any, error) {
	selection, err := view.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	node, err := view.resolve(ref, selection, view.index.Roots())
	if err != nil {
		return nil, err
	}
	result, err := view.active(node, selection)
	if err != nil {
		return nil, err
	}
	view.reportOpen(ref, node)
	return result, nil
}

func (view *Disclosure) reportOpen(ref string, node Node) {
	if node.nodeKind() == RoleKind || node.nodeKind() == SkillKind {
		reportTelemetry(view.telemetry, CallEvent{Ref: ref})
	}
}

func (view *Disclosure) card(node Node, bound bool) map[string]any {
	card, _ := RouteOf(node)
	ref, _ := view.index.RefOf(node)
	card["ref"] = ref
	// Execution facts are meaningful only when this disclosure is backed by a
	// bound Index. A disclosure-only Host intentionally gives agents structural
	// routing cards, not a claim that a Tool can be invoked or is read-only.
	if tool, ok := node.(*Tool); ok && bound && view.bound {
		card["read_only"] = tool.ReadOnly
		if binding, err := tool.Binding(); err == nil {
			card["input_schema"] = binding.Schema()
		}
	}
	return card
}

func (view *Disclosure) active(node Node, selection RootSelection) (map[string]any, error) {
	card := view.card(node, true)
	switch typed := node.(type) {
	case *Role:
		if err := view.activeRole(card, typed, node, selection); err != nil {
			return nil, err
		}
	case *PreProcess:
		if err := view.activeRole(card, (*Role)(typed), node, selection); err != nil {
			return nil, err
		}
	case *PostProcess:
		if err := view.activeRole(card, (*Role)(typed), node, selection); err != nil {
			return nil, err
		}
	case *Skill:
		card["instructions"] = typed.Instructions
		view.addUses(card, typed.Uses, selection)
	case *Tool:
		view.addUses(card, typed.Uses, selection)
	}
	return card, nil
}

func (view *Disclosure) activeRole(card map[string]any, role *Role, node Node, selection RootSelection) error {
	card["instructions"] = role.Instructions
	card["roles"] = []map[string]any{}
	card["skills"] = []map[string]any{}
	card["tools"] = []map[string]any{}
	children, _ := view.index.ChildrenOf(node)
	for _, child := range children {
		ref, _ := view.index.RefOf(child)
		if !selection.ContainsRef(ref) {
			continue
		}
		key := string(child.nodeKind()) + "s"
		card[key] = append(card[key].([]map[string]any), view.card(child, true))
	}
	view.addUses(card, role.Uses, selection)
	return addProcessDetails(card, role, view)
}

// addUses projects declared dependency targets in declaration order. A
// selection can only attenuate this view: excluded targets are omitted rather
// than exposing another root through a Role, Skill, or Tool that happens to
// use it. Targets are route cards, so Uses never recursively disclose a cycle.
func (view *Disclosure) addUses(card map[string]any, refs []string, selection RootSelection) {
	if len(refs) == 0 {
		return
	}
	uses := []map[string]any{}
	for _, ref := range refs {
		if !selection.ContainsRef(ref) {
			continue
		}
		root := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
		if _, prompt := view.promptRoots[root]; prompt {
			continue
		}
		target, _ := view.index.Find(ref)
		uses = append(uses, view.card(target, true))
	}
	card["uses"] = uses
}

func (view *Disclosure) resolve(ref string, selection RootSelection, roots []Node) (Node, error) {
	// Index is the single owner of lookup facts. Keeping its typed diagnostic
	// intact lets the gateway render an agent recovery while other Hosts can
	// still classify the reason with errors.As/errors.Is.
	node, err := view.index.Find(ref)
	if err != nil {
		return nil, err
	}
	topLevel := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)[0]
	for _, root := range roots {
		rootRef, _ := view.index.RefOf(root)
		if rootRef == topLevel && selection.ContainsRef(ref) {
			return node, nil
		}
	}
	return nil, view.index.lookupFailure(ref)
}
