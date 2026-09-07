# Contexture Go handbook

Contexture keeps a growing MCP application's capabilities navigable. A model
first sees small routing cards, opens one relevant branch at a time, and only
then receives a Skill procedure or a Tool schema. It does not choose models,
run an agent loop, or replace application authorization.

This handbook describes the Go binding as it exists today. Its public syntax is
native Go; its observable disclosure and gateway behavior is anchored to the
Contexture specification.

## 1. Create a project

Use Go 1.25 or newer. The native `project` template creates one static
application declaration, one read-only Tool, and a local command workflow:

```bash
contexture new operations --template project
cd operations
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --summary
go run ./cmd/assistant call operations/ping --input '{"target":"local"}'
```

`new` refuses to overwrite an existing directory and accepts only the explicit
`project` template. It is intentionally a small starting point: add only the
capabilities your application owns.

## 2. Declare one application

The generated `cmd/assistant/main.go` statically declares the application. The
declaration is inert: it does not open Channels, start an MCP server, or choose
a Host transport.

```go
package main

import (
	"context"
	"log"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type statusInput struct {
	Service string `json:"service"`
}

func main() {
	status, err := contexture.NewTool(
		"status", "Return one service status.", true,
		func(_ context.Context, input statusInput) (map[string]any, error) {
			return map[string]any{"service": input.Service, "healthy": true}, nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "operations",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{
				Name: "operations", Description: "Operate services.",
				Instructions: "Inspect evidence before changing anything.",
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{
						Name: "diagnose", Description: "Diagnose an unhealthy service.",
						Instructions: "Read status, then explain the evidence.",
						Uses: []string{"operations/status"},
					}
				}},
				Tools: []contexture.Factory{func() contexture.Node { return status }},
			}
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	_ = application
}
```

`Contexture(declaration)` is the named public alias for
`DeclareApplication(declaration)`. Both preserve lazy factories and normalize
the application name. Use a factory for every Role, Skill, and Tool:
compilation creates a fresh immutable graph snapshot from those factories.

The root `contexture` package is the SDK-neutral declaration facade. Its
native authoring inventory is `ApplicationDeclaration`, `Factory`, `Role`,
`Skill`, `Tool`, `Channels`, `Principal`, `Prompt`, and `Resource`.
`Prompt` and `Resource` are data declarations (aliases of the retained
`PromptDeclaration` and `ResourceDeclaration` spellings), not Python-style
subclass bases. Populate `Prompts` and `Resources` on `ApplicationDeclaration`
with those values. Go reports declaration and invocation categories through
error sentinels such as `ErrInvalidDeclaration`, `ErrDuplicate`, and
`ErrInvalidInput`, checked with `errors.Is`, rather than Python exception
classes. `contexture.Version` is the binding package version; it is distinct
from `contexture.SpecificationVersion`.

Go does not emulate Python exception inheritance. Instead, `errors.Is` gives
the same useful categories: `ErrContexture` is the package-wide equivalent of
`ContextureError`; `ErrModelValidation`, `ErrDeclaration`, and
`ErrDuplicateName` map the corresponding validation subclasses. The established
`ErrInvalidDeclaration` and `ErrDuplicate` remain the precise Go spellings and
also classify under those parents. `NodeNotFoundError` retains typed lookup
facts and offers `Within`, `KnownRefs`, and `DeveloperSummary`; its summary is
for developers and deliberately contains no agent recovery prose. A direct
`WrongDoorError` likewise states only the Tool facts; `Gateway` turns it into
the agent-facing next-action sentence.

`contexture.PackageName` is framework metadata (`"contexture"`), never an
application's MCP identity: the Host continues to publish the declared
application name. `contexture.ReferenceSeparator` is the canonical `"/"`
between reference segments. The four fixed model-facing names are typed
`GatewayName` values: `DiscoverGatewayName`, `OpenGatewayName`,
`InvokeReadOnlyGatewayName`, and `InvokeGatewayName`. They share one
foundation vocabulary with the model and MCP primitive layer. JSON-ready cards
and schemas use Go's native `map[string]any`/`[]any`; Contexture intentionally
does not expose a vacuous `any` alias for Python's static-only recursive JSON
types or unused `RequestId` annotation.

`Prompt`, `Resource`, and `ModelOpenPolicy` are likewise shared
foundation-owned declaration data. The retained `mcpinterface` spellings are
type aliases for compatibility, so an application model does not need to
depend on an MCP primitive package merely to validate publications.

