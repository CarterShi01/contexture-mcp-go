package model

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Factory constructs a fresh pointer-backed node during compilation.
type Factory func() Node

// CompileLevel is one of the three progressive-disclosure states for a Node.
type CompileLevel string

const (
	// RouteCompileLevel carries only broad routing facts.
	RouteCompileLevel CompileLevel = "route"
	// InspectCompileLevel carries one non-activating structural level.
	InspectCompileLevel CompileLevel = "inspect"
	// ActiveCompileLevel carries the selected Node's actionable facts.
	ActiveCompileLevel CompileLevel = "active"
)

// CompiledContext is one JSON-compatible route or active disclosure payload.
type CompiledContext map[string]any

// View supplies forest-owned addresses, schemas, and related routing cards.
type View interface {
	RefOf(Node) (string, error)
	CardOf(Node) (CompiledContext, error)
	CardFor(string) (CompiledContext, error)
	CardsOf([]Node) (CompiledContext, error)
	CardsFor([]string) ([]CompiledContext, error)
	ExecutionOf(Node) (CompiledContext, error)
	SchemaOf(Node) (map[string]any, error)
}

type inspectionView interface {
	RoutingCardsOf([]Node) (CompiledContext, error)
	RoutingCardsFor([]string) ([]CompiledContext, error)
}

// Node is the closed Contexture declaration set.
// Its unexported methods prevent external kinds from entering the forest.
type Node interface {
	nodeKind() Kind
	nodeName() string
	nodeDescription() string
	nodeUses() []string
	Kind() Kind
	NodeName() string
	NodeDescription() string
	NodeUses() []string
	Ref() (string, error)
}

// Kind identifies one Contexture node kind.
type Kind string

const (
	RoleKind  Kind = "role"
	SkillKind Kind = "skill"
	ToolKind  Kind = "tool"
)

// BranchesOf returns a compiled node's direct containment branches. Only a
// Role has branches; Skill and Tool deliberately return an empty result. The
// returned nodes are defensive snapshots in declaration order.
func BranchesOf(node Node) ([]Node, error) {
	if nilNode(node) {
		return nil, uncompiledNodeError()
	}
	role, ok := roleNode(node)
	if !ok {
		return []Node{}, nil
	}
	if role.owner == nil || role.ref == "" {
		if len(role.Children) > 0 {
			return nil, uncompiledNodeError()
		}
		return []Node{}, nil
	}
	branches, err := role.Branches()
	if err != nil {
		return nil, err
	}
	result := make([]Node, 0, len(branches))
	for _, branch := range branches {
		result = append(result, branch)
	}
	return result, nil
}

// MembersOf returns a compiled node's direct containment members. Only a Role
// has members; Skill and Tool deliberately return an empty result. Role member
// order is PreProcess, child Roles, PostProcess, Skills, then Tools.
func MembersOf(node Node) ([]Node, error) {
	if nilNode(node) {
		return nil, uncompiledNodeError()
	}
	role, ok := roleNode(node)
	if !ok {
		return []Node{}, nil
	}
	if role.owner == nil || role.ref == "" {
		if role.PreProcess != nil || len(role.Children) > 0 || role.PostProcess != nil || len(role.Skills) > 0 || len(role.Tools) > 0 {
			return nil, uncompiledNodeError()
		}
		return []Node{}, nil
	}
	return role.Members()
}

// RouteOf renders the minimal immutable-by-convention facts used to choose a Node.
func RouteOf(node Node) (CompiledContext, error) {
	if nilNode(node) {
		return nil, uncompiledNodeError()
	}
	return CompiledContext{
		"kind":        string(node.nodeKind()),
		"name":        node.nodeName(),
		"description": node.nodeDescription(),
	}, nil
}

// CardOf renders one openable routing card through the forest that owns it.
func CardOf(node Node, view View) (CompiledContext, error) {
	if nilNode(node) || nilView(view) {
		return nil, uncompiledNodeError()
	}
	card, err := RouteOf(node)
	if err != nil {
		return nil, err
	}
	ref, err := view.RefOf(node)
	if err != nil {
		return nil, err
	}
	card["ref"] = ref
	if node.nodeKind() == ToolKind {
		execution, err := view.ExecutionOf(node)
		if err != nil {
			return nil, err
		}
		for key, value := range execution {
			card[key] = cloneCompiledValue(value)
		}
	}
	return card, nil
}

