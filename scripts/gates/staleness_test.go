package main

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
)

// A permission rule names a registered tool, and every delete has one.
func TestPermissionRulesAreTools(t *testing.T) {
	deletes := []string{"clear_values", "delete_cell_comment", "delete_data_source", "delete_dimensions", "delete_sheet"}
	registered := append([]string{"write_values"}, deletes...)
	ok := `"mcp__google-sheets__delete_sheet", "mcp__google-sheets__delete_dimensions",
		"mcp__google-sheets__clear_values", "mcp__google-sheets__delete_cell_comment",
		"mcp__google-sheets__delete_data_source", "mcp__google-sheets__write_values"`
	if got := permissionRulesAreTools(ok, registered, deletes); len(got) != 0 {
		t.Errorf("a rule for every delete, each registered: %v", got)
	}
	renamed := strings.Replace(ok, "delete_sheet", "remove_sheet", 1)
	want := []string{
		"README.md gives a permission rule for remove_sheet, which the binary does not register",
		"README.md gives no permission rule for delete_sheet, which deletes",
	}
	if got := permissionRulesAreTools(renamed, registered, deletes); !slices.Equal(got, want) {
		t.Errorf("a rule for a tool that does not exist: %v", got)
	}
	repeated := strings.Repeat(`"mcp__google-sheets__clear_values" `, 5)
	want = []string{
		"README.md gives no permission rule for delete_cell_comment, which deletes",
		"README.md gives no permission rule for delete_data_source, which deletes",
		"README.md gives no permission rule for delete_dimensions, which deletes",
		"README.md gives no permission rule for delete_sheet, which deletes",
	}
	if got := permissionRulesAreTools(repeated, registered, deletes); !slices.Equal(got, want) {
		t.Errorf("one rule five times: %v", got)
	}
	want = []string{"the binary lists no tool that takes confirm; this check is not looking at the deletes"}
	if got := permissionRulesAreTools(ok, registered, nil); !slices.Equal(got, want) {
		t.Errorf("no deletes read: %v", got)
	}
}

// The README documents a tool or a scope in its prose: a row or a scope
// only in a fenced example documents nothing. Its permission rules are a
// fenced block of their own, read as they are.
func TestTheReadmeIsReadWithItsFencesBlanked(t *testing.T) {
	var listed []string
	for _, s := range loginScopes() {
		listed = append(listed, "`"+s+"`")
	}
	rules := "```json\n\"mcp__google-sheets__delete_sheet\"\n```\n"
	prose := "| `read_range` | reads |\n| `delete_sheet` | deletes |\n\n" + strings.Join(listed, "\n") + "\n\n" + rules
	registered, deletes := []string{"delete_sheet", "read_range"}, []string{"delete_sheet"}
	if got := readmeProblems(prose, registered, deletes); got != nil {
		t.Errorf("a README with every tool and scope in its prose: %q", got)
	}
	fenced := "```markdown\n| `read_range` | reads |\n" + strings.Join(listed[:1], "\n") + "\n```\n| `delete_sheet` | deletes |\n\n" +
		strings.Join(listed[1:], "\n") + "\n\n" + rules
	want := []string{
		"README's tool table is missing: read_range",
		"README.md does not list the scope " + strings.Trim(listed[0], "`") + ", which `login` requests",
	}
	if got := readmeProblems(fenced, registered, deletes); !slices.Equal(got, want) {
		t.Errorf("a tool row and a scope only in a fence: %q, want %q", got, want)
	}
}

