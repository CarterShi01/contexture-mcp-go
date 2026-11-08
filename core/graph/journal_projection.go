package graph

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/benbjohnson/immutable"
)

type GraphJournalProjectionOptions struct {
	PublicationContracts map[string]PublicationContract
}

// GraphJournalProjection is a pure complete accepted cut, never a second store
// or authorization source. All getters return immutable handles or copies.
type GraphJournalProjection struct {
	records        *immutable.List[FrozenJSON]
	histories      map[string]History
	checkpoint     FrozenJSON
	checkpointOnce sync.Once
	storeID        string
	options        GraphJournalProjectionOptions
	receipts       *immutable.SortedMap[[2]string, FrozenJSON]
	refs           *immutable.SortedMap[[2]string, FrozenJSON]
	batches        *immutable.SortedMap[string, bool]
	releases       ReleaseState
	verified       bool
}

type journalPairComparer struct{}

func (journalPairComparer) Compare(a, b [2]string) int {
	if order := strings.Compare(a[0], b[0]); order != 0 {
		return order
	}
	return strings.Compare(a[1], b[1])
}

func ValidateTransactionLimits(limits TransactionLimits) (err error) {
	defer catchModel(&err)
	limits.validate()
	return nil
}
func projectionBlob(value any) map[string]any {
	ref := obj(value, "digest size media_type", "")
	releaseDigest(ref["digest"])
	releaseCounter(ref["size"], false)
	txt(ref["media_type"], false)
	return ref
}
func projectionTombstones(history History, event map[string]any) []any {
	items := map[string]any{}
	for _, value := range arr(history.Head().Tombstones()) {
		item := jsonObject(value)
		items[ident(item["node_id"])] = item
	}
	if facts, ok := event["resolved_facts"]; ok {
		for _, raw := range arr(facts) {
			fact := jsonObject(raw)
			switch fact["type"] {
			case "NodeDeleted":
				item := jsonObject(fact["tombstone"])
				items[ident(item["node_id"])] = item
			case "CreationPolicyMigrated":
				id := ident(fact["node_id"])
				if value, ok := items[id]; ok {
					item := jsonObject(value)
					item["mutable"] = fact["after"]
					items[id] = item
				}
			}
		}
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, items[id])
	}
	return out
}

func NewGraphJournalProjection(records []map[string]any, options GraphJournalProjectionOptions) (out *GraphJournalProjection, err error) {
	return extendGraphJournalProjection(records, options, nil)
}

func (p *GraphJournalProjection) Extend(suffix []map[string]any) (*GraphJournalProjection, error) {
	if p == nil || !p.verified {
		return nil, diagnostic("journal_corrupt", "Verified accepted prefix required", "")
	}
	return extendGraphJournalProjection(suffix, p.options, p)
}

