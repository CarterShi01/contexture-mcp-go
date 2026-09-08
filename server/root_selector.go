package server

import (
	"fmt"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

// SelectHeader is the canonical request header for path-selected surfaces.
const SelectHeader = "Contexture-Select"

// RootsHeader is the legacy root-only spelling retained for compatibility.
const RootsHeader = "Contexture-Roots"

// SurfaceCeiling derives the maximum surface a verified identity may receive.
type SurfaceCeiling func(*contexture.Principal) (contexture.SurfaceSelection, error)

// SurfaceSelector resolves an immutable surface from request facts.
type SurfaceSelector interface {
	Select(index *contexture.Index, headers map[string]string, principal *contexture.Principal) (contexture.SurfaceSelection, error)
}

// FixedSurfaceSelector exposes one transport-independent surface.
type FixedSurfaceSelector struct{ Selection contexture.SurfaceSelection }

func (selector FixedSurfaceSelector) Select(index *contexture.Index, _ map[string]string, _ *contexture.Principal) (contexture.SurfaceSelection, error) {
	return selector.Selection.Resolve(index)
}

// HeaderSurfaceSelector reads exact refs or direct-child patterns, accepts the
// legacy root header only when the canonical header is absent, and intersects
// the request with an optional identity-derived ceiling.
type HeaderSurfaceSelector struct {
	Header        string
	LegacyHeader  string
	DisableLegacy bool
	Ceiling       SurfaceCeiling
	MaxLength     int
	MaxRoots      int
}

func (selector HeaderSurfaceSelector) Select(index *contexture.Index, headers map[string]string, principal *contexture.Principal) (contexture.SurfaceSelection, error) {
	if index == nil {
		return contexture.SurfaceSelection{}, fmt.Errorf("Contexture surface selector needs an Index")
	}
	header, legacy, maxLength, maxSelectors := selector.Header, selector.LegacyHeader, selector.MaxLength, selector.MaxRoots
	if header == "" {
		header = SelectHeader
	}
	if legacy == "" && !selector.DisableLegacy {
		legacy = RootsHeader
	}
	if selector.DisableLegacy {
		legacy = ""
	}
	if maxLength == 0 {
		maxLength = 4096
	}
	if maxSelectors == 0 {
		maxSelectors = 128
	}
	raw, present := headerValue(headers, header)
	legacyRaw, legacyPresent := "", false
	if legacy != "" && !strings.EqualFold(legacy, header) {
		legacyRaw, legacyPresent = headerValue(headers, legacy)
	}
	if present && legacyPresent {
		return contexture.SurfaceSelection{}, &contexture.SurfaceSelectionError{Message: fmt.Sprintf("send either %s or %s, not both", header, legacy)}
	}
	usedHeader := header
	if !present && legacyPresent {
		raw, present, usedHeader = legacyRaw, true, legacy
	}
	requested := contexture.AllSurfaces()
	if present {
		if len(raw) > maxLength {
			return contexture.SurfaceSelection{}, &contexture.SurfaceSelectionError{Message: fmt.Sprintf("%s exceeds the %d-character limit", usedHeader, maxLength)}
		}
		parts := strings.Split(raw, ",")
		if len(parts) > maxSelectors {
			label := "selector"
			if strings.EqualFold(usedHeader, RootsHeader) {
				label = "root"
			}
			return contexture.SurfaceSelection{}, &contexture.SurfaceSelectionError{Message: fmt.Sprintf("%s exceeds the %d-%s limit", usedHeader, maxSelectors, label)}
		}
		var err error
		requested, err = contexture.OnlySurfaces(parts...)
		if err != nil {
			return contexture.SurfaceSelection{}, err
		}
	}
	requested, err := requested.Resolve(index)
	if err != nil || selector.Ceiling == nil {
		return requested, err
	}
	ceiling, err := selector.Ceiling(principal)
	if err != nil {
		return contexture.SurfaceSelection{}, err
	}
	ceiling, err = ceiling.Resolve(index)
	if err != nil {
		return contexture.SurfaceSelection{}, err
	}
	effective, err := requested.Intersect(ceiling)
	if err != nil {
		return contexture.SurfaceSelection{}, err
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

// Compatibility names share the generalized path semantics.
type RootCeiling = SurfaceCeiling
type RootSelector = SurfaceSelector
type FixedRootSelector = FixedSurfaceSelector
type HeaderRootSelector = HeaderSurfaceSelector
