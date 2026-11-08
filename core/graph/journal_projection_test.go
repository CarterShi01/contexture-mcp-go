package graph

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestGraphProjectionExtensionDoesNotEagerlyBuildFullCheckpoint(t *testing.T) {
	_, seed := projectionSeed(t, "graph")
	first := projectionRecord(1, nil, []any{seed.JSON()}, nil)
	original, err := NewGraphJournalProjection([]map[string]any{first}, GraphJournalProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current := original
	previous := first["record_hash"]
	for sequence := 2; sequence <= 1001; sequence++ {
		event := map[string]any{"type": "CommandCompleted", "graph_id": "graph", "revision": "1",
			"command_digest": evOptions().CommandDigest, "status": "noop"}
		receipt := map[string]any{"schema": "contexture.command-receipt/v1", "graph_id": "graph",
			"actor_ref": "tenant-actor", "idempotency_key": fmt.Sprintf("noop-%d", sequence),
			"request_digest": evOptions().CommandDigest, "revision": "1", "journal_seq": fmt.Sprint(sequence),
			"status": "noop", "durability": "strict", "operation_result": map[string]any{"inline": nil}}
		record := projectionRecord(sequence, previous, []any{event}, receipt)
		current, err = current.Extend([]map[string]any{record})
		if err != nil {
			t.Fatal(err)
		}
		if current.checkpoint.value != nil {
			t.Fatal("Extension eagerly materialized all historical receipts")
		}
		previous = record["record_hash"]
	}
	if original.Watermark() != "1" || original.receipts.Len() != 0 || original.checkpoint.value != nil {
		t.Fatal("Immutable original cut changed or eagerly serialized")
	}
	var group sync.WaitGroup
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			if len(arr(jsonObject(current.Checkpoint().JSON())["receipts_index"])) != 1000 {
				t.Error("Lazy checkpoint lost exact receipt index")
			}
		}()
	}
	group.Wait()
	if current.Watermark() != "1001" || len(current.Records()) != 1001 {
		t.Fatal("Accepted records were lost")
	}
}