func extendGraphJournalProjection(records []map[string]any, options GraphJournalProjectionOptions, prefix *GraphJournalProjection) (out *GraphJournalProjection, err error) {
	defer catchModel(&err)
	projection := &GraphJournalProjection{histories: map[string]History{}, records: immutable.NewList[FrozenJSON](), releases: EmptyReleaseState()}
	receipts := immutable.NewSortedMap[[2]string, FrozenJSON](journalPairComparer{})
	refs := immutable.NewSortedMap[[2]string, FrozenJSON](journalPairComparer{})
	batches := immutable.NewSortedMap[string, bool](nil)
	var previous any
	offset := 0
	if prefix != nil {
		projection.histories = maps.Clone(prefix.histories)
		projection.records = prefix.records
		projection.storeID, projection.releases = prefix.storeID, prefix.releases
		receipts, refs, batches = prefix.receipts, prefix.refs, prefix.batches
		offset = prefix.records.Len()
		if offset > 0 {
			previous = jsonObject(prefix.records.Get(offset - 1).JSON())["record_hash"]
		}
	}
	options.PublicationContracts = maps.Clone(options.PublicationContracts)
	projection.options = options
	addRef := func(value any) {
		ref := projectionBlob(value)
		key := [2]string{txt(ref["digest"], false), txt(ref["media_type"], false)}
		if prior, exists := refs.Get(key); exists && !jsonEqual(prior.JSON(), ref) {
			failModel("Conflicting required blob evidence")
		}
		refs = refs.Set(key, mustFreeze(ref))
	}
	for ordinal, raw := range records {
		record := obj(mustFreeze(raw).JSON(), "schema store_id journal_seq event_batch_id prev_record_hash events written_at actor_ref correlation_id payload_digest record_hash", "command_receipt")
		ident(record["actor_ref"])
		ident(record["correlation_id"])
		batch := ident(record["event_batch_id"])
		if _, exists := batches.Get(batch); exists {
			failModel("Duplicate Journal event batch identity")
		}
		batches = batches.Set(batch, true)
		written := txt(record["written_at"], false)
		at, timeErr := time.Parse(time.RFC3339Nano, written)
		if timeErr != nil || at.Year() < 1 || !releaseTimestamp.MatchString(written) {
			failModel("Invalid Journal UTC timestamp")
		}
		releaseRecordProof(record)
		releaseDigest(record["record_hash"])
		if record["schema"] != "contexture.journal-record/v1" || record["journal_seq"] != fmt.Sprint(offset+ordinal+1) || record["prev_record_hash"] != previous {
			failModel("Accepted Journal sequence/hash prefix differs")
		}
		storeID := ident(record["store_id"])
		if projection.storeID != "" && projection.storeID != storeID {
			failModel("Journal physical origin changed")
		}
		projection.storeID = storeID
		previous = record["record_hash"]
		events := arr(record["events"])
		if len(events) == 0 {
			failModel("Complete event batch required")
		}
		graphEvents := map[string]bool{}
		var lastGraphEvent map[string]any
		for _, value := range events {
			event := jsonObject(value)
			switch event["type"] {
			case "GraphChangeCommitted", "CommandCompleted":
				lastGraphEvent = event
				graphID := ident(event["graph_id"])
				if graphEvents[graphID] {
					pureFail("unsupported_contract", "Existing History requires one graph event per graph per Journal record")
				}
				graphEvents[graphID] = true
				history, exists := projection.histories[graphID]
				if !exists {
					unseeded, e := NewUnseededState(graphID)
					if e != nil {
						return nil, e
					}
					history, err = BootstrapHistory(unseeded, event, txt(record["journal_seq"], false), options.PublicationContracts)
				} else {
					history, err = history.Append(event, txt(record["journal_seq"], false), ApplyEventOptions{
						Tombstones: projectionTombstones(history, event), PublicationContracts: options.PublicationContracts})
				}
				if err != nil {
					return nil, err
				}
				projection.histories[graphID] = history
			case "RecoveryActionRecorded":
				obj(event, "type action_id action evidence", "")
				ident(event["action_id"])
				txt(event["action"], false)
				addRef(event["evidence"])
			case "ReleaseRequested", "ReleaseSuperseded", "ReleasePreparationSucceeded", "ReleasePreparationFailed", "ReplicaObserved":
				projection.releases, err = ReduceReleaseEvent(projection.releases, event, record)
				if err != nil {
					return nil, err
				}
			default:
				pureFail("unsupported_contract", "Unregistered required Journal event")
			}
		}
		if value, ok := record["command_receipt"]; ok {
			receipt := obj(value, "schema graph_id actor_ref idempotency_key request_digest revision journal_seq status durability operation_result", "")
			key := [2]string{ident(receipt["actor_ref"]), ident(receipt["idempotency_key"])}
			_, duplicate := receipts.Get(key)
			if receipt["schema"] != "contexture.command-receipt/v1" || receipt["actor_ref"] != record["actor_ref"] ||
				receipt["journal_seq"] != record["journal_seq"] || duplicate {
				failModel("Receipt actor/key/accepted position differs")
			}
			ident(receipt["graph_id"])
			releaseDigest(receipt["request_digest"])
			releaseCounter(receipt["revision"], false)
			enum(receipt["status"], "committed noop")
			enum(receipt["durability"], "strict process_crash")
			if lastGraphEvent != nil {
				revision := lastGraphEvent["revision"]
				if value, ok := lastGraphEvent["result_revision"]; ok {
					revision = value
				}
				status := "committed"
				if lastGraphEvent["type"] == "CommandCompleted" {
					status = "noop"
				}
				if receipt["graph_id"] != lastGraphEvent["graph_id"] || receipt["revision"] != revision ||
					receipt["request_digest"] != lastGraphEvent["command_digest"] || receipt["status"] != status {
					failModel("Receipt differs from accepted graph event")
				}
			}
			result := obj(receipt["operation_result"], "", "inline blob")
			if len(result) != 1 {
				failModel("Exactly one operation result carrier required")
			}
			if ref, ok := result["blob"]; ok {
				addRef(ref)
			}
			receipts = receipts.Set(key, mustFreeze(receipt))
		}
		projection.records = projection.records.Append(mustFreeze(record))
	}
	projection.receipts, projection.refs, projection.batches = receipts, refs, batches
	projection.verified = true
	return projection, nil
}

