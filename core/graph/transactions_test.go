package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type pureMap = map[string]any

func txRole(id string, extra pureMap) pureMap {
	n := pureMap{"node_id": id, "kind": "role", "name": id, "description": "node", "instructions": ""}
	for k, v := range extra {
		n[k] = v
	}
	return n
}
func txGraph(t *testing.T, nodes []any, extra pureMap) *GraphDefinition {
	t.Helper()
	if nodes == nil {
		nodes = []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "right"}}}), txRole("left", nil), txRole("right", nil)}
	}
	wire := pureMap{"schema": "contexture.graph/v1", "semantic_version": "1", "graph_id": "native", "application_name": "native-app", "mode": "disclosure_only", "nodes": nodes, "roots": []any{pureMap{"node_id": "hub", "plane": "model"}}, "presentations": pureMap{"default_locale": "en", "catalogs": []any{}}}
	for k, v := range extra {
		wire[k] = v
	}
	g, err := NewGraphDefinition(wire, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func txState(t *testing.T, g *GraphDefinition, revision string, history any) PureRevisionState {
	t.Helper()
	if g == nil {
		g = txGraph(t, nil, nil)
	}
	s, err := NewPureRevisionState(g, revision, history)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func txRequest(commands ...any) pureMap {
	return pureMap{"schema": "contexture.transaction/v1", "graph_id": "native", "base_revision": "12", "idempotency_key": "attempt", "correlation_id": "trace", "reason": "independent native test", "commands": commands}
}
func txRename(id, name string) pureMap {
	return pureMap{"op": "rename_node", "node_id": id, "new_name": name}
}
func txMember(parent, slot string, position int) pureMap {
	return pureMap{"parent_id": parent, "slot": slot, "position": position}
}
func txMove(id string, target pureMap) pureMap {
	return pureMap{"op": "move_node", "node_id": id, "target": target}
}
func txPrepare(t *testing.T, s PureRevisionState, commands ...any) PurePreparedTransaction {
	t.Helper()
	p, err := Prepare(s, txRequest(commands...), PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func txCode(t *testing.T, err error, code string) {
	t.Helper()
	var g *GraphError
	if !errors.As(err, &g) || g.Code() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func txReject(t *testing.T, s PureRevisionState, code string, commands ...any) {
	t.Helper()
	before, _ := s.definition.Digest()
	_, err := Prepare(s, txRequest(commands...), PrepareOptions{})
	txCode(t, err, code)
	after, _ := s.definition.Digest()
	if before != after {
		t.Fatal("rejection mutated base")
	}
}
func txEqual(t *testing.T, a, b any) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %#v; want %#v", a, b)
	}
}

// Test-only independent ASCII/integer JCS oracle: encoding/json sorts map keys.
// No golden bytes or production hash/canonical helper participates in hashing.
func txHash(t *testing.T, value any, domain string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(append([]byte(domain+"\x00"), b...))
	return "sha256:" + hex.EncodeToString(h[:])
}

func TestPureTransactionFactsAndExactDomains(t *testing.T) {
	s := txState(t, nil, "12", nil)
	request := txRequest(txRename("left", "renamed"))
	p, err := Prepare(s, request, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	txEqual(t, p.Facts(), []any{pureMap{"type": "NodeFieldsSet", "node_id": "left", "before": pureMap{"name": pureMap{"present": true, "value": "left"}}, "after": pureMap{"name": pureMap{"present": true, "value": "renamed"}}}})
	txEqual(t, p.Protection().Impact(), []string{"hub", "left"})
	txEqual(t, p.Protection().ReferenceTargets(), []string{})
	commandSubject := pureMap{}
	for _, k := range []string{"schema", "graph_id", "base_revision", "commands", "reason"} {
		commandSubject[k] = request[k]
	}
	cmd, _ := p.Envelope().CommandDigest()
	txEqual(t, cmd, txHash(t, commandSubject, "contexture.command/v1"))
	txEqual(t, p.FactsDigest(), txHash(t, p.Facts(), "contexture.facts/v1"))
	baseDigest, _ := s.DefinitionDigest()
	candidate := p.Candidate()
	candidateDigest, _ := candidate.Digest()
	subject := pureMap{"schema": "contexture.preview/v1", "graph_id": "native", "base_revision": "12", "base_digest": baseDigest, "candidate_digest": candidateDigest, "command_digest": cmd, "resolved_fact_digest": p.FactsDigest()}
	txEqual(t, p.ApprovalSubject(), subject)
	txEqual(t, p.ApprovalSubjectDigest(), txHash(t, subject, "contexture.preview/v1"))
	txEqual(t, p.ResultRevision(), "13")
	txEqual(t, p.Status(), "prepared-pure-candidate")
	n, _ := s.definition.Node("left")
	txEqual(t, n.Name(), "left")
}

func TestPureTransactionElevenOrderedCommands(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s,
		pureMap{"op": "create_node", "node": txRole("third", nil)},
		pureMap{"op": "update_node", "node_id": "third", "set_fields": pureMap{"description": "changed"}, "unset_fields": []any{}},
		txRename("third", "third-name"), pureMap{"op": "attach_node", "node_id": "third", "target": txMember("hub", "children", 2)},
		txMove("third", txMember("hub", "children", 0)), pureMap{"op": "detach_node", "node_id": "third"},
		pureMap{"op": "add_root", "node_id": "third", "plane": "prompt", "position": 1}, pureMap{"op": "remove_root", "node_id": "third"},
		pureMap{"op": "attach_node", "node_id": "third", "target": txMember("hub", "children", 2)},
		pureMap{"op": "reorder_members", "parent_id": "hub", "slot": "children", "node_ids": []any{"right", "third", "left"}},
		pureMap{"op": "add_use", "source_id": "third", "target_id": "left"}, pureMap{"op": "remove_use", "source_id": "third", "target_id": "left"})
	n := p.candidate.nodes["hub"]
	txEqual(t, n.members.children, []string{"right", "third", "left"})
	txEqual(t, p.candidate.nodes["third"].description, "changed")
	facts := p.Facts().([]any)
	txEqual(t, len(facts), 2)
	txEqual(t, facts[0].(pureMap)["type"], "NodeCreated")
	txEqual(t, facts[1].(pureMap)["type"], "MembersAssigned")
	if !p.RootOperations() || p.Protection().RootsChanged() {
		t.Fatal("root intent was confused with endpoint changes")
	}
}

func TestPureTransactionRemoveFirstAndFinalValidation(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s, txMove("left", txMember("hub", "children", 1)))
	txEqual(t, p.candidate.nodes["hub"].members.children, []string{"right", "left"})
	txReject(t, s, "schema_invalid", txMove("left", txMember("hub", "children", 2)))
	p = txPrepare(t, s, txRename("left", "right"), txRename("right", "left"),
		pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"instruction_template": pureMap{"parts": []any{pureMap{"kind": "Text", "text": "temporary"}}}}, "unset_fields": []any{}},
		pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"instruction_template": pureMap{"parts": []any{pureMap{"kind": "Text", "text": "final"}}}}, "unset_fields": []any{"instructions"}})
	txEqual(t, p.candidate.nodes["left"].name, "right")
	txEqual(t, p.candidate.nodes["left"].instructions.parts[0].Text, "final")
}

