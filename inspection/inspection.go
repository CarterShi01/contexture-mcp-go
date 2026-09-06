// Package inspection replays Contexture disclosure without starting a Host
// transport or invoking a writing Tool.
package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

// Connect is the pseudo-call delivered when a Host connects.
const Connect = "session start"

// Cost is the measured character, UTF-8 byte, and approximate token cost.
type Cost struct {
	Characters int `json:"characters"`
	Bytes      int `json:"bytes"`
	Tokens     int `json:"estimated_tokens"`
}

// CostOf measures a text value the same way the inspection renderer does.
func CostOf(text string) Cost {
	wide := 0
	for _, character := range text {
		if (character >= 0x1100 && character <= 0x11FF) || (character >= 0x2E80 && character <= 0x9FFF) || (character >= 0xA960 && character <= 0xA97F) || (character >= 0xAC00 && character <= 0xD7FF) || (character >= 0xF900 && character <= 0xFAFF) || (character >= 0xFF00 && character <= 0xFF60) || (character >= 0x20000 && character <= 0x3FFFF) {
			wide++
		}
	}
	characters := utf8.RuneCountInString(text)
	return Cost{Characters: characters, Bytes: len([]byte(text)), Tokens: int(float64(wide) + float64(characters-wide)/4 + 0.5)}
}

// Add combines two disclosure costs.
func (cost Cost) Add(other Cost) Cost {
	return Cost{Characters: cost.Characters + other.Characters, Bytes: cost.Bytes + other.Bytes, Tokens: cost.Tokens + other.Tokens}
}

// Check is one host-limit or disclosure-quality observation.
type Check struct {
	OK   bool   `json:"ok"`
	Note string `json:"note"`
}

// Step is one thing an agent receives while connecting or navigating.
type Step struct {
	Call    string  `json:"call"`
	Ref     string  `json:"ref"`
	Refused bool    `json:"refused"`
	Body    string  `json:"body"`
	Payload any     `json:"payload"`
	Checks  []Check `json:"checks"`
	Cost    Cost    `json:"cost"`
	Aside   string  `json:"aside"`
}

func step(call, body string, options Step) Step {
	options.Call, options.Body, options.Cost = call, body, CostOf(body)
	if options.Checks == nil {
		options.Checks = []Check{}
	}
	return options
}

// MarshalJSON preserves the reference API's explicit nulls for absent fields.
// Empty refs are not valid user-facing addresses, so they are the Go-native
// representation of an omitted optional value in Step.
func (item Step) MarshalJSON() ([]byte, error) {
	type encodedStep struct {
		Call    string  `json:"call"`
		Ref     *string `json:"ref"`
		Refused bool    `json:"refused"`
		Body    string  `json:"body"`
		Payload any     `json:"payload"`
		Checks  []Check `json:"checks"`
		Cost    Cost    `json:"cost"`
		Aside   *string `json:"aside"`
	}
	var ref, aside *string
	if item.Ref != "" {
		ref = &item.Ref
	}
	if item.Aside != "" {
		aside = &item.Aside
	}
	return json.Marshal(encodedStep{
		Call: item.Call, Ref: ref, Refused: item.Refused, Body: item.Body,
		Payload: item.Payload, Checks: item.Checks, Cost: item.Cost, Aside: aside,
	})
}

// Trace is an ordered transport-free disclosure replay.
type Trace struct {
	Steps []Step `json:"steps"`
	Total Cost   `json:"total"`
}

// Failures returns refused steps and failed checks.
func (trace Trace) Failures() []Step {
	result := []Step{}
	for _, item := range trace.Steps {
		failed := item.Refused
		for _, check := range item.Checks {
			failed = failed || !check.OK
		}
		if failed {
			result = append(result, item)
		}
	}
	return result
}

