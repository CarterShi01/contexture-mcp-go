# Verify with Claude Code

Build and validate the candidate first:

```bash
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
go build -o /tmp/contexture-go ./cmd/contexture
```

Create an isolated MCP configuration that launches `/tmp/contexture-go demo`
through WSL. Run Claude Code from outside the repository with
`--strict-mcp-config`. Set both `--tools` and `--allowed-tools` to exactly these
four names:

```text
mcp__contexture-demo__contexture_discover
mcp__contexture-demo__contexture_open
mcp__contexture-demo__contexture_invoke_read_only
mcp__contexture-demo__contexture_invoke
```

Use this task:

```text
Use only the contexture-demo MCP server to diagnose why pod
payments-api-7d9c in namespace prod keeps restarting. Start from Contexture's
disclosed Role and Skill context. Do not inspect files, use shell commands, or
use any non-MCP source. Call the disclosed evidence tools and the
crash_loop_runbook Tool. Explain the root cause, cite the exit code, and give
the smallest safe next action.
```

A pass must collect status, previous logs, events, and the runbook; identify the
missing `DB_URL`; cite exit code 1 rather than 137; recommend repairing the
ConfigMap or Secret before rollout; and reject a blind restart. The JSON result
must contain no permission denials.

The 2026-09-10 run passed with Claude Code 2.1.133 against commit `23cc42f` in
seven model turns. Real Host attempts first exposed stdio option revalidation
and non-object `structuredContent` defects; both are fixed and covered by the
official-SDK suite in that commit.

Delete the temporary binary and MCP configuration after the run.
