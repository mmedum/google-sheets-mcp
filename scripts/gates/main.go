// Command gates is this repository's own checks.
//
// They are Go rather than shell for two reasons. They run on every
// platform CI uses, so a gate cannot quietly become Linux-only while a
// three-platform matrix says otherwise; and they have tests, because a
// gate nobody has watched fail is not yet a gate — "found nothing" and
// "looked at nothing" print the same sentence otherwise, so every check
// here also asserts a floor on how much it read.
//
// `go run ./scripts/gates` lists the commands and their arguments.
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

// command is one thing this program does.
type command struct {
	// run is given the words after the command's name, as many as args
	// allows.
	run func(args []string)
	// args spells those words for the usage text, and says how many the
	// command takes: a word in brackets is optional, and a word in lower
	// case is given as written.
	args string
	doc  string
	// runsIn is the pipeline that has to run the command, which the parity
	// gate holds.
	runsIn pipeline
}

// pipeline is where a command has to run.
type pipeline int

const (
	// manual is run by hand or by the pre-commit hook, never by `make
	// check`. It starts at one, so a command that names no pipeline is
	// refused rather than read as manual.
	manual pipeline = iota + 1
	// inCheck runs in `make check` and in CI.
	inCheck
	// inPR runs in CI's pull-request job and not in `make check`: it
	// measures from the merge-base, which only a pull request has.
	inPR
	// inRelease runs in the release workflows or the goreleaser config.
	inRelease
)

func (p pipeline) String() string {
	switch p {
	case manual:
		return "manual"
	case inCheck:
		return "make check"
	case inPR:
		return "pull request"
	case inRelease:
		return "release"
	}
	return "no pipeline"
}

// commands is the one list of what this program does. The dispatch, the
// usage text, the docs gate and the parity gate all read it, so none of
// them can name a command the others do not.
//
// Filled in init rather than as a literal, because the docs and parity
// gates read this map and Go sees that as an initialization cycle.
var commands map[string]command

