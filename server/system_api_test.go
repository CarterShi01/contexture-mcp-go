package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestGatewayWithPublicationsPreservesGatewayRecoveryAndReservationTypes(t *testing.T) {
	called := false
	reservedTool, err := contexture.NewTool("change", "Change.", true, func(context.Context, struct{}) (string, error) {
		called = true
		return "changed", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "gateway-publications",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return reservedTool }}}
		}, func() contexture.Node {
			return &contexture.Role{Name: "other", Description: "Other.", Instructions: "Inspect."}
		}},
		Prompts: []contexture.Prompt{{Name: "change-command", Opens: "operations/change", Description: "Change.", ModelOpen: contexture.ModelReservedForPerson}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}

	_, err = callGateway(context.Background(), gateway, contexture.OpenGatewayName, json.RawMessage(`{"ref":"missing"}`), contexture.AllRoots(), compiled.Publications)
	var missing *contexture.RefusedError
	if !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) {
		t.Fatalf("missing publication open = %#v; want RefusedError wrapping lookup facts", err)
	}
	_, err = callGateway(context.Background(), gateway, contexture.OpenGatewayName, json.RawMessage(`{"ref":"operations/change"}`), contexture.AllRoots(), compiled.Publications)
	var reserved *contexture.RefusedError
	if !errors.As(err, &reserved) || !errors.Is(err, contexture.ErrNodeNotFound) && reserved.Cause != nil {
		t.Fatalf("reserved publication open = %#v; want typed RefusedError without lookup failure", err)
	}
	_, err = callGateway(context.Background(), gateway, contexture.InvokeReadOnlyGatewayName, json.RawMessage(`{"ref":"operations/change","arguments":{}}`), contexture.AllRoots(), compiled.Publications)
	if !errors.As(err, &reserved) || !strings.Contains(reserved.Error(), "opened by a person") || called {
		t.Fatalf("reserved publication invocation = %#v; called=%t", err, called)
	}
	other, err := contexture.OnlyRoots("other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = callGateway(context.Background(), gateway, contexture.InvokeReadOnlyGatewayName, json.RawMessage(`{"ref":"operations/change","arguments":{}}`), other, compiled.Publications)
	var outside *contexture.RootOutsideSelectionError
	if !errors.As(err, &outside) || strings.Contains(err.Error(), "person") {
		t.Fatalf("publication ceiling must win over reservation: %#v", err)
	}
}