// The configuration page names every variable the server reads, the
// removed one included, as a whole word, names no other, and states each
// of the bundle's field rules, what a profile name is among them, in the
// server's own words.
func TestConfigDocProblems(t *testing.T) {
	doc := string(repoFile(t, configDocPath))
	if got := configDocProblems(config.Vars(), fieldRules, doc); len(got) != 0 {
		t.Errorf("the committed page: %q", got)
	}
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{"a variable left out", "`GSHEETS_WRITE_TIMEOUT`", "`the write timeout`",
			[]string{"docs/configuration.md does not document GSHEETS_WRITE_TIMEOUT"}},
		{"a removed variable left out", "`GSHEETS_REQUIRE_PROMPT`", "`the prompt setting`",
			[]string{"docs/configuration.md does not document GSHEETS_REQUIRE_PROMPT"}},
		{"a name kept only inside a longer one", "`GSHEETS_CONFIG_DIR`", "`GSHEETS_CONFIG_DIRECTORY`", []string{
			"docs/configuration.md does not document GSHEETS_CONFIG_DIR",
			"docs/configuration.md documents GSHEETS_CONFIG_DIRECTORY, which the code does not read",
		}},
		{"a variable the code does not read", "`GSHEETS_MAX_CHARS`", "`GSHEETS_MAX_CHARS` and `GSHEETS_MAX_ROWS`",
			[]string{"docs/configuration.md documents GSHEETS_MAX_ROWS, which the code does not read"}},
		{"a variable named only in a fenced example", "`GSHEETS_WRITE_TIMEOUT`", "the write timeout\n\n```\nGSHEETS_WRITE_TIMEOUT=1m\n```\n",
			[]string{"docs/configuration.md does not document GSHEETS_WRITE_TIMEOUT"}},
		{"the profile rule in other words", "- and _, starting", "`-` and `_`, starting",
			[]string{`docs/configuration.md does not say a profile name is "up to 64 characters from a to z, ` +
				`0 to 9, - and _, starting with a letter or digit"`}},
	} {
		if !strings.Contains(doc, tc.old) {
			t.Fatalf("%s: the page does not say %q", tc.name, tc.old)
		}
		if got := configDocProblems(config.Vars(), fieldRules, strings.ReplaceAll(doc, tc.old, tc.new)); !slices.Equal(got,
			tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// A rule added to the bundle's field is held on the page too.
	rules := maps.Clone(fieldRules)
	rules[config.EnvPrefix+"LOG_LEVEL"] = fieldRule{words: "one of debug, info, warn and error", fails: "say a log level is"}
	want := []string{`docs/configuration.md does not say a log level is "one of debug, info, warn and error"`}
	if got := configDocProblems(config.Vars(), rules, doc); !slices.Equal(got, want) {
		t.Errorf("a rule the page does not state: %q, want %q", got, want)
	}
}

// stalenessChangelog is a CHANGELOG with nothing new since 1.9.0.
const stalenessChangelog = "## [Unreleased]\n\n## [1.9.0]\n\n- old\n"

// stalenessRepo is a git checkout of a module whose binary, cmd/fixture,
// imports internal/a, which embeds a file, and not internal/unused, with a
// release tag v1.9.0 on its one commit, as the working directory. Each
// case starts from stalenessReset.
func stalenessRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	for name, body := range map[string]string{
		changelogPath: stalenessChangelog,
		"go.mod": textLines("module example.com/fixture", "", "go 1.21", "",
			"require example.com/dep v1.0.0", "", "replace example.com/dep => ./dep"),
		"dep/go.mod": textLines("module example.com/dep", "", "go 1.21"),
		"dep/dep.go": textLines("package dep"),
		"cmd/fixture/main.go": textLines("package main", "", `import (`, `	_ "example.com/dep"`,
			`	_ "example.com/fixture/internal/a"`, ")", "", "func main() {}"),
		"tools.go":              textLines("package fixture"),
		"internal/a/a.go":       textLines("package a", "", `import _ "embed"`, "", "//go:embed data/x.txt", "var X string"),
		"internal/a/b.go":       textLines("package a"),
		"internal/a/data/x.txt": "x\n",
		"internal/a/notes.md":   "notes\n",
		"internal/unused/u.go":  textLines("package unused"),
		"internal/a/a_test.go":  textLines("package a"),
		"internal/a/testdata/x": "x\n",
		"scripts/gates/main.go": textLines("package main", "", "func main() {}"),
		"docs/x.md":             "x\n",
	} {
		put(t, dir, name, body)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	gitIn(t, dir, "tag", "v1.9.0")
	t.Chdir(dir)
	return dir
}

// stalenessReset puts the checkout back at the release.
func stalenessReset(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "reset", "-q", "--hard", "v1.9.0")
}

