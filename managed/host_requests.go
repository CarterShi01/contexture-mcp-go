package managed

import (
	"errors"
	"slices"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

type Publication = graph.Publication

// ServingCoordinates are actual lease facts, never requested routing claims.
type ServingCoordinates struct {
	GraphID        string `json:"graph_id"`
	Revision       string `json:"revision"`
	GenerationUUID string `json:"generation_uuid"`
	BootID         string `json:"boot_id"`
	TargetID       string `json:"target_id"`
	InstanceID     string `json:"instance_id"`
	Locale         string `json:"locale"`
}

func (q *ManagedRequest) observeScope(id string, publication bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	ceiling := q.modelCeiling
	if publication {
		ceiling = q.publicationCeiling
	}
	if !slices.Contains(ceiling, id) {
		return ErrDenied
	}
	if publication {
		if q.publicationObserved == nil {
			q.publicationObserved = map[string]bool{}
		}
		q.publicationObserved[id] = true
	} else {
		if q.modelObserved == nil {
			q.modelObserved = map[string]bool{}
		}
		q.modelObserved[id] = true
	}
	return nil
}

func requestSelection(serving ServingGeneration, options ManagedRequestOptions) ([]string, []string, []string, error) {
	anchors, ceiling, err := selection(serving, options.Selection)
	if err != nil {
		return nil, nil, nil, err
	}
	definition := serving.Generation.plan.Definition()
	publications := options.PublicationSelection
	if publications == nil {
		publications = []string{}
		for _, p := range definition.Publications() {
			publications = append(publications, p.OpensID)
		}
	}
	publications = slices.Clone(publications)
	selected := map[string]bool{}
	for _, id := range publications {
		if _, err := serving.Generation.plan.Index().Get(id, "id"); err != nil {
			return nil, nil, nil, ErrDenied
		}
		selected[id] = true
	}
	for _, id := range serving.Generation.plan.Index().Preorder() {
		location, ok := serving.Generation.plan.Index().Location(id)
		if !ok {
			return nil, nil, nil, ErrNotReady
		}
		if location.HasParent && selected[location.ParentID] {
			selected[id] = true
		}
		if selected[id] {
			ceiling = append(ceiling, id)
		}
	}
	return anchors, publications, intersect(ceiling, ceiling), nil
}

func (q *ManagedRequest) Coordinates() ServingCoordinates {
	a := q.lease.Attempt
	return ServingCoordinates{a.Release.GraphID, q.lease.Generation.context.Revision, a.GenerationUUID,
		a.BootID, a.Release.TargetID, a.InstanceID, q.lease.Generation.plan.SelectedLocale()}
}

// Attenuate intersects retained scope with the trusted Host's current selection.
func (q *ManagedRequest) Attenuate(options ManagedRequestOptions) error {
	_, _, selected, err := requestSelection(q.lease.ServingGeneration, options)
	if err != nil {
		return err
	}
	_, modelIDs, err := selection(q.lease.ServingGeneration, options.Selection)
	if err != nil {
		return err
	}
	_, _, publicationIDs, err := requestSelection(q.lease.ServingGeneration,
		ManagedRequestOptions{Selection: []string{}, PublicationSelection: options.PublicationSelection})
	if err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.open {
		return ErrClosed
	}
	q.ceiling = intersect(q.ceiling, selected)
	q.modelCeiling = intersect(q.modelCeiling, modelIDs)
	q.publicationCeiling = intersect(q.publicationCeiling, publicationIDs)
	return nil
}

// ValidateReturn refreshes authorization after asynchronous business work.
// Existing general-purpose Runtime callers opt in; Host adapters must call it.
func (q *ManagedRequest) ValidateReturn() error {
	q.mu.Lock()
	for id := range q.modelObserved {
		if !slices.Contains(q.modelCeiling, id) {
			q.mu.Unlock()
			return ErrDenied
		}
		for id := range q.publicationObserved {
			if !slices.Contains(q.publicationCeiling, id) {
				q.mu.Unlock()
				return ErrDenied
			}
		}
	}
	observed := make([]string, 0, len(q.observed))
	for id := range q.observed {
		observed = append(observed, id)
	}
	q.mu.Unlock()
	slices.Sort(observed)
	for _, id := range observed {
		if _, err := q.authorized("return", id); err != nil {
			return err
		}
	}
	if len(observed) == 0 {
		_, _, err := q.runtime.authorize(q.ctx, q.identity, "return", nil)
		return err
	}
	return q.ctx.Err()
}

func (q *ManagedRequest) visible(operation string) ([]string, error) {
	q.authMu.Lock()
	defer q.authMu.Unlock()
	q.mu.Lock()
	ceiling := slices.Clone(q.ceiling)
	q.mu.Unlock()
	current, _, err := q.runtime.authorize(q.ctx, q.identity, operation, ceiling)
	if err != nil {
		return nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ceiling = intersect(q.ceiling, current)
	return slices.Clone(q.ceiling), nil
}

func (q *ManagedRequest) descriptor(id string) (map[string]any, error) {
	if _, err := q.authorized("inspect", id); err != nil {
		return nil, err
	}
	node, err := q.lease.Generation.plan.Index().Get(id, "id")
	if err != nil {
		return nil, err
	}
	ref, err := q.Reference(id)
	if err != nil {
		return nil, err
	}
	presentation, ok := q.lease.Generation.plan.Presentation(id)
	if !ok {
		return nil, ErrNotReady
	}
	out := map[string]any{"reference": ref, "kind": string(node.Kind()), "name": node.Name(),
		"title": presentation.Title, "description": presentation.Description}
	if binding, ok := q.lease.Generation.plan.Binding(id); ok {
		out["effect"], out["input_schema"] = binding.Effect(), binding.Registration().InputSchema()
	}
	return out, nil
}

func (q *ManagedRequest) Routes() (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	allowed, err := q.visible("discover")
	if err != nil {
		return result, err
	}
	roots := []any{}
	index := q.lease.Generation.plan.Index()
	q.mu.Lock()
	modelIDs := intersect(allowed, q.modelCeiling)
	q.mu.Unlock()
	projected := map[string]bool{}
	covered := map[string]bool{}
	for _, id := range modelIDs {
		projected[id] = true
	}
	for _, id := range index.Preorder() {
		loc, ok := index.Location(id)
		if !ok {
			return result, ErrNotReady
		}
		if loc.HasParent && covered[loc.ParentID] {
			covered[id] = true
		}
		open, e := index.ModelMayOpen(id)
		if e != nil {
			return result, e
		}
		if !open || !projected[id] || covered[id] {
			continue
		}
		if err := q.observeScope(id, false); err != nil {
			return result, err
		}
		d, e := q.descriptor(id)
		if e != nil {
			return result, e
		}
		roots = append(roots, d)
		covered[id] = true
	}
	return graph.FreezeJSON(map[string]any{"roots": roots})
}

func (q *ManagedRequest) Relation(reference, relation string) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	id, err := q.node(reference)
	if err != nil {
		return result, err
	}
	if _, err = q.authorized(relation, id); err != nil {
		return result, err
	}
	return q.relation(id, relation, false)
}

func (q *ManagedRequest) relation(id, relation string, publication bool) (FrozenJSON, error) {
	node, err := q.lease.Generation.plan.Index().Get(id, "id")
	if err != nil {
		return FrozenJSON{}, err
	}
	allowed, err := q.visible(relation)
	if err != nil {
		return FrozenJSON{}, err
	}
	q.mu.Lock()
	if publication {
		allowed = intersect(allowed, q.publicationCeiling)
	} else {
		allowed = intersect(allowed, q.modelCeiling)
	}
	q.mu.Unlock()
	entries := []any{}
	add := func(target string, fields map[string]any) error {
		open, e := q.lease.Generation.plan.Index().ModelMayOpen(target)
		if e != nil {
			return e
		}
		if e := q.observeScope(target, publication); e != nil {
			return e
		}
		if (!publication && !open) || !slices.Contains(allowed, target) {
			return nil
		}
		d, e := q.descriptor(target)
		if e != nil {
			return e
		}
		for key, value := range fields {
			d[key] = value
		}
		entries = append(entries, d)
		return nil
	}
	switch relation {
	case "members":
		for _, placement := range node.Members().Placements() {
			if err := add(placement.NodeID, map[string]any{"slot": string(placement.Slot), "ordinal": placement.Ordinal}); err != nil {
				return FrozenJSON{}, err
			}
		}
	case "uses":
		for _, target := range node.Uses() {
			if err := add(target, nil); err != nil {
				return FrozenJSON{}, err
			}
		}
	default:
		return FrozenJSON{}, errors.New("unknown managed relation")
	}
	return graph.FreezeJSON(entries)
}

func (q *ManagedRequest) program(id string, includeProcesses, publication bool) (map[string]any, error) {
	if err := q.observeScope(id, publication); err != nil {
		return nil, err
	}
	d, err := q.descriptor(id)
	if err != nil {
		return nil, err
	}
	plan := q.lease.Generation.plan
	node, err := plan.Index().Get(id, "id")
	if err != nil {
		return nil, err
	}
	if node.Kind() != graph.Tool {
		parts, err := plan.ResolveInstructions(id, q.GenerationUUID())
		if err != nil {
			return nil, err
		}
		material := []any{}
		for _, part := range parts {
			switch part.Kind {
			case "Text":
				material = append(material, map[string]any{"kind": "Text", "text": part.Text})
			case "NodeRef":
				if _, err := q.authorized("open", part.Reference.NodeID()); err != nil {
					return nil, err
				}
				wire, err := part.Reference.Wire()
				if err != nil {
					return nil, err
				}
				entry := map[string]any{"kind": "NodeRef", "reference": wire}
				if part.HasLabel {
					entry["label"] = part.Label
				}
				material = append(material, entry)
			default:
				return nil, ErrNotReady
			}
		}
		d["instructions"] = material
	}
	members, err := q.relation(id, "members", publication)
	if err != nil {
		return nil, err
	}
	uses, err := q.relation(id, "uses", publication)
	if err != nil {
		return nil, err
	}
	d["members"], d["uses"] = members.JSON(), uses.JSON()
	if includeProcesses {
		if processes, ok := plan.Processes(id); ok {
			for _, process := range []struct{ key, target string }{
				{"pre_process", processes.PreProcess}, {"post_process", processes.PostProcess},
			} {
				key, target := process.key, process.target
				if target == "" {
					continue
				}
				program, err := q.program(target, false, publication)
				if err != nil {
					return nil, err
				}
				d[key] = program
			}
		}
	}
	return d, nil
}

func (q *ManagedRequest) Open(reference string) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	id, err := q.node(reference)
	if err != nil {
		return result, err
	}
	if _, err := q.authorized("open", id); err != nil {
		return result, err
	}
	program, err := q.program(id, true, false)
	if err != nil {
		return result, err
	}
	return graph.FreezeJSON(program)
}

