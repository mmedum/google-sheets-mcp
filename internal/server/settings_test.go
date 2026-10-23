package server_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// settingsWriters are the packages through which code could change this
// server's own settings: the stored profile, the credentials, the sign-in,
// and the process's files and environment. Every tool is registered from
// this package, so nothing it reaches may import one.
var settingsWriters = []string{
	"internal/auth", "internal/credentials", "internal/userconfig",
	"os", "os/exec", "syscall",
}

// TestNoToolCanWriteTheServersSettings holds §9a's rule that the model
// never widens its own reach: no tool turns on a toolset, lifts a limit
// or edits the configuration, since only the operator may, outside the
// conversation. It is held by what the tool path can reach rather than
// by what each tool happens to do: walking every package this one
// imports, tests aside, none is a package that writes settings.
func TestNoToolCanWriteTheServersSettings(t *testing.T) {
	root := filepath.Join("..", "..")
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(mod), "\n", 2)[0], "module"))
	seen := map[string]bool{}
	var reach func(pkg, via string)
	reach = func(pkg, via string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pkg), "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				local, inModule := strings.CutPrefix(path, module+"/")
				switch {
				case slices.Contains(settingsWriters, path), inModule && slices.Contains(settingsWriters, local):
					t.Errorf("%s imports %s, through %s; a tool could write the server's own settings", pkg, path, via)
				case inModule:
					reach(local, via+" -> "+local)
				}
			}
		}
	}
	reach("internal/server", "internal/server")
	// Zero findings and zero packages read look the same, so the walk
	// must have reached the tool path's own packages.
	for _, want := range []string{"internal/tools", "internal/service", "internal/config", "internal/gapi"} {
		if !seen[want] {
			t.Errorf("the walk never reached %s; it read %d package(s)", want, len(seen))
		}
	}
	if len(seen) < 10 {
		t.Errorf("the walk read %d package(s); the tool path has more", len(seen))
	}
}
