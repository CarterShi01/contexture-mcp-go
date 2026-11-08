package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// OfflineStoreCustody is trusted operator approval, not graph-author JSON or
// model invocation authority. Approve must use the supplied request context.
type OfflineStoreCustody interface {
	Approve(context.Context, OfflineStoreRequest) error
}

type OfflineStoreRequest struct {
	Operation string
	Path      string
}

type StoreBudget struct {
	MaxEntries, MaxBytes, MaxFileBytes, MaxSnapshotBytes int64
}

func DefaultStoreBudget() StoreBudget {
	return StoreBudget{100000, 64 << 20, 16 << 20, 160 << 20}
}

// SoftwareCompatibility is exact declared support, never package-version
// ordering or a permission to upgrade/upcast accepted records.
type SoftwareCompatibility struct {
	ReaderBuild, WriterBuild string
	StoreFormats, RecordFormats, EventTypes, FactTypes, CanonicalProfiles,
	SemanticVersions, DurabilityProfiles []string
}

func NativeJournalCompatibility() SoftwareCompatibility {
	return SoftwareCompatibility{
		ReaderBuild: contexture.Version, WriterBuild: contexture.Version,
		StoreFormats: []string{"contexture.file-journal/v1"}, RecordFormats: []string{"contexture.journal-record/v1"},
		EventTypes: []string{"GraphChangeCommitted", "CommandCompleted", "RecoveryActionRecorded",
			"ReleaseRequested", "ReleaseSuperseded", "ReleasePreparationSucceeded", "ReleasePreparationFailed", "ReplicaObserved"},
		FactTypes: []string{"GraphSeeded", "NodeCreated", "NodeRestored", "NodeFieldsSet", "GraphFieldsSet",
			"RootsAssigned", "MembersAssigned", "UsesAssigned", "PublicationPut", "PublicationRemoved",
			"PresentationsAssigned", "NodeDeleted", "SemanticMigrated", "CreationPolicyMigrated", "PresentationPut", "PresentationRemoved"},
		CanonicalProfiles: []string{graph.CanonicalProfile}, SemanticVersions: []string{"1"},
		DurabilityProfiles: []string{"strict", "process_crash"},
	}
}

type StoreResult struct {
	Operation               string `json:"operation"`
	Code                    string `json:"code"`
	StorageVerified         bool   `json:"storage_verified"`
	ProjectionVerified      bool   `json:"projection_verified"`
	DefinitionLoaded        bool   `json:"definition_loaded"`
	DesiredPrepared         bool   `json:"desired_prepared"`
	ServingReady            bool   `json:"serving_ready"`
	WriteEnabled            bool   `json:"write_enabled"`
	IsolatedRestoreVerified bool   `json:"isolated_restore_verified"`
}

// StoreOperationError is a bounded redacted boundary error. Unwrap retains the
// exact underlying typed failure for trusted callers, without exposing its
// paths, event bodies or exception text in ordinary diagnostics.
type StoreOperationError struct {
	Operation, Diagnostic string
	cause                 error
}

func (e *StoreOperationError) Error() string {
	return "offline store " + e.Operation + ": " + e.Diagnostic
}
func (e *StoreOperationError) Code() string  { return e.Diagnostic }
func (e *StoreOperationError) Unwrap() error { return e.cause }

func storeBoundaryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &StoreOperationError{operation, StoreDiagnosticCode(err), err}
}

type StoreAnchor struct {
	StoreID    string `json:"store_id"`
	JournalSeq string `json:"journal_seq"`
	RecordHash any    `json:"record_hash"`
}

// StoreBackup contains privileged original assets; diagnostics never serialize
// it. Snapshot is the interoperable FileJournal envelope. Bundle also carries
// every original checkpoint/hint/index/quarantine asset and a subject manifest.
type StoreBackup struct {
	snapshot, bundle []byte
	manifest         JournalObject
	sourceRoot       string
	Result           StoreResult
}

