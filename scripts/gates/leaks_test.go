package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRulesCatchPlantedIdentifiers is the scanner watching itself fail.
// A guard that has caught nothing is working; a guard that matched
// nothing prints the same thing and is not.
func TestRulesCatchPlantedIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		leaks bool
	}{
		{"address at a real domain", "contact alice@acme.co.uk for access", true}, // leakcheck:allow
		{"documentation address", "owner fixture@example.test signed in", false},
		{"test domain", "a@b.test", false},
		{"example.com", "a@example.com", false},
		{"google account id", "sub 109876543210987654321 signed in", true}, // leakcheck:allow
		// Twenty digits with no letter: a rule wanting a capital and a
		// digit misses this, and a sibling shipped exactly that hole.
		{"drive permission id", "permission 09876543210987654321 granted", true},                              // leakcheck:allow
		{"oauth client id", "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com", true}, // leakcheck:allow gitleaks:allow
		{"api host on its own", "https://sheets.googleapis.com/v4/spreadsheets", false},
		{"user content url", "https://lh3.googleusercontent.com/a-/AOh14GhAbCdEfGhIjK", true},                                 // leakcheck:allow
		{"real-looking spreadsheet id", "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms", true},                                 // leakcheck:allow
		{"spreadsheet url", "https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit", true}, // leakcheck:allow
		{"synthetic id", "1SyntheticFixtureSpreadsheetIdXXXXXXXXXXXXXXX", false},
		{"invented by repetition", "1AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH", false},
		{"a commit sha is not an id", "pinned at f06c13b6b1a9625abc9e6e439d9c05a8f2190e94", false},
		// A SHA that starts with a digit the id pattern also matches.
		{"a commit sha starting with 0", "pinned at 0f6c13b6b1a9625abc9e6e439d9c05a8f2190e94", false},
		{"a commit sha starting with 1", "pinned at 1f6c13b6b1a9625abc9e6e439d9c05a8f2190e94", false},
		// A Drive folder or shared-drive id starts 0A, and the pattern
		// only knew about file ids until a live run put one of these in
		// a transcript.
		{"drive folder id", "folder 0ADXPgqEx786XUk9PVA", true}, // leakcheck:allow
		{"synthetic folder id", "folder 0AFixtureFolderIdXXXXXXX", false},
		// A formula is the worst case: it carries another spreadsheet's
		// id inside a string literal.
		{"importrange formula", `=IMPORTRANGE("1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms","A1:B2")`, true}, // leakcheck:allow
		{"a sheet id is not in scope", "sheetId 1837 and tableId 42", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := findLeaks(tc.text)
			if tc.leaks && len(got) == 0 {
				t.Errorf("a planted identifier went unnoticed: %s", tc.text)
			}
			if !tc.leaks && len(got) > 0 {
				t.Errorf("false positive on %s: %v", tc.text, got)
			}
		})
	}
}

// TestFindingsAreAbbreviated keeps the gate from reprinting the leak in
// the CI log that reports it.
func TestFindingsAreAbbreviated(t *testing.T) {
	id := "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms" // leakcheck:allow
	found := findLeaks(id)
	if len(found) == 0 {
		t.Fatal("the id was not caught at all")
	}
	if strings.Contains(found[0], id) {
		t.Errorf("the finding reprints the whole id: %s", found[0])
	}
	if !strings.Contains(found[0], "…") {
		t.Errorf("the finding is not abbreviated: %s", found[0])
	}
}

// TestTheMarkerExcusesOneLineOnly is why this file can contain the
// shapes it catches without excusing itself wholesale.
func TestTheMarkerExcusesOneLineOnly(t *testing.T) {
	text := "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms " + marker + "\n" + // leakcheck:allow
		"1CxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms\n" // leakcheck:allow
	got := findLeaks(strip(text))
	if len(got) != 1 {
		t.Fatalf("want exactly the unmarked line to be a finding, got %v", got)
	}
}

func TestAllowlistEntriesHaveReasons(t *testing.T) {
	if err := checkAllowlistHasReasons(); err != nil {
		t.Error(err)
	}
	// An entry without a reason is how a gate quietly stops working, so
	// the check itself is checked.
	allowedValues["planted"] = ""
	defer delete(allowedValues, "planted")
	if err := checkAllowlistHasReasons(); err == nil {
		t.Error("an allowlist entry with no reason was accepted")
	}
}

func TestInventedAndRuns(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"1SyntheticThing", true},
		{"1FixtureThing", true},
		{"1NoSuchThing", true},
		{"1AAAAthing", true},
		{"1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs", false}, // leakcheck:allow
	} {
		if got := invented(tc.in); got != tc.ok {
			t.Errorf("invented(%q) = %v", tc.in, got)
		}
	}
	if hasRun("abc", 2) || !hasRun("abbc", 2) || !hasRun("aaaa", 4) {
		t.Error("hasRun is wrong")
	}
}

