// Package messages owns server text intended for a person or a Host.
//
// Kernel refusals and fixed gateway descriptions belong to core/model. This
// package instead owns person-controlled Prompt wording, completion notices,
// and command signposts so publications and transports cannot drift apart.
package messages

import (
	"fmt"
	"strings"
)

const (
	// GotoPrompt is the one person-controlled command every Contexture server publishes.
	GotoPrompt = "goto"
	// GotoArgument is the sole argument accepted by GotoPrompt.
	GotoArgument = "ref"
	// CompletionLimit is the maximum number of values in one completion response.
	CompletionLimit = 100

	// Preamble teaches the fixed gateway before the capability roster.
	Preamble = `Everything this server offers is behind contexture_open. Start from the list
below: open the role that fits the task to see its skills, tools and
sub-roles, then open the skill you chose for its procedure. Each call
reveals one level; keep opening down the branch that fits.
Run a tool with contexture_invoke_read_only or contexture_invoke, whichever its
card says, passing the ref and arguments from that card.
Collect evidence before stating a cause; never assert system state you have
not read.`
	// RefRule requires consumers to reuse canonical refs from cards.
	RefRule = "Every card carries a `ref`. Pass it back to contexture_open to open that node; never assemble a ref yourself."

	// GotoDescription is the human-facing description of the universal goto command.
	GotoDescription = "Open any capability this server holds, by reference. The reference completes as you type, so the whole tree can be browsed here without asking the agent to go and look."
	// GotoArgumentDescription describes the reference accepted by goto.
	GotoArgumentDescription = "A reference such as payments/ledger/settlement. Completes on any part of the path."

	// CommandPreamble opens a node reached by a person rather than model navigation.
	CommandPreamble = "You are at {ref}, opened by name at a person's request."
	// SignpostPreamble says that ancestors are not yet disclosed.
	SignpostPreamble = "Signposts for the path above it. These are **not disclosed**: you may open one with contexture_open, and until you do you know only that it exists. Do not assert anything about what any of them holds."
	// CommandClosing returns a person to the regular model-controlled surface.
	CommandClosing = "Continue with contexture_open, contexture_invoke_read_only or contexture_invoke, using refs taken from what is above. Nothing listed here was reached by navigating, so nothing beside it has been shown to you."
)

// SignpostLevel describes one ancestor without disclosing its children.
type SignpostLevel struct {
	Ref          string
	SubRoleCount int
}

// TruncatedCompletion tells a person that matching refs were omitted.
func TruncatedCompletion(shown, total int) string {
	return fmt.Sprintf("... %d more match; keep typing to narrow.", total-shown)
}

// Signpost renders ancestors without revealing their contents.
func Signpost(levels []SignpostLevel) string {
	if len(levels) == 0 {
		return ""
	}
	lines := make([]string, 0, len(levels)+1)
	lines = append(lines, SignpostPreamble)
	for _, level := range levels {
		if level.SubRoleCount > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %d sub-role(s) here; contexture_open to see them.", level.Ref, level.SubRoleCount))
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: no sub-roles; contexture_open to see what it holds.", level.Ref))
	}
	return strings.Join(lines, "\n")
}

// CommandDescription renders a declared Prompt in a person's menu.
func CommandDescription(ref, description string) string {
	return fmt.Sprintf("%s (%s)", description, ref)
}
