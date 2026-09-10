# Host verification

## Contexture 0.12 Go candidate

Recorded 2026-09-10 with Claude Code 2.1.133 against commit `23cc42f`.

| Client | Version | Result |
| --- | --- | --- |
| Claude Code | 2.1.133 | passed after two Host-discovered fixes |
| Codex CLI | 0.153.0 | blocked before inference: not logged in |
| Official Go MCP client | 1.7.0 | passed in the automated suite |

Claude Code was started with an isolated MCP configuration that launched a
freshly built Go demo binary through WSL. All built-in tools were disabled; the
explicit available and allowed set contained only `contexture_discover`,
`contexture_open`, `contexture_invoke_read_only`, and `contexture_invoke` from
that server.

Real Host use found two defects before the passing run:

1. revalidating default stdio options mistook effective host, port, and path
   defaults for explicitly stated HTTP fields;
2. scalar and array Tool results were placed directly in `structuredContent`,
   while MCP requires that field to be an object.

Commit `23cc42f` makes option validation idempotent and encodes every structured
result as an object, retaining object results directly and wrapping other values
under `result`. Focused official-SDK tests cover string and array results, and
the complete conformance, race, external-module, and vet gates pass.

After those fixes, the model navigated the maintained Kubernetes incident,
called Pod status, previous logs, events, and `crash_loop_runbook`, and reported:

- `CrashLoopBackOff`, 14 restarts, and `ready=false`;
- `DB_URL` missing from the process environment;
- exit code 1, explicitly distinguished from OOM/137;
- add the key to the projected ConfigMap or Secret, then roll out;
- do not restart or delete the Pod first.

The successful result used seven model turns, returned no permission denial,
and used no repository, shell, filesystem, or web evidence.

Codex CLI was available through the pinned ephemeral npm package, but
`codex login status` returned `Not logged in`. No model request was made and no
pass or product failure is claimed for that row.

See [verify_claude_code.md](verify_claude_code.md) and
[verify_codex.md](verify_codex.md) for reproduction and cleanup.
