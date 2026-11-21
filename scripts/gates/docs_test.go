package main

import (
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixture checkout's paths, named once because most rows edit them.
const (
	fxArch       = "docs/architecture.md"
	fxRunbook    = "docs/runbook.md"
	fxReadme     = "README.md"
	fxAgent      = "CLAUDE.md"
	fxChangelog  = "CHANGELOG.md"
	fxClient     = "internal/gapi/client.go"
	fxClientTest = "internal/gapi/client_test.go"
	fxServer     = "cmd/server/main.go"
	fxRecord     = "testdata/api-coverage.tsv"
	fxFields     = "testdata/api-fields.tsv"
	fxGolangci   = ".golangci.yml"
	fxMakefile   = "Makefile"
	fxGitignore  = ".gitignore"
	fxGitleaks   = ".gitleaks.toml"
	fxGates      = "scripts/gates/main.go"
	fxDriver     = "scripts/livesheet/main.go"
	fxDriverStub = "scripts/livesheet/disabled.go"
	fxDriverTest = "scripts/livesheet/main_test.go"
	fxSpikes     = "scripts/spikes/main.go"
)

// textLines is a file of these lines, so a fixture's line numbers can be
// read off its source: the nth argument is line n.
func textLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// The index rows, named because several rows edit them whole.
const (
	fxRow01 = "| D-01 | Retries back off | accepted | TestRetry, smoke |"
	fxRow02 = "| D-02 | Retry forever | superseded by D-03 | — |"
	fxRow03 = "| D-03 | Retry three times | accepted | forbidigo, lint, review |"
)

// The ends of the problems several tests expect, written once.
const (
	notAHolder = "which is not review, a make target, a gates command, an enabled linter or a test or fuzz function"
	bySlug     = "cite a D-ID, docs/<file>.md#<slug> or a source link"
)

// docsFixture is a checkout the docs gate accepts as it stands. Between
// them its files hold one of everything the gate must stay quiet about:
// each kind of Held by item, a superseded decision cited only where that
// is allowed, a repeated heading,
// links to a file and a directory, a codemap naming each package, live
// steps of each kind cited across comment lines, config comments, the
// records' reasons, and citations in a fence, a string literal, a
// testdata page and a released changelog section.
func docsFixture() map[string]string {
	return map[string]string{
		fxMakefile: textLines(
			"# The checks CI runs (D-03).",
			"check: lint docs",
			"",
			".PHONY: lint",
			"lint:",
			"\tgolangci-lint run",
			"",
			".PHONY: docs",
			"docs:",
			"\t@go run ./scripts/gates docs",
			"",
			".PHONY: live",
			"live:",
			"\tgo run -tags=live ./scripts/livesheet",
		),
		fxGolangci: textLines(
			`version: "2"`,
			"linters:",
			"  default: none",
			"  enable:",
			"    - errcheck",
			"    - forbidigo # stdout carries only frames",
			"  settings:",
			"    govet:",
			"      enable:",
			"        - nilness",
			"    misspell:",
			"      locale: US # American English, as D-01 says",
			"formatters:",
			"  enable:",
			"    - gofmt",
		),
		fxGitignore: textLines("internal/generated/", "# Generated, as docs/runbook.md#steps says (D-01)."),
		fxGitleaks: textLines(
			`title = "fixture"`,
			"# Fixtures are generated (D-01).",
			"[allowlist]",
			"paths = ['''internal/gapi/testdata/'''] # synthetic, as D-01 says",
		),
		"go.mod": textLines("module example.com/fixture", "", "go 1.21"),
		// The commands are the dispatch's own map, read in process, so
		// the fixture's gates directory needs only to exist.
		fxGates: textLines("package main"),
		fxClient: textLines(
			"package gapi",
			"",
			"// Retries back off (D-01), as measured live.",
			"// A redirect is followed once, as RFC 9110 §15.4 allows.",
			`func Retry() string { return "D-99 and §4 in a string are data" }`,
			"",
			"// Uploads are capped. Checked by the live",
			`// step "a file uploads", and by the live steps "a repeat delete`,
			`// reports nothing" and "the per-user rate limit".`,
			"func Upload() {}",
			"",
			"// Drafts go for good. Checked by the live steps",
			`// "delete a draft", "the rules read back" and "a file uploads".`,
			"func Delete() {}",
			"",
			`// Writes compare checkpoints. Checked by the live steps "a checkpoint goes stale"`,
			`// and "read back as written: A1", and by the live step "a write (retried)".`,
			"func Write() {}",
		),
		fxClientTest: textLines(
			"package gapi",
			"",
			`import "testing"`,
			"",
			"func TestRetry(t *testing.T) { _ = Retry() }",
			"",
			"func FuzzRetry(f *testing.F) { f.Fuzz(func(*testing.T, string) { _ = Retry() }) }",
			"",
			"func BenchmarkRetry(b *testing.B) { _ = Retry() }",
			"",
			"func ExampleRetry() { _ = Retry() }",
		),
		// Two steps by key, one by a function that takes its name second,
		// one by a function value a table's method builds, two by methods
		// whose name joins a literal to an argument, one of them holding a
		// function value that returns a step of its own, and one run alone;
		// and three things that are not live steps: a step named from a
		// variable, a table of another type and a test's fake. The driver
		// builds only under its tag.
		fxDriver: textLines(
			"//go:build live",
			"",
			"package main",
			"",
			"type step struct{ name, tool string }",
			"",
			"type driver struct{}",
			"",
			"func deleteStep(tool, name string) step { return step{name: name, tool: tool} }",
			"",
			"func (d *driver) readsBack(want string) step { return step{name: \"read back as written: \" + want} }",
			"",
			"func (d *driver) run(s step) {}",
			"",
			"func (d *driver) retried(what string) step {",
			`	fallback := func(why string) step { return step{name: "a fallback: " + why} }`,
			`	d.run(fallback("the first try failed"))`,
			`	return step{name: what + " (retried)"}`,
			"}",
			"",
			"func (d *driver) steps() []step {",
			"	listed := func(name string, field string) step { return step{name: name, tool: field} }",
			`	cell := "B2"`,
			"	return []step{",
			`		{name: "a file uploads", tool: "upload_file"},`,
			`		{tool: "delete_comment", name: "a repeat delete reports nothing"},`,
			`		deleteStep("delete_sheet", "delete a draft"),`,
			`		listed("the rules read back", "manage_pivot_table"),`,
			`		d.readsBack("A1"),`,
			"		d.readsBack(cell),",
			`		d.retried("a write"),`,
			"	}",
			"}",
			"",
			"func (d *driver) checkpoints() {",
			`	d.run(step{name: "a checkpoint goes stale", tool: "write_values"})`,
			"}",
			"",
			`var probes = []struct{ name, query string }{{name: "a probe, not a step", query: "q"}}`,
		),
		fxDriverStub: textLines(
			"//go:build !live",
			"",
			"package main",
			"",
			"func main() {}",
		),
		fxDriverTest: textLines(
			"//go:build live",
			"",
			"package main",
			"",
			`var testSteps = []step{{name: "a test-only step"}}`,
		),
		fxSpikes: textLines(
			"//go:build live",
			"",
			"package main",
			"",
			"type spike struct {",
			"	name string",
			"	run  func()",
			"}",
			"",
			"var spikes = []spike{",
			`	{name: "the per-user rate limit", run: func() {}},`,
			"}",
		),
		"internal/gapi/testdata/pg.md": textLines("A fixture page citing E-777 and §5, and `internal/gone`."),
		fxServer:                       textLines("package main", "", "func main() {}"),
		fxRecord: textLines(
			"# api\tkind\tname\tverdict\treason",
			"sheets\trequest\tAddBandingRequest\tout\tbanding is a format the server reads but never writes (D-01)",
		),
		fxFields: textLines(
			"# schema\tproperty\tverdict\treason",
			"Spreadsheet\tdataSourceSchedules\tout\trefresh schedules stay in the Sheets UI (D-03)",
		),
		fxChangelog: textLines(
			"# Changelog",
			"",
			"## [Unreleased]",
			"",
			"- Retries back off (D-01), as `docs/architecture.md#codemap` says.",
			"",
			"## [1.0.0]",
			"",
			"- Cites D-99 and `internal/gone`, true when written.",
			"",
			"[Unreleased]: https://example.com/compare/v1.0.0...HEAD",
			"[1.0.0]: https://example.com/releases/v1.0.0",
		),
		fxArch: textLines(
			"# Architecture",
			"",
			"## Codemap",
			"",
			"- `internal/gapi` owns the REST client, in `internal/gapi/client.go`.",
			"- `cmd/server` is the binary, and `scripts/livesheet` drives it live.",
			"",
			"## Decisions",
			"",
			"| ID | Decision | Status | Held by |",
			"| --- | --- | --- | --- |",
			fxRow01,
			fxRow02,
			fxRow03,
			"",
			"### D-01 Retries back off",
			"",
			"Google answers a burst with 429, so a retry waits.",
			"",
			"```markdown",
			"### D-09 An example entry, not a decision",
			"It has no Rejected or Cost line.",
			"```",
			"",
			"Rejected: retrying at once, which meets the same 429.",
			"Cost: a burst takes longer to drain.",
			"",
			"### D-02 Retry forever",
			"",
			"Rejected: giving up, which drops the write.",
			"Cost: a write can wait forever.",
			"",
			"### D-03 Retry three times",
			"",
			"Three tries replace D-02, since a write that waits forever never reports.",
			"",
			"Rejected: retrying forever.",
			"Cost: a fourth 429 fails the call.",
			"",
			"Next ID: D-04",
		),
		fxRunbook: textLines(
			"# Runbook",
			"",
			"## Steps",
			"",
			"See [the codemap](architecture.md#codemap) and [the readme](../README.md).",
			"The retry is in `internal/gapi/client.go`, linked as [the client](../internal/gapi/client.go).",
			"",
			"## Steps",
			"",
			"Follow the old runbook, in [the server](/cmd/server).",
			"",
			"Back to [the first steps](#steps).",
			"",
			"[map]: architecture.md#codemap",
		),
		fxReadme: textLines(
			"# Fixture",
			"",
			"Read the [decisions](docs/architecture.md#decisions) and [the second steps](docs/runbook.md#steps-1).",
			"Code cites `docs/architecture.md#d-01-retries-back-off`.",
			"A redirect is followed once, as RFC 9110 §15.4 allows.",
			"The [spec](https://example.com/spec#§2) says so too.",
			"",
			"```text",
			"§3, D-99 and [a link](nowhere.md) in a fence are an example.",
			"```",
		),
		fxAgent: textLines(
			"# CLAUDE.md",
			"",
			"Read `docs/architecture.md` and `docs/architecture.md#codemap` first.",
			"The client is in `internal/gapi/`, and `go.mod` names the module.",
			"Run `make check`, or `go run ./scripts/gates docs` alone; `scripts/gates smoke` needs a binary.",
			"A made-up id says so: `spreadsheets/AAAAsheet1`. Streams are named in `main`, never in `cmd/*/x.go`.",
		),
	}
}

// docsEdit changes one fixture file: old replaced by new, the whole file
// written when old is empty, or the file removed when both are.
type docsEdit struct{ file, old, new string }

func replace(file, old, new string) docsEdit { return docsEdit{file, old, new} }
func add(file, body string) docsEdit         { return docsEdit{file, "", body} }
func drop(file string) docsEdit              { return docsEdit{file: file} }

// docsEdited is the fixture with the edits applied. An edit whose old
// text is not there fails the test, so a fixture change cannot quietly
// turn a reject row into a copy of the accepted checkout.
func docsEdited(t *testing.T, edits ...docsEdit) map[string]string {
	t.Helper()
	return applyEdits(t, docsFixture(), edits...)
}

// applyEdits is files with the edits applied, as docsEdited applies them.
func applyEdits(t *testing.T, files map[string]string, edits ...docsEdit) map[string]string {
	t.Helper()
	for _, e := range edits {
		switch body := files[e.file]; {
		case e.old == "" && e.new == "":
			delete(files, e.file)
		case e.old == "":
			files[e.file] = e.new
		case !strings.Contains(body, e.old):
			t.Fatalf("the fixture's %s does not contain %q", e.file, e.old)
		default:
			files[e.file] = strings.Replace(body, e.old, e.new, 1)
		}
	}
	return files
}

// codemapNames adds a line naming pkg to the fixture's codemap, for a
// row that adds a package.
func codemapNames(pkg string) docsEdit {
	return replace(fxArch, "drives it live.\n", "drives it live.\n- `"+pkg+"` is a package.\n")
}

// docsTree writes files to a fresh directory and returns it.
func docsTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		put(t, root, name, body)
	}
	return root
}

