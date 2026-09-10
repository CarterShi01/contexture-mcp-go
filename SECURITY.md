# Security policy

## Supported versions

The project has no published versions yet. Security fixes apply to current
`master` until the first release line is declared.

## Report a vulnerability

Use GitHub private vulnerability reporting for
`CarterShi01/contexture-mcp-go`. Do not open a public issue or include
production credentials, tokens, or personal data in a report.

Include the affected revision, Go version, transport, minimal reproduction,
impact, and suggested mitigation when known. An acknowledgement should arrive
within seven days; fix and disclosure timing depends on severity and upstream
impact.

## Security boundary

Progressive disclosure is not business authorization. Applications remain
responsible for permission decisions based on verified identity and domain
policy. Streamable HTTP beyond loopback requires an explicit authentication or
anonymous-access decision plus appropriate Host and Origin allowlists. REST
routes are explicit allowlists and reuse the validated Tool Binding; they do
not create an arbitrary ref dispatcher.

Reports involving the MCP Go SDK, Go standard library, or another dependency
should name the upstream advisory when known.
