package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type runtimeInput struct {
	Value string `json:"value"`
}

func TestRuntimeUsesBindingDoorsAndRequestContext(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(ctx context.Context, input runtimeInput) (string, error) {
		if contexture.CurrentPrincipal(ctx) != "alice" {
			return "", errors.New("wrong principal")
		}
		if graph := contexture.CurrentGraph(ctx); graph == nil || len(graph.Walk()) != 3 {
			return "", errors.New("missing selected graph")
		}
		return input.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(context.Context, runtimeInput) (string, error) { return "restarted", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "runtime", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	telemetry := contexture.NewMemoryTelemetry()
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), telemetry)
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.InvokeReadOnly(contexture.WithPrincipal(context.Background(), "alice"), "operations/status", json.RawMessage(`{"value":"ok"}`), contexture.AllRoots())
	if err != nil || value != "ok" {
		t.Fatalf("InvokeReadOnly = %#v, %v", value, err)
	}
	if _, err := runtime.Invoke(context.Background(), "operations/status", json.RawMessage(`{"value":"x"}`), contexture.AllRoots()); !errors.Is(err, contexture.ErrWrongDoor) {
		t.Fatalf("Invoke wrong door = %v", err)
	}
	if _, err := runtime.InvokeReadOnly(context.Background(), "operations/restart", json.RawMessage(`{"value":"x"}`), contexture.AllRoots()); !errors.Is(err, contexture.ErrWrongDoor) {
		t.Fatalf("InvokeReadOnly wrong door = %v", err)
	}
	if len(telemetry.Events()) != 1 {
		t.Fatal("wrong-door calls must not invoke telemetry")
	}
}