// isolateGit keeps the machine's git configuration from every git the
// test runs, the gate's own included, so a global ignore file cannot hide
// a fixture file, and turns automatic maintenance off as gitNoMaintenance
// does.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, kv := range gitNoMaintenance {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

// gitNoMaintenance turns automatic maintenance off for every git a test
// runs, isolated or through gitIn. A commit starts it detached, and it
// holds .git/objects/maintenance.lock after git returns, so a copy of the
// repository races it, and so does t.TempDir's cleanup
// (https://github.com/git/git/blob/v2.51.0/builtin/gc.c#L1663).
var gitNoMaintenance = []string{
	// Command-scope pairs, which every config file yields to:
	// https://git-scm.com/docs/git-config#Documentation/git-config.txt-GITCONFIGCOUNT
	"GIT_CONFIG_COUNT=2",
	// No command starts git maintenance run --auto after a write:
	// https://git-scm.com/docs/git-maintenance#Documentation/git-maintenance.txt-maintenanceauto
	"GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false",
	// A porcelain command that runs git gc --auto itself finds no work:
	// https://git-scm.com/docs/git-gc#Documentation/git-gc.txt-gcauto
	"GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0",
}

// A fixture's git leaves nothing running when it returns, as the gate's own
// git runs under isolateGit and through gitIn alone: a commit starts no
// maintenance, and a git gc --auto with more packs than its limit packs
// nothing. gitIn adds the pairs itself, so the isolated case runs the gate's
// git, which takes only the environment.
func TestIsolatedGitRunsNoAutomaticMaintenance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		isolated bool
	}{{"the gate's git under isolateGit", true}, {"through gitIn alone", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, trace := t.TempDir(), filepath.Join(t.TempDir(), "trace")
			run := func(args ...string) { gitIn(t, dir, args...) }
			if tc.isolated {
				isolateGit(t)
				run = func(args ...string) {
					t.Helper()
					if _, err := git(append([]string{"-C", dir}, args...)...); err != nil {
						t.Fatal(err)
					}
				}
			}
			initRepo(t, dir)
			t.Setenv("GIT_TRACE", trace)
			for _, msg := range []string{"first", "second"} {
				run("commit", "--allow-empty", "-q", "-m", msg)
				run("repack", "-q")
			}
			run("-c", "gc.autoPackLimit=1", "-c", "gc.autoDetach=false", "gc", "--auto", "--quiet")
			out, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			// Before git 2.29 a commit starts git gc --auto itself, in the
			// foreground, and gc.auto=0 returns it before it can detach
			// (https://github.com/git/git/blob/v2.28.0/builtin/gc.c#L343):
			// the pack count below holds that, so the trace may show it.
			if !strings.Contains(string(out), "built-in: git commit") {
				t.Fatalf("GIT_TRACE wrote %q, which traces no commit", out)
			} else if strings.Contains(string(out), "git maintenance run") {
				t.Errorf("GIT_TRACE wrote %q, want no git maintenance run started", out)
			}
			packs, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.pack"))
			if err != nil {
				t.Fatal(err)
			}
			if len(packs) != 2 {
				t.Errorf("git gc --auto over 2 packs with gc.autoPackLimit=1 left %d, want the 2 untouched", len(packs))
			}
		})
	}
}

