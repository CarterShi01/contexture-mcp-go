package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/cli"
	"github.com/CarterShi01/contexture-mcp-go/demo"
)

func runDemo(t *testing.T, arguments ...string) (int, string, string) {
	t.Helper()
	application, err := demo.Application()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	status := cli.RunApplication(context.Background(), application, arguments, stdout, stderr)
	return status, stdout.String(), stderr.String()
}

func TestRunApplicationRunsLocalProjectWorkflows(t *testing.T) {
	if status, stdout, stderr := runDemo(t, "check"); status != 0 || !strings.Contains(stdout, "OK contexture-demo: 3 role(s), 2 skill(s), 7 tool(s)") || stderr != "" {
		t.Fatalf("check = %d, %q, %q", status, stdout, stderr)
	}
	if status, stdout, stderr := runDemo(t, "list"); status != 0 || !strings.Contains(stdout, "roll_back_deployment  (needs approval)") || stderr != "" {
		t.Fatalf("list = %d, %q, %q", status, stdout, stderr)
	}
	if status, stdout, stderr := runDemo(t, "inspect", "--all", "--json"); status != 0 || !strings.Contains(stdout, "contexture_discover") || stderr != "" {
		t.Fatalf("inspect = %d, %q, %q", status, stdout, stderr)
	}
	if status, stdout, stderr := runDemo(t, "call", "kubernetes-platform/incident-response/get_pod_logs", "--input", `{"namespace":"prod","pod":"payments-api-7d9c"}`); status != 0 || !strings.Contains(stdout, "DB_URL is missing") || stderr != "" {
		t.Fatalf("call = %d, %q, %q", status, stdout, stderr)
	}
	if status, _, stderr := runDemo(t, "call", "kubernetes-platform/deployment-ops/roll_back_deployment", "--input", `{"namespace":"prod","deployment":"payments-api"}`); status != 2 || !strings.Contains(stderr, "--allow-write") {
		t.Fatalf("unsafe write = %d, %q", status, stderr)
	}
	if status, stdout, stderr := runDemo(t, "call", "kubernetes-platform/deployment-ops/roll_back_deployment", "--input", `{"namespace":"prod","deployment":"payments-api"}`, "--allow-write"); status != 0 || !strings.Contains(stdout, "Rolled prod/payments-api") || stderr != "" {
		t.Fatalf("approved write = %d, %q, %q", status, stdout, stderr)
	}
}

func TestServeArgumentsShareTheSafeTransportPolicy(t *testing.T) {
	for _, arguments := range [][]string{
		{"serve", "--host", "127.0.0.1"},
		{"serve", "--transport", "stdio", "--port", "8000"},
		{"serve", "--transport", "streamable-http", "--host", "0.0.0.0"},
		{"serve", "--transport", "not-a-transport"},
		{"serve", "--port", "not-a-number"},
	} {
		status, _, stderr := runDemo(t, arguments...)
		if status != 2 || !strings.HasPrefix(stderr, "contexture: ") {
			t.Fatalf("serve %q = %d, %q", arguments, status, stderr)
		}
	}
}

func TestInspectControlsItsRealDisclosureReplay(t *testing.T) {
	status, stdout, stderr := runDemo(t, "inspect", "--all", "--read", "--no-discover", "--json")
	if status != 0 || stderr != "" {
		t.Fatalf("inspect = %d, %q, %q", status, stdout, stderr)
	}
	if strings.Contains(stdout, `"call": "contexture_discover"`) {
		t.Fatalf("--no-discover was ignored: %q", stdout)
	}
	if count := strings.Count(stdout, `"call": "contexture_invoke_read_only"`); count != 2 {
		t.Fatalf("--read ran %d content calls, want 2: %q", count, stdout)
	}
	if status, _, _ := runDemo(t, "inspect", "--all", "--roster-budget", "0"); status != 1 {
		t.Fatalf("truncated roster status = %d, want 1", status)
	}
	if status, _, stderr := runDemo(t, "inspect", "--roster-budget", "-1"); status != 2 || !strings.Contains(stderr, "roster-budget") {
		t.Fatalf("negative roster budget = %d, %q", status, stderr)
	}
}

func TestCallReadsJSONInputFromAFile(t *testing.T) {
	inputFile := filepath.Join(t.TempDir(), "pod.json")
	if err := os.WriteFile(inputFile, []byte(`{"namespace":"prod","pod":"payments-api-7d9c"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr := runDemo(t, "call", "kubernetes-platform/incident-response/get_pod_logs", "--input-file", inputFile)
	if status != 0 || !strings.Contains(stdout, "DB_URL is missing") || stderr != "" {
		t.Fatalf("input file call = %d, %q, %q", status, stdout, stderr)
	}
	if status, _, stderr := runDemo(t, "call", "kubernetes-platform/incident-response/get_pod_logs", "--input", `{}`, "--input-file", inputFile); status != 2 || !strings.Contains(stderr, "exactly one") {
		t.Fatalf("duplicate input source = %d, %q", status, stderr)
	}
}
