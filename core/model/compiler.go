package model

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// Index is the immutable canonical address view of one fresh compilation.
type Index struct {
	name        string
	roots       []Node
	modelRoots  []Node
	promptRoots []Node
	channels    ChannelHandle
	bound       bool
	byRef       map[string]Node
	refByNode   map[Node]string
	parent      map[Node]*Role
	dependents  map[string][]string
	order       []string
	byKind      map[Kind][]Node
	branchRefs  map[string][]string
}

// Compile builds one fresh canonical forest from a lazy Application.
func Compile(application *Application) (*Index, error) {
	return compile(application, true)
}

func compile(application *Application, bindTools bool) (*Index, error) {
	if application == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("application must not be nil"))
	}
	state := compiler{index: &Index{name: application.name, channels: application.channels, bound: bindTools, byRef: map[string]Node{}, refByNode: map[Node]string{}, parent: map[Node]*Role{}, dependents: map[string][]string{}, byKind: map[Kind][]Node{}, branchRefs: map[string][]string{}}, active: map[uintptr]bool{}, seen: map[Node]bool{}}
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
			if target == ref {
				return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("%s %q names itself in Uses", node.nodeKind(), ref))
			}
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
	return state.buildExpected(factory, parent, path, "")
}

func (state *compiler) buildExpected(factory Factory, parent *Role, path []string, process string) (Node, error) {
	if factory == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("node factory must not be nil"))
	}
	// PreProcess and PostProcess are adapted to Factory below. Those adapter
	// closures share one code pointer even when they close over unrelated role
	// slots, so function-code identity cannot represent their declaration
	// identity or containment. Concrete node identity is still checked by seen.
	if process == "" {
		key := reflect.ValueOf(factory).Pointer()
		if state.active[key] {
			return nil, ErrContainmentCycle
		}
		state.active[key] = true
		defer delete(state.active, key)
	}
	declaration := factory()
	if declaration == nil || (reflect.ValueOf(declaration).Kind() == reflect.Ptr && reflect.ValueOf(declaration).IsNil()) {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("node factory returned nil"))
	}
	if process == "pre_process" {
		if _, ok := declaration.(*PreProcess); !ok {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("Role PreProcess factory must return a constructed *PreProcess"))
		}
	}
	if process == "post_process" {
		if _, ok := declaration.(*PostProcess); !ok {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("Role PostProcess factory must return a constructed *PostProcess"))
		}
	}
	if state.seen[declaration] {
		return nil, fmt.Errorf("%w: node %q was reused", ErrDuplicate, declaration.nodeName())
	}
	state.seen[declaration] = true
	if err := validateNode(declaration); err != nil {
		return nil, err
	}
	ref := strings.Join(append(append([]string(nil), path...), declaration.nodeName()), foundation.ReferenceSeparator)
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
	state.index.byKind[node.nodeKind()] = append(state.index.byKind[node.nodeKind()], node)
	role, isRole := node.(*Role)
	if !isRole {
		switch typed := node.(type) {
		case *PreProcess:
			role = (*Role)(typed)
		case *PostProcess:
			role = (*Role)(typed)
		default:
			return node, nil
		}
	}
	if role.PreProcess != nil {
		prepared, err := state.buildExpected(func() Node { return role.PreProcess() }, role, strings.Split(ref, foundation.ReferenceSeparator), "pre_process")
		if err != nil {
			return nil, err
		}
		role.preProcess = internalRole(prepared)
	}
	if err := state.buildBranches(role.Children, role, ref); err != nil {
		return nil, err
	}
	if role.PostProcess != nil {
		finished, err := state.buildExpected(func() Node { return role.PostProcess() }, role, strings.Split(ref, foundation.ReferenceSeparator), "post_process")
		if err != nil {
			return nil, err
		}
		role.postProcess = internalRole(finished)
	}
	if err := state.buildGroup(role.Skills, role, ref, SkillKind); err != nil {
		return nil, err
	}
	if err := state.buildGroup(role.Tools, role, ref, ToolKind); err != nil {
		return nil, err
	}
	return node, nil
}

func internalRole(node Node) *Role {
	switch typed := node.(type) {
	case *Role:
		return typed
	case *PreProcess:
		return (*Role)(typed)
	case *PostProcess:
		return (*Role)(typed)
	default:
		return nil
	}
}

func (state *compiler) buildBranches(factories []Factory, parent *Role, ref string) error {
	for _, factory := range factories {
		child, err := state.build(factory, parent, strings.Split(ref, foundation.ReferenceSeparator))
		if err != nil {
			return err
		}
		if child.nodeKind() != RoleKind {
			return fmt.Errorf("%w: role %q contains %s in %s group", ErrInvalidDeclaration, parent.Name, child.nodeKind(), RoleKind)
		}
		state.index.branchRefs[ref] = append(state.index.branchRefs[ref], state.index.refByNode[child])
	}
	return nil
}

