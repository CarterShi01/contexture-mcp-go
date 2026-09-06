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

func recoveryGateway(t *testing.T, calls *atomic.Int32) *contexture.Gateway {
	t.Helper()
	read, err := contexture.NewTool("read", "Read.", true, func(context.Context, struct{}) (string, error) {
		calls.Add(1)
		return "read", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("write", "Write.", false, func(context.Context, struct{}) (string, error) { return "write", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "gateway-recovery", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "alpha", Description: "Alpha.", Instructions: "Inspect."}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "beta", Description: "Beta.", Instructions: "Operate.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
		},
	}})
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
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func refused(t *testing.T, err error) *contexture.RefusedError {
	t.Helper()
	var refusal *contexture.RefusedError
	if !errors.As(err, &refusal) {
		t.Fatalf("error %T = %v, want RefusedError", err, err)
	}
	return refusal
}

func TestGatewayHasFixedOrderedSurfaceAndActionableLookupRecoveries(t *testing.T) {
	var calls atomic.Int32
	gateway := recoveryGateway(t, &calls)
	tools := contexture.GatewayTools()
	want := []contexture.GatewayName{contexture.DiscoverGatewayName, contexture.OpenGatewayName, contexture.InvokeReadOnlyGatewayName, contexture.InvokeGatewayName}
	if len(tools) != len(want) {
		t.Fatalf("tools = %#v", tools)
	}
	for i, name := range want {
		if tools[i].Name != name {
			t.Fatalf("tools[%d] = %q, want %q", i, tools[i].Name, name)
		}
	}
	if len(contexture.DisclosureGatewayTools()) != 2 || len(contexture.ExecutionGatewayTools()) != 2 {
		t.Fatal("gateway halves are not fixed")
	}
	// All lookup facts become a recovery that names the one viable next call.
	for _, check := range []struct{ ref, contains string }{
		{"", "contexture_discover"},
		{"missing", "This server serves: alpha, beta"},
		{"alpha/nope", "It holds nothing"},
		{"beta/nope", "It holds: read, write"},
		{"beta/read/again", "contexture_open"},
	} {
		_, err := gateway.Open(check.ref, contexture.AllRoots())
		if got := refused(t, err).Error(); !strings.Contains(got, check.contains) {
			t.Fatalf("open %q = %q, missing %q", check.ref, got, check.contains)
		}
	}
	// Asking an invocation door to run a non-tool describes opening it instead.
	_, err := gateway.InvokeReadOnly(context.Background(), "beta", json.RawMessage("{}"), contexture.AllRoots())
	if got := refused(t, err).Error(); !strings.Contains(got, "names a role, not a tool") || !strings.Contains(got, "contexture_open") {
		t.Fatalf("wrong kind = %q", got)
	}
	// Door validation occurs before the business handler and identifies the other door.
	_, err = gateway.Invoke(context.Background(), "beta/read", json.RawMessage("{}"), contexture.AllRoots())
	if got := refused(t, err).Error(); !strings.Contains(got, "contexture_invoke_read_only") || calls.Load() != 0 {
		t.Fatalf("wrong door = %q, calls = %d", got, calls.Load())
	}
}

func TestGatewayDoesNotRecoverRootCeilingAsAnAgentLookup(t *testing.T) {
	var calls atomic.Int32
	gateway := recoveryGateway(t, &calls)
	alpha, err := contexture.OnlyRoots("alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, err = gateway.Open("beta/read", alpha)
	var outside *contexture.RootOutsideSelectionError
	if !errors.As(err, &outside) || outside.Ref != "beta/read" {
		t.Fatalf("error = %#v, want typed outside-selection", err)
	}
	var refusal *contexture.RefusedError
	if errors.As(err, &refusal) || strings.Contains(err.Error(), "alpha") {
		t.Fatalf("ceiling leaked or was recovered: %v", err)
	}
}
