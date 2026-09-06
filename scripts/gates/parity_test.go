package main

import (
	"os"
	"strings"
	"testing"
)

// The gate has to fail on the three drifts that actually happened, or it
// is a check nobody has watched work.
func TestParityFindsTheDriftsThatHappened(t *testing.T) {
	for _, tc := range []struct {
		name string
		// remove is a target dropped from the Makefile's `check:` line,
		// drop a substring replaced in the workflow.
		remove string
		drop   string
		want   string
	}{
		{
			name: "CI does not vet the build-tagged code",
			drop: "go vet -tags=live ./...",
			want: `ci.yml does not run "go vet -tags=live ./..."`,
		},
		{
			name:   "make check does not scan for secrets",
			remove: "secrets",
			want:   "`make check` does not run \"secrets\"",
		},
		{
			name:   "nothing checks that go.mod is tidy",
			remove: "tidy",
			drop:   "go mod tidy -diff",
			want:   "tidy",
		},
		{
			name:   "a gate runs in CI and not in make check",
			remove: "staleness",
			want:   "`make check` does not run the \"staleness\" gate",
		},
		{
			name: "a gate runs in make check and not in CI",
			drop: "gates staleness",
			want: `ci.yml does not run the "staleness" gate`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			makefile, workflow := repoFiles(t)
			if tc.remove != "" {
				makefile = withoutTarget(t, makefile, tc.remove)
			}
			if tc.drop != "" {
				workflow = strings.ReplaceAll(workflow, tc.drop, "nothing-of-the-sort")
			}
			err := checkParity(makefile, workflow)
			if err == nil {
				t.Fatalf("the drift was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// And it passes on the tree as it stands, or the failures above prove
// only that it fails on everything.
func TestParityPassesOnThisRepository(t *testing.T) {
	makefile, workflow := repoFiles(t)
	if err := checkParity(makefile, workflow); err != nil {
		t.Errorf("the repository as it stands: %v", err)
	}
}

// The subcommand list comes from the dispatcher rather than from a list
// here, so a gate added there is covered from its first commit.
func TestSubcommandsComeFromTheDispatcher(t *testing.T) {
	atRepoRoot(t)
	got, err := gateSubcommands()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 8 {
		t.Fatalf("read %d subcommands from the dispatcher: %v", len(got), got)
	}
	for _, want := range []string{"coverage", "classes", "leaks", "parity", "staleness"} {
		if !contains(got, want) {
			t.Errorf("the dispatcher declares %q and the gate did not see it: %v", want, got)
		}
	}
}

// Every exemption carries a reason, the same rule the coverage floor and
// the leak allowlist keep.
func TestExemptionsHaveReasons(t *testing.T) {
	if len(precommitOnly) == 0 {
		t.Skip("nothing exempt")
	}
	for name, reason := range precommitOnly {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%q is exempt from parity with no reason", name)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// repoFiles reads the two lists from the repository root. A test runs
// with the package directory as its working directory, and the gate
// itself chdirs before doing anything.
func repoFiles(t *testing.T) (makefile, workflow string) {
	t.Helper()
	atRepoRoot(t)
	m, w, err := readBothLists()
	if err != nil {
		t.Fatal(err)
	}
	return m, w
}

// withoutTarget drops one prerequisite from the Makefile's `check:`
// line, leaving the rest of the file alone. Editing the whole file by
// substring would hit the target's own definition first and leave
// `check:` untouched, which is a test that passes for the wrong reason.
func withoutTarget(t *testing.T, makefile, target string) string {
	t.Helper()
	for _, line := range strings.Split(makefile, "\n") {
		if !strings.HasPrefix(line, "check:") {
			continue
		}
		var kept []string
		for _, f := range strings.Fields(line) {
			if f != target {
				kept = append(kept, f)
			}
		}
		edited := strings.Join(kept, " ")
		if edited == line {
			t.Fatalf("`check:` does not list %q, so removing it proves nothing", target)
		}
		return strings.Replace(makefile, line, edited, 1)
	}
	t.Fatal("no `check:` line in the Makefile")
	return ""
}

func atRepoRoot(t *testing.T) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