func projectionRecord(seq int, previous any, events []any, receipt any) map[string]any {
	record := map[string]any{"schema": "contexture.journal-record/v1", "store_id": "store", "journal_seq": fmt.Sprint(seq),
		"event_batch_id": fmt.Sprintf("batch-%d", seq), "prev_record_hash": previous, "events": events,
		"written_at": "2026-11-07T04:00:00.000001Z", "actor_ref": "tenant-actor", "correlation_id": fmt.Sprintf("correlation-%d", seq)}
	payload := map[string]any{"events": events}
	if receipt != nil {
		record["command_receipt"], payload["command_receipt"] = receipt, receipt
	}
	record["payload_digest"] = pureDigest(payload, "contexture.journal-payload/v1")
	record["record_hash"] = pureDigest(record, "contexture.journal-record/v1")
	return record
}
func projectionSeed(t *testing.T, graphID string) (*GraphDefinition, FrozenJSON) {
	t.Helper()
	raw := candidateGraph(candidateNode("root", "role"))
	raw["graph_id"] = graphID
	raw["nodes"].([]any)[0].(map[string]any)["mutable"] = true
	definition := mustDefinition(t, raw, nil)
	unseeded, _ := NewUnseededState(graphID)
	event, err := BootstrapEvent(unseeded, definition, evOptions(), "approved")
	if err != nil {
		t.Fatal(err)
	}
	return definition, event
}
func TestGraphJournalProjectionCompletePureCutAndIsolation(t *testing.T) {
	definition, seed := projectionSeed(t, "graph")
	record := projectionRecord(1, nil, []any{seed.JSON()}, nil)
	projection, err := NewGraphJournalProjection([]map[string]any{record}, GraphJournalProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if projection.Watermark() != "1" || projection.StoreID() != "store" || len(projection.GraphIDs()) != 1 {
		t.Fatal(projection)
	}
	checkpoint := projection.Checkpoint().JSON().(map[string]any)
	if len(checkpoint) != 8 || len(arr(checkpoint["graphs"])) != 1 {
		t.Fatal(checkpoint)
	}
	history, err := projection.History("graph")
	if err != nil {
		t.Fatal(err)
	}
	head := history.Head().Definition()
	a, _ := head.Digest()
	b, _ := definition.Digest()
	if a != b {
		t.Fatal("Projection invented a graph head")
	}
	mutateJSON(record)
	for _, copied := range projection.Records() {
		mutateJSON(copied)
	}
	mutateJSON(projection.Checkpoint().JSON())
	again, err := NewGraphJournalProjection(projection.Records(), GraphJournalProjectionOptions{})
	if err != nil || !jsonEqual(again.Checkpoint().JSON(), projection.Checkpoint().JSON()) {
		t.Fatal("Projection aliases escaped", err)
	}
}
func TestGraphJournalProjectionExtensionIsExactAndDoesNotPublishBadSuffix(t *testing.T) {
	_, seed := projectionSeed(t, "graph")
	first := projectionRecord(1, nil, []any{seed.JSON()}, nil)
	prefix, err := NewGraphJournalProjection([]map[string]any{first}, GraphJournalProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original := prefix.Checkpoint()
	event := map[string]any{"type": "RecoveryActionRecorded", "action_id": "verify",
		"action": "verify", "evidence": map[string]any{"digest": "sha256:" + strings.Repeat("a", 64),
			"size": "3", "media_type": "text/plain"}}
	second := projectionRecord(2, first["record_hash"], []any{event}, nil)
	extended, err := prefix.Extend([]map[string]any{second})
	if err != nil {
		t.Fatal(err)
	}
	full, err := NewGraphJournalProjection([]map[string]any{first, second}, GraphJournalProjectionOptions{})
	if err != nil || !jsonEqual(extended.Checkpoint().JSON(), full.Checkpoint().JSON()) {
		t.Fatal("Incremental and full replay differ", err)
	}
	for _, suffix := range [][]map[string]any{{first}, {projectionRecord(3, second["record_hash"], []any{event}, nil)}} {
		if _, err := prefix.Extend(suffix); err == nil {
			t.Fatal("Invalid prefix/suffix boundary accepted")
		}
		if !jsonEqual(prefix.Checkpoint().JSON(), original.JSON()) || prefix.Watermark() != "1" {
			t.Fatal("Failed extension changed its accepted prefix")
		}
	}
	if _, err := (&GraphJournalProjection{}).Extend([]map[string]any{first}); err == nil {
		t.Fatal("Unverified projection used as a prefix")
	}
}

func TestGraphJournalProjectionOriginHashAndEventBatchProvenance(t *testing.T) {
	_, seed := projectionSeed(t, "graph")
	first := projectionRecord(1, nil, []any{seed.JSON()}, nil)
	for _, mode := range []string{"hash", "sequence", "origin", "batch", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			second := projectionRecord(2, first["record_hash"], []any{map[string]any{"type": "CommandCompleted", "graph_id": "graph", "revision": "1",
				"command_digest": "sha256:" + strings.Repeat("a", 64), "status": "noop"}}, nil)
			switch mode {
			case "hash":
				second["actor_ref"] = "tampered"
			case "sequence":
				second["journal_seq"] = "3"
			case "origin":
				second["store_id"] = "another-store"
			case "batch":
				second["event_batch_id"] = first["event_batch_id"]
			case "unknown":
				second["events"] = []any{map[string]any{"type": "InventedRequiredEvent"}}
			}
			if mode != "hash" {
				payload := map[string]any{"events": second["events"]}
				second["payload_digest"] = pureDigest(payload, "contexture.journal-payload/v1")
				delete(second, "record_hash")
				second["record_hash"] = pureDigest(second, "contexture.journal-record/v1")
			}
			if _, err := NewGraphJournalProjection([]map[string]any{first, second}, GraphJournalProjectionOptions{}); err == nil {
				t.Fatal("Invalid accepted cut replayed", mode)
			}
		})
	}
}
func TestGraphJournalProjectionPreservesSameGraphPerRecordKernelLimit(t *testing.T) {
	_, seed := projectionSeed(t, "graph")
	noop := map[string]any{"type": "CommandCompleted", "graph_id": "graph", "revision": "1", "command_digest": evOptions().CommandDigest, "status": "noop"}
	if _, err := NewGraphJournalProjection([]map[string]any{projectionRecord(1, nil, []any{seed.JSON(), noop}, nil)}, GraphJournalProjectionOptions{}); err == nil {
		t.Fatal("Multiple same-graph intermediate revisions were silently dropped")
	}
	_, other := projectionSeed(t, "other")
	if projection, err := NewGraphJournalProjection([]map[string]any{projectionRecord(1, nil, []any{seed.JSON(), other.JSON()}, nil)}, GraphJournalProjectionOptions{}); err != nil || len(projection.GraphIDs()) != 2 {
		t.Fatal("Different graphs in one record should retain separate exact histories", err)
	}
}
func TestGraphJournalProjectionOnlySchemaDefinedBlobClosure(t *testing.T) {
	definition, _ := projectionSeed(t, "graph")
	raw := definition.AsJSON().(map[string]any)
	fake := map[string]any{"digest": "sha256:" + strings.Repeat("0", 64), "size": "2", "media_type": "application/json"}
	raw["nodes"].([]any)[0].(map[string]any)["extensions"] = map[string]any{"opaque:metadata": map[string]any{"blob": fake}}
	definition = mustDefinition(t, raw, nil)
	unseeded, _ := NewUnseededState("graph")
	seed, err := BootstrapEvent(unseeded, definition, evOptions(), "approved")
	if err != nil {
		t.Fatal(err)
	}
	first := projectionRecord(1, nil, []any{seed.JSON()}, nil)
	projection, err := NewGraphJournalProjection([]map[string]any{first}, GraphJournalProjectionOptions{})
	if err != nil || len(arr(jsonObject(projection.Checkpoint().JSON())["blob_refs"])) != 0 {
		t.Fatal("Opaque author JSON was interpreted as required blob authority", err)
	}
	ref := map[string]any{"digest": "sha256:" + strings.Repeat("a", 64), "size": "3", "media_type": "text/plain"}
	second := projectionRecord(2, first["record_hash"], []any{map[string]any{"type": "RecoveryActionRecorded", "action_id": "recovery", "action": "verify", "evidence": ref}}, nil)
	projection, err = NewGraphJournalProjection([]map[string]any{first, second}, GraphJournalProjectionOptions{})
	if err != nil || len(arr(jsonObject(projection.Checkpoint().JSON())["blob_refs"])) != 1 {
		t.Fatal("Required recovery evidence closure absent", err)
	}
}
