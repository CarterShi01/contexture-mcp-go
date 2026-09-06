package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const cliVersion = "0.12.0rc1"

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
	name, remainder, destination := arguments[1], arguments[2:], ""
	for len(remainder) > 0 {
		if remainder[0] != "--into" || destination != "" || len(remainder) < 2 {
			_, _ = fmt.Fprintln(stderr, "contexture: use `contexture new NAME [--into DIR]`.")
			return 2
		}
		destination, remainder = remainder[1], remainder[2:]
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
	_, _ = fmt.Fprintln(stdout, "Next: go mod tidy && go run ./cmd/assistant")
	return 0
}

func runProjectCommand(arguments []string, stdout, stderr io.Writer) int {
	command := arguments[0]
	if command == "demo" {
		_, _ = fmt.Fprintln(stderr, "contexture: demo is not installed in this build yet.")
		return 2
	}
	if command != "list" && command != "check" && command != "call" && command != "inspect" && command != "serve" {
		_, _ = fmt.Fprintln(stderr, "contexture: expected new, list, check, call, inspect, serve, or demo.")
		return 2
	}
	root, err := findProject()
	if err != nil {
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
	return "", fmt.Errorf("no Go Contexture project found in this directory or above it. Run contexture new first.")
}
