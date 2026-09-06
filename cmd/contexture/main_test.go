package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestInspectOutsideAProjectReplaysTheBundledDemo(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	var stdout, stderr bytes.Buffer
	if status := run([]string{"inspect", "--summary"}, &stdout, &stderr); status != 0 {
		t.Fatalf("inspect outside project = %d, stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "bundled demo") {
		t.Fatalf("fallback notice missing from stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "session start") {
		t.Fatalf("demo inspection missing from stdout: %q", stdout.String())
	}
}
