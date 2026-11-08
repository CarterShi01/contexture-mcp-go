// Package introspection joins one immutable native Index, declaration and
// Disclosure into a trusted SDK-neutral application read model. It never
// publishes an MCP tool, invokes a handler, or opens a Channels lifecycle.
package introspection

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CarterShi01/contexture-mcp-go/core/model"
)

const Schema = "contexture.node-catalog/v1"

type CatalogView string
type CatalogDetail string
type MemberSlot string

const (
	ApplicationView  CatalogView   = "application"
	ModelView        CatalogView   = "model"
	SurfaceView      CatalogView   = "surface"
	SummaryDetail    CatalogDetail = "summary"
	DefinitionDetail CatalogDetail = "definition"
	PreProcessSlot   MemberSlot    = "pre_process"
	ChildSlot        MemberSlot    = "child"
	PostProcessSlot  MemberSlot    = "post_process"
	SkillSlot        MemberSlot    = "skill"
	ToolSlot         MemberSlot    = "tool"
)

// Descriptors are detached values. Mutating a returned descriptor, including
// nested maps or slices, cannot change its owning Catalog or Snapshot.
type MemberDescriptor struct {
	Ref  string     `json:"ref"`
	Kind model.Kind `json:"kind"`
	Slot MemberSlot `json:"slot"`
}

type PresentationDescriptor struct {
	Title       string  `json:"title"`
	Description *string `json:"description,omitempty"`
}

type RoleDefinition struct {
	AuthoredInstructions  string  `json:"authored_instructions"`
	EffectiveInstructions string  `json:"effective_instructions"`
	PreProcessRef         *string `json:"pre_process_ref"`
	PostProcessRef        *string `json:"post_process_ref"`
}

type SkillDefinition struct {
	AuthoredInstructions  string `json:"authored_instructions"`
	EffectiveInstructions string `json:"effective_instructions"`
}

type ToolDefinition struct {
	ExecutionAvailable bool           `json:"execution_available"`
	ReadOnly           *bool          `json:"read_only,omitempty"`
	InputSchema        map[string]any `json:"input_schema,omitempty"`
}

