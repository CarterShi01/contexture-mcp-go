package server

// Native single-owner candidate FileJournal. This adapter stores admitted facts;
// it does not perform authorization, graph reduction, or release side effects.
import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/google/jsonschema-go/jsonschema"
)

type JournalObject = map[string]any
type JournalProjector func([]JournalObject) (JournalObject, error)

type JournalOptions struct {
	Schema     any
	Durability string
	Projector  JournalProjector
	// Fault runs synchronously under the owner mutex. It must not reenter.
	Fault func(string) error
}
type JournalAppendOptions struct {
	ActorRef                               string
	CommandReceipt                         JournalObject
	CorrelationID, EventBatchID, WrittenAt string
}
type JournalError struct{ Diagnostic, Message string }

func (e *JournalError) Error() string  { return e.Message }
func (e *JournalError) Code() string   { return e.Diagnostic }
func journalFail(code, message string) { panic(&JournalError{code, message}) }
func journalMust(err error) {
	if err != nil {
		panic(err)
	}
}
func journalCatch(err *error) {
	if p := recover(); p != nil {
		if e, ok := p.(error); ok {
			*err = e
		} else {
			panic(p)
		}
	}
}
func journalUnknown() {
	journalFail("commit_unknown", "Publication may have happened; close and recover under lock")
}
func journalCopy(v any) any { f, e := graph.FreezeJSON(v); journalMust(e); return f.JSON() }
func journalMap(v any) JournalObject {
	m, ok := v.(map[string]any)
	if !ok {
		journalFail("schema_invalid", "Expected object")
	}
	return m
}
func journalArray(v any) []any {
	a, ok := v.([]any)
	if !ok {
		journalFail("schema_invalid", "Expected array")
	}
	return a
}
func journalBytes(v any) []byte { b, e := graph.CanonicalJSON(v); journalMust(e); return b }
func journalDigest(domain string, v any) string {
	d, e := graph.CanonicalDigest(v, domain)
	journalMust(e)
	return d
}
func journalByteDigest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func journalEqual(a, b any) bool { return bytes.Equal(journalBytes(a), journalBytes(b)) }
func journalUUID() string {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	journalMust(e)
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func journalNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func journalDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

var journalHex = regexp.MustCompile(`^[0-9a-f]{64}$`)
var journalPad = regexp.MustCompile(`^[0-9]{20}$`)
var journalProjectionKeys = []string{"graphs", "desired_releases", "release_operations", "prepare_attempt_history", "observations_history", "receipts_index", "projection_versions", "blob_refs"}
var journalDirectories = []string{"records", "blobs", "checkpoints", "hints", "indexes", "tmp", "quarantine"}

// FileJournal serializes every operation, and owns a stable OS lock until Close.
// Callback reentry is forbidden. Returned values are recursively detached.
type FileJournal struct {
	mu               sync.Mutex
	root, durability string
	options          JournalOptions
	lock             *os.File
	closed, unknown  bool
	meta             JournalObject
	accepted         []JournalObject
	receipts         map[[2]string]JournalObject
	batches          map[any]bool
	validators       map[string]*jsonschema.Resolved
	recovery         []JournalObject
	// Used only by the detached backup verifier, never a live owner.
	backupBlobs map[string][]byte
	// Logical graph custody is local to this physical Journal instance.
	graphOwner *FileJournalGraphOwner
}

func newJournal(root string, o JournalOptions) *FileJournal {
	for _, p := range strings.Split(filepath.ToSlash(root), "/") {
		if p == ".." {
			journalFail("unsafe_path", "Dot traversal forbidden")
		}
	}
	abs, e := filepath.Abs(root)
	journalMust(e)
	j := &FileJournal{root: abs, durability: journalDefault(o.Durability, "strict"), options: o, receipts: map[[2]string]JournalObject{}, batches: map[any]bool{}, validators: map[string]*jsonschema.Resolved{}, accepted: []JournalObject{}}
	schema := journalMap(journalCopy(o.Schema))
	var inspect func(any)
	inspect = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, c := range v {
				// RE2 has no lookahead or ECMAScript Unicode escape syntax.
				// Translate only the registered absolute-end idiom and fixed
				// scalar escapes; this preserves semantics, not schema assets.
				if k == "pattern" {
					pattern, ok := c.(string)
					if !ok {
						journalFail("schema_invalid", "Invalid schema pattern")
					}
					pattern = strings.ReplaceAll(pattern, `(?![\s\S])`, `\z`)
					pattern = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`).ReplaceAllString(pattern, `\x{$1}`)
					v[k] = pattern
				}
				if k == "$ref" {
					s, ok := c.(string)
					if !ok || !strings.HasPrefix(s, "#/$defs/") {
						journalFail("schema_invalid", "Only local registered schema references allowed")
					}
				}
				if k == "$dynamicRef" || k == "$recursiveRef" {
					journalFail("schema_invalid", "Dynamic references unsupported")
				}
				inspect(c)
			}
		case []any:
			for _, c := range v {
				inspect(c)
			}
		}
	}
	inspect(schema)
	for _, n := range []string{"FileJournalMeta", "CommitRecord", "BlobRef", "Checkpoint", "Event", "CommandReceipt"} {
		var s jsonschema.Schema
		journalMust(json.Unmarshal(journalBytes(JournalObject{"$defs": schema["$defs"], "$ref": "#/$defs/" + n}), &s))
		r, e := s.Resolve(nil)
		if e != nil {
			journalFail("schema_invalid", "Invalid local managed schema")
		}
		j.validators[n] = r
	}
	return j
}
func OpenFileJournal(root string, o JournalOptions) (j *FileJournal, err error) {
	return openFileJournal(root, o, nil)
}
func openFileJournal(root string, o JournalOptions, restore []byte) (j *FileJournal, err error) {
	defer journalCatch(&err)
	j = newJournal(root, o)
	defer func() {
		if p := recover(); p != nil {
			if j.lock != nil {
				_ = j.lock.Close()
			}
			j.closed = true
			panic(p)
		}
	}()
	journalMust(journalPlatform(j.root, j.durability))
	j.mkdir(j.root)
	p := filepath.Join(j.root, "writer.lock")
	j.checkPath(p, false)
	f, e := journalOpen(p, os.O_CREATE|os.O_RDWR, 0600)
	journalMust(e)
	if e = journalLock(f); e != nil {
		_ = f.Close()
		panic(e)
	}
	j.lock = f
	j.checkPath(p, false)
	j.syncDir(j.root)
	for _, n := range journalDirectories {
		j.mkdir(filepath.Join(j.root, n))
	}
	p = filepath.Join(j.root, "META.json")
	if journalExists(p) {
		j.meta = j.readJSON(p)
		j.validate("FileJournalMeta", j.meta)
		if j.meta["durability"] != j.durability {
			journalFail("durability_unsupported", "Persisted durability must match")
		}
		if restore != nil {
			journalFail("restore_conflict", "Restore requires a fresh store")
		}
	} else {
		for _, n := range []string{"records", "blobs", "checkpoints"} {
			if len(j.entries(filepath.Join(j.root, n))) > 0 {
				journalFail("corruption", "Missing META with durable assets")
			}
		}
		j.meta = JournalObject{"schema": "contexture.file-journal/v1", "store_id": journalUUID(), "canonical_profile": graph.CanonicalProfile, "durability": j.durability, "created_at": journalNow()}
		if restore != nil {
			j.meta["restored_from"] = j.putBlob(restore, "application/json")
		}
		j.validate("FileJournalMeta", j.meta)
		j.publish(p, journalBytes(j.meta), "meta")
	}
	j.recover()
	if restore == nil {
		j.verifyRestoreOrigin()
	}
	return j, nil
}
func (j *FileJournal) validate(n string, v any) {
	if e := j.validators[n].Validate(v); e != nil {
		journalFail("schema_invalid", "Closed "+n+" contract rejected")
	}
}
func (j *FileJournal) guard(write bool) {
	if j.closed || j.lock == nil {
		journalFail("closed", "Store owner is closed")
	}
	if write && j.unknown {
		journalUnknown()
	}
}

// AssertWritable is a synchronous I/O-free readiness check for shared owners.
// Unknown publication, closed custody and callback/concurrent reentry refuse.
func (j *FileJournal) AssertWritable() (err error) {
	if j == nil {
		return &JournalError{"closed", "Store owner is absent"}
	}
	if !j.mu.TryLock() {
		return &JournalError{"resource_busy", "Store owner is busy or reentered"}
	}
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(true)
	return nil
}
func (j *FileJournal) hit(s string) {
	if j.options.Fault != nil {
		journalMust(j.options.Fault(s))
	}
}
func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if j.lock != nil {
		e := j.lock.Close()
		j.lock = nil
		return e
	}
	return nil
}
func (j *FileJournal) Meta() JournalObject {
	j.mu.Lock()
	defer j.mu.Unlock()
	return journalMap(journalCopy(j.meta))
}
func (j *FileJournal) Watermark() (v string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	return strconv.Itoa(len(j.accepted)), nil
}
func (j *FileJournal) RecoveryActions() []JournalObject {
	j.mu.Lock()
	defer j.mu.Unlock()
	return journalRecordsCopy(j.recovery)
}
func journalRecordsCopy(rs []JournalObject) []JournalObject {
	out := make([]JournalObject, len(rs))
	for i, r := range rs {
		out[i] = journalMap(journalCopy(r))
	}
	return out
}
func journalExists(p string) bool {
	_, e := os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		return false
	}
	journalMust(e)
	return true
}
func (j *FileJournal) checkPath(p string, dir bool) {
	if p != j.root && !strings.HasPrefix(p, j.root+string(os.PathSeparator)) && !strings.HasPrefix(j.root, p+string(os.PathSeparator)) && p != string(os.PathSeparator) {
		journalFail("unsafe_path", "Path escaped store boundary")
	}
	var paths []string
	for q := p; ; q = filepath.Dir(q) {
		paths = append(paths, q)
		if filepath.Dir(q) == q {
			break
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		q := paths[i]
		s, e := os.Lstat(q)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		journalMust(e)
		if s.Mode()&os.ModeSymlink != 0 {
			journalFail("unsafe_path", "Symlinks forbidden")
		}
		if q == p && dir && !s.IsDir() {
			journalFail("unsafe_path", "Expected directory")
		}
		if q == j.root || strings.HasPrefix(q, j.root+string(os.PathSeparator)) {
			journalMust(journalPrivate(s))
			if !s.IsDir() && !s.Mode().IsRegular() {
				journalFail("unsafe_path", "Expected regular file")
			}
		}
	}
}
func (j *FileJournal) mkdir(p string) {
	j.checkPath(p, true)
	if journalExists(p) {
		return
	}
	if !journalExists(filepath.Dir(p)) {
		j.mkdir(filepath.Dir(p))
	}
	e := os.Mkdir(p, 0700)
	if errors.Is(e, os.ErrExist) {
		j.checkPath(p, true)
		return
	}
	journalMust(e)
	j.hit("directory.created")
	j.syncDir(filepath.Dir(p))
	j.hit("directory.synced")
}
func (j *FileJournal) syncDir(p string) {
	j.checkPath(p, true)
	if j.durability == "strict" {
		f, e := journalOpenDir(p)
		journalMust(e)
		defer f.Close()
		journalMust(f.Sync())
	}
}
func (j *FileJournal) confirm(p string) {
	j.checkPath(p, false)
	f, e := journalOpen(p, os.O_RDONLY, 0)
	journalMust(e)
	e = f.Sync()
	_ = f.Close()
	journalMust(e)
	j.syncDir(filepath.Dir(p))
	if filepath.Dir(p) != j.root {
		j.syncDir(filepath.Dir(filepath.Dir(p)))
	}
}
func (j *FileJournal) readBytes(p string) []byte {
	j.checkPath(p, false)
	f, e := journalOpen(p, os.O_RDONLY, 0)
	journalMust(e)
	defer f.Close()
	s, e := f.Stat()
	journalMust(e)
	journalMust(journalPrivate(s))
	if !s.Mode().IsRegular() {
		journalFail("unsafe_path", "Expected regular file")
	}
	b, e := io.ReadAll(f)
	journalMust(e)
	return b
}
func (j *FileJournal) readJSON(p string) JournalObject {
	b := j.readBytes(p)
	v, e := graph.DecodeJSON(b)
	if e != nil {
		journalFail("corruption", "Invalid canonical JSON")
	}
	m, ok := v.(map[string]any)
	if !ok || !bytes.Equal(journalBytes(v), b) {
		journalFail("corruption", "Asset is not a canonical object")
	}
	return m
}
func (j *FileJournal) entries(p string) []os.DirEntry {
	j.checkPath(p, true)
	a, e := os.ReadDir(p)
	journalMust(e)
	return a
}
func (j *FileJournal) publish(final string, b []byte, kind string) {
	j.mkdir(filepath.Dir(final))
	j.syncDir(filepath.Dir(filepath.Dir(final)))
	j.checkPath(final, false)
	temp := filepath.Join(j.root, "tmp", journalUUID()+".tmp")
	j.checkPath(temp, false)
	f, e := journalOpen(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	journalMust(e)
	attempted := false
	defer func() {
		if p := recover(); p != nil {
			if attempted {
				j.unknown = true
				if kind == "record" {
					journalUnknown()
				}
			}
			panic(p)
		}
	}()
	func() {
		defer f.Close()
		for offset := 0; offset < len(b); {
			n, e := f.Write(b[offset:])
			journalMust(e)
			if n <= 0 || n > len(b)-offset {
				journalFail("write_failed", "Short write made no progress")
			}
			offset += n
		}
		j.hit(kind + ".temp_written")
		journalMust(f.Sync())
		j.hit(kind + ".file_synced")
	}()
	if journalExists(final) {
		journalFail("corruption", "Immutable destination exists")
	}
	j.hit(kind + ".before_rename")
	attempted = true
	journalMust(journalNoReplace(temp, final))
	j.hit(kind + ".renamed")
	j.syncDir(filepath.Dir(final))
	j.syncDir(filepath.Dir(temp))
	j.hit(kind + ".directory_synced")
}
func (j *FileJournal) recordPath(n *big.Int) string {
	if n.Sign() <= 0 || len(n.String()) > 20 {
		journalFail("limit_exceeded", "Journal counter exhausted")
	}
	b := new(big.Int).Sub(n, big.NewInt(1))
	b.Div(b, big.NewInt(10000))
	return filepath.Join(j.root, "records", fmt.Sprintf("%020s", b.String()), fmt.Sprintf("%020s.json", n.String()))
}
func (j *FileJournal) blobPath(d any) string {
	s, ok := d.(string)
	if !ok || !strings.HasPrefix(s, "sha256:") || !journalHex.MatchString(s[7:]) {
		journalFail("unsafe_path", "Invalid blob selector")
	}
	return filepath.Join(j.root, "blobs", "sha256", s[7:9], s[7:]+".blob")
}
func journalRefs(v any) []JournalObject {
	refs := []JournalObject{}
	switch x := v.(type) {
	case []JournalObject:
		for _, c := range x {
			refs = append(refs, journalRefs(c)...)
		}
	case []any:
		for _, c := range x {
			refs = append(refs, journalRefs(c)...)
		}
	case map[string]any:
		switch x["schema"] {
		case "contexture.journal-record/v1":
			refs = append(refs, journalRefs(x["command_receipt"])...)
			for _, e := range journalArray(x["events"]) {
				m := journalMap(e)
				if m["type"] == "RecoveryActionRecorded" {
					refs = append(refs, journalMap(m["evidence"]))
				}
			}
		case "contexture.command-receipt/v1":
			if b, ok := journalMap(x["operation_result"])["blob"]; ok {
				refs = append(refs, journalMap(b))
			}
		case "contexture.checkpoint/v1":
			for _, r := range journalArray(x["blob_refs"]) {
				refs = append(refs, journalMap(r))
			}
			refs = append(refs, journalRefs(x["receipts_index"])...)
		}
	}
	return refs
}
func (j *FileJournal) putBlob(b []byte, media string) JournalObject {
	if media == "application/json" {
		v, e := graph.DecodeJSON(b)
		journalMust(e)
		if !bytes.Equal(journalBytes(v), b) {
			journalFail("schema_invalid", "JSON blob must be canonical")
		}
	}
	r := JournalObject{"digest": journalByteDigest(b), "size": strconv.Itoa(len(b)), "media_type": media}
	j.validate("BlobRef", r)
	p := j.blobPath(r["digest"])
	if journalExists(p) {
		if !bytes.Equal(j.getBlob(r), b) {
			journalFail("corruption", "Blob conflict")
		}
		j.confirm(p)
	} else {
		j.publish(p, b, "blob")
	}
	return r
}
func (j *FileJournal) getBlob(r JournalObject) []byte {
	j.validate("BlobRef", r)
	var b []byte
	if j.backupBlobs != nil {
		var ok bool
		b, ok = j.backupBlobs[r["digest"].(string)]
		if !ok {
			journalFail("backup_invalid", "Missing required backup blob")
		}
	} else {
		p := j.blobPath(r["digest"])
		if !journalExists(p) {
			journalFail("history_unavailable", "Required retained blob missing")
		}
		b = j.readBytes(p)
	}
	if r["size"] != strconv.Itoa(len(b)) || r["digest"] != journalByteDigest(b) {
		journalFail("corruption", "Blob hash or size mismatch")
	}
	if r["media_type"] == "application/json" {
		v, e := graph.DecodeJSON(b)
		if e != nil || !bytes.Equal(journalBytes(v), b) {
			journalFail("corruption", "Noncanonical JSON blob")
		}
	}
	return bytes.Clone(b)
}
func (j *FileJournal) PutBlob(b []byte, media string) (r JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(true)
	return journalMap(journalCopy(j.putBlob(bytes.Clone(b), journalDefault(media, "application/octet-stream")))), nil
}
func (j *FileJournal) GetBlob(r JournalObject) (b []byte, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	return j.getBlob(journalMap(journalCopy(r))), nil
}

func (j *FileJournal) verifyRecord(r JournalObject, n int, previous any) {
	j.validate("CommitRecord", r)
	if r["store_id"] != j.meta["store_id"] || r["journal_seq"] != strconv.Itoa(n) || r["prev_record_hash"] != previous {
		journalFail("corruption", "Record store, sequence or previous hash mismatch")
	}
	sub := journalMap(journalCopy(r))
	delete(sub, "record_hash")
	payload := JournalObject{"events": r["events"]}
	if v, ok := r["command_receipt"]; ok {
		payload["command_receipt"] = v
	}
	if r["payload_digest"] != journalDigest("contexture.journal-payload/v1", payload) || r["record_hash"] != journalDigest("contexture.journal-record/v1", sub) {
		journalFail("corruption", "Record or payload digest mismatch")
	}
	if v, ok := r["command_receipt"]; ok {
		c := journalMap(v)
		if c["journal_seq"] != strconv.Itoa(n) || c["actor_ref"] != r["actor_ref"] || c["durability"] != j.durability {
			journalFail("corruption", "Receipt actor, sequence or durability mismatch")
		}
		var last JournalObject
		for _, v := range journalArray(r["events"]) {
			e := journalMap(v)
			if e["type"] == "GraphChangeCommitted" || e["type"] == "CommandCompleted" {
				last = e
			}
		}
		if last != nil {
			rev := last["revision"]
			if result, ok := last["result_revision"]; ok {
				rev = result
			}
			status := "committed"
			if last["type"] == "CommandCompleted" {
				status = "noop"
			}
			if c["graph_id"] != last["graph_id"] || c["revision"] != rev || c["request_digest"] != last["command_digest"] || c["status"] != status {
				journalFail("corruption", "Receipt differs from accepted graph event")
			}
		}
	}
	for _, ref := range journalRefs(r) {
		j.getBlob(ref)
	}
}
func (j *FileJournal) recover() {
	journalMust(filepath.WalkDir(j.root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		j.checkPath(p, d.IsDir())
		return nil
	}))
	if r, ok := j.meta["restored_from"]; ok {
		j.getBlob(journalMap(r))
	}
	paths := []string{}
	for _, bucket := range j.entries(filepath.Join(j.root, "records")) {
		if !bucket.IsDir() || !journalPad.MatchString(bucket.Name()) {
			journalFail("corruption", "Unexpected record bucket")
		}
		dir := filepath.Join(j.root, "records", bucket.Name())
		for _, file := range j.entries(dir) {
			name := file.Name()
			if !strings.HasSuffix(name, ".json") || !journalPad.MatchString(strings.TrimSuffix(name, ".json")) || file.IsDir() {
				journalFail("corruption", "Unexpected committed record filename")
			}
			n, ok := new(big.Int).SetString(strings.TrimSuffix(name, ".json"), 10)
			if !ok || filepath.Join(dir, name) != j.recordPath(n) {
				journalFail("corruption", "Wrong record bucket")
			}
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)
	var previous any
	batches := map[any]bool{}
	j.batches = batches
	for i, p := range paths {
		if filepath.Base(p) != fmt.Sprintf("%020d.json", i+1) {
			journalFail("corruption", "Missing or duplicate committed record")
		}
		r := j.readJSON(p)
		j.verifyRecord(r, i+1, previous)
		if batches[r["event_batch_id"]] {
			journalFail("corruption", "Duplicate event batch")
		}
		batches[r["event_batch_id"]] = true
		if c, ok := r["command_receipt"]; ok {
			receipt := journalMap(c)
			key := journalReceiptKey(receipt)
			if _, exists := j.receipts[key]; exists {
				journalFail("corruption", "Duplicate accepted receipt key")
			}
			j.receipts[key] = receipt
		}
		j.confirm(p)
		j.accepted = append(j.accepted, r)
		previous = r["record_hash"]
	}
	for _, f := range j.entries(filepath.Join(j.root, "tmp")) {
		if f.IsDir() {
			journalFail("corruption", "Unexpected temporary directory")
		}
		source := filepath.Join(j.root, "tmp", f.Name())
		target := filepath.Join(j.root, "quarantine", journalUUID()+".artifact")
		evidence := JournalObject{"schema": "contexture.quarantine/v1", "source": f.Name(), "artifact": filepath.Base(target), "digest": journalByteDigest(j.readBytes(source)), "action": "unpublished-temp"}
		j.publish(strings.TrimSuffix(target, ".artifact")+".json", journalBytes(evidence), "recovery")
		journalMust(journalNoReplace(source, target))
		j.syncDir(filepath.Dir(target))
		j.syncDir(filepath.Dir(source))
		j.recovery = append(j.recovery, evidence)
	}
}
func journalReceiptKey(r JournalObject) [2]string {
	return [2]string{r["actor_ref"].(string), r["idempotency_key"].(string)}
}
func (j *FileJournal) verifyRestoreOrigin() {
	r, ok := j.meta["restored_from"]
	if !ok {
		return
	}
	v, e := graph.DecodeJSON(j.getBlob(journalMap(r)))
	journalMust(e)
	m := journalMap(v)
	if m["schema"] != "contexture.file-journal-backup/v1" {
		journalFail("corruption", "Restore origin is not a backup")
	}
	source := journalArray(m["records"])
	if len(j.accepted) < len(source) {
		journalFail("restore_incomplete", "Interrupted restore must remain isolated")
	}
	for i, v := range source {
		expected := journalMap(v)
		for _, key := range []string{"events", "command_receipt", "event_batch_id", "actor_ref", "written_at", "correlation_id", "journal_seq"} {
			if !journalEqual(expected[key], j.accepted[i][key]) {
				journalFail("corruption", "Restored records differ from backup provenance")
			}
		}
	}
}
func (j *FileJournal) Records(after string) (out []JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	n, e := graph.ParseCounter(journalDefault(after, "0"))
	journalMust(e)
	if n.Cmp(big.NewInt(int64(len(j.accepted)))) > 0 {
		journalFail("schema_invalid", "Cursor exceeds durable watermark")
	}
	return journalRecordsCopy(j.accepted[int(n.Int64()):]), nil
}
func (j *FileJournal) GetReceipt(actor, key string) (r JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	if v := j.receipts[[2]string{actor, key}]; v != nil {
		return journalMap(journalCopy(v)), nil
	}
	return nil, nil
}
func (j *FileJournal) Append(events any, o JournalAppendOptions) (r JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(true)
	return j.append(events, o), nil
}
func (j *FileJournal) append(events any, o JournalAppendOptions) JournalObject {
	n := len(j.accepted) + 1
	var receipt JournalObject
	if o.CommandReceipt != nil {
		receipt = journalMap(journalCopy(o.CommandReceipt))
		j.validate("CommandReceipt", receipt)
		if receipt["actor_ref"] != o.ActorRef {
			journalFail("schema_invalid", "Receipt and record actor must agree")
		}
		if old := j.receipts[journalReceiptKey(receipt)]; old != nil {
			if old["request_digest"] != receipt["request_digest"] {
				journalFail("idempotency_conflict", "Actor/key accepted another request")
			}
			seq, e := strconv.Atoi(old["journal_seq"].(string))
			journalMust(e)
			return journalMap(journalCopy(j.accepted[seq-1]))
		}
	}
	var previous any
	if len(j.accepted) > 0 {
		previous = j.accepted[len(j.accepted)-1]["record_hash"]
	}
	r := JournalObject{"schema": "contexture.journal-record/v1", "store_id": j.meta["store_id"], "journal_seq": strconv.Itoa(n), "event_batch_id": journalDefault(o.EventBatchID, journalUUID()), "prev_record_hash": previous, "events": journalCopy(events), "written_at": journalDefault(o.WrittenAt, journalNow()), "actor_ref": o.ActorRef, "correlation_id": journalDefault(o.CorrelationID, journalUUID())}
	payload := JournalObject{"events": r["events"]}
	if receipt != nil {
		r["command_receipt"] = receipt
		payload["command_receipt"] = receipt
	}
	r["payload_digest"] = journalDigest("contexture.journal-payload/v1", payload)
	r["record_hash"] = journalDigest("contexture.journal-record/v1", r)
	j.verifyRecord(r, n, previous)
	if j.batches[r["event_batch_id"]] {
		journalFail("schema_invalid", "Batch identity already accepted")
	}
	for _, ref := range journalRefs(r) {
		j.confirm(j.blobPath(ref["digest"]))
	}
	returned := journalMap(journalCopy(r))
	j.publish(j.recordPath(big.NewInt(int64(n))), journalBytes(r), "record")
	defer func() {
		if p := recover(); p != nil {
			j.unknown = true
			delete(j.batches, r["event_batch_id"])
			if receipt != nil {
				delete(j.receipts, journalReceiptKey(receipt))
			}
			j.accepted = j.accepted[:n-1]
			journalUnknown()
		}
	}()
	j.batches[r["event_batch_id"]] = true
	j.hit("record.batch_indexed")
	if receipt != nil {
		j.receipts[journalReceiptKey(receipt)] = receipt
		j.hit("record.receipt_indexed")
	}
	j.accepted = append(j.accepted, r)
	j.hit("record.memory_indexed")
	return returned
}
func (j *FileJournal) project(records []JournalObject) JournalObject {
	if j.options.Projector == nil {
		journalFail("projection_required", "Complete independent application projection port required")
	}
	defer func() {
		if p := recover(); p != nil {
			j.unknown = true
			panic(p)
		}
	}()
	v, e := j.options.Projector(journalRecordsCopy(records))
	journalMust(e)
	m := journalMap(journalCopy(v))
	if len(m) != len(journalProjectionKeys) {
		journalFail("projection_invalid", "Every complete projection required")
	}
	for _, k := range journalProjectionKeys {
		if _, ok := m[k]; !ok {
			journalFail("projection_invalid", "Missing projection")
		}
	}
	return m
}
func journalOrdered(items any, key func(JournalObject) []string) {
	var previous []string
	for _, v := range journalArray(items) {
		next := key(journalMap(v))
		if previous != nil && journalTupleCompare(previous, next) >= 0 {
			journalFail("checkpoint_invalid", "Checkpoint ordering or uniqueness violation")
		}
		previous = next
	}
}
func journalTupleCompare(a, b []string) int {
	for i := range a {
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
func (j *FileJournal) checkCheckpoint(v JournalObject) {
	j.validate("Checkpoint", v)
	subject := journalMap(journalCopy(v))
	delete(subject, "checkpoint_digest")
	if v["checkpoint_digest"] != journalDigest("contexture.checkpoint/v1", subject) {
		journalFail("checkpoint_invalid", "Checkpoint subject digest mismatch")
	}
	n, e := graph.ParseCounter(v["last_seq"].(string))
	journalMust(e)
	if v["store_id"] != j.meta["store_id"] || n.Sign() <= 0 || n.Cmp(big.NewInt(int64(len(j.accepted)))) > 0 {
		journalFail("checkpoint_invalid", "Checkpoint anchor mismatch")
	}
	count := int(n.Int64())
	if v["last_record_hash"] != j.accepted[count-1]["record_hash"] {
		journalFail("checkpoint_invalid", "Checkpoint record anchor mismatch")
	}
	p := JournalObject{}
	for _, k := range journalProjectionKeys {
		p[k] = v[k]
	}
	journalOrdered(p["graphs"], func(g JournalObject) []string { return []string{journalMap(g["definition"])["graph_id"].(string)} })
	for _, g := range journalArray(p["graphs"]) {
		m := journalMap(g)
		journalOrdered(m["tombstones"], func(t JournalObject) []string { return []string{t["node_id"].(string)} })
		if m["definition_digest"] != journalDigest("contexture.graph/v1", m["definition"]) {
			journalFail("checkpoint_invalid", "Graph digest mismatch")
		}
	}
	journalOrdered(p["desired_releases"], func(r JournalObject) []string {
		q := journalMap(r["request"])
		return []string{q["graph_id"].(string), q["target_id"].(string)}
	})
	journalOrdered(p["receipts_index"], func(r JournalObject) []string {
		return []string{r["actor_ref"].(string), r["idempotency_key"].(string)}
	})
	journalOrdered(p["blob_refs"], func(r JournalObject) []string { return []string{r["digest"].(string), r["media_type"].(string)} })
	receipts := []any{}
	for _, r := range j.accepted[:count] {
		if c, ok := r["command_receipt"]; ok {
			receipts = append(receipts, c)
		}
	}
	sort.Slice(receipts, func(a, b int) bool {
		x, y := journalReceiptKey(journalMap(receipts[a])), journalReceiptKey(journalMap(receipts[b]))
		return journalTupleCompare(x[:], y[:]) < 0
	})
	if !journalEqual(p["receipts_index"], receipts) {
		journalFail("checkpoint_invalid", "Checkpoint lost accepted receipt results")
	}
	if !journalEqual(p, j.project(j.accepted[:count])) {
		journalFail("checkpoint_invalid", "Complete projections differ from full replay")
	}
	refs := map[[2]string]bool{}
	for _, r := range journalRefs(j.accepted[:count]) {
		refs[[2]string{r["digest"].(string), r["media_type"].(string)}] = true
	}
	for _, v := range journalArray(p["blob_refs"]) {
		r := journalMap(v)
		j.getBlob(r)
		delete(refs, [2]string{r["digest"].(string), r["media_type"].(string)})
	}
	if len(refs) > 0 {
		journalFail("checkpoint_invalid", "Checkpoint omitted retained blob closure")
	}
}
func (j *FileJournal) checkpoint() JournalObject {
	if len(j.accepted) == 0 {
		journalFail("checkpoint_invalid", "Checkpoint requires positive watermark")
	}
	v := j.project(j.accepted)
	v["schema"] = "contexture.checkpoint/v1"
	v["store_id"] = j.meta["store_id"]
	v["last_seq"] = strconv.Itoa(len(j.accepted))
	v["last_record_hash"] = j.accepted[len(j.accepted)-1]["record_hash"]
	v["checkpoint_digest"] = journalDigest("contexture.checkpoint/v1", v)
	j.checkCheckpoint(v)
	p := filepath.Join(j.root, "checkpoints", fmt.Sprintf("%020d", len(j.accepted)), v["checkpoint_digest"].(string)[7:]+".json")
	if journalExists(p) {
		if !journalEqual(j.readJSON(p), v) {
			journalFail("corruption", "Immutable checkpoint conflict")
		}
		j.confirm(p)
	} else {
		j.publish(p, journalBytes(v), "checkpoint")
	}
	return v
}
func (j *FileJournal) Checkpoint() (v JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(true)
	return journalMap(journalCopy(j.checkpoint())), nil
}
func (j *FileJournal) findCheckpoint() JournalObject {
	paths := []string{}
	for _, b := range j.entries(filepath.Join(j.root, "checkpoints")) {
		dir := filepath.Join(j.root, "checkpoints", b.Name())
		j.checkPath(dir, true)
		if !journalPad.MatchString(b.Name()) {
			continue
		}
		for _, f := range j.entries(dir) {
			if strings.HasSuffix(f.Name(), ".json") && journalHex.MatchString(strings.TrimSuffix(f.Name(), ".json")) {
				paths = append(paths, filepath.Join(dir, f.Name()))
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, p := range paths {
		v, e := func() (v JournalObject, err error) {
			defer journalCatch(&err)
			v = j.readJSON(p)
			count, ok := new(big.Int).SetString(filepath.Base(filepath.Dir(p)), 10)
			if !ok || v["last_seq"] != count.String() || v["checkpoint_digest"] != "sha256:"+strings.TrimSuffix(filepath.Base(p), ".json") {
				return nil, nil
			}
			j.checkCheckpoint(v)
			return v, nil
		}()
		if e != nil {
			var je *JournalError
			if errors.As(e, &je) && (je.Code() == "corruption" || je.Code() == "schema_invalid" || je.Code() == "checkpoint_invalid") {
				continue
			}
			panic(e)
		}
		if v != nil {
			return v
		}
	}
	return nil
}
func (j *FileJournal) ReadCheckpoint() (v JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	v = j.findCheckpoint()
	if v != nil {
		v = journalMap(journalCopy(v))
	}
	return v, nil
}
func (j *FileJournal) Projections() (v JournalObject, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(false)
	return j.project(j.accepted), nil
}
func (j *FileJournal) ExportSnapshot() (b []byte, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer journalCatch(&err)
	j.guard(true)
	refs := journalRefs(j.accepted)
	if r, ok := j.meta["restored_from"]; ok {
		refs = append(refs, journalMap(r))
	}
	var checkpoint any
	if j.options.Projector != nil {
		if c := j.findCheckpoint(); c != nil {
			checkpoint = c
			refs = append(refs, journalRefs(c)...)
		}
	}
	blobs := JournalObject{}
	for _, r := range refs {
		blobs[r["digest"].(string)] = hex.EncodeToString(j.getBlob(r))
	}
	records := make([]any, len(j.accepted))
	for i, r := range j.accepted {
		records[i] = r
	}
	v := JournalObject{"schema": "contexture.file-journal-backup/v1", "meta": j.meta, "records": records, "blobs": blobs, "checkpoint": checkpoint}
	v["backup_digest"] = journalDigest("contexture.file-journal-backup/v1", v)
	return journalBytes(v), nil
}

// verifyJournalSnapshot is shared by restore and read-only offline inspection.
// It owns no file descriptor and performs no filesystem reads or writes.
func verifyJournalSnapshot(root string, snapshot []byte, o JournalOptions) *FileJournal {
	return verifyJournalSnapshotValue(root, decodeJournalSnapshot(snapshot), o)
}
func decodeJournalSnapshot(snapshot []byte) JournalObject {
	raw, e := graph.DecodeJSON(snapshot)
	journalMust(e)
	v := journalMap(raw)
	if len(v) != 6 || v["schema"] != "contexture.file-journal-backup/v1" || !bytes.Equal(journalBytes(v), snapshot) {
		journalFail("backup_invalid", "Invalid closed canonical backup envelope")
	}
	for _, k := range []string{"schema", "meta", "records", "blobs", "checkpoint", "backup_digest"} {
		if _, ok := v[k]; !ok {
			journalFail("backup_invalid", "Missing backup member")
		}
	}
	sub := journalMap(journalCopy(v))
	delete(sub, "backup_digest")
	if v["backup_digest"] != journalDigest("contexture.file-journal-backup/v1", sub) {
		journalFail("backup_invalid", "Backup digest mismatch")
	}
	return v
}
func verifyJournalSnapshotValue(root string, v JournalObject, o JournalOptions) *FileJournal {
	verifier := newJournal(root, o)
	verifier.meta = journalMap(v["meta"])
	verifier.validate("FileJournalMeta", verifier.meta)
	if verifier.meta["durability"] != verifier.durability {
		journalFail("durability_unsupported", "Restore cannot relabel durability")
	}
	verifier.backupBlobs = map[string][]byte{}
	for digest, encoded := range journalMap(v["blobs"]) {
		s, ok := encoded.(string)
		if !ok {
			journalFail("backup_invalid", "Invalid blob encoding")
		}
		b, e := hex.DecodeString(s)
		if e != nil || hex.EncodeToString(b) != s || journalByteDigest(b) != digest {
			journalFail("backup_invalid", "Backup blob digest mismatch")
		}
		verifier.backupBlobs[digest] = b
	}
	var previous any
	batches := map[any]bool{}
	keys := map[[2]string]bool{}
	for i, item := range journalArray(v["records"]) {
		r := journalMap(item)
		verifier.verifyRecord(r, i+1, previous)
		if batches[r["event_batch_id"]] {
			journalFail("backup_invalid", "Duplicate backup batch")
		}
		batches[r["event_batch_id"]] = true
		if c, ok := r["command_receipt"]; ok {
			k := journalReceiptKey(journalMap(c))
			if keys[k] {
				journalFail("backup_invalid", "Duplicate backup receipt")
			}
			keys[k] = true
		}
		verifier.accepted = append(verifier.accepted, r)
		previous = r["record_hash"]
	}
	if r, ok := verifier.meta["restored_from"]; ok {
		verifier.getBlob(journalMap(r))
	}
	if v["checkpoint"] != nil {
		verifier.checkCheckpoint(journalMap(v["checkpoint"]))
	}
	return verifier
}
func RestoreFileJournal(root string, snapshot []byte, o JournalOptions) (j *FileJournal, err error) {
	defer journalCatch(&err)
	value := decodeJournalSnapshot(snapshot)
	if journalExists(root) {
		journalFail("restore_conflict", "Restore destination must not exist")
	}
	verifier := verifyJournalSnapshotValue(root, value, o)
	j, e := openFileJournal(root, o, snapshot)
	journalMust(e)
	defer func() {
		if p := recover(); p != nil {
			_ = j.Close()
			panic(p)
		}
	}()
	for _, b := range verifier.backupBlobs {
		j.putBlob(b, "application/octet-stream")
	}
	for _, r := range verifier.accepted {
		opts := JournalAppendOptions{ActorRef: r["actor_ref"].(string), CorrelationID: r["correlation_id"].(string), EventBatchID: r["event_batch_id"].(string), WrittenAt: r["written_at"].(string)}
		if c, ok := r["command_receipt"]; ok {
			opts.CommandReceipt = journalMap(c)
		}
		j.append(r["events"], opts)
	}
	j.verifyRestoreOrigin()
	if o.Projector != nil && len(j.accepted) > 0 {
		j.checkpoint()
	}
	return j, nil
}
