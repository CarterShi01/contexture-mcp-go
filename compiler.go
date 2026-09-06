package contexture

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ErrDuplicate identifies duplicate names, addresses, or node identity.
var ErrDuplicate = errors.New("duplicate Contexture identity")

// ErrContainmentCycle identifies recursive containment factories.
var ErrContainmentCycle = errors.New("Contexture containment cycle")

// ErrUnresolvedReference identifies a uses reference absent from the forest.
var ErrUnresolvedReference = errors.New("unresolved Contexture reference")

// Index is the immutable canonical address view of one fresh compilation.
type Index struct {
	name        string
	roots       []Node
	modelRoots  []Node
	promptRoots []Node
	byRef       map[string]Node
	refByNode   map[Node]string
	parent      map[Node]*Role
	dependents  map[string][]string
}

// Compile builds one fresh canonical forest from a lazy Application.
func Compile(application *Application) (*Index, error) {
	if application == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application must not be nil"))
	}
	state := compiler{index: &Index{name: application.name, byRef: map[string]Node{}, refByNode: map[Node]string{}, parent: map[Node]*Role{}, dependents: map[string][]string{}}, active: map[uintptr]bool{}, seen: map[Node]bool{}}
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
	for ref, node := range state.index.byRef {
		for _, target := range node.nodeUses() {
			if _, ok := state.index.byRef[target]; !ok {
				return nil, fmt.Errorf("%w: %q uses %q", ErrUnresolvedReference, ref, target)
			}
			state.index.dependents[target] = append(state.index.dependents[target], ref)
		}
	}
	return state.index, nil
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
	node := factory()
	delete(state.active, key)
	if node == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("node factory returned nil"))
	}
	if state.seen[node] {
		return nil, fmt.Errorf("%w: node %q was reused", ErrDuplicate, node.nodeName())
	}
	state.seen[node] = true
	if err := validateNode(node); err != nil {
		return nil, err
	}
	ref := strings.Join(append(append([]string(nil), path...), node.nodeName()), "/")
	if _, exists := state.index.byRef[ref]; exists {
		return nil, fmt.Errorf("%w: address %q", ErrDuplicate, ref)
	}
	state.index.byRef[ref] = node
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
func (index *Index) Roots() []Node { return append([]Node(nil), index.roots...) }

// ModelRoots returns model-visible roots in declaration order.
func (index *Index) ModelRoots() []Node { return append([]Node(nil), index.modelRoots...) }

// PromptRoots returns person-controlled roots in declaration order.
func (index *Index) PromptRoots() []Node { return append([]Node(nil), index.promptRoots...) }

// Find resolves one canonical address.
func (index *Index) Find(ref string) (Node, error) {
	node, ok := index.byRef[ref]
	if !ok {
		return nil, fmt.Errorf("unknown Contexture reference %q", ref)
	}
	return node, nil
}

// RefOf returns a canonical address for a node compiled into this Index.
func (index *Index) RefOf(node Node) (string, error) {
	ref, ok := index.refByNode[node]
	if !ok {
		return "", errors.New("node is not registered in this Index")
	}
	return ref, nil
}

// ParentOf returns the containment owner of a node.
func (index *Index) ParentOf(node Node) (*Role, error) {
	parent, ok := index.parent[node]
	if !ok {
		return nil, errors.New("node is not registered in this Index")
	}
	return parent, nil
}

// DependentsOf returns uses sources in declaration order.
func (index *Index) DependentsOf(ref string) ([]string, error) {
	if _, err := index.Find(ref); err != nil {
		return nil, err
	}
	return append([]string(nil), index.dependents[ref]...), nil
}
