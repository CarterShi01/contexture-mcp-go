package server

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Launch is the host-facing command that starts one Contexture stdio server.
type Launch struct {
	Name    string
	Command string
	Args    []string
}

// AsList returns a fresh command vector suitable for process spawning.
func (launch Launch) AsList() []string {
	return append([]string{launch.Command}, launch.Args...)
}

// AsShell returns the POSIX-shell spelling used by host installation commands.
func (launch Launch) AsShell() string {
	parts := launch.AsList()
	for index, part := range parts {
		parts[index] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

// ClaudeCodeConfig renders Claude Code's project-scoped .mcp.json configuration.
func ClaudeCodeConfig(launch Launch) string {
	payload := struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}{MCPServers: map[string]struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{launch.Name: {Type: "stdio", Command: launch.Command, Args: append([]string{}, launch.Args...)}}}
	var result bytes.Buffer
	encoder := json.NewEncoder(&result)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		panic(err)
	}
	return result.String()
}

// CursorConfig renders Cursor's .cursor/mcp.json, which uses the same shape.
func CursorConfig(launch Launch) string { return ClaudeCodeConfig(launch) }

// CodexConfig renders the Contexture stanza for Codex's config.toml.
func CodexConfig(launch Launch) string {
	args := make([]string, len(launch.Args))
	for index, argument := range launch.Args {
		args[index] = pythonJSONString(argument)
	}
	return "[mcp_servers." + launch.Name + "]\ncommand = " + pythonJSONString(launch.Command) + "\nargs = [" + strings.Join(args, ", ") + "]\n"
}

// CLICommands returns the documented one-line install command for each host.
func CLICommands(launch Launch) map[string]string {
	return map[string]string{
		"claude-code": "claude mcp add --scope project " + launch.Name + " -- " + launch.AsShell(),
		"codex":       "codex mcp add " + launch.Name + " -- " + launch.AsShell(),
	}
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(character rune) bool {
		return !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_@%+=:,./-", character)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func pythonJSONString(value string) string {
	var result bytes.Buffer
	encoder := json.NewEncoder(&result)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return asciiJSON(strings.TrimSuffix(result.String(), "\n"))
}

func asciiJSON(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character <= 0x7f {
			result.WriteRune(character)
			continue
		}
		if character <= 0xffff {
			result.WriteString("\\u")
			result.WriteString(hex4(uint16(character)))
			continue
		}
		offset := character - 0x10000
		result.WriteString("\\u")
		result.WriteString(hex4(uint16(0xd800 + offset>>10)))
		result.WriteString("\\u")
		result.WriteString(hex4(uint16(0xdc00 + offset&0x3ff)))
	}
	return result.String()
}

func hex4(value uint16) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[(value>>12)&15], digits[(value>>8)&15], digits[(value>>4)&15], digits[value&15]})
}