func TestBinaryDetection(t *testing.T) {
	if !isBinary([]byte{'a', 0, 'b'}) || isBinary([]byte("plain text")) {
		t.Error("isBinary is wrong")
	}
}

// TestATrackedBinaryIsAFinding runs the branch rather than the helper.
//
// isBinary has been tested since phase 0 and the branch that uses it
// never was, so what the scan does with a committed binary was a
// question only the source could answer — and three people read that
// source today and got it wrong, including one who had just described it
// to somebody else. A check nobody has run is worse than a gap nobody
// has explained: an unexplained gap invites doubt, and a name invites
// agreement.
//
// A NUL byte in the first few kilobytes is the whole rule, which is what
// git itself uses, so this needs no real executable. That is the part
// worth noticing: the test costs a second and its absence is what left
// the question to be settled by reading.
func TestATrackedBinaryIsAFinding(t *testing.T) {
	dir := fixtureRepo(t)
	write(t, dir, "committed-artifact", "\x7fELF\x02\x01\x01\x00binary")
	gitIn(t, dir, "add", "-f", "committed-artifact")
	gitIn(t, dir, "commit", "-qm", "the accident")

	inDir(t, dir)
	err := scanTree()
	if err == nil {
		t.Fatal("a committed binary was accepted")
	}
	if !strings.Contains(err.Error(), "committed-artifact") {
		t.Errorf("err = %v, want it to name the file", err)
	}
	// And it says what is wrong rather than only that something is.
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("err = %v, want it to say the file is binary", err)
	}
}

func TestSafeDomain(t *testing.T) {
	for domain, want := range map[string]bool{
		"example.test": true, "example.com": true, "sub.example.invalid": true,
		// RFC 2606 reserves the apex and everything under it.
		"sub.example.org": true, "deep.sub.example.net": true,
		// But not a domain that merely ends in the same letters.
		"notexample.com": false, "example.com.evil.co": false,
		"localhost": false, "acme.co.uk": false, "EXAMPLE.COM": true, "example.com.": true,
	} {
		if got := safeDomain(domain); got != want {
			t.Errorf("safeDomain(%q) = %v, want %v", domain, got, want)
		}
	}
}

// The scan reads the working tree, not just the index.
//
// A tracked-only scan is blind to exactly the files that most need
// scanning: a phase's new ones, which nobody has looked at before. This
// repository ran `make check` green a dozen times over 167 files while
// 28 of phase 3's own were untracked, and the first `git add -A` found a
// spreadsheet id in a test written that afternoon.
//
// Driven against a throwaway repository rather than this one, so it can
// watch both halves fail without anything reaching the real tree.
func TestScanReadsUntrackedFilesToo(t *testing.T) {
	dir := fixtureRepo(t)
	inDir(t, dir)

	if err := scanTree(); err != nil {
		t.Fatalf("a clean tree was refused: %v", err)
	}

	// An untracked text file carrying an identifier. This is the case
	// the tracked-only scan passed silently.
	write(t, dir, "new.go", "const id = \"1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789\"\n") // leakcheck:allow
	if err := scanTree(); err == nil {
		t.Error("an untracked file carrying an id was not scanned")
	}
	if err := os.Remove(filepath.Join(dir, "new.go")); err != nil {
		t.Fatal(err)
	}

	// An untracked build artifact, refused while it is still untracked —
	// which is what fails before a wildcard add can sweep it in.
	write(t, dir, "artifact", "\x7fELF\x02\x01\x01\x00binary")
	err := scanTree()
	if err == nil {
		t.Fatal("an untracked binary was accepted")
	}
	if !strings.Contains(err.Error(), "artifact") {
		t.Errorf("err = %v, want it to name the artifact", err)
	}
	if err := os.Remove(filepath.Join(dir, "artifact")); err != nil {
		t.Fatal(err)
	}

	// And a gitignored one is left alone: it cannot be committed either,
	// and refusing it would fail every working tree that has ever run
	// `go build`.
	write(t, dir, "ignored-artifact", "\x7fELF\x02\x01\x01\x00binary")
	if err := scanTree(); err != nil {
		t.Errorf("a gitignored build output was refused: %v", err)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// fixtureRepo is a throwaway repository with enough tracked files to
// meet the scan's own "am I seeing a repository" floor, and one
// gitignored name to prove the ignore rules are still honoured.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.email", "fixture@example.test")
	gitIn(t, dir, "config", "user.name", "Fixture")
	for i := range 20 {
		write(t, dir, fmt.Sprintf("kept%02d.md", i), "Quorbin and Nardle, which are invented.\n")
	}
	write(t, dir, ".gitignore", "/ignored-artifact\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fixture")
	return dir
}

// inDir runs the rest of the test with dir as the working directory,
// because the scan reads the repository it is standing in.
func inDir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