`Principal` is an immutable request fact, not an authorization policy. Its
accessors return defensive copies for scopes and claims. Normal Go diagnostic
formatting (`%v`, `%+v`, and `%#v`) includes only subject, client ID, issuer,
and sorted scopes; claims are deliberately redacted because they may contain a
decoded token or other sensitive values.

`Prompt.ModelOpen` uses a native zero-safe policy rather than a boolean whose
zero value would accidentally reserve every Prompt target. Its default,
`contexture.ModelMayOpen`, permits both model navigation and the named person
Prompt. Set `ModelOpen: contexture.ModelReservedForPerson` to keep the target
card visible in its parent while refusing only the model's direct open; the
named Prompt and `goto` still open it for a person. Prompt and Resource facts
that can be checked without compiling the graph (blank `Opens`, supplied blank
`Name`, descriptions, URI, or an invalid policy) are rejected by
`DeclareApplication`; target existence and Resource Tool shape are checked
when publications compile.

`Index.Find` and `Index.Tool` return a typed `*contexture.NodeNotFoundError`
for failed canonical lookup. Use `errors.Is(err, contexture.ErrNodeNotFound)`
and `errors.As` to read its `Reason`, `Ref`, segment, scope, kind, wanted kind,
and known alternatives. This is the Go equivalent of Python's
`NodeNotFoundError`; `LookupFailure` constants such as `NoSuchMember` and
`WrongKind` make the facts machine-checkable without attaching Host prose.
For a direct `Runtime` call through the wrong mutation door, `errors.As` can
also read `*contexture.WrongDoorError`'s `Ref` and `ReadOnly` facts; it still
unwraps to `ErrWrongDoor`. Gateway callers receive their usual `RefusedError`
next action with that typed cause preserved.

The facade intentionally does not import the MCP SDK, `server`, or `web`.
Import `server` or `web` only when the declaration is ready to be compiled for
one of those Host surfaces.

### Typed Tool bindings and explicit schemas

`NewTool` derives a schema from a tagged input struct. `NewToolWithSchema` is
the public escape hatch for constraints such as enums, but it does not let a
published card promise inputs that Go will reject. At construction its explicit
root schema must be an object whose properties exactly match the input struct's
exported JSON-tagged fields; required names must exactly match fields without
`omitempty` or `omitzero`; and it must set `additionalProperties: false`.
This matches the binding's strict `encoding/json` decoder, preserves that
unknown-field policy in the disclosed explicit schema, and rejects drift with
`ErrInvalidDeclaration` before an Application is compiled.

The reference-derived `NewTool` card intentionally omits
`additionalProperties`, so it and its decoder accept unknown arguments, just
as the pinned Python/MCP binding does. Its required and typed fields are still
validated. Choose `NewToolWithSchema` when the contract needs strict unknown
field rejection; the explicit `additionalProperties: false` is then both
published and enforced recursively for nested input structs.

JSON tag options are scanned rather than positionally interpreted, so both
`json:"value,omitempty,string"` and `json:"value,string,omitempty"` make
`value` optional. Property-level JSON Schema constraints may narrow accepted
values, but cannot reshape the top-level object or weaken its unknown-field
policy. Explicit numeric fields must state finite `minimum` and `maximum`
bounds inside the target Go type width; an unbounded JSON number could promise
a value `encoding/json` cannot store. Array item schemas, map value schemas,
and map `patternProperties` are checked recursively for the same reason.
`patternProperties` is not valid for a struct field because strict decoding
would reject every matching unknown name. Call failures remain `ErrInvalidInput`
and never reach the handler.

Both public Tool constructors reject a blank name, a `/` in the name, or a
blank description immediately with `ErrInvalidDeclaration`; a handwritten
`Tool` literal receives the same validation at registration or compilation.
The constructor requires an explicit `readOnly` bool, while a raw Go `Tool`
literal has Go's zero value (`false`, writing) until it is compiled. This is
the native equivalent of Python's optional `read_only=False` field: it keeps
the mutation classification explicit at the normal executable-constructor
callsite. A Tool without a Binding is valid only in a disclosure-only Index;
`Tool.Binding` and runtime compilation classify an attempt to execute it as
`ErrInvalidDeclaration`. Binding schemas are defensive copies, so caller
mutation cannot alter a later `Schema` result or a compiled Tool card.

### Imperative registration

