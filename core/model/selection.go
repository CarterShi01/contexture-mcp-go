package model

import (
	"errors"
	"fmt"
	"strings"
)

// RootSelection is all roots or an exact immutable root-name allowlist.
type RootSelection struct{ names map[string]struct{} }

// AllRoots returns the compatibility projection containing every root.
func AllRoots() RootSelection { return RootSelection{} }

// OnlyRoots constructs an exact non-empty root selection.
func OnlyRoots(names ...string) (RootSelection, error) {
	if len(names) == 0 {
		return RootSelection{}, errors.Join(ErrInvalidSelection, errors.New("a root selection must name at least one root"))
	}
	selection := RootSelection{names: map[string]struct{}{}}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "/") {
			return RootSelection{}, errors.Join(ErrInvalidSelection, errors.New("root selections contain non-empty root refs only"))
		}
		selection.names[name] = struct{}{}
	}
	return selection, nil
}

// Resolve validates names against an Index's root set.
func (selection RootSelection) Resolve(index *Index) (RootSelection, error) {
	if selection.names == nil {
		return selection, nil
	}
	roots := map[string]struct{}{}
	for _, root := range index.Roots() {
		ref, _ := index.RefOf(root)
		roots[ref] = struct{}{}
	}
	for name := range selection.names {
		if _, ok := roots[name]; !ok {
			return RootSelection{}, fmt.Errorf("%w: unknown root %q", ErrInvalidSelection, name)
		}
	}
	return selection, nil
}

// ContainsRef reports whether a complete root tree contains ref.
func (selection RootSelection) ContainsRef(ref string) bool {
	if selection.names == nil {
		return true
	}
	root := strings.Split(ref, "/")[0]
	_, ok := selection.names[root]
	return ok
}

// RequireRef refuses an address outside this root projection.
func (selection RootSelection) RequireRef(ref string) error {
	if selection.ContainsRef(ref) {
		return nil
	}
	return fmt.Errorf("reference %q is outside this request's root surface", ref)
}

// Intersect returns the monotonic intersection of two root projections.
func (selection RootSelection) Intersect(other RootSelection) (RootSelection, error) {
	if selection.names == nil {
		return other, nil
	}
	if other.names == nil {
		return selection, nil
	}
	result := RootSelection{names: map[string]struct{}{}}
	for name := range selection.names {
		if _, ok := other.names[name]; ok {
			result.names[name] = struct{}{}
		}
	}
	if len(result.names) == 0 {
		return RootSelection{}, errors.Join(ErrInvalidSelection, errors.New("the effective root selection is empty"))
	}
	return result, nil
}
