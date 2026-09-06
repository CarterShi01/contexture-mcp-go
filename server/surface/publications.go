package surface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

// PromptCard describes one MCP Prompt publication.
type PromptCard struct {
	Name        string
	Description string
	Arguments   []PromptArgument
}

// PromptArgument describes one MCP Prompt argument.
type PromptArgument struct {
	Name     string
	Required bool
}

// ResourceCard describes one MCP Resource publication.
type ResourceCard struct {
	Name        string
	URI         string
	Description string
	MIMEType    string
}

// Publications validates and projects prompts, resources, completion, and instructions.
type Publications struct {
	disclosure *contexture.Disclosure
	runtime    *contexture.Runtime
	prompts    []contexture.PromptDeclaration
	resources  []contexture.ResourceDeclaration
}

// NewPublications validates application publications over one compiled surface.
func NewPublications(application *contexture.Application, disclosure *contexture.Disclosure, runtime *contexture.Runtime) (*Publications, error) {
	if application == nil || disclosure == nil {
		return nil, errors.New("Contexture application and Disclosure must not be nil")
	}
	publications := &Publications{disclosure: disclosure, runtime: runtime, prompts: application.Prompts(), resources: application.Resources()}
	if err := publications.validate(); err != nil {
		return nil, err
	}
	return publications, nil
}

// PromptCards returns selected prompt cards plus Contexture's fixed goto prompt.
func (publications *Publications) PromptCards(selection contexture.RootSelection) ([]PromptCard, error) {
	effective, err := publications.effective(selection)
	if err != nil {
		return nil, err
	}
	result := []PromptCard{}
	for _, entry := range publications.prompts {
		if effective.ContainsRef(entry.Opens) {
			result = append(result, PromptCard{Name: publicationName(entry.Name, entry.Opens), Description: fmt.Sprintf("%s (%s)", entry.Description, entry.Opens), Arguments: []PromptArgument{}})
		}
	}
	result = append(result, PromptCard{Name: "goto", Description: "Open any capability this server holds, by reference. The reference completes as you type, so the whole tree can be browsed here without asking the agent to go and look.", Arguments: []PromptArgument{{Name: "ref", Required: true}}})
	return result, nil
}

// ResourceCards returns resources in declaration order.
func (publications *Publications) ResourceCards(selection contexture.RootSelection) []ResourceCard {
	effective, err := publications.effective(selection)
	if err != nil {
		return nil
	}
	result := make([]ResourceCard, 0, len(publications.resources))
	for _, entry := range publications.resources {
		if !effective.ContainsRef(entry.Opens) {
			continue
		}
		result = append(result, ResourceCard{Name: publicationName(entry.Name, entry.Opens), URI: entry.URI, Description: entry.Description, MIMEType: entry.MIMEType})
	}
	return result
}

// Command opens the node reserved by a named person-controlled Prompt.
func (publications *Publications) Command(name string, selection contexture.RootSelection) (string, error) {
	for _, entry := range publications.prompts {
		if publicationName(entry.Name, entry.Opens) == name {
			return publications.openForPerson(entry.Opens, selection)
		}
	}
	return "", fmt.Errorf("No Contexture Prompt named %q.", name)
}

// Goto opens a person-supplied ref through person-controlled navigation.
func (publications *Publications) Goto(ref string, selection contexture.RootSelection) (string, error) {
	return publications.openForPerson(ref, selection)
}

// Complete returns selected canonical refs beginning with input, capped by limit.
func (publications *Publications) Complete(input string, selection contexture.RootSelection, limit int) ([]string, int, error) {
	effective, err := publications.effective(selection)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	type scoredRef struct {
		rank, length int
		ref          string
	}
	scored := []scoredRef{}
	wanted := strings.ToLower(strings.TrimSpace(input))
	for _, ref := range publications.disclosure.Index().Walk() {
		if !effective.ContainsRef(ref) {
			continue
		}
		lowered := strings.ToLower(ref)
		rank := -1
		switch {
		case wanted == "" || strings.HasPrefix(lowered, wanted):
			rank = 0
		case strings.HasPrefix(lowered[strings.LastIndex(lowered, "/")+1:], wanted):
			rank = 1
		case anyRefPartStartsWith(lowered, wanted):
			rank = 2
		case strings.Contains(lowered, wanted):
			rank = 3
		}
		if rank >= 0 {
			scored = append(scored, scoredRef{rank: rank, length: len(ref), ref: ref})
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
	if len(scored) > limit {
		scored = scored[:limit]
	}
	values := make([]string, 0, len(scored))
	for _, candidate := range scored {
		values = append(values, candidate.ref)
	}
	return values, total, nil
}

func anyRefPartStartsWith(ref, wanted string) bool {
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, wanted) {
			return true
		}
	}
	return false
}

