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

	"github.com/CarterShi01/contexture-mcp-go/core/foundation"
)

// versionPattern accepts stable v1 module tags. The first public release
// establishes the v1 compatibility line, so release candidates and pre-1.0
// tags are intentionally not accepted here.
var versionPattern = regexp.MustCompile(`^v1\.[0-9]+\.[0-9]+$`)

var majorVersionModulePathPattern = regexp.MustCompile(`/v[0-9]+$`)

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

func validateRelease(version, path, status string) error {
	expectedVersion := "v" + foundation.PackageVersion
	if !versionPattern.MatchString(expectedVersion) {
		return fmt.Errorf("package version %q is not a stable v1 semantic version", foundation.PackageVersion)
	}
	if version != expectedVersion {
		return fmt.Errorf("release version must equal package version %s", expectedVersion)
	}
	if majorVersionModulePathPattern.MatchString(path) {
		return fmt.Errorf("v1 release %s must use an unsuffixed module path, got %q", version, path)
	}
	if status != "conformant" {
		return fmt.Errorf("refusing to release while conformance status is %q; want %q", status, "conformant")
	}
	return nil
}

func run(arguments []string) error {
	if len(arguments) != 1 {
		return errors.New("releasecheck requires exactly one version argument")
	}
	path, err := modulePath()
	if err != nil {
		return err
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
	return validateRelease(arguments[0], path, conformance.Status)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
