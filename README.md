# Contexture for Go

[简体中文](README.zh-CN.md)

Go implementation of Contexture, a progressive-disclosure framework for
building MCP applications whose capabilities remain navigable as they grow.

Implementations:
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[Specification](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **Status: all applicable 0.15 source and behavioral-test rows are verified;
> release remains guarded.** This repository ships native project commands,
> inspection, generated applications, real MCP transports, authenticated
> request-local path selection, REST, and the maintained demo. Remaining parity
> work is documentation and release-asset review plus a clean-checkout release
> audit. Do not create the first module tag until those gates pass.

Public packages are the module root, `core/model`, `server`, `server/surface`,
`web`, `demo`, `inspection`, and `cli`. The release check creates a separate Go
module and imports each package through a local module replacement.

## Node model

The public SDK-neutral root facade exposes the closed node set. Its source is
split by responsibility under `core/model/`:

- `Role` is a responsibility and containment boundary.
- `Skill` is procedure followed by a model.
- `Tool` is an executable capability with one typed Binding.
- `Node` is the sealed interface implemented by their pointer types.

### Optional Publication

Set `Role.Publication` to a lazy factory returning `*contexture.Publication`
when finishing that Role requires separately disclosed procedure and equipment:

```go
Publication: func() contexture.Node {
	return &contexture.Publication{
		Name: "publish", Description: "Preserve the result.",
		Instructions: "Review evidence, obtain approval, then save the result.",
	}
},
```

A Publication remains kind `role` on the wire and can hold ordinary Roles,
Skills, Tools, and an explicitly nested Publication. It is finishing equipment,
not an alternative child branch or automatic callback. Opening the owner adds
its card and the framework closing contract; opening the Publication only
discloses procedure. Only explicit Tool invocation has effects, and blocked,
failed, or approval-pending publication must be reported honestly.

`NewTool` and `NewToolWithSchema` are backed by `core/model/binding.go`. The
root facade intentionally exposes declarations only; MCP and web adapters are
separate imports.

## Example

```go
package main

import (
	"context"
	"log"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type statusInput struct {
	Service string `json:"service"`
}

func main() {
	status, err := contexture.NewTool(
		"status",
		"Return one service status.",
		true,
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
				Name:         "operations",
				Description:  "Operate services.",
				Instructions: "Inspect before changing anything.",
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{
						Name:         "diagnose",
						Description:  "Diagnose an unhealthy service.",
						Instructions: "Read status and explain the evidence.",
						Uses:         []string{"operations/status"},
					}
				}},
				Tools: []contexture.Factory{func() contexture.Node { return status }},
			}
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	index, err := contexture.Compile(application)
	if err != nil {
		log.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(
		index, contexture.AllRoots(), contexture.AllRoots(), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		log.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(
		server.Identity{Name: "operations", Version: "0.1.0"}, gateway,
	)
	_ = adapter.Server // Connect it to an official MCP SDK transport chosen by the Host.
}
```

Business Tools remain behind Contexture's five fixed gateway Tools:
`contexture_discover`, `contexture_inspect`, `contexture_open`,
`contexture_invoke_read_only`, and `contexture_invoke`. `contexture_inspect`
atomically compares 1–32 unique refs through pure routing cards for each target,
its direct members, and declared uses. It activates nothing, invokes nothing,
and discloses no instructions, execution facets, or Publication contract. The root
package is SDK-neutral; `server` owns the official MCP Go SDK and `web` owns
explicit `net/http` REST adapters. Request-local facts use `context.Context`, and
application dependencies use `Channels` with reverse-order cleanup.

`contexture.Contexture(declaration)` is the named public alias for
`contexture.DeclareApplication`; both create the same lazy application declaration.

## Inspect agent-visible context

The MCP gateway method `Gateway.Inspect` is separate from the existing CLI
command below. The CLI keeps replaying complete agent-visible sessions; its
behavior and flags are unchanged.

`contexture inspect` replays the exact instructions, discovery payload, and
progressive-disclosure cards produced by the native implementation. It starts
no MCP transport. Use it after changing a declaration, before connecting a
Host:

```bash
go run ./cmd/contexture inspect operations --all --summary
go run ./cmd/contexture inspect operations/runbook --read
go run ./cmd/contexture inspect --all --json > contexture-trace.json
```

`--all` walks every visible ref once in breadth-first role order; `--summary`
keeps the cost and host-limit checks while omitting payload bodies; `--json`
creates a stable trace for CI comparison. `--read` additionally calls only a
no-argument, read-only content Tool, so use it only when that local read is
intended. With no project configuration or explicit target, `inspect` replays
the bundled demo and reports that fallback on stderr.

## Create and run a project

The native command creates the single supported `project` template. Its
generated application owns its local workflows, so run them from the new
project rather than from this repository:

```bash
contexture new operations --template project
cd operations
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --summary
go run ./cmd/assistant call operations/ping --input '{"target":"local"}'
```

`contexture new` refuses an existing destination and unknown templates. The
generated `check` validates without opening application dependencies; `call`
uses the same runtime Binding as a served application and requires
`--allow-write` for a writing Tool.

## Host configuration

Keep host configuration as a pointer to the server command, rather than a copy
of an application's declared context. `server.Launch` produces the exact
formats for Claude Code, Cursor, and Codex:

```go
launch := server.Launch{
	Name: "operations", Command: "go",
	Args: []string{"run", "./cmd/assistant", "serve"},
}
fmt.Print(server.ClaudeCodeConfig(launch)) // .mcp.json or .cursor/mcp.json
fmt.Print(server.CodexConfig(launch))      // stanza for ~/.codex/config.toml
```

`server.CLICommands(launch)` returns safely quoted `claude mcp add` and `codex
mcp add` commands. The same API works for applications with a custom stdio
entry point.

For streamable HTTP, `HeaderSurfaceSelector` reads the canonical
`Contexture-Select` header. A comma-separated direct path such as
`team/notebook-editor`, or a terminal wildcard such as `team/*`, promotes each
resolved match to a request-local surface root. Selection never widens runtime
or identity ceilings. The legacy `Contexture-Roots` header remains supported,
but new integrations should send `Contexture-Select`. Invalid selectors return
a safe JSON-RPC `-32602` response with the request ID preserved.

## Run a project

The Go CLI runs the static application declared by a project's
`cmd/assistant/main.go`; it never imports arbitrary source based on a string.

```bash
go run ./cmd/contexture new my-context
cd my-context
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --read --summary
go run ./cmd/assistant serve                    # MCP stdio; blocks
go run ./cmd/assistant serve --transport streamable-http
```

From inside a generated project, an installed `contexture` command forwards
the same `check`, `list`, `inspect`, `call`, and `serve` workflows to that
application. `call` refuses a writing Tool unless `--allow-write` is explicit;
it accepts JSON from `--input` or `--input-file`, never both.

Run the maintained deterministic Kubernetes application with:

```bash
go run ./cmd/contexture demo                    # MCP stdio; blocks
go run ./cmd/contexture demo --transport streamable-http
```

## Development checks

Requires Go 1.25 or newer.

```bash
git clone https://github.com/CarterShi01/contexture-mcp-go.git
cd contexture-mcp-go
go mod download
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
```

The port targets Contexture Specification 0.14 at the immutable revision in
[`conformance/specification.json`](conformance/specification.json). Pinned
fixtures and golden outputs are stored under `conformance/`; tests construct and
run the Go implementation before comparing its observations with them. These
checks validate implemented behavior, not a full-product release claim.

## Repository map

Read the [Go handbook](docs/handbook.md), its
[Simplified Chinese translation](docs/handbook.zh-CN.md), and the
[architecture document](docs/architecture.md). Real Host evidence and
reproduction steps are recorded in [Host verification](docs/verification/hosts.md).

```text
facade.go        public declaration-facing SDK-neutral facade
core/foundation/ errors and pinned specification identity
core/mcpinterface/ SDK-neutral Prompt, Resource, and gateway declarations
core/model/      compiler, disclosure, runtime, lifecycle, and bindings
server/          MCP SDK adapter and publication surface
web/             explicit HTTP route and REST adapter
conformance/    pinned specification identity, fixtures, and golden data
```

English is the primary project language. Simplified Chinese documentation is a
maintained translation.

## License

Apache-2.0. See [LICENSE](LICENSE).
