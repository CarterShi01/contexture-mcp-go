package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestStoreCLINativeSubprocessJourneys(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "contexture")
	build := exec.Command("go", "build", "-o", executable, ".")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Actual CLI build: %v\n%s", err, raw)
	}
	schemaPath := os.Getenv("CONTEXTURE_MANAGED_SCHEMA")
	rawSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := graph.DecodeJSON(rawSchema)
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "source-secret-directory")
	options := server.JournalOptions{Schema: schema, Durability: "process_crash",
		Projector: server.GraphJournalProjector(managed.GraphJournalProjectionOptions{})}
	journal, err := server.OpenFileJournal(store, options)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	ref, err := journal.PutBlob([]byte("source-secret-payload"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Append([]any{map[string]any{"type": "RecoveryActionRecorded", "action_id": "original",
		"action": "retain source-secret-payload", "evidence": ref}}, server.JournalAppendOptions{ActorRef: "source-secret-actor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(store, "writer.lock")
	originalLock, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	runCLI := func(t *testing.T, operation string, path string, extra []string, want int, code string) server.StoreResult {
		t.Helper()
		args := []string{"store", operation, "--store", path, "--schema", schemaPath, "--durability", "process_crash", "--json"}
		args = append(args, extra...)
		command := exec.Command(executable, args...)
		command.Dir = root
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		status := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			status = exit.ExitCode()
		}
		t.Logf("actual CLI %s exit=%d stdout=%s stderr=%s", operation, status, stdout.Bytes(), stderr.Bytes())
		if status != want {
			t.Fatalf("want %d got %d", want, status)
		}
		for _, secret := range []string{"source-secret", schemaPath, root} {
			if strings.Contains(stdout.String()+stderr.String(), secret) {
				t.Fatal("Closed diagnostics disclosed private source facts")
			}
		}
		var result server.StoreResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Code != code || result.ServingReady || result.WriteEnabled || result.DesiredPrepared {
			t.Fatal("CLI diagnostics or readiness claims differ", result, err)
		}
		return result
	}
	trust := []string{"--trusted-offline"}
	t.Run("authority", func(t *testing.T) { runCLI(t, "doctor", store, nil, 3, "authority_required") })
	t.Run("active-writer", func(t *testing.T) {
		runCLI(t, "doctor", store, trust, 4, "store_in_use")
		info, err := os.Stat(lockPath)
		if err != nil || !os.SameFile(originalLock, info) {
			t.Fatal("Active lifetime lock replaced", err)
		}
	})
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	metaBefore, err := os.ReadFile(filepath.Join(store, "META.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("doctor", func(t *testing.T) {
		result := runCLI(t, "doctor", store, trust, 0, "ok")
		if !result.StorageVerified || !result.ProjectionVerified || result.IsolatedRestoreVerified {
			t.Fatal(result)
		}
		metaAfter, err := os.ReadFile(filepath.Join(store, "META.json"))
		if err != nil || !bytes.Equal(metaBefore, metaAfter) {
			t.Fatal("Read-only doctor changed META", err)
		}
	})
	privateArtifacts := filepath.Join(root, "private-artifacts")
	publicArtifacts := filepath.Join(root, "public-artifacts")
	if err := os.Mkdir(privateArtifacts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(publicArtifacts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(publicArtifacts, 0755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(privateArtifacts, "backup.json")
	t.Run("backup", func(t *testing.T) {
		runCLI(t, "backup", store, []string{"--trusted-offline", "--output", filepath.Join(publicArtifacts, "forbidden.json")}, 1, "unsafe_permissions")
		runCLI(t, "backup", store, []string{"--trusted-offline", "--output", artifact}, 0, "ok")
		runCLI(t, "backup", store, []string{"--trusted-offline", "--output", filepath.Join(store, "forbidden.json")}, 1, "unsafe_path")
	})
	t.Run("exclusive-publication", func(t *testing.T) {
		runCLI(t, "backup", store, []string{"--trusted-offline", "--output", artifact}, 4, "restore_conflict")
	})
	clone := filepath.Join(root, "isolated")
	t.Run("restore", func(t *testing.T) {
		result := runCLI(t, "restore", clone, []string{"--trusted-offline", "--input", artifact}, 0, "ok")
		if !result.IsolatedRestoreVerified {
			t.Fatal(result)
		}
		restored, err := server.OpenFileJournal(clone, options)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		if restored.Meta()["store_id"] == journal.Meta()["store_id"] || restored.Meta()["restored_from"] == nil {
			t.Fatal("CLI reused source store identity or lost provenance")
		}
	})
	t.Run("no-live-overwrite", func(t *testing.T) {
		runCLI(t, "restore", clone, []string{"--trusted-offline", "--input", artifact}, 4, "restore_conflict")
	})
	t.Run("software-version", func(t *testing.T) {
		runCLI(t, "doctor", store, []string{"--trusted-offline", "--require-reader-build", "1.0.0"}, 5, "software_unsupported")
	})
	t.Run("not-ready", func(t *testing.T) {
		missing := filepath.Join(root, "not-ready")
		runCLI(t, "doctor", missing, trust, 6, "store_missing")
		if _, err := os.Lstat(missing); !os.IsNotExist(err) {
			t.Fatal("Doctor initialized missing store", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(root, "linked")
		if err := os.Symlink(store, link); err != nil {
			t.Fatal(err)
		}
		runCLI(t, "doctor", link, trust, 1, "unsafe_path")
	})
	t.Run("wrong-format", func(t *testing.T) {
		meta, err := graph.DecodeJSON(metaBefore)
		if err != nil {
			t.Fatal(err)
		}
		meta.(map[string]any)["schema"] = "contexture.file-journal/v99"
		raw, err := graph.CanonicalJSON(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store, "META.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		runCLI(t, "doctor", store, trust, 5, "format_unsupported")
		if err := os.WriteFile(filepath.Join(store, "META.json"), metaBefore, 0600); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("corruption", func(t *testing.T) {
		record := filepath.Join(store, "records", strings.Repeat("0", 20), strings.Repeat("0", 19)+"1.json")
		if err := os.WriteFile(record, []byte("{private-corruption source-secret}"), 0600); err != nil {
			t.Fatal(err)
		}
		runCLI(t, "doctor", store, trust, 1, "corruption")
	})
	info, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(originalLock, info) {
		t.Fatal("Offline operations removed stable lifetime lock", err)
	}
}

func TestStoreProjectCLIBypassesApplicationCompilation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := cliStoreWithoutApplication(&stdout, &stderr)
	if status != 3 || !strings.Contains(stdout.String(), "authority_required") {
		t.Fatal(status, stdout.String(), stderr.String())
	}
}

func cliStoreWithoutApplication(stdout, stderr *bytes.Buffer) int {
	return run([]string{"store", "doctor", "--store", "unused-private", "--schema", "unused", "--json"}, stdout, stderr)
}
