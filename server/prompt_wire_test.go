package server_test

import (
	"context"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfficialSDKReservesPromptTargetFromModelOpenButNotPersonPrompt(t *testing.T) {
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "prompt-reservation",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
				return &contexture.Skill{Name: "change-window", Description: "Run an approved change.", Instructions: "Confirm approval."}
			}}}
		}},
		Prompts: []contexture.Prompt{{Name: "run-change", Opens: "operations/change-window", Description: "Run the approved change.", ModelOpen: contexture.ModelReservedForPerson}},
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
	adapter := server.NewContextureMCPServer(server.Identity{Name: "prompt-reservation", Version: "0.0.0"}, gateway, compiled.Publications)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "prompt-client", Version: "0.0.0"}, nil)
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
	opened, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.OpenGatewayName), Arguments: map[string]any{"ref": "operations"}})
	if err != nil || opened.IsError {
		t.Fatalf("parent open = %#v, %v", opened, err)
	}
	reserved, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: string(contexture.OpenGatewayName), Arguments: map[string]any{"ref": "operations/change-window"}})
	if err != nil || !reserved.IsError || len(reserved.Content) != 1 || !strings.Contains(textOf(reserved.Content[0]), "opened by a person") {
		t.Fatalf("reserved model open = %#v, %v", reserved, err)
	}
	prompt, err := clientSession.GetPrompt(ctx, &mcp.GetPromptParams{Name: "run-change"})
	if err != nil || len(prompt.Messages) != 1 || !strings.Contains(textOf(prompt.Messages[0].Content), "Confirm approval.") {
		t.Fatalf("person prompt = %#v, %v", prompt, err)
	}
}
