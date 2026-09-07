package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type executionInput struct {
	Value string `json:"value"`
}

func executionAPI(t *testing.T, ceiling contexture.RootSelection, promptCalls *atomic.Int32) (*contexture.ExecutionAPI, *contexture.Gateway) {
	t.Helper()
	read, err := contexture.NewTool("status", "Read status.", true, func(ctx context.Context, input executionInput) (string, error) {
		principal := contexture.CurrentPrincipal(ctx)
		if principal == nil || principal.Subject() != "ada" {
			return "", errors.New("missing request principal")
		}
		if graph := contexture.CurrentGraph(ctx); graph == nil || len(graph.Walk()) != 3 {
			return "", errors.New("missing selected request graph")
		}
		selection := contexture.CurrentSelection(ctx)
		if !selection.ContainsRef("operations/status") || selection.ContainsRef("prompt") {
			return "", errors.New("wrong effective request selection")
		}
		return input.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("change", "Change status.", false, func(context.Context, executionInput) (string, error) { return "changed", nil })
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := contexture.NewTool("prompt", "Person command.", true, func(context.Context, struct{}) (string, error) {
		promptCalls.Add(1)
		return "person result", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "execution-api",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
		}},
		PromptRoots: []contexture.Factory{func() contexture.Node { return prompt }},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), ceiling, nil)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := contexture.NewExecutionAPI(runtime)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return execution, gateway
}

func TestExecutionAPIUsesOnlyInvocationDoorsAndCarriesRequestFacts(t *testing.T) {
	var promptCalls atomic.Int32
	operations, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	execution, _ := executionAPI(t, operations, &promptCalls)
	tools := execution.Tools()
	if len(tools) != 2 || tools[0].Name != contexture.InvokeReadOnlyGatewayName || tools[1].Name != contexture.InvokeGatewayName || !tools[0].ReadOnly || tools[1].ReadOnly {
		t.Fatalf("execution tools = %#v", tools)
	}
	tools[0].Name = "changed"
	if execution.Tools()[0].Name != contexture.InvokeReadOnlyGatewayName {
		t.Fatal("ExecutionAPI tool inventory was mutable")
	}
	ctx := contexture.WithPrincipal(context.Background(), contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "ada"}))
	value, err := execution.InvokeReadOnly(ctx, "operations/status", json.RawMessage(`{"value":"healthy"}`), contexture.AllRoots())
	if err != nil || value != "healthy" {
		t.Fatalf("InvokeReadOnly = %#v, %v", value, err)
	}
	if promptCalls.Load() != 0 {
		t.Fatal("ordinary execution called the prompt root")
	}
}

func TestExecutionAPIRefusesModelPromptRootsAndRecoversAgentMistakes(t *testing.T) {
	var promptCalls atomic.Int32
	execution, gateway := executionAPI(t, contexture.AllRoots(), &promptCalls)

	_, err := execution.InvokeReadOnly(context.Background(), "prompt", json.RawMessage(`{}`), contexture.AllRoots())
	var promptRefusal *contexture.RefusedError
	if !errors.As(err, &promptRefusal) || !strings.Contains(promptRefusal.Error(), "opened by a person") || promptCalls.Load() != 0 {
		t.Fatalf("prompt model invocation = %#v; calls = %d", err, promptCalls.Load())
	}
	_, err = execution.InvokeReadOnly(context.Background(), "/prompt", json.RawMessage(`{}`), contexture.AllRoots())
	if !errors.As(err, &promptRefusal) || promptCalls.Load() != 0 {
		t.Fatalf("canonical prompt model invocation = %#v; calls = %d", err, promptCalls.Load())
	}
	value, err := execution.ReadForHost(context.Background(), "prompt", contexture.AllRoots())
	if err != nil || value != "person result" || promptCalls.Load() != 1 {
		t.Fatalf("ReadForHost = %#v, %v; calls = %d", value, err, promptCalls.Load())
	}
	_, err = execution.ReadForHost(context.Background(), "operations/change", contexture.AllRoots())
	var directWrongDoor *contexture.WrongDoorError
	if !errors.As(err, &directWrongDoor) || !errors.Is(err, contexture.ErrWrongDoor) {
		t.Fatalf("host read must retain an unexpected wrong-door fact = %#v", err)
	}

	_, err = execution.Invoke(context.Background(), "operations/status", json.RawMessage(`{"value":"x"}`), contexture.AllRoots())
	refusal := refused(t, err)
	var wrong *contexture.WrongDoorError
	if !errors.As(err, &wrong) || !errors.Is(err, contexture.ErrWrongDoor) || wrong.Ref != "operations/status" || !wrong.ReadOnly || !strings.Contains(refusal.Error(), string(contexture.InvokeReadOnlyGatewayName)) {
		t.Fatalf("wrong execution door = %#v", err)
	}

	_, err = execution.InvokeReadOnly(context.Background(), "operations/missing", json.RawMessage(`{}`), contexture.AllRoots())
	var missing *contexture.NodeNotFoundError
	if !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) || !strings.Contains(refused(t, err).Error(), string(contexture.OpenGatewayName)) {
		t.Fatalf("missing execution ref = %#v", err)
	}
	_, err = execution.ReadForHost(context.Background(), "operations/missing", contexture.AllRoots())
	if !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) || !strings.Contains(refused(t, err).Error(), string(contexture.OpenGatewayName)) {
		t.Fatalf("missing host resource ref = %#v", err)
	}
	_, err = gateway.InvokeReadOnly(context.Background(), "prompt", json.RawMessage(`{}`), contexture.AllRoots())
	if !errors.As(err, &promptRefusal) || promptCalls.Load() != 1 {
		t.Fatalf("gateway prompt invocation = %#v; calls = %d", err, promptCalls.Load())
	}
	_, err = gateway.Open("prompt", contexture.AllRoots())
	if !errors.As(err, &promptRefusal) || !strings.Contains(promptRefusal.Error(), "opened by a person") {
		t.Fatalf("gateway prompt open = %#v", err)
	}
}

func TestExecutionAPIPreservesRootCeilingsBeforePromptRefusal(t *testing.T) {
	var promptCalls atomic.Int32
	operations, err := contexture.OnlyRoots("operations")
	if err != nil {
		t.Fatal(err)
	}
	execution, _ := executionAPI(t, operations, &promptCalls)
	_, err = execution.InvokeReadOnly(context.Background(), "prompt", json.RawMessage(`{}`), contexture.AllRoots())
	var outside *contexture.RootOutsideSelectionError
	var refusal *contexture.RefusedError
	if !errors.As(err, &outside) || errors.As(err, &refusal) || strings.Contains(err.Error(), "person") || promptCalls.Load() != 0 {
		t.Fatalf("excluded prompt invocation = %#v; calls = %d", err, promptCalls.Load())
	}
	_, err = execution.ReadForHost(context.Background(), "prompt", contexture.AllRoots())
	if !errors.As(err, &outside) || promptCalls.Load() != 0 {
		t.Fatalf("excluded host read = %#v; calls = %d", err, promptCalls.Load())
	}
	if _, err := contexture.NewExecutionAPI(nil); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("NewExecutionAPI(nil) = %v", err)
	}
}
