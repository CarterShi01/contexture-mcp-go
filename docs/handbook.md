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

The facade intentionally does not import the MCP SDK, `server`, or `web`.
Import `server` or `web` only when the declaration is ready to be compiled for
one of those Host surfaces.

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

## 3. Choose the right node

| Use | When it belongs there |
| --- | --- |
| Role | A responsibility boundary or a choice between distinct branches. |
| Skill | A model-facing procedure, ordering rule, or evidence requirement. |
| Tool | Deterministic application code that Contexture validates and invokes. |

Do not add a child Role just to organize files. A model opens every direct
member of a Role together, so Skills and Tools needed for one responsibility
usually belong under the same Role. A `Uses` reference names the Tool a Skill
needs; take the canonical ref from `list` or a disclosed card rather than
constructing it from memory.

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
