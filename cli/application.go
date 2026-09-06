// Package cli runs a statically linked Go Contexture application through the
// same local workflows that the contexture executable exposes.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/inspection"
	"github.com/CarterShi01/contexture-mcp-go/server"
	"github.com/CarterShi01/contexture-mcp-go/server/instructions"
)

// RunApplication runs project-owned commands over one statically declared Go application.
func RunApplication(ctx context.Context, application *contexture.Application, arguments []string, stdout, stderr io.Writer) int {
	if application == nil {
		return fail(stderr, 1, "Contexture application must not be nil.")
	}
	if len(arguments) == 0 {
		return fail(stderr, 2, "expected check, list, inspect, call, or serve.")
	}
	compiled, err := server.CompileApplication(application)
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	switch arguments[0] {
	case "check":
		if len(arguments) != 1 {
			return fail(stderr, 2, "use check with no project target; this executable already owns one application.")
		}
		roles, skills, tools := 0, 0, 0
		for _, ref := range compiled.Index.Walk() {
			node, _ := compiled.Index.Find(ref)
			switch node.Kind() {
			case contexture.RoleKind:
				roles++
			case contexture.SkillKind:
				skills++
			case contexture.ToolKind:
				tools++
			}
		}
		_, _ = fmt.Fprintf(stdout, "OK %s: %d role(s), %d skill(s), %d tool(s)\n", application.Name(), roles, skills, tools)
		return 0
	case "list":
		if len(arguments) != 1 {
			return fail(stderr, 2, "use list with no project target; this executable already owns one application.")
		}
		for _, ref := range compiled.Index.Walk() {
			node, _ := compiled.Index.Find(ref)
			indent := strings.Repeat("  ", strings.Count(ref, "/"))
			switch typed := node.(type) {
			case *contexture.Role:
				_, _ = fmt.Fprintf(stdout, "%s%s  — %s\n", indent, typed.Name, typed.Description)
			case *contexture.Skill:
				_, _ = fmt.Fprintf(stdout, "%s  skill     %s\n", indent, ref)
			case *contexture.Tool:
				access := "needs approval"
				if typed.ReadOnly {
					access = "read-only"
				}
				_, _ = fmt.Fprintf(stdout, "%s  tool      %s  (%s)\n", indent, ref, access)
			}
		}
		return 0
	case "inspect":
		return runInspect(ctx, compiled, arguments[1:], stdout, stderr)
	case "call":
		return runCall(ctx, compiled, arguments[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, application, arguments[1:], stdout, stderr)
	default:
		return fail(stderr, 2, "expected check, list, inspect, call, or serve.")
	}
}

func runInspect(ctx context.Context, application *server.RuntimeApplication, arguments []string, stdout, stderr io.Writer) int {
	refs, all, asJSON, summary, includeRead, includeDiscover := []string{}, false, false, false, false, true
	rosterBudget := instructions.RosterBudget
	for len(arguments) > 0 {
		argument := arguments[0]
		arguments = arguments[1:]
		switch argument {
		case "--all":
			all = true
		case "--json":
			asJSON = true
		case "--summary":
			summary = true
		case "--read":
			includeRead = true
		case "--no-discover":
			includeDiscover = false
		case "--roster-budget":
			if len(arguments) == 0 || strings.HasPrefix(arguments[0], "--") {
				return fail(stderr, 2, "--roster-budget needs a non-negative integer.")
			}
			parsed, err := strconv.Atoi(arguments[0])
			arguments = arguments[1:]
			if err != nil || parsed < 0 {
				return fail(stderr, 2, "--roster-budget needs a non-negative integer.")
			}
			rosterBudget = parsed
		default:
			if strings.HasPrefix(argument, "--") {
				return fail(stderr, 2, "use inspect [REF ...] [--all] [--read] [--summary] [--json] [--no-discover] [--roster-budget BYTES].")
			}
			refs = append(refs, argument)
		}
	}
	if all && len(refs) > 0 {
		return fail(stderr, 2, "pass named refs or --all, not both.")
	}
	if all {
		refs = inspection.EveryRef(application.Disclosure)
	}
	text, err := instructions.Build(application.Disclosure, contexture.AllRoots(), rosterBudget)
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	trace := inspection.Replay(ctx, application.Disclosure, application.Runtime, refs, includeDiscover, includeRead, text)
	if asJSON {
		rendered, err := inspection.AsJSON(trace)
		if err != nil {
			return fail(stderr, 1, err.Error())
		}
		_, _ = fmt.Fprintln(stdout, rendered)
	} else {
		_, _ = fmt.Fprintln(stdout, inspection.Render(trace, !summary))
	}
	if len(trace.Failures()) > 0 {
		return 1
	}
	return 0
}

func runCall(ctx context.Context, application *server.RuntimeApplication, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return fail(stderr, 2, "call needs a Tool ref.")
	}
	ref, arguments := arguments[0], arguments[1:]
	input, allowWrite, inputSource := []byte("{}"), false, ""
	for len(arguments) > 0 {
		if arguments[0] == "--allow-write" {
			if allowWrite {
				return fail(stderr, 2, "use --allow-write at most once.")
			}
			allowWrite, arguments = true, arguments[1:]
			continue
		}
		if (arguments[0] != "--input" && arguments[0] != "--input-file") || len(arguments) < 2 {
			return fail(stderr, 2, "use call REF [--input JSON | --input-file FILE] [--allow-write].")
		}
		if inputSource != "" {
			return fail(stderr, 2, "use exactly one of --input or --input-file.")
		}
		inputSource = arguments[0]
		if inputSource == "--input" {
			input = []byte(arguments[1])
		} else {
			contents, err := os.ReadFile(arguments[1])
			if err != nil {
				return fail(stderr, 2, "cannot read Tool input file: "+err.Error())
			}
			input = contents
		}
		arguments = arguments[2:]
	}
	var decoded map[string]any
	if err := json.Unmarshal(input, &decoded); err != nil {
		return fail(stderr, 2, "Tool input must be a JSON object: "+err.Error())
	}
	if decoded == nil {
		return fail(stderr, 2, "Tool input must be a JSON object.")
	}
	tool, err := application.Runtime.Tool(ref)
	if err != nil {
		return fail(stderr, 2, "Cannot find Tool "+fmt.Sprintf("%q", ref)+". Run contexture list, or inspect a Role for its Tool refs.")
	}
	if !tool.ReadOnly && !allowWrite {
		return fail(stderr, 2, ref+" is not read-only. Re-run with --allow-write only when you intend this local call to change external state.")
	}
	var value any
	err = application.Runtime.Serve(ctx, func(ctx context.Context) error {
		if tool.ReadOnly {
			value, err = application.Runtime.InvokeReadOnly(ctx, ref, input, contexture.AllRoots())
		} else {
			value, err = application.Runtime.Invoke(ctx, ref, input, contexture.AllRoots())
		}
		return err
	})
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	if text, ok := value.(string); ok {
		_, _ = fmt.Fprintln(stdout, text)
		return 0
	}
	rendered, err := json.Marshal(value)
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	_, _ = fmt.Fprintln(stdout, string(rendered))
	return 0
}

