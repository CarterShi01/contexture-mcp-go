package contexture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	disclosure *Disclosure
	runtime    *Runtime
	prompts    []PromptDeclaration
	resources  []ResourceDeclaration
}

// NewPublications validates application publications over one compiled surface.
func NewPublications(application *Application, disclosure *Disclosure, runtime *Runtime) (*Publications, error) {
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
func (publications *Publications) PromptCards(selection RootSelection) ([]PromptCard, error) {
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
func (publications *Publications) ResourceCards() []ResourceCard {
	result := make([]ResourceCard, 0, len(publications.resources))
	for _, entry := range publications.resources {
		result = append(result, ResourceCard{Name: publicationName(entry.Name, entry.Opens), URI: entry.URI, Description: entry.Description, MIMEType: entry.MIMEType})
	}
	return result
}

// Command opens the node reserved by a named person-controlled Prompt.
func (publications *Publications) Command(name string, selection RootSelection) (string, error) {
	for _, entry := range publications.prompts {
		if publicationName(entry.Name, entry.Opens) == name {
			return publications.openForPerson(entry.Opens, selection)
		}
	}
	return "", fmt.Errorf("No Contexture Prompt named %q.", name)
}

// Goto opens a person-supplied ref through person-controlled navigation.
func (publications *Publications) Goto(ref string, selection RootSelection) (string, error) {
	return publications.openForPerson(ref, selection)
}

// Complete returns selected canonical refs beginning with input, capped by limit.
func (publications *Publications) Complete(input string, selection RootSelection, limit int) ([]string, int, error) {
	effective, err := publications.effective(selection)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	values := []string{}
	for _, ref := range publications.disclosure.index.Walk() {
		if effective.ContainsRef(ref) && strings.HasPrefix(ref, input) {
			values = append(values, ref)
		}
	}
	total := len(values)
	if len(values) > limit {
		values = values[:limit]
	}
	return values, total, nil
}

// Read invokes the exact published Resource Tool through the same Binding.
func (publications *Publications) Read(ctx context.Context, uri string, selection RootSelection) (any, error) {
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
func (publications *Publications) Instructions(selection RootSelection) (string, error) {
	effective, err := publications.effective(selection)
	if err != nil {
		return "", err
	}
	lines := []string{
		"Everything this server offers is behind contexture_open. Start from the list",
		"below: open the role that fits the task to see its skills, tools and",
		"sub-roles, then open the skill you chose for its procedure. Each call",
		"reveals one level; keep opening down the branch that fits.",
		"Run a tool with contexture_invoke_read_only or contexture_invoke, whichever its",
		"card says, passing the ref and arguments from that card.",
		"Collect evidence before stating a cause; never assert system state you have",
		"not read.", "", "Capabilities:",
	}
	queue := publications.disclosure.index.ModelRoots()
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		ref, _ := publications.disclosure.index.RefOf(node)
		if !effective.ContainsRef(ref) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", ref, node.nodeDescription()))
		if role, ok := node.(*Role); ok {
			children, childErr := publications.disclosure.index.ChildrenOf(role)
			if childErr != nil {
				return "", childErr
			}
			for _, child := range children {
				if child.nodeKind() == RoleKind {
					queue = append(queue, child)
				}
			}
		}
	}
	lines = append(lines, "", "Every card carries a `ref`. Pass it back to contexture_open to open that node; never assemble a ref yourself.")
	return strings.Join(lines, "\n"), nil
}

func (publications *Publications) effective(selection RootSelection) (RootSelection, error) {
	return publications.disclosure.selection.Intersect(selection)
}

func (publications *Publications) openForPerson(ref string, selection RootSelection) (string, error) {
	payload, err := publications.disclosure.OpenForPerson(ref, selection)
	if err != nil {
		return "", err
	}
	signposts, err := publications.signposts(ref, selection)
	if err != nil {
		return "", err
	}
	rendered, err := json.MarshalIndent(payload, "", "  ")
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

func (publications *Publications) signposts(ref string, selection RootSelection) (string, error) {
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
		if _, err := publications.disclosure.index.Find(entry.Opens); err != nil {
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
		node, err := publications.disclosure.index.Find(entry.Opens)
		if err != nil {
			return err
		}
		tool, ok := node.(*Tool)
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
