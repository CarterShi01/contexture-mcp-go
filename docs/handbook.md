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

## 6. Serve through an MCP Host

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

For Claude Code, Cursor, or Codex configuration, use `server.Launch`. It
renders Host configuration from the server command instead of duplicating the
application's declared context.

## 7. Keep the contract honest

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
