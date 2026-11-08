package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/cli"
	"github.com/CarterShi01/contexture-mcp-go/introspection"
)

func TestCatalogCLIExactFullEnvelopeRefsArrayAndTextShapes(t *testing.T) {
	status, text, stderr := runDemo(t, "catalog", "--json")
	if status != 0 || stderr != "" {
		t.Fatal(status, stderr)
	}
	snapshot, err := introspection.SnapshotFromJSON(strings.TrimSpace(text))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Application().View != introspection.ModelView || snapshot.Application().Detail != introspection.SummaryDetail || len(snapshot.Nodes()) != 12 {
		t.Fatal("default Catalog profile")
	}
	if status, text, stderr := runDemo(t, "catalog"); status != 0 || stderr != "" ||
		!strings.HasPrefix(text, "contexture-demo  runtime  model/summary\ndigest  sha256:") ||
		!strings.Contains(text, "\n  role   kubernetes-platform/incident-response  — ") {
		t.Fatal("full text shape", status, text, stderr)
	}
	ref := "kubernetes-platform/incident-response"
	status, text, stderr = runDemo(t, "catalog", ref, "--detail", "definition", "--json", "--target", "contexture-demo")
	if status != 0 || stderr != "" {
		t.Fatal(status, stderr)
	}
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(text), &nodes); err != nil || len(nodes) != 1 || nodes[0]["ref"] != ref || nodes[0]["role"] == nil {
		t.Fatal("ref JSON must be a descriptor array", err, text)
	}
	if status, text, stderr := runDemo(t, "catalog", ref); status != 0 || stderr != "" || !strings.HasPrefix(text, "role   "+ref+"  — ") || strings.Contains(text, "digest") {
		t.Fatal("ref text shape", text, stderr)
	}
	status, text, stderr = runDemo(t, "catalog", "--view", "surface", "--select", ref, "--locale", "zh_Hans_CN", "--target", "application", "--json")
	if status != 0 || stderr != "" {
		t.Fatal(status, stderr)
	}
	snapshot, err = introspection.SnapshotFromJSON(strings.TrimSpace(text))
	if err != nil || len(snapshot.Roots()) != 1 || snapshot.Roots()[0] != ref || *snapshot.Application().Locale != "zh-Hans-CN" {
		t.Fatal("CLI surface/locale", err)
	}
}

func TestCatalogCLIRefErrorsReadonlyAndUsageBeforeCompilation(t *testing.T) {
	for _, arguments := range [][]string{
		{"catalog", "--view", "bad"}, {"catalog", "--detail", "bad"}, {"catalog", "--view", "surface"},
		{"catalog", "--select", "ops"}, {"catalog", "--locale"}, {"catalog", "--host", "localhost"},
		{"debug", "REF"}, {"debug", "--json"}, {"debug", "--port", "-1"}, {"debug", "--port", "1.5"},
		{"debug", "--unknown", "x"},
	} {
		out, errout := &bytes.Buffer{}, &bytes.Buffer{}
		status := cli.RunApplication(context.Background(), &contexture.Application{}, arguments, out, errout)
		if status != 2 || out.Len() != 0 {
			t.Fatal("usage must precede declaration", arguments, status, out.String(), errout.String())
		}
	}
	for _, arguments := range [][]string{
		{"catalog", "missing"}, {"catalog", "--view", "surface", "--select", "missing"}, {"catalog", "--locale", "bad--locale"},
	} {
		status, out, errout := runDemo(t, arguments...)
		if status != 1 || out != "" || errout == "" {
			t.Fatal("runtime Catalog error", arguments, status, out, errout)
		}
	}
	if status, out, _ := runDemo(t, "catalog", "--target", "evil.module:factory"); status != 2 || out != "" {
		t.Fatal("arbitrary source target accepted")
	}
}

type catalogNotice struct{ text chan string }

func (n catalogNotice) Write(body []byte) (int, error) { n.text <- string(body); return len(body), nil }

func TestDebugCLIRealHTTPBlocksCancelsAndNeverInvokesHandler(t *testing.T) {
	calls := &atomic.Int32{}
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "consumer", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "ops", Description: "native", Instructions: "native\n", Tools: []contexture.Factory{func() contexture.Node {
			tool, err := contexture.NewTool("danger", "never call", false, func(context.Context, struct{}) (bool, error) { calls.Add(1); return true, nil })
			if err != nil {
				panic(err)
			}
			return tool
		}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notice := catalogNotice{make(chan string, 1)}
	stderr := &bytes.Buffer{}
	done := make(chan int, 1)
	go func() {
		done <- cli.RunApplication(ctx, app, []string{"debug", "--port", "0", "--host", "localhost"}, notice, stderr)
	}()
	var text string
	select {
	case text = <-notice.text:
	case <-time.After(5 * time.Second):
		t.Fatal("debug CLI not ready")
	}
	address := strings.TrimSpace(strings.TrimPrefix(text, "Contexture Node Catalog: "))
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(address + "/api/catalog")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal(err, response.StatusCode)
	}
	snapshot, err := introspection.SnapshotFromJSON(string(body))
	if err != nil || snapshot.Application().View != introspection.ApplicationView || snapshot.Application().Detail != introspection.DefinitionDetail {
		t.Fatal("debug defaults", err)
	}
	select {
	case status := <-done:
		t.Fatal("debug must block", status)
	default:
	}
	cancel()
	select {
	case status := <-done:
		if status != 0 || stderr.Len() != 0 {
			t.Fatal(status, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("debug CLI failed to stop")
	}
	if calls.Load() != 0 {
		t.Fatal("CLI invoked handler")
	}
}

func TestDebugCLIStartupCanceledAndUnsafeBindRefused(t *testing.T) {
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "native", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "root", Description: "root", Instructions: "root"}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, errout := &bytes.Buffer{}, &bytes.Buffer{}
	if status := cli.RunApplication(ctx, app, []string{"debug", "--port", "0"}, out, errout); status != 1 || out.Len() != 0 {
		t.Fatal("canceled debug started", status, out.String())
	}
	out.Reset()
	errout.Reset()
	if status := cli.RunApplication(context.Background(), app, []string{"debug", "--host", "0.0.0.0", "--port", "0"}, out, errout); status != 1 || out.Len() != 0 {
		t.Fatal("public debug bind accepted", status, out.String())
	}
}
