package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/CarterShi01/contexture-mcp-go/cli"
	"github.com/CarterShi01/contexture-mcp-go/demo"
)

const cliVersion = "0.12.0rc1"

var errNoProject = errors.New("no Go Contexture project found in this directory or above it")

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run executes implemented Contexture CLI workflows and returns a process exit code.
func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 1 && arguments[0] == "--version" {
		_, _ = fmt.Fprintln(stdout, cliVersion)
		return 0
	}
	if len(arguments) == 0 {
		_, _ = fmt.Fprintln(stderr, "contexture: expected new, list, check, call, inspect, serve, or demo.")
		return 2
	}
	if arguments[0] != "new" {
		return runProjectCommand(arguments, stdout, stderr)
	}
	if len(arguments) < 2 || arguments[1] == "" || arguments[1][0] == '-' {
		_, _ = fmt.Fprintln(stderr, "contexture: `contexture new` needs a project name.")
		return 2
	}
	name, remainder, destination, template, templateSet := arguments[1], arguments[2:], "", "project", false
	for len(remainder) > 0 {
		if len(remainder) < 2 || (remainder[0] != "--into" && remainder[0] != "--template") {
			_, _ = fmt.Fprintln(stderr, "contexture: use `contexture new NAME [--into DIR] [--template NAME]`.")
			return 2
		}
		switch remainder[0] {
		case "--into":
			if destination != "" {
				_, _ = fmt.Fprintln(stderr, "contexture: use --into at most once.")
				return 2
			}
			destination = remainder[1]
		case "--template":
			if templateSet {
				_, _ = fmt.Fprintln(stderr, "contexture: use --template at most once.")
				return 2
			}
			template = remainder[1]
			templateSet = true
		}
		remainder = remainder[2:]
	}
	if template != "project" {
		_, _ = fmt.Fprintf(stderr, "contexture: unknown template %q. Available: project.\n", template)
		return 2
	}
	root, err := NewProject(name, destination)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "contexture: %v\n", err)
		if _, ok := err.(*UsageError); ok {
			return 2
		}
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "Wrote "+root)
	_, _ = fmt.Fprintln(stdout, "Next: go mod tidy && go run ./cmd/assistant check")
	return 0
}

func runProjectCommand(arguments []string, stdout, stderr io.Writer) int {
	command := arguments[0]
	if command == "demo" {
		application, err := demo.Application()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "contexture: cannot build bundled demo: %v\n", err)
			return 1
		}
		return cli.RunApplication(context.Background(), application, append([]string{"serve"}, arguments[1:]...), stdout, stderr)
	}
	if command != "list" && command != "check" && command != "call" && command != "inspect" && command != "serve" {
		_, _ = fmt.Fprintln(stderr, "contexture: expected new, list, check, call, inspect, serve, or demo.")
		return 2
	}
	root, err := findProject()
	if err != nil {
		if command == "inspect" && errors.Is(err, errNoProject) {
			application, demoErr := demo.Application()
			if demoErr != nil {
				_, _ = fmt.Fprintf(stderr, "contexture: cannot build bundled demo: %v\n", demoErr)
				return 1
			}
			_, _ = fmt.Fprintln(stderr, "No Contexture project was found, so this is the bundled demo.")
			return cli.RunApplication(context.Background(), application, arguments, stdout, stderr)
		}
		_, _ = fmt.Fprintf(stderr, "contexture: %v\n", err)
		return 2
	}
	run := exec.Command("go", append([]string{"run", "./cmd/assistant"}, arguments...)...)
	run.Dir, run.Stdout, run.Stderr = root, stdout, stderr
	if err := run.Run(); err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			return exited.ExitCode()
		}
		_, _ = fmt.Fprintf(stderr, "contexture: cannot run project application: %v\n", err)
		return 1
	}
	return 0
}

func findProject() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		module := filepath.Join(current, "go.mod")
		entry := filepath.Join(current, "cmd", "assistant", "main.go")
		if info, err := os.Stat(module); err == nil && !info.IsDir() {
			if info, err := os.Stat(entry); err == nil && !info.IsDir() {
				return current, nil
			}
			return "", fmt.Errorf("found %s but no generated cmd/assistant application. Run contexture new, or add your project entry point.", module)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("%w. Run contexture new first.", errNoProject)
}
