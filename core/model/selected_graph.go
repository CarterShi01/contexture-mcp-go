package model

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// NodeRef is one canonical node encountered during a selected graph walk.
// Go returns this named pair where Python's iterator yields a tuple.
type NodeRef struct {
	Ref  string
	Node Node
}

// SelectedGraph is a read-only graph facade that cannot enumerate or resolve
// a node excluded from the current request surface.
type SelectedGraph struct {
	index     *Index
	selection RootSelection
}

// NewSelectedGraph validates and creates a path-projected graph facade.
func NewSelectedGraph(index *Index, selection RootSelection) (*SelectedGraph, error) {
	if index == nil {
		return nil, selectionError("a selected graph needs a compiled Index")
	}
	selection, err := selection.Resolve(index)
	if err != nil {
		return nil, err
	}
	return &SelectedGraph{index: index, selection: selection}, nil
}

// Index returns the immutable compiled graph this facade projects.
func (graph *SelectedGraph) Index() *Index {
	if graph == nil {
		return nil
	}
	return graph.index
}

// Selection returns the immutable surface projection serving this request.
func (graph *SelectedGraph) Selection() RootSelection {
	if graph == nil {
		return AllRoots()
	}
	return graph.selection
}

// Roots returns selected surface roots in Index declaration order. A selected
// descendant is promoted without exposing its parent or siblings.
func (graph *SelectedGraph) Roots() []Node {
	if graph == nil || graph.index == nil {
		return nil
	}
	result := make([]Node, 0)
	if graph.selection.IsAll() {
		for _, root := range graph.index.roots {
			ref := graph.index.refByNode[root]
			result = append(result, cloneNode(root, graph.index, ref))
		}
		return result
	}
	anchors := map[string]struct{}{}
	for _, ref := range graph.selection.Names() {
		anchors[ref] = struct{}{}
	}
	for _, ref := range graph.index.order {
		if _, selected := anchors[ref]; selected {
			result = append(result, cloneNode(graph.index.byRef[ref], graph.index, ref))
		}
	}
	return result
}

// Walk returns selected addresses in canonical Index declaration order.
func (graph *SelectedGraph) Walk() []string {
	if graph == nil || graph.index == nil {
		return nil
	}
	result := make([]string, 0)
	for _, ref := range graph.index.order {
		if graph.selection.ContainsRef(ref) {
			result = append(result, ref)
		}
	}
	return result
}

// NodesWithRefs returns selected nodes and their canonical refs in order.
func (graph *SelectedGraph) NodesWithRefs() []NodeRef {
	if graph == nil || graph.index == nil {
		return nil
	}
	result := make([]NodeRef, 0)
	for _, ref := range graph.Walk() {
		result = append(result, NodeRef{Ref: ref, Node: cloneNode(graph.index.byRef[ref], graph.index, ref)})
	}
	return result
}

// Find resolves an address only when its root is selected.
func (graph *SelectedGraph) Find(ref string) (Node, error) {
	if graph == nil || graph.index == nil {
		return nil, selectionError("a selected graph needs a compiled Index")
	}
	if err := graph.selection.RequireRef(ref); err != nil {
		return nil, err
	}
	return graph.index.Find(ref)
}

