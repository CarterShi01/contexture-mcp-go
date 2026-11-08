# Native managed Host adapters (local candidate)

The managed specification's accepted revision remains **UNSET**. This additive
adapter's `server.ManagedHostProfile` is explicit **local** negotiation, not an
accepted managed profile, shared publication convention, Task carrier, or product
equivalence claim. The existing static server, default tool names, wire formats,
and authored bytes remain unchanged.

## Construction and trusted custody

Use `server.NewManagedHostAdapter(server.ManagedHostOptions{Identity, Runtime,
Host, Runs?, Carrier?, MCPPath?, Navigation?, MaxBodyBytes?, MaxResponseBytes?})`.
Supply the existing native `managed.ManagedRuntime` over a real prepared/adopted
`LocalServingCoordinator`. Durable graph/release owners, preparation, current
request authorization, registry resources, runs, and Journal remain caller-owned.
This adapter invents none of those authorities and accepts no static Index,
source seed, import string, wire Principal, schema-inferred publication, or
permissive authority fallback.

`server.ManagedTransportHost` is a required trusted port:

```go
Enter(context.Context, ManagedHostInput,
    func(context.Context, ManagedHostScope) error) error
Fence(context.Context, ManagedHostInput,
    func(context.Context, ManagedHostScope) error) error
```

`ManagedHostInput` carries transport (`stdio`, `http`, or `rest`), actual HTTP
request when present, and method. `ManagedHostScope` aliases
`managed.ManagedRequestOptions{Selection, PublicationSelection}`. These are
trusted stable-ID anchors, not business arguments. Nil means each category's
declared default; an explicit empty slice removes that category. Model and
publication ceilings are independent and can only shrink within a request.

Enter authenticates the actual context with `managed.WithIdentity`, including
current actor, tenant, authority, and selection. Fence supplies **fresh** facts
and synchronously serializes its callback with that authority/selection's writes.
The configured runtime's `RequestIdentity.Current` and
`RequestAuthority.Verify` must independently attest current facts at return.
Do not use immutable context identity to emulate a changing authority.

Callbacks must run once synchronously, with non-nil contexts preserving request
cancellation. Missing, repeated, retained, early-returning, and swallowed-error
dispatches refuse; entered asynchronous work is joined rather than abandoned.
No callback runs after admission closes. Trusted Host application code is not a
sandbox; remote approval alone cannot replace the local return fence.

Every operation owns one real serving lease and returns actual
`graph_id`, `revision`, `generation_uuid`, `boot_id`, `target_id`, `instance_id`,
and `locale`, never a requested reference/header. Host response authorization is
rechecked after native decoder, validator, business handler, and resource work.
Revocation suppresses a success even when a previously admitted effect finished;
it does not roll that effect back.

## Official SDK and HTTP

- `ConnectStdio(ctx, transport)` uses official SDK `mcp.StdioTransport` by
  default. An explicit SDK `mcp.IOTransport` supports owned pipes.
- `ServeStdio(ctx)` uses real OS stdio and waits for EOF/cancellation then drain.
- `Listen(port)` binds only literal `127.0.0.1` and returns the actual URL;
  port zero chooses an available port.
- `Handler()` is a mountable `http.Handler` combining official SDK streamable
  HTTP (stateless JSON) and exact REST mounts.
- `Close()` stops admission/cancels operations, joins all entered work, and only
  then closes SDK sessions/HTTP connections. A non-cooperative handler can delay
  close. No cancellation/timeout fabricates resource release.

Close the adapter before caller-owned reads/runs, coordinator, and physical
Journal. The adapter never implicitly closes these owners. External listeners
remain responsible for TLS/network admission; the handler applies cross-origin
protection, exact local-listener Host admission, protected-header cardinality,
body/result budgets, and no-store/nosniff. Unknown protected carriers and failures
produce explicit refusals, never success-shaped fallbacks or internal secrets.

