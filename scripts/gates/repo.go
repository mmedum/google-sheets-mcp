package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Binary is the built server's name.
const Binary = "google-sheets-mcp"

// defaultBinary is where `make build` and CI put the server.
//
// On Windows that is the .exe, and this used to say the opposite. The
// reasoning was that `go build -o <name>` writes exactly <name> on every
// platform, which is true — and irrelevant, because Windows cannot
// execute a file with no extension in PATHEXT. So an extensionless build
// there produces a binary every gate can stat and none can run, and the
// first Windows CI run said exactly that: `exec: ".\google-sheets-mcp":
// executable file not found in %PATH%`.
//
// The comment that got it wrong was a reasoned one, which is worse than
// no comment: it explained the choice convincingly enough that a reader
// checking this would have stopped. The build produces a .exe on Windows
// now, and the extensionless name is checked second, for a tree built
// before this change.
func defaultBinary() string {
	plain := "." + string(filepath.Separator) + Binary
	if runtime.GOOS != "windows" {
		return plain
	}
	if _, err := os.Stat(plain + ".exe"); err == nil {
		return plain + ".exe"
	}
	return plain
}

// repoRoot walks up from the working directory to the module root, so a
// gate behaves the same however it was invoked.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// git runs a git command and returns its trimmed output.
func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// lastTag is the most recent tag, or "" when there is none yet.
func lastTag() string {
	tag, err := git("describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return tag
}
