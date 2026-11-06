package graph

// Exact stable-ID endpoint comparisons, not inferred execution equivalence.
import "slices"

type DefinitionDiff struct{ data FrozenJSON }

func (d DefinitionDiff) AsJSON() any { return d.data.JSON() }
func (d DefinitionDiff) Empty() bool {
	m, ok := d.data.value.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range diffGroups() {
		if len(arr(m[k])) != 0 {
			return false
		}
	}
	return true
}
func diffGroups() []string { return []string{"nodes", "membership", "uses", "publications", "locales"} }
func eventOptional(m map[string]any, k string) any {
	if v, ok := m[k]; ok {
		return map[string]any{"present": true, "value": v}
	}
	return map[string]any{"present": false}
}
func eventKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
func unionKeys(a, b map[string]any) []string {
	m := map[string]any{}
	for k := range a {
		m[k] = nil
	}
	for k := range b {
		m[k] = nil
	}
	return eventKeys(m)
}
func NewDefinitionDiff(value any) (out DefinitionDiff, err error) {
	defer catchModel(&err)
	m := obj(mustFreeze(value).JSON(), "nodes membership uses publications locales", "")
	for _, k := range diffGroups() {
		arr(m[k])
	}
	return DefinitionDiff{mustFreeze(m)}, nil
}
func Diff(base, candidate *GraphDefinition) (DefinitionDiff, error) {
	return DefinitionDiffBetween(base, candidate)
}
func DefinitionDiffBetween(base, candidate *GraphDefinition) (out DefinitionDiff, err error) {
	defer catchModel(&err)
	if base == nil || candidate == nil || !base.valid || !candidate.valid || base.graphID != candidate.graphID {
		failModel("Cannot align unvalidated or different graph identities")
	}
	groups := map[string]any{}
	for _, k := range diffGroups() {
		groups[k] = []any{}
	}
	entry := func(group, id, field string, a, b map[string]any, key string) {
		x, y := eventOptional(a, key), eventOptional(b, key)
		if !pureEqual(x, y) {
			groups[group] = append(arr(groups[group]), map[string]any{"id": id, "field": field, "before": x, "after": y})
		}
	}
	a, b := base.AsJSON().(map[string]any), candidate.AsJSON().(map[string]any)
	for _, k := range []string{"schema", "semantic_version", "application_name", "mode", "extensions"} {
		entry("nodes", base.graphID, pointer("", k), a, b, k)
	}
	entry("membership", base.graphID, "/roots", a, b, "roots")
	an, bn := eventNodes(base), eventNodes(candidate)
	for _, id := range unionKeys(an, bn) {
		x, xok := an[id]
		y, yok := bn[id]
		if !xok || !yok {
			left, right := map[string]any{}, map[string]any{}
			if xok {
				left["node"] = x
			}
			if yok {
				right["node"] = y
			}
			entry("nodes", id, "", left, right, "node")
			continue
		}
		for _, k := range unionKeys(x.(map[string]any), y.(map[string]any)) {
			group := "nodes"
			if k == "members" {
				group = "membership"
			}
			if k == "uses" {
				group = "uses"
			}
			entry(group, id, pointer("", k), x.(map[string]any), y.(map[string]any), k)
		}
	}
	entry("publications", base.graphID, "/publications", a, b, "publications")
	ap, bp := eventByID(arr(a["publications"]), "publication_id"), eventByID(arr(b["publications"]), "publication_id")
	for _, id := range unionKeys(ap, bp) {
		left, right := map[string]any{}, map[string]any{}
		if v, ok := ap[id]; ok {
			left["publication"] = v
		}
		if v, ok := bp[id]; ok {
			right["publication"] = v
		}
		entry("publications", id, "", left, right, "publication")
	}
	entry("locales", base.graphID, "/presentations", a, b, "presentations")
	for _, k := range diffGroups() {
		slices.SortFunc(arr(groups[k]), func(a, b any) int {
			x, y := a.(map[string]any), b.(map[string]any)
			if x["id"] != y["id"] {
				return compareText(x["id"].(string), y["id"].(string))
			}
			return compareText(x["field"].(string), y["field"].(string))
		})
	}
	return DefinitionDiff{mustFreeze(groups)}, nil
}
func compareText(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func eventNodes(g *GraphDefinition) map[string]any {
	m := map[string]any{}
	for _, n := range g.Nodes() {
		m[n.id] = n.AsJSON()
	}
	return m
}
func eventByID(values []any, key string) map[string]any {
	m := map[string]any{}
	for _, raw := range values {
		v := raw.(map[string]any)
		m[v[key].(string)] = v
	}
	return m
}
func projectionDiff(a, b any, kind string) (out FrozenJSON, err error) {
	defer catchModel(&err)
	x, y := mustFreeze(a), mustFreeze(b)
	if _, ok := x.value.(map[string]any); !ok {
		failModel("Explicit projection objects required")
	}
	if _, ok := y.value.(map[string]any); !ok {
		failModel("Explicit projection objects required")
	}
	return mustFreeze(map[string]any{"kind": kind, "equal": pureEqual(x.value, y.value), "before": x.value, "after": y.value}), nil
}
func BehaviorDiff(a, b any) (FrozenJSON, error)  { return projectionDiff(a, b, "behavior") }
func SurfaceDiff(a, b any) (FrozenJSON, error)   { return projectionDiff(a, b, "surface") }
func ExecutionDiff(a, b any) (FrozenJSON, error) { return projectionDiff(a, b, "execution") }
