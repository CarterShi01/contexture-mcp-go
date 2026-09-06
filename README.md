# Contexture for Go

[简体中文](README.zh-CN.md)

Go implementation of Contexture, a progressive-disclosure framework for
building MCP applications whose capabilities stay navigable as they grow.

Implementations:
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[Specification](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **Status: scaffold, not released.** The module currently establishes its
> permanent path, language-native declaration boundary, dependency layering,
> CI, and conformance lock. It is not yet a usable replacement for the Python
> implementation and no version tag should be published.

## Design boundary

Contexture keeps business declarations separate from Host adapters:

```text
application declarations
        ↓
SDK-neutral root package and internal compiler
        ↓
compile → disclose → invoke
        ↓
MCP and optional HTTP surfaces
```

The root package must not import an MCP SDK. The `server` package is the
adapter seam and currently proves integration with the official MCP Go SDK
without claiming that Contexture's fixed gateway has been implemented.

## Development

Prerequisites are Go 1.25 or newer and Git.

```bash
git clone https://github.com/CarterShi01/contexture-mcp-go.git
cd contexture-mcp-go
go mod download
go test -race ./...
go vet ./...
```

The current declaration seam is deliberately small:

```go
application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
    Name: "operations",
    Roots: []contexture.RoleFactory{func() contexture.Role {
        return contexture.Role{
            Name:         "operations",
            Description:  "Handle routine operational questions.",
            Instructions: "Inspect first.",
        }
    }},
})
```

Declaring the application does not call its root factories. Compilation, Index
construction, disclosure, and invocation are upcoming milestones.

## Conformance

The binding targets Contexture Specification 0.12 at the immutable revision in
[`conformance/specification.json`](conformance/specification.json). The status
file lists implemented rules explicitly; copied prose or an incomplete golden
run does not count as conformance.

The normative contract remains in the
[reference repository](https://github.com/CarterShi01/contexture-mcp/tree/master/spec).
Go APIs should follow Go conventions while producing the same observable
behavior and protocol payloads.

## Repository map

```text
*.go            public SDK-neutral authoring package
internal/       future compiler and release-only tooling
server/         MCP and future Host adapters
conformance/    pinned specification identity and implementation status
docs/           architecture and implementation plans
```

## Language policy

English is the primary project language. Source comments, identifiers, errors,
API documentation, release notes, and the authoritative README are English.
Simplified Chinese user documentation is maintained as a translation.

## License

Apache-2.0. See [LICENSE](LICENSE).