func TestPureTransactionNoopAndRootIntent(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s, txRename("left", "temp"), txRename("left", "left"), pureMap{"op": "remove_root", "node_id": "hub"}, pureMap{"op": "add_root", "node_id": "hub", "plane": "model", "position": 0})
	if !p.Noop() || !p.RootOperations() || p.Protection().RootsChanged() {
		t.Fatal("incorrect endpoint noop")
	}
	txEqual(t, p.ResultRevision(), "12")
	txEqual(t, p.Facts(), []any{})
	txEqual(t, p.Protection().Impact(), []string{})
	txEqual(t, p.FactsDigest(), txHash(t, []any{}, "contexture.facts/v1"))
	p = txPrepare(t, s, txMove("left", pureMap{"plane": "prompt", "position": 1}))
	if !p.Protection().RootsChanged() {
		t.Fatal("missing plane/root impact")
	}
	txEqual(t, p.candidate.roots[1], RootEntry{"left", PromptPlane})
}

func TestPureTransactionImmutableSchemasAndHandles(t *testing.T) {
	binding := pureMap{"kind": "registered", "binding_id": "native-tool", "version": "latest", "portability": pureMap{"kind": "native", "language": "go", "version": "1", "factory": "inert"}, "config": pureMap{"nested": []any{pureMap{"value": 1}}}}
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"tools": []any{"tool"}}}), pureMap{"node_id": "tool", "kind": "tool", "name": "tool", "description": "tool", "binding": binding}}, pureMap{"mode": "runtime"})
	s := txState(t, g, "12", nil)
	schema := pureMap{"type": "object", "properties": pureMap{"value": pureMap{"type": "string"}}}
	request := txRequest(pureMap{"op": "update_node", "node_id": "tool", "set_fields": pureMap{"input_contract": schema}, "unset_fields": []any{}})
	p, err := Prepare(s, request, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hash := p.ApprovalSubjectDigest()
	schema["properties"].(pureMap)["value"].(pureMap)["type"] = "number"
	request["reason"] = "mutated"
	binding["version"] = "other"
	returned := p.Facts().([]any)
	returned[0].(pureMap)["after"].(pureMap)["input_contract"].(pureMap)["value"].(pureMap)["type"] = "array"
	p.ApprovalSubject().(pureMap)["base_revision"] = "999"
	p.Envelope().AsJSON().(pureMap)["commands"] = []any{}
	copy := p.Candidate()
	copy = GraphDefinition{}
	_ = copy
	*g = GraphDefinition{}
	tool := p.candidate.nodes["tool"]
	contract, _ := tool.InputContract()
	txEqual(t, contract, pureMap{"type": "object", "properties": pureMap{"value": pureMap{"type": "string"}}})
	txEqual(t, tool.binding.version, "latest")
	txEqual(t, p.ApprovalSubjectDigest(), hash)
	if _, err := RecheckPrepared(s, p, PrepareOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestPureTransactionCASAndTamperReconstruction(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s, txRename("left", "new"))
	if _, err := RecheckPrepared(s, p, PrepareOptions{}); err != nil {
		t.Fatal(err)
	}
	states := []PureRevisionState{txState(t, nil, "13", nil), txState(t, txGraph(t, nil, pureMap{"application_name": "other"}), "12", nil), txState(t, nil, "12", []any{pureMap{"node_id": "past", "kind": "skill", "mutable": true, "deleted_revision": "2"}})}
	for _, current := range states {
		_, err := RecheckPrepared(current, p, PrepareOptions{})
		txCode(t, err, "revision_conflict")
	}
	for _, change := range []func(*PurePreparedTransaction){func(x *PurePreparedTransaction) { x.rootOperations = true }, func(x *PurePreparedTransaction) { x.facts = mustFreeze([]any{}) }, func(x *PurePreparedTransaction) { x.approvalSubjectDigest = "sha256:" + strings.Repeat("0", 64) }, func(x *PurePreparedTransaction) { x.resultRevision = "99" }, func(x *PurePreparedTransaction) { x.noop = true }, func(x *PurePreparedTransaction) { x.protection.impact = []string{} }} {
		q := p
		change(&q)
		_, err := RecheckPrepared(s, q, PrepareOptions{})
		txCode(t, err, "revision_conflict")
	}
	for _, key := range []string{"graph_id", "base_revision"} {
		request := txRequest(txRename("left", "new"))
		request[key] = "other"
		if key == "base_revision" {
			request[key] = "11"
		}
		_, err := Prepare(s, request, PrepareOptions{})
		txCode(t, err, "revision_conflict")
	}
}

func TestPureTransactionForwardReferencesAndRetainedIdentity(t *testing.T) {
	s := txState(t, nil, "12", nil)
	for _, commands := range [][]any{
		{pureMap{"op": "add_use", "source_id": "left", "target_id": "new"}, pureMap{"op": "create_node", "node": txRole("new", nil)}},
		{pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"instruction_template": pureMap{"parts": []any{pureMap{"kind": "NodeRef", "target_id": "new"}}}}, "unset_fields": []any{"instructions"}}, pureMap{"op": "create_node", "node": txRole("new", nil)}},
		{pureMap{"op": "create_node", "node": pureMap{"node_id": "new", "kind": "skill", "name": "new", "description": "new", "instruction_template": pureMap{"parts": []any{pureMap{"kind": "NodeRef", "target_id": "later"}}}}}},
	} {
		txReject(t, s, "reference_not_found", commands...)
	}
	txReject(t, s, "identity_conflict", pureMap{"op": "create_node", "node": txRole("left", nil)})
	history := []any{pureMap{"node_id": "retained", "kind": "role", "mutable": "inherit", "deleted_revision": "4"}}
	retained := txState(t, nil, "12", history)
	history[0].(pureMap)["node_id"] = "other"
	txReject(t, retained, "identity_conflict", pureMap{"op": "create_node", "node": txRole("retained", nil)})
	p := txPrepare(t, s, pureMap{"op": "create_node", "node": txRole("target", nil)}, pureMap{"op": "create_node", "node": pureMap{"node_id": "source", "kind": "skill", "name": "source", "description": "source", "instruction_template": pureMap{"parts": []any{pureMap{"kind": "NodeRef", "target_id": "target", "label": ""}}}}}, pureMap{"op": "attach_node", "node_id": "target", "target": txMember("hub", "children", 2)}, pureMap{"op": "attach_node", "node_id": "source", "target": txMember("hub", "skills", 0)})
	txEqual(t, p.Protection().ReferenceTargets(), []string{"target"})
	normalized := p.Envelope().AsJSON().(pureMap)["commands"].([]any)[0].(pureMap)["node"].(pureMap)
	txEqual(t, normalized["mutable"], "inherit")
	txEqual(t, normalized["members"], pureMap{"children": []any{}, "skills": []any{}, "tools": []any{}})
}