func (p *GraphJournalProjection) buildCheckpoint() FrozenJSON {
	checkpoint := jsonObject(p.releases.AsJSON())
	graphIDs := p.GraphIDs()
	graphs := []any{}
	for _, id := range graphIDs {
		head := p.histories[id].Head()
		definition := head.Definition()
		digest, _ := head.DefinitionDigest()
		graphs = append(graphs, map[string]any{"definition": definition.AsJSON(), "head": head.Revision(),
			"definition_digest": digest, "tombstones": head.Tombstones()})
	}
	orderedReceipts, orderedRefs := []any{}, []any{}
	for iterator := p.receipts.Iterator(); !iterator.Done(); {
		_, receipt, _ := iterator.Next()
		orderedReceipts = append(orderedReceipts, receipt.JSON())
	}
	for iterator := p.refs.Iterator(); !iterator.Done(); {
		_, ref, _ := iterator.Next()
		orderedRefs = append(orderedRefs, ref.JSON())
	}
	checkpoint["graphs"], checkpoint["receipts_index"] = graphs, orderedReceipts
	checkpoint["projection_versions"], checkpoint["blob_refs"] = map[string]any{}, orderedRefs
	return mustFreeze(checkpoint)
}
func (p *GraphJournalProjection) StoreID() string   { return p.storeID }
func (p *GraphJournalProjection) Watermark() string { return fmt.Sprint(p.records.Len()) }
func (p *GraphJournalProjection) Checkpoint() FrozenJSON {
	p.checkpointOnce.Do(func() { p.checkpoint = p.buildCheckpoint() })
	return p.checkpoint
}
func (p *GraphJournalProjection) Records() []map[string]any {
	out := make([]map[string]any, 0, p.records.Len())
	for iterator := p.records.Iterator(); !iterator.Done(); {
		_, record := iterator.Next()
		out = append(out, jsonObject(record.JSON()))
	}
	return out
}
func (p *GraphJournalProjection) GraphIDs() []string {
	out := make([]string, 0, len(p.histories))
	for id := range p.histories {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
func (p *GraphJournalProjection) History(graphID string) (History, error) {
	if _, err := ValidateID(graphID); err != nil {
		return History{}, err
	}
	history, ok := p.histories[graphID]
	if !ok {
		return History{}, diagnostic("history_unavailable", "Graph has no accepted definition", "")
	}
	return history, nil
}
func ProjectGraphJournal(records []map[string]any, options GraphJournalProjectionOptions) (FrozenJSON, error) {
	projection, err := NewGraphJournalProjection(records, options)
	if err != nil {
		return FrozenJSON{}, err
	}
	return projection.Checkpoint(), nil
}