type NodeDescriptor struct {
	Ref          string                  `json:"ref"`
	Kind         model.Kind              `json:"kind"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	Presentation *PresentationDescriptor `json:"presentation"`
	Plane        string                  `json:"plane"`
	ParentRef    *string                 `json:"parent_ref"`
	Ordinal      int                     `json:"ordinal"`
	Members      []MemberDescriptor      `json:"members"`
	Uses         []string                `json:"uses"`
	Dependents   []string                `json:"dependents"`
	Role         *RoleDefinition         `json:"role"`
	Skill        *SkillDefinition        `json:"skill"`
	Tool         *ToolDefinition         `json:"tool"`
	Extensions   map[string]any          `json:"extensions"`
}

type ApplicationDescriptor struct {
	Name           string        `json:"name"`
	Mode           string        `json:"mode"`
	View           CatalogView   `json:"view"`
	Detail         CatalogDetail `json:"detail"`
	Locale         *string       `json:"locale"`
	ExecutionBound bool          `json:"execution_bound"`
}

type Counts struct {
	Role  int `json:"role"`
	Skill int `json:"skill"`
	Tool  int `json:"tool"`
}

// SnapshotData is the typed construction boundary, not mutable stored state.
type SnapshotData struct {
	Application ApplicationDescriptor `json:"application"`
	Roots       []string              `json:"roots"`
	Counts      Counts                `json:"counts"`
	Nodes       []NodeDescriptor      `json:"nodes"`
}

// NodeCatalogSnapshot stores only validated, detached immutable facts.
type NodeCatalogSnapshot struct {
	data   SnapshotData
	digest string
}

func validText(value string) bool { return value != "" && utf8.ValidString(value) }
func validRef(value string) bool {
	return validText(value) && !strings.HasPrefix(value, "/") &&
		!strings.HasSuffix(value, "/") && !strings.Contains(value, "//")
}
func strip(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
	})
}
func validNamespace(value string) bool {
	if !utf8.ValidString(value) || !strings.Contains(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if strip(segment) == "" {
			return false
		}
	}
	return true
}
func validKind(value model.Kind) bool {
	return value == model.RoleKind || value == model.SkillKind || value == model.ToolKind
}
func validView(value CatalogView) bool {
	return value == ApplicationView || value == ModelView || value == SurfaceView
}
func validDetail(value CatalogDetail) bool {
	return value == SummaryDetail || value == DefinitionDetail
}
func ptrCopy[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func detachedObject(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, invalid("Catalog requires a JSON object")
	}
	copy, err := FreezeJSON(value)
	if err != nil {
		return nil, err
	}
	return copy.(map[string]any), nil
}
func cloneDescriptor(node NodeDescriptor) (NodeDescriptor, error) {
	node.ParentRef = ptrCopy(node.ParentRef)
	node.Members = append([]MemberDescriptor{}, node.Members...)
	node.Uses = append([]string{}, node.Uses...)
	node.Dependents = append([]string{}, node.Dependents...)
	if node.Presentation != nil {
		copy := *node.Presentation
		copy.Description = ptrCopy(copy.Description)
		node.Presentation = &copy
	}
	if node.Role != nil {
		copy := *node.Role
		copy.PreProcessRef, copy.PostProcessRef = ptrCopy(copy.PreProcessRef), ptrCopy(copy.PostProcessRef)
		node.Role = &copy
	}
	if node.Skill != nil {
		copy := *node.Skill
		node.Skill = &copy
	}
	if node.Tool != nil {
		copy := *node.Tool
		copy.ReadOnly = ptrCopy(copy.ReadOnly)
		if copy.InputSchema != nil {
			var err error
			copy.InputSchema, err = detachedObject(copy.InputSchema)
			if err != nil {
				return NodeDescriptor{}, err
			}
		}
		node.Tool = &copy
	}
	var err error
	node.Extensions, err = detachedObject(node.Extensions)
	return node, err
}

// NewSnapshot validates all cross-node facts and freezes opaque JSON.
func NewSnapshot(data SnapshotData) (*NodeCatalogSnapshot, error) {
	data.Application.Locale = ptrCopy(data.Application.Locale)
	data.Roots = append([]string{}, data.Roots...)
	nodes := make([]NodeDescriptor, len(data.Nodes))
	for i, node := range data.Nodes {
		var err error
		nodes[i], err = cloneDescriptor(node)
		if err != nil {
			return nil, err
		}
	}
	data.Nodes = nodes
	if err := validate(data); err != nil {
		return nil, err
	}
	digest, err := CanonicalDigest(bodyJSON(data))
	if err != nil {
		return nil, err
	}
	return &NodeCatalogSnapshot{data: data, digest: digest}, nil
}

func validate(data SnapshotData) error {
	app := data.Application
	if !validText(app.Name) || !validView(app.View) || !validDetail(app.Detail) ||
		(app.Mode != "runtime" && app.Mode != "disclosure-only") ||
		(app.Mode == "runtime") != app.ExecutionBound ||
		(app.Locale != nil && !validText(*app.Locale)) {
		return invalid("invalid Catalog application descriptor")
	}
	byRef := map[string]NodeDescriptor{}
	uses, dependents := map[string]map[string]bool{}, map[string]map[string]bool{}
	counts := Counts{}
	for _, node := range data.Nodes {
		if !validRef(node.Ref) || !validKind(node.Kind) || !validText(node.Name) ||
			!validText(node.Description) || (node.Plane != "model" && node.Plane != "prompt") ||
			node.Ordinal < 0 || (app.View != ApplicationView && node.Plane != "model") {
			return invalid("invalid Catalog node descriptor")
		}
		if _, exists := byRef[node.Ref]; exists {
			return invalid("Catalog refs must be unique")
		}
		byRef[node.Ref] = node
		switch node.Kind {
		case model.RoleKind:
			counts.Role++
		case model.SkillKind:
			counts.Skill++
		case model.ToolKind:
			counts.Tool++
		}
		for _, relation := range [][]string{node.Uses, node.Dependents} {
			set := map[string]bool{}
			for _, ref := range relation {
				if !validRef(ref) || ref == node.Ref || set[ref] {
					return invalid("invalid or repeated Catalog relation")
				}
				set[ref] = true
			}
		}
		uses[node.Ref], dependents[node.Ref] = stringSet(node.Uses), stringSet(node.Dependents)
		if node.Presentation != nil && (!validText(node.Presentation.Title) ||
			node.Presentation.Description != nil && !validText(*node.Presentation.Description)) {
			return invalid("invalid Catalog presentation")
		}
		for namespace, value := range node.Extensions {
			if !validNamespace(namespace) {
				return invalid("Catalog extension namespace requires owner/slash")
			}
			if _, ok := value.(map[string]any); !ok {
				return invalid("Catalog extension value must be an object")
			}
		}
		definitions := 0
		if node.Role != nil {
			definitions++
			if node.Kind != model.RoleKind || !validText(node.Role.AuthoredInstructions) || !validText(node.Role.EffectiveInstructions) {
				return invalid("invalid Catalog Role definition")
			}
		}
		if node.Skill != nil {
			definitions++
			if node.Kind != model.SkillKind || !validText(node.Skill.AuthoredInstructions) || !validText(node.Skill.EffectiveInstructions) {
				return invalid("invalid Catalog Skill definition")
			}
		}
		if node.Tool != nil {
			definitions++
			tool := node.Tool
			if node.Kind != model.ToolKind || tool.ExecutionAvailable != app.ExecutionBound ||
				(tool.ExecutionAvailable && (tool.ReadOnly == nil || tool.InputSchema == nil)) ||
				(!tool.ExecutionAvailable && (tool.ReadOnly != nil || tool.InputSchema != nil)) {
				return invalid("invalid Catalog Tool definition")
			}
		}
		if (app.Detail == SummaryDetail && definitions != 0) || (app.Detail == DefinitionDetail && definitions != 1) {
			return invalid("Catalog detail and definitions disagree")
		}
	}
	if counts != data.Counts {
		return invalid("Catalog counts disagree")
	}
	roots := stringSet(data.Roots)
	if len(roots) != len(data.Roots) {
		return invalid("Catalog roots must be unique")
	}
	for ordinal, ref := range data.Roots {
		root, exists := byRef[ref]
		if !exists || root.ParentRef != nil || root.Ordinal != ordinal {
			return invalid("Catalog roots/parents/ordinals disagree")
		}
	}
	for _, node := range data.Nodes {
		if node.ParentRef == nil {
			if !roots[node.Ref] {
				return invalid("parentless node must be a Catalog root")
			}
		} else {
			parent, exists := byRef[*node.ParentRef]
			if !exists || parent.Kind != model.RoleKind || parent.Ref == node.Ref || parent.Plane != node.Plane ||
				node.Ordinal >= len(parent.Members) || parent.Members[node.Ordinal].Ref != node.Ref {
				return invalid("Catalog parent/member/ordinal facts disagree")
			}
		}
		if node.Kind != model.RoleKind && len(node.Members) != 0 {
			return invalid("only Catalog Roles contain members")
		}
		memberRefs := map[string]bool{}
		processes := map[MemberSlot]string{}
		for ordinal, member := range node.Members {
			expected := model.RoleKind
			switch member.Slot {
			case PreProcessSlot, ChildSlot, PostProcessSlot:
			case SkillSlot:
				expected = model.SkillKind
			case ToolSlot:
				expected = model.ToolKind
			default:
				return invalid("unknown Catalog member slot")
			}
			target, exists := byRef[member.Ref]
			if !exists || !validRef(member.Ref) || memberRefs[member.Ref] || member.Kind != expected ||
				target.Kind != member.Kind || target.ParentRef == nil || *target.ParentRef != node.Ref || target.Ordinal != ordinal {
				return invalid("Catalog member slots disagree with targets")
			}
			memberRefs[member.Ref] = true
			if member.Slot == PreProcessSlot || member.Slot == PostProcessSlot {
				if processes[member.Slot] != "" {
					return invalid("Catalog process slot repeated")
				}
				processes[member.Slot] = member.Ref
			}
		}
		if node.Role != nil {
			for slot, designation := range map[MemberSlot]*string{
				PreProcessSlot: node.Role.PreProcessRef, PostProcessSlot: node.Role.PostProcessRef,
			} {
				expected := processes[slot]
				if (designation == nil) != (expected == "") || designation != nil && *designation != expected {
					return invalid("Catalog process designation disagrees with members")
				}
			}
		}
		for _, ref := range node.Uses {
			if !dependents[ref][node.Ref] {
				return invalid("Catalog uses/dependents must be reciprocal")
			}
		}
		for _, ref := range node.Dependents {
			if !uses[ref][node.Ref] {
				return invalid("Catalog uses/dependents must be reciprocal")
			}
		}
	}
	// Every parent chain must terminate at a root, including envelopes imported
	// by consumers. Shared visited colors make cycle validation linear.
	colors := map[string]uint8{}
	var visitParent func(string) error
	visitParent = func(ref string) error {
		if colors[ref] == 1 {
			return invalid("Catalog containment cycle")
		}
		if colors[ref] == 2 {
			return nil
		}
		colors[ref] = 1
		if parent := byRef[ref].ParentRef; parent != nil {
			if err := visitParent(*parent); err != nil {
				return err
			}
		}
		colors[ref] = 2
		return nil
	}
	for _, node := range data.Nodes {
		if err := visitParent(node.Ref); err != nil {
			return err
		}
	}
	return nil
}

func stringSet(refs []string) map[string]bool {
	result := map[string]bool{}
	for _, ref := range refs {
		result[ref] = true
	}
	return result
}

func pointerValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

// AsJSON returns fresh native JSON; opaque schema/extension numbers stay exact.
func (node NodeDescriptor) AsJSON() (map[string]any, error) {
	return detachedObject(node.jsonValue())
}

func (node NodeDescriptor) jsonValue() map[string]any {
	members := make([]any, len(node.Members))
	for i, member := range node.Members {
		members[i] = map[string]any{"ref": member.Ref, "kind": string(member.Kind), "slot": string(member.Slot)}
	}
	var presentation, role, skill, tool any
	if node.Presentation != nil {
		value := map[string]any{"title": node.Presentation.Title}
		if node.Presentation.Description != nil {
			value["description"] = *node.Presentation.Description
		}
		presentation = value
	}
	if node.Role != nil {
		role = map[string]any{
			"authored_instructions": node.Role.AuthoredInstructions, "effective_instructions": node.Role.EffectiveInstructions,
			"pre_process_ref": pointerValue(node.Role.PreProcessRef), "post_process_ref": pointerValue(node.Role.PostProcessRef),
		}
	}
	if node.Skill != nil {
		skill = map[string]any{"authored_instructions": node.Skill.AuthoredInstructions, "effective_instructions": node.Skill.EffectiveInstructions}
	}
	if node.Tool != nil {
		value := map[string]any{"execution_available": node.Tool.ExecutionAvailable}
		if node.Tool.ExecutionAvailable {
			value["read_only"] = pointerValue(node.Tool.ReadOnly)
			value["input_schema"] = node.Tool.InputSchema
		}
		tool = value
	}
	value := map[string]any{
		"ref": node.Ref, "kind": string(node.Kind), "name": node.Name, "description": node.Description,
		"presentation": presentation, "plane": node.Plane, "parent_ref": pointerValue(node.ParentRef),
		"ordinal": node.Ordinal, "members": members, "uses": node.Uses, "dependents": node.Dependents,
		"role": role, "skill": skill, "tool": tool, "extensions": node.Extensions,
	}
	return value
}

func bodyJSON(data SnapshotData) map[string]any {
	nodes := make([]any, len(data.Nodes))
	for i, node := range data.Nodes {
		nodes[i] = node.jsonValue()
	}
	app := data.Application
	return map[string]any{
		"schema": Schema,
		"application": map[string]any{
			"name": app.Name, "mode": app.Mode, "view": string(app.View), "detail": string(app.Detail),
			"locale": pointerValue(app.Locale), "execution_bound": app.ExecutionBound,
		},
		"roots":  append([]string{}, data.Roots...),
		"counts": map[string]any{"role": data.Counts.Role, "skill": data.Counts.Skill, "tool": data.Counts.Tool},
		"nodes":  nodes,
	}
}

func (snapshot *NodeCatalogSnapshot) ContentDigest() string { return snapshot.digest }
func (snapshot *NodeCatalogSnapshot) Application() ApplicationDescriptor {
	result := snapshot.data.Application
	result.Locale = ptrCopy(result.Locale)
	return result
}
func (snapshot *NodeCatalogSnapshot) Roots() []string {
	return append([]string{}, snapshot.data.Roots...)
}
func (snapshot *NodeCatalogSnapshot) Counts() Counts { return snapshot.data.Counts }
func (snapshot *NodeCatalogSnapshot) Nodes() []NodeDescriptor {
	result := make([]NodeDescriptor, len(snapshot.data.Nodes))
	for i, node := range snapshot.data.Nodes {
		result[i], _ = cloneDescriptor(node)
	}
	return result
}
func (snapshot *NodeCatalogSnapshot) AsJSON() map[string]any {
	value := bodyJSON(snapshot.data)
	value["content_digest"] = snapshot.digest
	copy, _ := detachedObject(value)
	return copy
}
func (snapshot *NodeCatalogSnapshot) ToJSON(indent *int) (string, error) {
	return FormatJSON(snapshot.AsJSON(), JSONOptions{Indent: indent, Spaced: indent == nil})
}
func (snapshot *NodeCatalogSnapshot) MarshalJSON() ([]byte, error) {
	text, err := snapshot.ToJSON(nil)
	return []byte(text), err
}

// SnapshotFromJSON validates an imported strict envelope and its content digest.
func SnapshotFromJSON(text string) (*NodeCatalogSnapshot, error) {
	value, err := ParseJSON(text)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalid("Catalog envelope must be an object")
	}
	schema, _ := object["schema"].(string)
	digest, _ := object["content_digest"].(string)
	if schema != Schema || len(object) != 6 {
		return nil, invalid("invalid Catalog envelope")
	}
	// Strict typed decoding rejects unknown fields. Required nested shape is
	// checked against the typed serialization below, including explicit nulls.
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var envelope struct {
		Schema        string `json:"schema"`
		ContentDigest string `json:"content_digest"`
		SnapshotData
	}
	if err := decoder.Decode(&envelope); err != nil {
		return nil, invalid("invalid Catalog shape: " + err.Error())
	}
	snapshot, err := NewSnapshot(envelope.SnapshotData)
	if err != nil {
		return nil, err
	}
	actual, err := FormatJSON(object, JSONOptions{})
	if err != nil {
		return nil, err
	}
	expected, err := FormatJSON(snapshot.AsJSON(), JSONOptions{})
	if err != nil {
		return nil, err
	}
	if snapshot.digest != digest || actual != expected {
		return nil, invalid("Catalog required shape or content digest mismatch")
	}
	return snapshot, nil
}
