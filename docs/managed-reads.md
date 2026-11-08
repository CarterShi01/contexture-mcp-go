# Native managed reads (bounded candidate)

Import `github.com/CarterShi01/contexture-mcp-go/managed`. This optional native
slice implements authorized ROUTE Catalog metadata, consistent model paging,
and definition-metadata committed-batch pull watch. It is not a negotiated
managed wire profile, a durable graph owner, an installed Host/CLI API, release
watch, or product-parity acceptance. Legacy Catalog/static output is unchanged.

## Required trusted ports

There is **no default authority**. Carry authenticated identity with
`managed.WithIdentity(ctx, identity)` on every operation.

- `DefinitionReadPort.Read(ctx, request, dispatch)` must capture retained verified
  `GraphIndex`/revision, **current** index/`graph.catalog` scope, identity,
  authority revision and store incarnation under one shared durable-owner read
  fence. Invoke dispatch exactly once synchronously. Historical grants never
  authorize reads. Propagate retention, blob and corruption failures.
- `ServingReadPort` requires an existing native `ManagedRuntime`,
  `StoreOrigin(ctx)`, and **`Fence(ctx, dispatch)`**. Unlike single-threaded
  synchronous TypeScript rendering, Go needs an explicit Host-shared fence to
  serialize current identity, authority and store-incarnation changes through
  response rendering. It must share the durable owner's admission serialization.
  Runtime `RequestAuthority.Verify`, identity lookup and `StoreOrigin` must be
  safe inside that fence; do not reacquire a non-reentrant owner lock. Lock order
  is Host/owner fence, then existing coordinator pointer fence, then local read
  state. Do not perform I/O or reenter serving from dispatch or these callbacks.
- `CommittedBatchReadPort.Read(ctx, graphID, dispatch)` must supply a single
  immutable verified committed cut containing snapshot, watermark,
  `RetainedAfter`, current `graph.watch` identity/scope/origin and bounded
  `ReadBatches(ctx, after, bounds)`. Group **all same-graph facts in one durable
  record** into one final-state batch. Include the exact retained before state,
  monotonic complete positions, last fully examined position and honest overflow
  attestation. Never substitute an uncommitted/live head or silently skip history.

The services check coordinates, dispatch usage and resulting errors. This is not
proof that an injected port actually reads a durable Journal or serializes its
authority correctly. No production implementation of these read ports is added.
`ManagedRuntime.WithReadAuthorization` is a thin native read adapter over its
existing current `RequestAuthority` and pointer fence, not another authority.
Its Host-shared admission fence must span the call.

## Catalog

```go
signer, err := managed.NewCursorSigner(hostOwnedKey) // at least 32 bytes
catalog, err := managed.NewManagedCatalogService(managed.CatalogOptions{
    PageStoreOptions: managed.PageStoreOptions{Signer: signer},
    Definition: durableOwnerReads,
    Serving: &servingReads, // optional; complete trusted port required
})
request := managed.CatalogRequest{
    GraphID: "graph", Source: managed.CatalogHead,
}
first, err := catalog.Start(ctx, request, 30*time.Second)
// first.JSON() is a defensive copy; read EACH initial stream cursor.
page, err := catalog.Continuation(ctx, nodesCursor, request, managed.CatalogNodes)
```

Every result is immutable `FrozenJSON`. `CatalogHead`, `CatalogRevision` (requires
`Revision`) and `CatalogServing` distinguish authoring from exact serving
generation. Head/revision pages retain immutable metadata across head advances
and history pruning. Serving pages retain the actual generation across swaps,
but every continuation reenters **current** admission and checks current stable
IDs. A retained snapshot is a lifetime pin, never permission.

Start returns explicit source/graph/revision, `detail: metadata_only`, profile,
authorized `view_digest`, random snapshot identity and separate initial cursors
for `nodes`, `containment`, `uses`. Serving additionally identifies target,
instance, boot, generation and selected locale. Continuations return
items/complete and a next cursor when incomplete.

Cards contain node ID, kind/name/description, optional localized serving title,
and `projected_root`. Both endpoints of each containment/uses edge must survive
authority and kind filtering. No hidden parent IDs, paths, raw ordinals,
instructions, schemas, application text, publications, global digests or counts
are returned. View digests hash only the authorized material and descriptor,
not revision, definition, manifest or Journal coordinates.

## Model paging

Construct `NewManagedPagingService(PagingOptions{..., Serving: servingReads})`.
`PagingRequest.Operation` is `PageRoutes`, `PageMembers`, `PageUses` or `PageOpen`.
All except routes require an actual generation-qualified `Reference`.
`Selection` contains subtree anchors; nil means the default view, an explicit
empty slice means no selection. Profiles normalize defensive copies.

