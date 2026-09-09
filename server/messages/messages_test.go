package messages_test

import (
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/server/messages"
)

func TestHumanFacingMessageContract(t *testing.T) {
	if messages.GotoPrompt != "goto" || messages.GotoArgument != "ref" || messages.CompletionLimit != 100 {
		t.Fatalf("goto contract = %q, %q, %d", messages.GotoPrompt, messages.GotoArgument, messages.CompletionLimit)
	}
	if got, want := messages.CommandDescription("operations/status", "Read status."), "Read status. (operations/status)"; got != want {
		t.Fatalf("CommandDescription = %q, want %q", got, want)
	}
	if got, want := messages.TruncatedCompletion(100, 103), "... 3 more match; keep typing to narrow."; got != want {
		t.Fatalf("TruncatedCompletion = %q, want %q", got, want)
	}
	if messages.GotoDescription != "Open any capability this server holds, by reference. The reference completes as you type, so the whole tree can be browsed here without asking the agent to go and look." {
		t.Fatal("GotoDescription drifted")
	}
	if messages.GotoArgumentDescription != "A reference such as payments/ledger/settlement. Completes on any part of the path." {
		t.Fatal("GotoArgumentDescription drifted")
	}
	if messages.CommandPreamble != "You are at {ref}, opened by name at a person's request." || messages.CommandClosing == "" {
		t.Fatal("command framing drifted")
	}
	if !strings.Contains(messages.Preamble, "contexture_open") || !strings.Contains(messages.RefRule, "never assemble a ref yourself") {
		t.Fatal("bootstrap message contract drifted")
	}
}

func TestSignpostNeverDisclosesAncestorContents(t *testing.T) {
	got := messages.Signpost([]messages.SignpostLevel{
		{Ref: "operations", SubRoleCount: 2},
		{Ref: "operations/status", SubRoleCount: 0},
	})
	want := messages.SignpostPreamble + "\n" +
		"- operations: 2 sub-role(s) here; contexture_open to see them.\n" +
		"- operations/status: no sub-roles; contexture_open to see what it holds."
	if got != want {
		t.Fatalf("Signpost = %q, want %q", got, want)
	}
	if messages.Signpost(nil) != "" {
		t.Fatal("empty Signpost must be empty")
	}
}
