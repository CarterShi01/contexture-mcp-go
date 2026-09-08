package model

import (
	"fmt"
	"sort"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// SurfaceSelectionError reports malformed, empty, unknown, or contradictory
// path-selection input. errors.Is classifies it as ErrInvalidSelection.
type SurfaceSelectionError struct{ Message string }

func (err *SurfaceSelectionError) Error() string {
	if err == nil {
		return ErrInvalidSelection.Error()
	}
	return err.Message
}

func (*SurfaceSelectionError) Unwrap() error { return ErrInvalidSelection }

// OutsideSelectionError reports an otherwise valid ref outside the selected surface.
type OutsideSelectionError struct{ Ref string }

func (err *OutsideSelectionError) Error() string {
	if err == nil {
		return ErrRootOutsideSelection.Error()
	}
	return fmt.Sprintf("reference %q is outside this request's selected surface; call %s and use a ref from its result", err.Ref, DiscoverGatewayName)
}

func (*OutsideSelectionError) Unwrap() error { return ErrRootOutsideSelection }

// SurfaceSelection is all capabilities or an immutable allowlist of complete
// subtrees. Before Resolve, names may include a direct-child pattern ending in
// /*. A resolved value contains exact refs reduced to a minimal antichain.
type SurfaceSelection struct{ names map[string]struct{} }

// Compatibility type aliases retain the 0.12 API with generalized semantics.
type RootSelection = SurfaceSelection
type RootSelectionError = SurfaceSelectionError
type RootOutsideSelectionError = OutsideSelectionError

// AllSurfaces returns the compatibility projection containing every capability.
func AllSurfaces() SurfaceSelection { return SurfaceSelection{} }

// AllRoots retains the 0.12 spelling for the all-capabilities surface.
func AllRoots() RootSelection { return AllSurfaces() }

// OnlySurfaces constructs an exact non-empty path/direct-child selection.
func OnlySurfaces(selectors ...string) (SurfaceSelection, error) {
	if len(selectors) == 0 {
		return SurfaceSelection{}, selectionError("a surface selection must name at least one ref or direct-child pattern")
	}
	selection := SurfaceSelection{names: map[string]struct{}{}}
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if err := validateSelector(selector); err != nil {
			return SurfaceSelection{}, err
		}
		selection.names[selector] = struct{}{}
	}
	return selection, nil
}

// OnlyRoots retains the 0.12 spelling; exact root callers continue to work,
// while compatibility values now share the generalized path semantics.
func OnlyRoots(names ...string) (RootSelection, error) { return OnlySurfaces(names...) }

func selectionError(message string) error { return &SurfaceSelectionError{Message: message} }

// IsAll reports whether the selection contains all capabilities.
func (selection SurfaceSelection) IsAll() bool { return selection.names == nil }

