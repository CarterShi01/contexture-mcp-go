package contexture_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server/surface"
)

type podInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
}

type logsInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Previous  bool   `json:"previous,omitempty" default:"false"`
}

type deploymentInput struct {
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
}

type noInput struct{}

const platformInstructions = "Route to the specialism the task belongs to, and open only that one. Diagnose\nbefore remediating: incident-response establishes a cause from evidence, and\ndeployment-ops reverses a release once the cause is known.\n"
const incidentInstructions = "Work from evidence, never from the shape of the question. Select the skill that\nmatches the reported symptom, follow its procedure, and collect tool output\nbefore naming a cause. Report the root cause and the smallest safe next action.\n"
const deploymentInstructions = "Remediation follows diagnosis and never replaces it. Read the policy, establish\nwhat the previous revision would restore, and say what evidence a rollback\ndestroys before proposing one. Anything that changes the cluster is run through\ncontexture_invoke, where a host can put a human in front of it.\n"
const diagnoseInstructions = "Establish the cause from evidence, in this order.\n\n1. Call get_pod_status. A high restart_count with ready=false confirms a\n   restart loop rather than a slow or pending start.\n2. Call get_pod_logs. The container's own output names the failure; read it\n   before forming a hypothesis.\n3. Call get_pod_events. Events tell you what the kubelet observed, including\n   the exit code, which separates an application failure from a kill.\n4. Call crash_loop_runbook and match the evidence you collected against its\n   table of causes. The same content is optionally published to hosts at\n   contexture://runbooks/crash-loop-backoff.\n\nThen report the root cause and the single smallest next action.\n\nConstraints:\n- Do not recommend restarting or deleting the Pod before the cause is known.\n  A restart does not repair a configuration error; it produces one more restart.\n- Do not state any cluster state you have not read from a tool.\n- Name the specific evidence, including the exit code, that supports your\n  conclusion."
const rollbackInstructions = "A rollback destroys the evidence it was called for. Work in this order.\n\n1. Call rollback_policy before doing anything else. The same content is\n   optionally published to hosts at contexture://runbooks/rollback-policy.\n2. Call get_rollout_status. Compare the current and previous image: if they\n   differ only in a tag, the cause may not be in the image at all.\n3. Establish the cause first, by opening the procedure listed under `uses` and\n   following it. A rollback that follows a guess will be needed again on the\n   next release.\n4. Only then call roll_back_deployment, and say plainly what evidence is lost.\n\nConstraints:\n- Do not roll back before the cause is known and the evidence is captured.\n- A configuration fault follows the previous revision back. Say so rather than\n  presenting a rollback as a fix.\n- roll_back_deployment changes the cluster. It is not read-only, so it must be\n  run through contexture_invoke and a human may be asked first."

func goldenFixture(t *testing.T, name string) string {
	t.Helper()
	uri := map[string]string{
		"CRASH_LOOP_RUNBOOK": "contexture://runbooks/crash-loop-backoff",
		"ROLLBACK_POLICY":    "contexture://runbooks/rollback-policy",
	}[name]
	body, ok := goldenJSON(t, "reads.json").(map[string]any)[uri].(string)
	if !ok {
		t.Fatalf("missing reference fixture %s", name)
	}
	return body
}

func goldenTool[I any](name, description string, readOnly bool, handler func(context.Context, I) (string, error)) contexture.Factory {
	return func() contexture.Node {
		tool, err := contexture.NewTool(name, description, readOnly, handler)
		if err != nil {
			panic(err)
		}
		return tool
	}
}

