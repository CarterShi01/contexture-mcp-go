# Native FileJournal release owner candidate

This is a bounded extension of the [managed native runtime](managed-runtime.md),
not managed-profile acceptance, language parity, or WP00-WP15 completion.
Stable specification pins, shared schemas, fixtures, golden assets and package
version remain unchanged. No release, deployment or business execution is implied.

The later [native graph owner](managed-graph.md) provides the actual complete
graph projector, exact same-Journal history reader, shared Host fence and
definition/watch bridges. Custom graph-history/projection logic is no longer
required for that production adapter; concrete Host authority remains required.

Pure author contracts/reducers are available through
`github.com/CarterShi01/contexture-mcp-go/managed`.
Physical `FileJournalReleaseOwner` and projection/readiness integration live at
`github.com/CarterShi01/contexture-mcp-go/server`. No filesystem or MCP SDK enters
the pure managed package.

## Persisted facts and receipts

The owner writes only existing candidate `ReleaseRequested`,
`ReleaseSuperseded`, `ReleasePreparationSucceeded`, `ReleasePreparationFailed`
and `ReplicaObserved` facts. A successor request atomically persists
supersession, desired intent and an actor-scoped `CommandReceipt` containing the
original inline `ReleaseReceipt`.

`ParseReleaseRequest(any)` returns an immutable author value. Its subject omits
`idempotency_key` and `approval_ref`, hashing under the existing
`contexture.command/v1` domain. Author JSON supplies references and requested
coordinates, **never** approval, history, registry or identity authority.
CAS uses exact desired epoch, not graph head. Both exact/latest persist the
explicit verified historical revision; background coalescing is not implemented.

Accepted retries consult the actual Journal actor/key index. They return the
original receipt even after supersession. Conflicting subjects refuse.
An explicit `receipt_read` Host proof checks current actor-scoped read admission
without repeating expired approval, historical compilation or CAS for a command
already accepted. Actual matching release facts and receipt provenance remain
mandatory; retry does not grant execution/swap/observation authority.

`EmptyReleaseState`, `ImportReleaseState`, `ReduceReleaseEvent` and
`ReplayReleases` rebuild the four existing release checkpoint sections. Replay
validates supplied record hashes, payloads, accepted sequence/hash prefix,
release lineage, receipts and terminal/observation provenance. It executes no
Host proofs, business handlers, compilation, resource acquisition or adoption.
No durable start/pending/preparing/consumed fact exists in the current schema;
crash-time in-flight attempts cannot be reconstructed or invented.

## Required independent graph and Host ports

```go
type ReleaseHistoryReader interface {
    Read(graphID, revision string, view JournalHistoryView) (VerifiedReleaseHistory, error)
}
type ReleaseCompilePort interface {
    Verify(managed.ReleaseRequest, VerifiedReleaseHistory, ReleaseHostContext) (*managed.StructuralPlan, error)
}
type ReleaseHostPort interface {
    Current() (ReleaseHostContext, error)
    VerifyApproval(ReleaseHostAdmission) (any, error)
    Admit(ReleaseHostAdmission, any) error
    Fence(ReleaseHostAdmission, any, func() error) error
}
```

All ports are required; there is no permissive policy/default identity/compiler/
graph projection. The Host supplies explicit application-context identity and
tenant-safe trusted actor attribution; the framework does not emulate ambient
goroutine-local or global request identity. The existing managed request runtime
continues to require explicit `context.Context` identity independently.

`JournalHistoryView` contains store ID, accepted watermark and detached accepted
records. The reader must independently replay/verify the exact immutable
requested graph revision from **that same Journal**, not substitute head or
deserialize caller claims. `VerifiedReleaseHistory` contains the snapshot,
store ID, positive accepted anchor sequence and record hash. The owner checks
graph/revision/definition digest and same-store accepted anchor/hash provenance.

The compile port independently compiles exact verified history against the
**current** trusted registry/environment/target. It must be pure, synchronous
and I/O-free. Graph/definition/manifest coordinates must match accepted intent.
Compilation is repeated at the final Host fence; a caller manifest digest cannot
replace current compiler evidence.

`ReleaseHostAdmission` includes current context, operation, immutable request and
subject digest, and either verified history/manifest, explicit `terminal_failure`
evidence, or an original receipt-only `receipt_read` proof. Admission history
handles are detached between Host calls. Terminal failure can authorize only a
supported failed-attempt fact when current compilation fails; it cannot authorize
acceptance, preparation success, adoption or observation.