func TestPureTransactionAtomicFailures(t *testing.T) {
	s := txState(t, nil, "12", nil)
	cases := map[string][]any{
		"unowned": {pureMap{"op": "detach_node", "node_id": "left"}}, "unattached": {pureMap{"op": "create_node", "node": txRole("new", nil)}},
		"owned attach":  {pureMap{"op": "attach_node", "node_id": "left", "target": txMember("hub", "children", 0)}},
		"double detach": {pureMap{"op": "detach_node", "node_id": "left"}, pureMap{"op": "detach_node", "node_id": "left"}},
		"nonroot":       {pureMap{"op": "remove_root", "node_id": "left"}}, "cycle": {txMove("hub", txMember("left", "children", 0))},
		"slot kind": {txMove("left", txMember("hub", "tools", 0))}, "slot variant": {txMove("left", txMember("hub", "pre_process", 0))},
		"reorder set":    {pureMap{"op": "reorder_members", "parent_id": "hub", "slot": "children", "node_ids": []any{"left"}}},
		"duplicate uses": {pureMap{"op": "add_use", "source_id": "left", "target_id": "right"}, pureMap{"op": "add_use", "source_id": "left", "target_id": "right"}},
		"self use":       {pureMap{"op": "add_use", "source_id": "left", "target_id": "left"}}, "absent use": {pureMap{"op": "remove_use", "source_id": "left", "target_id": "right"}},
		"collision": {txRename("left", "first-write")}, "bad name": {txRename("left", "bad/name")}, "bad update": {pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"mutable": true}, "unset_fields": []any{}}},
		"conflict update": {pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"instructions": ""}, "unset_fields": []any{"instructions"}}},
		"required unset":  {pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{}, "unset_fields": []any{"description"}}},
		"create uses":     {pureMap{"op": "create_node", "node": txRole("new", pureMap{"uses": []any{"left"}})}},
		"create members":  {pureMap{"op": "create_node", "node": txRole("new", pureMap{"members": pureMap{"children": []any{"left"}}})}},
		"root bounds":     {txMove("left", pureMap{"plane": "prompt", "position": 2})}, "uses bounds": {pureMap{"op": "add_use", "source_id": "left", "target_id": "right", "position": 1}},
	}
	for label, commands := range cases {
		t.Run(label, func(t *testing.T) {
			txReject(t, s, "schema_invalid", append([]any{txRename("right", "first-write")}, commands...)...)
		})
	}
	for _, op := range append(UnsupportedCommands(), "unknown", "bootstrap", "migrate") {
		t.Run(op, func(t *testing.T) { txReject(t, s, "unsupported_contract", pureMap{"op": op}) })
	}
}

