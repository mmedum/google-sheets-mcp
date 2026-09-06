package redact

import "testing"

func TestEmail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"someone@example.test", "s…@e….test"},
		{"a@b.co", "a…@b….co"}, // leakcheck:allow
		{"first.last@sub.example.org", "f…@s….org"},
		{"nodomain", "n…"},
		{"@example.test", "@…"},
		{"", ""},
	} {
		got := Email(tc.in)
		if got != tc.want {
			t.Errorf("Email(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEmailKeepsNothingUsable is the property, rather than the exact
// spelling: whatever the format, the local part and the organisation
// must not survive.
func TestEmailKeepsNothingUsable(t *testing.T) {
	for _, addr := range []string{
		"quorbin.plimth@grivetworks.example", // leakcheck:allow
		"q@grivetworks.example",              // leakcheck:allow
		"a.very.long.local.part@deep.sub.domain.example",
	} {
		got := Email(addr)
		for _, secret := range []string{"plimth", "grivetworks", "very", "sub.domain"} {
			if len(secret) > 2 && contains(got, secret) {
				t.Errorf("Email(%q) = %q, which still carries %q", addr, got, secret)
			}
		}
		if got == addr {
			t.Errorf("Email(%q) returned it unchanged", addr)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestPathMasksTheClientId is the unlucky route: the Cloud console names
// the file it hands you after the client id, so printing where the
// client secret lives prints the client id — into a bug report the issue
// form asks for.
func TestPathMasksTheClientId(t *testing.T) {
	// The shape, with an invented value. leakcheck:allow
	const id = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com" // leakcheck:allow gitleaks:allow
	in := "/home/someone/Downloads/client_secret_" + id + ".json"
	got := Path(in)
	if contains(got, "apps.googleusercontent.com") {
		t.Errorf("Path(%q) = %q", in, got)
	}
	if !contains(got, "<client-id>") {
		t.Errorf("Path did not say what it removed: %q", got)
	}
	// The directory survives: "it looked in the wrong place" is most of
	// what a first-run report is about, and a directory identifies
	// nobody.
	if !contains(got, "/Downloads/") {
		t.Errorf("Path threw away the directory: %q", got)
	}
	if got := Path("/home/someone/.config/google-sheets-mcp/client_secret.json"); contains(got, "<client-id>") {
		t.Errorf("Path masked a filename with no client id in it: %q", got)
	}
	if got := ClientID("failed for " + id + " on retry"); contains(got, "googleusercontent") {
		t.Errorf("ClientID = %q", got)
	}
}

// TestLineMasksWhatSomebodyElseAssembled is the live driver's
// transcript. §9.1 promises it is safe to paste into a commit message,
// and it was not: the driver substituted the ids it had created and let
// everything Drive returned through — an owner's address and a folder
// id, both on the never-list.
func TestLineMasksWhatSomebodyElseAssembled(t *testing.T) {
	// Shaped like what Drive returns, invented throughout: a folder id
	// is base64url starting 0A, and this one is not anybody's.
	const folder = "0AQuorbinNardleFolderIdZz"                                                    // leakcheck:allow
	in := "owner skerry@grivetworks.example; modified 2026-09-06T09:22:14.902Z; folder " + folder // leakcheck:allow
	got := Line(in)
	for _, forbidden := range []string{"grivetworks", "skerry@", folder} {
		if contains(got, forbidden) {
			t.Errorf("Line kept %q: %s", forbidden, got)
		}
	}
	// The parts that identify nobody survive, or the transcript stops
	// being worth reading.
	if !contains(got, "2026-09-06T09:22:14.902Z") || !contains(got, "owner ") || !contains(got, "folder ") {
		t.Errorf("Line threw away the readable part: %s", got)
	}

	// A file id too, and both spellings of a resource id.
	if got := Line("id 1SyntheticFixtureSpreadsheetIdXXXXXXXXXXXXXXX here"); contains(got, "Synthetic") {
		t.Errorf("a file id survived: %s", got)
	}
	// A commit SHA identifies a change, not a person.
	const sha = "f06c13b6b1a9625abc9e6e439d9c05a8f2190e94"
	if got := Line("pinned at " + sha); !contains(got, sha) {
		t.Errorf("a commit SHA was masked: %s", got)
	}
	// And one that starts with a digit the id pattern also matches.
	const numericSHA = "0f6c13b6b1a9625abc9e6e439d9c05a8f2190e94"
	if got := Line("pinned at " + numericSHA); !contains(got, numericSHA) {
		t.Errorf("a SHA starting with 0 was masked: %s", got)
	}
	// A grid range is not an id.
	if got := Line("rows 1-6 of 200; columns A:D"); got != "rows 1-6 of 200; columns A:D" {
		t.Errorf("Line mangled ordinary output: %s", got)
	}
}