// RoutingCardOf renders an openable card without Tool execution facets.
func RoutingCardOf(node Node, view View) (CompiledContext, error) {
	if nilNode(node) || nilView(view) {
		return nil, uncompiledNodeError()
	}
	card, err := RouteOf(node)
	if err != nil {
		return nil, err
	}
	ref, err := view.RefOf(node)
	if err != nil {
		return nil, err
	}
	card["ref"] = ref
	return card, nil
}

// GroupCards renders one closed sibling shape in declaration order.
func GroupCards(nodes []Node, view View) (CompiledContext, error) {
	if nilView(view) {
		return nil, uncompiledNodeError()
	}
	grouped := CompiledContext{
		"roles":  []CompiledContext{},
		"skills": []CompiledContext{},
		"tools":  []CompiledContext{},
	}
	for _, node := range nodes {
		if nilNode(node) {
			return nil, uncompiledNodeError()
		}
		card, err := view.CardOf(node)
		if err != nil {
			return nil, err
		}
		key := string(node.nodeKind()) + "s"
		grouped[key] = append(grouped[key].([]CompiledContext), cloneCompiledContext(card))
	}
	return grouped, nil
}

// GroupRoutingCards renders one sibling shape using only pure routing cards.
func GroupRoutingCards(nodes []Node, view View) (CompiledContext, error) {
	if nilView(view) {
		return nil, uncompiledNodeError()
	}
	grouped := CompiledContext{"roles": []CompiledContext{}, "skills": []CompiledContext{}, "tools": []CompiledContext{}}
	for _, node := range nodes {
		card, err := RoutingCardOf(node, view)
		if err != nil {
			return nil, err
		}
		key := string(node.nodeKind()) + "s"
		grouped[key] = append(grouped[key].([]CompiledContext), cloneCompiledContext(card))
	}
	return grouped, nil
}

// CompileNode renders one Node at ROUTE or ACTIVE through an optional owning View.
func CompileNode(node Node, level CompileLevel, view View) (CompiledContext, error) {
	if nilNode(node) {
		return nil, uncompiledNodeError()
	}
	if level == "" {
		level = RouteCompileLevel
	}
	if level == RouteCompileLevel {
		return RouteOf(node)
	}
	if level == InspectCompileLevel {
		if view == nil {
			view = aloneView{}
		} else if nilView(view) {
			return nil, uncompiledNodeError()
		}
		card, err := RoutingCardOf(node, view)
		if err != nil {
			return nil, err
		}
		members, err := MembersOf(node)
		if err != nil {
			return nil, err
		}
		projection, ok := view.(inspectionView)
		if !ok {
			return nil, errors.Join(ErrInvalidDeclaration, errors.New("INSPECT requires a compiled forest view"))
		}
		grouped, err := projection.RoutingCardsOf(members)
		if err != nil {
			return nil, err
		}
		uses, err := projection.RoutingCardsFor(node.nodeUses())
		if err != nil {
			return nil, err
		}
		return CompiledContext{"node": card, "members": grouped, "uses": uses}, nil
	}
	if level != ActiveCompileLevel {
		return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("unknown Contexture compile level %q", level))
	}
	if view == nil {
		view = aloneView{}
	} else if nilView(view) {
		return nil, uncompiledNodeError()
	}
	card, err := view.CardOf(node)
	if err != nil {
		return nil, err
	}
	card = cloneCompiledContext(card)
	if node.nodeKind() == ToolKind {
		return addCompiledUses(card, node.nodeUses(), view)
	}
	instructions := ""
	switch typed := node.(type) {
	case *Role:
		instructions = typed.Instructions
	case *PreProcess:
		instructions = typed.Instructions
	case *PostProcess:
		instructions = typed.Instructions
	case *Skill:
		instructions = typed.Instructions
	default:
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("active compilation requires a Role, Skill, or Tool"))
	}
	if strings.TrimSpace(instructions) == "" {
		return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("%s %q must have active instructions", node.nodeKind(), node.nodeName()))
	}
	card["instructions"] = instructions
	if node.nodeKind() == RoleKind {
		role, _ := roleNode(node)
		members, err := MembersOf(node)
		if err != nil {
			return nil, err
		}
		grouped, err := view.CardsOf(members)
		if err != nil {
			return nil, err
		}
		for key, value := range grouped {
			card[key] = cloneCompiledValue(value)
		}
		if err := addProcessDetails(card, role, view); err != nil {
			return nil, err
		}
	}
	return addCompiledUses(card, node.nodeUses(), view)
}

