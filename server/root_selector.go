package server

import (
	"fmt"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

// RootsHeader is the optional request header that attenuates a root surface.
// It is never an authentication assertion: an application-owned ceiling may
// reduce it further using the verified request Principal.
const RootsHeader = "Contexture-Roots"

// RootCeiling derives the maximum roots a verified identity may receive.
type RootCeiling func(*contexture.Principal) (contexture.RootSelection, error)

// RootSelector resolves an immutable root surface from request facts.
type RootSelector interface {
	Select(index *contexture.Index, headers map[string]string, principal *contexture.Principal) (contexture.RootSelection, error)
}

// FixedRootSelector exposes one transport-independent root surface.
type FixedRootSelector struct{ Selection contexture.RootSelection }

// Select returns the fixed selection after validating it against this application.
func (selector FixedRootSelector) Select(index *contexture.Index, _ map[string]string, _ *contexture.Principal) (contexture.RootSelection, error) {
	return selector.Selection.Resolve(index)
}

// HeaderRootSelector reads an exact comma-separated root allowlist from one
// request header, then intersects it with an optional identity-derived ceiling.
type HeaderRootSelector struct {
	Header    string
	Ceiling   RootCeiling
	MaxLength int
	MaxRoots  int
}

// Select resolves the requested roots and never lets a header widen a ceiling.
func (selector HeaderRootSelector) Select(index *contexture.Index, headers map[string]string, principal *contexture.Principal) (contexture.RootSelection, error) {
	if index == nil {
		return contexture.RootSelection{}, fmt.Errorf("Contexture root selector needs an Index")
	}
	header, maxLength, maxRoots := selector.Header, selector.MaxLength, selector.MaxRoots
	if header == "" {
		header = RootsHeader
	}
	if maxLength == 0 {
		maxLength = 4096
	}
	if maxRoots == 0 {
		maxRoots = 128
	}
	requested := contexture.AllRoots()
	if raw, present := headerValue(headers, header); present {
		if len(raw) > maxLength {
			return contexture.RootSelection{}, fmt.Errorf("%s exceeds the %d-character limit", header, maxLength)
		}
		parts := strings.Split(raw, ",")
		if len(parts) > maxRoots {
			return contexture.RootSelection{}, fmt.Errorf("%s exceeds the %d-root limit", header, maxRoots)
		}
		var err error
		requested, err = contexture.OnlyRoots(parts...)
		if err != nil {
			return contexture.RootSelection{}, err
		}
	}
	requested, err := requested.Resolve(index)
	if err != nil || selector.Ceiling == nil {
		return requested, err
	}
	ceiling, err := selector.Ceiling(principal)
	if err != nil {
		return contexture.RootSelection{}, err
	}
	ceiling, err = ceiling.Resolve(index)
	if err != nil {
		return contexture.RootSelection{}, err
	}
	effective, err := requested.Intersect(ceiling)
	if err != nil {
		return contexture.RootSelection{}, err
	}
	return effective.Resolve(index)
}

func headerValue(headers map[string]string, wanted string) (string, bool) {
	for name, value := range headers {
		if strings.EqualFold(name, wanted) {
			return value, true
		}
	}
	return "", false
}