// commitChange commits the files with their new contents, a file with no
// contents removed.
func commitChange(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if body == "" {
			gitIn(t, dir, "rm", "-q", name)
		} else {
			put(t, dir, name, body)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "change")
}

// A change to what the binary is built from needs an entry: any file but a
// test of a package it imports, a file one embeds, the bundle's manifest,
// and go.mod but its require and tool lines. A test, a fixture, a package
// it does not import and a dependency bump do not.
func TestTheChangelogRuleCountsWhatShips(t *testing.T) {
	const goMod = "go.mod"
	dir := stalenessRepo(t)
	for _, tc := range []struct {
		name  string
		files map[string]string
		ships bool
	}{
		{"a package the binary imports", map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}, true},
		{"a file added to one", map[string]string{"internal/a/c.go": "package a\n"}, true},
		{"a file removed from one", map[string]string{"internal/a/b.go": ""}, true},
		{"a file moved out of one", map[string]string{"internal/a/b.go": "", "docs/b.go": "package a\n"}, true},
		{"the command", map[string]string{"cmd/fixture/main.go": "package main\n\nfunc main() { println() }\n"}, true},
		{"a file a package embeds", map[string]string{"internal/a/data/x.txt": "y\n"}, true},
		// The go command builds a package from assembly, a .syso and C too
		// (`go help filetype`), so every file of its directory counts.
		{"assembly in one", map[string]string{"internal/a/a_amd64.s": "// asm\n"}, true},
		{"a .syso in one", map[string]string{"internal/a/res.syso": "obj\n"}, true},
		{"any other file of one", map[string]string{"internal/a/notes.md": "more\n"}, true},
		{"the bundle's manifest", map[string]string{mcpbManifestPath: "{}\n"}, true},
		{"the bundle's launcher", map[string]string{launcherPath: "#!/bin/sh\n"}, true},
		// The bundle packs the README to be read, as the archives do.
		{"the root README", map[string]string{"README.md": "more\n"}, false},
		{"go.mod's go line", map[string]string{goMod: strings.Replace(stalenessGoMod(t, dir), "go 1.21", "go 1.22", 1)}, true},
		{"go.mod's toolchain", map[string]string{goMod: stalenessGoMod(t, dir) + "\ntoolchain go1.21.0\n"}, true},
		{"go.mod's replace", map[string]string{goMod: strings.Replace(stalenessGoMod(t, dir), "dep =>", "dep v1.0.0 =>", 1)}, true},
		{"a dependency bump", map[string]string{goMod: strings.Replace(stalenessGoMod(t, dir),
			"require example.com/dep v1.0.0", "require (\n\texample.com/dep v1.1.0 // a bump\n)", 1)}, false},
		// The go.mod grammar reads ( as a token of its own (https://go.dev/ref/mod#go-mod-file-lexical).
		{"a dependency bump in a require( block", map[string]string{goMod: strings.Replace(stalenessGoMod(t, dir),
			"require example.com/dep v1.0.0", "require(\n\texample.com/dep v1.1.0\n)", 1)}, false},
		// A tool directive names a tool `go tool` runs, which the binary does not
		// build (https://go.dev/ref/mod#go-mod-file-tool).
		{"a tool directive", map[string]string{goMod: stalenessGoMod(t, dir) + "\ntool example.com/dep\n"}, false},
		{"a tool( block", map[string]string{goMod: stalenessGoMod(t, dir) + "\ntool(\n\texample.com/dep\n)\n"}, false},
		{"a test", map[string]string{"internal/a/a_test.go": "package a\n\n// changed\n"}, false},
		{"a fixture", map[string]string{"internal/a/testdata/x": "y\n"}, false},
		{"assembly in a fixture", map[string]string{"internal/a/testdata/a_amd64.s": "// asm\n"}, false},
		{"a file under a package that it does not embed", map[string]string{"internal/a/data/other.txt": "more\n"}, false},
		{"a package the binary does not import", map[string]string{"internal/unused/u.go": "package unused\n\n// changed\n"}, false},
		// A dependency's package sits at a path of its own module's, never this one's.
		{"a package at the path of a dependency's", map[string]string{"tools.go": "package fixture\n\n// changed\n"}, false},
		{"a script", map[string]string{"scripts/gates/main.go": "package main\n\nfunc main() { println() }\n"}, false},
		{"a doc", map[string]string{"docs/x.md": "y\n"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stalenessReset(t, dir)
			commitChange(t, dir, tc.files)
			problems, err := checkChangelog()
			var want []string
			if tc.ships {
				want = []string{"the binary's source changed since v1.9.0 but CHANGELOG.md documents nothing new; " +
					"put the entries under [Unreleased], or under the version heading this release is about to tag"}
			}
			if err != nil || !slices.Equal(problems, want) {
				t.Errorf("checkChangelog() = %q, %v; want %q", problems, err, want)
			}
		})
	}
}