// Read invokes the exact published Resource Tool through the same Binding.
func (publications *Publications) Read(ctx context.Context, uri string, selection contexture.RootSelection) (any, error) {
	if publications.runtime == nil {
		return nil, errors.New("A disclosure-only application has no Resources.")
	}
	for _, entry := range publications.resources {
		if entry.URI == uri {
			return publications.runtime.InvokeReadOnly(ctx, entry.Opens, json.RawMessage("{}"), selection)
		}
	}
	return nil, fmt.Errorf("No Contexture Resource at %q.", uri)
}

// Instructions describes selected model-controlled roles and their navigation door.
func (publications *Publications) Instructions(selection contexture.RootSelection) (string, error) {
	return instructions.Build(publications.disclosure, selection, instructions.RosterBudget)
}

func (publications *Publications) effective(selection contexture.RootSelection) (contexture.RootSelection, error) {
	return publications.disclosure.EffectiveSelection(selection)
}

func (publications *Publications) openForPerson(ref string, selection contexture.RootSelection) (string, error) {
	payload, err := publications.disclosure.OpenForPerson(ref, selection)
	if err != nil {
		return "", err
	}
	signposts, err := publications.signposts(ref, selection)
	if err != nil {
		return "", err
	}
	rendered, err := json.MarshalIndent(orderedContexture(payload), "", "  ")
	if err != nil {
		return "", err
	}
	parts := []string{fmt.Sprintf("You are at %s, opened by name at a person's request.", ref)}
	if signposts != "" {
		parts = append(parts, signposts)
	}
	parts = append(parts, string(rendered), "Continue with contexture_open, contexture_invoke_read_only or contexture_invoke, using refs taken from what is above. Nothing listed here was reached by navigating, so nothing beside it has been shown to you.")
	return strings.Join(parts, "\n\n"), nil
}

type orderedContexture map[string]any

func (value orderedContexture) MarshalJSON() ([]byte, error) {
	keys := []string{"kind", "name", "description", "ref"}
	switch value["kind"] {
	case "role":
		keys = append(keys, "instructions", "roles", "skills", "tools")
	case "skill":
		keys = append(keys, "instructions", "uses")
	case "tool":
		keys = append(keys, "read_only", "input_schema")
	}
	return marshalOrderedMap(map[string]any(value), keys, func(key string, item any) any {
		if key == "input_schema" {
			if schema, ok := item.(map[string]any); ok {
				return orderedSchema(schema)
			}
		}
		return orderedContextureValue(item)
	})
}

type orderedSchema map[string]any

func (value orderedSchema) MarshalJSON() ([]byte, error) {
	return marshalOrderedMap(map[string]any(value), []string{"properties", "required", "type", "default", "anyOf", "items", "additionalProperties"}, func(key string, item any) any {
		if key == "properties" {
			if properties, ok := item.(map[string]any); ok {
				orderedNames := []string{}
				if required, ok := value["required"].([]any); ok {
					for _, name := range required {
						if text, ok := name.(string); ok {
							orderedNames = append(orderedNames, text)
						}
					}
				}
				return orderedProperties{values: properties, names: orderedNames}
			}
		}
		switch typed := item.(type) {
		case map[string]any:
			return orderedSchema(typed)
		case []any:
			result := make([]any, len(typed))
			for position, child := range typed {
				if mapped, ok := child.(map[string]any); ok {
					result[position] = orderedSchema(mapped)
				} else {
					result[position] = child
				}
			}
			return result
		default:
			return item
		}
	})
}

type orderedProperties struct {
	values map[string]any
	names  []string
}