// RefOf returns a node's canonical ref only when it is selected.
func (graph *SelectedGraph) RefOf(node Node) (string, error) {
	if graph == nil || graph.index == nil {
		return "", selectionError("a selected graph needs a compiled Index")
	}
	ref, err := graph.index.RefOf(node)
	if err != nil {
		return "", err
	}
	if err := graph.selection.RequireRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

// ParentOf resolves a selected node's containment parent.
func (graph *SelectedGraph) ParentOf(node Node) (*Role, error) {
	ref, err := graph.RefOf(node)
	if err != nil {
		return nil, err
	}
	for _, anchor := range graph.selection.Names() {
		if anchor == ref {
			return nil, nil
		}
	}
	parent, err := graph.index.ParentOf(node)
	if err != nil || parent == nil {
		return parent, err
	}
	parentRef, err := graph.index.RefOf(parent)
	if err != nil {
		return nil, err
	}
	if err := graph.selection.RequireRef(parentRef); err != nil {
		return nil, err
	}
	return parent, nil
}

// ChildrenOf resolves immediate selected children in declaration order.
func (graph *SelectedGraph) ChildrenOf(node Node) ([]Node, error) {
	if _, err := graph.RefOf(node); err != nil {
		return nil, err
	}
	children, err := graph.index.ChildrenOf(node)
	if err != nil {
		return nil, err
	}
	result := make([]Node, 0, len(children))
	for _, child := range children {
		ref, refErr := graph.index.RefOf(child)
		if refErr == nil && graph.selection.ContainsRef(ref) {
			result = append(result, child)
		}
	}
	return result, nil
}

// UsesOf returns selected dependency refs for one selected source node.
func (graph *SelectedGraph) UsesOf(ref string) ([]string, error) {
	if err := graph.require(ref); err != nil {
		return nil, err
	}
	uses, err := graph.index.UsesOf(ref)
	if err != nil {
		return nil, err
	}
	return graph.filterRefs(uses), nil
}

// DependentsOf returns selected source refs for one selected target node.
func (graph *SelectedGraph) DependentsOf(ref string) ([]string, error) {
	if err := graph.require(ref); err != nil {
		return nil, err
	}
	dependents, err := graph.index.DependentsOf(ref)
	if err != nil {
		return nil, err
	}
	return graph.filterRefs(dependents), nil
}

// MatchingRefs ranks selected refs with the same rank, length, and lexical
// ordering as Index completion. total is the count before the requested limit.
func (graph *SelectedGraph) MatchingRefs(value string, limit int) ([]string, int) {
	return matchingRefs(graph.Walk(), value, limit)
}

func matchingRefs(refs []string, value string, limit int) ([]string, int) {
	wanted := strings.ToLower(strings.TrimSpace(value))
	type candidate struct {
		rank, length int
		ref          string
	}
	scored := []candidate{}
	for _, ref := range refs {
		lowered := strings.ToLower(ref)
		rank := -1
		switch {
		case wanted == "" || strings.HasPrefix(lowered, wanted):
			rank = 0
		case strings.HasPrefix(lowered[strings.LastIndex(lowered, foundation.ReferenceSeparator)+len(foundation.ReferenceSeparator):], wanted):
			rank = 1
		case matchingPart(lowered, wanted):
			rank = 2
		case strings.Contains(lowered, wanted):
			rank = 3
		}
		if rank >= 0 {
			scored = append(scored, candidate{rank: rank, length: utf8.RuneCountInString(ref), ref: ref})
		}
	}
	sort.Slice(scored, func(left, right int) bool {
		if scored[left].rank != scored[right].rank {
			return scored[left].rank < scored[right].rank
		}
		if scored[left].length != scored[right].length {
			return scored[left].length < scored[right].length
		}
		return scored[left].ref < scored[right].ref
	})
	total := len(scored)
	if limit < 0 {
		limit = 0
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]string, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.ref)
	}
	return result, total
}

func (graph *SelectedGraph) require(ref string) error {
	if graph == nil || graph.index == nil {
		return selectionError("a selected graph needs a compiled Index")
	}
	return graph.selection.RequireRef(ref)
}

func (graph *SelectedGraph) filterRefs(refs []string) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		if graph.selection.ContainsRef(ref) {
			result = append(result, ref)
		}
	}
	return result
}

func matchingPart(ref, wanted string) bool {
	for _, part := range strings.Split(ref, foundation.ReferenceSeparator) {
		if strings.HasPrefix(part, wanted) {
			return true
		}
	}
	return false
}

func (graph *SelectedGraph) String() string {
	if graph == nil {
		return "<nil SelectedGraph>"
	}
	return fmt.Sprintf("SelectedGraph(%v)", graph.selection.Names())
}
