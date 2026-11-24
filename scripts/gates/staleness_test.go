package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v5/internal/config"
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
func TestTheChangelogGateCountsWhatShips(t *testing.T) {
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
			out, err := git("diff", "--name-only", "--no-renames", "-z", "HEAD~1", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			watched, _, err := changelogWatched("HEAD~1", "HEAD", strings.FieldsFunc(out, func(r rune) bool { return r == 0 }))
			if err != nil {
				t.Fatal(err)
			}
			if ships := len(watched) > 0; ships != tc.ships {
				t.Errorf("changelogWatched() = %q, ships = %t; want %t", watched, ships, tc.ships)
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

// A finding fails the gate and is named; nothing found passes it.
func TestStalenessVerdict(t *testing.T) {
	err := stalenessVerdict([]string{"README's tool table is missing: x"})
	want := "1 problem(s):\n  README's tool table is missing: x"
	if err == nil || err.Error() != want || exitCode(err) != 1 {
		t.Errorf("a finding: %v; want exit 1 with %q", err, want)
	}
	if err := stalenessVerdict(nil); err != nil {
		t.Errorf("nothing found: %v", err)
	}
}
