package graph

// This file implements a bounded managed candidate, not the public Catalog API.
// Registry evidence is supplied by the caller; it is not execution authority.
import (
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type Kind string
type Plane string
type Slot string
type Mutable string

const (
	Role         Kind    = "role"
	Skill        Kind    = "skill"
	Tool         Kind    = "tool"
	ModelPlane   Plane   = "model"
	PromptPlane  Plane   = "prompt"
	PreProcess   Slot    = "pre_process"
	Children     Slot    = "children"
	PostProcess  Slot    = "post_process"
	Skills       Slot    = "skills"
	Tools        Slot    = "tools"
	Inherit      Mutable = "inherit"
	MutableTrue  Mutable = "true"
	MutableFalse Mutable = "false"
)

type Placement struct {
	Slot    Slot
	Ordinal int
	NodeID  string
}
type Members struct {
	pre, post               string
	children, skills, tools []string
}

func (m Members) Placements() []Placement {
	out := []Placement{}
	if m.pre != "" {
		out = append(out, Placement{PreProcess, 0, m.pre})
	}
	for i, id := range m.children {
		out = append(out, Placement{Children, i, id})
	}
	if m.post != "" {
		out = append(out, Placement{PostProcess, 0, m.post})
	}
	for i, id := range m.skills {
		out = append(out, Placement{Skills, i, id})
	}
	for i, id := range m.tools {
		out = append(out, Placement{Tools, i, id})
	}
	return out
}

type TemplatePart struct {
	Kind, Text, TargetID, Label string
	HasLabel                    bool
}
type Instructions struct {
	text     string
	template bool
	parts    []TemplatePart
}

func (i Instructions) Text() string          { return i.text }
func (i Instructions) IsTemplate() bool      { return i.template }
func (i Instructions) Parts() []TemplatePart { return slices.Clone(i.parts) }

type Portability struct{ Kind, ContractID, Language, Version, Factory string }
type BindingDescriptor struct {
	data                      FrozenJSON
	kind, id, version, digest string
	portability               Portability
	secrets                   []string
}

func (b BindingDescriptor) Kind() string                   { return b.kind }
func (b BindingDescriptor) BindingID() string              { return b.id }
func (b BindingDescriptor) Version() string                { return b.version }
func (b BindingDescriptor) ExpectedContractDigest() string { return b.digest }
func (b BindingDescriptor) Portability() Portability       { return b.portability }
func (b BindingDescriptor) SecretRefs() []string           { return slices.Clone(b.secrets) }
func (b BindingDescriptor) AsJSON() any                    { return b.data.JSON() }

type NodeSpec struct {
	id, name, description, variant string
	kind                           Kind
	mutable                        Mutable
	uses                           []string
	members                        Members
	instructions                   Instructions
	binding                        *BindingDescriptor
	data                           FrozenJSON
}

func (n NodeSpec) NodeID() string             { return n.id }
func (n NodeSpec) Name() string               { return n.name }
func (n NodeSpec) Description() string        { return n.description }
func (n NodeSpec) Kind() Kind                 { return n.kind }
func (n NodeSpec) RoleVariant() string        { return n.variant }
func (n NodeSpec) Mutable() Mutable           { return n.mutable }
func (n NodeSpec) Uses() []string             { return slices.Clone(n.uses) }
func (n NodeSpec) Members() Members           { return n.members }
func (n NodeSpec) Instructions() Instructions { return n.instructions }
func (n NodeSpec) Binding() (BindingDescriptor, bool) {
	if n.binding == nil {
		return BindingDescriptor{}, false
	}
	return *n.binding, true
}
func (n NodeSpec) AsJSON() any     { return n.data.JSON() }
func (n NodeSpec) Extensions() any { return n.AsJSON().(map[string]any)["extensions"] }
func (n NodeSpec) InputContract() (any, bool) {
	v, ok := n.AsJSON().(map[string]any)["input_contract"]
	return v, ok
}

type RootEntry struct {
	NodeID string
	Plane  Plane
}
type Publication struct {
	Kind, PublicationID, Name, Description, OpensID, URI, MIMEType, Method, Path string
	ModelMayOpen                                                                 bool
}
type Presentation struct {
	NodeID, Title, Description string
	HasTitle, HasDescription   bool
}
type PresentationCatalog struct {
	locale  string
	entries []Presentation
}

func (p PresentationCatalog) Locale() string          { return p.locale }
func (p PresentationCatalog) Entries() []Presentation { return slices.Clone(p.entries) }

type Presentations struct {
	defaultLocale string
	catalogs      []PresentationCatalog
}

func (p Presentations) DefaultLocale() string           { return p.defaultLocale }
func (p Presentations) Catalogs() []PresentationCatalog { return slices.Clone(p.catalogs) }

// PublicationContract is validated, immutable pinned registry data, not an author assertion.
type PublicationContract struct {
	binding             BindingDescriptor
	effect              string
	argumentFree, valid bool
}

func NewPublicationContract(binding any, effect string, argumentFree bool) (out PublicationContract, err error) {
	defer catchModel(&err)
	f := mustFreeze(binding)
	b := parseBinding(f.JSON())
	out = PublicationContract{b, enum(effect, "read_only write"), argumentFree, true}
	return
}
func (c PublicationContract) Binding() BindingDescriptor { return c.binding }
func (c PublicationContract) Effect() string             { return c.effect }
func (c PublicationContract) ArgumentFree() bool         { return c.argumentFree }

// GraphDefinition owns all model state. The zero value is invalid. All JSON and
// slice getters return detached values; private maps never escape. Inputs must
// not be mutated concurrently with construction. Completed snapshots are safe
// for concurrent reads, including reads of copies of their value handles.
type GraphDefinition struct {
	graphID, applicationName, mode string
	nodes                          map[string]NodeSpec
	roots                          []RootEntry
	publications                   []Publication
	presentations                  Presentations
	data                           FrozenJSON
	valid                          bool
}

func (g *GraphDefinition) GraphID() string              { return g.graphID }
func (g *GraphDefinition) ApplicationName() string      { return g.applicationName }
func (g *GraphDefinition) Mode() string                 { return g.mode }
func (g *GraphDefinition) Roots() []RootEntry           { return slices.Clone(g.roots) }
func (g *GraphDefinition) Publications() []Publication  { return slices.Clone(g.publications) }
func (g *GraphDefinition) Presentations() Presentations { return g.presentations }
func (g *GraphDefinition) AsJSON() any                  { return g.data.JSON() }
func (g *GraphDefinition) Digest() (string, error) {
	if g == nil || !g.valid {
		return "", invalid("Invalid graph definition")
	}
	return CanonicalDigest(g.data.value, "contexture.graph/v1")
}
func (g *GraphDefinition) Node(id string) (NodeSpec, bool) { n, ok := g.nodes[id]; return n, ok }
func (g *GraphDefinition) Nodes() []NodeSpec {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]NodeSpec, 0, len(ids))
	for _, id := range ids {
		out = append(out, g.nodes[id])
	}
	return out
}