// docsFixtureFloors keeps the floors out of the rows whose subject is not a
// floor. The fixture is far smaller than the repository, and a pass that
// reads nothing still fails.
var docsFixtureFloors = docsFloors{steps: 1, spikes: 1, links: 1, spanPaths: 1, packages: 1}

// docsRun runs every check over files, listed as git lists them, held to
// floors. Listing a checkout is docsFiles' part, and the tests that need
// git test it.
func docsRun(t *testing.T, files map[string]string, floors docsFloors) *docsCheck {
	t.Helper()
	g := newDocsCheck(docsTree(t, files), slices.Sorted(maps.Keys(files)), floors)
	g.run()
	return g
}

// docsCase is one checkout and every problem the gate reports for it. A
// nil want means the gate accepts it.
type docsCase struct {
	name  string
	edits []docsEdit
	want  []string
}

// checkDocsCases runs each row against its own checkout, in parallel,
// and compares the whole problem list, so a row also fails on a problem
// it did not expect.
func checkDocsCases(t *testing.T, cases []docsCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDocs(t, docsFixtureFloors, tc.edits, tc.want)
		})
	}
}

// checkDocs runs the checks, held to floors, over the fixture with edits,
// and wants exactly the problems want, or none when want is nil.
func checkDocs(t *testing.T, floors docsFloors, edits []docsEdit, want []string) {
	t.Helper()
	if got := docsRun(t, docsEdited(t, edits...), floors).problems; !slices.Equal(got, want) {
		t.Errorf("problems\n got %q\nwant %q", got, want)
	}
}

func TestDocsGateAcceptsAConsistentCheckout(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{name: "the fixture as it stands"},
		{"a Go file under testdata is neither parsed nor read for citations", []docsEdit{
			add("internal/gapi/testdata/fixture.go", "package fixture\n\n// Cites D-99.\nfunc {\n"),
		}, nil},
		{"footnotes, and a link out of the repository", []docsEdit{
			replace(fxReadme, "says so too.", "says so too.[^spec] See [issue 51](../../issues/51)."),
			replace(fxReadme, "```text", "[^spec]: The spec's own words.\n\n```text"),
		}, nil},
		{"a nested doc's anchor in a Go comment, and nested names that are not references", []docsEdit{
			add("scripts/livesheet/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live; ./scripts/livesheet/NOTES.md#setup says how.\n"+
				"// Each run writes out/summary.md, as https://example.com/x/blob/main/MANIFEST.md says."),
		}, nil},
		{"a doc in a Go comment read from the file's directory, up and out of it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live; testdata/pg.md, ../../README.md#fixture and ../../../outside.md say so."),
		}, nil},
		{"a doc path inside a URL or a query string in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, at https://pkg.go.dev/example.com/fixture@v1.0.0/RUNBOOK.md#setup\n"+
				"// and at /render?page=docs/gone.md."),
		}, nil},
		{"a root doc's anchor, and a lowercase file name, in a Go comment", []docsEdit{
			replace(fxClient, "§15.4 allows.", "§15.4 allows, as README.md#fixture says.\n// Each retry is logged to retries.md."),
		}, nil},
		{"a doc path in a string of a test-helper package, which is fixture content", []docsEdit{
			codemapNames("internal/gapi/sheetstest"),
			add("internal/gapi/sheetstest/fake.go", textLines(
				"package sheetstest",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, nil},
		{"a bare file name in a Go string, and a doc path in a test's string", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, §4 and NOTES.md"),
			replace(fxClientTest, "_ = Retry()", `_ = Retry() + "docs/gone.md"`),
		}, nil},
		{"an escaped pipe in an index cell, unescaped to match the entry", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a \\| b` times |"),
			replace(fxArch, "### D-03 Retry three times", "### D-03 Retry `a | b` times"),
		}, nil},
		{"a link written in a code span", []docsEdit{
			replace(fxReadme, "says so too.", "says so too. Write `[x](docs/example.md#slug)` for a link."),
		}, nil},
		{"a placeholder or a wildcard where a make target, a gates command or a path goes", []docsEdit{
			replace(fxAgent, "needs a binary.", "needs a binary; `make <target>` and `go run ./scripts/gates <name>` run one,\n"+
				"`make ...` and `scripts/gates ...` run more, and `go test ./internal/gone/...` tests them."),
		}, nil},
		{"a manual gates command", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke` needs a binary.", "`scripts/gates smoke` needs a binary and `scripts/gates api-diff` a network."),
			replace(fxArch, "| forbidigo, lint, review |", "| forbidigo, api-diff, review |"),
		}, nil},
		{"a workflow by its bare name, and an archive's file, which is not a path here", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `ci.yml` checks it and `checksums.txt` lists an archive"),
		}, nil},
		{"a bare file name in a nested doc, read from its directory, then the root, then as a workflow", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemap`, `go.mod` and `ci.yml` say, the retry is in"),
		}, nil},
		{"a generated table under a package's testdata, which is not a record", []docsEdit{
			add("internal/gapi/testdata/golden.tsv", textLines("method\tnote", "values.get\tD-99 and E-12")),
		}, nil},
		{"what a command writes beside the records: a snapshot, the baseline and a golden file", []docsEdit{
			add("testdata/api-fields.json", textLines(`{"note": "D-99, §4 and E-12"}`)),
			add("testdata/schema-baseline.json", textLines(`{"note": "D-98"}`)),
			add("testdata/golden/card.txt", textLines("D-97 and §5")),
		}, nil},
		{"IDs in a YAML or TOML value, after a # with no space before it, and in a Makefile recipe", []docsEdit{
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      - run: echo D-99 §4 https://example.com/#E-12",
			)),
			replace(fxGitleaks, `title = "fixture"`, `title = "D-99#E-12"`),
			replace(fxMakefile, "\tgolangci-lint run", "\tgolangci-lint run\n\t@echo D-99"),
		}, nil},
	})
}

