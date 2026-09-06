package model

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Index is the immutable canonical address view of one fresh compilation.
type Index struct {
	name        string
	roots       []Node
	modelRoots  []Node
	promptRoots []Node
	channels    Channels
	bound       bool
	byRef       map[string]Node
	refByNode   map[Node]string
	parent      map[Node]*Role
	dependents  map[string][]string
	order       []string
}

// Compile builds one fresh canonical forest from a lazy Application.
func Compile(application *Application) (*Index, error) {
	return compile(application, true)
}

func compile(application *Application, bindTools bool) (*Index, error) {
	if application == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application must not be nil"))
	}
	state := compiler{index: &Index{name: application.name, channels: application.channels, bound: bindTools, byRef: map[string]Node{}, refByNode: map[Node]string{}, parent: map[Node]*Role{}, dependents: map[string][]string{}}, active: map[uintptr]bool{}, seen: map[Node]bool{}}
	for _, factory := range application.roots {
		node, err := state.build(factory, nil, nil)
		if err != nil {
			return nil, err
		}
		state.index.modelRoots = append(state.index.modelRoots, node)
		state.index.roots = append(state.index.roots, node)
	}
	for _, factory := range application.promptRoots {
		node, err := state.build(factory, nil, nil)
		if err != nil {
			return nil, err
		}
		state.index.promptRoots = append(state.index.promptRoots, node)
		state.index.roots = append(state.index.roots, node)
	}
	for _, ref := range state.index.order {
		node := state.index.byRef[ref]
		if bindTools {
			if tool, ok := node.(*Tool); ok && tool.binding == nil {
				return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("Tool %q must have an execution Binding", ref))
			}
		}
		for _, target := range node.nodeUses() {
			if _, ok := state.index.byRef[target]; !ok {
				return nil, fmt.Errorf("%w: %q uses %q", ErrUnresolvedReference, ref, target)
			}
			state.index.dependents[target] = append(state.index.dependents[target], ref)
		}
	}
	return state.index, nil
}

// CompileDisclosure builds an independent navigation Index without execution
// Bindings. It never invokes a Tool handler or opens application Channels.
func CompileDisclosure(application *Application) (*Index, error) {
	if application == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application must not be nil"))
	}
	if application.channels != nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("a disclosure-only application cannot declare Channels"))
	}
	if len(application.resources) > 0 {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("a disclosure-only application cannot declare Resources"))
	}
	return compile(application, false)
}

type compiler struct {
	index  *Index
	active map[uintptr]bool
	seen   map[Node]bool
}

func (state *compiler) build(factory Factory, parent *Role, path []string) (Node, error) {
	if factory == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("node factory must not be nil"))
	}
	key := reflect.ValueOf(factory).Pointer()
	if state.active[key] {
		return nil, ErrContainmentCycle
	}
	state.active[key] = true
	defer delete(state.active, key)
	declaration := factory()
	if declaration == nil || (reflect.ValueOf(declaration).Kind() == reflect.Ptr && reflect.ValueOf(declaration).IsNil()) {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("node factory returned nil"))
	}
	if state.seen[declaration] {
		return nil, fmt.Errorf("%w: node %q was reused", ErrDuplicate, declaration.nodeName())
	}
	state.seen[declaration] = true
	if err := validateNode(declaration); err != nil {
		return nil, err
	}
	ref := strings.Join(append(append([]string(nil), path...), declaration.nodeName()), "/")
	if _, exists := state.index.byRef[ref]; exists {
		return nil, fmt.Errorf("%w: address %q", ErrDuplicate, ref)
	}
	node := cloneNode(declaration, state.index, ref)
	if !state.index.bound {
		if tool, ok := node.(*Tool); ok {
			tool.binding = nil
		}
	}
	state.index.byRef[ref] = node
	state.index.order = append(state.index.order, ref)
	state.index.refByNode[node] = ref
	state.index.parent[node] = parent
	role, isRole := node.(*Role)
	if !isRole {
		return node, nil
	}
	if err := state.buildGroup(role.Children, role, ref, RoleKind); err != nil {
		return nil, err
	}
	if err := state.buildGroup(role.Skills, role, ref, SkillKind); err != nil {
		return nil, err
	}
	if err := state.buildGroup(role.Tools, role, ref, ToolKind); err != nil {
		return nil, err
	}
	return node, nil
}

func (state *compiler) buildGroup(factories []Factory, parent *Role, ref string, expected Kind) error {
	for _, factory := range factories {
		child, err := state.build(factory, parent, strings.Split(ref, "/"))
		if err != nil {
			return err
		}
		if child.nodeKind() != expected {
			return fmt.Errorf("%w: role %q contains %s in %s group", ErrInvalidDeclaration, parent.Name, child.nodeKind(), expected)
		}
	}
	return nil
}

