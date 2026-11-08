package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalModuleConsumerManagedNativeHost(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("testdata", "managed-host-consumer.go"))
	if err != nil {
		t.Fatal(err)
	}
	consumer := t.TempDir()
	mod := "module managed-native-host-consumer\n\ngo 1.25.0\n\nrequire github.com/CarterShi01/contexture-mcp-go v0.0.0\n\nreplace github.com/CarterShi01/contexture-mcp-go => " + filepath.ToSlash(root) + "\n"
	for name, contents := range map[string][]byte{"go.mod": []byte(mod), "main.go": source} {
		if err := os.WriteFile(filepath.Join(consumer, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-mod=mod", ".")
	command.Dir = consumer
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh typed managed Host consumer: %v\n%s", err, output)
	}
	if string(output) != "fresh typed native Host consumer passed\n" {
		t.Fatalf("unexpected consumer output: %s", output)
	}
}