func failModel(message string)                   { panic(invalid(message)) }
func failModelAt(message, location, code string) { panic(diagnostic(code, message, location)) }
func parseNodeAt(value any, ordinal int) (out NodeSpec) {
	defer func() {
		if v := recover(); v != nil {
			if e, ok := v.(*GraphError); ok {
				panic(atLocation(e, "/nodes/"+strconv.Itoa(ordinal)))
			}
			panic(v)
		}
	}()
	return parseNode(value)
}
func catchModel(err *error) {
	if v := recover(); v != nil {
		if e, ok := v.(*GraphError); ok {
			*err = e
		} else {
			panic(v)
		}
	}
}
func mustFreeze(v any) FrozenJSON {
	f, e := FreezeJSON(v)
	if e != nil {
		panic(e)
	}
	return f
}
func obj(v any, required, optional string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		failModel("Expected an object")
	}
	allowed := strings.Fields(required + " " + optional)
	for _, key := range strings.Fields(required) {
		if _, ok := m[key]; !ok {
			failModel("Missing required field: " + key)
		}
	}
	for key := range m {
		if !slices.Contains(allowed, key) {
			failModel("Unsupported core field: " + key)
		}
	}
	return m
}
func arr(v any) []any {
	a, ok := v.([]any)
	if !ok {
		failModel("Expected an array")
	}
	return a
}
func txt(v any, empty bool) string {
	s, ok := v.(string)
	if !ok || (!empty && s == "") {
		failModel("Expected text")
	}
	return s
}
func enum(v any, choices string) string {
	s := txt(v, false)
	if !slices.Contains(strings.Fields(choices), s) {
		failModel("Invalid enum value")
	}
	return s
}
func flag(v any) bool {
	b, ok := v.(bool)
	if !ok {
		failModel("Expected a boolean")
	}
	return b
}
func ident(v any) string {
	s, e := ValidateID(txt(v, false))
	if e != nil {
		panic(e)
	}
	return s
}
func authorName(v any) string {
	s := txt(v, false)
	for _, c := range s {
		if c == '/' || c < 32 || (c >= 127 && c <= 159) {
			failModel("Names cannot contain slash or control characters")
		}
	}
	return s
}
func get(m map[string]any, k string, fallback any) any {
	if v, ok := m[k]; ok {
		return v
	}
	return fallback
}
func ids(v any) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range arr(v) {
		id := ident(x)
		if seen[id] {
			failModel("Duplicate identity")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
func jsonIDs(v []string) []any {
	a := make([]any, len(v))
	for i, x := range v {
		a[i] = x
	}
	return a
}

var extensionKey = regexp.MustCompile(`^[a-z][a-z0-9.-]*:[A-Za-z0-9_.-]+$`)
var contractDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func jsonObject(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		failModel("Expected a JSON object")
	}
	return m
}
func parseExtensions(v any) map[string]any {
	m := jsonObject(v)
	for k := range m {
		if !extensionKey.MatchString(k) {
			failModel("Extension keys must be namespace:key")
		}
	}
	return m
}

func parseBinding(v any) BindingDescriptor {
	m := obj(v, "kind binding_id version portability", "config secret_refs expected_contract_digest")
	p := obj(m["portability"], "kind", "contract_id language version factory")
	port := Portability{Kind: enum(p["kind"], "portable native")}
	if port.Kind == "portable" {
		obj(p, "kind contract_id", "")
		port.ContractID = ident(p["contract_id"])
	} else {
		obj(p, "kind language version factory", "")
		port.Language = enum(p["language"], "python typescript go")
		port.Version = txt(p["version"], false)
		port.Factory = txt(p["factory"], false)
	}
	b := BindingDescriptor{kind: enum(m["kind"], "registered remote"), id: ident(m["binding_id"]), version: txt(m["version"], false), portability: port, secrets: ids(get(m, "secret_refs", []any{}))}
	if d, ok := m["expected_contract_digest"]; ok {
		b.digest = txt(d, false)
		if !contractDigest.MatchString(b.digest) {
			failModel("Invalid expected contract digest")
		}
	}
	n := map[string]any{"kind": b.kind, "binding_id": b.id, "version": b.version, "portability": p, "config": jsonObject(get(m, "config", map[string]any{})), "secret_refs": jsonIDs(b.secrets)}
	if b.digest != "" {
		n["expected_contract_digest"] = b.digest
	}
	b.data = mustFreeze(n)
	return b
}

func parseTemplate(v any) (Instructions, map[string]any) {
	m := obj(v, "parts", "")
	out := Instructions{template: true}
	parts := []any{}
	for _, raw := range arr(m["parts"]) {
		p := obj(raw, "kind", "text target_id label")
		part := TemplatePart{Kind: enum(p["kind"], "Text NodeRef")}
		if part.Kind == "Text" {
			obj(p, "kind text", "")
			part.Text = txt(p["text"], true)
		} else {
			obj(p, "kind target_id", "label")
			part.TargetID = ident(p["target_id"])
			if v, ok := p["label"]; ok {
				part.Label = txt(v, true)
				part.HasLabel = true
			}
		}
		out.parts = append(out.parts, part)
		parts = append(parts, p)
	}
	return out, map[string]any{"parts": parts}
}

func parseMembers(v any) (Members, map[string]any) {
	m := obj(v, "", "pre_process children post_process skills tools")
	out := Members{children: ids(get(m, "children", []any{})), skills: ids(get(m, "skills", []any{})), tools: ids(get(m, "tools", []any{}))}
	n := map[string]any{"children": jsonIDs(out.children), "skills": jsonIDs(out.skills), "tools": jsonIDs(out.tools)}
	if v, ok := m["pre_process"]; ok {
		out.pre = ident(v)
		n["pre_process"] = out.pre
	}
	if v, ok := m["post_process"]; ok {
		out.post = ident(v)
		n["post_process"] = out.post
	}
	return out, n
}

func parseNode(v any) NodeSpec {
	m := obj(v, "node_id kind name description", "mutable uses extensions instructions instruction_template role_variant members binding input_contract")
	n := NodeSpec{id: ident(m["node_id"]), name: authorName(m["name"]), description: txt(m["description"], false), kind: Kind(enum(m["kind"], "role skill tool")), uses: ids(get(m, "uses", []any{})), mutable: Inherit}
	mut := get(m, "mutable", "inherit")
	if b, ok := mut.(bool); ok {
		if b {
			n.mutable = MutableTrue
		} else {
			n.mutable = MutableFalse
		}
	} else {
		enum(mut, "inherit")
	}
	out := map[string]any{"node_id": n.id, "name": n.name, "description": n.description, "kind": string(n.kind), "mutable": mut, "uses": jsonIDs(n.uses), "extensions": parseExtensions(get(m, "extensions", map[string]any{}))}
	_, hasMembers := m["members"]
	_, hasVariant := m["role_variant"]
	_, hasText := m["instructions"]
	_, hasTemplate := m["instruction_template"]
	_, hasBinding := m["binding"]
	_, hasContract := m["input_contract"]
	if n.kind != Role && (hasMembers || hasVariant) {
		failModel("Only Roles contain members or have a variant")
	}
	if n.kind == Tool {
		if hasText || hasTemplate {
			failModel("Tools cannot have instructions")
		}
		if hasBinding {
			b := parseBinding(m["binding"])
			n.binding = &b
			out["binding"] = b.AsJSON()
		}
		if hasContract {
			validateInputContract(m["input_contract"])
			out["input_contract"] = m["input_contract"]
		}
	} else {
		if hasBinding || hasContract {
			failModel("Only Tools have execution assertions")
		}
		if hasText == hasTemplate {
			failModel("Supply exactly one instruction representation")
		}
		if hasText {
			n.instructions.text = txt(m["instructions"], true)
			out["instructions"] = n.instructions.text
		} else {
			var normalized map[string]any
			n.instructions, normalized = parseTemplate(m["instruction_template"])
			out["instruction_template"] = normalized
		}
		if n.kind == Role {
			n.variant = enum(get(m, "role_variant", "ordinary"), "ordinary pre_process post_process")
			out["role_variant"] = n.variant
			var normalized map[string]any
			n.members, normalized = parseMembers(get(m, "members", map[string]any{}))
			out["members"] = normalized
		}
	}
	n.data = mustFreeze(out)
	return n
}

// Assertion syntax only: no regex compilation, reference resolution, fetching,
// or execution validation. Unsupported vocabulary is rejected, not ignored.
func validateInputContract(value any) {
	stack := []any{value}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := v.(bool); ok {
			continue
		}
		m := jsonObject(v)
		for key, item := range m {
			switch {
			case slices.Contains(strings.Fields("$id $schema $ref $anchor $comment title description format contentEncoding contentMediaType"), key):
				txt(item, true)
			case slices.Contains(strings.Fields("minimum maximum exclusiveMinimum exclusiveMaximum multipleOf"), key):
				x, ok := item.(float64)
				if !ok || (key == "multipleOf" && x <= 0) {
					failModel("Invalid JSON Schema numeric keyword")
				}
			case slices.Contains(strings.Fields("minLength maxLength minItems maxItems minContains maxContains minProperties maxProperties"), key):
				x, ok := item.(float64)
				if !ok || x < 0 || x > float64(MaxSafeInteger) || x != float64(int64(x)) {
					failModel("Invalid JSON Schema count")
				}
			case slices.Contains(strings.Fields("readOnly writeOnly deprecated uniqueItems"), key):
				flag(item)
			case key == "type":
				values, ok := item.([]any)
				if !ok {
					values = []any{item}
				}
				if len(values) == 0 {
					failModel("Empty JSON Schema type")
				}
				seen := map[string]bool{}
				for _, v := range values {
					t := enum(v, "null boolean object array number integer string")
					if seen[t] {
						failModel("Duplicate JSON Schema type")
					}
					seen[t] = true
				}
			case key == "required":
				uniqueTexts(item)
			case key == "enum":
				values := arr(item)
				if len(values) == 0 {
					failModel("Empty JSON Schema enum")
				}
				seen := map[string]bool{}
				for _, v := range values {
					digest, err := CanonicalDigest(v, "contexture.graph/v1")
					if err != nil {
						panic(err)
					}
					if seen[digest] {
						failModel("Duplicate JSON Schema enum")
					}
					seen[digest] = true
				}
			case key == "const" || key == "default": // Inert canonical JSON.
			case key == "examples":
				arr(item)
			case slices.Contains(strings.Fields("items contains additionalProperties propertyNames not if then else unevaluatedItems unevaluatedProperties contentSchema"), key):
				stack = append(stack, item)
			case slices.Contains(strings.Fields("properties $defs dependentSchemas"), key):
				for _, v := range jsonObject(item) {
					stack = append(stack, v)
				}
			case slices.Contains(strings.Fields("allOf anyOf oneOf prefixItems"), key):
				values := arr(item)
				if key != "prefixItems" && len(values) == 0 {
					failModel("Empty schema combinator")
				}
				stack = append(stack, values...)
			case key == "dependentRequired":
				for _, v := range jsonObject(item) {
					uniqueTexts(v)
				}
			default:
				failModel("Unsupported input_contract profile keyword: " + key)
			}
		}
	}
}
func uniqueTexts(v any) {
	seen := map[string]bool{}
	for _, x := range arr(v) {
		s := txt(x, true)
		if seen[s] {
			failModel("Duplicate schema requirement")
		}
		seen[s] = true
	}
}

var absoluteScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
var ipvFuture = regexp.MustCompile(`^[vV][a-fA-F0-9]+\..+$`)

func resourceURI(v any) string {
	s := txt(v, false)
	scheme := absoluteScheme.FindString(s)
	if scheme == "" {
		failModel("Resource URI must be absolute")
	}
	for _, c := range s {
		if c <= 32 || unicode.IsSpace(c) || c == 0x85 {
			failModel("Resource URI contains whitespace")
		}
	}
	rest := s[len(scheme):]
	if !strings.HasPrefix(rest, "//") {
		return s
	}
	// Split without discarding an empty authority.
	host := rest[2:]
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	// The bounded Go profile refuses non-ASCII authorities: no partial NFKC
	// implementation or silent acceptance of compatibility delimiters.
	for _, c := range host {
		if c > 127 {
			failModel("Unsupported non-ASCII resource authority")
		}
	}
	if strings.Contains(host, "[") != strings.Contains(host, "]") {
		failModel("Unbalanced IP literal")
	}
	if strings.Contains(host, "[") {
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		end := strings.IndexByte(host, ']')
		if !strings.HasPrefix(host, "[") || end < 0 || (end+1 < len(host) && host[end+1] != ':') {
			failModel("Invalid bracketed authority")
		}
		literal := host[1:end]
		if strings.HasPrefix(strings.ToLower(literal), "v") {
			if !ipvFuture.MatchString(literal) {
				failModel("Invalid IPvFuture literal")
			}
		} else {
			scoped := strings.Split(literal, "%")
			if len(scoped) > 2 || (len(scoped) == 2 && scoped[1] == "") {
				failModel("Invalid IPv6 scope")
			}
			address, err := netip.ParseAddr(literal)
			if err != nil || !address.Is6() {
				failModel("Invalid IPv6 literal")
			}
		}
	}
	return s
}
func parsePublication(v any) Publication {
	m := obj(v, "kind publication_id opens_id", "name description model_may_open uri mime_type method path")
	p := Publication{Kind: enum(m["kind"], "prompt resource route"), PublicationID: ident(m["publication_id"]), OpensID: ident(m["opens_id"])}
	switch p.Kind {
	case "prompt":
		obj(m, "kind publication_id name description opens_id model_may_open", "")
		p.Name = authorName(m["name"])
		p.Description = txt(m["description"], false)
		p.ModelMayOpen = flag(m["model_may_open"])
	case "resource":
		obj(m, "kind publication_id name uri mime_type opens_id", "")
		p.Name = authorName(m["name"])
		p.URI = resourceURI(m["uri"])
		p.MIMEType = txt(m["mime_type"], false)
	case "route":
		obj(m, "kind publication_id method path opens_id", "")
		p.Method = enum(m["method"], "GET HEAD POST PUT PATCH DELETE")
		p.Path = txt(m["path"], false)
		if !strings.HasPrefix(p.Path, "/") {
			failModel("Route path must start with slash")
		}
	}
	return p
}

