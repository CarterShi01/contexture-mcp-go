# Releasing

A public Git tag is a Go module release. Do not push a version tag unless
conformance status is exactly `conformant`; the release workflow enforces this
gate.

## First public stable release

1. Start from current `master` with a clean worktree. Complete the release
   scope, move user-visible changes into the planned `1.0.0` changelog section
   (and date it when the tag is created), and update both READMEs and handbooks.
2. Set `conformance/specification.json` to the honestly achieved status.
3. Run `go mod tidy`, confirm `gofmt -l` reports no files, then run
   `go run ./internal/conformancecheck`, `go test -race ./...`, `go vet ./...`,
   and `go run ./internal/releasecheck v1.0.0`. Confirm the external-module
   release check imports every documented public package from a temporary
   module and the worktree remains clean.
4. Record current real-Host verification from the exact release binary.
   Historical candidate runs do not satisfy the v1.0.0 Host gate.
5. Only after a maintainer separately authorizes public release, trigger the
   `Release Go module` workflow with `v1.0.0` and the exact authorization
   phrase `RELEASE APPROVED`. Its metadata check requires the tag to equal
   `v` plus the built `contexture.Version` (currently `v1.0.0`), conformance
   status `conformant`, and the unsuffixed module path
   `github.com/CarterShi01/contexture-mcp-go`.
   Go major version 1 deliberately has no `/v1` module-path suffix.
6. Approve the protected `go-module` environment. The workflow rechecks the
   source and creates an annotated tag on the exact verified commit.
7. Ask the public Go proxy to resolve the immutable version:

   ```bash
   GOPROXY=https://proxy.golang.org go list -m \
      github.com/CarterShi01/contexture-mcp-go@v1.0.0
   ```

8. Verify the module page on `pkg.go.dev`.

Never reuse, force-move, or delete-and-recreate a published version tag. Major
version 2 will require `/v2` in the module and import paths.
