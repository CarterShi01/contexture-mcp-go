package server

import (
	"context"
	"errors"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

func TestGraphOwnerJournalReceiptMatchesResultRevisionAndRejectsWrongRevision(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	preview, err := f.owner.PreviewBootstrap(f.context, f.bootstrap(), graphPreviewExpiry)
	if err != nil {
		t.Fatal(err)
	}
	digest := releaseText(journalMap(preview.Receipt().JSON())["request_digest"])
	unseeded, _ := graph.NewUnseededState("graph")
	event, err := graph.BootstrapEvent(unseeded, f.definition, graph.EventOptions{CommandDigest: digest, Reason: "native seed"}, "approved")
	if err != nil {
		t.Fatal(err)
	}
	invalid := JournalObject{"schema": "contexture.command-receipt/v1", "graph_id": "graph", "actor_ref": "tenant-actor", "idempotency_key": "bad",
		"request_digest": digest, "revision": "0", "journal_seq": "1", "status": "committed", "durability": f.journal.durability,
		"operation_result": JournalObject{"inline": JournalObject{"graph_id": "graph"}}}
	if _, err := f.journal.Append([]any{event.JSON()}, JournalAppendOptions{ActorRef: "tenant-actor", CommandReceipt: invalid}); err == nil {
		t.Fatal("Journal guard was weakened instead of matching result_revision")
	}
	if watermark, _ := f.journal.Watermark(); watermark != "0" {
		t.Fatal("Invalid receipt was physically published")
	}
	if _, err := f.owner.Commit(f.context, preview, GraphCommitOptions{}); err != nil {
		t.Fatal("Existing schema-defined result_revision receipt did not publish", err)
	}
}
func TestGraphOwnerRequiresEveryTrustedPortAndClearsDeniedResults(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	for _, options := range []GraphOwnerOptions{
		{Host: f, Serialization: f.serialization},
		{Host: f, Clock: f.clock},
		{Serialization: f.serialization, Clock: f.clock},
	} {
		if _, err := NewFileJournalGraphOwner(f.journal, options); err == nil {
			t.Fatal("Permissive Host/serialization/clock fallback accepted")
		}
	}
	f.boot()
	f.mu.Lock()
	f.beforeDecision = func(admission GraphAdmission) {
		if admission.Operation == "graph.read" {
			f.mu.Lock()
			f.version = "moved"
			f.mu.Unlock()
		}
	}
	f.mu.Unlock()
	result, err := f.owner.Read(f.context, "graph", "")
	if err == nil || result.Revision() != "" {
		t.Fatal("Denied current-authority read leaked a usable snapshot", result.Revision(), err)
	}
	cancelled, cancel := context.WithCancel(f.context)
	cancel()
	if _, err := f.owner.Read(cancelled, "graph", ""); !errors.Is(err, context.Canceled) {
		t.Fatal("Explicit context cancellation was ignored", err)
	}
	if _, err := f.owner.Read(context.Background(), "graph", ""); !errors.Is(err, managed.ErrDenied) {
		t.Fatal("Missing explicit context identity fell back implicitly", err)
	}
}
