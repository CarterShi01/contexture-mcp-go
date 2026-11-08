package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestPortableBindingPrimitiveObservations(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/managed/portable-binding-primitives.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Schema      string `json:"schema"`
		ContractID  string `json:"contract_id"`
		InputSchema any    `json:"input_schema"`
		Cases       []struct {
			ID       string `json:"id"`
			Input    any    `json:"input"`
			Expected any    `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Schema != "contexture.portable-binding-primitives/candidate-v1" {
		t.Fatal("Unexpected corpus schema")
	}
	stages := []any{}
	retained := []map[string]any{}
	type decoded struct {
		count int
		label string
	}
	resource := &struct{ marker bool }{true}
	identity := ManagedIdentity{ActorID: "actor", TenantID: "tenant", AuthorityRevision: "1"}
	decimal := regexp.MustCompile(`\A(?:0|[1-9][0-9]?|-[1-9][0-9]?)\z`)
	registration, err := NewBindingRegistration(BindingRegistrationOptions[decoded]{
		BindingID: "primitives", Version: "1", Kind: "registered", Effect: "read_only", ABI: ManagedABI,
		ArtifactDigest:      "sha256:" + strings.Repeat("a", 64),
		Portability:         Portability{Kind: "portable", ContractID: corpus.ContractID},
		InputSchema:         corpus.InputSchema,
		ValidatorContractID: "count-range/v1", DecoderContractID: "decimal-count-label/v1",
		DefaultContractID: "count-two-label-default/v1", CoercionContractID: "bounded-decimal/v1",
		Decoder: func(args FrozenJSON) (decoded, error) {
			stages = append(stages, "decoder")
			input, ok := args.JSON().(map[string]any)
			if !ok {
				return decoded{}, errors.New("Expected object")
			}
			value, present := input["count"]
			count := 2
			if present {
				switch value := value.(type) {
				case string:
					if !decimal.MatchString(value) {
						return decoded{}, errors.New("Noncanonical bounded decimal")
					}
					var err error
					count, err = strconv.Atoi(value)
					if err != nil {
						return decoded{}, err
					}
				case float64:
					count = int(value)
				default:
					return decoded{}, errors.New("Expected native count")
				}
			}
			label := "default"
			if value, present := input["label"]; present {
				var ok bool
				label, ok = value.(string)
				if !ok {
					return decoded{}, errors.New("Expected native label")
				}
			}
			return decoded{count, label}, nil
		},
		Validator: func(input decoded) error {
			stages = append(stages, "validator")
			if input.count < 0 || input.count > 9 {
				return errors.New("Count outside native contract")
			}
			return nil
		},
		Handler: func(_ context.Context, input decoded, call ManagedCallContext) (any, error) {
			stages = append(stages, "handler")
			if call.Resource != resource || call.Identity != identity {
				return nil, errors.New("Captured context/resource changed")
			}
			result := map[string]any{"count": input.count, "label": input.label, "actor": call.Identity.ActorID}
			retained = append(retained, result)
			return result, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observations := []any{}
	for _, candidate := range corpus.Cases {
		t.Run(candidate.ID, func(t *testing.T) {
			stages = []any{}
			before, err := CanonicalJSON(candidate.Input)
			if err != nil {
				t.Fatal(err)
			}
			result, callErr := registration.Call(context.Background(), candidate.Input,
				ManagedCallContext{Identity: identity, Resource: resource})
			actual := map[string]any{"status": "rejected", "stages": append([]any{}, stages...)}
			if callErr == nil {
				actual["status"], actual["output"] = "ok", result.JSON()
				retained[len(retained)-1]["label"] = "mutated-after-call"
				if !jsonEqual(result.JSON(), actual["output"]) {
					t.Fatal("Result alias escaped")
				}
			}
			after, err := CanonicalJSON(candidate.Input)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("Input was changed", err)
			}
			if !jsonEqual(actual, candidate.Expected) {
				t.Fatalf("Observation mismatch: got %v, want %v (error: %v)", actual, candidate.Expected, callErr)
			}
			observations = append(observations, map[string]any{"id": candidate.ID, "observation": actual})
		})
	}
	if output := os.Getenv("CONTEXTURE_BINDING_OBSERVATIONS"); output != "" {
		raw, err := CanonicalJSON(observations)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
