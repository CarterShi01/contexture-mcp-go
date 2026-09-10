# Changelog

All notable changes will be documented here. This project follows Semantic
Versioning once public releases begin.

## Unreleased

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
