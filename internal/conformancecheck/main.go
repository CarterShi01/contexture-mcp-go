// Command conformancecheck validates the binding's deterministic contract metadata.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

const (
	expectedRevision  = "cda2721c7c40128cd0b7eef990e5909edabd3b17"
	expectedVersion   = "0.16"
	expectedRuleCount = 18
)

var expectedFixtures = []string{
	"disclosure-only-application.json",
	"prompt-roots-application.json",
	"reference-application.json",
	"rest-routes.json",
	"root-selections.json",
}

var expectedGolden = []string{
	"commands.json",
	"completions.json",
	"discover.json",
	"instructions.txt",
	"open.json",
	"prompts.json",
	"reads.json",
	"refusals.json",
	"resources.json",
	"tools.json",
}

type ruleState struct {
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type manifest struct {
	Schema               string               `json:"$schema"`
	Repository           string               `json:"repository"`
	Revision             string               `json:"revision"`
	SpecificationVersion string               `json:"specificationVersion"`
	Status               string               `json:"status"`
	ImplementedRules     []int                `json:"implementedRules"`
	Rules                map[string]ruleState `json:"rules"`
	Fixtures             []string             `json:"fixtures"`
	Golden               []string             `json:"golden"`
	Notes                string               `json:"notes"`
}

type schemaDocument struct {
	Required   []string `json:"required"`
	Properties struct {
		Rules struct {
			Required []string `json:"required"`
		} `json:"rules"`
	} `json:"properties"`
}

func decodeStrict(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func run() error {
	var current manifest
	if err := decodeStrict("conformance/specification.json", &current); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	var schema schemaDocument
	schemaData, err := os.ReadFile("conformance/specification.schema.json")
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	source, err := os.ReadFile("core/foundation/specification.go")
	if err != nil {
		return fmt.Errorf("read specification constants: %w", err)
	}
	if current.Revision != expectedRevision || !strings.Contains(string(source), `SpecificationRevision = "`+expectedRevision+`"`) {
		return errors.New("revision pin is not synchronized")
	}
	if current.SpecificationVersion != expectedVersion || !strings.Contains(string(source), `SpecificationVersion = "`+expectedVersion+`"`) {
		return errors.New("specification version is not synchronized")
	}
	if !slices.Equal(current.Fixtures, expectedFixtures) {
		return errors.New("fixture inventory is incomplete or unordered")
	}
	if !slices.Equal(current.Golden, expectedGolden) {
		return errors.New("golden inventory is incomplete or unordered")
	}
	for _, fixture := range expectedFixtures {
		if _, err := os.Stat("conformance/fixtures/" + fixture); err != nil {
			return fmt.Errorf("fixture asset %q is unavailable: %w", fixture, err)
		}
	}
	for _, golden := range expectedGolden {
		if _, err := os.Stat("conformance/golden/" + golden); err != nil {
			return fmt.Errorf("golden asset %q is unavailable: %w", golden, err)
		}
	}

	implemented := make([]int, 0, expectedRuleCount)
	for number := 1; number <= expectedRuleCount; number++ {
		entry, ok := current.Rules[fmt.Sprint(number)]
		if !ok {
			return fmt.Errorf("rule %d is missing", number)
		}
		if entry.Status != "not-started" && entry.Status != "in-progress" && entry.Status != "implemented" {
			return fmt.Errorf("rule %d has invalid status %q", number, entry.Status)
		}
		seen := make(map[string]struct{}, len(entry.Evidence))
		for _, evidence := range entry.Evidence {
			if evidence == "" {
				return fmt.Errorf("rule %d has empty evidence", number)
			}
			if _, duplicate := seen[evidence]; duplicate {
				return fmt.Errorf("rule %d repeats evidence %q", number, evidence)
			}
			seen[evidence] = struct{}{}
		}
		if entry.Status == "implemented" {
			if len(entry.Evidence) == 0 {
				return fmt.Errorf("rule %d needs evidence", number)
			}
			for _, evidence := range entry.Evidence {
				if _, err := os.Stat(evidence); err != nil {
					return fmt.Errorf("rule %d evidence %q is unavailable: %w", number, evidence, err)
				}
			}
			implemented = append(implemented, number)
		}
	}
	if len(current.Rules) != expectedRuleCount {
		return fmt.Errorf("rules must contain exactly 1 through %d", expectedRuleCount)
	}
	if !slices.Equal(current.ImplementedRules, implemented) {
		return errors.New("implementedRules disagrees with rule statuses")
	}
	expectedStatus := "partial"
	if slices.Equal(implemented, []int{1}) {
		expectedStatus = "scaffold"
	} else if len(implemented) == expectedRuleCount {
		expectedStatus = "conformant"
	}
	if current.Status != expectedStatus {
		return fmt.Errorf("status must be %q", expectedStatus)
	}

	expectedRequired := []string{"repository", "revision", "specificationVersion", "status", "implementedRules", "rules", "fixtures", "golden"}
	if !slices.Equal(schema.Required, expectedRequired) {
		return errors.New("vendored schema required fields drifted")
	}
	expectedRuleKeys := make([]string, expectedRuleCount)
	for index := range expectedRuleKeys {
		expectedRuleKeys[index] = fmt.Sprint(index + 1)
	}
	if !slices.Equal(schema.Properties.Rules.Required, expectedRuleKeys) {
		return errors.New("vendored schema does not require all rules")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "invalid conformance metadata:", err)
		os.Exit(1)
	}
	fmt.Println("conformance metadata is valid")
}