// What a pass reads is in its summary, so "found nothing" and "looked at
// nothing" cannot read the same. A failure counts its problems and prints
// nothing.
func TestDocsGateSaysWhatItRead(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := docsRun(t, docsFixture(), docsFixtureFloors).verdict(&out); err != nil {
		t.Fatalf("the fixture failed: %v", err)
	}
	want := "3 decisions, 2 packages, 8 live steps, 8 links and 14 code-span paths, in 21 files\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}

	out.Reset()
	edited := docsEdited(t, replace(fxClient, "(D-01)", "(D-07)"), replace(fxReadme, "says so too.", "says so too, as D-08 did."))
	err := docsRun(t, edited, docsFixtureFloors).verdict(&out)
	want = "2 problem(s):\n  README.md:6: cites D-08, which is not a decision\n  internal/gapi/client.go:3: cites D-07, which is not a decision"
	if err == nil || err.Error() != want || out.Len() != 0 {
		t.Errorf("failed with %v after printing %q, want %q and nothing printed", err, out.String(), want)
	}
}

// The gate reads what git would commit, as it is on disk: an untracked
// doc is read and an ignored file is not. A file deleted in the worktree
// stays in the index until the deletion is staged, and has nothing to
// read.
func TestDocsGateReadsWhatGitWouldCommit(t *testing.T) {
	isolateGit(t)
	root := docsTree(t, docsEdited(t,
		add("internal/old/old.go", "package old\n"),
		add("internal/generated/broken.go", "this is not Go\n"),
	))
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	if err := os.Remove(filepath.Join(root, "internal", "old", "old.go")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "docs/new.md", "A new page cites D-07.\n")
	err := docsGateAt(root, docsFixtureFloors, io.Discard)
	if want := "1 problem(s):\n  docs/new.md:1: cites D-07, which is not a decision"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestDocsGateHoldsDecisionIntegrity(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"an ID in the index twice", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n"+fxRow03),
		}, []string{"docs/architecture.md:15: D-03 is in the index twice"}},
		{"an ID with two entries", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-03 Retry three times\n\nRejected: none.\nCost: none.\n\nNext ID: D-04"),
		}, []string{"docs/architecture.md:40: D-03 has two entries"}},
		{"an ID at Next ID", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-03"),
		}, []string{"docs/architecture.md:14: D-03 is not below Next ID D-03"}},
		{"an ID below Next ID that is gone", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 is below Next ID D-05 and is missing; an ID is never deleted"}},
		{"no Next ID line", []docsEdit{
			replace(fxArch, "\n\nNext ID: D-04", ""),
		}, []string{`docs/architecture.md:38: the last line is not "Next ID: D-NN"`}},
		{"a status outside the set", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off | accepted |", "| D-01 | Retries back off | proposed |"),
		}, []string{`docs/architecture.md:12: D-01 has status "proposed", want accepted, rejected, deprecated or superseded by D-NN`}},
		{"a superseded status that names no decision ID", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-3 |"),
		}, []string{
			`docs/architecture.md:13: D-02 has status "superseded by D-3", want accepted, rejected, deprecated or superseded by D-NN`,
			`docs/architecture.md:13: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a superseded status naming a decision that does not exist", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-07 |"),
		}, []string{"docs/architecture.md:13: cites D-07, which is not a decision"}},
		{"an entry titled unlike its index row", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off |", "| D-01 | Retries back off fast |"),
		}, []string{`docs/architecture.md:16: D-01's entry is titled "Retries back off" and its index row "Retries back off fast"`}},
		{"an entry with no Rejected line", []docsEdit{
			replace(fxArch, "Rejected: giving up, which drops the write.\n", ""),
		}, []string{`docs/architecture.md:28: D-02 has no "Rejected:" line`}},
		{"an entry with no Cost line", []docsEdit{
			replace(fxArch, "Cost: a fourth 429 fails the call.\n", ""),
		}, []string{`docs/architecture.md:33: D-03 has no "Cost:" line`}},
		{"an index row with no entry", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | accepted | live |"),
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:15: D-04 has no entry"}},
		{"an entry with no index row", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 has no index row"}},
		{"an ID with one digit, which the citation scan also reads, reported once", []docsEdit{
			replace(fxArch, "| D-03 | Retry three", "| D-3 | Retry three"),
		}, []string{
			`docs/architecture.md:14: "D-3" is not written as a decision ID like D-03`,
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"a pipe left unescaped in an index cell, even in a code span", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a | b` times |"),
		}, []string{
			"docs/architecture.md:14: an index row has 5 cells, want 4",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index with a column too many", []docsEdit{
			replace(fxArch, "| Held by |\n| --- | --- | --- | --- |", "| Held by | Links |\n| --- | --- | --- | --- | --- |"),
		}, []string{
			`docs/architecture.md:10: the decision index has the columns ["ID" "Decision" "Status" "Held by" "Links"], ` +
				`want ["ID" "Decision" "Status" "Held by"]`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:16: D-01 has no index row",
			"docs/architecture.md:28: D-02 has no index row",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index that is not directly under its heading", []docsEdit{
			replace(fxArch, "## Decisions\n\n|", "## Decisions\n\nThe index.\n\n|"),
		}, []string{
			`docs/architecture.md:8: the decision index is not a table directly under "## Decisions"`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:18: D-01 has no index row",
			"docs/architecture.md:30: D-02 has no index row",
			"docs/architecture.md:35: D-03 has no index row",
		}},
	})
}

func TestDocsGateHoldsHeldByToWhatExists(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a fuzz target, whose seeds go test runs", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| FuzzRetry, smoke |"),
		}, nil},
		{"a test function that does not exist", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetryLater, smoke |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "TestRetryLater", ` + notAHolder}},
		{"a benchmark or an example, which go test does not run by name", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| BenchmarkRetry, ExampleRetry, smoke |"),
		}, []string{
			`docs/architecture.md:12: D-01 is held by "BenchmarkRetry", ` + notAHolder,
			`docs/architecture.md:12: D-01 is held by "ExampleRetry", ` + notAHolder,
		}},
		{"a gates command that is not dispatched", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, smok |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "smok", ` + notAHolder}},
		{"a linter configured but not enabled", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| misspell, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "misspell", ` + notAHolder}},
		{"a linter enabled only inside another linter's settings", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| nilness, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "nilness", ` + notAHolder}},
		{"an accepted decision held by a dash", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| — |"),
		}, []string{"docs/architecture.md:14: D-03 is accepted and held by —; an accepted decision names what holds it"}},
		{"a decision held by nothing", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| |"),
		}, []string{"docs/architecture.md:12: D-01 is held by nothing"}},
		// live is the Makefile's target, so it holds only while the target
		// exists.
		{"live, while make live exists", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, live |"),
		}, nil},
		{"live, once make live is gone", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, live |"),
			replace(fxMakefile, ".PHONY: live\nlive:\n\tgo run -tags=live ./scripts/livesheet\n", ""),
		}, []string{"docs/architecture.md:12: D-01 is held by \"live\", " + notAHolder}},
		{"a linter list under keys with a comment after them", []docsEdit{
			replace(fxGolangci, "linters:\n  default: none\n  enable:", "linters: # the set\n  default: none\n  enable: # every one named"),
		}, nil},
		{"a linter list with a tab before each comment", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n",
				"  enable:\t# every one named\n    - errcheck\t# unchecked errors\n    - forbidigo\t# stdout carries only frames\n"),
		}, nil},
	})
}

// Evidence has no IDs: what was verified upstream is stated at the line
// that depends on it. An E- citation fails wherever another citation is read.
func TestDocsGateRefusesEvidenceIDs(t *testing.T) {
	const noIDs = "; evidence has no IDs: state the behavior at the line that depends on it, with its source link"
	checkDocsCases(t, []docsCase{
		{"an evidence ID in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured in E-001."),
		}, []string{"internal/gapi/client.go:3: cites E-001" + noIDs}},
		{"an evidence ID in a doc, at any width", []docsEdit{
			replace(fxArch, "Google answers a burst with 429, so", "Google answers a burst with 429 (E-12), so"),
		}, []string{"docs/architecture.md:18: cites E-12" + noIDs}},
		{"an evidence ID in a record's reason", []docsEdit{
			replace(fxRecord, "(D-01)", "(E-12)"),
		}, []string{"testdata/api-coverage.tsv:2: cites E-12" + noIDs}},
		{"an evidence ID in a fenced block", []docsEdit{
			replace(fxReadme, "§3, D-99 and", "§3, D-99, E-001 and"),
		}, nil},
		{"an evidence ID in a string literal", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, E-001 and §4"),
		}, nil},
	})
}

// A comment names the live step that checks it, and the name has to be a
// step the live driver runs or a spike the spikes ask.
func TestDocsGateHoldsLiveStepsToTheLiveRun(t *testing.T) {
	const undefined = ", which neither scripts/livesheet nor scripts/spikes defines"
	checkDocsCases(t, []docsCase{
		{"a name wrapped across comment lines", []docsEdit{
			replace(fxClient, "// reports nothing\"", "// reports something\""),
		}, []string{`internal/gapi/client.go:8: names the live step "a repeat delete reports something"` + undefined}},
		{"the second name of a list", []docsEdit{
			replace(fxClient, `and "the per-user rate limit".`, `and "a file downloads".`),
		}, []string{`internal/gapi/client.go:9: names the live step "a file downloads"` + undefined}},
		{"a step only a test builds", []docsEdit{
			replace(fxClient, `step "a file uploads", and`, `step "a test-only step", and`),
		}, []string{`internal/gapi/client.go:8: names the live step "a test-only step"` + undefined}},
		{"a row of a table of another type", []docsEdit{
			replace(fxClient, `step "a file uploads", and`, `step "a probe, not a step", and`),
		}, []string{`internal/gapi/client.go:8: names the live step "a probe, not a step"` + undefined}},
		{"a step named from a variable", []docsEdit{
			replace(fxClient, `"read back as written: A1"`, `"read back as written: B2"`),
		}, []string{`internal/gapi/client.go:17: names the live step "read back as written: B2"` + undefined}},
		{"a step run alone, renamed", []docsEdit{
			replace(fxDriver, `"a checkpoint goes stale"`, `"a checkpoint is stale"`),
		}, []string{`internal/gapi/client.go:16: names the live step "a checkpoint goes stale"` + undefined}},
		{"an argument of a step's function that is not its name", []docsEdit{
			replace(fxClient, `// "delete a draft", "the rules`, `// "delete_sheet", "the rules`),
		}, []string{`internal/gapi/client.go:13: names the live step "delete_sheet"` + undefined}},
		{"an argument of a function value that is not its name", []docsEdit{
			replace(fxClient, `"the rules read back"`, `"manage_pivot_table"`),
		}, []string{`internal/gapi/client.go:13: names the live step "manage_pivot_table"` + undefined}},
		// A citation of a name two steps share could mean either.
		{"two steps of one name, and a spike of a step's name", []docsEdit{
			replace(fxDriver, `		deleteStep("delete_sheet", "delete a draft"),`,
				`		deleteStep("delete_sheet", "delete a draft"),`+"\n"+`		{name: "a file uploads", tool: "upload_file"},`),
			replace(fxSpikes, `	{name: "the per-user rate limit", run: func() {}},`,
				`	{name: "the per-user rate limit", run: func() {}},`+"\n"+`	{name: "delete a draft", run: func() {}},`),
		}, []string{
			`scripts/livesheet/main.go:28: names the live step "a file uploads", which scripts/livesheet/main.go:25 names too; a step's name is its own`,
			`scripts/spikes/main.go:12: names the live step "delete a draft", which scripts/livesheet/main.go:27 names too; a step's name is its own`,
		}},
		// Prose, a record's reason and a config comment cite a live step as
		// a Go comment does; a placeholder shows the form and names none.
		{"a paragraph across a line break, a real step, a placeholder and a fenced example", []docsEdit{
			replace(fxRunbook, "Back to [the first steps](#steps).", "Back to [the first steps](#steps). Checked by the live\n"+
				`step "a file uploads", and by the live step "no such`+"\n"+`step". Written `+"`"+`the live step "<name>"`+"`.\n\n"+
				"```text\n"+`the live step "a fenced example"`+"\n```"),
		}, []string{`docs/runbook.md:13: names the live step "no such step"` + undefined}},
		{"a step under [Unreleased], and one in a released section", []docsEdit{
			replace(fxChangelog, "#codemap` says.", "#codemap` says, as the live step \"gone now\" checks."),
			replace(fxChangelog, "true when written.", "true when written, as the live step \"gone then\" checked."),
		}, []string{`CHANGELOG.md:5: names the live step "gone now"` + undefined}},
		{"a record's reason", []docsEdit{
			replace(fxRecord, "never writes (D-01)", "never writes (D-01), as the live step \"no such step\" shows"),
		}, []string{`testdata/api-coverage.tsv:2: names the live step "no such step"` + undefined}},
		{"config comments, on a line of their own and after a value", []docsEdit{
			replace(fxMakefile, "# The checks CI runs (D-03).", "# The checks CI runs (D-03), as the live step\n# \"no such step\" shows."),
			replace(fxGolangci, "    - forbidigo # stdout carries only frames", "    - forbidigo # stdout carries frames, as the live step \"gone too\" shows"),
		}, []string{
			`.golangci.yml:6: names the live step "gone too"` + undefined,
			`Makefile:2: names the live step "no such step"` + undefined,
		}},
	})
}

