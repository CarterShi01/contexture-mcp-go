// Command releasecheck validates a proposed Go module version before tagging.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

var versionPattern = regexp.MustCompile(`^v0\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type conformanceStatus struct {
	Status string `json:"status"`
}

func run(arguments []string) error {
	if len(arguments) != 1 || !versionPattern.MatchString(arguments[0]) {
		return errors.New("version must be a pre-1.0 semantic version such as v0.1.0-rc.1")
	}

	data, err := os.ReadFile("conformance/specification.json")
	if err != nil {
		return fmt.Errorf("read conformance status: %w", err)
	}
	var conformance conformanceStatus
	if err := json.Unmarshal(data, &conformance); err != nil {
		return fmt.Errorf("decode conformance status: %w", err)
	}
	if conformance.Status == "scaffold" {
		return errors.New("refusing to release while conformance status is scaffold")
	}
	if conformance.Status != "partial" && conformance.Status != "conformant" {
		return fmt.Errorf("unknown conformance status %q", conformance.Status)
	}

	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
