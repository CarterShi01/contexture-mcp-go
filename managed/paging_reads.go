package managed

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

type PageOperation string
type PageRelation string

const (
	PageRoutes  PageOperation = "routes"
	PageMembers PageOperation = "members"
	PageUses    PageOperation = "uses"
	PageOpen    PageOperation = "open"
)

type PagingRequest struct {
	Operation PageOperation
	Reference string
	Selection []string
	Profile   *PageProfile
}
type PagingOptions struct {
	PageStoreOptions
	Serving ServingReadPort
}
type ManagedPagingService struct {
	port  ServingReadPort
	pages *snapshotPages
}

func NewManagedPagingService(o PagingOptions) (*ManagedPagingService, error) {
	if o.Serving.Runtime == nil || o.Serving.StoreOrigin == nil || o.Serving.Fence == nil {
		return nil, errors.New("complete explicit serving read port required")
	}
	pages, err := newSnapshotPages(o.PageStoreOptions)
	if err != nil {
		return nil, err
	}
	return &ManagedPagingService{o.Serving, pages}, nil
}
func (s *ManagedPagingService) Counts() ReadCounts          { return s.pages.counts() }
func (s *ManagedPagingService) RequiresExplicitSweep() bool { return !s.pages.automatic }
func (s *ManagedPagingService) Sweep() int                  { return s.pages.sweep() }
func (s *ManagedPagingService) Shutdown()                   { s.pages.shutdown() }

