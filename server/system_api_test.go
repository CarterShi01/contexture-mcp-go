package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestGatewayWithPublicationsPreservesGatewayRecoveryAndReservationTypes(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "gateway-publications",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "change", Description: "Change.", Instructions: "Ask."}
			}}}
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
}
