package graph

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"time"
)

type ReleaseRequest struct{ data FrozenJSON }

var releaseTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z$`)

func ParseReleaseRequest(value any) (out ReleaseRequest, err error) {
	defer catchModel(&err)
	if raw, ok := value.([]byte); ok {
		value, err = DecodeJSON(raw)
		if err != nil {
			return out, err
		}
	}
	m := obj(mustFreeze(value).JSON(), "schema graph_id target_id revision definition_digest manifest_digest expected_desired_epoch idempotency_key policy reason", "approval_ref")
	if txt(m["schema"], false) != "contexture.release-request/v1" {
		failModel("Invalid release request schema")
	}
	for _, key := range []string{"graph_id", "target_id", "idempotency_key", "approval_ref"} {
		if v, ok := m[key]; ok {
			ident(v)
		}
	}
	releaseCounter(m["revision"], true)
	releaseCounter(m["expected_desired_epoch"], false)
	releaseDigest(m["definition_digest"])
	releaseDigest(m["manifest_digest"])
	enum(m["policy"], "exact latest")
	txt(m["reason"], false)
	return ReleaseRequest{mustFreeze(m)}, nil
}
func (r ReleaseRequest) AsJSON() any { return r.data.JSON() }
func (r ReleaseRequest) field(key string) string {
	m, ok := r.data.value.(map[string]any)
	if !ok {
		return ""
	}
	v, _ := m[key].(string)
	return v
}
func (r ReleaseRequest) GraphID() string              { return r.field("graph_id") }
func (r ReleaseRequest) TargetID() string             { return r.field("target_id") }
func (r ReleaseRequest) Revision() string             { return r.field("revision") }
func (r ReleaseRequest) DefinitionDigest() string     { return r.field("definition_digest") }
func (r ReleaseRequest) ManifestDigest() string       { return r.field("manifest_digest") }
func (r ReleaseRequest) ExpectedDesiredEpoch() string { return r.field("expected_desired_epoch") }
func (r ReleaseRequest) IdempotencyKey() string       { return r.field("idempotency_key") }
func (r ReleaseRequest) ApprovalRef() string          { return r.field("approval_ref") }
func (r ReleaseRequest) Subject() FrozenJSON {
	m, ok := r.data.JSON().(map[string]any)
	if !ok {
		return FrozenJSON{}
	}
	delete(m, "idempotency_key")
	delete(m, "approval_ref")
	return mustFreeze(m)
}
func (r ReleaseRequest) Digest() (string, error) {
	if r.GraphID() == "" {
		return "", invalid("Invalid release request")
	}
	return CanonicalDigest(r.Subject().JSON(), "contexture.command/v1")
}
func releaseCounter(value any, positive bool) string {
	text := txt(value, false)
	n, err := ParseCounter(text)
	if err != nil {
		panic(err)
	}
	if positive && n.Sign() == 0 {
		failModel("Positive release counter required")
	}
	return text
}
func releaseDigest(value any) string {
	text := txt(value, false)
	if !validDigest(text) {
		failModel("Invalid release digest")
	}
	return text
}
func releaseUUID(value any) string {
	text := txt(value, false)
	if !validGeneration(text) {
		failModel("Invalid release UUID")
	}
	return text
}
func releaseIdentity(value any) map[string]any {
	m := obj(value, "graph_id target_id release_epoch", "")
	ident(m["graph_id"])
	ident(m["target_id"])
	releaseCounter(m["release_epoch"], true)
	return m
}
func releaseAttempt(value any) map[string]any {
	m := obj(value, "release instance_id boot_id attempt_id", "")
	releaseIdentity(m["release"])
	ident(m["instance_id"])
	ident(m["attempt_id"])
	releaseUUID(m["boot_id"])
	return m
}
func ParseReleaseReceipt(value any) (out FrozenJSON, err error) {
	defer catchModel(&err)
	m := obj(mustFreeze(value).JSON(), "schema release journal_seq request_digest status", "superseded_by")
	id := releaseIdentity(m["release"])
	if txt(m["schema"], false) != "contexture.release-receipt/v1" {
		failModel("Invalid release receipt schema")
	}
	status := enum(m["status"], "requested superseded")
	releaseCounter(m["journal_seq"], true)
	releaseDigest(m["request_digest"])
	superseded, has := m["superseded_by"]
	if has != (status == "superseded") {
		failModel("Invalid release receipt supersession")
	}
	if has && pureCounter(releaseCounter(superseded, true)).Cmp(pureCounter(id["release_epoch"])) <= 0 {
		failModel("Superseding epoch must advance")
	}
	return mustFreeze(m), nil
}
func releaseObservation(value any) map[string]any {
	m := obj(value, "release instance_id boot_id generation_id revision definition_digest manifest_digest observed_at", "")
	releaseIdentity(m["release"])
	ident(m["instance_id"])
	releaseUUID(m["boot_id"])
	releaseUUID(m["generation_id"])
	releaseCounter(m["revision"], true)
	releaseDigest(m["definition_digest"])
	releaseDigest(m["manifest_digest"])
	text := txt(m["observed_at"], false)
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || !releaseTimestamp.MatchString(text) || t.Year() < 1 {
		failModel("Invalid observation timestamp")
	}
	return m
}
func releaseDiagnostics(value any) {
	a := arr(value)
	if len(a) == 0 {
		failModel("Preparation failure requires diagnostics")
	}
	for _, item := range a {
		m := obj(item, "code location message", "node_id publication_id")
		txt(m["code"], false)
		txt(m["message"], true)
		location := txt(m["location"], true)
		if location != "" && location[0] != '/' {
			failModel("Invalid diagnostic pointer")
		}
		for i := 0; i < len(location); i++ {
			if location[i] == '~' {
				if i+1 == len(location) || location[i+1] != '0' && location[i+1] != '1' {
					failModel("Invalid diagnostic pointer escaping")
				}
				i++
			}
		}
		for _, key := range []string{"node_id", "publication_id"} {
			if id, ok := m[key]; ok {
				ident(id)
			}
		}
	}
}
func releaseTerminal(value any) map[string]any {
	m := obj(value, "identity status", "generation_id diagnostics")
	releaseAttempt(m["identity"])
	switch enum(m["status"], "ready failed") {
	case "ready":
		obj(m, "identity status generation_id", "")
		releaseUUID(m["generation_id"])
	case "failed":
		obj(m, "identity status diagnostics", "")
		releaseDiagnostics(m["diagnostics"])
	}
	return m
}
func releaseKey(value any) [3]string {
	m := releaseIdentity(value)
	return [3]string{txt(m["graph_id"], false), txt(m["target_id"], false), txt(m["release_epoch"], false)}
}
func releaseTarget(value any) [2]string {
	key := releaseKey(value)
	return [2]string{key[0], key[1]}
}
func requestIdentity(r ReleaseRequest, epoch string) map[string]any {
	return map[string]any{"graph_id": r.GraphID(), "target_id": r.TargetID(), "release_epoch": epoch}
}
func mustReleaseRequest(value any) ReleaseRequest {
	r, err := ParseReleaseRequest(value)
	if err != nil {
		panic(err)
	}
	return r
}
func mustReleaseReceipt(value any) map[string]any {
	r, err := ParseReleaseReceipt(value)
	if err != nil {
		panic(err)
	}
	return jsonObject(r.JSON())
}
func releaseMatches(desired map[string]any, id map[string]any, value map[string]any) {
	if desired == nil || desired["release_epoch"] != id["release_epoch"] {
		return
	}
	request := mustReleaseRequest(desired["request"])
	for _, key := range []string{"revision", "definition_digest", "manifest_digest"} {
		if v, ok := value[key]; ok && v != request.field(key) {
			failModelAt("Current release metadata differs", "", "contract_mismatch")
		}
	}
}

// ReleaseState owns pure checkpoint facts, not admission or local serving state.
type ReleaseState struct{ data FrozenJSON }

func EmptyReleaseState() ReleaseState {
	return ReleaseState{mustFreeze(map[string]any{"desired_releases": []any{}, "release_operations": []any{}, "prepare_attempt_history": []any{}, "observations_history": []any{}})}
}
func (s ReleaseState) AsJSON() any { return s.data.JSON() }
func (s ReleaseState) Items(section string) []any {
	m, ok := s.data.JSON().(map[string]any)
	if !ok {
		return nil
	}
	a, _ := m[section].([]any)
	return a
}
func ImportReleaseState(value any) (out ReleaseState, err error) {
	defer catchModel(&err)
	m := obj(mustFreeze(value).JSON(), "desired_releases release_operations prepare_attempt_history observations_history", "")
	operations := map[[3]string]map[string]any{}
	epochs := map[[2]string]*big.Int{}
	superseded := map[[3]string]bool{}
	previousSeq := big.NewInt(0)
	for _, value := range arr(m["release_operations"]) {
		op := mustReleaseReceipt(value)
		id := releaseIdentity(op["release"])
		key, target := releaseKey(id), releaseTarget(id)
		seq := pureCounter(op["journal_seq"])
		if seq.Cmp(previousSeq) < 0 {
			failModel("Release operation sequences regressed")
		}
		previousSeq = seq
		epoch := epochs[target]
		if epoch == nil {
			epoch = big.NewInt(0)
		}
		if op["status"] == "requested" {
			if operations[key] != nil || pureCounter(id["release_epoch"]).Cmp(new(big.Int).Add(epoch, big.NewInt(1))) != 0 {
				failModelAt("Release epoch sequence differs", "", "revision_conflict")
			}
			operations[key] = op
			epochs[target] = pureCounter(id["release_epoch"])
		} else {
			if operations[key] == nil || superseded[key] || op["request_digest"] != operations[key]["request_digest"] ||
				pureCounter(op["superseded_by"]).Cmp(new(big.Int).Add(epoch, big.NewInt(1))) > 0 {
				failModel("Invalid superseded release lineage")
			}
			superseded[key] = true
		}
	}
	desired := map[[2]string]map[string]any{}
	for _, value := range arr(m["desired_releases"]) {
		item := obj(value, "request release_epoch", "")
		r := mustReleaseRequest(item["request"])
		id := requestIdentity(r, releaseCounter(item["release_epoch"], true))
		key, target := releaseKey(id), releaseTarget(id)
		digest, _ := r.Digest()
		if desired[target] != nil || epochs[target] == nil || pureCounter(id["release_epoch"]).Cmp(epochs[target]) != 0 ||
			pureCounter(id["release_epoch"]).Cmp(new(big.Int).Add(pureCounter(r.ExpectedDesiredEpoch()), big.NewInt(1))) != 0 ||
			operations[key] == nil || operations[key]["request_digest"] != digest {
			failModel("Desired release has no matching accepted operation")
		}
		desired[target] = item
	}
	if len(desired) != len(epochs) {
		failModel("Desired release targets incomplete")
	}
	attempts := map[string]bool{}
	for _, value := range arr(m["prepare_attempt_history"]) {
		item := releaseTerminal(value)
		attempt := releaseAttempt(item["identity"])
		raw, _ := CanonicalJSON(attempt)
		if attempts[string(raw)] || operations[releaseKey(attempt["release"])] == nil {
			failModel("Preparation terminal provenance differs")
		}
		attempts[string(raw)] = true
	}
	for _, value := range arr(m["observations_history"]) {
		item := releaseObservation(value)
		id := releaseIdentity(item["release"])
		if operations[releaseKey(id)] == nil {
			failModel("Observation release provenance absent")
		}
		releaseMatches(desired[releaseTarget(id)], id, item)
	}
	items := arr(m["desired_releases"])
	slices.SortFunc(items, func(a, b any) int {
		x, y := mustReleaseRequest(jsonObject(a)["request"]), mustReleaseRequest(jsonObject(b)["request"])
		if x.GraphID() < y.GraphID() || x.GraphID() == y.GraphID() && x.TargetID() < y.TargetID() {
			return -1
		}
		if x.GraphID() == y.GraphID() && x.TargetID() == y.TargetID() {
			return 0
		}
		return 1
	})
	return ReleaseState{mustFreeze(m)}, nil
}
func releaseRecordProof(record map[string]any) {
	releaseCounter(record["journal_seq"], true)
	ident(record["actor_ref"])
	if previous, ok := record["prev_record_hash"]; ok && previous != nil {
		releaseDigest(previous)
	}
	if hash, ok := record["record_hash"]; ok {
		releaseDigest(hash)
		releaseDigest(record["payload_digest"])
		payload := map[string]any{"events": record["events"]}
		if receipt, ok := record["command_receipt"]; ok {
			payload["command_receipt"] = receipt
		}
		subject := jsonObject(mustFreeze(record).JSON())
		delete(subject, "record_hash")
		if pureDigest(payload, "contexture.journal-payload/v1") != record["payload_digest"] ||
			pureDigest(subject, "contexture.journal-record/v1") != hash {
			failModel("Release journal record integrity differs")
		}
	}
}
func ReduceReleaseEvent(state ReleaseState, eventValue, recordValue any) (out ReleaseState, err error) {
	defer catchModel(&err)
	current, err := ImportReleaseState(state.AsJSON())
	if err != nil {
		return out, err
	}
	event := jsonObject(mustFreeze(eventValue).JSON())
	tag := txt(event["type"], false)
	fields := map[string]string{"ReleaseRequested": "type request release_epoch", "ReleaseSuperseded": "type release superseded_by",
		"ReleasePreparationSucceeded": "type attempt generation_id manifest_digest", "ReleasePreparationFailed": "type attempt diagnostics", "ReplicaObserved": "type observation"}
	required, ok := fields[tag]
	if !ok {
		failModel("Unsupported release event")
	}
	obj(event, required, "")
	record := jsonObject(mustFreeze(recordValue).JSON())
	releaseRecordProof(record)
	if values, ok := record["events"]; ok {
		found := false
		for _, value := range arr(values) {
			found = found || jsonEqual(value, event)
		}
		if !found {
			failModel("Release event is not in its journal record")
		}
	}
	m := jsonObject(current.AsJSON())
	desired, operations := arr(m["desired_releases"]), arr(m["release_operations"])
	attempts, observations := arr(m["prepare_attempt_history"]), arr(m["observations_history"])
	seq, actor := record["journal_seq"], record["actor_ref"]
	if len(operations) > 0 && pureCounter(seq).Cmp(pureCounter(jsonObject(operations[len(operations)-1])["journal_seq"])) < 0 {
		failModel("Release fact sequence regressed")
	}
	if tag == "ReleaseRequested" {
		request := mustReleaseRequest(event["request"])
		epoch := releaseCounter(event["release_epoch"], true)
		prior := -1
		expected := "0"
		for i, item := range desired {
			r := mustReleaseRequest(jsonObject(item)["request"])
			if r.GraphID() == request.GraphID() && r.TargetID() == request.TargetID() {
				prior, expected = i, txt(jsonObject(item)["release_epoch"], false)
			}
		}
		if request.ExpectedDesiredEpoch() != expected || pureCounter(epoch).Cmp(new(big.Int).Add(pureCounter(expected), big.NewInt(1))) != 0 {
			failModelAt("Desired release epoch differs", "", "revision_conflict")
		}
		digest, _ := request.Digest()
		receipt := mustReleaseReceipt(map[string]any{"schema": "contexture.release-receipt/v1", "release": requestIdentity(request, epoch), "journal_seq": seq, "request_digest": digest, "status": "requested"})
		if value, ok := record["command_receipt"]; ok {
			c := obj(value, "schema graph_id actor_ref idempotency_key request_digest revision journal_seq status durability operation_result", "")
			inline := obj(c["operation_result"], "inline", "")
			releaseCounter(c["revision"], false)
			if c["schema"] != "contexture.command-receipt/v1" || c["status"] != "committed" ||
				c["actor_ref"] != actor || c["graph_id"] != request.GraphID() || c["idempotency_key"] != request.IdempotencyKey() ||
				c["request_digest"] != digest || c["journal_seq"] != seq || !jsonEqual(mustReleaseReceipt(inline["inline"]), receipt) {
				failModel("Release receipt provenance differs")
			}
			enum(c["durability"], "strict process_crash")
		}
		if prior >= 0 {
			desired = append(desired[:prior], desired[prior+1:]...)
		}
		desired = append(desired, map[string]any{"request": request.AsJSON(), "release_epoch": epoch})
		operations = append(operations, receipt)
	} else {
		var id map[string]any
		switch tag {
		case "ReleaseSuperseded":
			id = releaseIdentity(event["release"])
		case "ReplicaObserved":
			id = releaseIdentity(releaseObservation(event["observation"])["release"])
		default:
			id = releaseIdentity(releaseAttempt(event["attempt"])["release"])
		}
		var prior, target map[string]any
		for _, value := range operations {
			op := jsonObject(value)
			if releaseKey(op["release"]) == releaseKey(id) && op["status"] == "requested" {
				prior = op
			}
		}
		if prior == nil {
			failModel("Release fact has no accepted requested operation")
		}
		for _, value := range desired {
			item := jsonObject(value)
			r := mustReleaseRequest(item["request"])
			if r.GraphID() == txt(id["graph_id"], false) && r.TargetID() == txt(id["target_id"], false) {
				target = item
			}
		}
		switch tag {
		case "ReleaseSuperseded":
			for _, value := range operations {
				op := jsonObject(value)
				if releaseKey(op["release"]) == releaseKey(id) && op["status"] == "superseded" {
					failModel("Release already superseded")
				}
			}
			operations = append(operations, mustReleaseReceipt(map[string]any{"schema": "contexture.release-receipt/v1", "release": id, "journal_seq": seq,
				"request_digest": prior["request_digest"], "status": "superseded", "superseded_by": event["superseded_by"]}))
		case "ReplicaObserved":
			item := releaseObservation(event["observation"])
			releaseMatches(target, id, item)
			observations = append(observations, item)
		default:
			attempt := releaseAttempt(event["attempt"])
			for _, value := range attempts {
				if jsonEqual(jsonObject(value)["identity"], attempt) {
					failModel("Attempt already has a terminal fact")
				}
			}
			item := map[string]any{"identity": attempt}
			if tag == "ReleasePreparationSucceeded" {
				releaseDigest(event["manifest_digest"])
				releaseMatches(target, id, event)
				item["status"], item["generation_id"] = "ready", releaseUUID(event["generation_id"])
			} else {
				releaseDiagnostics(event["diagnostics"])
				item["status"], item["diagnostics"] = "failed", event["diagnostics"]
			}
			attempts = append(attempts, releaseTerminal(item))
		}
	}
	m["desired_releases"], m["release_operations"] = desired, operations
	m["prepare_attempt_history"], m["observations_history"] = attempts, observations
	return ImportReleaseState(m)
}
func ReplayReleases(records []map[string]any) (state ReleaseState, err error) {
	defer catchModel(&err)
	state = EmptyReleaseState()
	var previous any
	for i, raw := range records {
		record := jsonObject(mustFreeze(raw).JSON())
		releaseRecordProof(record)
		releaseDigest(record["record_hash"])
		if record["journal_seq"] != fmt.Sprint(i+1) || record["prev_record_hash"] != previous {
			failModel("Release replay journal prefix differs")
		}
		previous = record["record_hash"]
		for _, raw := range arr(record["events"]) {
			event := jsonObject(raw)
			tag, _ := event["type"].(string)
			if slices.Contains([]string{"ReleaseRequested", "ReleaseSuperseded", "ReleasePreparationSucceeded", "ReleasePreparationFailed", "ReplicaObserved"}, tag) {
				state, err = ReduceReleaseEvent(state, event, record)
				if err != nil {
					return ReleaseState{}, err
				}
			}
		}
	}
	return state, nil
}