`ApplicationDeclaration` is the normal Go composition root: it keeps its
`Factory` values lazy until `Compile`. `NewControllerManager` is the separate,
intentional imperative alternative for a program that wants registration to
construct and validate a fixed forest before it declares an Application. Use
the typed `RegisterRole`, `RegisterSkill`, and `RegisterTool` methods; their
typed factory arguments make a standalone root's kind visible at the call site.
`RegisterRoot` is available only for a caller that truly learns the kind at
runtime.
Go registration accepts factories rather than Python's class-or-instance union:
wrap an existing declaration in a typed factory if needed. This makes the
construction boundary explicit and lets the manager take ownership through its
snapshot rather than exposing a mutable registered node.

The manager owns a defensive registration snapshot. Root names share one
namespace, roots are exposed as Roles then standalone Skills then standalone
Tools (preserving registration order within each kind), and duplicate identity,
cycles, wrong member groups, and invalid node facts fail at registration with
typed error sentinels. `manager.Application(name)` and `manager.Compile(name)`
snapshot the registered forest again; later caller mutation or registration
cannot change an existing Application or Index.

`NewControllerManagerWithChannels` attaches the lifecycle-safe `Channels`
interface to Applications the manager produces. `RebindChannels` affects only
future Application/Index snapshots, never a server that is already compiled or
serving. This is deliberately different from Python's arbitrary handle stamped
onto each node: Go keeps dependencies on the compiled Index and never exposes
them as model-node fields.

`WithChannels` is the transport-neutral lifecycle boundary used by Runtime and
Host serving loops. A successful scope is `Open → serve → Close → registered
cleanup in reverse order`; an Open failure skips `Close` but still unwinds every
cleanup registered before the failure. `CleanupRegistrar.Defer` is valid only
while `Channels.Open` is running and panics if retained for later registration.
If Open or serve panics, Contexture completes the applicable unwind and
re-panics that original value even when Close or cleanup also panics. Ordinary
returned errors keep their existing joined-error behavior.

### Compiled Index queries

`Index` is an immutable compilation snapshot. `Count`, `Has`, `Bound`, and
`Channels` report its captured facts; `OfKind`, `NodesWithRefs`, `Skills`,
`RolesWithRefs`, and `RolesByLevel` return defensive node snapshots in their
documented canonical order (the final method is breadth-first). `Walk` remains
the compatibility ref-only traversal, while `NodesWithRefs` is the Go-native
address/node pair form. `BindingOf` and `SchemaOf` are available only on a
bound Index; a disclosure-only Index returns an `ErrInvalidDeclaration`-typed
error instead of exposing execution data, and schemas are defensive copies.