func TestPureTransactionSingletonAndUsesCycles(t *testing.T) {
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"pre_process": "left", "children": []any{"right"}}}), txRole("left", pureMap{"role_variant": "pre_process"}), txRole("right", pureMap{"role_variant": "pre_process"})}, nil)
	s := txState(t, g, "12", nil)
	txReject(t, s, "schema_invalid", txMove("right", txMember("hub", "pre_process", 0)))
	p := txPrepare(t, s, pureMap{"op": "detach_node", "node_id": "left"}, txMove("right", txMember("hub", "pre_process", 0)), pureMap{"op": "attach_node", "node_id": "left", "target": txMember("hub", "children", 0)}, pureMap{"op": "add_use", "source_id": "left", "target_id": "right"}, pureMap{"op": "add_use", "source_id": "right", "target_id": "left"})
	txEqual(t, p.candidate.nodes["hub"].members.pre, "right")
	txEqual(t, p.Protection().ReferenceTargets(), []string{"left", "right"})
}

func TestPureTransactionLimitsLexicalAndCounter(t *testing.T) {
	s := txState(t, nil, "12", nil)
	request := txRequest(txRename("left", "changed"), txRename("right", "changed-too"))
	for _, change := range []func(*TransactionLimits){func(l *TransactionLimits) { l.MaxCommands = 1 }, func(l *TransactionLimits) { l.MaxTransactionBytes = 8 }, func(l *TransactionLimits) { l.MaxDefinitionBytes = 8 }, func(l *TransactionLimits) { l.MaxResolvedBytes = 1 }, func(l *TransactionLimits) { l.MaxNodes = 1 }, func(l *TransactionLimits) { l.MaxEdges = 1 }, func(l *TransactionLimits) { l.MaxNodeBytes = 1 }} {
		l := DefaultTransactionLimits()
		change(&l)
		_, err := Prepare(s, request, PrepareOptions{Limits: &l})
		txCode(t, err, "limit_exceeded")
	}
	deep := txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left"}}}), txRole("left", pureMap{"members": pureMap{"children": []any{"right"}}}), txRole("right", nil)}, nil), "12", nil)
	l := DefaultTransactionLimits()
	l.MaxDepth = 1
	_, err := Prepare(deep, txRequest(txRename("right", "deep")), PrepareOptions{Limits: &l})
	txCode(t, err, "limit_exceeded")
	for _, edit := range []func(pureMap){func(m pureMap) { m["extra"] = true }, func(m pureMap) { m["base_revision"] = "012" }, func(m pureMap) { m["commands"] = []any{} }, func(m pureMap) {
		m["commands"] = []any{pureMap{"op": "add_use", "source_id": "left", "target_id": "right", "position": 0.5}}
	}} {
		m := txRequest(txRename("left", "new"))
		edit(m)
		if _, err := Prepare(s, m, PrepareOptions{}); err == nil {
			t.Fatal("accepted malformed envelope")
		}
	}
	raw, _ := json.Marshal(txRequest(txRename("left", "new")))
	if _, err := Prepare(s, raw, PrepareOptions{}); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), "\"commands\":", "\"reason\":\"duplicate\",\"commands\":", 1)
	_, err = Prepare(s, []byte(bad), PrepareOptions{})
	txCode(t, err, "schema_invalid")
	raw, _ = json.Marshal(txRequest(pureMap{"op": "add_use", "source_id": "left", "target_id": "right", "position": 1}))
	_, err = Prepare(s, []byte(strings.Replace(string(raw), "\"position\":1", "\"position\":9007199254740993", 1)), PrepareOptions{})
	txCode(t, err, "schema_invalid")
	maxState := txState(t, nil, MaxCounter, nil)
	m := txRequest(txRename("left", "new"))
	m["base_revision"] = MaxCounter
	_, err = Prepare(maxState, m, PrepareOptions{})
	txCode(t, err, "limit_exceeded")
	m["commands"] = []any{txRename("left", "left")}
	p, err := Prepare(maxState, m, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	txEqual(t, p.ResultRevision(), MaxCounter)
}

func TestPureTransactionDigestProvenanceAndScalarOrder(t *testing.T) {
	a := txRequest(txRename("left", "new"))
	b := txRequest(txRename("left", "new"))
	b["idempotency_key"], b["correlation_id"], b["approval_ref"] = "other", "other", "approval"
	x, err := NewTransactionEnvelope(a, DefaultTransactionLimits())
	if err != nil {
		t.Fatal(err)
	}
	y, err := NewTransactionEnvelope(b, DefaultTransactionLimits())
	if err != nil {
		t.Fatal(err)
	}
	xd, _ := x.CommandDigest()
	yd, _ := y.CommandDigest()
	txEqual(t, xd, yd)
	b["reason"] = "different"
	y, err = NewTransactionEnvelope(b, DefaultTransactionLimits())
	if err != nil {
		t.Fatal(err)
	}
	yd, _ = y.CommandDigest()
	if xd == yd {
		t.Fatal("reason excluded from command hash")
	}
	s := txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"𐀀", "\ue000"}}}), txRole("𐀀", nil), txRole("\ue000", nil)}, nil), "12", nil)
	p := txPrepare(t, s, txRename("𐀀", "astral"), txRename("\ue000", "bmp"))
	facts := p.Facts().([]any)
	txEqual(t, facts[0].(pureMap)["node_id"], "\ue000")
	txEqual(t, facts[1].(pureMap)["node_id"], "𐀀")
}

