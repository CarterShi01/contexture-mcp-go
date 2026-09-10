// Package model is Contexture's SDK-neutral kernel.
//
// It owns capability declarations, bindings, canonical references, selected
// graphs, disclosure, execution, lifecycle, identity context, and telemetry.
// It has no dependency on an MCP, HTTP, CLI, or other Host adapter. Go resolves
// this facade at compile time; the public root package re-exports its authoring
// concepts without needing Python's runtime lazy-attribute mechanism.
package model
