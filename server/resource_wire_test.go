package server_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/CarterShi01/contexture-mcp-go/server/surface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type resourceWireInput struct{}

func TestOfficialSDKListsAndReadsOnlySelectedContextureResources(t *testing.T) {
	included, err := contexture.NewTool("included", "Included document.", true, func(context.Context, resourceWireInput) (string, error) {
		return "# Included\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := contexture.NewTool("excluded", "Excluded document.", true, func(context.Context, resourceWireInput) (string, error) {
		return "# Excluded\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "resource-wire",
		Roots: []contexture.Factory{
			func() contexture.Node { return included },
			func() contexture.Node { return excluded },
		},
		Resources: []contexture.ResourceDeclaration{
			{Opens: "included", URI: "contexture://included", Description: "Included document.", MIMEType: "text/markdown"},
			{Opens: "excluded", URI: "contexture://excluded", Description: "Excluded document.", MIMEType: "text/markdown"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	selection, err := contexture.OnlyRoots("included")
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServerForRoots(
		server.Identity{Name: "resource-test", Version: "0.0.0"}, gateway, selection, compiled.Publications,
	)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "resource-test-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := adapter.Server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	listed, err := clientSession.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Resources) != 1 {
		t.Fatalf("resources = %#v", listed.Resources)
	}
	resource := listed.Resources[0]
	if resource.Name != "included" || resource.URI != "contexture://included" || resource.Description != "Included document." || resource.MIMEType != "text/markdown" {
		t.Fatalf("resource = %#v", resource)
	}
	read, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "contexture://included"})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || read.Contents[0].URI != "contexture://included" || read.Contents[0].Text != "# Included\n" || read.Contents[0].MIMEType != "text/markdown" {
		t.Fatalf("resource content = %#v", read.Contents)
	}
	if _, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "contexture://excluded"}); err == nil {
		t.Fatal("reading a resource outside the fixed root selection succeeded")
	}
}

// makeResourceTargetStale simulates a Host retaining a publication after the
// target it once validated has disappeared. Public construction cannot create
// this state by design, so the test-only mutation is intentionally narrow: it
// verifies the defensive read boundary rather than weakening validation.
func makeResourceTargetStale(t *testing.T, publications *surface.Publications, ref string) {
	t.Helper()
	resources := reflect.ValueOf(publications).Elem().FieldByName("resources")
	if !resources.IsValid() || resources.Len() != 1 || !resources.CanAddr() {
		t.Fatalf("unexpected Publications resource storage: %#v", publications)
	}
	entry := resources.Index(0)
	opens := entry.FieldByName("Opens")
	if !opens.IsValid() || !opens.CanAddr() {
		t.Fatal("unexpected ResourceDeclaration Opens storage")
	}
	reflect.NewAt(opens.Type(), unsafe.Pointer(opens.UnsafeAddr())).Elem().SetString(ref)
}

func TestOfficialSDKResourceReaderUsesHostReadRefusalForStaleTarget(t *testing.T) {
	runbook, err := contexture.NewTool("runbook", "Read the runbook.", true, func(context.Context, resourceWireInput) (string, error) {
		return "# Runbook\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "resource-stale-wire",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return runbook }}}
		}},
		Resources: []contexture.ResourceDeclaration{{Opens: "operations/runbook", URI: "contexture://operations/runbook", Description: "Runbook.", MIMEType: "text/markdown"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := compiled.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(server.Identity{Name: "resource-stale-wire", Version: "0.0.0"}, gateway, compiled.Publications)
	makeResourceTargetStale(t, compiled.Publications, "operations/missing")

	_, err = compiled.Publications.Read(context.Background(), "contexture://operations/runbook", contexture.AllRoots())
	var refusal *contexture.RefusedError
	var missing *contexture.NodeNotFoundError
	if !errors.As(err, &refusal) || !errors.As(err, &missing) || !errors.Is(err, contexture.ErrNodeNotFound) || !strings.Contains(refusal.Error(), "contexture_open") {
		t.Fatalf("stale publication read = %#v; want RefusedError wrapping lookup facts", err)
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "resource-stale-client", Version: "0.0.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := adapter.Server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	_, err = clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "contexture://operations/runbook"})
	if err == nil || !strings.Contains(err.Error(), "Role 'operations' holds no member named 'missing'") || !strings.Contains(err.Error(), "contexture_open") {
		t.Fatalf("official stale resource read = %v; want host-read refusal text", err)
	}
}