HTTP requires `Contexture-Managed-Profile: <ManagedHostProfile>`.
Each handled MCP method also requires
`params._meta["contexture/profile"] = server.ManagedHostProfile`.
The server advertises this LOCAL marker as experimental. Other metadata carriers,
missing/unknown markers, malformed references, and list cursors refuse.

Only the fixed five gateway tools exist:
`contexture_discover {}`, `contexture_inspect {refs}`,
`contexture_open {ref}`, `contexture_invoke_read_only {ref, arguments?}`,
`contexture_invoke {ref, arguments?}`. Extra controls/identity/selection fields
refuse. Read-only uses the actual native binding effect. Business methods are
never standalone MCP tools.

Discover projects authorized model roots. Open returns structured native owner
instruction parts, process programs, ordered members and uses, not execution or
synthesized instructions. Missing process/template targets fail composition.
Prompt/list/get and Resource/list/read use exact declared names/URI/opens_id.
List entries carry `_meta["contexture/reference"]`; get/read require that exact
generation-qualified reference in request metadata. Prompt material is one user
text message containing the structured program as JSON. Resource text bytes and
the exact URI are preserved; non-text results refuse. No URI/name aliases are
inferred. Undeclared resource templates/completion suggestions are honestly empty.

## Exact REST publication and additive navigation

Published routes require the exact declared HTTP method + path and
`Contexture-Reference` matching the actual pinned `opens_id`. GET/HEAD pass `{}`;
write methods require bounded `application/json`. Query aliases, absolute-form
targets and encoded-path aliases refuse. Responses are
`{actualServingCoordinates..., value}`; HEAD has no body and reports the actual
generation through `Contexture-Generation`. There is no current-ref substitution.

Navigation has **no invented default paths**. Explicitly configure unique
`ManagedNavigationMount{Path, Operation}` entries:

| Method | Operation |
|---|---|
| GET | routes, prompts, resources |
| POST | inspect, open, members, uses, invoke, invoke_read_only, prompt.get, resource.read |

Bodies use the corresponding strict gateway shape; prompt.get is `{name, ref}`,
resource.read is `{uri, ref}`. Mounts cannot equal the MCP path. Any exact serving
route on a chosen mount refuses instead of being shadowed. Reference URIs, paths,
schemas, names and effects are never used to invent publication mappings.

## Protected local runs

Runs and carrier must be supplied together, for the same runtime:

- `ManagedChannelCarrier{Current}` obtains a protected native
  `*managed.RunLeaseContext` from trusted context/configuration. Any
  `Contexture-Run` header refuses.
- `ManagedHeaderCarrier{Approve}` approves/looks up each supplied bounded opaque
  `Contexture-Run` token independently **every request**, HTTP only. A bad header
  never falls back to ordinary serving. Absence means an ordinary current request.

Contexts are existing non-serializable local capabilities, not credentials.
Wire JSON cannot manufacture service/boot/actor/tenant custody. Run issuance,
create/renew/upgrade/release/TTL are the existing trusted native APIs, not new
remote endpoints. `RunLeaseService.IsFor` checks custody;
`CheckCurrent(ctx, run)` is the opt-in final expiry/release/shutdown/identity fence.
Host runs also intersect CURRENT trusted model/publication selection and policy.
Matching retained old runs may use old generations; ordinary old refs refuse.
Upgrade moves future children, never already entered children.

SDK-neutral additive `ManagedRequest` APIs are `Coordinates`, `Attenuate`,
`Routes`, `Relation`, `Open`, `ListPublications`, `Prompt`, `ReadResource`,
`Route`, `AssertHostMount`, and `ValidateReturn`. Existing general-purpose runtime
callers opt in to the last response fence; it is not imposed during their shutdown.

The native qualification includes real SDK loopback HTTP, exact mountable REST,
an OS-pipe subprocess stdio exchange/EOF drain, current revocation and scope,
retained runs/expiry/wrong boot-service, native binding/resource observations,
and a fresh external typed module consumer. Evidence lives in the uniquely
labeled verification directory, not historical 833-test receipts.
