package main

import (
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
