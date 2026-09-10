package contexture_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestProcessOpenHasNoEffectsAndExplicitToolsEnforceOrder(t *testing.T) {
	events := []string{}
	step := func(name string) *contexture.Tool {
		tool, err := contexture.NewTool(name, "Perform "+name+".", false, func(context.Context, struct{}) (string, error) {
			if name == "work" && !reflect.DeepEqual(events, []string{"prepare"}) {
				return "", &processOrderError{}
			}
			events = append(events, name)
			return name, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return tool
	}
	prepare, work, finish := step("prepare"), step("work"), step("finish")
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "runtime-process", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "worker", Description: "Work.", Instructions: "Perform owner work.",
			PreProcess: func() *contexture.PreProcess {
				return &contexture.PreProcess{Name: "setup", Description: "Set up.", Instructions: "Prepare.", Tools: []contexture.Factory{func() contexture.Node { return prepare }}}
			},
			PostProcess: func() *contexture.PostProcess {
				return &contexture.PostProcess{Name: "cleanup", Description: "Clean up.", Instructions: "Finish.", Tools: []contexture.Factory{func() contexture.Node { return finish }}}
			},
			Tools: []contexture.Factory{func() contexture.Node { return work }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(app)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := contexture.NewDisclosure(index, contexture.AllRoots())
	api, _ := contexture.NewDisclosureAPI(view)
	opened, err := api.Open("worker", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened["instructions"].(string), "return to this role's own instructions") {
		t.Fatal("pre contract omitted return")
	}
	for _, ref := range []string{"worker/setup", "worker/cleanup"} {
		if _, err := api.Open(ref, contexture.AllRoots()); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 0 {
		t.Fatalf("opening executed process tools: %v", events)
	}
	runtime, _ := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if _, err := runtime.Invoke(context.Background(), "worker/work", json.RawMessage(`{}`), contexture.AllRoots()); err == nil {
		t.Fatal("work bypassed enforced preparation")
	}
	if len(events) != 0 {
		t.Fatalf("failed work changed events: %v", events)
	}
	for _, ref := range []string{"worker/setup/prepare", "worker/work", "worker/cleanup/finish"} {
		if _, err := runtime.Invoke(context.Background(), ref, json.RawMessage(`{}`), contexture.AllRoots()); err != nil {
			t.Fatalf("invoke %s: %v", ref, err)
		}
	}
	if !reflect.DeepEqual(events, []string{"prepare", "work", "finish"}) {
		t.Fatalf("events = %v", events)
	}
}

type processOrderError struct{}

func (*processOrderError) Error() string { return "preparation is required by the work tool" }
