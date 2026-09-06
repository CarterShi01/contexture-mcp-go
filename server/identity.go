package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// TokenVerifier is business-owned bearer validation; Contexture never issues tokens.
type TokenVerifier interface {
	Verify(context.Context, string) (*contexture.Principal, error)
}

// Auth configures one HTTP MCP resource server.
type Auth struct {
	Verifier       TokenVerifier
	Issuer         string
	Resource       string
	RequiredScopes []string
}

func (value Auth) validate() error {
	if value.Verifier == nil {
		return fmt.Errorf("Auth needs a TokenVerifier")
	}
	for name, raw := range map[string]string{"issuer": value.Issuer, "resource": value.Resource} {
		parsed, err := url.ParseRequestURI(raw)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return fmt.Errorf("Auth %s must be an absolute http(s) URL", name)
		}
	}
	return nil
}

// Middleware returns the official SDK bearer gate for an HTTP MCP handler.
func (value Auth) Middleware() (func(http.Handler) http.Handler, error) {
	if err := value.validate(); err != nil {
		return nil, err
	}
	return auth.RequireBearerToken(func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		principal, err := value.Verifier.Verify(ctx, token)
		if err != nil {
			return nil, err
		}
		if principal == nil {
			return nil, auth.ErrInvalidToken
		}
		exp, ok := principal.Claims()["exp"].(float64)
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: principal.Subject(), Scopes: principal.Scopes(), Expiration: time.Unix(int64(exp), 0), Extra: map[string]any{"contexture.principal": principal}}, nil
	}, &auth.RequireBearerTokenOptions{Scopes: append([]string(nil), value.RequiredScopes...), ResourceMetadataURL: value.Resource + "/.well-known/oauth-protected-resource"}), nil
}

// PrincipalOf recovers the business identity installed by Auth's SDK middleware.
func PrincipalOf(ctx context.Context) *contexture.Principal {
	info := auth.TokenInfoFromContext(ctx)
	if info == nil || info.Extra == nil {
		return nil
	}
	principal, _ := info.Extra["contexture.principal"].(*contexture.Principal)
	return principal
}