func (q *ManagedRequest) ListPublications(kind string) (result []Publication, err error) {
	op, err := q.enter()
	if err != nil {
		return nil, err
	}
	defer func() { q.leave(op, err) }()
	allowed, err := q.visible(kind + ".list")
	if err != nil {
		return nil, err
	}
	q.mu.Lock()
	allowed = intersect(allowed, q.publicationCeiling)
	q.mu.Unlock()
	definition := q.lease.Generation.plan.Definition()
	result = []Publication{}
	for _, p := range definition.Publications() {
		if p.Kind == kind && slices.Contains(allowed, p.OpensID) {
			if _, err := q.authorized(kind+".list", p.OpensID); err != nil {
				return nil, err
			}
			q.observePublication(p.OpensID)
			result = append(result, p)
		}
	}
	return result, nil
}

func (q *ManagedRequest) publication(kind, key, reference string) (Publication, error) {
	ref, err := graph.ParseReference(reference)
	if err != nil {
		return Publication{}, err
	}
	if ref.GraphID() != q.Coordinates().GraphID || ref.GenerationUUID() != q.GenerationUUID() {
		return Publication{}, ErrDenied
	}
	definition := q.lease.Generation.plan.Definition()
	for _, p := range definition.Publications() {
		value := p.Name
		if kind == "resource" {
			value = p.URI
		}
		if kind == "route" {
			value = p.Method + " " + p.Path
		}
		if p.Kind == kind && value == key && p.OpensID == ref.NodeID() {
			q.mu.Lock()
			selected := slices.Contains(q.publicationCeiling, p.OpensID)
			q.mu.Unlock()
			if !selected {
				return Publication{}, ErrDenied
			}
			_, err := q.authorized(kind, p.OpensID)
			if err == nil {
				q.observePublication(p.OpensID)
			}
			return p, err
		}

	}
	return Publication{}, ErrDenied
}

