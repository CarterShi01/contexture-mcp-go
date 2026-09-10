# Verify with Codex

Use the same built demo binary and diagnosis task as
[verify_claude_code.md](verify_claude_code.md). The only intended difference is
Codex CLI configuration syntax; no Codex-specific Contexture artifact exists.

Register an isolated demo entry, confirm it is enabled, and run an ephemeral
read-only session that forbids shell and repository inspection. A pass requires
the same status → logs → events → runbook evidence, exit code 1, missing
`DB_URL`, and safe remediation as the Claude checklist. Remove the temporary
entry immediately afterward.

## Current status

On 2026-09-10, Codex CLI 0.153.0 was available through the ephemeral npm
package, but `codex login status` returned `Not logged in`. The run was blocked
before inference, so this is neither a product failure nor a pass. Repeat this
row after a maintainer authenticates directly in the terminal; never place a
token in repository configuration or automation output.

Official MCP client, conformance, race-enabled, and external-module checks
remain green independently of this account-level blocker.
