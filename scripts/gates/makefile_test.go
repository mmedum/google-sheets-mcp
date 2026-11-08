package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMakefileReadsAsMakeDoes(t *testing.T) {
	mk := parseMakefile(textLines(
		"A ?= one",
		"A ?= two",
		"B = x#comment",
		"B += y",
		"C += z",
		"export D := d",
		"override E = e",
		"F := first",
		"F = second",
		`G = docs/architecture.md\#d-67 # note`,
		`H = a\\#b`,
		`I = a\\\#b # c`,
		"J = one \\",
		"    two",
		"K = $(A) $(G)",
		"check: a",
		"\techo one",
		"check: b a ## a help text",
		"check:",
		"lint:",
		"\techo old",
		"lint:",
		"\techo new",
		"quiet:",
		"\t@-echo one",
		"\t+ echo two",
		"\t# a comment the shell would skip",
		"long:",
		"\techo $(A) \\",
		"\t  $$HOME $$(B)",
	))
	// \# is a literal #, and a run of backslashes before a # is halved, as
	// GNU make 4.4.1 reads them.
	wantVars := map[string]string{"A": "one", "B": "x y", "C": "z", "D": "d", "E": "e", "F": "second",
		"G": "docs/architecture.md#d-67", "H": `a\`, "I": `a\#b`, "J": "one two", "K": "$(A) $(G)"}
	if !maps.Equal(mk.vars, wantVars) {
		t.Errorf("vars = %q, want %q", mk.vars, wantVars)
	}
	if got := mk.targets["check"].prereqs; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("check's prerequisites = %q, want [a b]: a target's rules merge them, each once", got)
	}
	if got := mk.recipe("check"); !slices.Equal(got, []string{"echo one"}) {
		t.Errorf("check's recipe = %q, want [echo one]: a rule with no recipe leaves the one before", got)
	}
	if got := mk.recipe("lint"); !slices.Equal(got, []string{"echo new"}) {
		t.Errorf("lint's recipe = %q, want [echo new]: the last recipe is the one that runs", got)
	}
	if got := mk.recipe("quiet"); !slices.Equal(got, []string{"echo one", "echo two"}) {
		t.Errorf("quiet's recipe = %q, want [echo one echo two]: @, - and + are not part of a command", got)
	}
	// $$ is the $ make hands the shell, never the start of a variable.
	if got := mk.recipe("long"); !slices.Equal(got, []string{"echo one $HOME $(B)"}) {
		t.Errorf("long's recipe = %q, want [echo one $HOME $(B)]", got)
	}
	if got := mk.closure("check"); !slices.Equal(got, []string{"a", "b", "check"}) {
		t.Errorf("closure(check) = %q, want [a b check]", got)
	}
	// A variable's value names variables in turn, and expands through them.
	if got := mk.expand("$(K)"); got != "one docs/architecture.md#d-67" {
		t.Errorf("expand($(K)) = %q, want %q", got, "one docs/architecture.md#d-67")
	}
	if got := mk.refs("$(K) $(Z)"); !maps.Equal(got, map[string]bool{"K": true, "A": true, "G": true, "Z": true}) {
		t.Errorf("refs = %v, want K, A, G and Z", got)
	}
	if mk.refused != nil {
		t.Errorf("refused = %q, want nothing: every line is one the reader reads", mk.refused)
	}
}

// A line outside what the reader knows is refused by name, never dropped:
// a recipe or a variable it hides is one no gate would see.
func TestMakefileRefusesWhatItCannotRead(t *testing.T) {
	mk := parseMakefile(textLines(
		"ifeq ($(OS),Windows_NT)",
		"EXE = .exe",
		"endif",
		"-include local.mk",
		"VERSION != git describe",
		"$(BIN): main.go",
		"\tgo build -o $(BIN) \\",
		"\t  ./cmd/x",
		"lint: GOFLAGS = -mod=mod",
		"vet test: gates",
		"%.out: %.in",
		"clean:: cache",
		"fmt: ; gofmt -l .",
		"check: lint # fmt: ; x",
		"\tgo vet ./...",
	))
	want := []string{
		`Makefile:1: "ifeq ($(OS),Windows_NT)" is a directive or an assignment, which this reader does not read`,
		`Makefile:3: "endif" is a directive or an assignment, which this reader does not read`,
		`Makefile:4: "-include local.mk" is a directive or an assignment, which this reader does not read`,
		`Makefile:5: "VERSION != git describe" is a directive or an assignment, which this reader does not read`,
		`Makefile:6: "$(BIN): main.go" is a rule whose target is a variable, which this reader does not read`,
		`Makefile:7: "go build -o $(BIN) ./cmd/x" is a recipe line outside any rule this reader reads`,
		`Makefile:9: "lint: GOFLAGS = -mod=mod" is a target-specific variable, which this reader does not read`,
		`Makefile:10: "vet test: gates" is a rule with more than one target, which this reader does not read`,
		`Makefile:11: "%.out: %.in" is a pattern rule, which this reader does not read`,
		`Makefile:12: "clean:: cache" is a double-colon rule, which this reader does not read`,
		`Makefile:13: "fmt: ; gofmt -l ." is a recipe after a semicolon, which this reader does not read`,
	}
	if !slices.Equal(mk.refused, want) {
		t.Errorf("refused:\n  %s\nwant:\n  %s", strings.Join(mk.refused, "\n  "), strings.Join(want, "\n  "))
	}
	// A refused rule is no target, and a rule beside it is read whole.
	if got := slices.Sorted(maps.Keys(mk.targets)); !slices.Equal(got, []string{"check"}) {
		t.Errorf("targets = %q, want [check]", got)
	}
	if got := mk.recipe("check"); !slices.Equal(got, []string{"go vet ./..."}) {
		t.Errorf("check's recipe = %q, want [go vet ./...]", got)
	}
}

// GNU make runs GNUmakefile or makefile in place of Makefile, so either one
// in the module root is refused by name. Where the file system folds case,
// makefile is Makefile itself, and that row skips.
func TestMakefileRefusesAFileMakeReadsFirst(t *testing.T) {
	const (
		gnu   = "GNUmakefile: GNU make reads it before Makefile, so `make` runs a file no gate reads; fold it into Makefile"
		lower = "makefile: GNU make reads it before Makefile, so `make` runs a file no gate reads; fold it into Makefile"
	)
	for _, tc := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"Makefile alone", nil, nil},
		{"a GNUmakefile", []string{"GNUmakefile"}, []string{gnu}},
		{"a makefile", []string{"makefile"}, []string{lower}},
		{"both", []string{"makefile", "GNUmakefile"}, []string{gnu, lower}},
		{"a GNUmakefile in a subdirectory", []string{"sub/GNUmakefile"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, makefilePath, "all:\n\techo all\n")
			for _, f := range tc.files {
				if _, err := os.Stat(filepath.Join(root, f)); err == nil {
					t.Skipf("this file system reads %s as %s, so the two are one file", f, makefilePath)
				}
				put(t, root, f, "secrets:\n\t@echo skipped\n")
			}
			t.Chdir(root)
			mk, err := readMakefile()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(mk.refused, tc.want) {
				t.Errorf("refused:\n  %s\nwant:\n  %s", strings.Join(mk.refused, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
