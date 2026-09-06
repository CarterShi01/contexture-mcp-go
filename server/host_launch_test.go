package server_test

import (
	"reflect"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestLaunchRendersHostConfigurationAndSafeInstallCommands(t *testing.T) {
	launch := server.Launch{Name: "operations", Command: "node", Args: []string{"dist/cli/main.js", "serve", "--label", "O'Reilly & sons"}}
	if got, want := launch.AsList(), []string{"node", "dist/cli/main.js", "serve", "--label", "O'Reilly & sons"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AsList() = %#v, want %#v", got, want)
	}
	if got, want := launch.AsShell(), `node dist/cli/main.js serve --label 'O'"'"'Reilly & sons'`; got != want {
		t.Fatalf("AsShell() = %q, want %q", got, want)
	}
	wantJSON := "{\n  \"mcpServers\": {\n    \"operations\": {\n      \"type\": \"stdio\",\n      \"command\": \"node\",\n      \"args\": [\n        \"dist/cli/main.js\",\n        \"serve\",\n        \"--label\",\n        \"O'Reilly & sons\"\n      ]\n    }\n  }\n}\n"
	if got := server.ClaudeCodeConfig(launch); got != wantJSON {
		t.Fatalf("ClaudeCodeConfig() = %q", got)
	}
	if got := server.CursorConfig(launch); got != wantJSON {
		t.Fatalf("CursorConfig() = %q", got)
	}
	if got, want := server.CodexConfig(launch), "[mcp_servers.operations]\ncommand = \"node\"\nargs = [\"dist/cli/main.js\", \"serve\", \"--label\", \"O'Reilly & sons\"]\n"; got != want {
		t.Fatalf("CodexConfig() = %q, want %q", got, want)
	}
}

func TestLaunchPreservesPythonCompatibleEmptyAndUnicodeTOML(t *testing.T) {
	launch := server.Launch{Name: "unicode", Command: "程序", Args: []string{"", "世界"}}
	if got, want := launch.AsShell(), "'程序' '' '世界'"; got != want {
		t.Fatalf("AsShell() = %q, want %q", got, want)
	}
	if got, want := server.CodexConfig(launch), "[mcp_servers.unicode]\ncommand = \"\\u7a0b\\u5e8f\"\nargs = [\"\", \"\\u4e16\\u754c\"]\n"; got != want {
		t.Fatalf("CodexConfig() = %q, want %q", got, want)
	}
}
