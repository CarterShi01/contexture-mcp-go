package introspection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/CarterShi01/contexture-mcp-go/core/model"
	"github.com/CarterShi01/contexture-mcp-go/core/presentation"
	"golang.org/x/text/cases"
)

type noInput struct{}
type contributor struct {
	namespace string
	describe  func(string, model.Node, *model.Index) (map[string]any, error)
}

func (c *contributor) Namespace() string { return c.namespace }
func (c *contributor) Describe(ref string, node model.Node, index *model.Index) (map[string]any, error) {
	return c.describe(ref, node, index)
}

type countedChannels struct {
	model.ChannelsLifecycle
	calls atomic.Int32
}

func (c *countedChannels) Open(context.Context, model.CleanupRegistrar) error {
	c.calls.Add(1)
	return nil
}
func (c *countedChannels) Close(context.Context) error { c.calls.Add(1); return nil }

func forest(t *testing.T, bound bool, copy presentation.Source, channels model.Channels, calls *atomic.Int32) *model.Index {
	t.Helper()
	app, err := model.DeclareApplication(model.ApplicationDeclaration{
		Name: "native", Presentation: copy, Channels: channels,
		Roots: []model.Factory{func() model.Node {
			return &model.Role{Name: "ops", Description: "Raw Straße Σς ﬃ", Instructions: "own\n",
				PreProcess: func() *model.PreProcess {
					return &model.PreProcess{Name: "prepare", Description: "prepare", Instructions: "pre\n"}
				},
				Children: []model.Factory{func() model.Node {
					return &model.Role{Name: "branch", Description: "branch", Instructions: "branch\n", Skills: []model.Factory{func() model.Node {
						return &model.Skill{Name: "procedure", Description: "procedure", Instructions: "do\n", Uses: []string{"ops/read"}}
					}}}
				}},
				PostProcess: func() *model.PostProcess {
					return &model.PostProcess{Name: "finish", Description: "finish", Instructions: "post\n"}
				},
				Skills: []model.Factory{func() model.Node {
					return &model.Skill{Name: "guide", Description: "guide", Instructions: "guide\n", Uses: []string{"person"}}
				}},
				Tools: []model.Factory{func() model.Node {
					tool, err := model.NewTool("read", "read", true, func(context.Context, noInput) (bool, error) { calls.Add(1); return true, nil })
					if err != nil {
						panic(err)
					}
					return tool
				}},
			}
		}},
		PromptRoots: []model.Factory{func() model.Node {
			return &model.Role{Name: "person", Description: "prompt", Instructions: "person\n", Uses: []string{"ops/guide"}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var index *model.Index
	if bound {
		index, err = model.Compile(app)
	} else {
		index, err = model.CompileDisclosure(app)
	}
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestCatalogViewsSlotsProgramsAndZeroExecution(t *testing.T) {
	calls := &atomic.Int32{}
	channels := &countedChannels{}
	index := forest(t, true, nil, channels, calls)
	catalog, err := New(index, CatalogOptions{View: ApplicationView, Detail: DefinitionDetail})
	if err != nil {
		t.Fatal(err)
	}
	root, err := catalog.Get("ops")
	if err != nil {
		t.Fatal(err)
	}
	wantSlots := []MemberSlot{PreProcessSlot, ChildSlot, PostProcessSlot, SkillSlot, ToolSlot}
	for i, member := range root.Members {
		if member.Slot != wantSlots[i] {
			t.Fatalf("slot %d: %s", i, member.Slot)
		}
		child, err := catalog.Get(member.Ref)
		if err != nil || child.Ordinal != i || child.ParentRef == nil || *child.ParentRef != "ops" {
			t.Fatalf("member %d: %+v %v", i, child, err)
		}
	}
	native, err := index.Find("ops")
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := model.NewDisclosure(index, model.AllSurfaces())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := model.CompileNode(native, model.ActiveCompileLevel, disclosure.Unrestricted())
	if err != nil {
		t.Fatal(err)
	}
	if root.Role.AuthoredInstructions != "own\n" || root.Role.EffectiveInstructions != opened["instructions"] ||
		*root.Role.PreProcessRef != "ops/prepare" || *root.Role.PostProcessRef != "ops/finish" {
		t.Fatal("native program mismatch")
	}
	if !strings.Contains(root.Role.EffectiveInstructions, "does not execute") {
		t.Fatal("process policy absent")
	}
	for _, view := range []CatalogView{ModelView, ApplicationView} {
		projected, err := New(index, CatalogOptions{View: view, Detail: DefinitionDetail})
		if err != nil {
			t.Fatal(err)
		}
		guide, err := projected.Get("ops/guide")
		if err != nil {
			t.Fatal(err)
		}
		if (len(guide.Uses) == 1) != (view == ApplicationView) {
			t.Fatal("prompt relationship projection mismatch")
		}
		if view == ModelView {
			if _, err := projected.Get("person"); err == nil {
				t.Fatal("model exposed prompt root")
			}
		} else {
			person, err := projected.Get("person")
			if err != nil || person.Plane != "prompt" || person.Ordinal != 1 {
				t.Fatal("prompt plane mismatch")
			}
		}
	}
	children, err := catalog.Children("ops")
	if err != nil || len(children) != 5 {
		t.Fatal("children mismatch")
	}
	tool, err := catalog.Get("ops/read")
	if err != nil || !tool.Tool.ExecutionAvailable || tool.Tool.ReadOnly == nil || !*tool.Tool.ReadOnly {
		t.Fatal("tool shape mismatch")
	}
	schema, err := index.SchemaOf(toolNode(t, index, "ops/read"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := FormatJSON(tool.Tool.InputSchema, JSONOptions{})
	b, _ := FormatJSON(schema, JSONOptions{})
	if a != b {
		t.Fatal("catalog schema divorced from actual binding")
	}
	if calls.Load() != 0 || channels.calls.Load() != 0 {
		t.Fatal("Catalog ran business code")
	}
}

func toolNode(t *testing.T, index *model.Index, ref string) model.Node {
	t.Helper()
	node, err := index.Find(ref)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestCatalogStructuralToolsAndSummaryHaveNoProgramSchema(t *testing.T) {
	calls := &atomic.Int32{}
	for _, bound := range []bool{true, false} {
		index := forest(t, bound, nil, nil, calls)
		for _, detail := range []CatalogDetail{SummaryDetail, DefinitionDetail} {
			catalog, err := New(index, CatalogOptions{View: ApplicationView, Detail: detail})
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range catalog.Snapshot().Nodes() {
				if detail == SummaryDetail && (node.Role != nil || node.Skill != nil || node.Tool != nil) {
					t.Fatal("summary exposed program/schema")
				}
				if node.Tool != nil && node.Tool.ExecutionAvailable != bound {
					t.Fatal("structural execution claimed")
				}
				if node.Tool != nil && !bound && (node.Tool.ReadOnly != nil || node.Tool.InputSchema != nil) {
					t.Fatal("structural schema claimed")
				}
			}
			if catalog.Snapshot().Application().ExecutionBound != bound {
				t.Fatal("application binding mismatch")
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("Catalog executed handler")
	}
}

func TestCatalogDeepCutsRelationshipsAndAcceptedSelectors(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	for _, row := range []struct {
		selectors []string
		roots     []string
		count     int
	}{
		{[]string{"ops/branch/procedure"}, []string{"ops/branch/procedure"}, 1},
		{[]string{"ops/branch", "ops/branch/procedure", "ops/read"}, []string{"ops/branch", "ops/read"}, 3},
		{[]string{"ops/*"}, []string{"ops/prepare", "ops/branch", "ops/finish", "ops/guide", "ops/read"}, 6},
		{[]string{"*", "ops/branch"}, []string{"ops"}, 7},
		{[]string{"person"}, []string{}, 0},
	} {
		selection, err := model.OnlySurfaces(row.selectors...)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := New(index, CatalogOptions{View: SurfaceView, Detail: DefinitionDetail, SurfaceSelection: &selection})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(catalog.Roots(), row.roots) || len(catalog.Snapshot().Nodes()) != row.count {
			t.Fatalf("%v => %v %d", row.selectors, catalog.Roots(), len(catalog.Snapshot().Nodes()))
		}
		for _, ref := range catalog.Roots() {
			root, err := catalog.Get(ref)
			if err != nil || root.ParentRef != nil {
				t.Fatal("cut root retains parent")
			}
		}
		if len(row.selectors) == 1 && row.selectors[0] == "ops/branch/procedure" {
			procedure, _ := catalog.Get(row.selectors[0])
			if len(procedure.Uses) != 0 {
				t.Fatal("deep cut leaked edge")
			}
		}
	}
	for _, selectors := range [][]string{{"unknown"}, {"ops/read/*"}, {"person/missing"}} {
		selection, _ := model.OnlySurfaces(selectors...)
		if _, err := New(index, CatalogOptions{View: SurfaceView, SurfaceSelection: &selection}); err == nil {
			t.Fatal("unknown/empty selector accepted")
		}
	}
}

func TestCatalogLocaleCopyRawDescriptionAndFullFold(t *testing.T) {
	title, _ := presentation.NewNodePresentation(presentation.NodeOptions{Title: "中文 Straße ﬃ", Description: ptrCopy(&[]string{"本地化"}[0])})
	english, _ := presentation.NewPresentation(presentation.Options{})
	chinese, _ := presentation.NewPresentation(presentation.Options{Nodes: map[string]*presentation.NodePresentation{"ops": title}})
	copy, err := presentation.NewPresentationCatalog(presentation.CatalogOptions{DefaultLocale: "en", Locales: map[string]*presentation.Presentation{"en": english, "zh": chinese}})
	if err != nil {
		t.Fatal(err)
	}
	index := forest(t, true, copy, nil, &atomic.Int32{})
	locale := "zh_Hans_CN"
	catalog, err := New(index, CatalogOptions{View: ModelView, Detail: DefinitionDetail, Locale: &locale})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := catalog.Get("ops")
	if *catalog.Snapshot().Application().Locale != "zh" || root.Description != "Raw Straße Σς ﬃ" ||
		root.Presentation.Title != title.Title() || root.Role.AuthoredInstructions != "own\n" {
		t.Fatal("copy corrupted raw definition")
	}
	for _, query := range []string{"STRASSE", "σσ", "FFI", "本地化", "\x1c中文\x1f", "OWN", "preparation procedure"} {
		result, err := catalog.List(ListOptions{Query: query})
		if err != nil || len(result) == 0 {
			t.Fatalf("query %q: %v %v", query, result, err)
		}
	}
	neutral, err := New(index, CatalogOptions{View: ModelView})
	if err != nil {
		t.Fatal(err)
	}
	neutralRoot, _ := neutral.Get("ops")
	if neutralRoot.Presentation != nil {
		t.Fatal("locale mutated another view")
	}
}

func TestCatalogContributorsCalledExactlyOnceIncludedAndDetached(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	value := map[string]any{"nested": []any{map[string]any{"message": "original"}}, "integer": new(big.Int).Lsh(big.NewInt(1), 200)}
	calls := map[string]int{}
	c := &contributor{namespace: "acme/source", describe: func(ref string, node model.Node, graph *model.Index) (map[string]any, error) {
		if graph != index {
			t.Fatal("second graph authority")
		}
		calls[ref]++
		if role := asRole(node); role != nil {
			role.Instructions = "untrusted mutation"
		}
		return value, nil
	}}
	selection, _ := model.OnlySurfaces("ops/branch")
	catalog, err := New(index, CatalogOptions{View: SurfaceView, Detail: DefinitionDetail, SurfaceSelection: &selection, Contributors: []NodeMetadataContributor{c}})
	if err != nil {
		t.Fatal(err)
	}
	value["nested"].([]any)[0].(map[string]any)["message"] = "changed"
	for i := 0; i < 4; i++ {
		for _, ref := range []string{"ops/branch", "ops/branch/procedure"} {
			node, _ := catalog.Get(ref)
			meta := node.Extensions["acme/source"].(map[string]any)
			if meta["nested"].([]any)[0].(map[string]any)["message"] != "original" || calls[ref] != 1 {
				t.Fatal("contributor reran or metadata aliased")
			}
			meta["integer"].(*big.Int).SetInt64(0)
		}
	}
	if len(calls) != 2 {
		t.Fatal("excluded contributor node called")
	}
	branch, _ := catalog.Get("ops/branch")
	if branch.Role.AuthoredInstructions != "branch\n" {
		t.Fatal("contributor mutated authored instructions")
	}
}

func TestCatalogContributorErrorsNoDefaultsAndGuards(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	expected := errors.New("metadata unavailable")
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	for _, row := range []struct {
		name string
		c    []NodeMetadataContributor
	}{
		{"nil", []NodeMetadataContributor{nil}},
		{"typed-nil", []NodeMetadataContributor{(*contributor)(nil)}},
		{"invalid namespace", []NodeMetadataContributor{&contributor{namespace: "bad"}}},
		{"duplicate namespace", []NodeMetadataContributor{&contributor{namespace: "acme/source"}, &contributor{namespace: "acme/source"}}},
		{"error", []NodeMetadataContributor{&contributor{namespace: "acme/source", describe: func(string, model.Node, *model.Index) (map[string]any, error) { return nil, expected }}}},
		{"cyclic", []NodeMetadataContributor{&contributor{namespace: "acme/source", describe: func(string, model.Node, *model.Index) (map[string]any, error) { return cyclic, nil }}}},
		{"nonfinite", []NodeMetadataContributor{&contributor{namespace: "acme/source", describe: func(string, model.Node, *model.Index) (map[string]any, error) {
			return map[string]any{"value": math.Inf(1)}, nil
		}}}},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := New(index, CatalogOptions{View: ModelView, Contributors: row.c})
			if err == nil {
				t.Fatal("invalid metadata became defaults")
			}
			if row.name == "error" && !errors.Is(err, expected) {
				t.Fatal("error lost")
			}
		})
	}
}

func TestCatalogImmutableSnapshotFreshJSONAndConcurrentReads(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	catalog, err := New(index, CatalogOptions{View: ApplicationView, Detail: DefinitionDetail})
	if err != nil {
		t.Fatal(err)
	}
	digest := catalog.Snapshot().ContentDigest()
	node, _ := catalog.Get("ops")
	node.Members[0].Ref = "changed"
	node.Role.AuthoredInstructions = "changed"
	nodes := catalog.Snapshot().Nodes()
	nodes[0].Role.EffectiveInstructions = "changed"
	roots := catalog.Roots()
	roots[0] = "changed"
	facts := catalog.Snapshot().AsJSON()
	facts["nodes"].([]any)[0].(map[string]any)["description"] = "changed"
	var wait sync.WaitGroup
	for i := 0; i < 16; i++ {
		wait.Go(func() {
			for j := 0; j < 20; j++ {
				node, err := catalog.Get("ops")
				if err != nil || node.Role.AuthoredInstructions != "own\n" || node.Members[0].Ref != "ops/prepare" {
					t.Error("aliased Catalog")
				}
				if catalog.Snapshot().ContentDigest() != digest {
					t.Error("digest mutated")
				}
				list, err := catalog.List(ListOptions{Kind: model.SkillKind})
				if err != nil || len(list) != 2 {
					t.Error("concurrent list")
				}
			}
		})
	}
	wait.Wait()
}

func TestCatalogInvalidOptionsAndExactLookup(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	selection := model.AllSurfaces()
	empty := ""
	for _, options := range []CatalogOptions{{}, {View: "unknown"}, {View: ModelView, Detail: "unknown"}, {View: SurfaceView}, {View: ModelView, SurfaceSelection: &selection}, {View: ModelView, Locale: &empty}} {
		if _, err := New(index, options); err == nil {
			t.Fatal("invalid option accepted")
		}

	}
	if _, err := New(nil, CatalogOptions{View: ModelView}); err == nil {
		t.Fatal("nil index accepted")
	}
	catalog, _ := New(index, CatalogOptions{View: ModelView})
	for _, ref := range []string{"//ops", "ops/", "missing"} {
		if _, err := catalog.Get(ref); err == nil {
			t.Fatal("noncanonical lookup accepted")
		}
	}
	if _, err := catalog.Children("missing"); err == nil {
		t.Fatal("missing children accepted")
	}
	if _, err := catalog.List(ListOptions{Kind: "unknown"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestCatalogExplicitReadModelIgnoresAmbientSelectedGraph(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	selection, err := model.OnlySurfaces("ops/branch/procedure")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := model.NewSelectedGraph(index, selection)
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.WithGraph(context.Background(), graph)
	if len(model.CurrentGraph(ctx).Walk()) != 1 {
		t.Fatal("test ambient selection missing")
	}
	catalog, err := New(index, CatalogOptions{View: ApplicationView})
	if err != nil || len(catalog.Snapshot().Nodes()) != index.Count() {
		t.Fatal("explicit application read model was narrowed by ambient graph", err)
	}
	if len(model.CurrentGraph(ctx).Walk()) != 1 {
		t.Fatal("Catalog rewrote caller context")
	}
}

func TestCatalogProjectionOperationBounds(t *testing.T) {
	for _, size := range []int{400, 1200, 1600} {
		factories, selectors := make([]model.Factory, size), make([]string, size)
		for i := range factories {
			name := fmt.Sprintf("r%04d", i)
			selectors[i] = name + "/child"
			factories[i] = func() model.Node {
				return &model.Role{Name: name, Description: "root", Instructions: "root", Children: []model.Factory{func() model.Node {
					return &model.Role{Name: "child", Description: "child", Instructions: "child", Skills: []model.Factory{func() model.Node {
						return &model.Skill{Name: "leaf", Description: "leaf", Instructions: "leaf"}
					}}}
				}}}
			}
		}
		prompts := make([]model.Factory, size)
		for i := range prompts {
			name := fmt.Sprintf("p%04d", i)
			prompts[i] = func() model.Node { return &model.Role{Name: name, Description: "prompt", Instructions: "prompt"} }
		}
		app, _ := model.DeclareApplication(model.ApplicationDeclaration{Name: "scale", Roots: factories, PromptRoots: prompts})
		index, err := model.Compile(app)
		if err != nil {
			t.Fatal(err)
		}
		selection, _ := model.OnlySurfaces(selectors...)
		catalog, err := New(index, CatalogOptions{View: SurfaceView, Detail: DefinitionDetail, SurfaceSelection: &selection})
		if err != nil {
			t.Fatal(err)
		}
		stats, n := catalog.Operations(), index.Count()
		lookups := stats.NativeLookups + stats.SelectionLookups
		if stats.Walks != 1 || stats.Entries != 3*n || stats.MemberVisits > 2*n || lookups > 12*n || len(catalog.Roots()) != size {
			t.Fatalf("operation bound for %d: %+v, nodes=%d roots=%d", size, stats, n, len(catalog.Roots()))
		}
		t.Logf("%d anchors/%d prompt roots: %+v lookup=%d <=12N=%d", size, size, stats, lookups, 12*n)
	}
}

func TestCatalogCanonicalNumericUnicodeVectors(t *testing.T) {
	digest, err := CanonicalDigest(map[string]any{"\ue000": 1, "😀": 2})
	if err != nil || digest != "sha256:871954531859c7572c6279f90eb83a594ddc3a289e8bdc28d2a84ffb8c1a1703" {
		t.Fatal(digest, err)
	}
	value, err := ParseJSON(`{"huge":90071992547409931234567890,"float":1e23,"tiny":0.00001,"zero":-0.0}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatJSON(value, JSONOptions{})
	if err != nil || got != `{"float":99999999999999991611392,"huge":90071992547409931234567890,"tiny":1e-05,"zero":0}` {
		t.Fatal(got, err)
	}
	for _, row := range []struct {
		number float64
		text   string
	}{
		{1e-7, "1e-07"}, {0.0001, "0.0001"}, {2.2250738585072014e-308, "2.2250738585072014e-308"}, {math.SmallestNonzeroFloat64, "5e-324"},
	} {
		got, err := FormatJSON(row.number, JSONOptions{})
		if err != nil || got != row.text {
			t.Fatalf("%g => %s %v", row.number, got, err)
		}
	}
	got, _ = FormatJSON(map[string]any{"html": "<>&\u2028", "control": "\x00\n"}, JSONOptions{})
	if got != "{\"control\":\"\\u0000\\n\",\"html\":\"<>&\u2028\"}" {
		t.Fatal("Unicode/HTML escape mismatch", got)
	}
}

type accessorLike struct{ calls *atomic.Int32 }

func (value accessorLike) MarshalJSON() ([]byte, error) { value.calls.Add(1); return []byte("1"), nil }

func TestCatalogOpaqueJSONNonJSONCycleNonfiniteAccessorGuards(t *testing.T) {
	calls := &atomic.Int32{}
	cycle := map[string]any{}
	cycle["self"] = cycle
	array := make([]any, 1)
	array[0] = array
	for _, value := range []any{math.NaN(), math.Inf(-1), cycle, array, accessorLike{calls}, func() {}, make(chan int), map[int]any{1: "x"}, &struct{}{}, json.Number("NaN"), json.Number("1e9999"), string([]byte{0xff})} {
		if _, err := FreezeJSON(value); err == nil {
			t.Fatalf("non-JSON accepted: %T", value)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("accessor-like marshaler evaluated")
	}
	for _, text := range []string{`"\ud800"`, `"\udfff"`, `"\ud800\u0041"`, `{} true`} {
		if _, err := ParseJSON(text); err == nil {
			t.Fatal("non-scalar JSON accepted", text)
		}
	}
	if value, err := ParseJSON(`"\ud83d\ude00"`); err != nil || value != "😀" {
		t.Fatal("valid surrogate pair rejected", value, err)
	}
}

func TestCatalogPrettySearchSeparators(t *testing.T) {
	value := map[string]any{"b": []any{1, 2}, "a": map[string]any{}}
	got, _ := FormatJSON(value, JSONOptions{Spaced: true})
	if got != `{"a": {}, "b": [1, 2]}` {
		t.Fatal(got)
	}
	indent := 2
	got, _ = FormatJSON(value, JSONOptions{Indent: &indent})
	if got != "{\n  \"a\": {},\n  \"b\": [\n    1,\n    2\n  ]\n}" {
		t.Fatal(got)
	}
	zero, minus := 0, -1
	a, _ := FormatJSON(value, JSONOptions{Indent: &zero})
	b, _ := FormatJSON(value, JSONOptions{Indent: &minus})
	if a != b || !strings.Contains(a, "\n\"a\"") {
		t.Fatal("zero indent mismatch")
	}
}

func TestCatalogBinary64ReferenceOracle10000(t *testing.T) {
	body, err := os.ReadFile("testdata/binary64-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Bits, Canonical string }
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10000 {
		t.Fatal("oracle must have 10000 deterministic finite binary64 inputs")
	}
	for _, row := range rows {
		bits, err := strconv.ParseUint(row.Bits, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatJSON(map[string]any{"value": math.Float64frombits(bits)}, JSONOptions{})
		if err != nil || got != row.Canonical {
			t.Fatalf("bits=%s got %s want %s: %v", row.Bits, got, row.Canonical, err)
		}
	}
}

func TestCatalogUnicode15FullReferenceFoldProfile(t *testing.T) {
	if unicode.Version != "15.0.0" {
		t.Fatalf("qualify Unicode profile before changing it: %s", unicode.Version)
	}
	body, err := os.ReadFile("testdata/casefold-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct{ Text, Fold string }
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1530 {
		t.Fatal("expected complete 1530-mapping reference Unicode15 fold corpus")
	}
	fold := cases.Fold()
	expected := map[string]string{}
	for _, row := range rows {
		expected[row.Text] = row.Fold
		if got := foldText(fold, row.Text); got != row.Fold {
			t.Fatalf("%U folds to %q, expected %q", []rune(row.Text)[0], got, row.Fold)
		}
	}
	total, libraryTotal := 0, 0
	for point := rune(0); point <= 0x10ffff; point++ {
		if point >= 0xd800 && point <= 0xdfff {
			continue
		}
		text := string(point)
		if fold.String(text) != text {
			libraryTotal++
		}
		if actual := foldText(fold, text); actual != text {
			total++
			if expected[text] != actual {
				t.Fatalf("unqualified extra full fold %U -> %q", point, actual)
			}
		}
	}
	if total != 1530 {
		t.Fatalf("Go x/text Fold profile changed: %d mappings", total)
	}
	if libraryTotal != 1616 {
		t.Fatalf("qualify x/text table update: %d raw mappings", libraryTotal)
	}
	for _, text := range []string{"Ꭰꭰ", "AᎠﬃ", "Ᏸᏸ"} {
		wanted := "ᎠᎠ"
		if text == "AᎠﬃ" {
			wanted = "aᎠffi"
		}
		if text == "Ᏸᏸ" {
			wanted = "ᏰᏰ"
		}
		if got := foldText(fold, text); got != wanted {
			t.Fatalf("mixed Cherokee fold %q != %q", got, wanted)
		}
	}
	t.Logf("qualified Go Unicode %s: %d reference mappings; %d raw library mappings (86 Cherokee capitals preserved)", unicode.Version, total, libraryTotal)
}

func TestCatalogSnapshotStrictShapeDigestAndRelations(t *testing.T) {
	index := forest(t, true, nil, nil, &atomic.Int32{})
	catalog, err := New(index, CatalogOptions{View: ApplicationView, Detail: DefinitionDetail})
	if err != nil {
		t.Fatal(err)
	}
	source, _ := catalog.Snapshot().ToJSON(nil)
	roundtrip, err := SnapshotFromJSON(source)
	if err != nil || roundtrip.ContentDigest() != catalog.Snapshot().ContentDigest() {
		t.Fatal("snapshot roundtrip", err)
	}
	for _, mutation := range []func(map[string]any){
		func(v map[string]any) { v["content_digest"] = "sha256:" + strings.Repeat("0", 64) },
		func(v map[string]any) { delete(v, "roots") },
		func(v map[string]any) { v["unexpected"] = true },
		func(v map[string]any) { v["counts"].(map[string]any)["role"] = 0 },
		func(v map[string]any) {
			nodes := v["nodes"].([]any)
			nodes[1].(map[string]any)["ref"] = nodes[0].(map[string]any)["ref"]
		},
		func(v map[string]any) {
			v["nodes"].([]any)[0].(map[string]any)["role"].(map[string]any)["pre_process_ref"] = "ops/finish"
		},
		func(v map[string]any) { v["nodes"].([]any)[1].(map[string]any)["ordinal"] = 100 },
		func(v map[string]any) {
			v["nodes"].([]any)[0].(map[string]any)["members"].([]any)[0].(map[string]any)["slot"] = "tool"
		},
		func(v map[string]any) { v["nodes"].([]any)[4].(map[string]any)["dependents"] = []any{"missing"} },
		func(v map[string]any) { delete(v["nodes"].([]any)[0].(map[string]any), "skill") },
		func(v map[string]any) { v["application"].(map[string]any)["mode"] = "disclosure-only" },
	} {
		value := catalog.Snapshot().AsJSON()
		mutation(value)
		text, err := FormatJSON(value, JSONOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SnapshotFromJSON(text); err == nil {
			t.Fatal("invalid snapshot accepted", text)
		}
	}
	// Exercise graph validation independently from digest checks.
	data := SnapshotData{Application: catalog.Snapshot().Application(), Roots: catalog.Roots(), Counts: catalog.Snapshot().Counts(), Nodes: catalog.Snapshot().Nodes()}
	data.Nodes[0].Ordinal = 1
	if _, err := NewSnapshot(data); err == nil {
		t.Fatal("bad root ordinal accepted")
	}
}