var localePart = regexp.MustCompile(`^[A-Za-z0-9]{1,8}$`)
var localeLanguage = regexp.MustCompile(`^[A-Za-z]{2,8}$`)
var localeExtlang = regexp.MustCompile(`^[A-Za-z]{3}$`)
var localeScript = regexp.MustCompile(`^[A-Za-z]{4}$`)
var localeRegion = regexp.MustCompile(`^(?:[A-Za-z]{2}|[0-9]{3})$`)
var localeVariant = regexp.MustCompile(`^(?:[A-Za-z0-9]{5,8}|[0-9][A-Za-z0-9]{3})$`)

// NormalizeLocale performs RFC5646 syntax/casing, not IANA alias rewriting.
func NormalizeLocale(value string) (out string, err error) {
	defer catchModel(&err)
	out = normalizeLocale(value)
	return
}
func normalizeLocale(value any) string {
	tag := txt(value, false)
	lower := strings.ToLower(tag)
	for _, old := range strings.Fields("en-GB-oed i-ami i-bnn i-default i-enochian i-hak i-klingon i-lux i-mingo i-navajo i-pwn i-tao i-tay i-tsu sgn-BE-FR sgn-BE-NL sgn-CH-DE art-lojban cel-gaulish no-bok no-nyn zh-guoyu zh-hakka zh-min zh-min-nan zh-xiang") {
		if lower == strings.ToLower(old) {
			return old
		}
	}
	parts := strings.Split(tag, "-")
	for _, p := range parts {
		if !localePart.MatchString(p) {
			failModel("Invalid BCP47 locale")
		}
	}
	if strings.EqualFold(parts[0], "x") {
		if len(parts) == 1 {
			failModel("Private-use locale requires a subtag")
		}
		return lower
	}
	if !localeLanguage.MatchString(parts[0]) {
		failModel("Invalid locale language")
	}
	result := []string{strings.ToLower(parts[0])}
	i := 1
	if len(parts[0]) <= 3 {
		for count := 0; count < 3 && i < len(parts) && localeExtlang.MatchString(parts[i]); count++ {
			result = append(result, strings.ToLower(parts[i]))
			i++
		}
	}
	if i < len(parts) && localeScript.MatchString(parts[i]) {
		s := strings.ToLower(parts[i])
		result = append(result, strings.ToUpper(s[:1])+s[1:])
		i++
	}
	if i < len(parts) && localeRegion.MatchString(parts[i]) {
		result = append(result, strings.ToUpper(parts[i]))
		i++
	}
	variants := map[string]bool{}
	for i < len(parts) && localeVariant.MatchString(parts[i]) {
		s := strings.ToLower(parts[i])
		if variants[s] {
			failModel("Duplicate locale variant")
		}
		variants[s] = true
		result = append(result, s)
		i++
	}
	singletons := map[string]bool{}
	for i < len(parts) {
		s := strings.ToLower(parts[i])
		i++
		if len(s) != 1 || singletons[s] {
			failModel("Invalid or duplicate locale extension")
		}
		singletons[s] = true
		result = append(result, s)
		start := i
		for i < len(parts) && (s == "x" || len(parts[i]) >= 2) {
			result = append(result, strings.ToLower(parts[i]))
			i++
		}
		if i == start {
			failModel("Locale extension requires subtags")
		}
	}
	return strings.Join(result, "-")
}
func parsePresentations(v any) (Presentations, map[string]any) {
	m := obj(v, "default_locale catalogs", "")
	out := Presentations{defaultLocale: normalizeLocale(m["default_locale"])}
	catalogs := []any{}
	seen := map[string]bool{}
	for _, raw := range arr(m["catalogs"]) {
		c := obj(raw, "locale entries", "")
		locale := normalizeLocale(c["locale"])
		key := strings.ToLower(locale)
		if seen[key] {
			failModel("Duplicate normalized locale")
		}
		seen[key] = true
		catalog := PresentationCatalog{locale: locale}
		entries := []any{}
		targets := map[string]bool{}
		for _, rawEntry := range arr(c["entries"]) {
			e := obj(rawEntry, "node_id", "title description")
			p := Presentation{NodeID: ident(e["node_id"])}
			if targets[p.NodeID] {
				failModel("Duplicate presentation target")
			}
			targets[p.NodeID] = true
			if title, ok := e["title"]; ok {
				p.Title = txt(title, false)
				p.HasTitle = true
			}
			if description, ok := e["description"]; ok {
				p.Description = txt(description, false)
				p.HasDescription = true
			}
			if !p.HasTitle && !p.HasDescription {
				failModel("Presentation requires title or description")
			}
			catalog.entries = append(catalog.entries, p)
			entries = append(entries, e)
		}
		out.catalogs = append(out.catalogs, catalog)
		catalogs = append(catalogs, map[string]any{"locale": locale, "entries": entries})
	}
	return out, map[string]any{"default_locale": out.defaultLocale, "catalogs": catalogs}
}

