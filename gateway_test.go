package contexture_test

import (
	"errors"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestGatewayPublishesOnlyFixedSystemTools(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "gateway", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Skill{Name: "business", Description: "Business.", Instructions: "Use it."}
	}}})
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
	gateway, err := contexture.NewGateway(disclosure, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := gateway.Tools()
	if len(tools) != 3 || tools[0].Name != contexture.DiscoverGatewayName || tools[1].Name != contexture.InspectGatewayName || tools[2].Name != contexture.OpenGatewayName {
		t.Fatalf("disclosure gateway tools = %#v", tools)
	}
	if _, err := gateway.InvokeReadOnly(nil, "business", nil, contexture.AllRoots()); err == nil {
		t.Fatal("disclosure-only gateway invoked a Tool")
	}
	if _, err := gateway.Open("business", contexture.AllRoots()); err != nil && !errors.Is(err, contexture.ErrInvalidSelection) {
		t.Fatal(err)
	}
}
