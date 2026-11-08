package managed

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

type CatalogStream string

const (
	CatalogNodes       CatalogStream = "nodes"
	CatalogContainment CatalogStream = "containment"
	CatalogUses        CatalogStream = "uses"
)

type CatalogRequest struct {
	GraphID   string
	Source    CatalogSource
	Revision  string
	Selection []string
	Profile   *CatalogProfile
}
type CatalogOptions struct {
	PageStoreOptions
	Definition DefinitionReadPort
	Serving    *ServingReadPort
}

// ManagedCatalogService is a ROUTE metadata projection, not legacy Catalog v1.
type ManagedCatalogService struct {
	definition DefinitionReadPort
	serving    *ServingReadPort
	pages      *snapshotPages
}

func NewManagedCatalogService(o CatalogOptions) (*ManagedCatalogService, error) {
	if o.Definition == nil && o.Serving == nil {
		return nil, errors.New("explicit trusted definition or serving read port required")
	}
	if o.Serving != nil && (o.Serving.Runtime == nil || o.Serving.StoreOrigin == nil || o.Serving.Fence == nil) {
		return nil, errors.New("complete explicit serving read port required")
	}
	pages, err := newSnapshotPages(o.PageStoreOptions)
	if err != nil {
		return nil, err
	}
	var serving *ServingReadPort
	if o.Serving != nil {
		copy := *o.Serving
		serving = &copy
	}
	return &ManagedCatalogService{o.Definition, serving, pages}, nil
}
func (s *ManagedCatalogService) Counts() ReadCounts          { return s.pages.counts() }
func (s *ManagedCatalogService) RequiresExplicitSweep() bool { return !s.pages.automatic }
func (s *ManagedCatalogService) Sweep() int                  { return s.pages.sweep() }
func (s *ManagedCatalogService) Shutdown()                   { s.pages.shutdown() }

func catalogReadRequest(r CatalogRequest) (string, PageProfile, []string, error) {
	if _, err := graph.ValidateID(r.GraphID); err != nil {
		return "", PageProfile{}, nil, readError("schema_invalid")
	}
	if (r.Source != CatalogHead && r.Source != CatalogRevision && r.Source != CatalogServing) ||
		(r.Source == CatalogRevision) != (r.Revision != "") {
		return "", PageProfile{}, nil, readError("schema_invalid")
	}
	if r.Revision != "" {
		if _, err := graph.ParseCounter(r.Revision); err != nil {
			return "", PageProfile{}, nil, readError("schema_invalid")
		}
	}
	p := PageProfile{}
	if r.Profile != nil {
		p = PageProfile(*r.Profile)
	}
	p, err := normalizePageProfile(p, "native-metadata-catalog-v1")
	if err != nil {
		return "", PageProfile{}, nil, err
	}
	anchors, err := readSelection(r.Selection)
	if err != nil {
		return "", PageProfile{}, nil, err
	}
	var revision any
	if r.Revision != "" {
		revision = r.Revision
	}
	binding, err := readEncode(map[string]any{"graph_id": r.GraphID, "source": string(r.Source),
		"revision": revision, "selection": nullableReadArray(anchors), "profile": p.asJSON()})
	return binding, p, anchors, err
}

