# Native managed runtime candidate

Import `github.com/CarterShi01/contexture-mcp-go/managed`.

This bounded native slice is separate from the stable static runtime and its
unchanged conformance pin. It implements compiler/binding and local serving/
request/run-lifetime counterparts of WP07-WP08. It is not managed-profile
acceptance, Python/TypeScript product equivalence, or WP00-WP15 completion.
No subprocess implements runtime execution. Existing graph models, indexes,
references, counters, immutable JSON and revision snapshots are reused.
The [native FileJournal release owner](managed-releases.md) now closes the durable
release/observation port using existing candidate facts and required GraphOwner/
Host authority. It does not change the generic runtime's trust boundaries.

## Typed registry and pure compilation

```go
registration, err := managed.NewBindingRegistration(
    managed.BindingRegistrationOptions[Input]{
        BindingID: "status", Version: "1", Kind: "registered",
        Effect: "read_only", ABI: managed.NativeABI,
        ArtifactDigest: artifactDigest,
        Portability: managed.Portability{
            Kind: "native", Language: "go", Version: "1.25", Factory: "status",
        },
        InputSchema: schema,
        ValidatorContractID: "status.validate.v1",
        DecoderContractID: "status.decode.v1",
        DefaultContractID: "defaults.none",
        CoercionContractID: "coercion.none",
        Decoder: decode, Validator: validate, Handler: handle,
    },
)
```

`Decoder` has signature `func(managed.FrozenJSON) (Input, error)`;
`Validator` is `func(Input) error`; `Handler` is
`func(context.Context, Input, managed.CallContext) (any, error)`.
The typed functions are captured together without an unchecked type-erasure
cast. Their implementation/closure stability is the trusted application's
responsibility, not a sandbox guarantee. Returned values must pass the existing
strict JSON guard; custom marshalers/structs are not implicitly accepted.

`NewRegistryGeneration(build, registrations...)` pins **exact** ID/version
registrations. Its digest includes the build and all registrations, even unused
ones, sorted by Unicode scalar ID then version. Binding contracts capture
artifact, schema, ABI, effect, semantic IDs, config/schema and secret HANDLE
allowlists, resource policy and argument-free attestation. Config defaults to
an empty-only object schema. Secret references never grant a config key.
`ParseBindingDescriptor` validates inert author data; `Resolve` supplies the exact
registration's publication contract for constructing graphs with publications.

`Compile(definition, CompilerOptions)` returns an immutable `*StructuralPlan`.
Runtime compilation requires an exact registry. Disclosure-only compilation
ignores a supplied registry and requires `NoExecutionRegistryDigest`. Compilation
does not execute decoders, validators, handlers or resource acquisition. It
resolves publication evidence, locales, instruction templates and pre/post
process metadata. Processes remain equipment, never automatic business callbacks.
Manifest binding entries preserve forest order and repeated node bindings.
Executable extensions refuse.

`CompileSchema` uses the existing `jsonschema-go` evaluator, offline Draft
2020-12 only. Only supported local JSON-pointer subschema references are allowed.
Remote loading, schema IDs/anchors/dynamic references, unknown semantic keywords
and non-progressing recursive references refuse. Recursion consuming object
properties/array elements is supported. Input is copied; no coercion, insertion
of defaults or stripping of properties occurs. Formats/content keywords remain
annotations; regular expressions have Go RE2 semantics. These native schema
limitations are explicit candidate constraints, not cross-language acceptance.
The registered decoder/default/coercion/validator IDs attest the remaining native
semantics. The ABI is **`go-managed-call/v1`**, never the TypeScript or Python ABI.

Private provisional identity domains are
`contexture.binding-contract/implementation-v1` and
`contexture.registry-contract/implementation-v1`; their preimages are UTF-8
domain, NUL, then existing managed canonical JSON. Manifests use the existing
`contexture.manifest/v1` domain/shape. `ParseExecutionManifest` verifies inert
integrity evidence only; it does not install a trusted registry.

## Resource-only preparation and actual drain