// ConnectStep measures bootstrap instructions exactly as a Host receives them.
func ConnectStep(disclosure *contexture.Disclosure, text string) Step {
	cost := CostOf(text)
	roles := modelRoleCount(disclosure)
	listed, cut := rosterLines(text)
	checks := []Check{
		{OK: cost.Bytes <= instructions.InstructionsLimit, Note: fmt.Sprintf("%d of %d bytes — Claude Code truncates what is over, mid-sentence", cost.Bytes, instructions.InstructionsLimit)},
		{OK: strings.Contains(string([]rune(text)[:min(utf8.RuneCountInString(text), instructions.SelfContainedPrefix)]), "contexture_open"), Note: fmt.Sprintf("contexture_open named in the first %d characters — that is how far Codex reads while deciding whether to use this server", instructions.SelfContainedPrefix)},
		{OK: !cut && listed == roles, Note: fmt.Sprintf("roster lists %d of %d role(s)%s", listed, roles, map[bool]string{true: " — the rest were cut for budget", false: ""}[cut])},
	}
	gateway := gatewayFacts(disclosure)
	payload := map[string]any{"instructions": text, "gateway": gateway}
	return step(Connect, text, Step{Payload: payload, Checks: checks, Aside: fmt.Sprintf("the %d gateway tool descriptions arrive here too, costing %d more estimated tokens; they are fixed, so they are not counted below", len(gateway), gatewayCost(gateway).Tokens)})
}

// DiscoverStep replays the fixed model discovery response.
func DiscoverStep(disclosure *contexture.Disclosure) Step {
	payload, err := disclosure.Discover(contexture.AllRoots())
	if err != nil {
		return step("contexture_discover", err.Error(), Step{Refused: true})
	}
	return step("contexture_discover", wire(payload), Step{Payload: payload})
}

// OpenStep replays one model-controlled open response or its recovery sentence.
func OpenStep(disclosure *contexture.Disclosure, ref string) Step {
	payload, err := disclosure.Open(ref, contexture.AllRoots())
	if err != nil {
		var failure *contexture.NodeNotFoundError
		if errors.As(err, &failure) {
			err = &contexture.RefusedError{Message: contexture.UnresolvedMessage(failure), Cause: err}
		}
		return step("contexture_open", err.Error(), Step{Ref: ref, Refused: true, Aside: "this recovery sentence is all the agent receives"})
	}
	result := Step{Ref: ref, Payload: payload, Checks: routingChecks(payload)}
	if contentTool(disclosure, ref) {
		result.Aside = "the document itself is not here — an agent runs it with contexture_invoke_read_only; pass --read to include it and its cost"
	}
	return step("contexture_open", wire(payload), result)
}

// ReadStep invokes a no-argument read-only Tool only when inspection requests it.
func ReadStep(ctx context.Context, runtime *contexture.Runtime, ref string) Step {
	if runtime == nil {
		return step("contexture_invoke_read_only", "inspection needs an execution Runtime to read content", Step{Ref: ref, Refused: true})
	}
	value, err := runtime.InvokeReadOnly(ctx, ref, json.RawMessage("{}"), contexture.AllRoots())
	if err != nil {
		return step("contexture_invoke_read_only", err.Error(), Step{Ref: ref, Refused: true})
	}
	if bytes, ok := value.([]byte); ok {
		return step("contexture_invoke_read_only", fmt.Sprintf("<%d bytes of binary>", len(bytes)), Step{Ref: ref, Aside: "binary content is described rather than printed"})
	}
	if text, ok := value.(string); ok {
		return step("contexture_invoke_read_only", text, Step{Ref: ref})
	}
	return step("contexture_invoke_read_only", wire(value), Step{Ref: ref})
}

// EveryRef walks visible roles breadth-first and each role's leaf members once.
func EveryRef(disclosure *contexture.Disclosure) []string {
	if disclosure == nil {
		return []string{}
	}
	selection, err := disclosure.EffectiveSelection(contexture.AllRoots())
	if err != nil {
		return []string{}
	}
	result, queue := []string{}, []*contexture.Role{}
	for _, node := range disclosure.Index().ModelRoots() {
		ref, _ := disclosure.Index().RefOf(node)
		if !selection.ContainsRef(ref) {
			continue
		}
		if node.Kind() == contexture.RoleKind {
			queue = append(queue, node.(*contexture.Role))
		} else {
			result = append(result, ref)
		}
	}
	for len(queue) > 0 {
		role := queue[0]
		queue = queue[1:]
		ref, _ := disclosure.Index().RefOf(role)
		result = append(result, ref)
		children, _ := disclosure.Index().ChildrenOf(role)
		for _, child := range children {
			childRef, _ := disclosure.Index().RefOf(child)
			if !selection.ContainsRef(childRef) {
				continue
			}
			if child.Kind() == contexture.RoleKind {
				queue = append(queue, child.(*contexture.Role))
			} else {
				result = append(result, childRef)
			}
		}
	}
	return result
}

