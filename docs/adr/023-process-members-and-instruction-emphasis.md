# ADR 023 — Process members and framework instruction emphasis

**Status:** accepted

**Date:** 2026-09-10

**Supersedes:** ADR 021, Role Publication and instruction composition.

## Context

The 0.14 `Publication` extension encoded only the finishing half of a more
general mechanism: a Role may need a separately disclosed capability subtree
for a phase of its work and a framework-owned instruction requiring the Agent
to enter it. Preparation has the same shape. The old appended paragraph also
looked like ordinary business prose, so its different authority was unclear.

## Decision

`Publication` and `Role.Publication` are removed without compatibility aliases.
Go exposes distinct strong `PreProcess` and `PostProcess` Role types and matching
lazy `Role.PreProcess` and `Role.PostProcess` slots. The signatures reject an
ordinary Role or the opposite process kind. Neither type may be an application
root, and both remain kind `role` on the wire.

Containment order is pre-process, children, post-process, Skills, Tools.
Branches and the routing roster remain children only. Every process subtree
uses ordinary uniqueness, cycle, canonical address, Channels, Binding,
selection, manager snapshot, Prompt audience, and disclosure-only rules.
Containment never inherits a process member; nesting must be explicit.

ACTIVE owner instructions are composed as:

1. fixed PreProcess framework contract, when present;
2. unchanged business `Role.Instructions`;
3. fixed PostProcess framework contract, when present.

Each contract uses one stable head and tail and a `>>> REQUIRED:` action line.
It names the existing `contexture_open` gateway with the actual ref supplied by
the view, in single quotes. The designated member also appears as an ordinary
Role card. If either card is unavailable, the whole open is rejected rather
than emitting a dangling or hidden ref. ROUTE and INSPECT expose neither the
designation nor contract.

`BindingInstruction(source, body, action)` is public for application-owned hard
rules. It refuses empty sources and sources beginning with `contexture`,
case-insensitively. The framework composer remains private because only this
package may claim framework authority.

## Consequences

This is a breaking pre-1.0 API and payload change. Business subclass names such
as `TaskPublication` may remain, but their base becomes `PostProcess` and the
owner slot becomes `PostProcess`. There is no new node kind, gateway, callback,
start/finish event, or workflow runtime. Opening a process Role only discloses
procedure; only explicit Tool invocation has effects. A procedure written for
an Agent is not an enforced invariant, so guarantees still belong in the Tool
that can refuse a violating call.

Go evidence is in `process_test.go`, `process_integration_test.go`,
`public_api_test.go`, and the external module release consumer. The normative
source is immutable Python revision
`cda2721c7c40128cd0b7eef990e5909edabd3b17`, specification 0.16.
