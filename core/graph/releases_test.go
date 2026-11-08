package graph

import (
	"reflect"
	"strings"
	"testing"
)

func releaseTestRequest() map[string]any {
	return map[string]any{"schema": "contexture.release-request/v1", "graph_id": "graph", "target_id": "target", "revision": "1",
		"definition_digest": "sha256:" + strings.Repeat("a", 64), "manifest_digest": "sha256:" + strings.Repeat("b", 64),
		"expected_desired_epoch": "0", "idempotency_key": "key", "policy": "exact", "reason": "test", "approval_ref": "approval"}
}
func releaseTestRecord(events []any, receipt any) map[string]any {
	record := map[string]any{"schema": "contexture.journal-record/v1", "journal_seq": "1", "actor_ref": "actor", "prev_record_hash": nil, "events": events}
	payload := map[string]any{"events": events}
	if receipt != nil {
		record["command_receipt"], payload["command_receipt"] = receipt, receipt
	}
	record["payload_digest"] = pureDigest(payload, "contexture.journal-payload/v1")
	record["record_hash"] = pureDigest(record, "contexture.journal-record/v1")
	return record
}
func TestReleaseRequestImmutableSubjectAndReceiptProvenance(t *testing.T) {
	raw := releaseTestRequest()
	request, err := ParseReleaseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := request.Digest()
	raw["approval_ref"], raw["idempotency_key"] = "other", "other"
	same, _ := ParseReleaseRequest(raw)
	otherDigest, _ := same.Digest()
	if digest != otherDigest {
		t.Fatal("approval/key entered release subject")
	}
	mutateJSON(raw)
	event := map[string]any{"type": "ReleaseRequested", "request": request.AsJSON(), "release_epoch": "1"}
	result := map[string]any{"schema": "contexture.release-receipt/v1", "release": requestIdentity(request, "1"), "journal_seq": "1", "request_digest": digest, "status": "requested"}
	receipt := map[string]any{"schema": "contexture.command-receipt/v1", "graph_id": "graph", "actor_ref": "actor", "idempotency_key": "key", "request_digest": digest,
		"revision": "1", "journal_seq": "1", "status": "committed", "durability": "strict", "operation_result": map[string]any{"inline": result}}
	record := releaseTestRecord([]any{event}, receipt)
	state, err := ReplayReleases([]map[string]any{record})
	if err != nil || len(state.Items("release_operations")) != 1 {
		t.Fatal(state.AsJSON(), err)
	}
	mutateJSON(state.AsJSON())
	if _, err := ImportReleaseState(state.AsJSON()); err != nil {
		t.Fatal("release state alias escaped", err)
	}
	tampered := mustFreeze(record).JSON().(map[string]any)
	tampered["actor_ref"] = "other"
	if _, err := ReplayReleases([]map[string]any{tampered}); err == nil {
		t.Fatal("record hash tampering accepted")
	}
	receipt["actor_ref"] = "other"
	record = releaseTestRecord([]any{event}, receipt)
	if _, err := ReplayReleases([]map[string]any{record}); err == nil {
		t.Fatal("receipt/record actor provenance mismatch accepted")
	}
}
func TestReleasePureTerminalSupersessionAndCheckpointSections(t *testing.T) {
	request, _ := ParseReleaseRequest(releaseTestRequest())
	first := map[string]any{"type": "ReleaseRequested", "request": request.AsJSON(), "release_epoch": "1"}
	record := releaseTestRecord([]any{first}, nil)
	state, err := ReduceReleaseEvent(EmptyReleaseState(), first, record)
	if err != nil {
		t.Fatal(err)
	}
	id := map[string]any{"release": requestIdentity(request, "1"), "instance_id": "instance", "boot_id": "01234567-89ab-cdef-0123-456789abcdef", "attempt_id": "attempt"}
	ready := map[string]any{"type": "ReleasePreparationSucceeded", "attempt": id, "generation_id": "01234567-89ab-cdef-0123-456789abcdef", "manifest_digest": request.ManifestDigest()}
	state, err = ReduceReleaseEvent(state, ready, map[string]any{"journal_seq": "2", "actor_ref": "actor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReduceReleaseEvent(state, ready, map[string]any{"journal_seq": "3", "actor_ref": "actor"}); err == nil {
		t.Fatal("duplicate terminal accepted")
	}
	observation := map[string]any{"type": "ReplicaObserved", "observation": map[string]any{"release": requestIdentity(request, "1"), "instance_id": "instance",
		"boot_id": id["boot_id"], "generation_id": ready["generation_id"], "revision": "1", "definition_digest": request.DefinitionDigest(),
		"manifest_digest": request.ManifestDigest(), "observed_at": "2026-11-07T00:00:00Z"}}
	state, err = ReduceReleaseEvent(state, observation, map[string]any{"journal_seq": "3", "actor_ref": "actor"})
	if err != nil {
		t.Fatal(err)
	}
	raw := releaseTestRequest()
	raw["expected_desired_epoch"], raw["idempotency_key"], raw["policy"] = "1", "next", "latest"
	next, _ := ParseReleaseRequest(raw)
	events := []any{map[string]any{"type": "ReleaseSuperseded", "release": requestIdentity(request, "1"), "superseded_by": "2"},
		map[string]any{"type": "ReleaseRequested", "request": next.AsJSON(), "release_epoch": "2"}}
	for _, event := range events {
		state, err = ReduceReleaseEvent(state, event, map[string]any{"journal_seq": "4", "actor_ref": "actor", "events": events})
		if err != nil {
			t.Fatal(err)
		}
	}
	statuses := []any{}
	for _, item := range state.Items("release_operations") {
		statuses = append(statuses, jsonObject(item)["status"])
	}
	if !reflect.DeepEqual(statuses, []any{"requested", "superseded", "requested"}) || len(state.Items("prepare_attempt_history")) != 1 || len(state.Items("observations_history")) != 1 {
		t.Fatal(state.AsJSON())
	}
	bad := state.AsJSON().(map[string]any)
	bad["prepare_attempt_history"] = []any{map[string]any{"identity": id, "status": "preparing"}}
	if _, err := ImportReleaseState(bad); err == nil {
		t.Fatal("absent-schema durable pending state invented")
	}
}
func TestReleaseParsersRejectUnknownAndNoncanonicalValues(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["revision"] = "0" },
		func(m map[string]any) { m["revision"] = "01" },
		func(m map[string]any) { m["manifest_digest"] = "sha256:" + strings.Repeat("a", 64) + "\n" },
		func(m map[string]any) { m["policy"] = "coalesced" },
		func(m map[string]any) { m["pending"] = true },
	} {
		raw := releaseTestRequest()
		mutate(raw)
		if _, err := ParseReleaseRequest(raw); err == nil {
			t.Fatal("invalid release request accepted", raw)
		}
	}
}
