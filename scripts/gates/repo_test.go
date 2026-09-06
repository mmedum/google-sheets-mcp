package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDefaultBinaryMatchesWhatIsBuilt is the Windows leg of the matrix
// caught before it runs. `go build -o <name>` writes exactly <name> on
// every platform, and both the Makefile and the workflow pass one — so a
// gate that expected an extension would have failed every check on
// windows-latest and passed everywhere else.
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

	built := filepath.Join(dir, Binary)
	if err := os.WriteFile(built, []byte("not really a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := defaultBinary()
	if _, err := os.Stat(got); err != nil {
		t.Errorf("defaultBinary() = %q, which is not what `go build -o %s` produces: %v", got, Binary, err)
	}
	if runtime.GOOS != "windows" && strings.HasSuffix(got, ".exe") {
		t.Errorf("defaultBinary() = %q", got)
	}
}
