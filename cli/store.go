package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/core/graph"
	"github.com/CarterShi01/contexture-mcp-go/managed"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

const storeUsage = "store doctor|backup|restore --store DIR --schema FILE --trusted-offline [--durability strict|process_crash] [--json]; backup: --output FILE [--interoperable]; restore: --input FILE; doctor/backup: [--anchor FILE]"

type localOfflineCustody struct{}

func (localOfflineCustody) Approve(ctx context.Context, _ server.OfflineStoreRequest) error {
	return ctx.Err()
}

// RunStore runs explicit trusted offline operations, without loading an
// application, invoking a handler, or manufacturing a graph/release Principal.
func RunStore(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, _ = fmt.Fprintln(stdout, "contexture "+storeUsage)
		return 0
	}
	if len(args) == 0 || args[0] != "doctor" && args[0] != "backup" && args[0] != "restore" {
		return fail(stderr, 2, storeUsage)
	}
	operation := args[0]
	flags := flag.NewFlagSet("store "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	store := flags.String("store", "", "existing source or fresh isolated restore directory")
	schema := flags.String("schema", "", "explicit registered managed schema")
	profile := flags.String("durability", "strict", "exact persisted durability profile")
	input := flags.String("input", "", "restore artifact")
	output := flags.String("output", "", "exclusive private backup artifact")
	anchorFile := flags.String("anchor", "", "independent durable anchor")
	asJSON := flags.Bool("json", false, "closed redacted diagnostics")
	trusted := flags.Bool("trusted-offline", false, "explicit trusted local operator; owner must be stopped")
	interop := flags.Bool("interoperable", false, "publish standard FileJournal snapshot")
	build := flags.String("require-reader-build", contexture.Version, "require this exact reader build, not a software fallback")
	seen := map[string]bool{}
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--") {
			name := strings.SplitN(strings.TrimPrefix(arg, "--"), "=", 2)[0]
			if seen[name] {
				return fail(stderr, 2, "store options must appear at most once.")
			}
			seen[name] = true
		}
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *store == "" || *schema == "" ||
		operation == "backup" && (*output == "" || *input != "") ||
		operation == "restore" && (*input == "" || *output != "" || *anchorFile != "" || *interop) ||
		operation == "doctor" && (*input != "" || *output != "" || *interop) {
		return fail(stderr, 2, storeUsage)
	}
	result := server.StoreResult{Operation: operation}
	report := func(err error) int {
		result.Code = server.StoreDiagnosticCode(err)
		if *asJSON {
			if e := json.NewEncoder(stdout).Encode(result); e != nil {
				return 1
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "store %s: %s; storage_verified=%t projection_verified=%t isolated_restore_verified=%t serving_ready=false write_enabled=false\n",
				operation, result.Code, result.StorageVerified, result.ProjectionVerified, result.IsolatedRestoreVerified)
		}
		if err == nil {
			return 0
		}
		switch result.Code {
		case "authority_required", "permission_denied", "approval_stale":
			return 3
		case "store_in_use", "resource_busy", "restore_conflict", "commit_unknown":
			return 4
		case "durability_unsupported", "format_unsupported", "software_unsupported", "semantic_unsupported", "unsupported_contract":
			return 5
		case "store_missing", "recovery_required", "history_unavailable", "reference_not_found":
			return 6
		default:
			return 1
		}
	}
	if !*trusted {
		return report(&server.JournalError{Diagnostic: "authority_required", Message: "Explicit trusted local offline operator required"})
	}
	if *build != contexture.Version {
		return report(&server.JournalError{Diagnostic: "software_unsupported", Message: "Requested reader build is not this executable"})
	}
	if *profile != "strict" && *profile != "process_crash" {
		return report(&server.JournalError{Diagnostic: "durability_unsupported", Message: "Unknown durability profile"})
	}
	rawSchema, err := readStoreInput(*schema, 1<<20)
	if err != nil {
		return report(err)
	}
	decoded, err := graph.DecodeJSON(rawSchema)
	if err != nil {
		return report(&server.JournalError{Diagnostic: "schema_invalid", Message: "Registered schema JSON invalid"})
	}
	ops, err := server.NewStoreOperations(server.StoreOperationsOptions{
		Schema: decoded, Durability: *profile, Projector: server.GraphJournalProjector(managed.GraphJournalProjectionOptions{}),
		Custody: localOfflineCustody{}, Compatibility: server.NativeJournalCompatibility(),
	})
	if err != nil {
		return report(err)
	}
	var anchor *server.StoreAnchor
	if *anchorFile != "" {
		raw, err := readStoreInput(*anchorFile, 4096)
		if err != nil {
			return report(err)
		}
		value, err := graph.DecodeJSON(raw)
		if err != nil {
			return report(&server.JournalError{Diagnostic: "schema_invalid", Message: "Anchor JSON invalid"})
		}
		object, ok := value.(map[string]any)
		if !ok || len(object) != 3 {
			return report(&server.JournalError{Diagnostic: "schema_invalid", Message: "Closed anchor required"})
		}
		storeID, idOK := object["store_id"].(string)
		seq, seqOK := object["journal_seq"].(string)
		hash, exists := object["record_hash"]
		_, counterErr := graph.ParseCounter(seq)
		text, textOK := hash.(string)
		if !idOK || storeID == "" || !seqOK || counterErr != nil || !exists ||
			hash != nil && (!textOK || !strings.HasPrefix(text, "sha256:") || len(text) != 71) {
			return report(&server.JournalError{Diagnostic: "schema_invalid", Message: "Typed anchor required"})
		}
		anchor = &server.StoreAnchor{StoreID: storeID, JournalSeq: seq, RecordHash: hash}
	}
	switch operation {
	case "doctor":
		result, err = ops.Doctor(ctx, *store, anchor)
	case "backup":
		var artifact *server.StoreBackup
		artifact, err = ops.Backup(ctx, *store, anchor)
		if err == nil {
			err = ops.PublishBackup(ctx, *output, artifact, *interop)
			if err == nil {
				result = artifact.Result
			}
		}
	case "restore":
		var raw []byte
		raw, err = readStoreInput(*input, server.DefaultStoreBudget().MaxSnapshotBytes)
		if err == nil {
			result, err = ops.Restore(ctx, *store, raw)
		}
	}
	return report(err)
}

func readStoreInput(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, &server.JournalError{Diagnostic: "limit_exceeded", Message: "Offline input budget exceeded"}
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("offline input must be a regular file")
	}
	return raw, nil
}