// NewGraphDefinition accepts detached native JSON or strict raw JSON bytes.
// It refuses unsupported fields and builds an index before returning a snapshot.
func NewGraphDefinition(value any, contracts map[string]PublicationContract) (out *GraphDefinition, err error) {
	defer catchModel(&err)
	if data, ok := value.([]byte); ok {
		value, err = DecodeJSON(data)
		if err != nil {
			return nil, err
		}
	}
	frozen := mustFreeze(value)
	m := obj(frozen.JSON(), "schema semantic_version graph_id application_name mode nodes roots presentations", "publications extensions")
	if m["schema"] != "contexture.graph/v1" || m["semantic_version"] != "1" {
		failModel("Unsupported graph schema or semantic version")
	}
	// Match the reference's explicitly located graph identity validation.
	graphID, ok := m["graph_id"].(string)
	if !ok {
		failModelAt("Identity must be a string", "/graph_id", "schema_invalid")
	}
	if _, e := ValidateID(graphID); e != nil {
		panic(atLocation(e, "/graph_id"))
	}
	g := &GraphDefinition{graphID: graphID, applicationName: authorName(m["application_name"]), mode: enum(m["mode"], "runtime disclosure_only"), nodes: map[string]NodeSpec{}, roots: []RootEntry{}, publications: []Publication{}}
	for i, raw := range arr(m["nodes"]) {
		n := parseNodeAt(raw, i)
		if _, ok := g.nodes[n.id]; ok {
			failModelAt("Duplicate node identity", "/nodes/"+strconv.Itoa(i)+"/node_id", "schema_invalid")
		}
		g.nodes[n.id] = n
	}
	roots := []any{}
	for _, raw := range arr(m["roots"]) {
		r := obj(raw, "node_id plane", "")
		entry := RootEntry{ident(r["node_id"]), Plane(enum(r["plane"], "model prompt"))}
		g.roots = append(g.roots, entry)
		roots = append(roots, r)
	}
	publications := []any{}
	for _, raw := range arr(get(m, "publications", []any{})) {
		g.publications = append(g.publications, parsePublication(raw))
		publications = append(publications, raw)
	}
	var presentations map[string]any
	g.presentations, presentations = parsePresentations(m["presentations"])
	nodes := []any{}
	for _, n := range g.Nodes() {
		nodes = append(nodes, n.AsJSON())
	}
	g.data = mustFreeze(map[string]any{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": g.graphID, "application_name": g.applicationName, "mode": g.mode, "nodes": nodes, "roots": roots, "publications": publications, "presentations": presentations, "extensions": parseExtensions(get(m, "extensions", map[string]any{}))})
	g.valid = true
	if _, err = BuildGraphIndex(g, contracts); err != nil {
		return nil, err
	}
	return g, nil
}
