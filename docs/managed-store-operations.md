# Native offline FileJournal operations

The Go SDK and actual `contexture store` CLI implement bounded, trusted **offline**
doctor, consistent backup, and isolated restore. They reuse the production
FileJournal schema, chain/blob/checkpoint verifier, complete native graph/release
fact projector, restore/provenance protocol, and Linux custody primitives. No
Python process is a production implementation.

## Operator boundary and capabilities

`server.NewStoreOperations(server.StoreOperationsOptions{...})` requires an
explicit registered schema, a complete pure `JournalProjector`, exact
`SoftwareCompatibility`, and a trusted `OfflineStoreCustody` port. `Approve`
receives `context.Context` and the operation/path. Graph-author JSON, an approval
string, or a package version is not authority. There is no permissive authority,
handler, clock, registry, or source-seed fallback.

`NativeJournalCompatibility()` declares this reader/writer build (`1.0.1`), the
exact store/record formats, required events and resolved facts, canonical profile,
graph semantic versions, and durability profiles. Support is checked before
restore creates its destination. Removing support for any retained required fact
refuses the restore; software downgrade is not release rollback or an upcaster.

Public methods:

* `Doctor(ctx, root, *StoreAnchor) (StoreResult, error)` validates a bounded,
  private, existing store under its **existing** stable exclusive lock. It does
  not create the store, initialize META/indexes/owners, sync/repair/quarantine
  assets, delete locks, or change source bytes. It performs full-chain replay
  in memory, not temporary restore. Storage/projection verification never
  implies a confirmed live-owner watermark, runtime preparation, or readiness.
* `Backup(ctx, root, *StoreAnchor) (*StoreBackup, error)` holds that same
  stopped-owner lock across capture, full validation/replay and profile-specific
  immutable-file durability confirmation. Complete final records are validated
  and confirmed, not assumed acknowledged from visibility. Temporary/unpublished
  work requires explicit owner recovery. Failed confirmation returns
  `commit_unknown`, never an artifact.
* `Restore(ctx, freshRoot, bytes) (StoreResult, error)` verifies exact
  compatibility, the full subject and original assets before calling
  `RestoreFileJournal`. The destination must not exist. Records are replayed
  into a new store identity/hash chain, retaining original facts/receipts and
  `restored_from` provenance. It verifies complete projections and closes the
  isolated owner before returning. It does not overwrite a live store, adopt
  desired releases, enable business writes, truncate history, or authorize an
  upgrade.
* `PublishBackup(ctx, path, artifact, interoperable) error` publishes atomically
  and exclusively to a private existing directory on a supported filesystem.
  It refuses publication inside the source store and never overwrites an artifact;
  post-publication sync uncertainty retains
  the visible artifact and reports `commit_unknown`.

`StoreBackup.Snapshot()` is the existing canonical
`contexture.file-journal-backup/v1` envelope accepted by the original Python
restore adapter. `Bundle()` additionally provides the closed native
`contexture.offline-store-backup/v1` envelope and manifest. Every original
META/record/blob/checkpoint/hint/index/quarantine file is captured, digested and
retained in an immutable archive blob in the snapshot. The manifest binds the
complete archive, durable-prefix anchor, interoperable backup digest and whole
projection digest; its digest and the bundle subject digest are checked on
restore. Offline subject digests are SHA-256 of the complete canonical subject;
the normative kernel's registered digest domains are unchanged. This is
privileged backup data, **not** diagnostic JSON, and whole-object JSON
serialization is refused.

An optional independent `StoreAnchor` detects a mismatched store/prefix. A
self-consistent chain cannot prove externally deleted suffixes without an
independent anchor. Noncooperating administrator tampering is outside the
exclusive-owner guarantee.

## Actual CLI

No project or application import is needed:

```sh
contexture store --help
contexture store doctor --store /private/store --schema managed.schema.json \
  --trusted-offline --durability strict --json
contexture store backup --store /private/store --schema managed.schema.json \
  --trusted-offline --durability strict --output /private/backup.json --json
contexture store restore --store /private/new-isolated-store \
  --schema managed.schema.json --trusted-offline --durability strict \
  --input /private/backup.json --json
```

`--trusted-offline` is explicit local operator setup: stop the service first.
An active writer returns `store_in_use`; there is no online force/takeover flag.
`--interoperable` publishes the standard snapshot instead of the native bundle;
restore accepts either. Doctor/backup accept `--anchor FILE` with exactly
`store_id`, decimal-string `journal_seq`, and nullable `record_hash`.
`--require-reader-build 1.0.1` asserts the actual executable build; another
requested build returns `software_unsupported`, never a fallback.
Durability defaults to `strict`; `process_crash` is an explicit weaker profile
and must exactly match META. Duplicate/unknown options are usage errors.

JSON/human output consists only of bounded status fields. Paths, store/actor
identities, exception text, event bodies, and secret handles are never reported.
`StoreDiagnosticCode` preserves registered stable codes. `StoreOperationError`
has a bounded redacted message and retains the exact underlying typed error
through `Unwrap`/`errors.As`. Exit statuses: 0 success; 1 verification/safety error;
2 usage; 3 custody denied; 4 owner/publication/restore conflict or uncertainty;
5 unsupported format/semantics/software/durability; 6 missing/not-ready/history.
`serving_ready`, `desired_prepared`, `definition_loaded`, and `write_enabled`
remain false. Only a real verified isolated restore sets
`isolated_restore_verified`.

## Platform and validation boundary

Native supported-location checks are unchanged: Linux private local
ext4/xfs/btrfs; explicit `process_crash` may also use tmpfs/overlay. Symlinks,
multi-link files, nonprivate assets, source worktrees, 9p/drvfs/SMB/network and
cloud-synchronized paths refuse. Windows/macOS unverified locking/durability
primitives return `durability_unsupported`; no substitute primitives or claimed
power-loss tests are used.

Defaults are admission bounds, not capacity claims: 100,000 entries, 64 MiB total
original files, 16 MiB per original file and 160 MiB per envelope. Files are
read with limits and identity/change checks; directory reads are chunked.

The candidate provides focused/full race, CLI subprocess, new typed consumer and
Python→TypeScript→Go→Python / Go-offline-backup→Python-restore tests. Native
execution is parent-run pending, not measured acceptance; actual status and
retained evidence live under
`docs/verification/go-managed-native-offline-20261107-1152/`.
The runner creates a new evidence label, retains all raw events/errors, and
checks before/after source and input fingerprints. Native Journal tests must be
run by the parent in its explicit native filesystem environment; no repository
scratch store substitutes for custody. Deployment-host-specific hardware,
credentials and TLS tests alone remain user-deferred. This is neither release
authorization nor a new normative golden/specification claim.
