package main

import (
	"fmt"
	"io"
	"os"
)

const cliVersion = "0.12.0rc1"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run executes implemented Contexture CLI workflows and returns a process exit code.
func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 1 && arguments[0] == "--version" {
		_, _ = fmt.Fprintln(stdout, cliVersion)
		return 0
	}
	if len(arguments) == 0 || arguments[0] != "new" {
		_, _ = fmt.Fprintln(stderr, "contexture: expected `contexture new NAME [--into DIR]`, or `contexture --version`. Other product commands are not installed in this build.")
		return 2
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