func TestPureTransactionConcurrentReadsAndPrepare(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s, txRename("left", "concurrent"))
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				q, err := RecheckPrepared(s, p, PrepareOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if q.ApprovalSubjectDigest() != p.ApprovalSubjectDigest() {
					t.Error("nondeterministic proposal")
				}
				q.Facts().([]any)[0].(pureMap)["node_id"] = "local-copy"
				q.Protection().Impact()[0] = "local-copy"
			}
		}()
	}
	wg.Wait()
}

func TestPureTransactionOptionalFactsAndOrderedRelations(t *testing.T) {
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s,
		pureMap{"op": "update_node", "node_id": "left", "set_fields": pureMap{"instruction_template": pureMap{"parts": []any{pureMap{"kind": "Text", "text": "program"}}}}, "unset_fields": []any{"instructions"}},
		pureMap{"op": "add_use", "source_id": "left", "target_id": "right"},
		pureMap{"op": "add_use", "source_id": "left", "target_id": "hub", "position": 0},
		pureMap{"op": "add_use", "source_id": "right", "target_id": "left"})
	txEqual(t, p.Facts(), []any{
		pureMap{"type": "NodeFieldsSet", "node_id": "left", "before": pureMap{"instructions": pureMap{"present": true, "value": ""}, "instruction_template": pureMap{"present": false}}, "after": pureMap{"instructions": pureMap{"present": false}, "instruction_template": pureMap{"present": true, "value": pureMap{"parts": []any{pureMap{"kind": "Text", "text": "program"}}}}}},
		pureMap{"type": "UsesAssigned", "node_id": "left", "before": []any{}, "after": []any{"hub", "right"}},
		pureMap{"type": "UsesAssigned", "node_id": "right", "before": []any{}, "after": []any{"left"}},
	})
	txEqual(t, p.Protection().ReferenceTargets(), []string{"hub", "left", "right"})
	p = txPrepare(t, s, txMove("left", pureMap{"plane": "prompt", "position": 1}))
	txEqual(t, p.Facts(), []any{
		pureMap{"type": "RootsAssigned", "before": []any{pureMap{"node_id": "hub", "plane": "model"}}, "after": []any{pureMap{"node_id": "hub", "plane": "model"}, pureMap{"node_id": "left", "plane": "prompt"}}},
		pureMap{"type": "MembersAssigned", "node_id": "hub", "before": pureMap{"children": []any{"left", "right"}, "skills": []any{}, "tools": []any{}}, "after": pureMap{"children": []any{"right"}, "skills": []any{}, "tools": []any{}}},
	})
}

func TestPureTransactionBoundaryValuesFailClosed(t *testing.T) {
	g := txGraph(t, nil, nil)
	for _, history := range []any{
		[]any{pureMap{"node_id": "past", "kind": "role", "mutable": "inherit", "deleted_revision": "0"}},
		[]any{pureMap{"node_id": "past", "kind": "role", "mutable": "inherit", "deleted_revision": "13"}},
		[]any{pureMap{"node_id": "past", "kind": "role", "mutable": "true", "deleted_revision": "2"}},
		[]any{pureMap{"node_id": "past", "kind": "role", "mutable": true, "deleted_revision": "2"}, pureMap{"node_id": "past", "kind": "role", "mutable": true, "deleted_revision": "2"}},
	} {
		_, err := NewPureRevisionState(g, "12", history)
		txCode(t, err, "schema_invalid")
	}
	_, err := NewPureRevisionState(g, "12", []any{pureMap{"node_id": "left", "kind": "skill", "mutable": "inherit", "deleted_revision": "2"}})
	txCode(t, err, "identity_conflict")
	for _, revision := range []string{"0", "012", "100000000000000000000"} {
		_, err = NewPureRevisionState(g, revision, nil)
		txCode(t, err, "schema_invalid")
	}
	_, err = Prepare(PureRevisionState{}, txRequest(txRename("left", "new")), PrepareOptions{})
	txCode(t, err, "schema_invalid")
	s := txState(t, g, "12", nil)
	_, err = Prepare(s, TransactionEnvelope{}, PrepareOptions{})
	txCode(t, err, "schema_invalid")
	_, err = RecheckPrepared(s, PurePreparedTransaction{}, PrepareOptions{})
	txCode(t, err, "schema_invalid")
	l := DefaultTransactionLimits()
	l.MaxCommands = 0
	_, err = Prepare(s, txRequest(txRename("left", "new")), PrepareOptions{Limits: &l})
	txCode(t, err, "schema_invalid")
	txReject(t, s, "identity_conflict", pureMap{"op": "create_node", "node": txRole("new", nil)}, pureMap{"op": "create_node", "node": txRole("new", nil)})
}

