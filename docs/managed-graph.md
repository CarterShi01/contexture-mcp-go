# Native FileJournal graph owner candidate

This bounded candidate closes the managed graph-authoring/history/projector
ports using actual FileJournal facts and existing native transaction/event/
history/protection kernels. It is not managed-profile acceptance, cross-language
product equivalence, full Host/CLI coverage or WP00-WP15 completion.
Shared schemas, fixtures, golden assets, version and conformance pin are unchanged.

Pure projection and kernel entry points are exported from
`github.com/CarterShi01/contexture-mcp-go/managed`. Physical graph custody and
the release/catalog/watch adapters live in
`github.com/CarterShi01/contexture-mcp-go/server`.
No SDK or filesystem dependency enters the pure package.

Trusted offline doctor/consistent backup/isolated restore now have native SDK
and actual CLI entry points; see [store operations](managed-store-operations.md)
for explicit custody, compatibility, read-only source and evidence boundaries.

## Physical setup

```go
journal, err := server.OpenFileJournal(root, server.JournalOptions{
    Schema: configuredManagedSchema,
    Projector: server.GraphJournalProjector(managed.GraphJournalProjectionOptions{
        PublicationContracts: publicationEvidence,
    }),
})
owner, err := server.NewFileJournalGraphOwner(journal, server.GraphOwnerOptions{
    Host: currentHost,
    Serialization: &server.SynchronousJournalSerialization{},
    Clock: trustedUTCClock,
    PublicationContracts: publicationEvidence,
})
```

Strict durability remains the FileJournal default. The caller retains the
physical Journal lifetime/OS lock; `owner.Close()` detaches the logical manager
without closing physical custody. One graph owner binds each FileJournal
instance. There is no memory-store fallback or independently mutable graph head.
Every owner operation uses a verified accepted cut. An unchanged native Journal
watermark reuses the same immutable semantic projection within that owner;
identity, admission, approval and the Host fence are still evaluated on every
operation. New accepted records extend that verified prefix through
`GraphJournalProjection.Extend`: sequence, origin, hash chain, batch/receipt
uniqueness, graph facts, release facts and blob closure are validated on the
suffix before publishing a new immutable cut. Failed extension leaves the old
cut unchanged. Fresh construction and physical recovery always replay completely.
History/map copying and physical Journal work still have history-dependent costs;
this is not whole-operation O(delta) or 10k/100k capacity certification. The supplied local
serialization grants serialization only, never identity, approval or policy;
it must be explicitly supplied or replaced by a stronger shared port.

## Required context-aware Host authority

```go
type GraphOwnerHost interface {
    Current(context.Context) (GraphOwnerContext, error)
    VerifyApproval(context.Context, GraphOwnerContext,
        reference, subjectDigest, now string) (GraphApproval, error)
    Decide(context.Context, GraphAdmission) (GraphAdmissionDecision, error)
    Fence(context.Context, GraphOwnerContext, func() error) error
}
type GraphOwnerSerialization interface {
    Run(func() error) error
}
```

`Current` supplies tenant-safe store-wide actor attribution, authenticated
identity/authority revision and explicit Host permissions. Public authoring,
read and bridge operations require `managed.WithIdentity(ctx, identity)`;
the current Host must independently attest that actor/tenant. There is no
ambient/goroutine-local/process-global request principal or implicit identity.
The application still owns concrete credential validation and policy.

`GraphAdmission` carries the graph/operation, ordered operation names, exact base
revision/digest and candidate digest, impact, separately referenceable target
IDs, optional exact approval, and explicit staged payload references. Decisions
must be current, allowed and cover every impact/reference target. Full raw
definition/history/receipt reads require complete scope, not a partial grant.
Catalog/watch use explicit partial scope and existing metadata projection.

`GraphApproval` binds current actor, tenant, authority revision, exact subject
digest and expiry. Approvals are freshly verified before commit. The required
UTC clock is explicit; fractional seconds are compared without collapsing
microseconds or finer precision to milliseconds.