func (b *StoreBackup) Snapshot() []byte {
	if b == nil {
		return nil
	}
	return bytes.Clone(b.snapshot)
}
func (b *StoreBackup) Bundle() []byte {
	if b == nil {
		return nil
	}
	return bytes.Clone(b.bundle)
}
func (b *StoreBackup) Manifest() JournalObject {
	if b == nil || b.manifest == nil {
		return nil
	}
	return journalMap(journalCopy(b.manifest))
}
func (*StoreBackup) String() string   { return "<privileged offline store backup>" }
func (*StoreBackup) GoString() string { return "<privileged offline store backup>" }
func (*StoreBackup) MarshalJSON() ([]byte, error) {
	return nil, &JournalError{"permission_denied", "Privileged backup must not be serialized as diagnostics"}
}

type StoreOperationsOptions struct {
	Schema        any
	Durability    string
	Projector     JournalProjector
	Custody       OfflineStoreCustody
	Compatibility SoftwareCompatibility
	Budget        StoreBudget
}

// StoreOperations is offline only. It pauses a stopped owner's existing stable
// lock; it never opens/initializes/repairs the source as a FileJournal owner.
type StoreOperations struct {
	journal JournalOptions
	custody OfflineStoreCustody
	support SoftwareCompatibility
	budget  StoreBudget
}

func NewStoreOperations(o StoreOperationsOptions) (out *StoreOperations, err error) {
	defer journalCatch(&err)
	if o.Custody == nil {
		return nil, &JournalError{"authority_required", "Explicit trusted offline custody required"}
	}
	if o.Projector == nil {
		return nil, &JournalError{"projection_required", "Complete native fact projection required"}
	}
	if o.Budget == (StoreBudget{}) {
		o.Budget = DefaultStoreBudget()
	}
	if o.Budget.MaxEntries <= 0 || o.Budget.MaxBytes <= 0 || o.Budget.MaxFileBytes <= 0 || o.Budget.MaxSnapshotBytes <= 0 {
		return nil, &JournalError{"limit_exceeded", "Positive offline budgets required"}
	}
	s := o.Compatibility
	if s.ReaderBuild == "" || s.WriterBuild == "" {
		return nil, &JournalError{"software_unsupported", "Exact reader and writer support required"}
	}
	sets := []*[]string{&s.StoreFormats, &s.RecordFormats, &s.EventTypes, &s.FactTypes,
		&s.CanonicalProfiles, &s.SemanticVersions, &s.DurabilityProfiles}
	for _, set := range sets {
		if len(*set) == 0 || slices.Contains(*set, "") {
			return nil, &JournalError{"software_unsupported", "Nonempty exact compatibility sets required"}
		}
		*set = slices.Clone(*set)
		slices.Sort(*set)
		*set = slices.Compact(*set)
	}
	options := JournalOptions{Schema: o.Schema, Durability: journalDefault(o.Durability, "strict"), Projector: o.Projector}
	// Compile/validate once without opening a store or touching a filesystem.
	newJournal(".", options)
	options.Schema = journalCopy(o.Schema)
	return &StoreOperations{options, o.Custody, s, o.Budget}, nil
}

// StoreDiagnosticCode retains coded errors, never path/identity/exception text.
func StoreDiagnosticCode(err error) string {
	if err == nil {
		return "ok"
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		switch code := coded.Code(); code {
		case "unsafe_path", "unsafe_permissions", "durability_unsupported", "store_in_use", "closed",
			"corruption", "journal_corrupt", "schema_invalid", "backup_invalid", "history_unavailable",
			"restore_conflict", "restore_incomplete", "projection_required", "projection_invalid",
			"checkpoint_invalid", "commit_unknown", "limit_exceeded", "recovery_required",
			"format_unsupported", "semantic_unsupported", "software_unsupported", "store_missing",
			"authority_required", "permission_denied", "resource_busy", "approval_stale":
			return code
		case "corrupt_facts", "unsupported_contract", "reference_not_found", "identity_conflict",
			"mutation_protected", "scope_escape", "revision_conflict", "idempotency_conflict", "storage_error":
			return code
		}
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "history_unavailable"
	}
	if errors.Is(err, syscall.ENOSPC) {
		return "disk_full"
	}
	if errors.Is(err, syscall.EROFS) {
		return "read_only"
	}
	if errors.Is(err, syscall.ELOOP) {
		return "unsafe_path"
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return "storage_error"
	}
	return "verification_failed"
}

