# Changelog

All notable changes will be documented here. This project follows Semantic
Versioning once public releases begin.

## Unreleased

- Align with Contexture 0.16 symmetric optional Role process members:
  `PreProcess` / `PreProcess` and `PostProcess` / `PostProcess` are distinct
  strong Go types and slots with ordinary containment, selection, Channels,
  Tool Binding, manager snapshots, and disclosure-only behavior.
- Replace the framework-level `Publication` type and `Role.Publication` field
  without an alias. This is a breaking pre-1.0 change; application subclass
  names may still use publication as business vocabulary.
- Compose exact fixed framework instruction blocks before and after unchanged
  business instructions, using the actual disclosed refs and refusing owners
  atomically when either process card is unavailable.
- Add public `BindingInstruction` for application-owned hard rules while
  keeping the framework composer private.
- Align with Contexture 0.15 non-activating `INSPECT` and the fifth fixed
  `contexture_inspect` gateway over atomic batches of 1–32 unique trimmed refs.
- Return only pure routing cards for inspected nodes, direct members, and
  declared uses; omit instructions, execution facets, framework process contracts,
  recursive expansion, and invocation results while invoking nothing.
- Record inspection telemetry separately from Role/Skill activation and expose
  discover/inspect/open on disclosure-only MCP applications.
- Align with Contexture 0.14 optional Role Publications as strongly typed,
  lazily constructed finishing procedure and equipment.
- Include Publication subtrees in containment, selection, inspection, runtime,
  and disclosure-only views while excluding them from alternative branches.
- Compose the framework closing contract only when opening an owning Role;
  opening never executes Publication Tools or establishes success.
- Align with Contexture 0.13 path-selected capability surfaces: direct paths
  and terminal wildcards can promote descendants to request-local surface roots.
- Resolve wildcard selectors before applying runtime and identity ceilings, and
  build initialization instructions from the effective promoted roots.
- Parse selection headers by Unicode code point and return safe JSON-RPC
  `-32602` responses for invalid selectors before MCP SDK dispatch.
- Implement all 16 Contexture 0.12 conformance rules across declarations,
  compilation, disclosure, execution, publications, lifecycle, MCP, and REST.
- Add repository-local byte-identical fixtures and golden assets so a standalone
  clone can execute the conformance suite.
- Make compiled Index nodes defensive snapshots, bind Channels to Application
  serving, and enforce runtime/disclosure-only separation.
- Document and verify the public core/model, server/surface, demo, and web
  package facades from an external Go module.
- Complete fixed/request-selected server assembly, Prompt/Resource projection,
  and exported Kubernetes demo Tool factories.
- Fix repeated validation of default stdio options and encode scalar/array MCP
  Tool results as object-shaped structured content, both found by real Claude
  Code Host verification.
- Keep the first Go module tag guarded pending final release review.
