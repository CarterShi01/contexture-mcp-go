package contexture_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestPublicAuthoringConceptsResolveThroughRootPackage(t *testing.T) {
	var _ contexture.Channels
	var _ contexture.Factory
	var _ contexture.Prompt
	var _ contexture.Resource
	var _ contexture.Role
	var _ contexture.Skill
	var _ contexture.Tool
	var _ contexture.Principal
	var _ *contexture.NodeNotFoundError
	var _ contexture.Telemetry
	var _ contexture.NodeUsage
	values := map[string]any{
		"Contexture":       contexture.Contexture,
		"NewPrincipal":     contexture.NewPrincipal,
		"CurrentGraph":     contexture.CurrentGraph,
		"CurrentPrincipal": contexture.CurrentPrincipal,
		"CurrentTelemetry": contexture.CurrentTelemetry,
		"Version":          contexture.Version,
	}
	for name, value := range values {
		if value == nil {
			t.Fatalf("public authoring concept %s did not resolve", name)
		}
	}
	_ = context.Background()
}

func TestPublicServerConceptsResolveThroughServerPackage(t *testing.T) {
	var _ *contexture.Runtime
	var _ *server.RuntimeApplication
	var _ *server.ApplicationServer
	var _ server.ContextureOptions
	var _ server.RootSelector
	var _ server.RootCeiling
	var _ server.TokenVerifier
	values := map[string]any{
		"CompileApplication":   server.CompileApplication,
		"BuildServer":          server.BuildServer,
		"NewContextureOptions": server.NewContextureOptions,
		"ConfigureLogging":     server.ConfigureLogging,
		"ClaudeCodeConfig":     server.ClaudeCodeConfig,
		"CLICommands":          server.CLICommands,
		"CodexConfig":          server.CodexConfig,
		"CursorConfig":         server.CursorConfig,
		"DefaultHost":          server.DefaultHost,
		"DefaultPath":          server.DefaultPath,
		"DefaultPort":          server.DefaultPort,
	}
	for name, value := range values {
		if value == nil {
			t.Fatalf("public server concept %s did not resolve", name)
		}
	}
}
