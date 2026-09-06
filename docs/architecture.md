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

The `server` package maps compiled APIs to the official MCP SDK. Its `surface`
package projects Prompts and Resources. The independent `web` package maps an
explicit route allowlist to `net/http`. Business Tools never become top-level
MCP tools; Contexture exposes a fixed navigation and invocation gateway.

## Implemented contract areas

1. Core node model, registration, validation, and immutable Index.
2. Disclosure API and exact golden discover/open/refusal payloads.
3. Typed Tool binding, `context.Context` call state, and fixed MCP gateway.
4. Prompt, Resource, completion, and selected-root behavior.
5. Channels lifecycle, identity, telemetry, HTTP, and explicit REST routes.

The first five areas have focused kernel-conformance evidence. The following
Python product modules remain absent and must be implemented before a parity or
release claim: `cli/`, `inspection.py`, `demo/`, project templates, server
assembly/options/root selection, external-consumer verification, and the
release workflow. Their absence is deliberate status, not an equivalent
language-native substitution.