func roleNode(node Node) (*Role, bool) {
	switch typed := node.(type) {
	case *Role:
		return typed, typed != nil
	case *PreProcess:
		return (*Role)(typed), typed != nil
	case *PostProcess:
		return (*Role)(typed), typed != nil
	default:
		return nil, false
	}
}

func addProcessDetails(card CompiledContext, role *Role, view View) error {
	if role == nil {
		return nil
	}
	if role.preProcess != nil {
		ref, err := disclosedProcessRef(card, role.preProcess, "PreProcess", view)
		if err != nil {
			return err
		}
		card["pre_process"] = ref
		card["instructions"] = frameworkInstruction("PreProcess", "Opening it only discloses the preparation procedure; it does not execute it. Complete what it requires, then return to this role's own instructions below and carry on with its work. If the preparation is blocked or fails, report that state rather than continuing as though it had succeeded.", fmt.Sprintf("Call %s with ref='%s' before starting this role's work.", OpenGatewayName, ref)) + "\n\n" + card["instructions"].(string)
	}
	if role.postProcess != nil {
		ref, err := disclosedProcessRef(card, role.postProcess, "PostProcess", view)
		if err != nil {
			return err
		}
		card["post_process"] = ref
		card["instructions"] = card["instructions"].(string) + "\n\n" + frameworkInstruction("PostProcess", "Opening it only discloses the procedure; it does not execute it or establish success. Use its available capabilities as instructed, respect required approvals, and report the actual outcome. If it is blocked, fails, or awaits approval, report that state rather than claiming success or bypassing approval.", fmt.Sprintf("Call %s with ref='%s' before finishing this role's work.", OpenGatewayName, ref))
	}
	return nil
}

func disclosedProcessRef(card CompiledContext, process *Role, kind string, view View) (string, error) {
	ref, err := view.RefOf(process)
	if err != nil {
		return "", err
	}
	available := false
	switch roles := card["roles"].(type) {
	case []CompiledContext:
		for _, candidate := range roles {
			available = available || candidate["ref"] == ref
		}
	case []map[string]any:
		for _, candidate := range roles {
			available = available || candidate["ref"] == ref
		}
	}
	if !available {
		return "", errors.Join(ErrInvalidDeclaration, fmt.Errorf("the declared %s is unavailable in this view; open the owning Role through a surface containing its complete %s subtree", kind, kind))
	}
	return ref, nil
}

func addCompiledUses(card CompiledContext, refs []string, view View) (CompiledContext, error) {
	if len(refs) == 0 {
		return card, nil
	}
	uses, err := view.CardsFor(refs)
	if err != nil {
		return nil, err
	}
	card["uses"] = cloneCompiledValue(uses)
	return card, nil
}

type aloneView struct{}

func (aloneView) RefOf(node Node) (string, error) {
	if ref, err := node.Ref(); err == nil {
		return ref, nil
	}
	if nilNode(node) {
		return "", uncompiledNodeError()
	}
	return node.nodeName(), nil
}

func (view aloneView) CardOf(node Node) (CompiledContext, error) { return CardOf(node, view) }

func (view aloneView) RoutingCardOf(node Node) (CompiledContext, error) {
	return RoutingCardOf(node, view)
}

func (aloneView) CardFor(ref string) (CompiledContext, error) {
	return nil, errors.Join(ErrInvalidDeclaration, fmt.Errorf("nothing can resolve %q outside a compiled forest", ref))
}

