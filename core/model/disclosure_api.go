package model

import (
	"errors"
	"fmt"
	"strings"
)

// DisclosureAPI is the model-facing progressive-navigation half of
// Contexture's fixed gateway. It owns no transport and never constructs a
// Runtime: the underlying Disclosure remains the owner of cards, projection,
// selected roots, and disclosure telemetry.
//
// The optional reserved refs are person-controlled model-navigation targets.
// They remain reachable through OpenForPerson, while model Open returns the
// completed agent-facing refusal after selection authorization has succeeded.
type DisclosureAPI struct {
	disclosure *Disclosure
	reserved   map[string]struct{}
}

// NewDisclosureAPI constructs the independently installable navigation half
// over a bound or disclosure-only Disclosure. Reserved refs are copied and
// canonicalized; they are intentionally checked only at model Open time, so a
// request ceiling remains the first non-leaking authorization boundary.
func NewDisclosureAPI(disclosure *Disclosure, reserved ...string) (*DisclosureAPI, error) {
	if disclosure == nil || disclosure.index == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI requires a Disclosure"))
	}
	entries := make(map[string]struct{}, len(reserved))
	for _, ref := range reserved {
		entries[canonicalRef(ref)] = struct{}{}
	}
	return &DisclosureAPI{disclosure: disclosure, reserved: entries}, nil
}

// Tools returns the immutable ordered disclosure gateway.
func (*DisclosureAPI) Tools() []GatewayTool { return DisclosureGatewayTools() }

// Index returns the immutable compiled graph underlying this disclosure view.
// Callers that need whole-graph facts should use its explicit query methods;
// model disclosure itself remains progressively projected through this API.
func (api *DisclosureAPI) Index() *Index {
	if api == nil {
		return nil
	}
	return api.disclosure.index
}

// SelectedGraph returns the request-selected read-only graph facts for this
// view. It shares the same monotonic root calculation as Discover and Open.
func (api *DisclosureAPI) SelectedGraph(requested RootSelection) (*SelectedGraph, error) {
	if api == nil || api.disclosure == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI must not be nil"))
	}
	selection, err := api.disclosure.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	return NewSelectedGraph(api.disclosure.index, selection)
}

// Discover returns one routing-card level for every selected model-visible
// root. It is stateless: a prior Open cannot change a future projection.
func (api *DisclosureAPI) Discover(requested RootSelection) (map[string][]map[string]any, error) {
	if api == nil || api.disclosure == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI must not be nil"))
	}
	return api.disclosure.Discover(requested)
}

// Inspect atomically validates and projects 1 through 32 unique trimmed refs.
func (api *DisclosureAPI) Inspect(refs []string, requested RootSelection) (CompiledContext, error) {
	if api == nil || api.disclosure == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI must not be nil"))
	}
	invalidBatch := func() error {
		return &RefusedError{Message: fmt.Sprintf("%s requires from 1 through 32 unique non-empty refs.", InspectGatewayName)}
	}
	if len(refs) < 1 || len(refs) > 32 {
		return nil, invalidBatch()
	}
	selection, err := api.disclosure.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	normalized := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, value := range refs {
		ref := strings.TrimSpace(value)
		if ref == "" {
			return nil, invalidBatch()
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, &RefusedError{Message: fmt.Sprintf("%s names a ref more than once: %q.", InspectGatewayName, ref)}
		}
		seen[ref] = struct{}{}
		normalized = append(normalized, ref)
	}
	// Prevalidate the complete batch before rendering or reporting any item.
	for _, ref := range normalized {
		if err := selection.RequireRef(ref); err != nil {
			return nil, err
		}
		root := strings.Split(canonicalRef(ref), "/")[0]
		_, prompt := api.disclosure.promptRoots[root]
		_, reserved := api.reserved[canonicalRef(ref)]
		if prompt || reserved {
			return nil, &RefusedError{Message: TakenByPersonMessage(ref)}
		}
		if _, err := api.disclosure.index.Find(ref); err != nil {
			return nil, recoverDisclosureError(err)
		}
	}
	payload, err := api.disclosure.Inspect(normalized, requested)
	if err != nil {
		return nil, recoverDisclosureError(err)
	}
	for _, ref := range normalized {
		reportInspection(api.disclosure.telemetry, ref)
	}
	return payload, nil
}

// Open progressively discloses one node through the model door. Ordinary
// lookup facts become a RefusedError with a usable recovery sentence; a root
// outside the selected surface remains typed and non-leaking.
func (api *DisclosureAPI) Open(ref string, requested RootSelection) (map[string]any, error) {
	if api == nil || api.disclosure == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI must not be nil"))
	}
	selection, err := api.disclosure.EffectiveSelection(requested)
	if err != nil {
		return nil, err
	}
	if err := selection.RequireRef(ref); err != nil {
		return nil, err
	}
	if _, reserved := api.reserved[canonicalRef(ref)]; reserved {
		return nil, &RefusedError{Message: TakenByPersonMessage(ref)}
	}
	value, err := api.disclosure.Open(ref, requested)
	return value, recoverDisclosureError(err)
}

// OpenForPerson progressively discloses one node through the person door.
// It bypasses only model reservations and Prompt-root visibility, never a root
// selection ceiling. Lookup failure remains an agent-readable RefusedError so
// a Host's prompt/goto surface does not invent a second recovery vocabulary.
func (api *DisclosureAPI) OpenForPerson(ref string, requested RootSelection) (map[string]any, error) {
	if api == nil || api.disclosure == nil {
		return nil, errors.Join(ErrInvalidDeclaration, errors.New("DisclosureAPI must not be nil"))
	}
	value, err := api.disclosure.OpenForPerson(ref, requested)
	return value, recoverDisclosureError(err)
}

// OpenForAPerson is the retained Go compatibility spelling for OpenForPerson.
func (api *DisclosureAPI) OpenForAPerson(ref string, requested RootSelection) (map[string]any, error) {
	return api.OpenForPerson(ref, requested)
}

func recoverDisclosureError(err error) error {
	if err == nil {
		return nil
	}
	var outside *RootOutsideSelectionError
	if errors.As(err, &outside) {
		return err
	}
	var failure *NodeNotFoundError
	if errors.As(err, &failure) {
		return &RefusedError{Message: UnresolvedMessage(failure), Cause: err}
	}
	return err
}
