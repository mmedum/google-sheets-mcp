package main

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
)

// The path check has to actually fail on a dead link, and the way to
// know is to feed it one. A gate nobody has watched fail is a gate
// nobody knows the shape of.
func TestPathsExistCatchesADeadLink(t *testing.T) {
	atRepoRoot(t)
	if problems := pathsExist(); len(problems) > 0 {
		t.Fatalf("the repository's own documents have dead links: %v", problems)
	}

	// A link from the root and a link from docs/, which resolve
	// differently: the second reaches the root through "..".
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.MkdirAll("docs", 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "see [the license](LICENSE) and [the plan](docs/architecture.md)\n"+
		"and [a site](https://example.test/x) and [a heading](#tools)\n")
	write("docs/architecture.md", "back to [the readme](../README.md), and [gone](../MISSING.md)\n")
	write("LICENSE", "x\n")

	problems := pathsExist()
	var found bool
	for _, p := range problems {
		if strings.Contains(p, "MISSING.md") {
			found = true
		}
		// A URL and a bare fragment are somebody else's to keep working.
		if strings.Contains(p, "example.test") || strings.Contains(p, "#tools") {
			t.Errorf("the check followed something that is not a path: %s", p)
		}
	}
	if !found {
		t.Errorf("a link to a file that does not exist was not caught: %v", problems)
	}
}

// The package map is derived, so a package added under internal/ has to
// appear in the architecture's tree or fail here.
func TestPackageMapNamesEveryPackage(t *testing.T) {
	atRepoRoot(t)
	if problems := packageMapIsComplete(); len(problems) > 0 {
		t.Errorf("%v", problems)
	}
}

// The status line is the first thing a reader sees and the furthest
// thing from any test. Every case below is watched failing or passing on
// purpose: a check nobody has seen fire is a check nobody knows the
// shape of.
func TestStatusLineIsTrue(t *testing.T) {
	atRepoRoot(t)
	// This repository is the pre-tag case: a phase in the status line,
	// no version claimed, no tag. It must pass silently.
	if problems := statusLineIsTrue(); len(problems) > 0 {
		t.Fatalf("the repository's own status lines: %v", problems)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.MkdirAll("docs", 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(readme, arch, changelog string) {
		for path, body := range map[string]string{
			"README.md": readme, "docs/architecture.md": arch, "CHANGELOG.md": changelog,
		} {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	const phase = "> **Status: phase 2 of five, not yet released.**\n"
	const claimsV2 = "**Status: v0.2.0, released.**\n"

	// A phase and no tag: nothing to confirm and nothing claimed.
	write(phase, phase, "# Changelog\n\n## [Unreleased]\n")
	if problems := statusLineIsTrue(); len(problems) > 0 {
		t.Errorf("a phase with no tag was reported: %v", problems)
	}

	// A version claimed with nothing released at all. This is the case
	// that used to be judged the other way round, failing every project
	// that had not shipped rather than the one document overclaiming.
	write(claimsV2, phase, "# Changelog\n\n## [Unreleased]\n")
	problems := statusLineIsTrue()
	if len(problems) != 1 || !strings.Contains(problems[0], "name the phase instead") {
		t.Errorf("a version claimed with nothing released gave %v", problems)
	}

	// The release-commit case: the changelog carries the version before
	// the tag can exist, and the claim matches it.
	write(claimsV2, phase, "# Changelog\n\n## [0.2.0] - 2026-09-06\n")
	if problems := statusLineIsTrue(); len(problems) > 0 {
		t.Errorf("a claim matching the newest changelog heading was reported: %v", problems)
	}

	// The failure this check exists for: a status line left behind.
	write("**Status: v0.1.0, released.**\n", phase, "# Changelog\n\n## [0.2.0] - 2026-09-06\n")
	problems = statusLineIsTrue()
	if len(problems) != 1 || !strings.Contains(problems[0], "the newest released version is 0.2.0") {
		t.Errorf("a stale version claim gave %v", problems)
	}

	// And the floor: no status line anywhere means the pattern changed
	// and the check is reading nothing, which must not pass.
	write("# google-sheets-mcp\n", "# Architecture\n", "# Changelog\n")
	problems = statusLineIsTrue()
	if len(problems) != 1 || !strings.Contains(problems[0], "reading nothing") {
		t.Errorf("documents with no status line gave %v", problems)
	}
}

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