func (view aloneView) CardsOf(nodes []Node) (CompiledContext, error) {
	return GroupCards(nodes, view)
}

func (view aloneView) RoutingCardsOf(nodes []Node) (CompiledContext, error) {
	return GroupRoutingCards(nodes, view)
}

func (view aloneView) CardsFor(refs []string) ([]CompiledContext, error) {
	cards := make([]CompiledContext, 0, len(refs))
	for _, ref := range refs {
		card, err := view.CardFor(ref)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (view aloneView) RoutingCardsFor(refs []string) ([]CompiledContext, error) {
	if len(refs) == 0 {
		return []CompiledContext{}, nil
	}
	return nil, errors.Join(ErrInvalidDeclaration, errors.New("nothing can resolve uses outside a compiled forest"))
}

func (aloneView) ExecutionOf(node Node) (CompiledContext, error) {
	tool, ok := node.(*Tool)
	if !ok {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("only a Tool has an executable disclosure facet"))
	}
	return CompiledContext{"read_only": tool.ReadOnly, "input_schema": map[string]any{}}, nil
}

func (aloneView) SchemaOf(Node) (map[string]any, error) { return map[string]any{}, nil }

func nilNode(node Node) bool {
	if node == nil {
		return true
	}
	switch typed := node.(type) {
	case *Role:
		return typed == nil
	case *PreProcess:
		return typed == nil
	case *PostProcess:
		return typed == nil
	case *Skill:
		return typed == nil
	case *Tool:
		return typed == nil
	default:
		return false
	}
}

func nilView(view View) bool {
	if view == nil {
		return true
	}
	value := reflect.ValueOf(view)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cloneCompiledContext(source CompiledContext) CompiledContext {
	if source == nil {
		return nil
	}
	result := make(CompiledContext, len(source))
	for key, value := range source {
		result[key] = cloneCompiledValue(value)
	}
	return result
}

func cloneCompiledValue(value any) any {
	switch typed := value.(type) {
	case CompiledContext:
		return cloneCompiledContext(typed)
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneCompiledValue(item)
		}
		return result
	case []CompiledContext:
		result := make([]CompiledContext, len(typed))
		for index, item := range typed {
			result[index] = cloneCompiledContext(item)
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(typed))
		for index, item := range typed {
			result[index] = cloneCompiledValue(item).(map[string]any)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneCompiledValue(item)
		}
		return result
	case []string:
		result := make([]string, len(typed))
		copy(result, typed)
		return result
	}
	return cloneJSONValue(reflect.ValueOf(value))
}

func cloneJSONValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return cloneJSONValue(value.Elem())
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type()).Interface()
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			item := cloneJSONValue(iterator.Value())
			cloned := reflect.ValueOf(item)
			if !cloned.IsValid() {
				cloned = reflect.Zero(value.Type().Elem())
			} else if !cloned.Type().AssignableTo(value.Type().Elem()) {
				if cloned.Type().ConvertibleTo(value.Type().Elem()) {
					cloned = cloned.Convert(value.Type().Elem())
				} else {
					return value.Interface()
				}
			}
			result.SetMapIndex(iterator.Key(), cloned)
		}
		return result.Interface()
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()).Interface()
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			item := cloneJSONValue(value.Index(index))
			cloned := reflect.ValueOf(item)
			if !cloned.IsValid() {
				cloned = reflect.Zero(value.Type().Elem())
			} else if !cloned.Type().AssignableTo(value.Type().Elem()) {
				if cloned.Type().ConvertibleTo(value.Type().Elem()) {
					cloned = cloned.Convert(value.Type().Elem())
				} else {
					return value.Interface()
				}
			}
			result.Index(index).Set(cloned)
		}
		return result.Interface()
	default:
		return value.Interface()
	}
}

func nodeRef(node Node) (string, error) {
	owner, ref := nodeLocation(node)
	if owner == nil || ref == "" || !owner.Has(ref) {
		return "", uncompiledNodeError()
	}
	return ref, nil
}

func uncompiledNodeError() error {
	return errors.Join(ErrInvalidDeclaration, errors.New("Node reference and containment queries require a compiled Index snapshot"))
}