Start returns `routes_page`, `members_page`, or `uses_page`; open returns both
independent member/uses pages. `Continuation(ctx, token, sameRequest, relation)`
returns only that relation, with the same serving coordinates. Routes are
projected model roots; members follow placement order and uses declared order.
The conservative paging ceiling is `GraphIndex.ModelMayOpen` plus current native
open authority. Serving Catalog instead uses model routing visibility, including
person-reserved route-visible nodes.

Open is Role-only. `owner_active` contains an indivisible program/card and
designations. Actual authorized `process_cards` accompany **every** independent
continuation. Kind filters attenuate relation cards, not required processes.
Missing process cards or template-reference targets refuse composition.
Relation/process cards remain ROUTE metadata; no execution/schema blobs exist.
Large programs/cards/compositions return explicit `too_large`, not truncation,
skipping or an empty infinite cursor. Paging does not invoke business handlers.

## Committed-batch watch

Construct `NewManagedWatchService(WatchOptions{Reader: committedReader,
Signer: signer})`, then `Start(ctx, WatchRequest{GraphID: ...}, ttl)`.
Watch selection is **exact stable-ID attenuation**, not Catalog subtree anchors.

Start's complete authorized snapshot and initial opaque cursor share one durable
cut. `Poll(ctx, inputCursor, sameRequest)` returns atomic descriptors with
`batch_id`, authorized-view `facts_digest`, full UTF8 payload `size`, `seqcursor`
and either complete inline `payload` or `authorized_fetch_ref`.
`Fetch(ctx, ref, sameRequest)` returns one exact complete bounded replacement,
never chunks or a reconstruction from current head. Current admission and ID
existence gate fetched historical bytes. Retried delivered projections that can
no longer safely preserve their payload invalidate the session rather than
regrant deleted IDs or change a stable batch's payload.

Apply the complete replacement, including fetches, **before** persisting its
checkpoint. Retrying the input checkpoint is at-least-once with stable IDs/bytes
while its authority view remains valid. Hidden/no-op batches advance only the
opaque checkpoint; responses expose no examined count, global position, raw
hash, remaining count or dishonest `caught_up` flag.

Backlog overflow, handle/material saturation or slow cooperative work invalidates
the whole session with `resync_required`. Retention gaps return
`history_unavailable`; corruption propagates. Descriptor/page boundaries never
split or skip an indivisible pending batch. Inline material whose **full response
envelope** does not fit uses honest complete fetch; an oversized descriptor or
snapshot refuses with `too_large`. Synchronous work budgets detect late ports,
not preempt uncooperative code.

## Bounds, key custody and cleanup

Page defaults: 200 entries/page (maximum 1000), 64 snapshots including pending
captures, 16 MiB retained material, 10,000 projected entries, 20,000 handles,
8 MiB **full encoded UTF8 response** and maximum 60-second TTL. Every atomic item
is preflighted before publishing a usable snapshot, including later pages.
Byte limits count complete responses, delimiters and cursor strings.

Watch defaults: 64 sessions, 4096 checkpoint/fetch handles, 16 MiB retained
session/fetch/fingerprint material, 8 MiB complete payload, 10,000 projection
entries and trusted backlog bounds of 1024 batches/16 MiB. Profile defaults:
64 examined batches, 1 MiB response, 256 KiB inline, one-second cooperative work
budget. TTL maximum is 60 seconds (configurable no greater than one hour).
These are serialized material/envelope budgets, **not** process RSS or bounds on
owner-held GraphIndex/compiler/resources.

Handles use random/keyed opaque 256-bit identities plus HMAC-SHA256. No plaintext
position, authority, filter or snapshot coordinate is encoded. The Host owns
keys; `signer.Rotate(newKey)` invalidates old handles. Restart loses local handle
state even if the key persists. No consumer checkpoint persistence is provided.

Every page/poll/fetch binds actor, tenant, authority revision, current view,
selection, filters/profile, origin and expiry. Failures discard inaccessible
state; permission/not-ready continuations become generic resync diagnostics.
Unexpected port errors/panics propagate after cleanup. Host transport adapters
must redact internal error messages/stacks before exposing diagnostics.

All concurrent read state is mutex/atomic protected. Watch refuses overlapping
or reentrant operations with `resource_busy`, without background queues.
Pages own expiry timers by default; custom monotonic clocks require `Sweep`.
Watch has lazy cleanup; the Host must call `Sweep` for idle expiration.
`Shutdown` clears immediately. Shutdown read services **before** the coordinator.
TTL governs resource retention only; it never extends authority.

Evidence and remaining owner/Host obligations:
[`verification/managed-native-reads-api-20261107.txt`](verification/managed-native-reads-api-20261107.txt).
