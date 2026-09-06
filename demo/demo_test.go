package demo_test

import (
	"context"
	"encoding/json"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/demo"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestApplicationIsLazyAndRunsTheReferenceDiagnosis(t *testing.T) {
	application, err := demo.Application()
	if err != nil {
		t.Fatal(err)
	}
	if application.Name() != "contexture-demo" || application.RootCount() != 1 {
		t.Fatalf("application = %#v", application)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := compiled.Disclosure.Discover(contexture.AllRoots())
	if err != nil || len(discovered["roles"]) != 1 {
		t.Fatalf("Discover() = %#v, %v", discovered, err)
	}
	input, _ := json.Marshal(map[string]string{"namespace": demo.Namespace, "pod": demo.Pod})
	value, err := compiled.Runtime.InvokeReadOnly(context.Background(), "kubernetes-platform/incident-response/get_pod_status", input, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	status, ok := value.(demo.PodStatus)
	if !ok || status.RestartCount != 14 || status.Ready {
		t.Fatalf("status = %#v", value)
	}
	if _, err := compiled.Runtime.Invoke(context.Background(), "kubernetes-platform/deployment-ops/roll_back_deployment", []byte(`{"namespace":"prod","deployment":"payments-api"}`), contexture.AllRoots()); err != nil {
		t.Fatal(err)
	}
}
