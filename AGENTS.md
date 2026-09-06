# Repository instructions

These instructions apply to the entire Go repository.

- English is the first language for code, identifiers, comments, errors, API
  documentation, release notes, and authoritative documentation. Simplified
  Chinese documents are translations.
- The normative contract is Contexture 0.12 at the revision pinned in
  `conformance/specification.json`. The reference repository's
  `spec/porting/FULL_PRODUCT_PARITY_PLAN.md` is the governing completion
  plan. The older kernel ledger remains evidence, not the product-completion
  criterion; specification, fixtures, and golden files outrank Python
  mechanisms.
- This repository is an incomplete kernel prototype with focused 0.12
  conformance evidence. Do not claim Python-product equivalence, create a
  release tag, or publish a module until the product manifest and release
  gates are verified.
- Never update expected golden bytes, weaken assertions, skip tests, or delete
  tests to obtain green CI. Copied fixtures or golden files are not execution
  evidence.
- `conformance/fixtures` and `conformance/golden` are byte-identical snapshots
  from the pinned reference revision. Tests must produce outputs through this
  implementation before comparing them with those assets.
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

GPT-5.6 Terra high owns bounded implementation slices from the product
manifest. GPT-5.6 Sol owns baseline changes, architecture, differential-test
design, and final acceptance. This is one uninterrupted Goal: stage reports,
green commits, local blockers, and test failures never trigger a handoff.
Diagnose, reduce or reorder work, and continue all independent tasks. Luna is
limited to mechanical work with an exact test oracle.