```go
ready, err := managed.PrepareGeneration(ctx, plan, preparation, adapters)
```

`PreparationContext` contains graph ID, revision and fresh generation UUID.
Adapters are keyed by executable node ID and must match exactly: binding ID,
version, Go ABI and contract digest. Every attestation/function is captured and
preflighted **before any acquisition**. `Acquire` receives the context, resolved
binding, node ID and preparation context. It returns `PreparedResource{Handle,
Dispose}`. Acquisition is forest-ordered and resource-only; failures/panics must
unwind any partial work inside the failing adapter. All successfully returned
resources have explicit disposers and close in reverse order, even after another
disposer fails/panics.

`ReadyGeneration.Lease()` admits an ordinary resource lease.
`Retain()` creates a keeper whose children may still enter during retirement.
`BeginRetirement()` closes ordinary admission synchronously. `Retire(ctx)` joins
real lease/keeper drain and disposal; cancellation stops waiting, **not cleanup**.
`Wait(ctx)` can subsequently join the same owned cleanup. A closed/expired keeper
cannot admit children but never closes already entered child resources.
Resource states are `prepared`, `draining`, `retired`, `cleanup_failed`.
There is no shared-resource pool, implicit factory, background consumer or cache.

## Required trusted release owner

```go
type ReleaseAuthority interface {
    Capture() (*CapturedRelease, error)
    Fence(ServingAttempt, *ReadyGeneration, func() error) (bool, error)
}
type ServingPreparationProvider interface {
    Prepare(context.Context, ServingAttempt) (*ReadyGeneration, error)
}
```

`NewLocalServingCoordinator(ServingOptions)` requires **both** ports.
`Capture` supplies committed desired intent plus retained verified exact
historical `RevisionState`, release epoch, committed journal-sequence watermark,
and expected manifest. It is not graph head or pending intent. Epoch/sequence
cannot regress; equal epoch cannot rewrite snapshot, manifest or sequence.
New epochs require a newer committed watermark. No durable release-event schema,
approval store or release-acceptance policy is invented here.

`Fence` must synchronously recheck current admission, approvals, desired epoch
and independently current registry/manifest evidence under the **same trusted
owner boundary** as `adopt`. It must call `adopt` at most once, during that call
only, and must not retain it or invoke it on another goroutine. Saved callbacks
refuse after return. Capture/fence must not reenter the coordinator.
Provider compilation and acquisition happen outside the pointer gate. The gate
does not execute native business work, I/O, acquisition or disposal.
Ports returning errors own their partial work; providers must not return an
owned generation together with an error.

`Prepare(ctx)` returns `LocalServingResult{Attempt, Status, Error}`.
`serving` means **local pointer adoption only**, not durable observation or fleet
readiness. Attempt/boot/generation UUIDs are locally minted. Generations have
exclusive owner claims; returning an existing owner's generation refuses
without retiring that owner's LKG. Late/failed/cancelled preparation and fence
rejection preserve LKG. Capacity defaults are 2 preparations and 8 reserved/
pending retirements; saturation refuses rather than queueing indefinitely.
Cleanup failures are observable through `CleanupErrors` and block further
preparations, not requests on LKG.

`Lease()` atomically captures the pointer and acquires its resource lease.
`Shutdown(ctx)` synchronously closes admission and joins actual preparation,
leases and disposal. A cancelled wait can be joined again; it is not successful
drain.

## Explicit context identity and per-request ceilings

```go
ctx, err := managed.WithIdentity(ctx, managed.Identity{
    ActorID: actor, TenantID: tenant, AuthorityRevision: revision,
})
runtime, err := managed.NewManagedRuntime(coordinator, requestAuthority, nil)
err = runtime.Request(ctx, managed.ManagedRequestOptions{
    Selection: []string{"stable-subtree-anchor"},
}, func(request *managed.ManagedRequest) error {
    ref, err := request.Reference("stable-tool-id")
    if err != nil { return err }
    result, err := request.Invoke(ref, arguments, true) // read-only door
    _ = result
    return err
})
```