func init() {
	commands = map[string]command{
		"coverage": {
			run: coverageCmd, args: "PROFILE MIN",
			doc: "enforce the coverage floor per package", runsIn: inCheck,
		},
		"classes": {
			run: func([]string) { check(classGate(), "error classes") },
			doc: "the error vocabulary, held closed from both sides", runsIn: inCheck,
		},
		"leaks": {
			run:    func(a []string) { check(leakGate(len(a) > 0 && a[0] == "history"), "leak scan") },
			args:   "[history]",
			doc:    "identifiers and data in the working tree, or in every blob and message of the history",
			runsIn: inCheck,
		},
		"transcript": {
			run: func([]string) { check(transcriptGate(), "transcript redaction") },
			doc: "the live drivers print only through their redactor", runsIn: inCheck,
		},
		"live-cover": {
			run: func(a []string) { check(liveCoverGate(binArg(a)), "live driver coverage") }, args: "[BIN]",
			doc: "the live driver exercises every tool option", runsIn: inCheck,
		},
		"pins": {
			run: func([]string) { check(pinGate(), "workflow pins") },
			doc: "actions pinned by SHA, tool versions exact, shells pinned", runsIn: inCheck,
		},
		"smoke": {
			run: func(a []string) { check(smoke(binArg(a)), "stdio smoke") }, args: "[BIN]",
			doc: "drive the binary over stdio", runsIn: inCheck,
		},
		"staleness": {
			run: func(a []string) { check(staleness(binArg(a)), "staleness") }, args: "[BIN]",
			doc: "the docs must match the code", runsIn: inCheck,
		},
		"docs": {
			run: func([]string) { check(docsGate(os.Stdout), "docs") },
			doc: "every decision, citation, link, path and live step the docs and code name resolves", runsIn: inCheck,
		},
		"schema-diff": {
			run: func(a []string) { check(schemaDiff(binArg(a)), "schema diff") }, args: "[BIN]",
			doc: "diff the tool schemas against the newest release's recorded baseline", runsIn: inCheck,
		},
		// A release commit runs it, recording the surface of the release
		// being cut; on any other commit it refuses, since the build is not
		// stamped as that release.
		"schema-baseline": {
			run: func(a []string) { check(writeBaseline(binArg(a)), "schema baseline") }, args: "[BIN]",
			doc: "record the release being cut as the baseline, in its release commit", runsIn: manual,
		},
		"schema-ack": {
			run: func(a []string) { check(schemaAck(os.Stdout, a[0], a[1]), "schema ack") }, args: "BASE HEAD",
			doc: "every tool-surface change since the merge-base is named in a commit footer", runsIn: inPR,
		},
		"mcpb": {
			run: func([]string) { check(mcpbGate(os.Stdout), "bundle manifest") },
			doc: "the Claude Desktop bundle's manifest, against the files it will pack", runsIn: inCheck,
		},
		// The universal binary's post hook, where the binaries it packs
		// exist; mcpb is the half that runs on every commit.
		"mcpb-pack": {
			run: mcpbPackCmd, args: "VERSION [DIST]",
			doc: "pack the bundle from the built binaries", runsIn: inRelease,
		},
		"api-coverage": {
			run: func([]string) { check(apiCoverageGate(), "API coverage") },
			doc: "every published API method and batchUpdate request is used or written off", runsIn: inCheck,
		},
		"api-fields": {
			run: func([]string) { check(apiFieldsGate(os.Stdout), "API fields") },
			doc: "every published field is modeled on purpose or written off", runsIn: inCheck,
		},
		// Manual: it reaches the network. What CI holds is the snapshot
		// this writes, not the fetch itself.
		"api-diff": {
			run:    func([]string) { check(apiDiff(os.Stdout), "API diff") },
			doc:    "refetch the discovery documents and rewrite the API snapshot",
			runsIn: manual,
		},
		// Manual for the same reason: a vendored schema's digest says the
		// bytes are the ones somebody reviewed, never that upstream still
		// serves them, and only a fetch can tell the difference.
		"schema-refetch": {
			run:    func([]string) { check(schemaRefetch(os.Stdout), "vendored schemas") },
			doc:    "check the vendored schemas against what their sources serve",
			runsIn: manual,
		},
		"parity": {
			run: func([]string) { check(parityGate(), "make check and CI agree") },
			doc: "`make check` and ci.yml run the same commands, and each command runs where it says", runsIn: inCheck,
		},
		// Not a gate: it prints the CHANGELOG section the workflow hands
		// goreleaser as --release-notes, and nothing else, since stdout is
		// the release body.
		"release-notes": {
			run: releaseNotesToStdout, args: "VERSION [CHANGELOG]",
			doc: "print the CHANGELOG section a tag publishes", runsIn: inRelease,
		},
		// It runs before goreleaser, and refuses a tag whose major version
		// is not go.mod's.
		"release-tag": {
			run: func(a []string) { check(releaseTag(a[0]), "release tag") }, args: "TAG",
			doc: "refuse a tag whose major version is not go.mod's", runsIn: inRelease,
		},
		// Not a gate: it runs after a release, printing the entry the
		// publish workflow hands mcp-publisher.
		"registry-publish": {
			run: func(a []string) {
				if err := registryPublish(os.Stdout, a[0], a[1]); err != nil {
					fail("registry-publish: %v", err)
				}
			},
			args: "VERSION CHECKSUMS", doc: "print the MCP registry entry for a release's checksums", runsIn: inRelease,
		},
		// The pre-commit hook's own entry point; it runs other gates rather
		// than being one.
		"precommit": {
			run: func([]string) { check(precommit(), "pre-commit") },
			doc: "the checks the pre-commit hook runs", runsIn: manual,
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		fail("%s", usage())
	}
	name, args := os.Args[1], os.Args[2:]
	c, ok := commands[name]
	if !ok {
		fail("unknown gate %q\n\n%s", name, usage())
	}
	if !fits(c.args, args) {
		fail("usage: gates %s", synopsis(name))
	}
	root, err := repoRoot()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Chdir(root); err != nil {
		fail("%v", err)
	}
	c.run(args)
}

// arity is the fewest and the most words a command's args spells.
func arity(args string) (least, most int) {
	for _, word := range strings.Fields(args) {
		most++
		if !strings.HasPrefix(word, "[") {
			least++
		}
	}
	return least, most
}

// fits reports whether args fit a command's args spelling: as many words as
// it spells, and a word it spells in lower case, such as [history], given
// as that word, so a typo is refused rather than run as the default.
func fits(spelling string, args []string) bool {
	if least, most := arity(spelling); len(args) < least || len(args) > most {
		return false
	}
	for i, word := range strings.Fields(spelling)[:len(args)] {
		if literal := strings.Trim(word, "[]"); literal == strings.ToLower(literal) && args[i] != literal {
			return false
		}
	}
	return true
}

// usage is generated from commands, so it cannot fall behind them.
func usage() string {
	names := slices.Sorted(maps.Keys(commands))
	width := 0
	for _, name := range names {
		width = max(width, len(synopsis(name)))
	}
	var b strings.Builder
	b.WriteString("usage: gates COMMAND [ARGS]\n\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %-*s  %s (%s)\n", width, synopsis(name), commands[name].doc, commands[name].runsIn)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// synopsis is a command's name and the arguments it takes.
func synopsis(name string) string {
	return strings.TrimSpace(name + " " + commands[name].args)
}

// binArg is the binary a command was given, or the one `make build` and
// CI write.
func binArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return defaultBinary()
}

func check(err error, what string) {
	if err != nil {
		failWith(exitCode(err), "%s: %v", what, err)
	}
	fmt.Printf("%s ok\n", what)
}

// floorProblems is a problem when a gate read fewer of what than least:
// found nothing and looked at nothing print the same sentence otherwise.
func floorProblems(what string, read, least int) []string {
	if read >= least {
		return nil
	}
	return []string{fmt.Sprintf("read %d %s, want at least %d: the reader is not seeing its input", read, what, least)}
}

// couldNotRun is a gate that could not reach a verdict, a failed build
// or git call, rather than one that found a problem. It says so, and
// exits 2 where a verdict exits 1: under `go run`, which exits 1 for
// both, the message is what tells them apart.
type couldNotRun struct{ err error }

func (e couldNotRun) Error() string { return "could not run: " + e.err.Error() }
func (e couldNotRun) Unwrap() error { return e.err }

// exitCode is 2 for a gate that could not run and 1 for a verdict.
func exitCode(err error) int {
	if errors.As(err, new(couldNotRun)) {
		return 2
	}
	return 1
}

func fail(format string, args ...any) { failWith(1, format, args...) }

func failWith(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gates: "+format+"\n", args...)
	os.Exit(code)
}

func coverageCmd(args []string) {
	minimum, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fail("coverage: %v", err)
	}
	check(coverageFloor(args[0], minimum), "coverage floor")
}

func mcpbPackCmd(args []string) {
	dist := ""
	if len(args) > 1 {
		dist = args[1]
	}
	check(mcpbPack(os.Stdout, args[0], dist), "bundle packed")
}
