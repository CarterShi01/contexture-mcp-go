// Package web adapts an explicit Contexture Tool allowlist to net/http.
//
// It is intentionally separate from the MCP Host adapter: web routes are
// selected by the embedding application, never inferred from its capability
// graph.
package web

import (
	"fmt"
	"net/http"
	"strings"
)

// RestRoute is one explicit HTTP route over a fixed Contexture Tool ref.
type RestRoute struct {
	Method string
	Path   string
	Ref    string
	// Status is the successful HTTP response status. It defaults to 200.
	Status int
}

func normalizeRestRoute(route RestRoute) (RestRoute, error) {
	route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
	route.Path = strings.TrimSpace(route.Path)
	route.Ref = strings.TrimSpace(route.Ref)
	if !supportedMethod(route.Method) {
		return RestRoute{}, fmt.Errorf("Contexture REST method %q is not supported", route.Method)
	}
	if !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#{") {
		return RestRoute{}, fmt.Errorf("Contexture REST path %q must be one fixed absolute path", route.Path)
	}
	if route.Path != "/" && strings.HasSuffix(route.Path, "/") {
		return RestRoute{}, fmt.Errorf("Contexture REST path %q must not end in /", route.Path)
	}
	if route.Ref == "" {
		return RestRoute{}, fmt.Errorf("a Contexture REST route must name one Tool ref")
	}
	if route.Status == 0 {
		route.Status = http.StatusOK
	}
	if route.Status < 100 || route.Status > 599 {
		return RestRoute{}, fmt.Errorf("a Contexture REST route status must be an HTTP status")
	}
	return route, nil
}

func supportedMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