// The changelog's scan reads the lines release-notes lifts, through the
// one reader both share: a fenced heading ends neither, and a heading with
// a tab after its #s ends both.
func TestDocsGateScansWhatTheReleaseNoteLifts(t *testing.T) {
	unreleased := "#codemap` says.\n"
	checkDocsCases(t, []docsCase{
		{"a heading in a fence", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n~~~markdown\n## [1.0.0]\n~~~\n\n- As D-07 says.\n"),
		}, []string{"CHANGELOG.md:11: cites D-07, which is not a decision"}},
		{"a heading with a tab", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n##\tLater\n\n- As D-07 says.\n"),
		}, nil},
	})
}

// A code span or a live step under [Unreleased]'s Removed heading names
// what the release takes away; a decision and a link there still resolve.
func TestDocsGateLetsTheRemovedSectionNameWhatIsGone(t *testing.T) {
	const undefined = ", which neither scripts/livesheet nor scripts/spikes defines"
	unreleased := "#codemap` says.\n"
	checkDocsCases(t, []docsCase{
		{"a removed path, target and live step", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- `internal/gone`, `make gone` and the live step \"gone now\".\n"),
		}, nil},
		{"a level-4 heading inside Removed keeps it", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n#### Tools\n\n- `internal/gone`.\n"),
		}, nil},
		{"a decision and a link under Removed", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- D-07, and [the page](docs/gone.md).\n"),
		}, []string{
			"CHANGELOG.md:9: cites D-07, which is not a decision",
			"CHANGELOG.md:9: links to docs/gone.md, which does not exist",
		}},
		{"the heading after Removed ends it", []docsEdit{
			replace(fxChangelog, unreleased, unreleased+"\n### Removed\n\n- `internal/gone`.\n\n### Fixed\n\n- `internal/lost` and the live step \"lost\".\n"),
		}, []string{
			"CHANGELOG.md:13: names internal/lost, which does not exist",
			`CHANGELOG.md:13: names the live step "lost"` + undefined,
		}},
		{"a Removed heading outside the changelog", []docsEdit{
			replace(fxRunbook, "Back to [the first steps](#steps).", "### Removed\n\n`internal/gone`."),
		}, []string{"docs/runbook.md:14: names internal/gone, which does not exist"}},
	})
}