func metadataReadMaterial(index *GraphIndex, visible []string, profile PageProfile, budget *readMaterialBudget, card func(string) (map[string]any, error)) ([]readStream, error) {
	allowed, err := readIDs(visible)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range index.Preorder() {
		node, err := index.Get(id, "id")
		if err != nil {
			return nil, err
		}
		if allowed[id] && slices.Contains(profile.Kinds, string(node.Kind())) {
			selected[id] = true
		}
	}
	streams := []readStream{{name: "nodes"}, {name: "containment"}, {name: "uses"}}
	emit := func(stream int, value any) error {
		item, err := budget.add(value, true)
		if err == nil {
			streams[stream].items = append(streams[stream].items, item)
		}
		return err
	}
	for _, id := range index.Preorder() {
		if !selected[id] {
			continue
		}
		node, _ := index.Get(id, "id")
		value := map[string]any{"kind": string(node.Kind()), "name": node.Name(), "description": node.Description()}
		if card != nil {
			value, err = card(id)
			if err != nil {
				return nil, err
			}
		}
		loc, _ := index.Location(id)
		value["node_id"], value["projected_root"] = id, !selected[loc.ParentID]
		if err := emit(0, value); err != nil {
			return nil, err
		}
		if node.Kind() == graph.Role {
			for _, placement := range node.Members().Placements() {
				if selected[placement.NodeID] {
					if err := emit(1, map[string]any{"source_id": id, "slot": string(placement.Slot), "target_id": placement.NodeID}); err != nil {
						return nil, err
					}
				}
			}
		}
		for _, target := range node.Uses() {
			if selected[target] {
				if err := emit(2, map[string]any{"source_id": id, "target_id": target}); err != nil {
					return nil, err
				}
			}
		}
	}
	return streams, nil
}
func streamsReadJSON(streams []readStream) map[string]any {
	out := map[string]any{}
	for _, stream := range streams {
		items := make([]any, len(stream.items))
		for i, item := range stream.items {
			items[i] = item.value.JSON()
		}
		out[stream.name] = items
	}
	return out
}
func servingReadCard(s servingReadSnapshot, id string) (map[string]any, error) {
	node, err := s.plan.Index().Get(id, "id")
	if err != nil {
		return nil, err
	}
	presentation, ok := s.plan.Presentation(id)
	if !ok {
		return nil, readError("contract_mismatch")
	}
	out := map[string]any{"kind": string(node.Kind()), "name": node.Name(), "description": presentation.Description}
	_, localized, err := s.plan.Index().Presentation(id, s.plan.SelectedLocale())
	if err != nil {
		return nil, err
	}
	if localized {
		out["title"] = presentation.Title
	}
	return out, nil
}
func servingReadEnvelope(s servingReadSnapshot) map[string]any {
	return map[string]any{"source": "serving", "graph_id": s.serving.Attempt.Release.GraphID,
		"revision": s.serving.Attempt.Release.Snapshot.Revision(), "generation_uuid": s.serving.Generation.Context().GenerationUUID,
		"target_id": s.serving.Attempt.Release.TargetID, "instance_id": s.serving.Attempt.InstanceID,
		"boot_id": s.serving.Attempt.BootID, "locale": s.plan.SelectedLocale()}
}
func (s *ManagedCatalogService) Start(ctx context.Context, r CatalogRequest, ttl time.Duration) (result FrozenJSON, err error) {
	binding, profile, anchors, err := catalogReadRequest(r)
	if err != nil {
		return result, err
	}
	expires, version, finish, err := s.pages.begin(ttl)
	if err != nil {
		return result, err
	}
	defer finish()
	id, err := randomReadID()
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			s.pages.discard(id)
			result = FrozenJSON{}
		}
	}()
	create := func(index *GraphIndex, visible []string, envelope map[string]any, card func(string) (map[string]any, error),
		authorize func(context.Context, func() (FrozenJSON, error)) (FrozenJSON, error), dispose func()) (FrozenJSON, error) {
		streams, err := metadataReadMaterial(index, visible, profile, s.pages.budget(), card)
		if err != nil {
			return FrozenJSON{}, err
		}
		descriptor := map[string]any{"source": string(r.Source), "graph_id": r.GraphID, "detail": "metadata_only",
			"profile": map[string]any{"name": profile.Name, "kinds": stringReadArray(profile.Kinds)}}
		if r.Source == CatalogServing {
			descriptor["locale"] = envelope["locale"]
		}
		digest, err := readDigest(map[string]any{"view_descriptor": descriptor, "catalog_payload": streamsReadJSON(streams)})
		if err != nil {
			return FrozenJSON{}, err
		}
		for k, v := range descriptor {
			envelope[k] = v
		}
		envelope["view_digest"] = digest
		frozen, err := graph.FreezeJSON(envelope)
		if err != nil {
			return FrozenJSON{}, err
		}
		return s.pages.create(ctx, &readSnapshot{id: id, binding: binding, version: version, expires: expires,
			envelope: frozen, streams: streams, profile: profile, authorize: authorize, dispose: dispose})
	}
	if r.Source == CatalogServing {
		if s.serving == nil {
			return result, readError("not_ready")
		}
		return captureServingRead(ctx, *s.serving, anchors, false, func(c servingReadSnapshot) (FrozenJSON, error) {
			definition := c.plan.Definition()
			if definition.GraphID() != r.GraphID {
				return FrozenJSON{}, readError("resync_required")
			}
			return create(c.plan.Index(), c.visible, servingReadEnvelope(c),
				func(id string) (map[string]any, error) { return servingReadCard(c, id) }, c.authorize, c.dispose)
		})
	}
	if s.definition == nil {
		return result, readError("not_ready")
	}
	read := func(ctx context.Context, request DefinitionReadRequest, action func(DefinitionReadView) (FrozenJSON, error)) (FrozenJSON, error) {
		return checkedReadFence(ctx, func(dispatch func(DefinitionReadView) (FrozenJSON, error)) (FrozenJSON, error) {
			return s.definition.Read(ctx, request, dispatch)
		}, action)
	}
	return read(ctx, DefinitionReadRequest{r.GraphID, r.Source, r.Revision}, func(view DefinitionReadView) (FrozenJSON, error) {
		if err := definitionReadView(ctx, view, r.GraphID); err != nil {
			return FrozenJSON{}, err
		}
		if r.Revision != "" && view.Revision != r.Revision {
			return FrozenJSON{}, readError("contract_mismatch")
		}
		identity, origin, index := view.Identity, view.StoreOrigin, view.Index
		ceiling, err := visibleDefinitionRead(view, index, anchors)
		if err != nil {
			return FrozenJSON{}, err
		}
		authorize := func(ctx context.Context, render func() (FrozenJSON, error)) (FrozenJSON, error) {
			return read(ctx, DefinitionReadRequest{GraphID: r.GraphID, Source: CatalogHead}, func(current DefinitionReadView) (FrozenJSON, error) {
				if err := definitionReadView(ctx, current, r.GraphID); err != nil {
					return FrozenJSON{}, err
				}
				visible, err := visibleDefinitionRead(current, index, anchors)
				if err != nil {
					return FrozenJSON{}, err
				}
				if current.Identity != identity || current.StoreOrigin != origin || !slices.Equal(ceiling, visible) {
					return FrozenJSON{}, readError("resync_required")
				}
				return render()
			})
		}
		return create(index, ceiling, map[string]any{"revision": view.Revision}, nil, authorize, func() {})
	})
}
func (s *ManagedCatalogService) Continuation(ctx context.Context, token string, r CatalogRequest, stream CatalogStream) (FrozenJSON, error) {
	binding, _, _, err := catalogReadRequest(r)
	if err != nil {
		return FrozenJSON{}, err
	}
	return s.pages.page(ctx, token, string(stream), binding)
}
