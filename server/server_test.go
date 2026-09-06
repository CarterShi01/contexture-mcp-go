package server_test

import (
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestNewMCPServerUsesOfficialSDK(t *testing.T) {
	t.Parallel()

	mcpServer := server.NewMCPServer(server.Identity{
		Name:    "contexture-test",
		Version: "0.0.0",
	})
	if mcpServer == nil {
		t.Fatal("NewMCPServer() returned nil")
	}
}
