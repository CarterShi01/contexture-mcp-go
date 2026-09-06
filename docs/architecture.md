# Architecture

This binding shares semantics, not implementation structure, with the Python
reference implementation.

## Dependency direction

```text
public facade → internal model/compiler → disclosure and execution APIs
                                          ↑
                  server adapters ────────┘
```

The SDK-neutral layers own declaration validation, canonical refs, immutable
Index facts, root-selected views, disclosure, execution bindings, and lifecycle
protocols. They cannot import MCP, HTTP, CLI, or framework-specific packages.

The `server` package maps compiled APIs to the official MCP SDK and optional
Host surfaces. Business Tools never become top-level MCP tools; Contexture
exposes a fixed navigation and invocation gateway.

## Intended milestones

1. Core node model, registration, validation, and immutable Index.
2. Disclosure API and exact golden discover/open/refusal payloads.
3. Typed Tool binding, `context.Context` call state, and fixed MCP gateway.
4. Prompt, Resource, completion, and selected-root behavior.
5. Channels lifecycle, identity, telemetry, HTTP, and explicit REST routes.
6. CLI/scaffold, module-consumer tests, real Host verification, and RC release.

Each milestone must add conformance evidence before the status file advances.