func goldenDemo(t *testing.T) (*contexture.Gateway, *surface.Publications) {
	t.Helper()
	crashLoop := goldenFixture(t, "CRASH_LOOP_RUNBOOK")
	rollbackPolicy := goldenFixture(t, "ROLLBACK_POLICY")
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "demo",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "kubernetes-platform", Description: "Operate a Kubernetes platform: diagnose incidents, and reverse releases.", Instructions: platformInstructions,
				Children: []contexture.Factory{
					func() contexture.Node {
						return &contexture.Role{Name: "incident-response", Description: "Diagnose unhealthy Kubernetes workloads from cluster evidence.", Instructions: incidentInstructions,
							Skills: []contexture.Factory{func() contexture.Node {
								return &contexture.Skill{Name: "diagnose-crash-loop-backoff", Description: "Find why a Pod restarts repeatedly, before proposing any remediation.", Instructions: diagnoseInstructions}
							}},
							Tools: []contexture.Factory{
								goldenTool("get_pod_status", "Return the current phase, container state, and restart count of a Pod.", true, func(context.Context, podInput) (string, error) { return "", nil }),
								goldenTool("get_pod_logs", "Return the recent container logs for a Pod.", true, func(context.Context, logsInput) (string, error) { return "", nil }),
								goldenTool("get_pod_events", "Return the Kubernetes events recorded against a Pod.", true, func(context.Context, podInput) (string, error) { return "", nil }),
								goldenTool("crash_loop_runbook", "How to diagnose a container that keeps restarting, and what not to do.", true, func(context.Context, noInput) (string, error) { return crashLoop, nil }),
							},
						}
					},
					func() contexture.Node {
						return &contexture.Role{Name: "deployment-ops", Description: "Inspect and reverse Kubernetes releases that have gone wrong.", Instructions: deploymentInstructions,
							Skills: []contexture.Factory{func() contexture.Node {
								return &contexture.Skill{Name: "roll-back-a-failed-release", Description: "Decide whether to roll a release back, and what to capture first.", Instructions: rollbackInstructions, Uses: []string{"kubernetes-platform/incident-response/diagnose-crash-loop-backoff"}}
							}},
							Tools: []contexture.Factory{
								goldenTool("get_rollout_status", "Return the current and previous revision of a Deployment's rollout.", true, func(context.Context, deploymentInput) (string, error) { return "", nil }),
								goldenTool("roll_back_deployment", "Restore a Deployment's previous revision, replacing its running Pods.", false, func(context.Context, deploymentInput) (string, error) { return "", nil }),
								goldenTool("rollback_policy", "When a rollback is the right remediation, and what it costs.", true, func(context.Context, noInput) (string, error) { return rollbackPolicy, nil }),
							},
						}
					},
				},
			}
		}},
		Prompts: []contexture.PromptDeclaration{{Name: "roll-back-a-release", Opens: "kubernetes-platform/deployment-ops/roll-back-a-failed-release", Description: "Put the rollback procedure in context: what to capture before a release is reversed, and what reversing it destroys."}},
		Resources: []contexture.ResourceDeclaration{
			{Opens: "kubernetes-platform/incident-response/crash_loop_runbook", URI: "contexture://runbooks/crash-loop-backoff", Description: "How to diagnose a container that keeps restarting, and what not to do.", MIMEType: "text/markdown"},
			{Opens: "kubernetes-platform/deployment-ops/rollback_policy", URI: "contexture://runbooks/rollback-policy", Description: "When a rollback is the right remediation, and what it costs.", MIMEType: "text/markdown"},
		},
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
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	publications, err := surface.NewPublications(application, disclosure, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return gateway, publications
}

