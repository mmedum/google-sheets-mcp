package main

import (
	"os"
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
	write("README.md", "see [the licence](LICENSE) and [the plan](docs/architecture.md)\n"+
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
