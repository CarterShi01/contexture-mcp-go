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