Approval must independently prove the exact subject digest. `approval_ref` is
only a reference. The Host fence must recheck current identity, admission and
approval under the **same serialization/authority boundary as the graph authoring
owner**. Call action once during the fence, return its error, never retain it or
invoke it on another goroutine. Proof ports and the fence are synchronous and
I/O-free; non-pointer actions may append Journal facts. Pointer adoption never
appends or disposes resources. Identity/authority-revision, accepted watermark
or current compilation movement across callbacks rejects before action.

## Wiring and custody

```go
projector, err := server.ReleaseJournalProjector(graphOwnerProjector)
journal, err := server.OpenFileJournal(storeRoot, server.JournalOptions{
    Schema: configuredManagedSchema, Projector: projector,
}) // strict durability remains the default
owner, err := server.NewFileJournalReleaseOwner(journal, server.FileJournalReleaseOptions{
    GraphID: graphID, TargetID: targetID,
    Host: hostPort, History: historyReader, Compilation: compilePort,
})
receipt, err := owner.Request(releaseRequest)
provider, err := owner.ServingProvider(resourceOnlyProvider)
coordinator, err := managed.NewLocalServingCoordinator(managed.ServingOptions{
    GraphID: graphID, TargetID: targetID, InstanceID: instanceID,
    Authority: owner, Provider: provider, Observation: owner,
})
```

`ReleaseJournalProjector` requires an independent complete graph projector
returning `ReleaseGraphProjection{Graphs, ProjectionVersions, BlobRefs}`.
It composes actual release sections and sorted actual `receipts_index` with those
graph/fact sections. There is no empty-graph fallback.
Journal lifetime custody belongs to the caller. One authoritative owner/
coordinator per graph/target and shared graph-authoring serialization are
deployment obligations, not a distributed lock fabricated by the adapter.
Concurrent/reentrant owner operations refuse as busy instead of deadlocking.
Benign Journal lock contention refuses without poisoning healthy custody;
actual closed/unknown readiness and append failures require recovery.

The serving-provider wrapper checks current preparation authority, performs
resource-only preparation outside the owner boundary, and persists the supported
terminal ready/failed fact. Returned generations must be fresh, unclaimed and
match accepted coordinates. A reused generation is refused without retiring its
existing serving owner. Successful terminal facts do not themselves publish a
pointer. Late ready facts may remain durable history, but cannot replace newer
desired intent.

The final owner fence requires actual local prepared custody plus durable ready
provenance, exact desired intent, current compilation/approval/admission and
Journal write readiness. Local attempt routing uses immutable Go value identity,
not a serialized credential; synthetic/replay-only attempts without actual
owner-recorded preparation and adoption refuse.

## Optional observation and unknown recovery

Generic callers without `ServingOptions.Observation` keep their existing
`serving`/`superseded`/`failed` behavior. The optional typed port is:

```go
type ServingObservationPort interface {
    Observe(context.Context, ServingAttempt) error
}
```

Observation runs **after adoption and outside the pointer gate**. Only a completed
matching durable append/read returns `local_observed`. Any observer failure
returns `serving_unobserved`, retaining the actual pointer/resources and one
bounded retry obligation. Replacement preparation/swaps freeze while pending;
current requests may still lease that pointer under independent current authority.
No error rolls back an already published pointer or retires it as a failed
candidate. `RetryObservation(ctx)` retries that exact adopted attempt.
Shutdown closes retry admission and joins in-flight observation.

`FileJournal.AssertWritable()` is synchronous and I/O-free. Unknown publication,
closed custody and callback/concurrent reentry refuse. It changes no append,
durability or recovery semantics. A different authoring component's
`commit_unknown` also freezes release swaps through this shared Journal guard.
An append failure freezes the release owner and requires physical recovery.

Close/reopen Journal custody first, then call **the same owner instance's**
`Recover(reopenedJournal)` before `coordinator.RetryObservation(ctx)`.
Recovery requires the same `store_id` and an extension of the last confirmed
hash prefix, then replays actual accepted facts. A backup clone with a different
store ID is not a rebind shortcut. Recovery never reconstructs a pointer.
An observation published before `commit_unknown` is matched/deduplicated by full
release/instance/boot/generation/revision/definition/manifest identity, not emitted
again. A newly constructed owner/coordinator starts with no local active pointer,
regardless of old-boot observations in replay.

## Remaining obligations

Concrete GraphOwner authoring commands, full graph/fact projection, independently
verified history, shared serialization and current Host context/admission/approval/
target compilation are required application ports, not simulated defaults.
Exact/latest background scheduling, fleet coordination, Host wire adapters,
durable Task/run-envelope provenance, shared resources/caches and the next query
service scope remain separate. This closes the bounded physical release/
observation port, not the user's entire WP00-WP15 goal or managed acceptance.