The independent Host fence must span current decision through append and share
the same authoring serialization used by release publication. A remote
decision-only callback cannot satisfy that stronger contract. Ports are trusted
synchronous native capabilities: invoke action once during the call, propagate
its error, never retain it or invoke it on another goroutine. Reentry, missing/
repeated dispatch, retained callbacks and changed identity/cuts refuse. This is
not a sandbox against malicious deployment code holding raw physical custody.

## Approved bootstrap and opaque preview/commit

Bootstrap uses the reference candidate's local closed envelope:

```text
schema: "contexture.bootstrap/v1"
graph_id, expected_revision: "0"
seed: {inline: definition} | {blob: BlobRef}
approval_ref, idempotency_key, reason, correlation_id
```

Its exact approval/command subject is schema + graph ID + expected revision +
normalized seed digest + reason, hashed under existing `contexture.command/v1`.
It requires independent `graph.bootstrap` permission plus exact verified
approval. Only existing `GraphChangeCommitted`/`GraphSeeded` facts are persisted.
Bootstrap preview receipt metadata is local, not a newly accepted wire contract.

```go
preview, err := owner.PreviewBootstrap(ctx, bootstrapEnvelope, expiresAt)
receipt, err := owner.Commit(ctx, preview, server.GraphCommitOptions{})

preview, err = owner.Preview(ctx, transactionEnvelope, expiresAt)
receipt, err = owner.Commit(ctx, preview, server.GraphCommitOptions{
    ApprovalRef: renewedApprovalReference, // optional explicit renewal
})
```

`GraphPreview` is an opaque owner-issued capability. `Receipt()` returns an
immutable existing candidate preview receipt, not a reconstructible commit
credential. Both pointer/value JSON/text serialization of the capability refuse.
The ordinary preview uses existing pure prepare/protection kernels, canonical
definition diff, impact, approval subject and processing limits.

Current full-read gates cover base and referenced historical snapshots.
Definition blobs come from that same real Journal only after current
`graph.payload` admission; existing canonical/digest/size/structure kernels
validate their bytes. Root intent requires independent `graph.roots` permission,
including transactions whose final delta is noop.

Commit rechecks exact base revision/digest/tombstones, reconstructs the complete
proposal, refreshes approval/current admission and rechecks preview/approval
expiry immediately before append. One atomic record contains one graph event
with all command facts plus the original actor-scoped `CommandReceipt`.
Noop persists `CommandCompleted`, unchanged graph revision and a real noop receipt.
The narrow Journal integration correctly matches graph receipts against existing
`result_revision` (or `revision` for noop); actor/digest/status checks remain.

Actor/key retries consult actual Journal receipts before CAS, stale preview or
old approval expiry, then require current complete read admission. They return
the original receipt and reject conflicting subjects. A legitimate same-store
owner-issued capability can retry after physical reopen, including under a new
logical owner. Another actor or physical store cannot consume it.

## Pure complete replay and immutable history

`NewGraphJournalProjection(records, options)` verifies complete accepted header,
sequence/store/hash/payload/batch/receipt provenance and replays existing native
`History`/`ApplyEvent` and release reducers. It reconstructs retained tombstones
and all eight checkpoint sections from actual facts:

- graph definitions/heads/tombstones;
- desired releases, release operations, preparation and observation histories;
- original sorted actor/key receipts;
- projection versions and required blob closure.

Only schema-defined `OperationResult.blob` and
`RecoveryActionRecorded.evidence` contribute required blob closure. Opaque author
JSON is not interpreted as a reference. Replay executes no Host, approval,
admission, clock, registry/resource factory, business decoder/validator/handler
or adoption callback.

```go
state, err := owner.Read(ctx, graphID, "")       // actual head
state, err = owner.Read(ctx, graphID, revision)  // exact retained revision
history, err := owner.History(ctx, graphID)
bytes, err := owner.Export(ctx, graphID, revision)
receipt, err := owner.Receipt(ctx, graphID, actorScopedKey)
```