func txDelete(idsValue []any, edges []any, policy string) pureMap {
	return pureMap{"op": "delete_nodes", "targets": idsValue, "deleted_ids": idsValue, "disconnections": edges, "policy": policy}
}
func txEdge(source, target, relation string) pureMap {
	return pureMap{"source_id": source, "target_id": target, "relation": relation}
}
func txEraseLeft() pureMap {
	return txDelete([]any{"left"}, []any{txEdge("hub", "left", "containment")}, "reject_references")
}
func txPayload(op string, wire any, retained, added, removed []any, scope string) pureMap {
	c := pureMap{"op": op, "id_mapping": pureMap{"retained_ids": retained, "new_ids": added, "deleted_ids": removed}, "payload": pureMap{"inline": wire}}
	if op == "replace_subtree" {
		c["anchor_id"] = scope
	} else {
		c["scope_ids"] = []any{scope}
	}
	return c
}
func TestPureTransactionAllTwentyDeleteRestoreFacts(t *testing.T) {
	txEqual(t, len(SupportedCommands()), 20)
	txEqual(t, UnsupportedCommands(), []string{})
	s := txState(t, nil, "12", nil)
	p := txPrepare(t, s, txEraseLeft())
	tomb := pureMap{"node_id": "left", "kind": "role", "mutable": "inherit", "deleted_revision": "13"}
	txEqual(t, p.Tombstones(), []any{tomb})
	facts := p.Facts().([]any)
	txEqual(t, facts[1], pureMap{"type": "NodeDeleted", "node_id": "left", "before": s.definition.nodes["left"].AsJSON(), "tombstone": tomb})
	txEqual(t, p.FactsDigest(), txHash(t, p.Facts(), "contexture.facts/v1"))
	current := txState(t, &p.candidate, "13", p.Tombstones())
	request := txRequest(pureMap{"op": "restore_nodes", "revision": "12", "node_ids": []any{"left"}}, pureMap{"op": "attach_node", "node_id": "left", "target": txMember("hub", "children", 0)})
	request["base_revision"] = "13"
	options := PrepareOptions{History: []PureRevisionState{s}}
	r, err := Prepare(current, request, options)
	if err != nil {
		t.Fatal(err)
	}
	txEqual(t, r.Facts().([]any)[0], pureMap{"type": "NodeRestored", "node": s.definition.nodes["left"].AsJSON(), "tombstone": tomb})
	txEqual(t, r.Tombstones(), p.Tombstones())
	txEqual(t, r.candidate.AsJSON(), s.definition.AsJSON())
	if _, err = RecheckPrepared(current, r, options); err != nil {
		t.Fatal(err)
	}
	_, err = RecheckPrepared(current, r, PrepareOptions{})
	txCode(t, err, "history_unavailable")
	r.tombstones = mustFreeze([]any{})
	_, err = RecheckPrepared(current, r, options)
	txCode(t, err, "revision_conflict")
	p.Tombstones().([]any)[0].(pureMap)["node_id"] = "local"
	txEqual(t, p.Tombstones(), []any{tomb})
	txEqual(t, s.Tombstones(), []any{})
	noop, err := Prepare(s, txRequest(txEraseLeft(), pureMap{"op": "restore_nodes", "revision": "12", "node_ids": []any{"left"}}, pureMap{"op": "attach_node", "node_id": "left", "target": txMember("hub", "children", 0)}), options)
	if err != nil {
		t.Fatal(err)
	}
	txEqual(t, noop.Noop(), true)
	txEqual(t, noop.Tombstones(), []any{})
}

func TestPureTransactionDeleteBoundaryAndTransientIdentity(t *testing.T) {
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "right"}}}), txRole("left", nil), txRole("right", pureMap{"uses": []any{"left"}})}, nil)
	s := txState(t, g, "12", nil)
	txReject(t, s, "schema_invalid", txEraseLeft())
	edges := []any{txEdge("hub", "left", "containment"), txEdge("right", "left", "uses")}
	p := txPrepare(t, s, txDelete([]any{"left"}, edges, "explicit_cascade"))
	txEqual(t, p.candidate.nodes["right"].uses, []string{})
	txReject(t, s, "schema_invalid", txDelete([]any{"left"}, edges, "reject_references"))
	txReject(t, s, "schema_invalid", txDelete([]any{"left"}, append(edges, edges[0]), "explicit_cascade"))
	create := pureMap{"op": "create_node", "node": txRole("transient", nil)}
	erase := txDelete([]any{"transient"}, []any{}, "reject_references")
	p = txPrepare(t, s, create, erase)
	txEqual(t, p.Noop(), true)
	txEqual(t, p.Tombstones(), []any{})
	txReject(t, s, "identity_conflict", create, erase, create)
}