// stalenessGoMod is the fixture's go.mod at the release.
func stalenessGoMod(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A release cut moves the entries under the heading it is about to tag,
// a prerelease's included; a heading in a fence cuts nothing, and only a
// bullet at the margin is an entry.
func TestTheChangelogRuleReadsTheReleaseCut(t *testing.T) {
	dir := stalenessRepo(t)
	for _, tc := range []struct {
		name, changelog string
		ok              bool
	}{
		{"entries under [Unreleased]", "## [Unreleased]\n\n- new\n\n## [1.9.0]\n\n- old\n", true},
		{"a prerelease cut", "## [Unreleased]\n\n## [2.0.0-rc.1]\n\n- new\n\n## [1.9.0]\n\n- old\n", true},
		{"no entry", stalenessChangelog, false},
		{"a cut heading only in a fence", "## [Unreleased]\n\n```\n## [2.0.0]\n\n- new\n```\n\n## [1.9.0]\n\n- old\n", false},
		{"an indented bullet alone", "## [Unreleased]\n\n  - new\n\n## [1.9.0]\n\n- old\n", false},
		{"prose alone", "## [Unreleased]\n\nSomething new.\n\n## [1.9.0]\n\n- old\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stalenessReset(t, dir)
			commitChange(t, dir, map[string]string{changelogPath: tc.changelog, "internal/a/b.go": "package a\n\n// changed\n"})
			if problems, err := checkChangelog(); err != nil || (len(problems) == 0) != tc.ok {
				t.Errorf("checkChangelog() = %q, %v; want ok: %v", problems, err, tc.ok)
			}
		})
	}
}

// A listing that names no command is not reading the binary.
func TestTheChangelogRuleNeedsTheBinary(t *testing.T) {
	commitChange(t, stalenessRepo(t), map[string]string{"cmd/fixture/main.go": "", "cmd/README.md": "moved\n"})
	_, err := checkChangelog()
	want := "could not run: the CHANGELOG rule: go list -deps ./cmd/... named no package of this module, so it is not reading the binary"
	if !errors.As(err, new(couldNotRun)) || err.Error() != want {
		t.Errorf("checkChangelog() = %v, want %q", err, want)
	}
}

