package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestStringFallsBackToDev(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() = %q, want the ldflags value", got)
	}

	// A test binary has no main module version, so the fallback path
	// ends at "dev" rather than at a recorded version.
	Version = "dev"
	if got := String(); got != "dev" && !strings.HasPrefix(got, "v") {
		t.Fatalf("String() = %q, want dev or a module version", got)
	}
}

func TestInfoNamesTheBinaryAndToolchain(t *testing.T) {
	info := Info()
	for _, want := range []string{"google-sheets-mcp", runtime.Version(), runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(info, want) {
			t.Errorf("Info() = %q, missing %q", info, want)
		}
	}
}

// TestOneSpellingWhicheverWayItWasBuilt is the drift a reader found by
// comparing five servers side by side: goreleaser stamps its own
// {{.Version}}, which has the leading v stripped, while `go install`
// leaves the fallback to read "v1.1.0" out of the build info. The same
// release reported two different strings depending on how it was
// installed, and the README promises it "reports the release it came
// from either way".
func TestOneSpellingWhicheverWayItWasBuilt(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.1.0", "v1.1.0"},  // goreleaser's stamping
		{"v1.1.0", "v1.1.0"}, // the build-info fallback
		{"1.1.0-rc.1", "v1.1.0-rc.1"},
		{"dev", "dev"}, // an untagged build says so
		{"", ""},
	} {
		if got := canonical(tc.in); got != tc.want {
			t.Errorf("canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
