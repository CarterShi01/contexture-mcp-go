# ADR 022 — INSPECT is a non-activating structural view

**Status:** accepted

**Date:** 2026-09-08

**Reference revision:** `471d0f75c6be0e5cff104f0d0c61f10957da792a`

## Context

ROUTE supports broad candidate discovery and ACTIVE adopts a node's actionable
instructions. Planning also needs a middle operation: compare a shortlist's immediate structure without mixing several competing procedures or creating a process-member obligation for candidates that were only evaluated.

An authored details field is rejected because it would duplicate descriptions
or instructions and become another source of drift.

## Decision

Contexture has three disclosure levels:

```text
ROUTE    choose a shortlist from each node's description
INSPECT  compare a shortlist through one structural level
ACTIVE   adopt one node's instructions and actionable facets
```

The fixed read-only `contexture_inspect` gateway accepts an atomic batch of 1–32
unique, non-empty refs after trimming. It validates the complete batch against
the same selection, Prompt-root, and person-reservation boundary as model open
before rendering or recording any item.

Each item contains only:

- the requested node's pure routing card;
- pure routing cards for direct containment members;
- pure routing cards for declared uses.

A pure routing card is exactly kind, name, description, and canonical ref.
INSPECT preserves request and declaration order. It never contains instructions, Tool schema or read-only classification, process-member designation or framework contract, content, invocation result, or recursive expansion. It invokes no business Tool.

Inspection telemetry is separate from ACTIVE Role/Skill use. Collectors may
implement the inspection extension; exporter error or panic cannot change a
successful inspection.

## Consequences

- Runtime MCP applications expose five fixed gateway Tools.
- Disclosure-only applications expose discover, inspect, and open.
- Existing ROUTE and ACTIVE payloads and the existing `contexture inspect` CLI
  replay command remain unchanged.
- Breadth uses one bounded batch; depth requires another explicit inspection of
  a returned ref.
- Uses never widen the selected surface, and process members appear only as plain Role member cards.
