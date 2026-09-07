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