// Replay produces connect, optional discovery, opens, and optional content reads.
func Replay(ctx context.Context, disclosure *contexture.Disclosure, runtime *contexture.Runtime, refs []string, includeDiscover, includeRead bool, text string) Trace {
	if text == "" {
		text, _ = instructions.Build(disclosure, contexture.AllRoots(), instructions.RosterBudget)
	}
	steps := []Step{ConnectStep(disclosure, text)}
	if includeDiscover {
		steps = append(steps, DiscoverStep(disclosure))
	}
	for _, ref := range refs {
		opened := OpenStep(disclosure, ref)
		steps = append(steps, opened)
		if includeRead && runtime != nil && contentTool(disclosure, ref) && !opened.Refused {
			steps = append(steps, ReadStep(ctx, runtime, ref))
		}
	}
	total := Cost{}
	for _, item := range steps {
		total = total.Add(item.Cost)
	}
	return Trace{Steps: steps, Total: total}
}

// isContent identifies an executable Resource-equivalent: a read-only Tool
// whose schema takes no arguments. Inspection must never guess arguments or
// call parameterized diagnostics merely because --read was requested.
func contentTool(disclosure *contexture.Disclosure, ref string) bool {
	if disclosure == nil {
		return false
	}
	node, err := disclosure.Index().Find(ref)
	tool, ok := node.(*contexture.Tool)
	if err != nil || !ok || !tool.ReadOnly {
		return false
	}
	binding, err := tool.Binding()
	if err != nil {
		return false
	}
	properties, _ := binding.Schema()["properties"].(map[string]any)
	return len(properties) == 0
}

// Render prints a trace without requiring callers to parse JSON.
func Render(trace Trace, payloads bool) string {
	lines := []string{}
	for index, item := range trace.Steps {
		heading := fmt.Sprintf("step %d  %s", index, item.Call)
		if item.Ref != "" {
			heading += "  " + item.Ref
		}
		if item.Refused {
			heading += "  [refused]"
		}
		lines = append(lines, heading, fmt.Sprintf("  %d characters, %d bytes, ~%d tokens", item.Cost.Characters, item.Cost.Bytes, item.Cost.Tokens))
		for _, check := range item.Checks {
			lines = append(lines, fmt.Sprintf("  %s%s", map[bool]string{true: "ok  ", false: "BAD "}[check.OK], check.Note))
		}
		if item.Aside != "" {
			lines = append(lines, "  note: "+item.Aside)
		}
		if payloads {
			for _, line := range strings.Split(item.Body, "\n") {
				lines = append(lines, "  | "+line)
			}
		}
		lines = append(lines, "")
	}
	return strings.Join(append(lines, renderSummary(trace)...), "\n")
}

// AsJSON renders a trace for scenario comparison.
func AsJSON(trace Trace) (string, error) {
	raw, err := json.MarshalIndent(trace, "", "  ")
	return string(raw), err
}

func modelRoleCount(disclosure *contexture.Disclosure) int {
	if disclosure == nil {
		return 0
	}
	selection, err := disclosure.EffectiveSelection(contexture.AllRoots())
	if err != nil {
		return 0
	}
	modelRoots := map[string]bool{}
	for _, root := range disclosure.Index().ModelRoots() {
		ref, _ := disclosure.Index().RefOf(root)
		modelRoots[strings.Split(ref, "/")[0]] = true
	}
	roles := 0
	for _, ref := range disclosure.Index().Walk() {
		node, err := disclosure.Index().Find(ref)
		if err == nil && node.Kind() == contexture.RoleKind && modelRoots[strings.Split(ref, "/")[0]] && selection.ContainsRef(ref) {
			roles++
		}
	}
	return roles
}

