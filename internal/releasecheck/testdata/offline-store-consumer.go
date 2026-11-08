package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

// This explicit test custody is not a production approval fallback.
type testCustody struct{}

func (testCustody) Approve(ctx context.Context, request server.OfflineStoreRequest) error {
	if request.Operation == "" || request.Path == "" {
		return errors.New("unapproved offline request")
	}
	return ctx.Err()
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	root, err := os.MkdirTemp("", "contexture-fresh-offline-consumer-")
	must(err)
	defer os.RemoveAll(root)
	schemaBytes, err := os.ReadFile(os.Getenv("CONTEXTURE_MANAGED_SCHEMA"))
	must(err)
	schema, err := graph.DecodeJSON(schemaBytes)
	must(err)
	options := server.JournalOptions{Schema: schema, Durability: "strict",
		Projector: server.GraphJournalProjector(managed.GraphJournalProjectionOptions{})}
	source := filepath.Join(root, "source")
	journal, err := server.OpenFileJournal(source, options)
	must(err)
	ref, err := journal.PutBlob([]byte("fresh native source"), "text/plain")
	must(err)
	_, err = journal.Append([]any{map[string]any{"type": "RecoveryActionRecorded",
		"action_id": "native", "action": "retained original", "evidence": ref}}, server.JournalAppendOptions{ActorRef: "operator"})
	must(err)
	_, err = journal.Checkpoint()
	must(err)
	sourceID := journal.Meta()["store_id"]
	must(journal.Close())
	ops, err := server.NewStoreOperations(server.StoreOperationsOptions{
		Schema: schema, Durability: "strict", Projector: options.Projector, Custody: testCustody{},
		Compatibility: server.NativeJournalCompatibility()})
	must(err)
	ctx := context.Background()
	result, err := ops.Doctor(ctx, source, nil)
	must(err)
	if result.Code != "ok" || !result.StorageVerified || result.ServingReady || result.WriteEnabled {
		panic("doctor confused storage with serving")
	}
	backup, err := ops.Backup(ctx, source, nil)
	must(err)
	output := filepath.Join(root, "backup.json")
	must(ops.PublishBackup(ctx, output, backup, false))
	bundle, err := os.ReadFile(output)
	must(err)
	clone := filepath.Join(root, "clone")
	result, err = ops.Restore(ctx, clone, bundle)
	must(err)
	if !result.IsolatedRestoreVerified || result.WriteEnabled || result.DesiredPrepared {
		panic("restore did not prove isolation")
	}
	restored, err := server.OpenFileJournal(clone, options)
	must(err)
	defer restored.Close()
	if restored.Meta()["store_id"] == sourceID || restored.Meta()["restored_from"] == nil {
		panic("restore reused source identity or lost provenance")
	}
	records, err := restored.Records("0")
	must(err)
	if len(records) != 1 || records[0]["actor_ref"] != "operator" {
		panic("real restored native history differs")
	}
	fmt.Println("fresh typed offline store consumer passed")
}
