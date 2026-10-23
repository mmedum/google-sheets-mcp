package main

import (
	"os"
	"slices"
	"strings"
	"testing"
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

// A permission rule names a registered tool, and the README has them.
func TestPermissionRulesAreTools(t *testing.T) {
	registered := []string{"clear_values", "delete_cell_comment", "delete_data_source", "delete_dimensions", "delete_sheet"}
	ok := `"mcp__google-sheets__delete_sheet", "mcp__google-sheets__delete_dimensions",
		"mcp__google-sheets__clear_values", "mcp__google-sheets__delete_cell_comment",
		"mcp__google-sheets__delete_data_source"`
	if got := permissionRulesAreTools(ok, registered); len(got) != 0 {
		t.Errorf("five rules for five registered tools: %v", got)
	}
	renamed := strings.Replace(ok, "delete_sheet", "remove_sheet", 1)
	want := []string{"README.md gives a permission rule for remove_sheet, which the binary does not register"}
	if got := permissionRulesAreTools(renamed, registered); !slices.Equal(got, want) {
		t.Errorf("a rule for a tool that does not exist: %v", got)
	}
	want = []string{"README.md gives 0 Claude Code permission rule(s); the deletes alone are five"}
	if got := permissionRulesAreTools("no block at all", registered); !slices.Equal(got, want) {
		t.Errorf("a README without rules: %v", got)
	}
}
