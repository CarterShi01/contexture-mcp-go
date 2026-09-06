# Contributing

Thank you for improving the Go binding.

## Set up

Use Go 1.25 or newer:

```bash
go mod download
go test -race ./...
go vet ./...
```

Run `gofmt` on every changed Go file. Create changes from `master`. Do not
commit credentials, local environment files, coverage output, or binaries.

## Contracts

- English is the primary language for code, comments, errors, API docs, commits,
  and review discussion. User-facing Simplified Chinese docs are translations.
- The root package and future `internal` compiler must remain independent of
  every MCP or HTTP SDK.
- Language-native APIs are encouraged; observable behavior must follow the
  pinned Contexture specification and golden fixtures.
- Update `conformance/specification.json` only after reviewing the upstream
  specification diff and proving newly claimed rules with tests.
- Do not create a version tag while conformance status remains `scaffold`.

Public API changes require tests and a changelog entry. Wire-level changes also
require a conformance-fixture review in the reference repository.