`MatchingRefs` ranks prefix, final-segment prefix, segment prefix, then
substring matches by rank, Unicode rune length, and lexical order. Its returned total is
pre-limit. Go deliberately treats a negative limit as zero results (rather
than Python's negative-slice behavior), avoiding accidental expansion of a
bounded completion response. `Signpost` exposes only ancestor refs and their
sub-role counts; `Crossings` lists declared `Uses` edges that cross roots. Both
are structural Index facts and do not disclose a node's member cards.
`Find` and `Signpost` normalize repeated or leading/trailing `/` separators to
the same canonical address before a successful lookup.

Every compiled `Node` also exposes `Ref()`: it returns that node's canonical
address without reconstructing it from mutable display fields. `BranchesOf`
and `MembersOf` are the Go-native common-node equivalents of Python's base
node traversal methods. A Role returns direct child Role branches, and direct
members in Roles → Skills → Tools declaration-group order; Skill and Tool
return empty defensive results without needing a snapshot because they hold no
factories. Role containment queries, like `Role.Members`, require a compiled
Index snapshot and never evaluate factories. `Ref()` on an uncompiled or
typed-nil node returns an `ErrInvalidDeclaration`-typed error instead. This maps
Python's constructed object members to Go's deliberate lazy factories while
keeping canonical ref identity and compiled snapshots immutable.

`Role.Branches`, `Role.Members`, and `Role.Member` provide the corresponding
Role-local structural queries on a compiled snapshot. Branches returns only
direct child Roles; Members returns direct child Roles, Skills, and Tools in
that declaration-group order; Member resolves one cross-kind direct child and
returns a typed `NodeNotFoundError` with the Role's sorted known names when it
is absent. They return defensive snapshots and never call lazy member factories.
Calling them on an uncompiled Role returns an `ErrInvalidDeclaration`-typed
error: Go declarations deliberately store factories, unlike Python's already
constructed member lists. Every `Uses` ref is checked after the complete forest
exists; it must resolve, be unique/non-blank, and must not name its own ref.

### Request root projections

`RootSelection` is either `AllRoots()` or an exact complete-root allowlist
created with `OnlyRoots`. It rejects empty and descendant refs, resolves names
without revealing unrelated roots, and only ever narrows through `Intersect`.
`RootSelectionError` classifies invalid or contradictory selectors with
`errors.Is(err, contexture.ErrInvalidSelection)`; a valid ref outside the
effective request surface instead returns `*RootOutsideSelectionError`, which
unwraps to `ErrRootOutsideSelection` and retains `Ref`.

`NewSelectedGraph(index, selection)` provides the request-safe graph view:
`Roots`, `Walk`, `NodesWithRefs`, `Find`, `RefOf`, `ParentOf`, `ChildrenOf`,
`UsesOf`, `DependentsOf`, and `MatchingRefs` all retain canonical ordering while
excluding other roots. Cross-root `uses` and dependents are filtered rather
than disclosed. `CurrentGraph(ctx)` and `CurrentSelection(ctx)` are scoped to a
Tool invocation. Go deliberately returns `nil` from `CurrentGraph` outside an
invocation (there is no ambient graph), while `CurrentSelection` safely defaults
to all roots. A Tool handler may retain and query its one `CurrentGraph` for its
whole invocation; concurrent calls receive distinct root-projected graphs.
`HeaderRootSelector` treats `Contexture-Roots` solely as an attenuation request:
it validates unknown names without listing other roots and intersects it with
the authenticated principal's ceiling.

### Telemetry

`ApplicationDeclaration.Telemetry` optionally supplies the one usage collector
shared by `server.CompileApplication`'s disclosure, gateway, and Runtime. If
omitted, compilation creates a `MemoryTelemetry`. `NodeUsage` is a typed,
JSON-ready snapshot with `ref`, `call_count`, `error_count`, and
`last_used_at`; an unseen ref has its ref and zero counts.

The framework records only successful Role and Skill opens and actual Tool
invocations (including a failing invocation). `discover` and opening a Tool
card do not count as use. `CurrentTelemetry(ctx)` is non-nil only inside the
Tool handler's request context. Exporter errors and panics are ignored so
telemetry cannot change a business result. `MemoryTelemetry.Events()` returns
non-destructive snapshots and deliberately retains all events; use a custom
`Telemetry` implementation when bounded retention or remote export is needed.
`ReportTelemetry(telemetry, ref, failed)` is the public Go-native equivalent
of Python's `telemetry.report` for a Host boundary that owns an additional
observation; it has the same exporter-error and panic isolation. Framework
navigation and invocation do not need callers to invoke it manually.

## 3. Choose the right node

| Use | When it belongs there |
| --- | --- |
| Role | A responsibility boundary or a choice between distinct branches. |
| Skill | A model-facing procedure, ordering rule, or evidence requirement. |
| Tool | Deterministic application code that Contexture validates and invokes. |

Do not add a child Role just to organize files. A model opens every direct
member of a Role together, so Skills and Tools needed for one responsibility
usually belong under the same Role. A `Uses` reference names a declared
dependency without making it containment, and it can cross roots. Opening a
Role, Skill, or Tool projects its `Uses` targets as route cards in declaration
order; a root selection filters excluded targets rather than leaking them. A
disclosure-only Index emits structural Tool cards only: it omits both
`input_schema` and `read_only`, because it has no executable binding. Take the
canonical ref from `list` or a disclosed card rather than constructing it from
memory.

## 4. Work locally before starting a Host

| Question | Command |
| --- | --- |
| Does the declaration compile? | `go run ./cmd/assistant check` |
| Which refs exist? | `go run ./cmd/assistant list` |
| What will an agent receive? | `go run ./cmd/assistant inspect --all --summary` |
| What does one read-only Tool return? | `go run ./cmd/assistant call REF --input JSON` |

`check` compiles without opening application Channels. `call` uses the same
validated Tool Binding as serving. It permits read-only Tools by default; a
writing Tool requires the explicit `--allow-write` decision.

## 5. Inspect agent-visible context

`inspect` is a transport-free replay, not an approximation. It builds the same
instructions, discovery payloads, open cards, and recovery text used by the
native implementation.

```bash
go run ./cmd/assistant inspect operations --all --summary
go run ./cmd/assistant inspect operations/runbook --read
go run ./cmd/assistant inspect --all --json > contexture-trace.json
```

`--all` visits each visible ref once in breadth-first Role order. `--summary`
keeps token estimates and Host-limit findings but suppresses payload bodies.
`--json` is suitable for CI diffs. `--read` runs only no-argument, read-only
content Tools, so use it only when that local read is intended. Outside a
project, `contexture inspect` deliberately replays the bundled demo and writes
its notice to stderr so JSON stdout remains valid.

## 6. Offer person-controlled navigation

Prompts are for a person choosing from a Host menu, not an alternate surface
for a model. A declared `PromptDeclaration` opens one fixed ref; every served
application also publishes `goto`, whose required `ref` argument lets a person
browse a known path without asking the model to navigate first.

```go
application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
	// Roots: ...,
	Prompts: []contexture.PromptDeclaration{{
		Name:         "open-change-window",
		Opens:        "operations/change-window",
		Description:  "Open the change-window procedure.",
		ModelMayOpen: false,
	}},
})
```

Both the named Prompt and `goto` use the same person-controlled open path. Its
text identifies the ref, includes ancestor signposts without disclosing their
contents, then shows the normal node payload. `ModelMayOpen: false` reserves a
declared capability from model navigation; it does not hide it from the person
who owns the Host. Do not present a Prompt as a business Tool or duplicate its
procedure in its description.

The native MCP completion endpoint serves only `goto`'s `ref` argument and
only refs inside the current selected root surface. It returns at most 100
values; if more match, the final visible value says how many remain while the
response keeps the true `total` and `hasMore` facts. A completion request for
another Prompt or argument returns no Contexture refs.

## 7. Publish host-readable documents

A `ResourceDeclaration` gives a Host a stable URI for content that already
belongs to a Tool. It must name an argument-free, read-only Tool, so a resource
read uses the same validated binding as a local read-only call and cannot
change the world. The resource metadata is what a Host lists; the URI is what
it reads.

```go
application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
	// Roots: ... including a read-only operations/runbook Tool with no input.
	Resources: []contexture.ResourceDeclaration{{
		Opens:       "operations/runbook",
		URI:         "contexture://operations/runbook",
		Description: "The current operations runbook.",
		MIMEType:    "text/markdown",
	}},
})
```

Resources outside the Host's selected root surface are neither listed nor
readable. Do not use a Resource for a parameterized lookup, a write, or a
second implementation of a Tool; use the declared Tool through Contexture's
gateway instead.

## 8. Serve through an MCP Host

The declaration does not change when it is served. The server adapter exposes
four fixed Contexture gateway Tools; business Tools are progressively disclosed
behind them rather than registered at MCP top level.

`GatewayTools()` exposes that immutable ordered inventory: `contexture_discover`,
`contexture_open`, `contexture_invoke_read_only`, and `contexture_invoke`.
`DisclosureGatewayTools()` is the first two for a navigation-only Host, while
`ExecutionGatewayTools()` is the two invocation doors. They are framework
controls, never business `Tool` nodes. A gateway lookup or wrong-door mistake
returns a typed `RefusedError` with an agent-facing next action; its cause still
retains `NodeNotFoundError` facts for a Host. A ref outside the selected root
ceiling is deliberately different: it remains a typed
`RootOutsideSelectionError`, is not converted to a recovery suggestion, and
does not reveal excluded roots. A Prompt reserved for a person is checked only
after that same ceiling, then tells the agent to ask the user to run the Host
command rather than attempting a workaround.

`NewExecutionAPI(runtime)` exposes the independently installable execution
half without a discovery surface or any transport dependency. Its two methods,
`InvokeReadOnly` and `Invoke`, take the native typed request facts
(`context.Context`, ref, `json.RawMessage` arguments, and `RootSelection`) and
return the Tool value or error; the Contexture request accessors such as
`CurrentPrincipal`, `CurrentGraph`, `CurrentSelection`, and
`CurrentTelemetry` remain available inside the Tool handler. Ordinary lookup
and wrong-door errors become agent-facing `RefusedError` values with their
typed causes preserved; `RootOutsideSelectionError` is left unchanged. Model
calls to a `PromptRoots` entry are likewise refused only after the root ceiling
check. `ReadForHost` (and retained `ReadForAHost`) is the separate no-argument,
read-only host resource path: it shares Runtime validation and request context,
but intentionally does not apply that model-only Prompt-root refusal. A
stale host resource address is rendered as the same lookup `RefusedError`; an
unexpected non-lookup Runtime error remains typed for the Host. A
published `Prompt.ModelOpen` reservation for an otherwise ordinary node stays
a `server/surface` Host policy and is checked by the server adapter before both
model open and invoke calls; the SDK-neutral execution facade does not import
that Host layer.

```bash
go run ./cmd/assistant serve
go run ./cmd/contexture demo --transport streamable-http
```

Stdio is the default. Use `--transport streamable-http` only with a deliberate
Host and network configuration. Non-loopback startup requires corresponding
Host, origin, and anonymous-access decisions; handle server option errors
rather than weakening them.

For programmatic startup, `server.ContextureOptions{LogLevel: server.WarnLogLevel}`
controls Contexture lifecycle records. `server.ConfigureLogging` installs the
same structured logger on stderr, so MCP stdio retains exclusive ownership of
stdout.

Unless `ApplicationServerOptions.Instructions` is set, Contexture returns a
compact, breadth-first roster with the fixed navigation contract in MCP
initialization. For HTTP root selection, that roster is generated for the
selected root surface on each request; it never advertises an omitted root.

For Claude Code, Cursor, or Codex configuration, use `server.Launch`. It
renders Host configuration from the server command instead of duplicating the
application's declared context.

## 9. Publish an explicit REST surface

`web` is a separate `net/http` adapter for an application that deliberately
chooses a small REST allowlist. It never derives public paths from the
Contexture graph. Each route names one fixed Tool ref; `GET` and `HEAD` can
name only read-only Tools, while `POST`, `PUT`, `PATCH`, and `DELETE` can name
only writing Tools.

```go
surface, err := web.NewRestSurface(runtime, []web.RestRoute{
	{Method: http.MethodGet, Path: "/status", Ref: "operations/status"},
	{Method: http.MethodPost, Path: "/restart", Ref: "operations/restart", Status: http.StatusAccepted},
}, web.RestRouterOptions{
	MaxBodyBytes: 1024 * 1024, // zero selects the same 1 MiB default
	Authenticator: func(_ context.Context, request web.WebRequest) *contexture.Principal {
		if request.Headers["authorization"] != "Bearer expected" {
			return nil
		}
		return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "operator"})
	},
})
if err != nil {
	log.Fatal(err)
}

server := &http.Server{Addr: "127.0.0.1:8080", Handler: surface}
if err := surface.Serve(context.Background(), func(_ context.Context, _ http.Handler) error {
	return server.ListenAndServe()
}); err != nil && !errors.Is(err, http.ErrServerClosed) {
	log.Fatal(err)
}
```

Read arguments come from the query string: one value becomes a JSON string and
repeated values become a JSON array. Commands accept an empty body or one JSON
object and reject other media types, invalid JSON, non-object bodies, and
bodies above the configured limit. `HEAD` falls back to an explicit `GET`
route and retains its response status and headers without a body. Successful
responses are JSON with `Cache-Control: no-store`; failures are structured
`application/problem+json` responses. The optional authenticator receives a
snapshot of lower-case headers and repeated query values. A non-nil principal
is placed in the Tool's `context.Context` (`contexture.CurrentPrincipal`), and
the same HTTP snapshot is available through `web.CurrentRequest`.

The Go adapter rejects 1xx, 204, 205, and 304 route statuses at construction:
unlike the ASGI reference surface, `net/http` cannot faithfully emit those
bodyless statuses together with Contexture's required JSON representation.
Use a normal final JSON status such as 200 or 202. A Tool can deliberately
return `web.Reject("client-safe reason")` to produce a 422 `rejected` problem;
unexpected Go errors remain 500 `controller-failed` problems.

Call `surface.Serve` around the actual serving loop so Contexture Channels are
opened once for all requests and closed after the loop. The same surface may
be served again, which establishes a fresh Channel lifetime. Calling
`ServeHTTP` directly is useful for tests but does not establish that
application lifetime.

## 10. Keep the contract honest

Run the full repository gate before proposing a change:

```bash
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
```

Do not change golden outputs merely to make a binding pass: those files are a
cross-language protocol contract.

Read [architecture.md](architecture.md) for dependency boundaries,
[CONTRIBUTING.md](../CONTRIBUTING.md) for contribution rules, and
[RELEASING.md](../RELEASING.md) for the intentionally closed release process.
The module remains unpublished until all product-parity and release gates are
actually satisfied.
