package server_test

import (
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func selectorIndex(t *testing.T) *contexture.Index {
	t.Helper()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "roots", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "diagnose", Description: "Diagnose.", Instructions: "Read."}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "release", Description: "Release.", Instructions: "Read."}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestHeaderRootSelectorAttenuatesButNeverWidensTheCeiling(t *testing.T) {
	index := selectorIndex(t)
	selector := server.HeaderRootSelector{Ceiling: func(principal *contexture.Principal) (contexture.RootSelection, error) {
		if principal == nil || !principal.HasScope("support") {
			return contexture.OnlyRoots("diagnose")
		}
		return contexture.AllRoots(), nil
	}}
	selection, err := selector.Select(index, map[string]string{"contexture-roots": "diagnose"}, contexture.NewPrincipal(contexture.PrincipalOptions{Scopes: []string{"support"}}))
	if err != nil || !selection.ContainsRef("diagnose/tool") || selection.ContainsRef("release/tool") {
		t.Fatalf("attenuated selection = %#v, %v", selection, err)
	}
	selection, err = selector.Select(index, nil, nil)
	if err != nil || !selection.ContainsRef("diagnose/tool") || selection.ContainsRef("release/tool") {
		t.Fatalf("ceiling selection = %#v, %v", selection, err)
	}
	if _, err := selector.Select(index, map[string]string{server.RootsHeader: "release"}, nil); err == nil || !strings.Contains(err.Error(), "effective root selection is empty") {
		t.Fatalf("widened selection error = %v", err)
	}
}

func TestHeaderRootSelectorRejectsMalformedAndOversizedRequests(t *testing.T) {
	index := selectorIndex(t)
	selector := server.HeaderRootSelector{MaxLength: 4, MaxRoots: 1}
	if _, err := selector.Select(index, map[string]string{server.RootsHeader: "diagnose"}, nil); err == nil || !strings.Contains(err.Error(), "character limit") {
		t.Fatalf("length error = %v", err)
	}
	selector.MaxLength = 100
	if _, err := selector.Select(index, map[string]string{server.RootsHeader: "diagnose,release"}, nil); err == nil || !strings.Contains(err.Error(), "root limit") {
		t.Fatalf("count error = %v", err)
	}
	if _, err := selector.Select(index, map[string]string{server.RootsHeader: "missing"}, nil); err == nil || !strings.Contains(err.Error(), "unknown root") {
		t.Fatalf("unknown root error = %v", err)
	}
}