func runServe(ctx context.Context, application *contexture.Application, arguments []string, stdout, stderr io.Writer) int {
	options, err := parseServeOptions(arguments)
	if err != nil {
		return fail(stderr, 2, err.Error())
	}
	assembly, err := server.BuildServer(application)
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	if options.Transport == server.StreamableHTTPTransport {
		_, _ = fmt.Fprintln(stdout, "Serving "+application.Name()+" at "+options.URL())
	}
	if err := assembly.Start(ctx, options); err != nil {
		return fail(stderr, 1, err.Error())
	}
	return 0
}

func parseServeOptions(arguments []string) (*server.ContextureOptions, error) {
	options := server.ContextureOptions{Transport: server.StdioTransport}
	seen := map[string]bool{}
	for len(arguments) > 0 {
		argument := arguments[0]
		arguments = arguments[1:]
		switch argument {
		case "--allow-anonymous":
			if seen[argument] {
				return nil, fmt.Errorf("use --allow-anonymous at most once")
			}
			seen[argument] = true
			options.AllowAnonymous = true
		case "--transport", "--host", "--port", "--path", "--allow-host", "--allow-origin":
			if len(arguments) == 0 || strings.HasPrefix(arguments[0], "--") {
				return nil, fmt.Errorf("%s needs a value", argument)
			}
			value := arguments[0]
			arguments = arguments[1:]
			switch argument {
			case "--transport":
				if seen[argument] {
					return nil, fmt.Errorf("use --transport at most once")
				}
				seen[argument] = true
				options.Transport = server.Transport(value)
			case "--host":
				if seen[argument] {
					return nil, fmt.Errorf("use --host at most once")
				}
				seen[argument] = true
				options.Host = value
			case "--port":
				if seen[argument] {
					return nil, fmt.Errorf("use --port at most once")
				}
				seen[argument] = true
				port, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("--port must be an integer: %w", err)
				}
				options.Port = port
			case "--path":
				if seen[argument] {
					return nil, fmt.Errorf("use --path at most once")
				}
				seen[argument] = true
				options.Path = value
			case "--allow-host":
				options.AllowedHosts = append(options.AllowedHosts, value)
			case "--allow-origin":
				options.AllowedOrigins = append(options.AllowedOrigins, value)
			}
		default:
			return nil, fmt.Errorf("use serve [--transport stdio|streamable-http] [--host HOST] [--port PORT] [--path PATH] [--allow-host HOST] [--allow-origin ORIGIN] [--allow-anonymous]")
		}
	}
	return server.NewContextureOptions(options)
}

func fail(stderr io.Writer, status int, message string) int {
	_, _ = fmt.Fprintln(stderr, "contexture: "+message)
	return status
}