func (o *StoreOperations) approve(ctx context.Context, operation, path string) error {
	if ctx == nil {
		return &JournalError{"authority_required", "Request context required"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return o.custody.Approve(ctx, OfflineStoreRequest{operation, path})
}

func (o *StoreOperations) checkSupport(v JournalObject) {
	meta := journalMap(v["meta"])
	for key, accepted := range map[string][]string{"schema": o.support.StoreFormats,
		"canonical_profile": o.support.CanonicalProfiles, "durability": o.support.DurabilityProfiles} {
		text, ok := meta[key].(string)
		if !ok || !slices.Contains(accepted, text) {
			journalFail("format_unsupported", "Persisted profile is not supported by this reader/writer")
		}
	}
	if meta["durability"] != o.journal.Durability {
		journalFail("durability_unsupported", "Current and persisted durability profiles differ")
	}
	for _, value := range journalArray(v["records"]) {
		r := journalMap(value)
		format, ok := r["schema"].(string)
		if !ok || !slices.Contains(o.support.RecordFormats, format) {
			journalFail("format_unsupported", "Required record format unsupported")
		}
		for _, value := range journalArray(r["events"]) {
			event := journalMap(value)
			tag, ok := event["type"].(string)
			if !ok || !slices.Contains(o.support.EventTypes, tag) {
				journalFail("format_unsupported", "Required event unsupported")
			}
			if version, ok := event["semantic_version"]; ok && !slices.Contains(o.support.SemanticVersions, releaseText(version)) {
				journalFail("semantic_unsupported", "Required graph semantics unsupported")
			}
			if facts, ok := event["resolved_facts"]; ok {
				for _, value := range journalArray(facts) {
					fact := journalMap(value)
					if !slices.Contains(o.support.FactTypes, releaseText(fact["type"])) {
						journalFail("format_unsupported", "Required resolved fact unsupported")
					}
					if fact["type"] == "GraphSeeded" && !slices.Contains(o.support.SemanticVersions, releaseText(journalMap(fact["after"])["semantic_version"])) {
						journalFail("semantic_unsupported", "Seed graph semantics unsupported")
					}
					if fact["type"] == "SemanticMigrated" {
						for _, key := range []string{"before", "after"} {
							if !slices.Contains(o.support.SemanticVersions, releaseText(fact[key])) {
								journalFail("semantic_unsupported", "Semantic migration unsupported")
							}
						}
					}
				}
			}
		}
	}
}

func storeJSON(raw []byte) JournalObject {
	value, err := graph.DecodeJSON(raw)
	if err != nil {
		journalFail("corruption", "Invalid canonical store object")
	}

	v := journalMap(value)
	if !bytes.Equal(raw, journalBytes(v)) {
		journalFail("corruption", "Noncanonical store object")
	}
	return v
}

// Offline envelopes use SHA-256 of their complete canonical subjects. They do
// not extend or relabel the normative kernel's registered digest domains.
func storeSubjectDigest(v any) string { return journalByteDigest(journalBytes(v)) }

func (o *StoreOperations) paused(root string) (*FileJournal, *os.File) {
	j := newJournal(root, o.journal)
	journalMust(journalPlatform(j.root, j.durability))
	if !journalExists(j.root) {
		journalFail("store_missing", "Existing private store required")
	}
	j.checkPath(j.root, true)
	p := filepath.Join(j.root, "writer.lock")
	if !journalExists(p) {
		journalFail("store_missing", "Existing stable owner lock required")
	}
	j.checkPath(p, false)
	before, err := os.Lstat(p)
	journalMust(err)
	f, err := journalOpen(p, os.O_RDONLY, 0)
	journalMust(err)
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	current, err := f.Stat()
	journalMust(err)
	if !os.SameFile(before, current) {
		journalFail("unsafe_path", "Stable lock identity changed")
	}
	journalMust(journalLock(f))
	after, err := os.Lstat(p)
	journalMust(err)
	if !os.SameFile(current, after) {
		journalFail("unsafe_path", "Stable lock identity changed")
	}
	ok = true
	return j, f
}

func (o *StoreOperations) readFile(j *FileJournal, p string, info os.FileInfo) []byte {
	if info.Size() > o.budget.MaxFileBytes {
		journalFail("limit_exceeded", "Offline file budget exceeded")
	}
	f, err := journalOpen(p, os.O_RDONLY, 0)
	journalMust(err)
	defer f.Close()
	before, err := f.Stat()
	journalMust(err)
	journalMust(journalPrivate(before))
	if !os.SameFile(info, before) {
		journalFail("unsafe_path", "Store asset identity changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, o.budget.MaxFileBytes+1))
	journalMust(err)
	after, err := f.Stat()
	journalMust(err)
	pathInfo, err := os.Lstat(p)
	journalMust(err)
	if !os.SameFile(before, pathInfo) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || int64(len(raw)) != after.Size() {
		journalFail("corruption", "Store asset changed during capture")
	}
	if int64(len(raw)) > o.budget.MaxFileBytes {
		journalFail("limit_exceeded", "Offline file budget exceeded")
	}
	return raw
}

func (o *StoreOperations) capture(ctx context.Context, j *FileJournal) JournalObject {
	assets := []any{}
	var entries, total int64
	var walk func(string, int)
	walk = func(dir string, depth int) {
		journalMust(ctx.Err())
		if depth > 16 {
			journalFail("limit_exceeded", "Offline directory depth exceeded")
		}
		j.checkPath(dir, true)
		f, err := journalOpenDir(dir)
		journalMust(err)
		defer f.Close()
		for {
			batch, err := f.ReadDir(128)
			if err != nil && !errors.Is(err, io.EOF) {
				journalMust(err)
			}
			for _, entry := range batch {
				journalMust(ctx.Err())
				entries++
				if entries > o.budget.MaxEntries {
					journalFail("limit_exceeded", "Offline entry budget exceeded")
				}
				p := filepath.Join(dir, entry.Name())
				info, err := os.Lstat(p)
				journalMust(err)
				j.checkPath(p, info.IsDir())
				rel, err := filepath.Rel(j.root, p)
				journalMust(err)
				name := filepath.ToSlash(rel)
				if dir == j.root {
					if info.IsDir() && !slices.Contains(journalDirectories, entry.Name()) ||
						!info.IsDir() && entry.Name() != "META.json" && entry.Name() != "writer.lock" {
						journalFail("format_unsupported", "Unknown store asset")
					}
				}
				if info.IsDir() {
					switch {
					case strings.HasPrefix(name, "records/"), strings.HasPrefix(name, "checkpoints/"):
						parts := strings.Split(name, "/")
						if len(parts) != 2 || !journalPad.MatchString(parts[1]) {
							journalFail("corruption", "Invalid immutable asset directory")
						}
					case strings.HasPrefix(name, "blobs/"):
						parts := strings.Split(name, "/")
						if len(parts) > 3 || parts[1] != "sha256" ||
							len(parts) == 3 && (len(parts[2]) != 2 || !journalHex.MatchString(parts[2]+strings.Repeat("0", 62))) {
							journalFail("corruption", "Invalid blob directory")
						}
					case strings.HasPrefix(name, "tmp/"):
						journalFail("recovery_required", "Unpublished directory requires owner recovery")
					}
					walk(p, depth+1)
					continue
				}
				if strings.HasPrefix(name, "tmp/") {
					journalFail("recovery_required", "Unpublished work requires explicit owner recovery")
				}
				raw := o.readFile(j, p, info)
				total += int64(len(raw))
				if total > o.budget.MaxBytes {
					journalFail("limit_exceeded", "Offline total byte budget exceeded")
				}
				assets = append(assets, JournalObject{"path": name, "digest": journalByteDigest(raw), "bytes": hex.EncodeToString(raw)})
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
	}
	walk(j.root, 0)
	for _, dir := range journalDirectories {
		p := filepath.Join(j.root, dir)
		if !journalExists(p) {
			journalFail("corruption", "Incomplete store layout")
		}
		j.checkPath(p, true)
	}
	slices.SortFunc(assets, func(a, b any) int {
		return strings.Compare(journalMap(a)["path"].(string), journalMap(b)["path"].(string))
	})
	return JournalObject{"schema": "contexture.offline-store-assets/v1", "assets": assets}
}

func (o *StoreOperations) build(root string, archive JournalObject, anchor *StoreAnchor) *StoreBackup {
	contents := map[string][]byte{}
	items := journalArray(archive["assets"])
	if len(archive) != 2 || archive["schema"] != "contexture.offline-store-assets/v1" || int64(len(items)) > o.budget.MaxEntries {
		journalFail("backup_invalid", "Closed bounded asset archive required")
	}
	var total int64
	var prior string
	for _, value := range items {
		item := journalMap(value)
		if len(item) != 3 {
			journalFail("backup_invalid", "Closed original asset required")
		}
		name, ok := item["path"].(string)
		if !ok || name == "" || name <= prior || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") ||
			strings.Contains(name, ":") || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
			journalFail("backup_invalid", "Unsafe or unordered original asset selector")
		}
		prior = name
		encoded, ok := item["bytes"].(string)
		if !ok || int64(len(encoded))/2 > o.budget.MaxFileBytes {
			journalFail("limit_exceeded", "Original asset budget exceeded")
		}
		raw, err := hex.DecodeString(encoded)
		if err != nil || hex.EncodeToString(raw) != encoded || item["digest"] != journalByteDigest(raw) {
			journalFail("backup_invalid", "Original asset digest differs")
		}
		total += int64(len(raw))
		if total > o.budget.MaxBytes {
			journalFail("limit_exceeded", "Original asset total budget exceeded")
		}
		contents[name] = raw
	}
	metaRaw, ok := contents["META.json"]
	if !ok {
		journalFail("corruption", "Store META is absent")
	}
	meta := storeJSON(metaRaw)
	records := []any{}
	blobs := JournalObject{}
	checkpoints := []JournalObject{}
	for name, raw := range contents {
		switch {
		case strings.HasPrefix(name, "records/"):
			parts := strings.Split(name, "/")
			if len(parts) != 3 || !journalPad.MatchString(parts[1]) || !strings.HasSuffix(parts[2], ".json") ||
				!journalPad.MatchString(strings.TrimSuffix(parts[2], ".json")) {
				journalFail("corruption", "Invalid committed record selector")
			}
			r := storeJSON(raw)
			n, err := graph.ParseCounter(releaseText(r["journal_seq"]))
			journalMust(err)
			j := &FileJournal{root: root}
			if filepath.ToSlash(j.recordPath(n)) != filepath.ToSlash(filepath.Join(root, filepath.FromSlash(name))) {
				journalFail("corruption", "Record bucket or sequence differs")
			}
			records = append(records, r)
		case strings.HasPrefix(name, "blobs/"):
			parts := strings.Split(name, "/")
			digest := strings.TrimSuffix(parts[len(parts)-1], ".blob")
			if len(parts) != 4 || parts[1] != "sha256" || !journalHex.MatchString(digest) ||
				parts[2] != digest[:2] || parts[3] != digest+".blob" || journalByteDigest(raw) != "sha256:"+digest {
				journalFail("corruption", "Original blob digest or selector differs")
			}
			blobs["sha256:"+digest] = hex.EncodeToString(raw)
		case strings.HasPrefix(name, "checkpoints/"):
			parts := strings.Split(name, "/")
			cp := storeJSON(raw)
			digest, ok := cp["checkpoint_digest"].(string)
			if len(parts) != 3 || !journalPad.MatchString(parts[1]) ||
				!ok || !strings.HasPrefix(digest, "sha256:") || !journalHex.MatchString(strings.TrimPrefix(digest, "sha256:")) ||
				parts[2] != strings.TrimPrefix(digest, "sha256:")+".json" {
				journalFail("checkpoint_invalid", "Checkpoint selector differs")
			}
			n, err := graph.ParseCounter(releaseText(cp["last_seq"]))
			journalMust(err)
			if fmtStoreCounter(n.String()) != parts[1] {
				journalFail("checkpoint_invalid", "Checkpoint sequence differs")
			}
			checkpoints = append(checkpoints, cp)
		case name == "META.json", name == "writer.lock":
		case strings.HasPrefix(name, "hints/"), strings.HasPrefix(name, "indexes/"), strings.HasPrefix(name, "quarantine/"):
		default:
			journalFail("format_unsupported", "Unknown original store asset")
		}
	}
	slices.SortFunc(records, func(a, b any) int {
		return strings.Compare(fmtStoreCounter(releaseText(journalMap(a)["journal_seq"])), fmtStoreCounter(releaseText(journalMap(b)["journal_seq"])))
	})
	subject := JournalObject{"schema": "contexture.file-journal-backup/v1", "meta": meta, "records": records, "blobs": blobs, "checkpoint": nil}
	o.checkSupport(subject)
	subject["backup_digest"] = journalDigest("contexture.file-journal-backup/v1", subject)
	verifier := verifyJournalSnapshot(root, journalBytes(subject), o.journal)
	projection := verifier.project(verifier.accepted)
	for _, cp := range checkpoints {
		verifier.checkCheckpoint(cp)
		if subject["checkpoint"] == nil || len(releaseText(cp["last_seq"])) > len(releaseText(journalMap(subject["checkpoint"])["last_seq"])) ||
			len(releaseText(cp["last_seq"])) == len(releaseText(journalMap(subject["checkpoint"])["last_seq"])) &&
				releaseText(cp["last_seq"]) > releaseText(journalMap(subject["checkpoint"])["last_seq"]) {
			subject["checkpoint"] = cp
		}
	}
	a := StoreAnchor{releaseText(meta["store_id"]), strconv.Itoa(len(records)), nil}
	if len(records) > 0 {
		a.RecordHash = journalMap(records[len(records)-1])["record_hash"]
	}
	if anchor != nil && !journalEqual(
		JournalObject{"store_id": a.StoreID, "journal_seq": a.JournalSeq, "record_hash": a.RecordHash},
		JournalObject{"store_id": anchor.StoreID, "journal_seq": anchor.JournalSeq, "record_hash": anchor.RecordHash}) {
		journalFail("history_unavailable", "Independent durable anchor differs")
	}
	// Retain all original immutable assets, including older checkpoints, inside
	// the standard backup's blob inventory. Restore provenance binds this blob.
	archiveBytes := journalBytes(archive)
	archiveDigest := journalByteDigest(archiveBytes)
	blobs[archiveDigest] = hex.EncodeToString(archiveBytes)
	delete(subject, "backup_digest")
	subject["backup_digest"] = journalDigest("contexture.file-journal-backup/v1", subject)
	snapshot := journalBytes(subject)
	manifest := JournalObject{"schema": "contexture.offline-store-manifest/v1",
		"store_id": a.StoreID, "journal_seq": a.JournalSeq, "record_hash": a.RecordHash,
		"backup_digest": subject["backup_digest"], "assets_digest": archiveDigest,
		"projection_digest": storeSubjectDigest(JournalObject{"schema": "contexture.offline-store-projection/v1", "projections": projection}),
		"watermark_kind":    "complete_offline_prefix",
		"reader_build":      o.support.ReaderBuild, "writer_build": o.support.WriterBuild}
	manifest["manifest_digest"] = storeSubjectDigest(manifest)
	bundle := JournalObject{"schema": "contexture.offline-store-backup/v1", "snapshot": subject, "manifest": manifest}
	bundle["bundle_digest"] = storeSubjectDigest(bundle)
	bundleBytes := journalBytes(bundle)
	if int64(len(snapshot)) > o.budget.MaxSnapshotBytes || int64(len(bundleBytes)) > o.budget.MaxSnapshotBytes {
		journalFail("limit_exceeded", "Offline envelope budget exceeded")
	}
	return &StoreBackup{snapshot: snapshot, bundle: bundleBytes, manifest: manifest,
		Result: StoreResult{Operation: "backup", Code: "ok", StorageVerified: true, ProjectionVerified: true}}
}

func fmtStoreCounter(counter string) string {
	if len(counter) > 20 {
		journalFail("limit_exceeded", "Journal counter exceeded")
	}
	return strings.Repeat("0", 20-len(counter)) + counter
}

func (o *StoreOperations) Backup(ctx context.Context, root string, anchor *StoreAnchor) (out *StoreBackup, err error) {
	defer func() {
		if err != nil {
			out = nil
			err = storeBoundaryError("backup", err)
		}
	}()
	defer journalCatch(&err)
	if err = o.approve(ctx, "backup", root); err != nil {
		return nil, err
	}
	j, lock := o.paused(root)
	defer func() {
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
	}()
	archive := o.capture(ctx, j)
	out = o.build(j.root, archive, anchor)
	out.sourceRoot = j.root
	// A raw final file is not evidence of acknowledged durability. Under the
	// stopped-owner lock, validate first, then confirm every immutable asset
	// through the existing profile-specific recovery primitive. No bytes,
	// hints, projections, quarantine or lock inodes are changed.
	for _, value := range journalArray(archive["assets"]) {
		item := journalMap(value)
		name := item["path"].(string)
		if name == "META.json" || strings.HasPrefix(name, "records/") ||
			strings.HasPrefix(name, "blobs/") || strings.HasPrefix(name, "checkpoints/") {
			journalMust(ctx.Err())
			if e := func() (err error) {
				defer journalCatch(&err)
				j.confirm(filepath.Join(j.root, filepath.FromSlash(name)))
				return nil
			}(); e != nil {
				return nil, &JournalError{"commit_unknown", "Offline immutable durability confirmation failed"}
			}
		}
	}
	journalMust(ctx.Err())
	return out, nil
}

func (o *StoreOperations) Doctor(ctx context.Context, root string, anchor *StoreAnchor) (result StoreResult, err error) {
	result.Operation = "doctor"
	defer func() {
		err = storeBoundaryError("doctor", err)
		result.Code = StoreDiagnosticCode(err)
	}()
	defer journalCatch(&err)
	if err = o.approve(ctx, "doctor", root); err != nil {
		return result, err
	}
	j, lock := o.paused(root)
	defer func() {
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
	}()
	o.build(j.root, o.capture(ctx, j), anchor)
	journalMust(ctx.Err())
	result.StorageVerified, result.ProjectionVerified = true, true
	return result, nil
}

// Restore accepts the native bundle or an original interoperable FileJournal
// snapshot. Exact compatibility and replay are checked before destination
// creation. It returns no live owner and never prepares/adopts a release.
func (o *StoreOperations) Restore(ctx context.Context, root string, input []byte) (result StoreResult, err error) {
	result.Operation = "restore"
	defer func() {
		err = storeBoundaryError("restore", err)
		result.Code = StoreDiagnosticCode(err)
	}()
	defer journalCatch(&err)
	if err = o.approve(ctx, "restore", root); err != nil {
		return result, err
	}
	if int64(len(input)) > o.budget.MaxSnapshotBytes {
		journalFail("limit_exceeded", "Restore envelope budget exceeded")
	}
	v := storeJSON(input)
	snapshot := bytes.Clone(input)
	if v["schema"] == "contexture.offline-store-backup/v1" {
		if len(v) != 4 {
			journalFail("backup_invalid", "Closed native backup required")
		}
		subject := journalMap(journalCopy(v))
		delete(subject, "bundle_digest")
		if v["bundle_digest"] != storeSubjectDigest(subject) {
			journalFail("backup_invalid", "Native backup subject digest differs")
		}
		manifest := journalMap(v["manifest"])
		reader, readerOK := manifest["reader_build"].(string)
		writer, writerOK := manifest["writer_build"].(string)
		if len(manifest) != 11 || manifest["schema"] != "contexture.offline-store-manifest/v1" ||
			manifest["watermark_kind"] != "complete_offline_prefix" || !readerOK || reader == "" || !writerOK || writer == "" {
			journalFail("backup_invalid", "Closed typed native manifest required")
		}
		s := journalMap(v["snapshot"])
		encoded, ok := journalMap(s["blobs"])[releaseText(manifest["assets_digest"])].(string)
		if !ok {
			journalFail("backup_invalid", "Original asset archive is absent")
		}
		raw, err := hex.DecodeString(encoded)
		journalMust(err)
		reconstructed := o.build(root, storeJSON(raw), nil)
		// Reader/writer labels are provenance, not compatibility assertions.
		current := reconstructed.manifest
		current["reader_build"], current["writer_build"] = manifest["reader_build"], manifest["writer_build"]
		delete(current, "manifest_digest")
		current["manifest_digest"] = storeSubjectDigest(current)
		if !journalEqual(current, manifest) || !journalEqual(storeJSON(reconstructed.snapshot), s) {
			journalFail("backup_invalid", "Complete original assets or manifest differ")
		}
		snapshot = journalBytes(s)
		v = s
	}
	o.checkSupport(v)
	verifier := verifyJournalSnapshot(root, snapshot, o.journal)
	verifier.project(verifier.accepted)
	journalMust(ctx.Err())
	j, err := RestoreFileJournal(root, snapshot, o.journal)
	journalMust(err)
	defer j.Close()
	actual, err := j.Projections()
	journalMust(err)
	if !journalEqual(actual, verifier.project(verifier.accepted)) || j.Meta()["store_id"] == verifier.meta["store_id"] {
		journalFail("restore_incomplete", "Isolated restore identity or complete projections differ")
	}
	if _, ok := j.Meta()["restored_from"]; !ok {
		journalFail("restore_incomplete", "Isolated restore provenance missing")
	}
	journalMust(ctx.Err())
	journalMust(j.Close())
	result.StorageVerified, result.ProjectionVerified, result.IsolatedRestoreVerified = true, true, true
	return result, nil
}

// PublishBackup publishes privately and exclusively on the declared local
// filesystem. It never overwrites an existing artifact. After visibility, sync
// uncertainty returns commit_unknown and retains the published artifact.
func (o *StoreOperations) PublishBackup(ctx context.Context, path string, backup *StoreBackup, interoperable bool) (err error) {
	defer func() { err = storeBoundaryError("publish_backup", err) }()
	defer journalCatch(&err)
	if err = o.approve(ctx, "publish_backup", path); err != nil {
		return err
	}
	if backup == nil || backup.Result.Code != "ok" || len(backup.snapshot) == 0 ||
		len(backup.bundle) == 0 || backup.manifest == nil {
		journalFail("backup_invalid", "Verified privileged backup required")
	}
	parent := filepath.Dir(path)
	j := newJournal(parent, o.journal)
	journalMust(journalPlatform(j.root, j.durability))
	j.checkPath(j.root, true)
	path = filepath.Join(j.root, filepath.Base(path))
	if backup.sourceRoot != "" && (path == backup.sourceRoot ||
		strings.HasPrefix(path, backup.sourceRoot+string(os.PathSeparator))) {
		journalFail("unsafe_path", "Backup publication must stay outside its source store")
	}
	j.checkPath(path, false)
	if journalExists(path) {
		journalFail("restore_conflict", "Backup destination already exists")
	}
	stage := filepath.Join(j.root, ".contexture-backup-"+journalUUID())
	f, e := journalOpen(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	journalMust(e)
	defer func() { _ = f.Close(); _ = os.Remove(stage) }()
	data := backup.bundle
	if interoperable {
		data = backup.snapshot
	}
	n, e := f.Write(data)
	journalMust(e)
	if n != len(data) {
		journalFail("storage_error", "Incomplete backup publication")
	}
	journalMust(f.Sync())
	journalMust(f.Close())
	journalMust(ctx.Err())
	journalMust(journalNoReplace(stage, path))
	if e = func() (err error) {
		defer journalCatch(&err)
		j.syncDir(j.root)
		return nil
	}(); e != nil {
		journalUnknown()
	}
	return nil
}