func TestDocsGateResolvesCitations(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a superseded decision cited from a Go comment", []docsEdit{
			replace(fxClient, "(D-01)", "(D-02)"),
		}, []string{"internal/gapi/client.go:3: cites D-02, which is superseded by D-03"}},
		{"a deprecated decision cited from a doc", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | deprecated | — |"),
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
			replace(fxReadme, "#d-01-retries-back-off`.", "#d-01-retries-back-off`. Logging was D-04."),
		}, []string{"README.md:4: cites D-04, which is deprecated"}},
		{"an unknown decision cited twice on a line, reported once", []docsEdit{
			replace(fxClient, "(D-01)", "(D-07, and D-07 again)"),
		}, []string{"internal/gapi/client.go:3: cites D-07, which is not a decision"}},
		{"an unknown decision in each record's reason", []docsEdit{
			replace(fxRecord, "(D-01)", "(D-07)"),
			replace(fxFields, "(D-03)", "(D-05), as §4 says"),
		}, []string{
			"testdata/api-coverage.tsv:2: cites D-07, which is not a decision",
			"testdata/api-fields.tsv:2: cites D-05, which is not a decision",
			"testdata/api-fields.tsv:2: cites a section by number (§4); " + bySlug,
		}},
		{"a link slug no heading has", []docsEdit{
			replace(fxReadme, "docs/architecture.md#decisions", "docs/architecture.md#decision"),
		}, []string{"README.md:3: links to docs/architecture.md#decision, and docs/architecture.md has no heading with that slug"}},
		{"a code span slug no heading has", []docsEdit{
			replace(fxReadme, "#d-01-retries-back-off`", "#d-01-retry`"),
		}, []string{"README.md:4: names docs/architecture.md#d-01-retry, and docs/architecture.md has no heading with that slug"}},
		{"a repeated heading's suffix past the last repeat", []docsEdit{
			replace(fxReadme, "#steps-1", "#steps-2"),
		}, []string{"README.md:3: links to docs/runbook.md#steps-2, and docs/runbook.md has no heading with that slug"}},
		{"a same-file anchor no heading has", []docsEdit{
			replace(fxRunbook, "(#steps)", "(#step)"),
		}, []string{"docs/runbook.md:12: links to docs/runbook.md#step, and docs/runbook.md has no heading with that slug"}},
		{"a reference definition no heading answers", []docsEdit{
			replace(fxRunbook, "[map]: architecture.md#codemap", "[map]: architecture.md#codemaps"),
		}, []string{"docs/runbook.md:14: links to docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug"}},
		{"a link to a doc that does not exist", []docsEdit{
			replace(fxReadme, "says so too.", "says so too, in [the guide](docs/gone.md)."),
		}, []string{"README.md:6: links to docs/gone.md, which does not exist"}},
		{"a link written from the root instead of the linking file", []docsEdit{
			replace(fxRunbook, "](architecture.md#codemap)", "](docs/architecture.md#codemap)"),
		}, []string{"docs/runbook.md:5: links to docs/docs/architecture.md, which does not exist"}},
		{"a docs citation in a Go comment to a file that does not exist", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live and in docs/retries.md#backoff."),
		}, []string{"internal/gapi/client.go:3: cites docs/retries.md, which does not exist"}},
		{"a bare section number in a Go comment", []docsEdit{
			replace(fxClient, "as RFC 9110 §15.4 allows", "as §15.4 allows"),
		}, []string{"internal/gapi/client.go:4: cites a section by number (§15); " + bySlug}},
		{"a section number not right after an RFC or inside a URL on its line", []docsEdit{
			replace(fxClient, "RFC 9110 §15.4 allows", "RFC 9110 allows and §15.4 says"),
			replace(fxReadme, "RFC 9110 §15.4", "RFC 9110  §15.4"),
			replace(fxReadme, "spec#§2) says", "spec) §2 says"),
		}, []string{
			"README.md:5: cites a section by number (§15); " + bySlug,
			"README.md:6: cites a section by number (§2); " + bySlug,
			"internal/gapi/client.go:4: cites a section by number (§15); " + bySlug,
		}},
		{"a bare section number in the changelog's Unreleased section", []docsEdit{
			replace(fxChangelog, "#codemap` says.", "#codemap` and §3 say."),
		}, []string{"CHANGELOG.md:5: cites a section by number (§3); " + bySlug}},
		{"a bare section number in a released changelog section", []docsEdit{
			replace(fxChangelog, "Cites D-99 and", "Cites D-99, §3 and"),
		}, nil},
		{"IDs written at the wrong width, in a Go comment and a doc", []docsEdit{
			replace(fxClient, "(D-01)", "(D-1)"),
			replace(fxReadme, "says so too.", "says so too, as D-001 did."),
		}, []string{
			`README.md:6: "D-001" is not written as a decision ID like D-01`,
			`internal/gapi/client.go:3: "D-1" is not written as a decision ID like D-01`,
		}},
		{"a one-line code span in fence backticks opens no fence", []docsEdit{
			replace(fxReadme, "says so too.\n", "says so too.\n```go``` is a span, not a fence.\nAs D-07 said.\n"),
		}, []string{"README.md:8: cites D-07, which is not a decision"}},
		{"a nested doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			add("scripts/livesheet/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live, packaging/README.md and scripts/livesheet/NOTES.md#teardown."),
		}, []string{
			"internal/gapi/client.go:3: cites packaging/README.md, which does not exist",
			"internal/gapi/client.go:3: cites scripts/livesheet/NOTES.md#teardown, and scripts/livesheet/NOTES.md has no heading with that slug",
		}},
		{"a doc in a Go comment missing from the file's directory, or up from it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, testdata/gone.md and ../gone.md."),
		}, []string{
			"internal/gapi/client.go:3: cites internal/gapi/testdata/gone.md, which does not exist",
			"internal/gapi/client.go:3: cites internal/gone.md, which does not exist",
		}},
		{"a root doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, RUNBOOK.md and README.md#retries."),
		}, []string{
			"internal/gapi/client.go:3: cites RUNBOOK.md, which does not exist",
			"internal/gapi/client.go:3: cites README.md#retries, and README.md has no heading with that slug",
		}},
		{"doc paths in a Go string that do not resolve", []docsEdit{
			replace(fxClient, "in a string are data", "in a string are data; docs/setup.md and README.md#setup are not"),
		}, []string{
			"internal/gapi/client.go:5: names docs/setup.md, which does not exist",
			"internal/gapi/client.go:5: names README.md#setup, and README.md has no heading with that slug",
		}},
		{"a doc path in an ordinary package's string, and in a test-helper package's comment", []docsEdit{
			codemapNames("internal/gapi/gapiutil"),
			codemapNames("internal/gapi/sheetstest"),
			add("internal/gapi/gapiutil/fake.go", textLines(
				"package gapiutil",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
			add("internal/gapi/sheetstest/fake.go", textLines(
				"package sheetstest",
				"",
				"// The fake's files, as docs/fake.md lists them.",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, []string{
			"internal/gapi/gapiutil/fake.go:3: names docs/guide.md, which does not exist",
			"internal/gapi/sheetstest/fake.go:3: cites docs/fake.md, which does not exist",
		}},
		{"a comment line in each kind of config file", []docsEdit{
			replace(fxMakefile, "(D-03)", "(D-3)"),
			replace(fxGitignore, "#steps says", "#stairs says"),
			replace(fxGitleaks, "generated (D-01)", "generated (D-07)"),
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      # Pinned, as E-12 found.",
				"      - run: go test ./...",
			)),
			add(".goreleaser.yaml", textLines("version: 2", "# Archives follow §3.")),
		}, []string{
			".github/workflows/ci.yml:5: cites E-12; evidence has no IDs: state the behavior at the line that depends on it, with its source link",
			".gitignore:2: cites docs/runbook.md#stairs, and docs/runbook.md has no heading with that slug",
			".gitleaks.toml:2: cites D-07, which is not a decision",
			".goreleaser.yaml:2: cites a section by number (§3); " + bySlug,
			`Makefile:1: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a trailing comment on a YAML or TOML line", []docsEdit{
			replace(fxGolangci, "as D-01 says", "as D-07 says"),
			replace(fxGitleaks, "synthetic, as D-01 says", "synthetic, as D-08 says"),
		}, []string{
			".gitleaks.toml:4: cites D-08, which is not a decision",
			".golangci.yml:12: cites D-07, which is not a decision",
		}},
		{"a TOML multi-line string's # is text, and a comment after it is read", []docsEdit{
			replace(fxGitleaks, "as D-01 says\n", "as D-01 says\n"+textLines(
				"description = '''",
				"# D-99 is text in a literal string,",
				"'''",
				`note = """`,
				"as is a # before D-97 in a basic one.",
				`""" # as D-08 says`,
			)),
		}, []string{".gitleaks.toml:10: cites D-08, which is not a decision"}},
	})
}