Snapshots are immutable. Current full-read scope gates every relevant historical
snapshot; unavailable history never substitutes head. A nil receipt means absent
for this current actor/graph, not a search across principals.
Failed authority reads return no usable snapshot/result.

## Physical recovery

Any append failure conservatively stops the logical owner. No blind append or
memory-head claim follows. Close/reopen physical custody, then call
`owner.Recover(ctx, reopenedJournal)` on the same logical owner.
The store ID and an extension of the last confirmed accepted hash prefix must
match; another store or backup clone is not a rebind shortcut.
Recovery replays actual accepted facts without external callbacks. Published-
unknown receipts are discovered, and a valid capability can then return the
original actual receipt without duplicating publication.

Benign concurrent/callback Journal contention refuses without poisoning healthy
custody. Actual closed/unknown readiness stops the owner. Native Go mutexes and
explicit contexts implement the boundary; no goroutine identity emulation exists.

## Actual release, catalog and watch bridges

```go
releaseOwner, err := server.NewFileJournalReleaseOwner(journal, server.FileJournalReleaseOptions{
    GraphID: graphID, TargetID: targetID,
    Host: owner.ReleaseHost(controlContext),
    History: owner.ReleaseHistoryReader(),
    Compilation: currentCompilePort,
})
catalog, err := managed.NewManagedCatalogService(managed.CatalogOptions{
    PageStoreOptions: managed.PageStoreOptions{Signer: signer},
    Definition: owner.DefinitionReads(),
})
watch, err := managed.NewManagedWatchService(managed.WatchOptions{
    Signer: signer, Reader: owner.CommittedReads(),
})
```

`ReleaseHost(controlContext)` explicitly binds a native control-plane context to
the existing context-free release port. The application owns that context's
lifetime/principal routing; it is not an implicit mutable request principal.
The bridge shares the actual graph owner serialization/Host authority fence and
current approval/admission, including actor-scoped receipt-read precedence.
Current compilation, resource preparation and observation remain the existing
explicit release/runtime ports. Graph head, desired historical revision and
local serving generation remain distinct facts.

`ReleaseHistoryReader` derives exact snapshots/positive accepted anchors from
the same physical Journal and rejects transplanted or unaccepted prefixes.
It is a trusted internal verification port, not a replacement for authorized
public raw reads. `ReleaseGraphProjector()` is available for compatibility with
the existing release projection composer; the native graph projector already
provides complete actual release sections without custom graph logic.

`DefinitionReads()` implements existing current `graph.catalog` admission with
actual head/historical indexes, partial visible scope and stable physical origin,
under one owner fence. `CommittedReads()` supplies actual retained before/batch/
through positions under current `graph.watch` scope. Windows are bounded and
complete, not partial batches; escaped callbacks refuse. Missing history never
falls back to today's head. These are native service ports, not new Host wire
adapters.

For joint recovery, rebind the graph owner to recovered physical custody, then
recover the same release owner before retrying observation. A real live pointer
is never reconstructed or rolled back by replay.

## Explicit existing limitations

- Existing `History` requires strictly increasing Journal sequence per graph.
  Multiple same-graph `GraphChangeCommitted`/`CommandCompleted` events in one
  record cannot retain intermediate exact revisions. Replay refuses
  `unsupported_contract`; multiple different graphs per record remain valid.
  Native transaction commits emit one graph event containing all command facts.
- Publication replay evidence remains an explicit immutable node-keyed
  `PublicationContract` map. Changing historical published bindings at one stable
  node ID needs a retained versioned evidence contract the existing map lacks.
  No registry callback or invented author assertion fills that gap.
- Replay is complete, not incremental/compacted/retention-optimized. No throughput
  or unlimited history scalability claim is made.
- Concrete authentication, tenant-safe actor mapping, policy, approvals, current
  compilation and a genuinely independent shared authority fence remain required.
  Full Host/CLI surfaces, privileged migration authoring, fleet operations,
  durable Task provenance and schema-absent release start/pending facts remain
  outside this slice. No accepted profile or all-WP completion is claimed.
