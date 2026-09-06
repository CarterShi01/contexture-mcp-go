package server_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
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
