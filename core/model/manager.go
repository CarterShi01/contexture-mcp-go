package model

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// RoleFactory constructs one root Role when a ControllerManager registers it.
// It is deliberately distinct from Factory so a Go caller states the expected
// root kind at the registration call site.
type RoleFactory func() *Role

// SkillFactory constructs one standalone root Skill for registration.
type SkillFactory func() *Skill

// ToolFactory constructs one standalone root Tool for registration.
type ToolFactory func() *Tool

// ControllerManager owns an imperative, pre-serving registration phase.
//
// ApplicationDeclaration is the preferred lazy composition root: it does not
// call factories until Compile. A ControllerManager is the Go-native equivalent
// of Python's manager for applications that intentionally want registration to
// construct, validate, and own a fixed forest before it is compiled. Registered
// nodes are copied into manager-owned snapshots, so a later caller mutation
// cannot change an Application or Index already produced by this manager.
type ControllerManager struct {
	mu       sync.RWMutex
	channels ChannelHandle
	roles    []Node
	skills   []Node
	tools    []Node
	seen     map[Node]string
}

// NewControllerManager creates an empty imperative registration owner.
func NewControllerManager() *ControllerManager {
	return &ControllerManager{seen: map[Node]string{}}
}

// NewControllerManagerWithChannels creates a manager whose later Applications
// use channels for their serving lifetime. Unlike Python's arbitrary stamped
// handle, Go accepts the lifecycle-safe Channels interface and keeps it on the
// compiled Index rather than exposing it through every Node.
func NewControllerManagerWithChannels(channels Channels) *ControllerManager {
	manager := NewControllerManager()
	manager.channels = channels
	return manager
}

// NewControllerManagerWithChannelHandle creates a manager that passes one
// ordinary dependency through to future Tool invocations without treating
// Open/Close lookalike methods as lifecycle ownership.
func NewControllerManagerWithChannelHandle(handle ChannelHandle) *ControllerManager {
	manager := NewControllerManager()
	manager.channels = handle
	return manager
}

// RegisterRole constructs and captures one root Role.
func (manager *ControllerManager) RegisterRole(factory RoleFactory) (*Role, error) {
	if factory == nil {
		return nil, registrationFactoryError(RoleKind)
	}
	node, err := manager.register(func() Node { return factory() }, RoleKind)
	if err != nil {
		return nil, err
	}
	return snapshotNode(node).(*Role), nil
}

// RegisterSkill constructs and captures one standalone root Skill.
func (manager *ControllerManager) RegisterSkill(factory SkillFactory) (*Skill, error) {
	if factory == nil {
		return nil, registrationFactoryError(SkillKind)
	}
	node, err := manager.register(func() Node { return factory() }, SkillKind)
	if err != nil {
		return nil, err
	}
	return snapshotNode(node).(*Skill), nil
}

// RegisterTool constructs and captures one standalone root Tool.
func (manager *ControllerManager) RegisterTool(factory ToolFactory) (*Tool, error) {
	if factory == nil {
		return nil, registrationFactoryError(ToolKind)
	}
	node, err := manager.register(func() Node { return factory() }, ToolKind)
	if err != nil {
		return nil, err
	}
	return snapshotNode(node).(*Tool), nil
}

// RegisterRoot constructs one root through a generic Factory and dispatches it
// by its concrete Contexture kind. Prefer RegisterRole, RegisterSkill, or
// RegisterTool when the kind is known statically.
func (manager *ControllerManager) RegisterRoot(factory Factory) (Node, error) {
	if factory == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("registration root factory must not be nil"))
	}
	return manager.register(factory, "")
}

