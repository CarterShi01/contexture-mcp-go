package main

import "testing"

func TestVersionPattern(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"v1.0.0", "v1.0.1", "v1.12.3"} {
		if !versionPattern.MatchString(version) {
			t.Errorf("versionPattern does not accept %q", version)
		}
	}
	for _, version := range []string{"0.1.0", "v0.14.0", "v1.0.0-rc.1", "v2.0.0", "latest", "v1.0"} {
		if versionPattern.MatchString(version) {
			t.Errorf("versionPattern unexpectedly accepts %q", version)
		}
	}
}

func TestCurrentModulePathSupportsCandidateTag(t *testing.T) {
	path, err := modulePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "github.com/CarterShi01/contexture-mcp-go" {
		t.Fatalf("module path = %q", path)
	}
	if err := run([]string{"v1.0.0"}); err != nil {
		t.Fatalf("candidate metadata = %v", err)
	}
	if err := run([]string{"v1.0.1"}); err == nil {
		t.Fatal("releasecheck accepted a tag that does not match the package version")
	}
}

func TestValidateReleaseRequirements(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version string
		path    string
		status  string
		wantErr bool
	}{
		{name: "current stable release", version: "v1.0.0", path: "github.com/CarterShi01/contexture-mcp-go", status: "conformant"},
		{name: "different v1 tag", version: "v1.0.1", path: "github.com/CarterShi01/contexture-mcp-go", status: "conformant", wantErr: true},
		{name: "pre-release tag", version: "v1.0.0-rc.1", path: "github.com/CarterShi01/contexture-mcp-go", status: "conformant", wantErr: true},
		{name: "suffixed v1 path", version: "v1.0.0", path: "github.com/CarterShi01/contexture-mcp-go/v1", status: "conformant", wantErr: true},
		{name: "partial conformance", version: "v1.0.0", path: "github.com/CarterShi01/contexture-mcp-go", status: "partial", wantErr: true},
		{name: "scaffold conformance", version: "v1.0.0", path: "github.com/CarterShi01/contexture-mcp-go", status: "scaffold", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateRelease(test.version, test.path, test.status)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateRelease(%q, %q, %q) error = %v, wantErr %t", test.version, test.path, test.status, err, test.wantErr)
			}
		})
	}
}