func gatewayFacts(disclosure *contexture.Disclosure) []map[string]any {
	gateway, err := contexture.NewGateway(disclosure, nil)
	if err != nil {
		return []map[string]any{}
	}
	facts := make([]map[string]any, 0, len(gateway.Tools()))
	for _, tool := range gateway.Tools() {
		facts = append(facts, map[string]any{
			"name":        string(tool.Name),
			"description": tool.Description,
			"read_only":   tool.ReadOnly,
		})
	}
	return facts
}

func gatewayCost(gateway []map[string]any) Cost {
	text := ""
	for _, tool := range gateway {
		text += fmt.Sprint(tool["name"]) + fmt.Sprint(tool["description"])
	}
	return CostOf(text)
}

func renderSummary(trace Trace) []string {
	width := 3
	for _, item := range trace.Steps {
		if len(item.Ref) > width {
			width = len(item.Ref)
		}
	}
	if width > 52 {
		width = 52
	}
	rule := strings.Repeat("-", 36+width)
	lines := []string{rule, fmt.Sprintf("%2s  %-26s  %-*s  ~tok", "#", "call", width, "ref")}
	running := Cost{}
	for index, item := range trace.Steps {
		running = running.Add(item.Cost)
		ref := item.Ref
		if ref == "" {
			ref = "-"
		}
		if len(ref) > width {
			ref = "…" + ref[len(ref)-width+1:]
		}
		lines = append(lines, fmt.Sprintf("%2d  %-26s  %-*s  %5d  (running %d)", index, item.Call, width, ref, item.Cost.Tokens, running.Tokens))
	}
	lines = append(lines, rule, fmt.Sprintf("total  %d characters, %d bytes, ~%d tokens over %d step(s)", trace.Total.Characters, trace.Total.Bytes, trace.Total.Tokens, len(trace.Steps)))
	refused, failed := 0, 0
	for _, item := range trace.Steps {
		if item.Refused {
			refused++
		}
		for _, check := range item.Checks {
			if !check.OK {
				failed++
			}
		}
	}
	if refused > 0 {
		lines = append(lines, fmt.Sprintf("       %d step(s) refused", refused))
	}
	if failed > 0 {
		lines = append(lines, fmt.Sprintf("       %d host limit(s) not met", failed))
	}
	return lines
}

func routingChecks(payload map[string]any) []Check {
	cards := []map[string]any{}
	for _, group := range []string{"roles", "skills", "tools"} {
		if entries, ok := payload[group].([]map[string]any); ok {
			cards = append(cards, entries...)
		}
	}
	if len(cards) == 0 {
		return []Check{}
	}
	held, long := map[string]bool{}, []string{}
	for _, card := range cards {
		name, description := fmt.Sprint(card["name"]), fmt.Sprint(card["description"])
		held[name] = true
		if utf8.RuneCountInString(description) > 200 {
			long = append(long, name)
		}
	}
	named := map[string]bool{}
	for _, card := range cards {
		for name := range held {
			if name != fmt.Sprint(card["name"]) && strings.Contains(fmt.Sprint(card["description"]), name) {
				named[fmt.Sprint(card["name"])] = true
			}
		}
	}
	description := fmt.Sprint(payload["description"])
	for name := range held {
		if strings.Contains(description, name) {
			named[fmt.Sprint(payload["name"])] = true
		}
	}
	listing := make([]string, 0, len(named))
	for name := range named {
		listing = append(listing, name)
	}
	sort.Strings(listing)
	return []Check{
		{OK: len(long) == 0, Note: conditionalNote(len(long) == 0, "every routing sentence is within 200 characters", "over 200 characters: "+strings.Join(long, ", "))},
		{OK: len(listing) == 0, Note: conditionalNote(len(listing) == 0, "no routing sentence names what its node holds", strings.Join(listing, ", ")+" name(s) their own members — the inside is what opening delivers, and describing it twice is how the two copies start disagreeing")},
	}
}

func conditionalNote(ok bool, accepted, rejected string) string {
	if ok {
		return accepted
	}
	return rejected
}
func rosterLines(text string) (int, bool) {
	listed, cut := 0, false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- ...and ") {
			cut = true
		} else if strings.HasPrefix(line, "- ") {
			listed++
		}
	}
	return listed, cut
}
func wire(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}
func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
