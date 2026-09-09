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

func TestCurrentGraphFailsFastOutsideAnInvocation(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("CurrentGraph outside invocation did not panic")
		}
	}()
	contexture.CurrentGraph(context.Background())
}

func TestCurrentTelemetryFailsFastOutsideAnInvocation(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("CurrentTelemetry outside invocation did not panic")
		}
	}()
	contexture.CurrentTelemetry(context.Background())
}

func TestRuntimeUsesBindingDoorsAndRequestContext(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(ctx context.Context, input runtimeInput) (string, error) {
		if principal := contexture.CurrentPrincipal(ctx); principal == nil || principal.Subject() != "alice" {
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
	value, err := runtime.InvokeReadOnly(contexture.WithPrincipal(context.Background(), contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "alice"})), "operations/status", json.RawMessage(`{"value":"ok"}`), contexture.AllRoots())
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

func TestRuntimeWrongDoorErrorRetainsFactsWithoutCallingToolOrTelemetry(t *testing.T) {
	called := false
	write, err := contexture.NewTool("change", "Change.", false, func(context.Context, runtimeInput) (string, error) {
		called = true
		return "changed", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "wrong-door", Roots: []contexture.Factory{func() contexture.Node {
		return write
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

	_, err = runtime.InvokeReadOnly(context.Background(), "change", json.RawMessage(`{"value":"x"}`), contexture.AllRoots())
	var wrong *contexture.WrongDoorError
	if !errors.As(err, &wrong) || !errors.Is(err, contexture.ErrWrongDoor) {
		t.Fatalf("wrong-door error = %T %v", err, err)
	}
	if wrong.Ref != "change" || wrong.ReadOnly {
		t.Fatalf("wrong-door facts = %#v", wrong)
	}
	if called || len(telemetry.Events()) != 0 {
		t.Fatalf("wrong-door call reached business or telemetry: called=%t events=%#v", called, telemetry.Events())
	}
}

type failingTelemetry struct{}

func (failingTelemetry) Record(contexture.CallEvent) error {
	return errors.New("telemetry unavailable")
}

func (failingTelemetry) Usage(ref string) contexture.NodeUsage { return contexture.NodeUsage{Ref: ref} }

func TestRuntimeSelectionOnlyAttenuatesCeilingAndTelemetryCannotChangeOutcome(t *testing.T) {
	tool, err := contexture.NewTool("status", "Status.", true, func(context.Context, runtimeInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "ceilings", Roots: []contexture.Factory{
		func() contexture.Node {
			return &contexture.Role{Name: "allowed", Description: "Allowed.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
		},
		func() contexture.Node {
			return &contexture.Role{Name: "other", Description: "Other.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node {
				clone, _ := contexture.NewTool("status", "Status.", true, func(context.Context, runtimeInput) (string, error) { return "other", nil })
				return clone
			}}}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	ceiling, err := contexture.OnlyRoots("allowed")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), ceiling, failingTelemetry{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.InvokeReadOnly(context.Background(), "allowed/status", json.RawMessage(`{"value":"ignored"}`), contexture.AllRoots())
	if err != nil || value != "ok" {
		t.Fatalf("allowed invocation = %#v, %v", value, err)
	}
	if _, err := runtime.InvokeReadOnly(context.Background(), "other/status", json.RawMessage(`{"value":"ignored"}`), contexture.AllRoots()); err == nil {
		t.Fatal("caller selection widened the identity ceiling")
	}
	other, err := contexture.OnlyRoots("other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InvokeReadOnly(context.Background(), "allowed/status", json.RawMessage(`{"value":"ignored"}`), other); err == nil {
		t.Fatal("caller selection did not attenuate the ceiling")
	}
}

func TestRuntimeRequestFactsAreConcurrentAndLocal(t *testing.T) {
	tool, err := contexture.NewTool("who", "Who.", true, func(ctx context.Context, _ runtimeInput) (string, error) {
		principal := contexture.CurrentPrincipal(ctx)
		graph := contexture.CurrentGraph(ctx)
		if graph == nil || len(graph.Walk()) != 2 {
			return "", errors.New("unexpected graph")
		}
		if principal == nil {
			return "", errors.New("missing principal")
		}
		return principal.Subject(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "concurrent", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "root", Description: "Root.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return tool }}}
	}}})
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
	results := make(chan string, 2)
	errs := make(chan error, 2)
	for _, subject := range []string{"alice", "bob"} {
		subject := subject
		go func() {
			principal := contexture.NewPrincipal(contexture.PrincipalOptions{Subject: subject})
			value, callErr := runtime.InvokeReadOnly(contexture.WithPrincipal(context.Background(), principal), "root/who", json.RawMessage(`{"value":"x"}`), contexture.AllRoots())
			if callErr != nil {
				errs <- callErr
				return
			}
			results <- value.(string)
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case err := <-errs:
			t.Fatal(err)
		case result := <-results:
			seen[result] = true
		}
	}
	if !seen["alice"] || !seen["bob"] {
		t.Fatalf("request principals leaked: %#v", seen)
	}
}

func TestPrincipalSnapshotsHostIdentityFacts(t *testing.T) {
	claims := map[string]any{"tenant": "acme"}
	principal := contexture.NewPrincipal(contexture.PrincipalOptions{
		Subject:  "ada",
		ClientID: "codex",
		Issuer:   "https://issuer.example",
		Scopes:   []string{"tools.read"},
		Claims:   claims,
	})
	claims["tenant"] = "mutated"

	if principal.Subject() != "ada" || principal.ClientID() != "codex" || principal.Issuer() != "https://issuer.example" {
		t.Fatalf("unexpected identity: %#v", principal)
	}
	if !principal.HasScope("tools.read") || principal.HasScope("tools.write") {
		t.Fatalf("unexpected scopes: %#v", principal.Scopes())
	}
	returnedClaims := principal.Claims()
	returnedClaims["tenant"] = "changed again"
	if principal.Claims()["tenant"] != "acme" {
		t.Fatalf("claims were not immutable: %#v", principal.Claims())
	}
}