func pagingReadRequest(r PagingRequest) (string, PageProfile, []string, error) {
	if (r.Operation != PageRoutes && r.Operation != PageMembers && r.Operation != PageUses && r.Operation != PageOpen) ||
		(r.Operation == PageRoutes) != (r.Reference == "") {
		return "", PageProfile{}, nil, readError("schema_invalid")
	}
	if r.Reference != "" {
		if _, err := graph.ParseReference(r.Reference); err != nil {
			return "", PageProfile{}, nil, err
		}
	}
	p := PageProfile{}
	if r.Profile != nil {
		p = *r.Profile
	}
	p, err := normalizePageProfile(p, "native-route-v1")
	if err != nil {
		return "", PageProfile{}, nil, err
	}
	anchors, err := readSelection(r.Selection)
	if err != nil {
		return "", PageProfile{}, nil, err
	}
	var reference any
	if r.Reference != "" {
		reference = r.Reference
	}
	binding, err := readEncode(map[string]any{"operation": string(r.Operation), "reference": reference,
		"selection": nullableReadArray(anchors), "profile": p.asJSON()})
	return binding, p, anchors, err
}
func pagingReadMaterial(s servingReadSnapshot, r PagingRequest, profile PageProfile, budget *readMaterialBudget) (map[string]any, []readStream, error) {
	index, generation := s.plan.Index(), s.serving.Generation.Context().GenerationUUID
	definition := index.Definition()
	visible, err := readIDs(s.visible)
	if err != nil {
		return nil, nil, err
	}
	ref := func(id string) (string, error) { return graph.FormatReference(definition.GraphID(), generation, id) }
	require := func(id string) error {
		if !visible[id] {
			return readError("permission_denied")
		}
		return nil
	}
	card := func(id string) (map[string]any, error) {
		if err := require(id); err != nil {
			return nil, err
		}
		out, err := servingReadCard(s, id)
		if err != nil {
			return nil, err
		}
		out["ref"], err = ref(id)
		return out, err
	}
	nodeID := ""
	if r.Reference != "" {
		parsed, err := graph.ParseReference(r.Reference)
		if err != nil {
			return nil, nil, err
		}
		if parsed.GraphID() != definition.GraphID() || parsed.GenerationUUID() != generation {
			return nil, nil, readError("resync_required")
		}
		nodeID = parsed.NodeID()
		if err := require(nodeID); err != nil {
			return nil, nil, err
		}
	}
	envelope := servingReadEnvelope(s)
	if r.Operation == PageOpen {
		node, err := index.Get(nodeID, "id")
		if err != nil || node.Kind() != graph.Role {
			return nil, nil, readError("schema_invalid")
		}
		activeCard, err := card(nodeID)
		if err != nil {
			return nil, nil, err
		}
		parts, err := s.plan.ResolveInstructions(nodeID, generation)
		if err != nil {
			return nil, nil, err
		}
		var instructions strings.Builder
		for _, part := range parts {
			if part.Kind == "Text" {
				instructions.WriteString(part.Text)
			} else {
				if err := require(part.Reference.NodeID()); err != nil {
					return nil, nil, err
				}
				wire, err := part.Reference.Wire()
				if err != nil {
					return nil, nil, err
				}
				instructions.WriteString(wire)
			}
		}
		active := map[string]any{"card": activeCard, "instructions": instructions.String()}
		processes := []any{}
		designations, ok := s.plan.Processes(nodeID)
		if !ok {
			return nil, nil, readError("contract_mismatch")
		}
		for _, p := range []struct{ name, target string }{{"pre_process", designations.PreProcess}, {"post_process", designations.PostProcess}} {
			if p.target == "" {
				continue
			}
			c, err := card(p.target)
			if err != nil {
				return nil, nil, err
			}
			processes = append(processes, map[string]any{"designation": p.name, "card": c})
			active[p.name], err = ref(p.target)
			if err != nil {
				return nil, nil, err
			}
		}
		envelope["owner_active"], envelope["process_cards"] = active, processes
	}
	if _, err := budget.add(envelope, false); err != nil {
		return nil, nil, err
	}
	relations := []string{string(r.Operation)}
	if r.Operation == PageOpen {
		relations = []string{"members", "uses"}
	}
	streams := []readStream{}
	for _, relation := range relations {
		ids := []string{}
		if relation == "routes" {
			for _, id := range index.Preorder() {
				loc, _ := index.Location(id)
				if visible[id] && !visible[loc.ParentID] {
					ids = append(ids, id)
				}
			}
		} else {
			node, err := index.Get(nodeID, "id")
			if err != nil {
				return nil, nil, err
			}
			if relation == "uses" {
				ids = node.Uses()
			} else if node.Kind() == graph.Role {
				for _, p := range node.Members().Placements() {
					ids = append(ids, p.NodeID)
				}
			}
		}
		stream := readStream{name: relation}
		for _, id := range ids {
			node, err := index.Get(id, "id")
			if err != nil {
				return nil, nil, err
			}
			if !visible[id] || !slices.Contains(profile.Kinds, string(node.Kind())) {
				continue
			}
			value, err := card(id)
			if err != nil {
				return nil, nil, err
			}
			item, err := budget.add(value, true)
			if err != nil {
				return nil, nil, err
			}
			stream.items = append(stream.items, item)
		}
		streams = append(streams, stream)
	}
	return envelope, streams, nil
}
func (s *ManagedPagingService) Start(ctx context.Context, r PagingRequest, ttl time.Duration) (result FrozenJSON, err error) {
	binding, profile, anchors, err := pagingReadRequest(r)
	if err != nil {
		return result, err
	}
	// Do not retain the caller's mutable selection/profile across admission.
	r.Selection, r.Profile = nil, nil
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
	return captureServingRead(ctx, s.port, anchors, true, func(c servingReadSnapshot) (FrozenJSON, error) {
		envelope, streams, err := pagingReadMaterial(c, r, profile, s.pages.budget())
		if err != nil {
			return FrozenJSON{}, err
		}
		frozen, err := graph.FreezeJSON(envelope)
		if err != nil {
			return FrozenJSON{}, err
		}
		return s.pages.create(ctx, &readSnapshot{id: id, binding: binding, version: version, expires: expires,
			envelope: frozen, streams: streams, profile: profile, relationPages: true, authorize: c.authorize, dispose: c.dispose})
	})
}
func (s *ManagedPagingService) Continuation(ctx context.Context, token string, r PagingRequest, relation PageRelation) (FrozenJSON, error) {
	binding, _, _, err := pagingReadRequest(r)
	if err != nil {
		return FrozenJSON{}, err
	}
	return s.pages.page(ctx, token, string(relation), binding)
}