func TestPureTransactionScopedReplacementAndBlobEvidence(t *testing.T) {
	s := txState(t, nil, "12", nil)
	for _, op := range []string{"replace_subtree", "import", "reconcile"} {
		t.Run(op, func(t *testing.T) {
			wire := s.definition.AsJSON().(pureMap)
			for _, raw := range wire["nodes"].([]any) {
				n := raw.(pureMap)
				if n["node_id"] == "left" {
					n["description"] = "replacement"
					n["members"].(pureMap)["children"] = []any{"new"}
				}
			}
			wire["nodes"] = append(wire["nodes"].([]any), txRole("new", nil))
			command := txPayload(op, wire, []any{"left"}, []any{"new"}, []any{}, "left")
			p := txPrepare(t, s, command)
			txEqual(t, p.candidate.nodes["left"].description, "replacement")
			txEqual(t, p.Protection().Impact(), []string{"hub", "left", "new"})
			if _, err := RecheckPrepared(s, p, PrepareOptions{}); err != nil {
				t.Fatal(err)
			}
			limits := DefaultTransactionLimits()
			limits.MaxExpandedOperations = 1
			_, err := Prepare(s, txRequest(command), PrepareOptions{Limits: &limits})
			txCode(t, err, "limit_exceeded")
			data := []byte(pureEncoding(wire))
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
			command["payload"] = pureMap{"blob": pureMap{"digest": digest, "size": fmt.Sprint(len(data)), "media_type": "application/json"}}
			_, err = Prepare(s, txRequest(command), PrepareOptions{})
			txCode(t, err, "unsupported_contract")
			options := PrepareOptions{Payloads: [][]byte{data}}
			blob, err := Prepare(s, txRequest(command), options)
			if err != nil {
				t.Fatal(err)
			}
			txEqual(t, blob.Facts(), p.Facts())
			data[0] = ' '
			_, err = RecheckPrepared(s, blob, options)
			txCode(t, err, "schema_invalid")
			command["payload"] = pureMap{"inline": wire}
			for _, raw := range wire["nodes"].([]any) {
				n := raw.(pureMap)
				if n["node_id"] == "right" {
					n["description"] = "outside"
				}
			}
			txReject(t, s, "scope_escape", command)
		})
	}
}

func TestPureTransactionPublicationsPresentationsAndProtection(t *testing.T) {
	s := txState(t, nil, "12", nil)
	pub := pureMap{"kind": "prompt", "publication_id": "prompt", "name": "prompt", "description": "prompt", "opens_id": "left", "model_may_open": true}
	p := txPrepare(t, s, pureMap{"op": "put_publication", "publication": pub}, pureMap{"op": "put_presentation", "locale": "EN-us", "presentation": pureMap{"node_id": "left", "title": "Left"}})
	txEqual(t, p.candidate.presentations.catalogs[0].locale, "en-US")
	txEqual(t, p.Facts().([]any)[0], pureMap{"type": "PublicationPut", "before": pureMap{"present": false}, "after": pub, "position": float64(0)})
	txEqual(t, p.Facts().([]any)[1].(pureMap)["type"], "PresentationsAssigned")
	current := txState(t, &p.candidate, "12", nil)
	txReject(t, current, "schema_invalid", txEraseLeft())
	removed := txPrepare(t, current, pureMap{"op": "remove_publication", "publication_id": "prompt"}, pureMap{"op": "remove_presentation", "locale": "en-US", "node_id": "left"}, txEraseLeft())
	txEqual(t, len(removed.candidate.publications), 0)
	protected := txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "right"}}}), txRole("left", pureMap{"mutable": false}), txRole("right", nil)}, nil), "12", nil)
	if _, err := Prepare(protected, txRequest(txEraseLeft()), PrepareOptions{}); err == nil {
		t.Fatal("deleted protected identity")
	}
}

// Optional live cross-language oracle. No fixtures or expected golden bytes
// are changed; all outputs are produced by the native Go candidate.
func TestPureTransactionExactHistoryEvidenceAndAtomicRejection(t *testing.T) {
	s := txState(t, nil, "12", nil)
	restore := pureMap{"op": "restore_nodes", "revision": "2", "node_ids": []any{"left"}}
	for _, history := range [][]PureRevisionState{nil, {PureRevisionState{}}, {txState(t, nil, "3", nil)}, {txState(t, txGraph(t, nil, pureMap{"graph_id": "other"}), "2", nil)}} {
		_, err := Prepare(s, txRequest(txRename("right", "temporary"), restore), PrepareOptions{History: history})
		txCode(t, err, "history_unavailable")
		txEqual(t, s.definition.nodes["right"].name, "right")
	}
	_, err := Prepare(s, txRequest(restore), PrepareOptions{History: []PureRevisionState{txState(t, nil, "2", nil)}})
	txCode(t, err, "identity_conflict")
}

func TestPureTransactionDeleteProgramsCatalogsAndSubtreeClosure(t *testing.T) {
	program := txRole("right", pureMap{"instruction_template": pureMap{"parts": []any{pureMap{"kind": "NodeRef", "target_id": "left"}}}})
	delete(program, "instructions")
	g := txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left", "right"}}}), txRole("left", nil), program}, pureMap{"presentations": pureMap{"default_locale": "en", "catalogs": []any{pureMap{"locale": "en", "entries": []any{pureMap{"node_id": "left", "title": "Left"}}}}}})
	s := txState(t, g, "12", nil)
	txReject(t, s, "schema_invalid", txEraseLeft())
	repair := pureMap{"op": "update_node", "node_id": "right", "set_fields": pureMap{"instructions": ""}, "unset_fields": []any{"instruction_template"}}
	txReject(t, s, "schema_invalid", repair, txEraseLeft())
	p := txPrepare(t, s, repair, pureMap{"op": "remove_presentation", "locale": "EN", "node_id": "left"}, txEraseLeft())
	txEqual(t, len(p.candidate.presentations.catalogs[0].entries), 0)
	deep := txState(t, txGraph(t, []any{txRole("hub", pureMap{"mutable": true, "members": pureMap{"children": []any{"left"}}}), txRole("left", pureMap{"members": pureMap{"children": []any{"right"}}}), txRole("right", nil)}, nil), "12", nil)
	txReject(t, deep, "schema_invalid", txEraseLeft())
	erase := txEraseLeft()
	erase["deleted_ids"] = []any{"right", "left"}
	p = txPrepare(t, deep, erase)
	txEqual(t, p.Tombstones(), []any{pureMap{"node_id": "left", "kind": "role", "mutable": "inherit", "deleted_revision": "13"}, pureMap{"node_id": "right", "kind": "role", "mutable": "inherit", "deleted_revision": "13"}})
	current := txState(t, &p.candidate, "13", p.Tombstones())
	request := txRequest(pureMap{"op": "restore_nodes", "revision": "12", "node_ids": []any{"left"}}, pureMap{"op": "attach_node", "node_id": "left", "target": txMember("hub", "children", 0)})
	request["base_revision"] = "13"
	_, err := Prepare(current, request, PrepareOptions{History: []PureRevisionState{deep}})
	if err == nil {
		t.Fatal("restored incomplete historical subtree")
	}
	request["commands"].([]any)[0].(pureMap)["node_ids"] = []any{"right", "left"}
	restored, err := Prepare(current, request, PrepareOptions{History: []PureRevisionState{deep}})
	if err != nil {
		t.Fatal(err)
	}
	txEqual(t, restored.candidate.AsJSON(), deep.definition.AsJSON())
}

