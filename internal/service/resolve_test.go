package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// TestNoSheetIsEverDefaulted is the failure a shipped server taught its
// model to make: its docs and tests offered Sheet1 as the default, and
// on a Portuguese account the first sheet is Página1, so every call
// failed with "Unable to parse range: Sheet1!A1:D3".
func TestNoSheetIsEverDefaulted(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Range: "A1:B2",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("a read with no sheet gave %v; nothing may be assumed", err)
	}
	// The refusal carries the answer: the titles that do exist.
	for _, title := range []string{sheetstest.FirstSheet, sheetstest.SecondSheet} {
		if !strings.Contains(err.Error(), title) {
			t.Errorf("the refusal does not list %q: %v", title, err)
		}
	}
}

func TestMissingSheetListsTheOnesThatExist(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: "Sheet1", Range: "A1:B2",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a missing sheet gave %v", err)
	}
	if !strings.Contains(err.Error(), sheetstest.SecondSheet) {
		t.Errorf("the refusal does not name the sheets that exist: %v", err)
	}
}

func TestSheetByIdAndByGid(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	// A numeric sheet id is stable across renames, so it is accepted
	// wherever a title is.
	res, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: "1837", Range: "A1:B1",
	})
	if err != nil {
		t.Fatalf("read by sheet id: %v", err)
	}
	if res.Sheet != sheetstest.SecondSheet {
		t.Errorf("sheet id 1837 resolved to %q", res.Sheet)
	}

	// A URL's gid names the sheet when the call does not.
	url := "https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit#gid=1837"
	res, err = svc.Read(ctx, service.ReadRequest{Spreadsheet: url, Range: "A1:B1"})
	if err != nil {
		t.Fatalf("read through a URL gid: %v", err)
	}
	if res.Sheet != sheetstest.SecondSheet {
		t.Errorf("the gid resolved to %q", res.Sheet)
	}

	bad := "https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit#gid=999999"
	if _, err := svc.Read(ctx, service.ReadRequest{Spreadsheet: bad, Range: "A1:B1"}); err == nil ||
		!strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("a gid for a sheet that does not exist gave %v", err)
	}
}

// TestTheTitleIsAlwaysQuotedOnTheWire is the trap that fails silently:
// a named range titled Vandel shadows the *sheet* named Vandel in an
// unquoted reference, so the wrong rectangle comes back and nothing
// errors. The fixture has exactly that clash.
func TestTheTitleIsAlwaysQuotedOnTheWire(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Range, "'") {
		t.Errorf("the range reported is unquoted: %q", res.Range)
	}
	for _, c := range srv.Calls() {
		for _, r := range c.Query["ranges"] {
			if !strings.HasPrefix(r, "'") {
				t.Errorf("an unquoted range reached the wire: %q", r)
			}
		}
	}
}

func TestSheetTitlesWithApostrophesAndAccents(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	for _, title := range []string{sheetstest.SecondSheet, sheetstest.ApostropheName} {
		res, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: title, Range: "A1:A1",
		})
		if err != nil {
			t.Fatalf("read of %q: %v", title, err)
		}
		if res.Sheet != title {
			t.Errorf("read of %q reported sheet %q", title, res.Sheet)
		}
	}
}

func TestARangeMayCarryItsOwnSheet(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	res, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Range: "'" + sheetstest.SecondSheet + "'!A1:B2",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Sheet != sheetstest.SecondSheet {
		t.Errorf("sheet = %q", res.Sheet)
	}

	// Naming it twice differently is a mistake worth catching rather
	// than resolving one way and hoping.
	_, err = svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Range: "'" + sheetstest.SecondSheet + "'!A1:B2",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("two different sheets gave %v", err)
	}
}

func TestBadRangeSaysWhatARangeIs(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2:C3",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("a malformed range gave %v", err)
	}
	if !strings.Contains(err.Error(), "B2:D40") {
		t.Errorf("the refusal does not show what a range looks like: %v", err)
	}
}