func TestDocsGateHoldsPathsToTheRepository(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"a path in CLAUDE.md that does not exist", []docsEdit{
			replace(fxAgent, "`internal/gapi/`", "`internal/api/`"),
		}, []string{"CLAUDE.md:4: names internal/api/, which does not exist"}},
		{"a path in another doc that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi/retry.go`"),
		}, []string{"docs/runbook.md:6: names internal/gapi/retry.go, which does not exist"}},
		{"a package in the codemap that does not exist", []docsEdit{
			replace(fxArch, "drives it live.", "drives it live.\n- `internal/gone` was removed."),
		}, []string{"docs/architecture.md:7: names internal/gone, which does not exist"}},
		{"a link to a file that does not exist", []docsEdit{
			replace(fxRunbook, "(../internal/gapi/client.go)", "(../internal/gapi/retry.go)"),
		}, []string{"docs/runbook.md:6: links to internal/gapi/retry.go, which does not exist"}},
		{"a link from the root to a directory that does not exist", []docsEdit{
			replace(fxRunbook, "(/cmd/server)", "(/cmd/client)"),
		}, []string{"docs/runbook.md:10: links to cmd/client, which does not exist"}},
		{"a file:line pointer, with punctuation after it", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`(see internal/gapi/client.go:3:5),` and `internal/gapi/client.go:3`"),
		}, nil},
		{"a file:line pointer to a file that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi/retry.go:12`"),
		}, []string{"docs/runbook.md:6: names internal/gapi/retry.go:12, which does not exist"}},
		{"a Go name, plain or qualified, in a package that exists", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/gapi.Retry` and `internal/gapi.Client.Do`"),
		}, nil},
		{"a missing file whose name less its extension is a directory", []docsEdit{
			codemapNames("internal/service/approver"),
			add("internal/service/approver/main.go", "package main\n"),
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/service/approver.go` and `scripts/livesheet.sh`"),
		}, []string{
			"docs/runbook.md:6: names internal/service/approver.go, which does not exist",
			"docs/runbook.md:6: names scripts/livesheet.sh, which does not exist",
		}},
		{"a Go name in a package that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/gapi/client.go`", "`internal/api.Retry`"),
		}, []string{"docs/runbook.md:6: names internal/api.Retry, which does not exist"}},
		{"a root file and a source directory that are gone, recognized by their shape", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `.goreleaser.yaml` the archives and `packaging/mcpb/manifest.json` the bundle"),
		}, []string{
			"CLAUDE.md:4: names .goreleaser.yaml, which does not exist",
			"CLAUDE.md:4: names packaging/mcpb/manifest.json, which does not exist",
		}},
		{"a bare workflow name no workflow has", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, and `release.yml` releases it"),
		}, []string{"CLAUDE.md:4: names release.yml, which does not exist"}},
		{"a bare file name in a nested doc, with no such heading in its directory, or found nowhere", []docsEdit{
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemaps` and `gone.md` say, the retry is in"),
		}, []string{
			"docs/runbook.md:6: names docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:6: names gone.md, which does not exist",
		}},
		{"a file named as a directory", []docsEdit{
			replace(fxAgent, "`go.mod`", "`go.mod/`"),
		}, []string{"CLAUDE.md:4: names go.mod/, which is not a directory"}},
		{"a make target that does not exist", []docsEdit{
			replace(fxAgent, "`make check`", "`make verify`"),
		}, []string{"CLAUDE.md:5: names make verify, which is not a Makefile target"}},
		{"a gates command that is not dispatched", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke`", "`scripts/gates smok`"),
		}, []string{"CLAUDE.md:5: names scripts/gates smok, which is not a gates command"}},
		{"no CLAUDE.md", []docsEdit{
			drop(fxAgent),
		}, []string{"CLAUDE.md is missing"}},
	})
}

// A gate that read nothing passes everything, so each input it depends on
// reading has a floor, and each row here sets one off.
func TestDocsGateRefusesToReadNothing(t *testing.T) {
	checkDocsCases(t, []docsCase{
		{"no Go files", []docsEdit{
			drop(fxClient), drop(fxClientTest), drop(fxServer), drop(fxDriver), drop(fxDriverStub),
			drop(fxDriverTest), drop(fxSpikes), drop(fxGates),
		}, []string{
			"no .go files read: this gate would be looking at nothing",
			"scripts/livesheet: 0 live steps read, fewer than 1: the step literals are not being read",
			"scripts/spikes: 0 live steps read, fewer than 1: the spike literals are not being read",
			`docs/architecture.md:12: D-01 is held by "TestRetry", ` + notAHolder,
			"CLAUDE.md:5: names ./scripts/gates, which does not exist",
			"CLAUDE.md:5: names scripts/gates, which does not exist",
			"docs/architecture.md:5: names internal/gapi/client.go, which does not exist",
			"docs/architecture.md:6: names cmd/server, which does not exist",
			"docs/architecture.md:6: names scripts/livesheet, which does not exist",
			"docs/runbook.md:6: links to internal/gapi/client.go, which does not exist",
			"docs/runbook.md:6: names internal/gapi/client.go, which does not exist",
			"docs/runbook.md:10: links to cmd/server, which does not exist",
			"0 package directories under internal/ and cmd/, fewer than 1: the tree is not being read",
		}},
		{"no step literal, so no citation of a step is checked", []docsEdit{
			add(fxDriver, textLines(
				"package main",
				"",
				"type step struct{ name string }",
				"",
				`var one = struct{ name string }{name: "a file uploads"}`,
			)),
		}, []string{"scripts/livesheet: 0 live steps read, fewer than 1: the step literals are not being read"}},
		{"no spike literal, so no citation of a step is checked", []docsEdit{
			replace(fxSpikes, "var spikes = []spike{", "var spikes = []struct {\n\tname string\n\trun  func()\n}{"),
		}, []string{"scripts/spikes: 0 live steps read, fewer than 1: the spike literals are not being read"}},
		{"an index with no rows", []docsEdit{
			replace(fxArch, fxRow01+"\n"+fxRow02+"\n"+fxRow03+"\n", ""),
		}, []string{
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:13: D-01 has no index row",
			"docs/architecture.md:25: D-02 has no index row",
			"docs/architecture.md:30: D-03 has no index row",
		}},
		{"a config that enables no linter", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", ""),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		// The reader takes a block list only. A flow list is read as none,
		// and the floor says so rather than passing over it.
		{"linters listed in flow style", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", "  enable: [errcheck, forbidigo]\n"),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		{"a Makefile with no rules", []docsEdit{
			add(fxMakefile, "GO ?= go\n"),
		}, []string{
			"Makefile: no targets read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "lint", ` + notAHolder,
			"CLAUDE.md:5: names make check, which is not a Makefile target",
		}},
		{"no architecture document", []docsEdit{
			drop(fxArch),
		}, []string{
			"docs/architecture.md is missing",
			"CHANGELOG.md:5: names docs/architecture.md#codemap, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md#codemap, which does not exist",
			"README.md:3: links to docs/architecture.md, which does not exist",
			"README.md:4: names docs/architecture.md#d-01-retries-back-off, which does not exist",
			"docs/runbook.md:5: links to docs/architecture.md, which does not exist",
			"docs/runbook.md:14: links to docs/architecture.md, which does not exist",
		}},
		{"a Go file that does not parse", []docsEdit{
			add("internal/gapi/broken.go", "package gapi\n\nfunc {\n"),
		}, []string{"gates cannot parse internal/gapi/broken.go: internal/gapi/broken.go:3:6: expected 'IDENT', found '{'"}},
	})
}

