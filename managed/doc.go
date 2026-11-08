// Package managed provides a bounded native versioned-graph runtime candidate.
//
// It is separate from the legacy static runtime and does not imply managed
// profile acceptance. ReleaseAuthority and RequestAuthority are required trusted
// owner ports, not durable authority implementations or transport adapters.
// Native callables and resource adapters are trusted application code.
package managed
