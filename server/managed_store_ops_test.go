package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
)

type storeTestCustody struct{ deny bool }

const storeInteropJSON = `{"integer":9007199254740991,"fraction":333333333.33333329,"small":1e-7,"counter":"99999999999999999999","unicode":"雪"}`

func (c storeTestCustody) Approve(ctx context.Context, request OfflineStoreRequest) error {
	if c.deny || request.Operation == "" || request.Path == "" {
		return &JournalError{"permission_denied", "Private test custody refused"}
	}
	return ctx.Err()
}

func storeTestOperations(t *testing.T, options JournalOptions) *StoreOperations {
	t.Helper()
	ops, err := NewStoreOperations(StoreOperationsOptions{Schema: options.Schema,
		Durability: journalDefault(options.Durability, "strict"), Projector: options.Projector,
		Custody: storeTestCustody{}, Compatibility: NativeJournalCompatibility()})
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func storeTestFingerprint(t *testing.T, root string) string {
	t.Helper()
	items := []any{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := JournalObject{"path": filepath.ToSlash(relative), "mode": info.Mode().String(),
			"modified": info.ModTime().String(), "size": fmt.Sprint(info.Size())}
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item["digest"] = journalByteDigest(raw)
		}
		items = append(items, item)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return storeSubjectDigest(items)
}

func TestStoreOperationsNativeGraphOwnerJourney(t *testing.T) {
	f := newGraphOwnerFixture(t, nil)
	f.boot()
	first, err := f.journal.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	f.edit("1", "edit", "offline original")
	second, err := f.journal.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := f.journal.Projections()
	if err != nil {
		t.Fatal(err)
	}
	ops := storeTestOperations(t, f.journalOptions)
	_, err = ops.Backup(f.context, f.root, nil)
	journalTestCode(t, err, "store_in_use")
	if err := f.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.journal.Close(); err != nil {
		t.Fatal(err)
	}
	before := storeTestFingerprint(t, f.root)
	calls := f.calls.Load()
	result, err := ops.Doctor(f.context, f.root, nil)
	if err != nil || !result.StorageVerified || !result.ProjectionVerified || result.ServingReady || result.WriteEnabled || result.IsolatedRestoreVerified {
		t.Fatal(result, err)
	}
	backup, err := ops.Backup(f.context, f.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != storeTestFingerprint(t, f.root) || f.calls.Load() != calls {
		t.Fatal("Offline inspection changed source or called graph Host")
	}
	manifest := backup.Manifest()
	anchor := &StoreAnchor{StoreID: manifest["store_id"].(string), JournalSeq: manifest["journal_seq"].(string), RecordHash: manifest["record_hash"]}
	if _, err := ops.Backup(f.context, f.root, anchor); err != nil {
		t.Fatal(err)
	}
	anchor.JournalSeq = "1"
	_, err = ops.Backup(f.context, f.root, anchor)
	journalTestCode(t, err, "history_unavailable")
	clone := journalTestRoot(t)
	result, err = ops.Restore(f.context, clone, backup.Bundle())
	if err != nil || !result.IsolatedRestoreVerified || result.WriteEnabled || result.ServingReady {
		t.Fatal(result, err)
	}
	restored := journalTestOpen(t, clone, f.journalOptions)
	actual, err := restored.Projections()
	if err != nil || !journalEqual(projection, actual) || restored.Meta()["store_id"] == manifest["store_id"] {
		t.Fatal("Complete clone projection or identity differs", err)
	}
	origin := journalMap(restored.Meta()["restored_from"])
	originBytes, err := restored.GetBlob(origin)
	if err != nil || !bytes.Equal(originBytes, backup.Snapshot()) {
		t.Fatal("Original backup provenance differs", err)
	}
	snapshot := storeJSON(originBytes)
	encoded := journalMap(snapshot["blobs"])[manifest["assets_digest"].(string)].(string)
	archive, err := restored.GetBlob(JournalObject{"digest": manifest["assets_digest"],
		"size": fmt.Sprint(len(encoded) / 2), "media_type": "application/json"})
	if err != nil || !bytes.Contains(archive, []byte(fmt.Sprintf("%x", journalBytes(first)))) ||
		!bytes.Contains(archive, []byte(fmt.Sprintf("%x", journalBytes(second)))) {
		t.Fatal("Complete original checkpoint inventory lost", err)
	}
	_, err = ops.Restore(f.context, clone, backup.Bundle())
	journalTestCode(t, err, "restore_conflict")
	_ = restored.Close()
	restored = journalTestOpen(t, clone, f.journalOptions)
	actual, err = restored.Projections()
	if err != nil || !journalEqual(actual, projection) {
		t.Fatal("Real reopened clone lost history", err)
	}
	owner, err := NewFileJournalGraphOwner(restored, GraphOwnerOptions{Host: f, Serialization: f.serialization, Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	state, err := owner.Read(f.context, "graph", "2")
	if err != nil || state.Revision() != "2" {
		t.Fatal("Public graph owner cannot read isolated restored history", err)
	}
}

func TestStoreOperationsNativeReleaseHistory(t *testing.T) {
	f := newReleaseFixture(t, nil)
	f.options.Projector = GraphJournalProjector(managed.GraphJournalProjectionOptions{})
	f.journal.options.Projector = f.options.Projector
	owner := f.owner()
	if _, err := owner.Request(f.request(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	projection, err := f.journal.Projections()
	if err != nil {
		t.Fatal(err)
	}
	options := f.options
	ops := storeTestOperations(t, options)
	_ = f.journal.Close()
	counts := [3]int32{f.compiles.Load(), f.handlers.Load(), f.decodes.Load()}
	before := storeTestFingerprint(t, f.root)
	backup, err := ops.Backup(context.Background(), f.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	clone := journalTestRoot(t)
	result, err := ops.Restore(context.Background(), clone, backup.Bundle())
	if err != nil || !result.IsolatedRestoreVerified {
		t.Fatal(result, err)
	}
	if before != storeTestFingerprint(t, f.root) || counts != [3]int32{f.compiles.Load(), f.handlers.Load(), f.decodes.Load()} {
		t.Fatal("Offline replay performed external release preparation or callbacks")
	}
	j := journalTestOpen(t, clone, options)
	actual, err := j.Projections()
	if err != nil || !journalEqual(actual, projection) || len(journalArray(actual["desired_releases"])) != 1 {
		t.Fatal("Desired release/operations history lost", err)
	}
}

func TestStoreOperationsReadOnlyRefusals(t *testing.T) {
	for _, kind := range []string{"missing_meta", "format", "profile", "noncanonical", "gap", "blob",
		"tmp", "unknown", "permissions", "symlink", "hardlink", "checkpoint", "schema"} {
		t.Run(kind, func(t *testing.T) {
			o := journalTestOptions(t)
			root := journalTestRoot(t)
			j := journalTestOpen(t, root, o)
			journalTestAppend(t, j, "one")
			ref, err := j.PutBlob([]byte("required original"), "text/plain")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := j.Append([]any{JournalObject{"type": "RecoveryActionRecorded", "action_id": "retain", "action": "retain", "evidence": ref}}, JournalAppendOptions{ActorRef: "actor"}); err != nil {
				t.Fatal(err)
			}
			cp, err := j.Checkpoint()
			if err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			record := filepath.Join(root, "records", strings.Repeat("0", 20), strings.Repeat("0", 19)+"1.json")
			code := "corruption"
			write := func(path string, raw []byte) {
				t.Helper()
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "missing_meta":
				if err := os.Remove(filepath.Join(root, "META.json")); err != nil {
					t.Fatal(err)
				}
			case "format", "profile", "schema":
				meta := storeJSON(journalBytes(j.meta))
				if kind == "format" {
					meta["schema"], code = "contexture.file-journal/v99", "format_unsupported"
				} else if kind == "profile" {
					meta["durability"], code = "strict", "durability_unsupported"
				} else {
					meta["unexpected"], code = true, "schema_invalid"
				}
				write(filepath.Join(root, "META.json"), journalBytes(meta))
			case "noncanonical":
				write(record, []byte("{}\n"))
			case "gap":
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
			case "blob":
				write(j.blobPath(ref["digest"]), []byte("bad"))
			case "tmp":
				write(filepath.Join(root, "tmp", "unpublished"), []byte("source-secret"))
				code = "recovery_required"
			case "unknown":
				write(filepath.Join(root, "unexpected"), []byte("source-secret"))
				code = "format_unsupported"
			case "permissions":
				if err := os.Chmod(record, 0644); err != nil {
					t.Fatal(err)
				}
				code = "unsafe_permissions"
			case "symlink":
				if err := os.Symlink(record, filepath.Join(root, "indexes", "link")); err != nil {
					t.Fatal(err)
				}
				code = "unsafe_path"
			case "hardlink":
				if err := os.Link(record, filepath.Join(root, "indexes", "link")); err != nil {
					t.Fatal(err)
				}
				code = "unsafe_path"
			case "checkpoint":
				cp["checkpoint_digest"] = "sha256:" + strings.Repeat("a", 64)
				write(filepath.Join(root, "checkpoints", fmtStoreCounter("2"), cp["checkpoint_digest"].(string)[7:]+".json"), journalBytes(cp))
				code = "checkpoint_invalid"
			}
			before := storeTestFingerprint(t, root)
			result, err := storeTestOperations(t, o).Doctor(context.Background(), root, nil)
			journalTestCode(t, err, code)
			if result.Code != code || result.StorageVerified || result.WriteEnabled || before != storeTestFingerprint(t, root) {
				t.Fatal("Doctor mutated/refused wrong facts", result, err)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "source-secret") {
				t.Fatal("SDK diagnostic disclosed source facts")
			}
		})
	}
}

func TestStoreOperationsCustodyBudgetsAndSoftware(t *testing.T) {
	o := journalTestOptions(t)
	options := StoreOperationsOptions{Schema: o.Schema, Durability: o.Durability, Projector: o.Projector, Compatibility: NativeJournalCompatibility()}
	_, err := NewStoreOperations(options)
	journalTestCode(t, err, "authority_required")
	options.Custody = storeTestCustody{}
	options.Projector = nil
	_, err = NewStoreOperations(options)
	journalTestCode(t, err, "projection_required")
	options.Projector = o.Projector
	options.Compatibility.ReaderBuild = ""
	_, err = NewStoreOperations(options)
	journalTestCode(t, err, "software_unsupported")
	options.Compatibility = NativeJournalCompatibility()
	root := journalTestRoot(t)
	j := journalTestOpen(t, root, o)
	journalTestAppend(t, j, "software")
	_ = j.Close()
	options.Custody = storeTestCustody{deny: true}
	ops, err := NewStoreOperations(options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ops.Backup(context.Background(), root, nil)
	journalTestCode(t, err, "permission_denied")
	options.Custody = storeTestCustody{}
	options.Budget = StoreBudget{1, 1, 1, 1}
	ops, err = NewStoreOperations(options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ops.Backup(context.Background(), root, nil)
	journalTestCode(t, err, "limit_exceeded")
	ops = storeTestOperations(t, o)
	backup, err := ops.Backup(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backup.MarshalJSON()
	journalTestCode(t, err, "permission_denied")
	copyBytes := backup.Snapshot()
	copyBytes[0] = 0
	if backup.Snapshot()[0] == 0 {
		t.Fatal("Privileged snapshot ownership escaped")
	}
	err = ops.PublishBackup(context.Background(), filepath.Join(root, "forbidden.json"), backup, false)
	journalTestCode(t, err, "unsafe_path")
	err = ops.PublishBackup(context.Background(), "never-created.json", &StoreBackup{Result: StoreResult{Code: "ok"}}, false)
	journalTestCode(t, err, "backup_invalid")
	options.Budget = DefaultStoreBudget()
	options.Compatibility.EventTypes = []string{"RecoveryActionRecorded"}
	old, err := NewStoreOperations(options)
	if err != nil {
		t.Fatal(err)
	}
	destination := journalTestRoot(t)
	_, err = old.Restore(context.Background(), destination, backup.Bundle())
	journalTestCode(t, err, "format_unsupported")
	if journalExists(destination) {
		t.Fatal("Software downgrade created destination")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ops.Backup(ctx, root, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	bad := storeJSON(backup.Bundle())
	bad["bundle_digest"] = "sha256:" + strings.Repeat("0", 64)
	_, err = ops.Restore(context.Background(), destination, journalBytes(bad))
	journalTestCode(t, err, "backup_invalid")
	if journalExists(destination) {
		t.Fatal("Invalid original assets created destination")
	}
}

func TestStoreOperationsPureCompatibilityAndContext(t *testing.T) {
	valid, err := graph.DecodeJSON([]byte(storeInteropJSON))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := graph.CanonicalJSON(valid)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := graph.DecodeJSON(canonical)
	if err != nil || !journalEqual(valid, roundTrip) {
		t.Fatal("Valid JCS writer bytes cannot be read back", err)
	}
	for _, raw := range []string{`9007199254740992`, `9007199254740993`, `1e30`} {
		_, err := graph.DecodeJSON([]byte(raw))
		var diagnostic *graph.GraphError
		if !errors.As(err, &diagnostic) || diagnostic.Code() != "schema_invalid" {
			t.Fatalf("Unsafe integer lexeme accepted: %s: %v", raw, err)
		}
	}
	schema := journalTestSchema(t)
	ops := storeTestOperations(t, JournalOptions{Schema: schema, Durability: "strict",
		Projector: GraphJournalProjector(managed.GraphJournalProjectionOptions{})})
	_, err = ops.Doctor(nil, "never-opened", nil)
	journalTestCode(t, err, "authority_required")
	subject := JournalObject{"schema": "contexture.file-journal-backup/v1",
		"meta":    JournalObject{"schema": "contexture.file-journal/v1", "durability": "strict", "canonical_profile": graph.CanonicalProfile},
		"records": []any{JournalObject{"schema": "contexture.journal-record/v1", "events": []any{JournalObject{"type": "FutureRequiredFact"}}}},
		"blobs":   JournalObject{}, "checkpoint": nil}
	subject["backup_digest"] = journalDigest("contexture.file-journal-backup/v1", subject)
	result, err := ops.Restore(context.Background(), "never-created", journalBytes(subject))
	journalTestCode(t, err, "format_unsupported")
	if result.Code != "format_unsupported" || result.IsolatedRestoreVerified || result.WriteEnabled {
		t.Fatal(result)
	}
	subject["records"] = []any{JournalObject{"schema": "contexture.journal-record/v1", "events": []any{
		JournalObject{"type": "GraphChangeCommitted", "semantic_version": "99"}}}}
	result, err = ops.Restore(context.Background(), "never-created", journalBytes(subject))
	journalTestCode(t, err, "semantic_unsupported")
	if result.Code != "semantic_unsupported" {
		t.Fatal(result)
	}
}

func TestStoreOperationsPublicationAndInterop(t *testing.T) {
	o := journalTestOptions(t)
	o.Projector = func(records []JournalObject) (JournalObject, error) {
		projection, err := journalTestProject(records)
		if err != nil {
			return nil, err
		}
		// Match the reference's explicitly receipt-only test oracle.
		projection["projection_versions"] = JournalObject{"receipts": "1"}
		return projection, nil
	}
	root := journalTestRoot(t)
	j := journalTestOpen(t, root, o)
	exact, err := graph.DecodeJSON([]byte(storeInteropJSON))
	if err != nil {
		t.Fatal(err)
	}
	receipt := journalTestReceipt(1, "exact", o.Durability)
	receipt["operation_result"] = JournalObject{"inline": exact}
	if _, err := j.Append(journalTestEvent("exact"), JournalAppendOptions{ActorRef: "actor", CommandReceipt: receipt}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PutBlob(journalBytes(exact), "application/json"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`9007199254740992`, `9007199254740993`, `1e30`} {
		_, err := graph.DecodeJSON([]byte(raw))
		var diagnostic *graph.GraphError
		if !errors.As(err, &diagnostic) || diagnostic.Code() != "schema_invalid" {
			t.Fatalf("Unsafe integer lexeme accepted: %s: %v", raw, err)
		}
	}
	_ = j.Close()
	ops := storeTestOperations(t, o)
	backup, err := ops.Backup(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(filepath.Dir(root), "offline-backup.json")
	if err := ops.PublishBackup(context.Background(), output, backup, true); err != nil {
		t.Fatal(err)
	}
	err = ops.PublishBackup(context.Background(), output, backup, true)
	journalTestCode(t, err, "restore_conflict")
	raw, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(raw, backup.Snapshot()) {
		t.Fatal(err)
	}
	python := os.Getenv("CONTEXTURE_JOURNAL_PYTHON")
	if python == "" {
		t.Fatal("Native interoperability requires explicit Python test oracle")
	}
	program := `import os,json
from pathlib import Path
from contexture.server.managed_journal import FileJournal
from tests.test_managed_journal import empty_projector
schema=json.loads(Path(os.environ["CONTEXTURE_MANAGED_SCHEMA"]).read_text())
with FileJournal.restore_snapshot(os.environ["STORE_RESTORE"],Path(os.environ["STORE_BACKUP"]).read_bytes(),schema=schema,durability="process_crash",projector=empty_projector) as j:
 assert j.watermark=="1"
 assert j.meta["restored_from"]
 result=j.get_receipt("actor","exact")["operation_result"]["inline"]
 assert result["integer"]==9007199254740991
 assert result["fraction"]==333333333.3333333
 assert result["small"]==1e-7
 assert result["counter"]=="99999999999999999999"
 print("python-restored-native-offline-backup")
`
	command := exec.Command(python, "-c", program)
	command.Env = append(os.Environ(), "STORE_BACKUP="+output, "STORE_RESTORE="+journalTestRoot(t))
	stdout, err := command.CombinedOutput()
	t.Logf("Original Python restore oracle: %s", stdout)
	if err != nil || !bytes.Contains(stdout, []byte("python-restored-native-offline-backup")) {
		t.Fatal(err)
	}
}