// Names returns selectors before resolution and exact canonical refs afterward.
func (selection SurfaceSelection) Names() []string {
	if selection.names == nil {
		return nil
	}
	names := make([]string, 0, len(selection.names))
	for name := range selection.names {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Selectors is the canonical path-oriented spelling of Names.
func (selection SurfaceSelection) Selectors() []string { return selection.Names() }

// Resolve expands direct-child patterns, validates exact refs, and canonicalizes.
func (selection SurfaceSelection) Resolve(index *Index) (SurfaceSelection, error) {
	if index == nil {
		return SurfaceSelection{}, selectionError("a surface selection needs a compiled Index")
	}
	if selection.names == nil {
		return selection, nil
	}
	resolved := map[string]struct{}{}
	unknown := []string{}
	for _, selector := range selection.Names() {
		matches := []string{}
		switch {
		case selector == "*":
			for _, ref := range index.order {
				if !strings.Contains(ref, foundation.ReferenceSeparator) {
					matches = append(matches, ref)
				}
			}
		case strings.HasSuffix(selector, foundation.ReferenceSeparator+"*"):
			parent := strings.TrimSuffix(selector, foundation.ReferenceSeparator+"*")
			prefix := parent + foundation.ReferenceSeparator
			for _, ref := range index.order {
				if strings.HasPrefix(ref, prefix) && !strings.Contains(strings.TrimPrefix(ref, prefix), foundation.ReferenceSeparator) {
					matches = append(matches, ref)
				}
			}
		default:
			if _, err := index.Find(selector); err == nil {
				matches = append(matches, selector)
			}
		}
		if len(matches) == 0 {
			unknown = append(unknown, selector)
		}
		for _, match := range matches {
			resolved[match] = struct{}{}
		}
	}
	if len(unknown) > 0 {
		quoted := make([]string, 0, len(unknown))
		for _, selector := range unknown {
			quoted = append(quoted, fmt.Sprintf("%q", selector))
		}
		return SurfaceSelection{}, selectionError("unknown or empty Contexture selector: " + strings.Join(quoted, ", "))
	}
	return SurfaceSelection{names: canonicalSelection(resolved)}, nil
}

// ContainsRef reports whether ref is inside a selected complete subtree.
func (selection SurfaceSelection) ContainsRef(ref string) bool {
	if selection.names == nil {
		return true
	}
	for anchor := range selection.names {
		if descendantOrSelf(ref, anchor) {
			return true
		}
	}
	return false
}

// RequireRef refuses an address outside this selected surface.
func (selection SurfaceSelection) RequireRef(ref string) error {
	if selection.ContainsRef(ref) {
		return nil
	}
	return &OutsideSelectionError{Ref: ref}
}

// Intersect returns the monotonic path-aware intersection of resolved surfaces.
func (selection SurfaceSelection) Intersect(other SurfaceSelection) (SurfaceSelection, error) {
	if selection.names == nil {
		return other, nil
	}
	if other.names == nil {
		return selection, nil
	}
	for selector := range selection.names {
		if strings.Contains(selector, "*") {
			return SurfaceSelection{}, selectionError("resolve wildcard selectors against an Index before intersecting them")
		}
	}
	for selector := range other.names {
		if strings.Contains(selector, "*") {
			return SurfaceSelection{}, selectionError("resolve wildcard selectors against an Index before intersecting them")
		}
	}
	overlaps := map[string]struct{}{}
	for left := range selection.names {
		for right := range other.names {
			switch {
			case descendantOrSelf(left, right):
				overlaps[left] = struct{}{}
			case descendantOrSelf(right, left):
				overlaps[right] = struct{}{}
			}
		}
	}
	if len(overlaps) == 0 {
		return SurfaceSelection{}, selectionError("the effective surface selection is empty")
	}
	return SurfaceSelection{names: canonicalSelection(overlaps)}, nil
}

func validateSelector(selector string) error {
	segments := strings.Split(selector, foundation.ReferenceSeparator)
	if selector == "" {
		return selectionError("a surface selection must name at least one ref or direct-child pattern")
	}
	wildcards := []int{}
	for index, segment := range segments {
		if segment == "" {
			return selectionError(fmt.Sprintf("invalid Contexture selector %q: refs contain no empty segments", selector))
		}
		if strings.Contains(segment, "*") {
			wildcards = append(wildcards, index)
		}
	}
	if len(wildcards) > 0 && (len(wildcards) != 1 || wildcards[0] != len(segments)-1 || segments[len(segments)-1] != "*") {
		return selectionError(fmt.Sprintf("invalid Contexture selector %q: '*' is allowed only as the complete final segment, for example 'team/*'", selector))
	}
	return nil
}

func descendantOrSelf(ref, ancestor string) bool {
	return ref == ancestor || strings.HasPrefix(ref, ancestor+foundation.ReferenceSeparator)
}

func canonicalSelection(refs map[string]struct{}) map[string]struct{} {
	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Slice(ordered, func(left, right int) bool {
		leftDepth := strings.Count(ordered[left], foundation.ReferenceSeparator)
		rightDepth := strings.Count(ordered[right], foundation.ReferenceSeparator)
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return ordered[left] < ordered[right]
	})
	result := map[string]struct{}{}
	for _, ref := range ordered {
		covered := false
		for ancestor := range result {
			if descendantOrSelf(ref, ancestor) {
				covered = true
				break
			}
		}
		if !covered {
			result[ref] = struct{}{}
		}
	}
	return result
}

func nonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
