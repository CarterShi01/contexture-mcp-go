package foundation

import "sort"

// PrincipalOptions is the immutable request identity supplied by a Host.
// Contexture records identity facts but never turns them into authorization
// policy; an application Tool decides what it permits.
type PrincipalOptions struct {
	Subject  string
	ClientID string
	Issuer   string
	Scopes   []string
	Claims   map[string]any
}

// Principal is one request's immutable authenticated identity.
type Principal struct {
	subject  string
	clientID string
	issuer   string
	scopes   map[string]struct{}
	claims   map[string]any
}

// NewPrincipal snapshots Host-supplied identity facts before a Tool can read them.
func NewPrincipal(options PrincipalOptions) *Principal {
	scopes := make(map[string]struct{}, len(options.Scopes))
	for _, scope := range options.Scopes {
		scopes[scope] = struct{}{}
	}
	claims := make(map[string]any, len(options.Claims))
	for key, value := range options.Claims {
		claims[key] = value
	}
	return &Principal{subject: options.Subject, clientID: options.ClientID, issuer: options.Issuer, scopes: scopes, claims: claims}
}

// Subject identifies the person the caller is acting for, if known.
func (principal *Principal) Subject() string {
	if principal == nil {
		return ""
	}
	return principal.subject
}

// ClientID identifies the connecting application, if known.
func (principal *Principal) ClientID() string {
	if principal == nil {
		return ""
	}
	return principal.clientID
}

// Issuer identifies who vouched for this principal, if known.
func (principal *Principal) Issuer() string {
	if principal == nil {
		return ""
	}
	return principal.issuer
}

// HasScope reports whether the immutable identity carries one asserted scope.
func (principal *Principal) HasScope(scope string) bool {
	if principal == nil {
		return false
	}
	_, exists := principal.scopes[scope]
	return exists
}

// Scopes returns a sorted defensive copy of asserted scopes.
func (principal *Principal) Scopes() []string {
	if principal == nil {
		return nil
	}
	result := make([]string, 0, len(principal.scopes))
	for scope := range principal.scopes {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result
}

// Claims returns a defensive copy of non-standard identity facts.
func (principal *Principal) Claims() map[string]any {
	if principal == nil {
		return nil
	}
	result := make(map[string]any, len(principal.claims))
	for key, value := range principal.claims {
		result[key] = value
	}
	return result
}
