package model

import (
	"fmt"
	"sort"
	"strings"
)

// RootSelectionError reports malformed, empty, unknown, or contradictory root
// selection input. errors.Is classifies it as ErrInvalidSelection.
type RootSelectionError struct{ Message string }

func (err *RootSelectionError) Error() string {
	if err == nil {
		return ErrInvalidSelection.Error()
	}
	return err.Message
}

func (*RootSelectionError) Unwrap() error { return ErrInvalidSelection }

// RootOutsideSelectionError reports an otherwise valid ref that belongs to a
// root deliberately excluded from the current request surface.
type RootOutsideSelectionError struct{ Ref string }

func (err *RootOutsideSelectionError) Error() string {
	if err == nil {
		return ErrRootOutsideSelection.Error()
	}
	return fmt.Sprintf("reference %q is outside this request's root surface; call contexture_discover and use a ref from its result", err.Ref)
}

func (*RootOutsideSelectionError) Unwrap() error { return ErrRootOutsideSelection }

// RootSelection is all roots or an exact immutable root-name allowlist. A nil
// names map is the all-roots compatibility value; concrete names are never
// ordered by caller input, because projections retain Index declaration order.
type RootSelection struct{ names map[string]struct{} }

// AllRoots returns the compatibility projection containing every root.
func AllRoots() RootSelection { return RootSelection{} }

// OnlyRoots constructs an exact non-empty root selection.
func OnlyRoots(names ...string) (RootSelection, error) {
	if len(names) == 0 {
		return RootSelection{}, selectionError("a root selection must name at least one root")
	}
	selection := RootSelection{names: map[string]struct{}{}}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return RootSelection{}, selectionError("a root selection must name at least one root")
		}
		if strings.Contains(name, "/") {
			return RootSelection{}, selectionError(fmt.Sprintf("root selection accepts root refs only, not descendant refs: %q", name))
		}
		selection.names[name] = struct{}{}
	}
	return selection, nil
}

func selectionError(message string) error { return &RootSelectionError{Message: message} }

// IsAll reports whether the selection has the compatibility all-roots value.
func (selection RootSelection) IsAll() bool { return selection.names == nil }

// Names returns the exact root allowlist in stable lexical order. It returns
// nil for the all-roots value so callers can preserve that distinction.
func (selection RootSelection) Names() []string {
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

// Resolve validates exact names against an Index without revealing its other
// roots when a request supplied an unknown one.
func (selection RootSelection) Resolve(index *Index) (RootSelection, error) {
	if index == nil {
		return RootSelection{}, selectionError("a root selection needs a compiled Index")
	}
	if selection.names == nil {
		return selection, nil
	}
	roots := map[string]struct{}{}
	for _, root := range index.roots {
		roots[index.refByNode[root]] = struct{}{}
	}
	unknown := []string{}
	for name := range selection.names {
		if _, ok := roots[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		quoted := make([]string, 0, len(unknown))
		for _, name := range unknown {
			quoted = append(quoted, fmt.Sprintf("%q", name))
		}
		return RootSelection{}, selectionError("unknown root selection: " + strings.Join(quoted, ", "))
	}
	return selection, nil
}

// ContainsRef reports whether a complete root tree contains ref. Empty refs
// stay admissible so Index can issue its established empty-reference lookup
// diagnostic instead of turning it into a projection failure.
func (selection RootSelection) ContainsRef(ref string) bool {
	if selection.names == nil {
		return true
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment != "" {
			_, ok := selection.names[segment]
			return ok
		}
	}
	return true
}

// RequireRef refuses an address outside this root projection.
func (selection RootSelection) RequireRef(ref string) error {
	if selection.ContainsRef(ref) {
		return nil
	}
	return &RootOutsideSelectionError{Ref: ref}
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
		return RootSelection{}, selectionError("the effective root selection is empty")
	}
	return result, nil
}
