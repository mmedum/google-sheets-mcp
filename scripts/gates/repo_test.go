package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDefaultBinaryMatchesWhatIsBuilt asserts the gates look for a
// binary they can *run*, which is not the same as one they can stat.
//
// The first version of this test wrote an extensionless file and checked
// that defaultBinary stat'd it — which is exactly the state that failed
// on the first Windows CI run, and this test passed on it. `go build -o
// <name>` does write exactly <name> on every platform, and on Windows
// that file cannot be executed, so the reasoning was right about the
// build and wrong about the thing that matters.
func TestDefaultBinaryMatchesWhatIsBuilt(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	name := Binary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not really a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := defaultBinary()
	if _, err := os.Stat(got); err != nil {
		t.Errorf("defaultBinary() = %q, which is not what the build produces: %v", got, err)
	}
	// The half the old test could not fail on: what it points at has to
	// be runnable on this platform.
	if runtime.GOOS == "windows" && !strings.HasSuffix(got, ".exe") {
		t.Errorf("defaultBinary() = %q; Windows cannot execute a file with no extension in PATHEXT", got)
	}
	if runtime.GOOS != "windows" && strings.HasSuffix(got, ".exe") {
		t.Errorf("defaultBinary() = %q", got)
	}
}
