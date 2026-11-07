package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The usage text is generated from the dispatch, so a command the
// dispatch runs is never missing from it, the manual ones included, and
// each line names the pipeline that runs it.
func TestUsageNamesEachCommandWithItsArguments(t *testing.T) {
	lines := strings.Split(usage(), "\n")
	if len(lines) != len(commands)+2 {
		t.Fatalf("usage has %d lines for %d commands:\n%s", len(lines), len(commands), usage())
	}
	for _, want := range []string{
		"  schema-refetch                      check the vendored schemas against what their sources serve (manual)",
		"  release-notes VERSION [CHANGELOG]   print the CHANGELOG section a tag publishes (release)",
		"  live-cover [BIN]                    the live driver exercises every tool option (make check)",
		"  schema-ack BASE HEAD                every tool-surface change since the merge-base is named in a commit footer (pull request)",
		"  api-coverage                        every published API method and batchUpdate request is used or written off (make check)",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("usage has no line %q:\n%s", want, usage())
		}
	}
}

// A command takes as many words as its usage spells, and a word in
// brackets may be left out.
func TestArityIsReadFromTheUsage(t *testing.T) {
	for _, tc := range []struct {
		args        string
		least, most int
	}{
		{"", 0, 0},
		{"[BIN]", 0, 1},
		{"TAG", 1, 1},
		{"VERSION [CHANGELOG]", 1, 2},
		{"BASE HEAD", 2, 2},
	} {
		if least, most := arity(tc.args); least != tc.least || most != tc.most {
			t.Errorf("arity(%q) = %d, %d, want %d, %d", tc.args, least, most, tc.least, tc.most)
		}
	}
}

// The words given fit the usage: as many as it spells, and a word it spells
// in lower case, such as leaks's [history], given as that word, so a typo
// is refused rather than run as the default.
func TestArgumentsFitTheUsage(t *testing.T) {
	for _, tc := range []struct {
		spelling string
		args     []string
		fit      bool
	}{
		{"[history]", nil, true},
		{"[history]", []string{"history"}, true},
		{"[history]", []string{"histroy"}, false},
		{"[history]", []string{"history", "x"}, false},
		{"[BIN]", []string{"./anything"}, true},
		{"BASE HEAD", []string{"origin/main"}, false},
		{"VERSION [CHANGELOG]", []string{"v1.0.0", "NOTES.md"}, true},
	} {
		if got := fits(tc.spelling, tc.args); got != tc.fit {
			t.Errorf("fits(%q, %q) = %v, want %v", tc.spelling, tc.args, got, tc.fit)
		}
	}
}

// The command line refuses a word the usage does not spell, before it runs
// anything: the test binary runs main as `gates leaks histroy`.
func TestTheDispatchRefusesAWordTheUsageDoesNotSpell(t *testing.T) {
	if os.Getenv("GATES_TEST_MAIN") != "" {
		os.Args = []string{"gates", "leaks", "histroy"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheDispatchRefusesAWordTheUsageDoesNotSpell$")
	cmd.Env = append(os.Environ(), "GATES_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || strings.TrimSpace(string(out)) != "gates: usage: gates leaks [history]" {
		t.Errorf("gates leaks histroy: %v\n%s", err, out)
	}
}
