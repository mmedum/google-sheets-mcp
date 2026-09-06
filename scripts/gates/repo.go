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
// No .exe, on any platform: `go build -o <name>` writes exactly that
// name, and both the Makefile and the workflow pass a name. Guessing an
// extension here would have failed every gate on the Windows leg of the
// matrix — three of them take no argument in CI — which is the shape of
// bug a three-platform matrix exists to catch and would have caught only
// on the first run.
//
// A person who built without -o on Windows has the .exe, so that is
// checked second rather than first.
func defaultBinary() string {
	plain := "." + string(filepath.Separator) + Binary
	if runtime.GOOS != "windows" {
		return plain
	}
	if _, err := os.Stat(plain); err == nil {
		return plain
	}
	return plain + ".exe"
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
