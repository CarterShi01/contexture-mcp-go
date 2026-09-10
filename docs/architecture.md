# Architecture

This binding follows the Python reference's product boundaries while using Go
packages and `context.Context` where the language requires them. It is not yet
full-product equivalent; this document distinguishes the implemented kernel
from the remaining product work.

## Dependency direction

```text
root declaration facade → core/model → core/foundation
                              ↑
                  core/mcpinterface
                              ↑
server MCP adapter ── server/surface     web HTTP adapter
```

The SDK-neutral layers own declaration validation, canonical refs, immutable
Index facts, root-selected views, disclosure, execution bindings, and lifecycle
protocols. They cannot import MCP, HTTP, CLI, or framework-specific packages.

`core/model` is the native equivalent of Python's lazy `contexture.core`
facade, while the root package re-exports its public authoring concepts. Go
resolves package symbols at compile time rather than resolving attributes on
first access; importing either SDK-neutral package does not load a Host
adapter.

The `server` package maps compiled APIs to the official MCP SDK. Its `surface`
package projects Prompts and Resources. The independent `web` package maps an
explicit route allowlist to `net/http`. Business Tools never become top-level
MCP tools; Contexture exposes a fixed navigation and invocation gateway.

## Implemented areas

1. Core node model, registration, validation, and immutable Index.
2. Disclosure API and exact golden discover/open/refusal payloads.
3. Typed Tool binding, `context.Context` call state, and fixed MCP gateway.
4. Prompt, Resource, completion, and selected-root behavior.
5. Channels lifecycle, principal context, telemetry, HTTP bearer identity,
   authenticated request-local root selection, and explicit REST routes.
6. Native CLI project generation and discovery, local check/list/inspect/call
   workflows, safe transport options, and a real MCP stdio/streamable-HTTP
   launcher.
7. The maintained Kubernetes demo, including complete reference procedures,
   resources, prompt publication, and runtime integration tests.

Every applicable 0.12 source and behavioral-test row now has focused native
evidence; maintained English and Simplified Chinese product documentation is
also mapped. Release remains intentionally closed until the Host verification
records, module metadata, and clean-checkout release audit are complete.