// Each pass has a floor of its own, so one that keeps reading cannot make
// up for another that stops. Each row raises one floor one past what the
// fixture holds.
func TestDocsGateHoldsEachPassToItsFloor(t *testing.T) {
	atFloor := docsFloors{steps: 7, spikes: 1, links: 8, spanPaths: 14, packages: 2}
	raised := func(raise func(*docsFloors)) docsFloors {
		f := atFloor
		raise(&f)
		return f
	}
	// Under its floor a source holds no citation of a step to itself.
	unknownStep := []docsEdit{replace(fxClient, `and "the per-user rate limit".`, `and "a file downloads".`)}
	for _, tc := range []struct {
		name   string
		floors docsFloors
		edits  []docsEdit
		want   []string
	}{
		{"each pass at its floor", atFloor, nil, nil},
		{"driver steps one under their floor", raised(func(f *docsFloors) { f.steps++ }), unknownStep,
			[]string{"scripts/livesheet: 7 live steps read, fewer than 8: the step literals are not being read"}},
		{"spikes one under their floor", raised(func(f *docsFloors) { f.spikes++ }), unknownStep,
			[]string{"scripts/spikes: 1 live steps read, fewer than 2: the spike literals are not being read"}},
		{"links one under their floor", raised(func(f *docsFloors) { f.links++ }), nil,
			[]string{"8 Markdown links checked, fewer than 9: the links are not being read"}},
		{"code-span paths one under their floor", raised(func(f *docsFloors) { f.spanPaths++ }), nil,
			[]string{"14 code-span paths checked, fewer than 15: the code spans are not being read"}},
		{"packages one under their floor", raised(func(f *docsFloors) { f.packages++ }), nil,
			[]string{"2 package directories under internal/ and cmd/, fewer than 3: the tree is not being read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDocs(t, tc.floors, tc.edits, tc.want)
		})
	}
}

// Outside a git checkout the gate has no file list. It reached no
// verdict, so it says so and exits 2, as a gate that could not run does.
func TestDocsGateNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	err := docsGateAt(dir, docsFixtureFloors, io.Discard)
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 ||
		!strings.HasPrefix(err.Error(), "could not run: git -C "+dir+" ls-files") {
		t.Errorf("got %v; want a git ls-files failure that could not run", err)
	}
}

// The codemap is held to the tree: every package directory under
// internal/ and cmd/, nested ones included, is named in a code span in
// the "## Codemap" section, and every directory it names holds Go.
func TestDocsGateHoldsTheCodemapToTheTree(t *testing.T) {
	const redact = "internal/redact/redact.go"
	checkDocsCases(t, []docsCase{
		{"a package named under a subheading of the codemap", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n### The logs\n\n`internal/redact` keeps the payload out.\n"),
		}, nil},
		{"a package the codemap leaves out", []docsEdit{
			add(redact, "package redact\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a nested package the codemap leaves out", []docsEdit{
			add("internal/gapi/sheetstest/server.go", "package sheetstest\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/gapi/sheetstest`}},
		{"a package named only by a file in it", []docsEdit{
			add(redact, "package redact\n"),
			codemapNames(redact),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only in a fenced example", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n```markdown\n- `internal/redact` keeps the payload out.\n```\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only after the section ends", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "so a retry waits.", "so a retry waits, logged by `internal/redact`."),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a directory the codemap names that holds no .go file", []docsEdit{
			add("internal/assets/logo.txt", "a logo\n"),
			codemapNames("internal/assets"),
		}, []string{`docs/architecture.md:7: "## Codemap" names internal/assets, which holds no .go file`}},
		{"no codemap heading", []docsEdit{
			replace(fxArch, "## Codemap", "## Packages"),
		}, []string{
			"CHANGELOG.md:5: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"CLAUDE.md:3: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:5: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:14: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			`docs/architecture.md: no "## Codemap" heading`,
		}},
	})
}

// A package directory holds a .go file of its own under internal/ or
// cmd/, at any depth, and outside testdata.
func TestDocsPackageDirs(t *testing.T) {
	files := []string{
		"cmd/google-sheets-mcp/main.go",
		"cmd/google-sheets-mcp/main_test.go",
		"internal/doc.go",
		"internal/gapi/client.go",
		"internal/gapi/sheetstest/server.go",
		"internal/gapi/testdata/fixture.go",
		"internal/render/testdata/golden/card.txt",
		"internal/assets/logo.txt",
		"scripts/gates/main.go",
		"README.md",
	}
	want := []string{"cmd/google-sheets-mcp", "internal/gapi", "internal/gapi/sheetstest"}
	if got := docsPackageDirs(files); !slices.Equal(got, want) {
		t.Errorf("docsPackageDirs = %q, want %q", got, want)
	}
}

// These slugs are what GitHub renders for the same headings.
func TestGitHubSlug(t *testing.T) {
	for _, tc := range []struct{ heading, want string }{
		{"Codemap", "codemap"},
		{"D-01 Stdout carries only MCP JSON-RPC frames", "d-01-stdout-carries-only-mcp-json-rpc-frames"},
		{"What a write may reach", "what-a-write-may-reach"},
		{"The `register` function", "the-register-function"},
		{"C# and F#?", "c-and-f"},
		{"snake_case stays", "snake_case-stays"},
		{"Two  spaces", "two--spaces"},
		{"Straße über alles", "straße-über-alles"},
		{"A [link](https://example.com) here", "a-link-here"},
	} {
		if got := docsGitHubSlug(tc.heading); got != tc.want {
			t.Errorf("docsGitHubSlug(%q) = %q, want %q", tc.heading, got, tc.want)
		}
	}
}
