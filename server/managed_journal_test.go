package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// The registered schema is supplied by the test host, not embedded or fetched
// by production. Pin/golden assets remain untouched.
func journalTestSchema(t *testing.T) any {
	t.Helper()
	p := os.Getenv("CONTEXTURE_MANAGED_SCHEMA")
	if p == "" {
		p = "../../contexture-mcp-graph-runtime-20261105/spec/managed/schemas/managed.schema.json"
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatalf("Test host must supply CONTEXTURE_MANAGED_SCHEMA: %v", e)
	}
	v, e := graph.DecodeJSON(b)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func journalTestRoot(t *testing.T) string {
	t.Helper()
	p, e := os.MkdirTemp("", "contexture-go-journal-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(p) })
	return filepath.Join(p, "store")
}
func journalTestOpen(t *testing.T, root string, o JournalOptions) *FileJournal {
	t.Helper()
	j, e := OpenFileJournal(root, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}
func journalTestOptions(t *testing.T) JournalOptions {
	return JournalOptions{Schema: journalTestSchema(t), Durability: "process_crash", Projector: journalTestProject}
}
func journalTestProject(rs []JournalObject) (JournalObject, error) {
	p := JournalObject{}
	for _, k := range journalProjectionKeys {
		p[k] = []any{}
	}
	p["projection_versions"] = JournalObject{"graphs": "1", "releases": "1", "receipts": "1"}
	refs := map[string]JournalObject{}
	for _, r := range rs {
		if c, ok := r["command_receipt"]; ok {
			p["receipts_index"] = append(p["receipts_index"].([]any), c)
		}
		for _, ref := range journalRefs(r) {
			refs[ref["digest"].(string)+ref["media_type"].(string)] = ref
		}
	}
	// This independent test projector intentionally models receipt/recovery-only
	// streams. A production graph/release reducer must be supplied by its host.
	for _, r := range refs {
		p["blob_refs"] = append(p["blob_refs"].([]any), r)
	}
	// Stable tuple ordering is part of the checkpoint wire contract.
	sortJournalTest(p["receipts_index"].([]any), func(m JournalObject) string { return m["actor_ref"].(string) + "\x00" + m["idempotency_key"].(string) })
	sortJournalTest(p["blob_refs"].([]any), func(m JournalObject) string { return m["digest"].(string) + "\x00" + m["media_type"].(string) })
	return p, nil
}
func sortJournalTest(a []any, key func(JournalObject) string) {
	for i := 1; i < len(a); i++ {
		for k := i; k > 0 && key(journalMap(a[k])) < key(journalMap(a[k-1])); k-- {
			a[k], a[k-1] = a[k-1], a[k]
		}
	}
}
func journalTestEvent(key string) []any {
	return []any{JournalObject{"type": "CommandCompleted", "graph_id": "g", "revision": "1", "command_digest": "sha256:" + strings.Repeat("a", 64), "status": "noop"}}
}
func journalTestReceipt(n int, key, profile string) JournalObject {
	return JournalObject{"schema": "contexture.command-receipt/v1", "graph_id": "g", "actor_ref": "actor", "idempotency_key": key, "request_digest": "sha256:" + strings.Repeat("a", 64), "revision": "1", "journal_seq": fmt.Sprint(n), "status": "noop", "durability": profile, "operation_result": JournalObject{"inline": JournalObject{"ok": true}}}
}
func journalTestAppend(t *testing.T, j *FileJournal, key string) JournalObject {
	t.Helper()
	wm, e := j.Watermark()
	if e != nil {
		t.Fatal(e)
	}
	var n int
	_, _ = fmt.Sscan(wm, &n)
	r, e := j.Append(journalTestEvent(key), JournalAppendOptions{ActorRef: "actor", CommandReceipt: journalTestReceipt(n+1, key, j.durability)})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func journalTestCode(t *testing.T, e error, code string) {
	t.Helper()
	var j *JournalError
	if !errors.As(e, &j) || j.Code() != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}

func TestManagedJournalRoundTrip(t *testing.T) {
	root := journalTestRoot(t)
	o := journalTestOptions(t)
	j := journalTestOpen(t, root, o)
	r := journalTestAppend(t, j, "first")
	again, e := j.Append(journalTestEvent("first"), JournalAppendOptions{ActorRef: "actor", CommandReceipt: journalTestReceipt(999, "first", j.durability)})
	if e != nil || !journalEqual(r, again) {
		t.Fatalf("retry: %v", e)
	}
	bad := journalTestReceipt(2, "first", j.durability)
	bad["request_digest"] = "sha256:" + strings.Repeat("b", 64)
	_, e = j.Append(journalTestEvent("first"), JournalAppendOptions{ActorRef: "actor", CommandReceipt: bad})
	journalTestCode(t, e, "idempotency_conflict")
	ref, e := j.PutBlob([]byte("original UTF-8: 雪"), "text/plain")
	if e != nil {
		t.Fatal(e)
	}
	data, e := j.GetBlob(ref)
	if e != nil || !bytes.Equal(data, []byte("original UTF-8: 雪")) {
		t.Fatal(e)
	}
	_, e = j.Append([]any{JournalObject{"type": "RecoveryActionRecorded", "action_id": "evidence", "action": "native test", "evidence": ref}}, JournalAppendOptions{ActorRef: "actor"})
	if e != nil {
		t.Fatal(e)
	}
	cp, e := j.Checkpoint()
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := j.ReadCheckpoint()
	if e != nil || !journalEqual(cp, loaded) {
		t.Fatalf("checkpoint %v", e)
	}
	backup, e := j.ExportSnapshot()
	if e != nil {
		t.Fatal(e)
	}
	_ = j.Close()
	j = journalTestOpen(t, root, o)
	if wm, _ := j.Watermark(); wm != "2" {
		t.Fatal(wm)
	}
	restored, e := RestoreFileJournal(journalTestRoot(t), backup, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if restored.Meta()["store_id"] == j.Meta()["store_id"] {
		t.Fatal("restore reused identity")
	}
	if _, ok := restored.Meta()["restored_from"]; !ok {
		t.Fatal("missing provenance")
	}
	_ = restored.Close()
	restored = journalTestOpen(t, restored.root, o)
	if wm, _ := restored.Watermark(); wm != "2" {
		t.Fatal(wm)
	}
	r["store_id"] = "mutated"
	records, e := j.Records("0")
	if e != nil || records[0]["store_id"] == "mutated" {
		t.Fatal("escaped owned state")
	}
}

func TestManagedJournalFaultBoundaries(t *testing.T) {
	for _, stage := range []string{"record.temp_written", "record.file_synced", "record.before_rename", "record.renamed", "record.directory_synced"} {
		t.Run(stage, func(t *testing.T) {
			root := journalTestRoot(t)
			o := journalTestOptions(t)
			o.Fault = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			j := journalTestOpen(t, root, o)
			_, e := j.Append(journalTestEvent("fault"), JournalAppendOptions{ActorRef: "actor", CommandReceipt: journalTestReceipt(1, "fault", j.durability)})
			if e == nil {
				t.Fatal("fault not observed")
			}
			unknown := stage == "record.renamed" || stage == "record.directory_synced"
			if unknown {
				journalTestCode(t, e, "commit_unknown")
				_, e = j.PutBlob([]byte("frozen"), "")
				journalTestCode(t, e, "commit_unknown")
				_, e = j.Checkpoint()
				journalTestCode(t, e, "commit_unknown")
				_, e = j.ExportSnapshot()
				journalTestCode(t, e, "commit_unknown")
			}
			if wm, _ := j.Watermark(); wm != "0" {
				t.Fatal("advanced unconfirmed watermark")
			}
			_ = j.Close()
			o.Fault = nil
			j = journalTestOpen(t, root, o)
			expected := "0"
			if unknown {
				expected = "1"
			}
			if wm, _ := j.Watermark(); wm != expected {
				t.Fatalf("recovered %s want %s", wm, expected)
			}
			if !unknown && len(j.RecoveryActions()) != 1 {
				t.Fatal("unpublished work lacks quarantine evidence")
			}
			journalTestAppend(t, j, "after")
		})
	}
}

func TestManagedJournalCorruptionAndSafety(t *testing.T) {
	for _, kind := range []string{"chain", "gap", "noncanonical", "missing_meta", "symlink", "hardlink", "permissions", "missing_blob", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			root := journalTestRoot(t)
			o := journalTestOptions(t)
			j := journalTestOpen(t, root, o)
			journalTestAppend(t, j, "one")
			journalTestAppend(t, j, "two")
			_, e := j.Checkpoint()
			if e != nil {
				t.Fatal(e)
			}
			ref, e := j.PutBlob([]byte("required"), "")
			if e != nil {
				t.Fatal(e)
			}
			_, e = j.Append([]any{JournalObject{"type": "RecoveryActionRecorded", "action_id": "e", "action": "retain", "evidence": ref}}, JournalAppendOptions{ActorRef: "actor"})
			if e != nil {
				t.Fatal(e)
			}
			p := j.recordPath(bigJournalTest(1))
			_ = j.Close()
			switch kind {
			case "chain":
				b, _ := os.ReadFile(p)
				b = bytes.Replace(b, []byte(`"actor_ref":"actor"`), []byte(`"actor_ref":"other"`), 1)
				e = os.WriteFile(p, b, 0600)
			case "gap":
				e = os.Remove(p)
			case "noncanonical":
				f, er := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
				if er != nil {
					t.Fatal(er)
				}
				_, e = f.WriteString("\n")
				_ = f.Close()
			case "missing_meta":
				e = os.Remove(filepath.Join(root, "META.json"))
			case "symlink":
				e = os.Symlink(p, filepath.Join(root, "indexes", "link"))
			case "hardlink":
				e = os.Link(p, filepath.Join(root, "indexes", "hard"))
			case "permissions":
				e = os.Chmod(p, 0644)
			case "missing_blob":
				e = os.Remove(j.blobPath(ref["digest"]))
			case "checkpoint":
				files, _ := filepath.Glob(filepath.Join(root, "checkpoints", "*", "*.json"))
				e = os.WriteFile(files[0], []byte("{}"), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			next, e := OpenFileJournal(root, o)
			if kind == "checkpoint" {
				if e != nil {
					t.Fatal(e)
				}
				defer next.Close()
				cp, e := next.ReadCheckpoint()
				if e != nil || cp != nil {
					t.Fatal("bad checkpoint did not fall back")
				}
			} else if e == nil {
				_ = next.Close()
				t.Fatal("corruption accepted")
			}
		})
	}
}
func bigJournalTest(n int64) *big.Int { return big.NewInt(n) }

func TestManagedJournalConcurrent(t *testing.T) {
	j := journalTestOpen(t, journalTestRoot(t), journalTestOptions(t))
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := j.Append(journalTestEvent(""), JournalAppendOptions{ActorRef: "worker"})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if wm, _ := j.Watermark(); wm != "24" {
		t.Fatal(wm)
	}
}

func TestManagedJournalSubprocess(t *testing.T) {
	if mode := os.Getenv("JOURNAL_CHILD_MODE"); mode != "" {
		o := journalTestOptions(t)
		if mode == "crash" {
			o.Fault = func(s string) error {
				if s == os.Getenv("JOURNAL_CHILD_STAGE") {
					os.Exit(73)
				}
				return nil
			}
		}
		j, e := OpenFileJournal(os.Getenv("JOURNAL_CHILD_ROOT"), o)
		if mode == "lock" {
			if e == nil {
				_ = j.Close()
				os.Exit(82)
			}
			var je *JournalError
			if errors.As(e, &je) && je.Code() == "store_in_use" {
				os.Exit(74)
			}
			t.Fatal(e)
		}
		if e != nil {
			t.Fatal(e)
		}
		journalTestAppend(t, j, "child")
		os.Exit(83)
	}
	for _, stage := range []string{"record.temp_written", "record.file_synced", "record.before_rename", "record.renamed", "record.directory_synced"} {
		t.Run(stage, func(t *testing.T) {
			root := journalTestRoot(t)
			o := journalTestOptions(t)
			j := journalTestOpen(t, root, o)
			journalChild(t, root, "lock", "", 74)
			_ = j.Close()
			journalChild(t, root, "crash", stage, 73)
			j = journalTestOpen(t, root, o)
			wm, _ := j.Watermark()
			want := "0"
			if stage == "record.renamed" || stage == "record.directory_synced" {
				want = "1"
			}
			if wm != want {
				t.Fatalf("actual crash recovery %s want %s", wm, want)
			}
			journalTestAppend(t, j, "parent")
		})
	}
}
func journalChild(t *testing.T, root, mode, stage string, want int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestManagedJournalSubprocess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "JOURNAL_CHILD_MODE="+mode, "JOURNAL_CHILD_ROOT="+root, "JOURNAL_CHILD_STAGE="+stage)
	b, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != want {
		t.Fatalf("child %s expected %d: %v %s", mode, want, e, b)
	}
}

func TestManagedJournalPlatformAndSchema(t *testing.T) {
	o := journalTestOptions(t)
	o.Durability = "unknown"
	_, e := OpenFileJournal(journalTestRoot(t), o)
	journalTestCode(t, e, "durability_unsupported")
	o = journalTestOptions(t)
	schema := journalMap(journalCopy(o.Schema))
	schema["$ref"] = "https://invalid.example/schema"
	o.Schema = schema
	_, e = OpenFileJournal(journalTestRoot(t), o)
	journalTestCode(t, e, "schema_invalid")
	o = journalTestOptions(t)
	_, e = OpenFileJournal(journalTestRoot(t)+"/../escape", o)
	journalTestCode(t, e, "unsafe_path")
	if runtime.GOOS == "linux" {
		if e := journalPlatform("/mnt/c/contexture-rejected", "strict"); e == nil {
			t.Fatal("drvfs must fail closed")
		}
	}
}

// Cross-binding handoff uses real production adapters, never copied fixtures.
// Explicit test-host paths are required so ordinary Go tests have no subprocess
// dependency on Python/Node. CI exercising parity must set both variables.
func TestManagedJournalHandoff(t *testing.T) {
	py, ts := os.Getenv("CONTEXTURE_JOURNAL_PYTHON"), os.Getenv("CONTEXTURE_JOURNAL_TS")
	if py == "" || ts == "" {
		t.Skip("Cross-language test host not configured")
	}
	root := journalTestRoot(t)
	o := journalTestOptions(t)
	run := func(program string, args ...string) {
		t.Helper()
		cmd := exec.Command(program, args...)
		cmd.Env = append(os.Environ(), "JOURNAL_HANDOFF_ROOT="+root)
		b, e := cmd.CombinedOutput()
		t.Logf("%s: %s", program, b)
		if e != nil {
			t.Fatal(e)
		}
	}
	python := `import os,json
from pathlib import Path
from contexture.server.managed_journal import FileJournal
s=json.loads(Path(os.environ['CONTEXTURE_MANAGED_SCHEMA']).read_text())
with FileJournal.open(os.environ['JOURNAL_HANDOFF_ROOT'],schema=s,durability='process_crash') as j:
 n=int(j.watermark)
 assert n in (0,3),(n,'unexpected sequential owner')
 if n==0:
  ref=j.put_blob(b'python native evidence',media_type='text/plain')
  j.append([{'type':'RecoveryActionRecorded','action_id':'python','action':'handoff','evidence':ref}],actor_ref='python')
 else:
  assert [r['actor_ref'] for r in j.records()]==['python','typescript','actor']
	backup=Path(os.environ['JOURNAL_HANDOFF_ROOT']).parent/'go-backup.json'
	with FileJournal.restore_snapshot(backup.parent/'python-restored',backup.read_bytes(),schema=s,durability='process_crash') as restored:
	 assert restored.watermark=='3'
	 assert restored.meta['store_id']!=j.meta['store_id']
	 assert [r['events'] for r in restored.records()]==[r['events'] for r in j.records()]
	 print(json.dumps({'owner':'python-restored-go-backup','watermark':restored.watermark}))
  j.append([{'type':'CommandCompleted','graph_id':'g','revision':'1','command_digest':'sha256:'+'a'*64,'status':'noop'}],actor_ref='python-final')
 print(json.dumps({'owner':'python','watermark':j.watermark,'records':[r['record_hash'] for r in j.records()]}))`
	python = strings.ReplaceAll(python, "\t", "  ")
	run(py, "-c", python)
	node := os.Getenv("CONTEXTURE_JOURNAL_NODE")
	if node == "" {
		node = "node"
	}
	js := `import fs from 'node:fs'; const {FileJournal}=await import(process.env.CONTEXTURE_JOURNAL_TS); const schema=JSON.parse(fs.readFileSync(process.env.CONTEXTURE_MANAGED_SCHEMA)); const j=FileJournal.open(process.env.JOURNAL_HANDOFF_ROOT,{schema,durability:'process_crash'}); try { if(j.watermark!=='1')throw Error('wrong owner watermark'); const ref=j.putBlob(Buffer.from('typescript native evidence'),'text/plain');j.append([{type:'RecoveryActionRecorded',action_id:'typescript',action:'handoff',evidence:ref}],{actor_ref:'typescript'}); console.log(JSON.stringify({owner:'typescript',watermark:j.watermark,records:j.records().map(r=>r.record_hash)}));}finally{j.close();}`
	run(node, "--input-type=module", "-e", js)
	j := journalTestOpen(t, root, o)
	journalTestAppend(t, j, "go-handoff")
	records, e := j.Records("0")
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Go actual records: %s", journalBytes(recordsAsAny(records)))
	backup, e := j.ExportSnapshot()
	if e != nil {
		t.Fatal(e)
	}
	if !json.Valid(backup) {
		t.Fatal("invalid native backup")
	}
	if e = os.WriteFile(filepath.Join(filepath.Dir(root), "go-backup.json"), backup, 0600); e != nil {
		t.Fatal(e)
	}
	_ = j.Close()
	run(py, "-c", python)
	j = journalTestOpen(t, root, o)
	if wm, _ := j.Watermark(); wm != "4" {
		t.Fatal(wm)
	}
}
func recordsAsAny(rs []JournalObject) []any {
	a := make([]any, len(rs))
	for i, r := range rs {
		a[i] = r
	}
	return a
}

func TestManagedJournalStrictAndNoOverwrite(t *testing.T) {
	o := journalTestOptions(t)
	o.Durability = "strict"
	root := journalTestRoot(t)
	j := journalTestOpen(t, root, o)
	journalTestAppend(t, j, "strict")
	_ = j.Close()
	j = journalTestOpen(t, root, o)
	if wm, _ := j.Watermark(); wm != "1" {
		t.Fatal(wm)
	}
	_ = j.Close()
	o.Durability = "process_crash"
	_, e := OpenFileJournal(root, o)
	journalTestCode(t, e, "durability_unsupported")
	p := journalTestRoot(t)
	if e = os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	a, b := filepath.Join(p, "a"), filepath.Join(p, "b")
	if e = os.WriteFile(a, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(b, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = journalNoReplace(a, b); e == nil {
		t.Fatal("native primitive overwrote destination")
	}
	raw, e := os.ReadFile(b)
	if e != nil || string(raw) != "old" {
		t.Fatal("destination changed")
	}
	if !journalExists(a) {
		t.Fatal("source consumed on conflict")
	}
}

func TestManagedJournalAuxiliaryUncertainty(t *testing.T) {
	for _, kind := range []string{"blob", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			o := journalTestOptions(t)
			armed := false
			o.Fault = func(s string) error {
				if armed && s == kind+".renamed" {
					return errors.New("auxiliary uncertain")
				}
				return nil
			}
			j := journalTestOpen(t, journalTestRoot(t), o)
			journalTestAppend(t, j, "accepted")
			armed = true
			var e error
			if kind == "blob" {
				_, e = j.PutBlob([]byte("uncertain"), "")
			} else {
				_, e = j.Checkpoint()
			}
			if e == nil {
				t.Fatal("missing injected fault")
			}
			_, e = j.Append(journalTestEvent("next"), JournalAppendOptions{ActorRef: "actor"})
			journalTestCode(t, e, "commit_unknown")
			if wm, _ := j.Watermark(); wm != "1" {
				t.Fatal("auxiliary failure reversed commit")
			}
		})
	}
}

func TestManagedJournalBackupValidationAndProjectionFreeze(t *testing.T) {
	o := journalTestOptions(t)
	j := journalTestOpen(t, journalTestRoot(t), o)
	journalTestAppend(t, j, "backup")
	raw, e := j.ExportSnapshot()
	if e != nil {
		t.Fatal(e)
	}
	value, e := graph.DecodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	m := journalMap(value)
	m["backup_digest"] = "sha256:" + strings.Repeat("0", 64)
	dest := journalTestRoot(t)
	_, e = RestoreFileJournal(dest, journalBytes(m), o)
	journalTestCode(t, e, "backup_invalid")
	if journalExists(dest) {
		t.Fatal("invalid backup touched destination")
	}
	o.Projector = func([]JournalObject) (JournalObject, error) { return JournalObject{}, nil }
	f := journalTestOpen(t, journalTestRoot(t), o)
	journalTestAppend(t, f, "projection")
	_, e = f.Checkpoint()
	journalTestCode(t, e, "projection_invalid")
	_, e = f.PutBlob([]byte("must freeze"), "")
	journalTestCode(t, e, "commit_unknown")
	o = journalTestOptions(t)
	o.Projector = nil
	f = journalTestOpen(t, journalTestRoot(t), o)
	journalTestAppend(t, f, "no-projector")
	_, e = f.Checkpoint()
	journalTestCode(t, e, "projection_required")
}