func validateNode(node Node) error {
	if strings.TrimSpace(node.nodeName()) == "" || strings.Contains(node.nodeName(), "/") {
		return errors.Join(ErrInvalidDeclaration, errors.New("node name must be non-empty and cannot contain /"))
	}
	if strings.TrimSpace(node.nodeDescription()) == "" {
		return errors.Join(ErrInvalidDeclaration, errors.New("node description must not be empty"))
	}
	if role, ok := node.(*Role); ok && strings.TrimSpace(role.Instructions) == "" {
		return errors.Join(ErrInvalidDeclaration, errors.New("role instructions must not be empty"))
	}
	if skill, ok := node.(*Skill); ok && strings.TrimSpace(skill.Instructions) == "" {
		return errors.Join(ErrInvalidDeclaration, errors.New("skill instructions must not be empty"))
	}
	seen := map[string]bool{}
	for _, ref := range node.nodeUses() {
		if strings.TrimSpace(ref) == "" || seen[ref] {
			return errors.Join(ErrInvalidDeclaration, errors.New("uses references must be unique and non-empty"))
		}
		seen[ref] = true
	}
	return nil
}

// Name returns the application name.
func (index *Index) Name() string { return index.name }

// Roots returns a defensive root copy in declaration order.
func (index *Index) Roots() []Node { return index.cloneNodes(index.roots) }

// ModelRoots returns model-visible roots in declaration order.
func (index *Index) ModelRoots() []Node { return index.cloneNodes(index.modelRoots) }

// PromptRoots returns person-controlled roots in declaration order.
func (index *Index) PromptRoots() []Node { return index.cloneNodes(index.promptRoots) }

// Find resolves one canonical address.
func (index *Index) Find(ref string) (Node, error) {
	node, ok := index.byRef[ref]
	if !ok {
		return nil, fmt.Errorf("unknown Contexture reference %q", ref)
	}
	return cloneNode(node, index, ref), nil
}

// RefOf returns a canonical address for a node compiled into this Index.
func (index *Index) RefOf(node Node) (string, error) {
	ref, ok := index.refByNode[node]
	if !ok {
		owner, candidate := nodeLocation(node)
		if owner == index {
			_, ok = index.byRef[candidate]
			ref = candidate
		}
	}
	if !ok {
		return "", errors.New("node is not registered in this Index")
	}
	return ref, nil
}

// ParentOf returns the containment owner of a node.
func (index *Index) ParentOf(node Node) (*Role, error) {
	internal, ok := index.internalNode(node)
	if !ok {
		return nil, errors.New("node is not registered in this Index")
	}
	parent := index.parent[internal]
	if parent == nil {
		return nil, nil
	}
	ref := index.refByNode[parent]
	return cloneNode(parent, index, ref).(*Role), nil
}

// DependentsOf returns uses sources in declaration order.
func (index *Index) DependentsOf(ref string) ([]string, error) {
	if _, err := index.Find(ref); err != nil {
		return nil, err
	}
	return append([]string(nil), index.dependents[ref]...), nil
}

// ChildrenOf returns immediate containment members in declaration order.
func (index *Index) ChildrenOf(node Node) ([]Node, error) {
	internal, ok := index.internalNode(node)
	if !ok {
		return nil, errors.New("node is not registered in this Index")
	}
	children := make([]Node, 0)
	for _, ref := range index.order {
		candidate := index.byRef[ref]
		if index.parent[candidate] == internal {
			children = append(children, cloneNode(candidate, index, ref))
		}
	}
	return children, nil
}

// Walk returns every canonical address in declaration order.
func (index *Index) Walk() []string { return append([]string(nil), index.order...) }

func (index *Index) cloneNodes(nodes []Node) []Node {
	result := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, cloneNode(node, index, index.refByNode[node]))
	}
	return result
}

func (index *Index) internalNode(node Node) (Node, bool) {
	if _, ok := index.refByNode[node]; ok {
		return node, true
	}
	owner, ref := nodeLocation(node)
	if owner != index {
		return nil, false
	}
	internal, ok := index.byRef[ref]
	return internal, ok
}

func cloneNode(node Node, owner *Index, ref string) Node {
	switch typed := node.(type) {
	case *Role:
		return &Role{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Children: append([]Factory(nil), typed.Children...), Skills: append([]Factory(nil), typed.Skills...), Tools: append([]Factory(nil), typed.Tools...), Uses: append([]string(nil), typed.Uses...), owner: owner, ref: ref}
	case *Skill:
		return &Skill{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...), owner: owner, ref: ref}
	case *Tool:
		return &Tool{Name: typed.Name, Description: typed.Description, ReadOnly: typed.ReadOnly, Uses: append([]string(nil), typed.Uses...), binding: typed.binding, owner: owner, ref: ref}
	default:
		panic("unreachable Contexture node kind")
	}
}

func nodeLocation(node Node) (*Index, string) {
	switch typed := node.(type) {
	case *Role:
		return typed.owner, typed.ref
	case *Skill:
		return typed.owner, typed.ref
	case *Tool:
		return typed.owner, typed.ref
	default:
		return nil, ""
	}
}
