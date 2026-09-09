// Package instructions builds the server bootstrap text that arrives before
// any MCP call. It is separate from the publication surface so host budgets
// and person-controlled Prompts cannot change one another's responsibilities.
package instructions

import (
	"fmt"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/messages"
)

const (
	// InstructionsLimit is Claude Code's server-instructions byte limit.
	InstructionsLimit = 2048
	// RosterBudget leaves room for fixed gateway descriptions in that limit.
	RosterBudget = 1200
	// SelfContainedPrefix is the initial portion Codex reads while deciding.
	SelfContainedPrefix = 512
)

// Neutral returns bootstrap text for a request-selected server without a roster.
func Neutral() string {
	return "This Contexture server exposes a request-specific set of complete root capabilities. Call contexture_discover for the roots available to this request, open the one that fits the task, and continue one level at a time using refs exactly as returned. Run a disclosed tool through the read-only or writing Contexture invoke door named on its card."
}

// Build returns contract-first bootstrap instructions and a breadth-first roster.
func Build(disclosure *contexture.Disclosure, requested contexture.RootSelection, budget int) (string, error) {
	if disclosure == nil {
		return "", fmt.Errorf("Contexture Disclosure must not be nil")
	}
	if budget < 0 {
		return "", fmt.Errorf("Contexture instruction roster budget must not be negative")
	}
	effective, err := disclosure.EffectiveSelection(requested)
	if err != nil {
		return "", err
	}
	roster, spent, dropped, full := []string{}, 0, 0, false
	for index, group := range siblingGroups(disclosure, effective) {
		entries := make([]string, 0, len(group))
		cost := 0
		for _, node := range group {
			ref, _ := disclosure.Index().RefOf(node)
			entry := fmt.Sprintf("- %s: %s", ref, node.NodeDescription())
			entries = append(entries, entry)
			cost += len(entry) + 1
		}
		if index == 0 {
			for _, entry := range entries {
				if spent+len(entry) > budget {
					dropped++
					continue
				}
				roster = append(roster, entry)
				spent += len(entry) + 1
			}
			if dropped > 0 {
				roster = append(roster, fmt.Sprintf("- ...and %d more root role(s); call contexture_discover for the complete list.", dropped))
				return assemble(roster), nil
			}
			continue
		}
		if full || spent+cost > budget {
			full = true
			dropped += len(entries)
			continue
		}
		roster = append(roster, entries...)
		spent += cost
	}
	if dropped > 0 {
		roster = append(roster, fmt.Sprintf("- ...and %d more role(s) below these; open one of the roles above to see what it holds.", dropped))
	}
	return assemble(roster), nil
}

func assemble(roster []string) string {
	return strings.Join(append([]string{messages.Preamble, "", "Capabilities:"}, append(roster, "", messages.RefRule)...), "\n")
}

func siblingGroups(disclosure *contexture.Disclosure, selection contexture.RootSelection) [][]contexture.Node {
	visible := func(node contexture.Node) bool {
		ref, _ := disclosure.Index().RefOf(node)
		return selection.ContainsRef(ref)
	}
	roots := []contexture.Node{}
	for _, node := range disclosure.Index().ModelRoots() {
		if visible(node) {
			roots = append(roots, node)
		}
	}
	groups := [][]contexture.Node{roots}
	queue := []*contexture.Role{}
	for _, node := range roots {
		if role, ok := node.(*contexture.Role); ok {
			queue = append(queue, role)
		}
	}
	for len(queue) > 0 {
		role := queue[0]
		queue = queue[1:]
		children, _ := disclosure.Index().ChildrenOf(role)
		roles := []contexture.Node{}
		for _, child := range children {
			if child.Kind() != contexture.RoleKind || !visible(child) {
				continue
			}
			roles = append(roles, child)
			queue = append(queue, child.(*contexture.Role))
		}
		if len(roles) > 0 {
			groups = append(groups, roles)
		}
	}
	return groups
}