// A git that cannot answer is no verdict, never "source changed", and so is
// a checkout whose history reaches no release.
func TestTheChangelogRuleNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	_, err := checkChangelog()
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 || !strings.HasPrefix(err.Error(), "could not run: the CHANGELOG rule: git tag --merged HEAD") {
		t.Errorf("outside git: %v; want a git tag failure that could not run", err)
	}
	dir = stalenessRepo(t)
	blob, err := git("hash-object", "-w", changelogPath)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "tag", "v2.0.0", blob)
	gitIn(t, dir, "tag", "-d", "v1.9.0")
	commitChange(t, dir, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"})
	_, err = checkChangelog()
	want := "could not run: the CHANGELOG rule: HEAD reaches no release tag (vX.Y.Z) to measure from"
	if !errors.As(err, new(couldNotRun)) || err == nil || err.Error() != want {
		t.Errorf("no release on HEAD's history: %v; want %q", err, want)
	}
}

// A finding fails and names why the CHANGELOG rule could not run, so a git
// that cannot answer hides no other check; the reason alone is no verdict.
func TestStalenessVerdictKeepsEveryCheck(t *testing.T) {
	unrun := couldNotRun{errors.New("the CHANGELOG rule: git tag --merged HEAD: no git")}
	err := stalenessVerdict([]string{"README's tool table is missing: x"}, unrun)
	want := "2 problem(s):\n  README's tool table is missing: x\n  could not run: the CHANGELOG rule: git tag --merged HEAD: no git"
	if err == nil || err.Error() != want || exitCode(err) != 1 {
		t.Errorf("a finding and no git: %v; want exit 1 with %q", err, want)
	}
	if err := stalenessVerdict(nil, unrun); !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 {
		t.Errorf("no git alone: %v; want it could not run", err)
	}
	if err := stalenessVerdict(nil, nil); err != nil {
		t.Errorf("nothing found: %v", err)
	}
}

// The release a change is measured from is the newest HEAD reaches by
// semver, a prerelease included: neither a newer release on another line
// nor a tag that names no release is HEAD's.
func TestLastTagIsOneTheRevisionReaches(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	initRepo(t, dir)
	t.Chdir(dir)
	empty := func(message string) { gitIn(t, dir, "commit", "--quiet", "--allow-empty", "-m", message) }
	empty("Start")
	gitIn(t, dir, "branch", "-M", "main")
	gitIn(t, dir, "branch", "before-any-release")
	empty("Release")
	gitIn(t, dir, "tag", "v1.0.0")
	gitIn(t, dir, "switch", "--quiet", "-c", "maint")
	empty("Fix")
	gitIn(t, dir, "tag", "v1.0.1")
	gitIn(t, dir, "switch", "--quiet", "main")
	empty("Next")
	gitIn(t, dir, "tag", "v2.0.0")
	for _, m := range []string{"More", "And more"} {
		empty(m)
	}
	gitIn(t, dir, "switch", "--quiet", "-c", "merged")
	gitIn(t, dir, "merge", "--quiet", "--no-ff", "-m", "Merge maint", "maint")
	// git's version sort puts v3.0.0-rc.1 after v3.0.0; semver puts it before.
	gitIn(t, dir, "switch", "--quiet", "-c", "next", "main")
	empty("Candidate")
	gitIn(t, dir, "tag", "v3.0.0-rc.1")
	gitIn(t, dir, "branch", "candidate")
	empty("Final")
	gitIn(t, dir, "tag", "v3.0.0")
	gitIn(t, dir, "switch", "--quiet", "main")
	empty("Weekly")
	gitIn(t, dir, "tag", "weekly")
	gitIn(t, dir, "tag", "v9.9.9--")
	for rev, want := range map[string]string{
		"main":               "v2.0.0",
		"maint":              "v1.0.1",
		"merged":             "v2.0.0",
		"candidate":          "v3.0.0-rc.1",
		"next":               "v3.0.0",
		"before-any-release": "",
	} {
		if got, err := lastTag(rev); err != nil || got != want {
			t.Errorf("lastTag(%s) = %q, %v; want %q", rev, got, err, want)
		}
	}
	if _, err := lastTag("no-such-rev"); err == nil {
		t.Error("lastTag of a revision git cannot read returned no error")
	}
}
