package tools

import (
	"strings"
	"testing"
)

// TestParseResourceURI covers the encoding cases, because a sheet title
// is somebody's prose. url.Parse would read a slash as a path
// separator, a question mark as a query and a hash as a fragment, and
// each of those hands back a title with the end quietly missing — which
// then resolves to no sheet, or to the wrong one.
func TestParseResourceURI(t *testing.T) {
	for _, tc := range []struct {
		name        string
		uri         string
		spreadsheet string
		sheet       string
		wantErr     string
	}{
		{
			name: "a card", uri: "gsheets://1SyntheticFixtureResourceIdXXXXXXXXXXXXXXXXX",
			spreadsheet: "1SyntheticFixtureResourceIdXXXXXXXXXXXXXXXXX",
		},
		{
			name: "a sheet", uri: "gsheets://1SyntheticFixtureResourceIdXXXXXXXXXXXXXXXXX/Vandel",
			spreadsheet: "1SyntheticFixtureResourceIdXXXXXXXXXXXXXXXXX", sheet: "Vandel",
		},
		{
			name: "a title with a space", uri: "gsheets://sheet-id/Quorbin%20Skerry",
			spreadsheet: "sheet-id", sheet: "Quorbin Skerry",
		},
		{
			name: "a title that is not ASCII", uri: "gsheets://sheet-id/%C3%9Crv%C3%A4l",
			spreadsheet: "sheet-id", sheet: "Ürväl",
		},
		{
			name: "a title with an apostrophe", uri: "gsheets://sheet-id/Yalmic%27s%20Bractal",
			spreadsheet: "sheet-id", sheet: "Yalmic's Bractal",
		},
		{
			// The one url.Parse would take as a query string, leaving
			// the title as everything before the question mark.
			name: "a title with a question mark", uri: "gsheets://sheet-id/Nardle%3F",
			spreadsheet: "sheet-id", sheet: "Nardle?",
		},
		{
			// And the one it would take as a fragment.
			name: "a title with a hash", uri: "gsheets://sheet-id/Grivet%23two",
			spreadsheet: "sheet-id", sheet: "Grivet#two",
		},
		{
			name: "a title with a slash", uri: "gsheets://sheet-id/Plimth%2FNardle",
			spreadsheet: "sheet-id", sheet: "Plimth/Nardle",
		},
		{
			// An unencoded slash cannot be told from a third path
			// segment, so it is refused with the encoding rather than
			// guessed at.
			name: "an unencoded slash", uri: "gsheets://sheet-id/Plimth/Nardle",
			wantErr: "%2F",
		},
		{name: "another scheme", uri: "https://example.test/x", wantErr: "not a gsheets:// URI"},
		{name: "no spreadsheet", uri: "gsheets://", wantErr: "names no spreadsheet"},
		{name: "a trailing slash", uri: "gsheets://sheet-id/", wantErr: "drop the slash"},
		{name: "a broken escape", uri: "gsheets://sheet-id/%zz", wantErr: "percent-encoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spreadsheet, sheet, err := ParseResourceURI(tc.uri)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseResourceURI(%q) = %q, %q; want an error", tc.uri, spreadsheet, sheet)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseResourceURI(%q): %v", tc.uri, err)
			}
			if spreadsheet != tc.spreadsheet || sheet != tc.sheet {
				t.Errorf("ParseResourceURI(%q) = %q, %q; want %q, %q",
					tc.uri, spreadsheet, sheet, tc.spreadsheet, tc.sheet)
			}
		})
	}
}

// The templates have to agree with the parser: a URI the SDK routed here
// by matching a template must be one this code can read.
func TestResourceTemplatesMatchTheParser(t *testing.T) {
	if !strings.HasPrefix(CardURI, Scheme) || !strings.HasPrefix(SheetURI, Scheme) {
		t.Fatalf("the templates are %q and %q; both must start with %q", CardURI, SheetURI, Scheme)
	}
	if CardURI+"/{sheet}" != SheetURI {
		t.Errorf("the sheet template is %q; it must be the card's plus one segment, or a card URI with a "+
			"slash in it would route to neither", SheetURI)
	}
}
