package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestWithGraphNestsAndRestoresByContextDerivation(t *testing.T) {
	index := selectionIndex(t, false)
	all, err := contexture.NewSelectedGraph(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := contexture.OnlyRoots("alpha")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := contexture.NewSelectedGraph(index, alpha)
	if err != nil {
		t.Fatal(err)
	}
	base := context.Background()
	outer := contexture.WithGraph(base, all)
	inner := contexture.WithGraph(outer, selected)
	if contexture.CurrentGraph(base) != nil || contexture.CurrentGraph(outer) != all || contexture.CurrentGraph(inner) != selected {
		t.Fatalf("nested graph contexts = base:%#v outer:%#v inner:%#v", contexture.CurrentGraph(base), contexture.CurrentGraph(outer), contexture.CurrentGraph(inner))
	}
	if _, err := contexture.CurrentGraph(inner).Find("beta"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
		t.Fatalf("nested selected graph leaked beta: %v", err)
	}
	// Context values are immutable parent links: leaving the inner scope means
	// retaining outer, and neither derived scope can mutate the other.
	if _, err := contexture.CurrentGraph(outer).Find("beta"); err != nil {
		t.Fatalf("outer graph was not restored: %v", err)
	}
}

func TestRuntimeOwnsInvocationFactsOverNestedCallerContext(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	var observedGraph *contexture.SelectedGraph
	tool, err := contexture.NewTool("probe", "Probe request facts.", true, func(ctx context.Context, _ struct{}) (string, error) {
		observedGraph = contexture.CurrentGraph(ctx)
		if observedGraph == nil {
			return "", errors.New("missing invocation graph")
		}
		if contexture.CurrentSelection(ctx).IsAll() || contexture.CurrentTelemetry(ctx) != collector {
			return "", errors.New("runtime did not own selection or telemetry")
		}
		principal := contexture.CurrentPrincipal(ctx)
		if principal == nil || principal.Subject() != "ada" {
			return "", errors.New("runtime did not retain host identity")
		}
		if _, err := observedGraph.Find("beta"); !errors.Is(err, contexture.ErrRootOutsideSelection) {
			return "", errors.New("caller graph widened runtime graph")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "graph-context", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Separate."}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), collector)
	if err != nil {
		t.Fatal(err)
	}
	all, err := contexture.NewSelectedGraph(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := contexture.OnlyRoots("alpha")
	if err != nil {
		t.Fatal(err)
	}
	caller := contexture.WithPrincipal(contexture.WithGraph(context.Background(), all), contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "ada"}))
	value, err := runtime.InvokeReadOnly(caller, "alpha/probe", json.RawMessage(`{}`), alpha)
	if err != nil || value != "ok" {
		t.Fatalf("runtime invocation = %#v, %v", value, err)
	}
	if observedGraph == nil || observedGraph == all {
		t.Fatalf("runtime retained caller graph: %#v", observedGraph)
	}
	if contexture.CurrentGraph(caller) != all || !contexture.CurrentSelection(caller).IsAll() || contexture.CurrentTelemetry(caller) != nil || contexture.CurrentPrincipal(caller).Subject() != "ada" {
		t.Fatalf("caller context changed after invocation: graph=%#v selection=%#v telemetry=%#v principal=%#v", contexture.CurrentGraph(caller), contexture.CurrentSelection(caller), contexture.CurrentTelemetry(caller), contexture.CurrentPrincipal(caller))
	}
}