func (manager *ControllerManager) register(factory Factory, expected Kind) (Node, error) {
	if manager == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("ControllerManager must not be nil"))
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	declaration, err := registrationNode(factory)
	if err != nil {
		return nil, err
	}
	if expected != "" && declaration.nodeKind() != expected {
		return nil, fmt.Errorf("%w: Register%s received a %s, not a %s", ErrInvalidDeclaration, kindTitle(expected), declaration.nodeKind(), expected)
	}
	for _, held := range manager.rootsLocked() {
		if held.nodeName() == declaration.nodeName() {
			return nil, fmt.Errorf("%w: root %q is already registered as a %s; a root name is the first segment of every address, so a %s root with that name would make its branch unreachable", ErrDuplicate, declaration.nodeName(), held.nodeKind(), declaration.nodeKind())
		}
	}
	seen := make(map[Node]string, len(manager.seen))
	for node, path := range manager.seen {
		seen[node] = path
	}
	captured, err := captureRegisteredNode(declaration, declaration.nodeName(), seen, map[Node]bool{}, map[string]Node{})
	if err == nil {
		switch captured.nodeKind() {
		case RoleKind:
			manager.roles = append(manager.roles, captured)
		case SkillKind:
			manager.skills = append(manager.skills, captured)
		case ToolKind:
			manager.tools = append(manager.tools, captured)
		default:
			err = errors.Join(ErrInvalidDeclaration, errors.New("registration root has an unsupported kind"))
		}
		if err == nil {
			manager.seen = seen
		}
	}
	if err != nil {
		return nil, err
	}
	return snapshotNode(captured), nil
}

func registrationFactoryError(kind Kind) error {
	return fmt.Errorf("%w: Register%s requires a non-nil %sFactory", ErrInvalidDeclaration, kindTitle(kind), kindTitle(kind))
}

func kindTitle(kind Kind) string {
	if len(kind) == 0 {
		return ""
	}
	return string(kind[:1]) + string(kind[1:])
}

func registrationNode(factory Factory) (Node, error) {
	declaration := factory()
	if declaration == nil || (reflect.ValueOf(declaration).Kind() == reflect.Ptr && reflect.ValueOf(declaration).IsNil()) {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("registration factory returned nil"))
	}
	return declaration, nil
}

func captureRegisteredNode(node Node, path string, seen map[Node]string, active map[Node]bool, addresses map[string]Node) (Node, error) {
	if previous, exists := addresses[path]; exists {
		return nil, fmt.Errorf("%w: address %q is already occupied by node %q", ErrDuplicate, path, previous.nodeName())
	}
	if previous, exists := seen[node]; exists {
		if active[node] {
			return nil, fmt.Errorf("%w: node %q contains itself at %q on the path to %q", ErrContainmentCycle, node.nodeName(), previous, path)
		}
		return nil, fmt.Errorf("%w: node %q is held twice at %q and %q; one capability has one canonical address", ErrDuplicate, node.nodeName(), previous, path)
	}
	if err := validateNode(node); err != nil {
		return nil, err
	}
	seen[node] = path
	addresses[path] = node
	active[node] = true
	defer delete(active, node)
	switch typed := node.(type) {
	case *Role:
		children, err := captureGroup(typed.Children, RoleKind, path, seen, active, addresses)
		if err != nil {
			return nil, err
		}
		skills, err := captureGroup(typed.Skills, SkillKind, path, seen, active, addresses)
		if err != nil {
			return nil, err
		}
		tools, err := captureGroup(typed.Tools, ToolKind, path, seen, active, addresses)
		if err != nil {
			return nil, err
		}
		return &Role{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...), Children: frozenFactories(children), Skills: frozenFactories(skills), Tools: frozenFactories(tools)}, nil
	case *Skill:
		return &Skill{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...)}, nil
	case *Tool:
		return &Tool{Name: typed.Name, Description: typed.Description, ReadOnly: typed.ReadOnly, Uses: append([]string(nil), typed.Uses...), binding: typed.binding}, nil
	default:
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("registration root must be a Contexture Role, Skill, or Tool"))
	}
}

func captureGroup(factories []Factory, expected Kind, parent string, seen map[Node]string, active map[Node]bool, addresses map[string]Node) ([]Node, error) {
	result := make([]Node, 0, len(factories))
	for _, factory := range factories {
		if factory == nil {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("registered Role has a nil member factory"))
		}
		member, err := registrationNode(factory)
		if err != nil {
			return nil, err
		}
		if member.nodeKind() != expected {
			return nil, fmt.Errorf("%w: role member %q is a %s in the %s group", ErrInvalidDeclaration, member.nodeName(), member.nodeKind(), expected)
		}
		captured, err := captureRegisteredNode(member, parent+foundation.ReferenceSeparator+member.nodeName(), seen, active, addresses)
		if err != nil {
			return nil, err
		}
		result = append(result, captured)
	}
	return result, nil
}

