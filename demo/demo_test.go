package demo_test

import (
	"context"
	"encoding/json"
	"strings"
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

func TestDemoPreservesTheCompleteReferenceProceduresAndDocuments(t *testing.T) {
	application, err := demo.Application()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	skill, err := compiled.Disclosure.Open("kubernetes-platform/incident-response/diagnose-crash-loop-backoff", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if instructions, _ := skill["instructions"].(string); !strings.Contains(instructions, "Do not recommend restarting or deleting the Pod") || !strings.Contains(instructions, "contexture://runbooks/crash-loop-backoff") {
		t.Fatalf("diagnosis procedure was abbreviated: %#v", skill)
	}
	input, err := compiled.Runtime.InvokeReadOnly(context.Background(), "kubernetes-platform/incident-response/crash_loop_runbook", []byte("{}"), contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	runbook, _ := input.(string)
	if !strings.Contains(runbook, "## Common causes, in the order they are usually found") || !strings.Contains(runbook, "OOMKilled, exit 137") {
		t.Fatalf("runbook was abbreviated: %q", runbook)
	}
}

func TestDemoExposesLazyTopologyPublicationsAndBuilder(t *testing.T) {
	if demo.KubernetesPlatform().NodeName() != "kubernetes-platform" || demo.IncidentResponse().NodeName() != "incident-response" || demo.DeploymentOps().NodeName() != "deployment-ops" {
		t.Fatal("public demo role factories drifted")
	}
	if demo.RollBackARelease().Name != "roll-back-a-release" || demo.CrashLoopRunbookDocument().MIMEType != "text/markdown" || demo.RollbackPolicyDocument().URI != "contexture://runbooks/rollback-policy" {
		t.Fatal("public demo publications drifted")
	}
	application, err := demo.Application()
	if err != nil || application.RootCount() != 1 || len(application.Prompts()) != 1 || len(application.Resources()) != 2 {
		t.Fatalf("demo Application = %#v, %v", application, err)
	}
	built, err := demo.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := built.Build(); err != nil {
		t.Fatal(err)
	}
}
