package model

import (
	"errors"
	"fmt"
	"strings"
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
	if index == nil || index.bound {
		return nil, errors.New("disclosure-only navigation requires an unbound Index")
	}
	return newDisclosure(index, selection, false, NewMemoryTelemetry())
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

// EffectiveSelection applies the view's ceiling to one requested root selection.
func (view *Disclosure) EffectiveSelection(requested RootSelection) (RootSelection, error) {
	selection, err := view.selection.Intersect(requested)
	if err != nil {
		return RootSelection{}, err
	}
	return selection.Resolve(view.index)
}

// Discover returns routing cards for model-visible selected roots only.
func (view *Disclosure) Discover(requested RootSelection) (map[string][]map[string]any, error) {
	selection, err := view.selection.Intersect(requested)
	if err != nil {
		return nil, err
	}
	selection, err = selection.Resolve(view.index)
	if err != nil {
		return nil, err
	}
	result := map[string][]map[string]any{"roles": {}, "skills": {}, "tools": {}}
	for _, root := range view.index.ModelRoots() {
		ref, _ := view.index.RefOf(root)
		if !selection.ContainsRef(ref) {
			continue
		}
		key := string(root.nodeKind()) + "s"
		result[key] = append(result[key], view.card(root, true))
	}
	return result, nil
}

// Open discloses one model-reachable node and one sibling level.
func (view *Disclosure) Open(ref string, requested RootSelection) (map[string]any, error) {
	selection, err := view.selection.Intersect(requested)
	if err != nil {
		return nil, err
	}
	selection, err = selection.Resolve(view.index)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	if _, prompt := view.promptRoots[strings.Split(ref, "/")[0]]; prompt {
		return nil, fmt.Errorf("%s is opened by a person, not by an agent", ref)
	}
	node, err := view.resolve(ref, selection, view.index.ModelRoots())
	if err != nil {
		return nil, err
	}
	result := view.active(node, selection)
	view.reportOpen(ref, node)
	return result, nil
}

// OpenForPerson resolves through both ordinary and Prompt roots.
func (view *Disclosure) OpenForPerson(ref string, requested RootSelection) (map[string]any, error) {
	selection, err := view.selection.Intersect(requested)
	if err != nil {
		return nil, err
	}
	selection, err = selection.Resolve(view.index)
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
	result := view.active(node, selection)
	view.reportOpen(ref, node)
	return result, nil
}

func (view *Disclosure) reportOpen(ref string, node Node) {
	if node.nodeKind() == RoleKind || node.nodeKind() == SkillKind {
		reportTelemetry(view.telemetry, CallEvent{Ref: ref})
	}
}

func (view *Disclosure) card(node Node, bound bool) map[string]any {
	ref, _ := view.index.RefOf(node)
	card := map[string]any{"kind": string(node.nodeKind()), "name": node.nodeName(), "description": node.nodeDescription(), "ref": ref}
	if tool, ok := node.(*Tool); ok {
		card["read_only"] = tool.ReadOnly
		if bound && view.bound {
			if binding, err := tool.Binding(); err == nil {
				card["input_schema"] = binding.Schema()
			}
		}
	}
	return card
}

func (view *Disclosure) active(node Node, selection RootSelection) map[string]any {
	card := view.card(node, true)
	switch typed := node.(type) {
	case *Role:
		card["instructions"] = typed.Instructions
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
	case *Skill:
		card["instructions"] = typed.Instructions
		if len(typed.Uses) > 0 {
			uses := []map[string]any{}
			for _, ref := range typed.Uses {
				if selection.ContainsRef(ref) {
					target, _ := view.index.Find(ref)
					uses = append(uses, view.card(target, true))
				}
			}
			card["uses"] = uses
		}
	}
	return card
}

func (view *Disclosure) resolve(ref string, selection RootSelection, roots []Node) (Node, error) {
	// Index is the single owner of lookup facts. Keeping its typed diagnostic
	// intact lets the gateway render an agent recovery while other Hosts can
	// still classify the reason with errors.As/errors.Is.
	node, err := view.index.Find(ref)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		rootRef, _ := view.index.RefOf(root)
		if selection.ContainsRef(rootRef) && strings.Split(ref, "/")[0] == rootRef {
			return node, nil
		}
	}
	return nil, view.index.lookupFailure(ref)
}
