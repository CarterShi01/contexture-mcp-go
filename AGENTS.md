# Repository instructions

These instructions apply to the entire Go repository.

- English is the first language for code, identifiers, comments, errors, API
  documentation, release notes, and authoritative documentation. Simplified
  Chinese documents are translations.
- The normative contract is Contexture 0.12 at the revision pinned in
  `conformance/specification.json`. Start porting work at the reference
  repository's `spec/porting/TERRA_GOAL.md`; specification, fixtures, and golden
  files outrank Python mechanisms.
- This repository is a scaffold implementing rule 1 only. Do not imply broader
  conformance or create a release tag until every claimed rule has execution
  evidence.
- Never update expected golden bytes, weaken assertions, skip tests, or delete
  tests to obtain green CI. Copied fixtures or golden files are not execution
  evidence.
- Keep the root package SDK-neutral. Use `context.Context` for request-local
  facts; never emulate Python `ContextVar` with goroutine-local or process-global
  mutable state. Use pointer-backed node identity and defensive copies of slices
  and maps. Never depend on map iteration order.
- Every concurrent implementation must pass the race detector. Keep one kernel
  concept or one Host adapter per review unit.

Run the full gate with:

```bash
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
```

GPT-5.6 Terra high owns the continuous porting goal. Decisions in the reference
repository's `spec/porting/PORTING_BRIEF.md` are approved defaults and should be
implemented without pausing merely because a module is high-risk. This is one
uninterrupted Goal: stage reports, green commits, local blockers, and test
failures never trigger a handoff. Diagnose, reduce or reorder work, and continue
all independent tasks. Record specification/Python conflicts, required
normative or golden changes, unrecorded product-semantic decisions, and failures
unresolved after expanded diagnosis for one final GPT-5.6 Sol audit. End only
when both ports are complete or one irreducible blocker prevents every remaining
task after all safe alternatives are exhausted. Luna is limited to mechanical
work with an exact test oracle.
