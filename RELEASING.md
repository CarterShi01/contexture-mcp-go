# Releasing

A public Git tag is a Go module release. Do not push a version tag while
conformance status is `scaffold`; the release workflow enforces this gate.

## First public candidate

1. Start from current `master` with a clean worktree. Complete the release
   scope, move user-visible changes into a dated changelog section, and update
   both READMEs and handbooks.
2. Set `conformance/specification.json` to the honestly achieved status.
3. Run `go mod tidy`, `go run ./internal/conformancecheck`, `gofmt`,
   `go test -race ./...`, and `go vet ./...`. Confirm the external-module
   release check imports every documented public package from a temporary
   module and the worktree remains clean.
4. Trigger the `Release Go module` workflow with a version such as
   `v0.1.0-rc.1`.
5. Approve the protected `go-module` environment. The workflow rechecks the
   source and creates an annotated tag on the exact verified commit.
6. Ask the public Go proxy to resolve the immutable version:

   ```bash
   GOPROXY=https://proxy.golang.org go list -m \
     github.com/CarterShi01/contexture-mcp-go@v0.1.0-rc.1
   ```

7. Verify the module page on `pkg.go.dev`.

Never reuse, force-move, or delete-and-recreate a published version tag. Major
version 2 will require `/v2` in the module and import paths.
