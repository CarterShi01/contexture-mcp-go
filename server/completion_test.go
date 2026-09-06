package server_test

import (
	"context"
	"fmt"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfficialSDKReturnsSelectedGotoCompletionsWithTrueTotal(t *testing.T) {
	roots := make([]contexture.Factory, 0, 101)
	for number := range 101 {
		name := fmt.Sprintf("capability-%03d", number)
		roots = append(roots, func() contexture.Node {
			return &contexture.Skill{Name: name, Description: "Capability.", Instructions: "Use it."}
		})
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "completion", Roots: roots})
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
	adapter := server.NewContextureMCPServer(server.Identity{Name: "completion-test", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "completion-client", Version: "0.0.0"}, nil)
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

	result, err := clientSession.Complete(ctx, &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "goto"},
		Argument: mcp.CompleteParamsArgument{Name: "ref", Value: "capability"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Completion.Total != 101 || !result.Completion.HasMore || len(result.Completion.Values) != 100 {
		t.Fatalf("completion = %#v", result.Completion)
	}
	if got, want := result.Completion.Values[99], "... 1 more match; keep typing to narrow."; got != want {
		t.Fatalf("truncated completion = %q, want %q", got, want)
	}
	for number := range 99 {
		want := fmt.Sprintf("capability-%03d", number)
		if got := result.Completion.Values[number]; got != want {
			t.Fatalf("completion[%d] = %q, want %q", number, got, want)
		}
	}

	empty, err := clientSession.Complete(ctx, &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "other-command"},
		Argument: mcp.CompleteParamsArgument{Name: "ref", Value: "capability"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Completion.Values) != 0 {
		t.Fatalf("unsupported completion leaked refs: %#v", empty.Completion)
	}
}
