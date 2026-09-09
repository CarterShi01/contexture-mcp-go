package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type identityVerifier struct{ principal *contexture.Principal }

func (verifier identityVerifier) Verify(context.Context, string) (*contexture.Principal, error) {
	return verifier.principal, nil
}

type failingIdentityVerifier struct{ err error }

func (verifier failingIdentityVerifier) Verify(context.Context, string) (*contexture.Principal, error) {
	return nil, verifier.err
}

func TestAuthPublishesPathAwareProtectedResourceMetadata(t *testing.T) {
	auth := server.Auth{
		Verifier: identityVerifier{}, Issuer: "https://issuer.example",
		Resource: "https://mcp.example/mcp", RequiredScopes: []string{"mcp"},
	}
	if got := auth.ResourceMetadataURL(); got != "https://mcp.example/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("metadata URL = %q", got)
	}
	request := httptest.NewRequest(http.MethodGet, auth.ResourceMetadataURL(), nil)
	response := httptest.NewRecorder()
	auth.MetadataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metadata status = %d", response.Code)
	}
	var body struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Resource != auth.Resource || len(body.AuthorizationServers) != 1 || body.AuthorizationServers[0] != auth.Issuer || len(body.Scopes) != 1 || body.Scopes[0] != "mcp" {
		t.Fatalf("metadata = %#v", body)
	}
}

func TestAuthClaimsIssuerOverridesExplicitPrincipalIssuer(t *testing.T) {
	principal := contexture.NewPrincipal(contexture.PrincipalOptions{
		Subject: "ada", ClientID: "client", Issuer: "https://explicit.example",
		Scopes: []string{"mcp"}, Claims: map[string]any{"exp": 2_000_000_000, "iss": "https://claim.example"},
	})
	authValue := server.Auth{Verifier: identityVerifier{principal: principal}, Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp"}
	middleware, err := authValue.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	var recovered *contexture.Principal
	handler := middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recovered = server.PrincipalOf(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "https://mcp.example/mcp", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || recovered == nil || recovered.Issuer() != "https://claim.example" || recovered == principal {
		t.Fatalf("round-tripped principal = %#v, status=%d", recovered, response.Code)
	}
}

func TestAuthChallengeUsesMetadataURLAndVerifierErrorsRemainServerFailures(t *testing.T) {
	authValue := server.Auth{Verifier: identityVerifier{}, Issuer: "https://issuer.example", Resource: "https://mcp.example/mcp", RequiredScopes: []string{"mcp"}}
	middleware, err := authValue.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, authValue.Resource, nil))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), `resource_metadata="https://mcp.example/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("challenge = status %d, %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}

	failure := errors.New("identity provider unavailable")
	broken := server.Auth{Verifier: failingIdentityVerifier{err: failure}, Issuer: authValue.Issuer, Resource: authValue.Resource}
	brokenMiddleware, err := broken.Middleware()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, broken.Resource, nil)
	request.Header.Set("Authorization", "Bearer token")
	response = httptest.NewRecorder()
	brokenMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), failure.Error()) {
		t.Fatalf("verifier failure = status %d, %q", response.Code, response.Body.String())
	}
}
