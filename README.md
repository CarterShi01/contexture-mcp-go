# Contexture for Go

[简体中文](README.zh-CN.md)

Go implementation of Contexture, a progressive-disclosure framework for
building MCP applications whose capabilities remain navigable as they grow.

Implementations:
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[Specification](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **Status: active 0.12 product port; not yet a release-ready replacement for
> Python.** The kernel has focused execution evidence, and this repository now
> has native project commands, inspection, a generated application, a real MCP
> launcher, authenticated request-local root selection, and the maintained demo.
> Remaining parity work includes complete documentation and scenario mapping, and
> a clean-checkout release audit. Do not treat this branch as full product parity.

## Node model

The public SDK-neutral root facade exposes the closed node set. Its source is
split by responsibility under `core/model/`:

- `Role` is a responsibility and containment boundary.
- `Skill` is procedure followed by a model.
- `Tool` is an executable capability with one typed Binding.
- `Node` is the sealed interface implemented by their pointer types.

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

Business Tools remain behind Contexture's four fixed gateway Tools. The root
package is SDK-neutral; `server` owns the official MCP Go SDK and `web` owns
explicit `net/http` REST adapters. Request-local facts use `context.Context`, and
application dependencies use `Channels` with reverse-order cleanup.

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

The port targets Contexture Specification 0.12 at the immutable revision in
[`conformance/specification.json`](conformance/specification.json). Pinned
fixtures and golden outputs are stored under `conformance/`; tests construct and
run the Go implementation before comparing its observations with them. These
checks validate implemented behavior, not a full-product release claim.

## Repository map

The architecture document is also available in [Simplified Chinese](docs/architecture.zh-CN.md).

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
