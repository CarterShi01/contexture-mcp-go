// Package web adapts an explicit Contexture Tool allowlist to net/http.
//
// It is intentionally separate from the MCP Host adapter: web routes are
// selected by the embedding application, never inferred from its capability
// graph.
package web

// RestRoute is one explicit HTTP route over a fixed Contexture Tool ref.
type RestRoute struct {
	Method string
	Path   string
	Ref    string
}