func frozenFactories(nodes []Node) []Factory {
	factories := make([]Factory, 0, len(nodes))
	for _, node := range nodes {
		node := node
		factories = append(factories, func() Node { return node })
	}
	return factories
}

// Roles returns registered root Roles in their registration order.
func (manager *ControllerManager) Roles() []*Role {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]*Role, 0, len(manager.roles))
	for _, node := range manager.roles {
		result = append(result, snapshotNode(node).(*Role))
	}
	return result
}

// Skills returns registered standalone root Skills in their registration order.
func (manager *ControllerManager) Skills() []*Skill {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]*Skill, 0, len(manager.skills))
	for _, node := range manager.skills {
		result = append(result, snapshotNode(node).(*Skill))
	}
	return result
}

// Tools returns registered standalone root Tools in their registration order.
func (manager *ControllerManager) Tools() []*Tool {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]*Tool, 0, len(manager.tools))
	for _, node := range manager.tools {
		result = append(result, snapshotNode(node).(*Tool))
	}
	return result
}

// Roots returns the roots in Contexture order: Roles, then standalone Skills,
// then standalone Tools. Within each kind, registration order is retained.
func (manager *ControllerManager) Roots() []Node {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.snapshotRootsLocked()
}

func (manager *ControllerManager) rootsLocked() []Node {
	return append(append(append([]Node(nil), manager.roles...), manager.skills...), manager.tools...)
}

func (manager *ControllerManager) snapshotRootsLocked() []Node {
	roots := manager.rootsLocked()
	result := make([]Node, 0, len(roots))
	for _, node := range roots {
		result = append(result, snapshotNode(node))
	}
	return result
}

// RebindChannels changes the Channels used by Applications produced after this
// call. Already declared Applications and compiled Index snapshots keep their
// original Channels, which prevents a live server from being retargeted.
func (manager *ControllerManager) RebindChannels(channels Channels) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.channels = channels
}

// RebindChannelHandle changes the ordinary dependency captured by future
// Applications without affecting already-created snapshots.
func (manager *ControllerManager) RebindChannelHandle(handle ChannelHandle) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.channels = handle
}

// Channels returns the serving dependency owner configured for future
// Applications. The value is an application-owned lifecycle object.
func (manager *ControllerManager) Channels() ChannelHandle {
	if manager == nil {
		return nil
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.channels
}

// Application produces a lazy Application over a defensive snapshot of the
// roots registered so far. Future registrations do not alter it.
func (manager *ControllerManager) Application(name string) (*Application, error) {
	if manager == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("ControllerManager must not be nil"))
	}
	manager.mu.RLock()
	roots := manager.rootsLocked()
	channels := manager.channels
	manager.mu.RUnlock()
	factories := make([]Factory, 0, len(roots))
	for _, root := range roots {
		root := root
		factories = append(factories, func() Node { return snapshotNode(root) })
	}
	declaration := ApplicationDeclaration{Name: name, Roots: factories}
	if channels != nil {
		if lifecycle, ok := channels.(Channels); ok {
			declaration.Channels = lifecycle
		}
	}
	application, err := DeclareApplication(declaration)
	if err != nil {
		return nil, err
	}
	application.channels = channels
	return application, nil
}

// Compile produces one immutable Index from the roots registered so far.
func (manager *ControllerManager) Compile(name string) (*Index, error) {
	application, err := manager.Application(name)
	if err != nil {
		return nil, err
	}
	return Compile(application)
}

func snapshotNode(node Node) Node {
	switch typed := node.(type) {
	case *Role:
		return &Role{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...), Children: snapshotFactories(typed.Children), Skills: snapshotFactories(typed.Skills), Tools: snapshotFactories(typed.Tools)}
	case *Skill:
		return &Skill{Name: typed.Name, Description: typed.Description, Instructions: typed.Instructions, Uses: append([]string(nil), typed.Uses...)}
	case *Tool:
		return &Tool{Name: typed.Name, Description: typed.Description, ReadOnly: typed.ReadOnly, Uses: append([]string(nil), typed.Uses...), binding: typed.binding}
	default:
		panic("unreachable Contexture node kind")
	}
}

func snapshotFactories(factories []Factory) []Factory {
	result := make([]Factory, 0, len(factories))
	for _, factory := range factories {
		node := factory()
		result = append(result, func() Node { return snapshotNode(node) })
	}
	return result
}
