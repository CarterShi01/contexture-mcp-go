package contexture_test

import (
	"fmt"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestPrincipalSnapshotsFactsAndRedactsClaimsFromFmt(t *testing.T) {
	scopes := []string{"write", "read", "read"}
	claims := map[string]any{"tenant": "acme", "ssn": "000-00-0000"}
	principal := contexture.NewPrincipal(contexture.PrincipalOptions{
		Subject: "alice", ClientID: "codex", Issuer: "https://issuer.example", Scopes: scopes, Claims: claims,
	})
	scopes[0] = "admin"
	claims["tenant"] = "mutated"

	if got := principal.Scopes(); strings.Join(got, ",") != "read,write" || principal.Claims()["tenant"] != "acme" {
		t.Fatalf("Principal did not snapshot construction facts: scopes=%#v claims=%#v", got, principal.Claims())
	}
	returnedScopes, returnedClaims := principal.Scopes(), principal.Claims()
	returnedScopes[0] = "changed"
	returnedClaims["tenant"] = "changed"
	if got := principal.Scopes(); strings.Join(got, ",") != "read,write" || principal.Claims()["tenant"] != "acme" {
		t.Fatalf("Principal did not defend returned facts: scopes=%#v claims=%#v", got, principal.Claims())
	}

	copied := *principal
	for receiver, value := range map[string]any{"pointer": principal, "value": copied} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			rendered := fmt.Sprintf(verb, value)
			if strings.Contains(rendered, "000-00-0000") || strings.Contains(rendered, "tenant") || strings.Contains(rendered, "acme") {
				t.Fatalf("%s fmt %s leaked claims: %q", receiver, verb, rendered)
			}
			if !strings.Contains(rendered, "alice") || !strings.Contains(rendered, "codex") || !strings.Contains(rendered, "https://issuer.example") || !strings.Contains(rendered, "read") || !strings.Contains(rendered, "write") {
				t.Fatalf("%s fmt %s omitted safe identity facts: %q", receiver, verb, rendered)
			}
		}
	}
	var absent *contexture.Principal
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(verb, absent); got != "<nil>" {
			t.Fatalf("nil Principal fmt %s = %q, want <nil>", verb, got)
		}
	}
}