func TestPureTransactionReplacementIdentityScopeAndCanonicalPayloadRefusals(t *testing.T) {
	s := txState(t, nil, "12", nil)
	for _, mutation := range []func(pureMap){
		func(w pureMap) { w["application_name"] = "renamed" },
		func(w pureMap) { w["extensions"] = pureMap{"example": true} },
		func(w pureMap) { w["presentations"].(pureMap)["default_locale"] = "fr" },
		func(w pureMap) {
			w["presentations"].(pureMap)["catalogs"] = []any{pureMap{"locale": "fr", "entries": []any{pureMap{"node_id": "right", "title": "Outside"}}}}
		},
	} {
		wire := s.definition.AsJSON().(pureMap)
		mutation(wire)
		_, err := Prepare(s, txRequest(txPayload("reconcile", wire, []any{"left"}, []any{}, []any{}, "left")), PrepareOptions{})
		if err == nil {
			t.Fatal("accepted scope/identity escape")
		}
	}
	wire := s.definition.AsJSON().(pureMap)
	command := txPayload("reconcile", wire, []any{}, []any{}, []any{}, "left")
	txReject(t, s, "schema_invalid", command)
	command = txPayload("reconcile", wire, []any{"left"}, []any{}, []any{}, "left")
	data := append([]byte(pureEncoding(wire)), '\n')
	command["payload"] = pureMap{"blob": pureMap{"digest": fmt.Sprintf("sha256:%x", sha256.Sum256(data)), "size": fmt.Sprint(len(data)), "media_type": "application/json"}}
	_, err := Prepare(s, txRequest(command), PrepareOptions{Payloads: [][]byte{data}})
	txCode(t, err, "schema_invalid")
	command["payload"].(pureMap)["blob"].(pureMap)["size"] = "0"
	_, err = Prepare(s, txRequest(command), PrepareOptions{Payloads: [][]byte{data}})
	txCode(t, err, "schema_invalid")
}

func TestPureTransactionTwelveTypeScriptDifferentialCases(t *testing.T) {
	path := os.Getenv("CONTEXTURE_TS_DIFFERENTIAL")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path + "/differential-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path + "/typescript-actual.json")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	cases, oracles := arr(raw), arr(expected)
	txEqual(t, len(cases), 12)
	txEqual(t, len(oracles), 12)
	outputs := []any{}
	for i, raw := range cases {
		c := raw.(pureMap)
		t.Run(c["label"].(string), func(t *testing.T) {
			g, err := NewGraphDefinition(c["base"], nil)
			if err != nil {
				t.Fatal(err)
			}
			s := txState(t, g, "7", c["tombstones"])
			options := PrepareOptions{}
			if h, ok := c["history"]; ok {
				for _, raw := range arr(h) {
					value := raw.(pureMap)
					g, err := NewGraphDefinition(value["definition"], nil)
					if err != nil {
						t.Fatal(err)
					}
					options.History = append(options.History, txState(t, g, value["revision"].(string), nil))
				}
			}
			p, err := Prepare(s, c["envelope"], options)
			var result any
			if err != nil {
				var e *GraphError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				result = pureMap{"error": e.Code()}
			} else {
				digest, _ := p.envelope.CommandDigest()
				result = pureMap{"candidate": p.candidate.AsJSON(), "commands": p.envelope.AsJSON().(pureMap)["commands"], "command_digest": digest, "facts": p.Facts(), "tombstones": p.Tombstones(), "facts_digest": p.FactsDigest(), "protection": p.Protection().AsJSON(), "root_operations": p.RootOperations(), "noop": p.Noop(), "result_revision": p.ResultRevision(), "approval_subject": p.ApprovalSubject(), "approval_subject_digest": p.ApprovalSubjectDigest()}
			}
			outputs = append(outputs, result)
			if !pureEqual(result, oracles[i]) {
				t.Fatalf("Go %s\nTS %s", pureEncoding(result), pureEncoding(oracles[i]))
			}
		})
	}
	if out := os.Getenv("CONTEXTURE_GO_DIFFERENTIAL_OUTPUT"); out != "" {
		if err := os.WriteFile(out, []byte(pureEncoding(outputs)), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
