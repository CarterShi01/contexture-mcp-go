package main

import "testing"

func TestVersionPattern(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"v0.1.0", "v0.1.0-rc.1", "v0.12.3-beta"} {
		if !versionPattern.MatchString(version) {
			t.Errorf("versionPattern does not accept %q", version)
		}
	}
	for _, version := range []string{"0.1.0", "v1.0.0", "latest", "v0.1"} {
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
	if err := run([]string{"v0.14.0-rc.1"}); err != nil {
		t.Fatalf("candidate metadata = %v", err)
	}
}
