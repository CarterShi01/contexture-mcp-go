package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type selectionInput struct{}

func selectionIndex(t *testing.T, includeScopedTools bool) *contexture.Index {
	t.Helper()
	alphaTools := []contexture.Factory{}
	betaTools := []contexture.Factory{}
	if includeScopedTools {
		alphaTools = append(alphaTools, scopedSelectionTool(t, "scope"))
		betaTools = append(betaTools, scopedSelectionTool(t, "scope"))
	}
	read, err := contexture.NewTool("read", "Read beta.", true, func(context.Context, selectionInput) (string, error) { return "beta", nil })
	if err != nil {
		t.Fatal(err)
	}
	betaTools = append(betaTools, func() contexture.Node { return read })
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "root-selection", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Inspect alpha.", Children: []contexture.Factory{func() contexture.Node {
				return &contexture.Role{Name: "child", Description: "Child.", Instructions: "Inspect child."}
			}}, Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "inspect", Description: "Inspect beta.", Instructions: "Read beta.", Uses: []string{"beta/read"}}
			}}, Tools: alphaTools}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Read beta.", Tools: betaTools}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func scopedSelectionTool(t *testing.T, name string) contexture.Factory {
	t.Helper()
	tool, err := contexture.NewTool(name, "Read the request projection.", true, func(ctx context.Context, _ selectionInput) ([]string, error) {
		graph := contexture.CurrentGraph(ctx)
		if graph == nil {
			return nil, errors.New("current graph was absent")
		}
		selection := contexture.CurrentSelection(ctx)
		return append(append([]string(nil), selection.Names()...), graph.Walk()...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return func() contexture.Node { return tool }
}

func TestRootSelectionValuesClassifyErrorsAndHideUnknownRoots(t *testing.T) {
	index := selectionIndex(t, false)
	selection, err := contexture.OnlyRoots(" beta ", "alpha", "beta")
	if err != nil || !reflect.DeepEqual(selection.Names(), []string{"alpha", "beta"}) || selection.IsAll() {
		t.Fatalf("OnlyRoots value = %#v, %v", selection, err)
	}
	for _, values := range [][]string{{}, {" "}, {"alpha/child"}} {
		if _, err := contexture.OnlyRoots(values...); !errors.Is(err, contexture.ErrInvalidSelection) {
			t.Fatalf("OnlyRoots(%#v) = %v, want ErrInvalidSelection", values, err)
		} else if typed := new(contexture.RootSelectionError); !errors.As(err, &typed) {
			t.Fatalf("OnlyRoots(%#v) did not return RootSelectionError: %T", values, err)
		}
	}
	unknown, _ := contexture.OnlyRoots("missing")
	if _, err := unknown.Resolve(index); !errors.Is(err, contexture.ErrInvalidSelection) || errors.Is(err, contexture.ErrRootOutsideSelection) || containsAny(err.Error(), "alpha", "beta") {
		t.Fatalf("unknown root leaked selection facts: %v", err)
	}
	alpha, _ := contexture.OnlyRoots("alpha")
	both, _ := contexture.OnlyRoots("alpha", "beta")
	if left, err := alpha.Intersect(both); err != nil || !reflect.DeepEqual(left.Names(), []string{"alpha"}) {
		t.Fatalf("intersection = %#v, %v", left, err)
	}
	if _, err := alpha.Intersect(mustOnlyRoot(t, "beta")); !errors.Is(err, contexture.ErrInvalidSelection) {
		t.Fatalf("empty intersection = %v", err)
	}
	if !contexture.CurrentSelection(context.Background()).IsAll() {
		t.Fatal("CurrentSelection outside invocation must be all-roots")
	}
}

func TestSelectedGraphProjectsEveryGraphOperationWithoutCrossRootLeakage(t *testing.T) {
	index := selectionIndex(t, false)
	alpha, _ := contexture.OnlyRoots("alpha")
	graph, err := contexture.NewSelectedGraph(index, alpha)
	if err != nil {
		t.Fatal(err)
	}
	if got := namesOf(graph.Roots()); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("selected Roots = %#v", got)
	}
	if got, want := graph.Walk(), []string{"alpha", "alpha/child", "alpha/inspect"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected Walk = %#v, want %#v", got, want)
	}
	pairs := graph.NodesWithRefs()
	if len(pairs) != 3 || pairs[1].Ref != "alpha/child" || pairs[1].Node.NodeName() != "child" {
		t.Fatalf("NodesWithRefs = %#v", pairs)
	}
	if _, err := graph.Find("beta/read"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("selected Find leaked beta: %v", err)
	} else if typed := new(contexture.RootOutsideSelectionError); !errors.As(err, &typed) || typed.Ref != "beta/read" {
		t.Fatalf("outside error facts = %#v", typed)
	}
	if _, err := graph.Find(""); !errors.Is(err, contexture.ErrNodeNotFound) {
		t.Fatalf("empty ref should retain Index error, got %v", err)
	}
	child, err := graph.Find("alpha/child")
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := graph.RefOf(child); err != nil || ref != "alpha/child" {
		t.Fatalf("RefOf = %q, %v", ref, err)
	}
	parent, err := graph.ParentOf(child)
	if err != nil || parent == nil || parent.Name != "alpha" {
		t.Fatalf("ParentOf = %#v, %v", parent, err)
	}
	root, _ := graph.Find("alpha")
	if got := namesOf(mustChildren(t, graph, root)); !reflect.DeepEqual(got, []string{"child", "inspect"}) {
		t.Fatalf("ChildrenOf = %#v", got)
	}
	if uses, err := graph.UsesOf("alpha/inspect"); err != nil || len(uses) != 0 {
		t.Fatalf("selected cross-root UsesOf = %#v, %v", uses, err)
	}
	if _, err := graph.DependentsOf("beta/read"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("selected DependentsOf leaked beta: %v", err)
	}
	betaGraph, err := contexture.NewSelectedGraph(index, mustOnlyRoot(t, "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if dependents, err := betaGraph.DependentsOf("beta/read"); err != nil || len(dependents) != 0 {
		t.Fatalf("cross-root dependents must be filtered = %#v, %v", dependents, err)
	}
	allGraph, _ := contexture.NewSelectedGraph(index, contexture.AllRoots())
	if dependents, err := allGraph.DependentsOf("beta/read"); err != nil || !reflect.DeepEqual(dependents, []string{"alpha/inspect"}) {
		t.Fatalf("all dependents = %#v, %v", dependents, err)
	}
	if matches, total := allGraph.MatchingRefs("ins", 1); total != 1 || !reflect.DeepEqual(matches, []string{"alpha/inspect"}) {
		t.Fatalf("MatchingRefs = %#v, %d", matches, total)
	}
	if matches, total := graph.MatchingRefs("", 10); total != 3 || !reflect.DeepEqual(matches, []string{"alpha", "alpha/child", "alpha/inspect"}) {
		t.Fatalf("selected MatchingRefs leaked or counted beta: %#v, %d", matches, total)
	}
}

func TestRuntimeCurrentGraphAndSelectionAreConcurrentRequestLocalProjections(t *testing.T) {
	index := selectionIndex(t, true)
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		root  string
		value []string
		err   error
	}
	results := make(chan outcome, 2)
	var group sync.WaitGroup
	for _, root := range []string{"alpha", "beta"} {
		root := root
		group.Add(1)
		go func() {
			defer group.Done()
			selection := mustOnlyRoot(t, root)
			value, callErr := runtime.InvokeReadOnly(context.Background(), root+"/scope", json.RawMessage("{}"), selection)
			if callErr != nil {
				results <- outcome{root: root, err: callErr}
				return
			}
			results <- outcome{root: root, value: value.([]string)}
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		for _, ref := range result.value {
			if ref != result.root && !strings.HasPrefix(ref, result.root+"/") {
				t.Fatalf("%s request observed another root: %#v", result.root, result.value)
			}
		}
	}
}

func TestRuntimeRetainsOneSelectedGraphAcrossOverlappingRootCalls(t *testing.T) {
	arrived := make(chan string, 2)
	release := make(chan struct{})
	probe := func(root, excluded string) contexture.Factory {
		tool, err := contexture.NewTool("probe", "Probe the selected graph.", true, func(ctx context.Context, _ selectionInput) (string, error) {
			graph := contexture.CurrentGraph(ctx)
			if graph == nil {
				return "", errors.New("CurrentGraph was absent")
			}
			arrived <- root
			<-release
			if contexture.CurrentGraph(ctx) != graph {
				return "", errors.New("CurrentGraph changed during invocation")
			}
			if _, err := graph.Find(root + "/probe"); err != nil {
				return "", err
			}
			if _, err := graph.Find(excluded + "/probe"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
				return "", errors.New("selected graph revealed an excluded root")
			}
			return root, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return func() contexture.Node { return tool }
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "graph-barrier", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Inspect.", Tools: []contexture.Factory{probe("alpha", "beta")}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Inspect.", Tools: []contexture.Factory{probe("beta", "alpha")}}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		root  string
		value any
		err   error
	}
	results := make(chan result, 2)
	for _, root := range []string{"alpha", "beta"} {
		root := root
		go func() {
			selection := mustOnlyRoot(t, root)
			value, callErr := runtime.InvokeReadOnly(context.Background(), root+"/probe", json.RawMessage("{}"), selection)
			results <- result{root: root, value: value, err: callErr}
		}()
	}
	seen := map[string]bool{<-arrived: true, <-arrived: true}
	if !seen["alpha"] || !seen["beta"] {
		t.Fatalf("barrier arrivals = %#v", seen)
	}
	close(release)
	for range 2 {
		outcome := <-results
		if outcome.err != nil || outcome.value != outcome.root {
			t.Fatalf("%s result = %#v, %v", outcome.root, outcome.value, outcome.err)
		}
	}
}

func mustOnlyRoot(t *testing.T, name string) contexture.RootSelection {
	t.Helper()
	selection, err := contexture.OnlyRoots(name)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func mustChildren(t *testing.T, graph *contexture.SelectedGraph, node contexture.Node) []contexture.Node {
	t.Helper()
	children, err := graph.ChildrenOf(node)
	if err != nil {
		t.Fatal(err)
	}
	return children
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