func (state *compiler) buildGroup(factories []Factory, parent *Role, ref string, expected Kind) error {
	for _, factory := range factories {
		child, err := state.build(factory, parent, strings.Split(ref, foundation.ReferenceSeparator))
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
	if strings.TrimSpace(node.nodeName()) == "" || strings.Contains(node.nodeName(), foundation.ReferenceSeparator) {
		return errors.Join(ErrInvalidDeclaration, fmt.Errorf("node name must be non-empty and cannot contain %s", foundation.ReferenceSeparator))
	}
	if strings.TrimSpace(node.nodeDescription()) == "" {
		return errors.Join(ErrInvalidDeclaration, errors.New("node description must not be empty"))
	}
	switch role := node.(type) {
	case *Role:
		if strings.TrimSpace(role.Instructions) == "" {
			return errors.Join(ErrInvalidDeclaration, errors.New("role instructions must not be empty"))
		}
	case *PreProcess:
		if strings.TrimSpace(role.Instructions) == "" {
			return errors.Join(ErrInvalidDeclaration, errors.New("PreProcess instructions must not be empty"))
		}
	case *PostProcess:
		if strings.TrimSpace(role.Instructions) == "" {
			return errors.Join(ErrInvalidDeclaration, errors.New("PostProcess instructions must not be empty"))
		}
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

// Count reports the number of addressable nodes in this immutable snapshot.
func (index *Index) Count() int {
	if index == nil {
		return 0
	}
	return len(index.byRef)
}

// Has reports whether ref is an exact canonical address in this snapshot.
func (index *Index) Has(ref string) bool {
	if index == nil {
		return false
	}
	_, ok := index.byRef[ref]
	return ok
}

// Bound reports whether this snapshot has executable Tool Bindings.
func (index *Index) Bound() bool { return index != nil && index.bound }

// Channels returns the lifecycle owner captured at compilation time. It is a
// snapshot fact; rebinding a manager later cannot change it.
func (index *Index) Channels() ChannelHandle {
	if index == nil {
		return nil
	}
	return index.channels
}

// Roots returns a defensive root copy in declaration order.
func (index *Index) Roots() []Node { return index.cloneNodes(index.roots) }

// ModelRoots returns model-visible roots in declaration order.
func (index *Index) ModelRoots() []Node { return index.cloneNodes(index.modelRoots) }

// PromptRoots returns person-controlled roots in declaration order.
func (index *Index) PromptRoots() []Node { return index.cloneNodes(index.promptRoots) }

// OfKind returns every node of kind in containment declaration order.
func (index *Index) OfKind(kind Kind) []Node {
	if index == nil {
		return nil
	}
	return index.cloneNodes(index.byKind[kind])
}

// Find resolves one canonical address.
func (index *Index) Find(ref string) (Node, error) {
	canonical := canonicalRef(ref)
	node, ok := index.byRef[canonical]
	if !ok {
		return nil, index.lookupFailure(ref)
	}
	return cloneNode(node, index, canonical), nil
}

// canonicalRef matches the reference parser used by lookup diagnostics: empty
// slash segments are separators, not address components. Every successful
// Index lookup therefore returns one canonical address spelling.
func canonicalRef(ref string) string {
	segments := make([]string, 0)
	for _, segment := range strings.Split(ref, foundation.ReferenceSeparator) {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return strings.Join(segments, foundation.ReferenceSeparator)
}

func (index *Index) lookupFailure(ref string) *NodeNotFoundError {
	segments := []string{}
	for _, segment := range strings.Split(ref, foundation.ReferenceSeparator) {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return &NodeNotFoundError{Reason: EmptyRef, Ref: ref, HasRef: true}
	}
	rootRef := segments[0]
	current, exists := index.byRef[rootRef]
	if !exists {
		known := make([]string, 0, len(index.roots))
		for _, root := range index.roots {
			known = append(known, root.nodeName())
		}
		sort.Strings(known)
		return &NodeNotFoundError{Reason: NoSuchRoot, Ref: ref, HasRef: true, Segment: rootRef, Scope: rootRef, Known: known}
	}
	for depth := 1; depth < len(segments); depth++ {
		role, ok := current.(*Role)
		if !ok {
			return &NodeNotFoundError{Reason: NotAContainer, Ref: ref, HasRef: true, Segment: segments[depth], Scope: current.nodeName(), Kind: string(current.nodeKind())}
		}
		known := []string{}
		var next Node
		for _, candidateRef := range index.order {
			candidate := index.byRef[candidateRef]
			if index.parent[candidate] != role {
				continue
			}
			known = append(known, candidate.nodeName())
			if candidate.nodeName() == segments[depth] {
				next = candidate
			}
		}
		if next == nil {
			sort.Strings(known)
			return &NodeNotFoundError{Reason: NoSuchMember, Ref: ref, HasRef: true, Segment: segments[depth], Scope: role.Name, Kind: string(role.nodeKind()), Known: known}
		}
		current = next
	}
	return &NodeNotFoundError{Reason: NoSuchMember, Ref: ref, HasRef: true, Segment: segments[len(segments)-1]}
}

// Tool resolves one canonical ref that must name a Tool.
func (index *Index) Tool(ref string) (*Tool, error) {
	node, err := index.Find(ref)
	if err != nil {
		return nil, err
	}
	tool, ok := node.(*Tool)
	if !ok {
		return nil, &NodeNotFoundError{Reason: WrongKind, Ref: ref, HasRef: true, Kind: string(node.nodeKind()), Wanted: string(ToolKind)}
	}
	return tool, nil
}

// BindingOf returns the one compiled Binding for a Tool ref. It rejects an
// unbound disclosure-only snapshot before exposing any execution capability.
func (index *Index) BindingOf(ref string) (Binding, error) {
	if index == nil || !index.bound {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("a disclosure-only Index has no executable Bindings"))
	}
	tool, err := index.Tool(ref)
	if err != nil {
		return nil, err
	}
	return tool.Binding()
}

// SchemaOf returns a defensive schema snapshot for one compiled Tool node.
func (index *Index) SchemaOf(node Node) (map[string]any, error) {
	ref, err := index.RefOf(node)
	if err != nil {
		return nil, err
	}
	binding, err := index.BindingOf(ref)
	if err != nil {
		return nil, err
	}
	return binding.Schema(), nil
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

// ParentOf returns the containment owner of a node while preserving a strong
// PreProcess or PostProcess identity when process equipment owns the node.
func (index *Index) ParentOf(node Node) (Node, error) {
	internal, ok := index.internalNode(node)
	if !ok {
		return nil, nil
	}
	parent := index.parent[internal]
	if parent == nil {
		return nil, nil
	}
	ref := index.refByNode[parent]
	return cloneNode(parent, index, ref), nil
}

// DependentsOf returns uses sources in declaration order.
func (index *Index) DependentsOf(ref string) ([]string, error) {
	if _, err := index.Find(ref); err != nil {
		return nil, err
	}
	return append([]string(nil), index.dependents[ref]...), nil
}

// UsesOf returns declared dependency targets in their node declaration order.
func (index *Index) UsesOf(ref string) ([]string, error) {
	node, err := index.Find(ref)
	if err != nil {
		return nil, err
	}
	return node.nodeUses(), nil
}

// ChildrenOf returns immediate containment members in declaration order.
func (index *Index) ChildrenOf(node Node) ([]Node, error) {
	internal, ok := index.internalNode(node)
	if !ok {
		if _, role := roleNode(node); !role {
			return []Node{}, nil
		}
		return nil, errors.New("node is not registered in this Index")
	}
	children := make([]Node, 0)
	internalRef := index.refByNode[internal]
	for _, ref := range index.order {
		candidate := index.byRef[ref]
		parent := index.parent[candidate]
		if parent != nil && parent.ref == internalRef {
			children = append(children, cloneNode(candidate, index, ref))
		}
	}
	return children, nil
}

// Walk returns every canonical address in declaration order.
func (index *Index) Walk() []string { return append([]string(nil), index.order...) }

// NodesWithRefs returns every canonical address/node pair in containment
// declaration order. Node values are defensive snapshots owned by this Index.
func (index *Index) NodesWithRefs() []NodeRef {
	if index == nil {
		return nil
	}
	result := make([]NodeRef, 0, len(index.order))
	for _, ref := range index.order {
		result = append(result, NodeRef{Ref: ref, Node: cloneNode(index.byRef[ref], index, ref)})
	}
	return result
}

// Skills returns every procedure with its canonical opening ref.
func (index *Index) Skills() []NodeRef { return index.nodesOfKind(SkillKind) }

// RolesWithRefs returns every Role in containment depth-first order.
func (index *Index) RolesWithRefs() []NodeRef { return index.nodesOfKind(RoleKind) }

func (index *Index) nodesOfKind(kind Kind) []NodeRef {
	if index == nil {
		return nil
	}
	result := []NodeRef{}
	for _, ref := range index.order {
		node := index.byRef[ref]
		if node.nodeKind() == kind {
			result = append(result, NodeRef{Ref: ref, Node: cloneNode(node, index, ref)})
		}
	}
	return result
}

// RolesByLevel returns the Role axis breadth-first. It follows containment
// only, never Uses, so reference cycles cannot affect startup enumeration.
func (index *Index) RolesByLevel() []NodeRef {
	if index == nil {
		return nil
	}
	queue := make([]string, 0)
	for _, root := range index.roots {
		if root.nodeKind() == RoleKind {
			queue = append(queue, index.refByNode[root])
		}
	}
	result := []NodeRef{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		role := index.byRef[ref].(*Role)
		result = append(result, NodeRef{Ref: ref, Node: cloneNode(role, index, ref)})
		queue = append(queue, index.branchRefs[ref]...)
	}
	return result
}

// MatchingRefs ranks all addressable refs against interactive input. Negative
// limits retain Python slice semantics by omitting results from the end.
func (index *Index) MatchingRefs(value string, limit int) ([]string, int) {
	if index == nil {
		return nil, 0
	}
	return matchingRefs(index.order, value, limit)
}

// SignpostLevel is one undisclosed ancestor and its direct sub-role count.
type SignpostLevel struct {
	Ref          string
	SubRoleCount int
}

// Signpost returns ancestors of ref from root toward its parent without
// disclosing their member names.
func (index *Index) Signpost(ref string) ([]SignpostLevel, error) {
	if _, err := index.Find(ref); err != nil {
		return nil, err
	}
	parts := strings.Split(canonicalRef(ref), foundation.ReferenceSeparator)
	result := make([]SignpostLevel, 0, len(parts)-1)
	for depth := 1; depth < len(parts); depth++ {
		ancestor := strings.Join(parts[:depth], foundation.ReferenceSeparator)
		count := len(index.branchRefs[ancestor])
		result = append(result, SignpostLevel{Ref: ancestor, SubRoleCount: count})
	}
	return result, nil
}

// ReferenceCrossing records one declared Uses edge into a different root.
type ReferenceCrossing struct {
	SourceRef  string
	TargetRef  string
	TargetRoot string
}

// Crossings returns cross-root Uses edges in containment declaration order.
func (index *Index) Crossings() []ReferenceCrossing {
	if index == nil {
		return nil
	}
	result := []ReferenceCrossing{}
	for _, source := range index.order {
		root := source[:strings.Index(source+foundation.ReferenceSeparator, foundation.ReferenceSeparator)]
		for _, target := range index.byRef[source].nodeUses() {
			targetRoot := target[:strings.Index(target+foundation.ReferenceSeparator, foundation.ReferenceSeparator)]
			if targetRoot != root {
				result = append(result, ReferenceCrossing{SourceRef: source, TargetRef: target, TargetRoot: targetRoot})
			}
		}
	}
	return result
}

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
		return cloneRole(typed, owner, ref)
	case *PreProcess:
		return (*PreProcess)(cloneRole((*Role)(typed), owner, ref))
	case *PostProcess:
		return (*PostProcess)(cloneRole((*Role)(typed), owner, ref))
	case *Skill:
		return &Skill{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...), owner: owner, ref: ref}
	case *Tool:
		return &Tool{Name: typed.Name, Description: typed.Description, ReadOnly: typed.ReadOnly, Uses: append([]string(nil), typed.Uses...), binding: typed.binding, owner: owner, ref: ref}
	default:
		panic("unreachable Contexture node kind")
	}
}

func cloneRole(typed *Role, owner *Index, ref string) *Role {
	clone := &Role{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, PreProcess: typed.PreProcess, Children: append([]Factory(nil), typed.Children...), PostProcess: typed.PostProcess, Skills: append([]Factory(nil), typed.Skills...), Tools: append([]Factory(nil), typed.Tools...), Uses: append([]string(nil), typed.Uses...), owner: owner, ref: ref}
	if typed.preProcess != nil && owner != nil {
		processRef := typed.preProcess.ref
		process := cloneNode(owner.byRef[processRef], owner, processRef).(*PreProcess)
		clone.preProcess = (*Role)(process)
		clone.PreProcess = func() *PreProcess { return process }
	}
	if typed.postProcess != nil && owner != nil {
		processRef := typed.postProcess.ref
		process := cloneNode(owner.byRef[processRef], owner, processRef).(*PostProcess)
		clone.postProcess = (*Role)(process)
		clone.PostProcess = func() *PostProcess { return process }
	}
	return clone
}

func nodeLocation(node Node) (*Index, string) {
	switch typed := node.(type) {
	case *Role:
		if typed == nil {
			return nil, ""
		}
		return typed.owner, typed.ref
	case *PreProcess:
		if typed == nil {
			return nil, ""
		}
		return typed.owner, typed.ref
	case *PostProcess:
		if typed == nil {
			return nil, ""
		}
		return typed.owner, typed.ref
	case *Skill:
		if typed == nil {
			return nil, ""
		}
		return typed.owner, typed.ref
	case *Tool:
		if typed == nil {
			return nil, ""
		}
		return typed.owner, typed.ref
	default:
		return nil, ""
	}
}
