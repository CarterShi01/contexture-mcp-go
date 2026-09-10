// Command releasecheck validates a proposed Go module version before tagging.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^v0\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type conformanceStatus struct {
	Status string `json:"status"`
}

func moduleRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		if _, err = os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect go.mod: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("cannot find go.mod in this directory or its parents")
		}
		directory = parent
	}
}

func modulePath() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if path, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			return strings.TrimSpace(path), nil
		}
	}
	return "", errors.New("go.mod does not declare a module path")
}

func run(arguments []string) error {
	if len(arguments) != 1 || !versionPattern.MatchString(arguments[0]) {
		return errors.New("version must be a pre-1.0 semantic version such as v0.1.0-rc.1")
	}
	path, err := modulePath()
	if err != nil {
		return err
	}
	if strings.HasSuffix(path, "/v2") {
		return fmt.Errorf("pre-1.0 release %s cannot use major-version module path %q", arguments[0], path)
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "conformance", "specification.json"))
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