func (q *ManagedRequest) observePublication(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.publicationObserved == nil {
		q.publicationObserved = map[string]bool{}
	}
	q.publicationObserved[id] = true
}

func (q *ManagedRequest) Prompt(name, reference string) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	p, err := q.publication("prompt", name, reference)
	if err != nil {
		return result, err
	}
	program, err := q.program(p.OpensID, true, true)
	if err != nil {
		return result, err
	}
	return graph.FreezeJSON(program)
}

func (q *ManagedRequest) callPublication(p Publication, arguments any) (FrozenJSON, error) {
	binding, ok := q.lease.Generation.plan.Binding(p.OpensID)
	if !ok {
		return FrozenJSON{}, ErrNotReady
	}
	identity, err := q.authorized(p.Kind, p.OpensID)
	if err != nil {
		return FrozenJSON{}, err
	}
	resource, err := q.lease.Resources.Handle(p.OpensID)
	if err != nil {
		return FrozenJSON{}, err
	}
	return binding.Registration().Call(q.ctx, arguments, CallContext{Identity: identity, Resource: resource})
}

func (q *ManagedRequest) ReadResource(uri, reference string) (text string, err error) {
	op, err := q.enter()
	if err != nil {
		return "", err
	}
	defer func() { q.leave(op, err) }()
	p, err := q.publication("resource", uri, reference)
	if err != nil {
		return "", err
	}
	result, err := q.callPublication(p, map[string]any{})
	if err != nil {
		return "", err
	}
	text, ok := result.JSON().(string)
	if !ok {
		return "", errors.New("resource binding must return text")
	}
	return text, q.ctx.Err()
}

func (q *ManagedRequest) Route(method, path, reference string, arguments any) (result FrozenJSON, err error) {
	op, err := q.enter()
	if err != nil {
		return result, err
	}
	defer func() { q.leave(op, err) }()
	p, err := q.publication("route", method+" "+path, reference)
	if err != nil {
		return result, err
	}
	return q.callPublication(p, arguments)
}

// AssertHostMount refuses shadowing any exact publication, regardless of policy.
func (q *ManagedRequest) AssertHostMount(path string) error {
	definition := q.lease.Generation.plan.Definition()
	for _, p := range definition.Publications() {
		if p.Kind == "route" && p.Path == path {
			return errors.New("managed Host mount conflicts with declared route")
		}
	}
	return nil
}