func goldenJSON(t *testing.T, name string) any {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("conformance", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(source, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func canonical(value any) any {
	raw, _ := json.Marshal(value)
	var result any
	_ = json.Unmarshal(raw, &result)
	return result
}

func TestGoDemoMatchesDiscoverOpenAndRefusalGoldens(t *testing.T) {
	gateway, _ := goldenDemo(t)
	actual, err := gateway.Discover(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical(actual), goldenJSON(t, "discover.json")) {
		t.Fatalf("discover differs\n got: %#v\nwant: %#v", canonical(actual), goldenJSON(t, "discover.json"))
	}
	expectedOpen := goldenJSON(t, "open.json").(map[string]any)
	for ref, expected := range expectedOpen {
		actual, err := gateway.Open(ref, contexture.AllRoots())
		if err != nil {
			t.Fatalf("open %s: %v", ref, err)
		}
		if !reflect.DeepEqual(canonical(actual), expected) {
			t.Fatalf("open %s differs\n got: %#v\nwant: %#v", ref, canonical(actual), expected)
		}
	}
	refusals := goldenJSON(t, "refusals.json").(map[string]any)
	for call, expected := range refusals {
		ref := regexp.MustCompile(`'([^']*)'`).FindStringSubmatch(call)[1]
		var callErr error
		switch {
		case regexp.MustCompile(`^open`).MatchString(call):
			_, callErr = gateway.Open(ref, contexture.AllRoots())
		case regexp.MustCompile(`^invoke_read_only`).MatchString(call):
			_, callErr = gateway.InvokeReadOnly(context.Background(), ref, nil, contexture.AllRoots())
		default:
			_, callErr = gateway.Invoke(context.Background(), ref, nil, contexture.AllRoots())
		}
		if callErr == nil || callErr.Error() != expected.(string) {
			t.Fatalf("%s error = %v, want %q", call, callErr, expected)
		}
	}
}

func TestGoDemoFixedGatewayMatchesToolGolden(t *testing.T) {
	gateway, _ := goldenDemo(t)
	tools := gateway.Tools()
	expected := goldenJSON(t, "tools.json").([]any)
	if len(tools) != len(expected) {
		t.Fatalf("gateway tool count = %d, want %d", len(tools), len(expected))
	}
	for position, tool := range tools {
		entry := expected[position].(map[string]any)
		annotations := entry["annotations"].(map[string]any)
		if string(tool.Name) != entry["name"] || tool.Description != entry["description"] || tool.ReadOnly != annotations["read_only_hint"] {
			t.Fatalf("gateway tool[%d] = %#v, golden %#v", position, tool, entry)
		}
	}
}

func TestGoDemoMatchesPublicationGoldens(t *testing.T) {
	_, publications := goldenDemo(t)
	prompts, err := publications.PromptCards(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	expectedPrompts := goldenJSON(t, "prompts.json").([]any)
	actualPrompts := make([]any, 0, len(prompts))
	for _, prompt := range prompts {
		arguments := make([]any, 0, len(prompt.Arguments))
		for _, argument := range prompt.Arguments {
			arguments = append(arguments, map[string]any{"name": argument.Name, "required": argument.Required})
		}
		actualPrompts = append(actualPrompts, map[string]any{"name": prompt.Name, "description": prompt.Description, "arguments": arguments})
	}
	expectedPromptCards := make([]any, 0, len(expectedPrompts))
	for _, prompt := range expectedPrompts {
		entry := prompt.(map[string]any)
		arguments := make([]any, 0, len(entry["arguments"].([]any)))
		for _, argument := range entry["arguments"].([]any) {
			item := argument.(map[string]any)
			arguments = append(arguments, map[string]any{"name": item["name"], "required": item["required"]})
		}
		expectedPromptCards = append(expectedPromptCards, map[string]any{"name": entry["name"], "description": entry["description"], "arguments": arguments})
	}
	if !reflect.DeepEqual(actualPrompts, expectedPromptCards) {
		t.Fatalf("prompts differ\n got %#v\nwant %#v", actualPrompts, expectedPromptCards)
	}
	resources := publications.ResourceCards()
	actualResources := make([]any, 0, len(resources))
	for _, resource := range resources {
		actualResources = append(actualResources, map[string]any{"name": resource.Name, "uri": resource.URI, "description": resource.Description, "mime_type": resource.MIMEType})
	}
	expectedResources := goldenJSON(t, "resources.json").([]any)
	expectedResourceCards := make([]any, 0, len(expectedResources))
	for _, resource := range expectedResources {
		entry := resource.(map[string]any)
		expectedResourceCards = append(expectedResourceCards, map[string]any{"name": entry["name"], "uri": entry["uri"], "description": entry["description"], "mime_type": entry["mime_type"]})
	}
	if !reflect.DeepEqual(actualResources, expectedResourceCards) {
		t.Fatal("resources differ")
	}
	instructions, err := publications.Instructions(contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	expectedInstructions, err := os.ReadFile(filepath.Join("conformance", "golden", "instructions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	expectedText := strings.TrimSuffix(string(expectedInstructions), "\n")
	if instructions != expectedText {
		t.Fatalf("instructions differ\n got %q\nwant %q", instructions, expectedText)
	}
	completions := goldenJSON(t, "completions.json").(map[string]any)
	for input, expected := range completions {
		values, total, err := publications.Complete(input, contexture.AllRoots(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(canonical(map[string]any{"values": values, "total": total}), expected) {
			t.Fatalf("completion %q differs: got %#v want %#v", input, canonical(map[string]any{"values": values, "total": total}), expected)
		}
	}
	commands := goldenJSON(t, "commands.json").(map[string]any)
	command, err := publications.Command("roll-back-a-release", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if command != commands["roll-back-a-release"] {
		t.Fatalf("command differs\n got %s\nwant %s", command, commands["roll-back-a-release"])
	}
	gotoText, err := publications.Goto("kubernetes-platform/deployment-ops", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if gotoText != commands["goto"] {
		t.Fatalf("goto differs\n got %s\nwant %s", gotoText, commands["goto"])
	}
	reads := goldenJSON(t, "reads.json").(map[string]any)
	for uri, expected := range reads {
		value, err := publications.Read(context.Background(), uri, contexture.AllRoots())
		if err != nil {
			t.Fatal(err)
		}
		if value != expected {
			t.Fatalf("read %s differs", uri)
		}
	}
}

func TestDisclosureOnlyOmitsSchemasAndExecution(t *testing.T) {
	tool, err := contexture.NewTool("tool", "Tool.", true, func(context.Context, noInput) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "disclosure", Roots: []contexture.Factory{func() contexture.Node { return tool }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.CompileDisclosure(application)
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosureOnly(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := disclosure.Open("tool", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["input_schema"]; exists {
		t.Fatal("disclosure-only Tool leaked input schema")
	}
	disclosedTool, err := index.Find("tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disclosedTool.(*contexture.Tool).Binding(); err == nil {
		t.Fatal("disclosure-only Index retained a Tool Binding")
	}
	readOnlyGateway, err := contexture.NewGateway(disclosure, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnlyGateway.Tools()) != 2 {
		t.Fatal("disclosure-only gateway exposes invoke doors")
	}
	if _, err := readOnlyGateway.InvokeReadOnly(context.Background(), "tool", nil, contexture.AllRoots()); err == nil {
		t.Fatal("disclosure-only execution succeeded")
	}
}