`RequestAuthority.Verify(context.Context, RequestAuthorization) ([]string,
error)` attests **current** policy at return. It receives the explicit Host
identity, current serving index, operation and detached requested stable IDs.
`RequestIdentity` can additionally provide a context-derived changing authority
revision. Even custom identity ports must match actor/tenant explicitly carried
by `WithIdentity`. There is no ambient/global/goroutine-local identity or static
runtime fallback.

A request captures one generation for references, inspection, instructions,
schema, handler and resources. Stable subtree anchors expand once into its model
plane. Initial current grants create a ceiling; every inspect/invoke intersects
that ceiling with current grants and currently model-open stable IDs. Once
attenuated, that request cannot reexpand. Pointer or authority-revision movement
across verification refuses before native dispatch. Admission is linearized
under the local pointer lock; admitted business execution holds only its actual
generation lease, never the pointer lock. Request authority must itself attest
current policy at return; a run TTL is not authorization.

Wrong graph/generation/plane references, non-executable nodes, writes through the
read-only door, raw schema failures and native validator failures reject before
handler entry. Arguments are copied before external authority calls. Go's typed
boolean read-only argument is captured by value. Captured decoder/validator/
handler dispatch has no intervening external authority call.
An escaped request rejects new calls. Scope exit calls `Finish()` and joins all
entered operations, including goroutines not joined by the action. Cancellation
is cooperative: a non-cooperative entered handler delays real lease release.

`CreatePin(ctx, options)` / `PinnedRequest(ctx, pin, action)` expose an opaque
native keeper capability for explicit integrations. A pin is routing/lifetime,
not authority. Close it explicitly after use.

## Boot-local run leases

```go
runs, err := managed.NewRunLeaseService(runtime, managed.RunLeaseOptions{})
envelope, err := runs.Create(ctx, time.Minute, managed.ManagedRequestOptions{})
envelope, err = runs.Renew(ctx, envelope, time.Minute)
envelope, err = runs.Upgrade(ctx, envelope, time.Minute) // explicit repin
err = runs.Request(ctx, envelope, action)
err = runs.Release(ctx, envelope)
```

Opaque `*RunLeaseContext` values route cryptographically minted 32-byte tokens
in a private service map, bound to actor/tenant and coordinator boot. They refuse
JSON/text serialization and are not credentials. Go TTLs/monotonic clocks use
`time.Duration`; defaults are 128 runs and maximum TTL 5 minutes. Maximum TTL
must be positive and at most 2147483647 milliseconds. Pending creates consume
capacity. Default expiry uses owned timers; a custom monotonic elapsed `Clock`
requires explicit `Sweep() (int, error)`. Clock regression/overflow fails closed.

Renew checks current authority and can only attenuate the stored ceiling.
Upgrade repins current generation using original anchors and intersects the old
ceiling, excluding newly added descendants and previously revoked IDs.
Concurrent renew/upgrade verifies the original pin is still current before
adoption. Already entered children retain their original generation. Expiry/
release closes future keeper admission, not entered child leases.

Shutdown order: `runs.Shutdown(ctx)`, then `coordinator.Shutdown(ctx)`. Both join
actual work; cancelling a wait does not fabricate completion.

## Evidence and remaining work

Focused external-package tests exercise the public managed entry point and
generic typed inference, plus deterministic acquisition/authority/preparation/
handler barriers and concurrent children under the race detector. Compiler tests
cover pure metadata and immutable registry/manifest/schema behavior.

The separate FileJournal release-owner slice implements durable release request
CAS/actor receipts/replay and observations, retaining explicit current Host and
graph-history/projection obligations. Outside these bounded slices: concrete
approval/policy integration, exact/latest background scheduling, fleet
coordination, shared resource/artifact caches, Host MCP/HTTP/
REST/Prompt/Resource run-envelope adapters, durable Task provenance, the remaining
WP00-WP15 integration/operational work, and managed-profile acceptance. No
fixtures, golden assets, conformance pin or stable static APIs are promoted.