func (value orderedProperties) MarshalJSON() ([]byte, error) {
	return marshalOrderedMap(value.values, value.names, func(_ string, item any) any {
		if schema, ok := item.(map[string]any); ok {
			return orderedSchema(schema)
		}
		return item
	})
}

func orderedContextureValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return orderedContexture(typed)
	case []map[string]any:
		result := make([]any, len(typed))
		for position, child := range typed {
			result[position] = orderedContexture(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for position, child := range typed {
			result[position] = orderedContextureValue(child)
		}
		return result
	default:
		return value
	}
}

func marshalOrderedMap(values map[string]any, preferred []string, transform func(string, any) any) ([]byte, error) {
	ordered := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, key := range preferred {
		if _, exists := values[key]; exists {
			ordered = append(ordered, key)
			seen[key] = true
		}
	}
	remaining := []string{}
	for key := range values {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)
	buffer := strings.Builder{}
	buffer.WriteByte('{')
	for position, key := range ordered {
		if position > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := json.Marshal(transform(key, values[key]))
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return []byte(buffer.String()), nil
}

func (publications *Publications) signposts(ref string, selection contexture.RootSelection) (string, error) {
	parts := strings.Split(ref, "/")
	lines := []string{}
	for depth := 1; depth < len(parts); depth++ {
		ancestor := strings.Join(parts[:depth], "/")
		payload, err := publications.disclosure.OpenForPerson(ancestor, selection)
		if err != nil {
			return "", err
		}
		roles, _ := payload["roles"].([]map[string]any)
		if len(roles) > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %d sub-role(s) here; contexture_open to see them.", ancestor, len(roles)))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: no sub-roles; contexture_open to see what it holds.", ancestor))
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return "Signposts for the path above it. These are **not disclosed**: you may open one with contexture_open, and until you do you know only that it exists. Do not assert anything about what any of them holds.\n" + strings.Join(lines, "\n"), nil
}

func (publications *Publications) validate() error {
	promptNames := map[string]bool{}
	for _, entry := range publications.prompts {
		if strings.TrimSpace(entry.Opens) == "" || strings.TrimSpace(entry.Description) == "" {
			return errors.New("A Contexture Prompt must name a node and have a description.")
		}
		if _, err := publications.disclosure.Index().Find(entry.Opens); err != nil {
			return err
		}
		name := publicationName(entry.Name, entry.Opens)
		if promptNames[name] {
			return fmt.Errorf("Contexture Prompt %q is declared more than once.", name)
		}
		promptNames[name] = true
	}
	resourceNames, resourceURIs := map[string]bool{}, map[string]bool{}
	for _, entry := range publications.resources {
		if publications.runtime == nil {
			return errors.New("A disclosure-only application cannot declare Resources.")
		}
		if strings.TrimSpace(entry.Opens) == "" || strings.TrimSpace(entry.URI) == "" || strings.TrimSpace(entry.Description) == "" {
			return errors.New("A Contexture Resource requires a Tool ref, URI, and description.")
		}
		node, err := publications.disclosure.Index().Find(entry.Opens)
		if err != nil {
			return err
		}
		tool, ok := node.(*contexture.Tool)
		if !ok {
			return fmt.Errorf("Resource %q must target a Tool.", entry.URI)
		}
		if !tool.ReadOnly {
			return fmt.Errorf("Resource %q must target a read-only Tool.", entry.URI)
		}
		binding, err := tool.Binding()
		if err != nil {
			return fmt.Errorf("Resource %q must target an executable Tool: %w", entry.URI, err)
		}
		properties, _ := binding.Schema()["properties"].(map[string]any)
		if len(properties) != 0 {
			return fmt.Errorf("Resource %q must target an argument-free Tool.", entry.URI)
		}
		name := publicationName(entry.Name, entry.Opens)
		if resourceNames[name] || resourceURIs[entry.URI] {
			return fmt.Errorf("Contexture Resource %q is declared more than once.", name)
		}
		resourceNames[name], resourceURIs[entry.URI] = true, true
	}
	return nil
}

func publicationName(name, ref string) string {
	if name != "" {
		return name
	}
	return ref[strings.LastIndex(ref, "/")+1:]
}
