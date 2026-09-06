package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
		exp, ok := expiration(principal.Claims()["exp"])
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: principal.Subject(), Scopes: principal.Scopes(), Expiration: time.Unix(int64(exp), 0), Extra: map[string]any{"contexture.principal": principal}}, nil
	}, &auth.RequireBearerTokenOptions{Scopes: append([]string(nil), value.RequiredScopes...), ResourceMetadataURL: value.Resource + "/.well-known/oauth-protected-resource"}), nil
}

// expiration accepts the ordinary Go representations of a JWT NumericDate.
// A verifier is native Go code, so its decoded or constructed claims should not
// have to use encoding/json's float64 representation just to cross this SDK
// boundary. Non-finite numbers remain invalid rather than becoming a surprising
// Unix timestamp through conversion.
func expiration(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) > uint64(math.MaxInt64) {
			return 0, false
		}
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > uint64(math.MaxInt64) {
			return 0, false
		}
		return int64(typed), true
	case float32:
		return floatingExpiration(float64(typed))
	case float64:
		return floatingExpiration(typed)
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer, true
		}
		if decimal, err := typed.Float64(); err == nil {
			return floatingExpiration(decimal)
		}
	}
	return 0, false
}

func floatingExpiration(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < math.MinInt64 || value > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
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
