package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestCreateReturnsTheCardAndTheSheetTitles(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Create(context.Background(), service.CreateRequest{
		Title: "Grivet plan", Sheets: []string{"Oblisk", "Trennow"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Spreadsheet == "" || res.Link == "" {
		t.Errorf("Create returned no id or link: %+v", res)
	}
	// A sheets list replaces the default sheet rather than adding to it,
	// verified live. A caller who named two sheets gets two.
	if !slices.Equal(res.Sheets, []string{"Oblisk", "Trennow"}) {
		t.Errorf("Sheets = %v, want exactly the two that were asked for", res.Sheets)
	}
	if !strings.Contains(res.Render(), "Grivet plan") {
		t.Errorf("the summary does not carry the card:\n%s", res.Render())
	}
}

// Seeding goes through the same values.update every other write uses, so
// input means the same thing and coercions are reported the same way.
// With no sheets list, Google makes one and names it in the account's
// language. The fake names it something that is not "Sheet1", so a test
// that assumed the English name fails here rather than in somebody's
// Portuguese account.
func TestCreateWithNoSheetsListTakesGooglesOwn(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Create(context.Background(), service.CreateRequest{Title: "Grivet plan"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(res.Sheets) != 1 {
		t.Fatalf("Sheets = %v, want the one Google makes", res.Sheets)
	}
	if res.Sheets[0] == "Sheet1" {
		t.Error("the fixture named it Sheet1, which is the assumption this project refuses to make")
	}
}

func TestCreateSeedsThroughTheOrdinaryWritePath(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Create(context.Background(), service.CreateRequest{
		Title: "Grivet plan", Values: [][]any{{"Plimth", "007"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Seeded == "" {
		t.Fatal("the seed values were not written")
	}
	if len(res.Coerced) != 1 || res.Coerced[0].Stored != "7" {
		t.Errorf("Coerced = %+v, want the 007 the seed carried", res.Coerced)
	}
}

// A new spreadsheet has nothing to destroy, and a formula that fetches a
// URL is no less outbound for being in a fresh file.
func TestCreateStillGatesExternalFormulas(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Create(context.Background(), service.CreateRequest{
		Title: "Grivet plan", Values: [][]any{{`=IMPORTDATA("https://example.test/x")`}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("a seeded IMPORTDATA gave %v", err)
	}
	if len(srv.Calls()) != 0 {
		t.Error("a refused create still made a spreadsheet")
	}
}

func TestCreateNeedsATitle(t *testing.T) {
	_, svc := standard(t)
	if _, err := svc.Create(context.Background(), service.CreateRequest{Title: "  "}); err == nil {
		t.Fatal("a blank title was accepted")
	}
}

func TestCreateDryRunSendsNothing(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Create(context.Background(), service.CreateRequest{Title: "Grivet plan", DryRun: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.DryRun || len(srv.Calls()) != 0 {
		t.Errorf("a dry run made %d call(s)", len(srv.Calls()))
	}
}

func TestManageSheetActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.SheetRequest
		want func(*testing.T, *service.SheetResult)
	}{
		{
			name: "add",
			req:  service.SheetRequest{Action: service.SheetAdd, Title: "Oblisk"},
			want: func(t *testing.T, r *service.SheetResult) {
				if r.Sheet != "Oblisk" || !slices.Contains(r.Sheets, "Oblisk") {
					t.Errorf("add gave %+v", r)
				}
			},
		},
		{
			name: "rename",
			req:  service.SheetRequest{Action: service.SheetRename, Sheet: sheetstest.SecondSheet, Title: "Oblisk"},
			want: func(t *testing.T, r *service.SheetResult) {
				if !slices.Contains(r.Sheets, "Oblisk") || slices.Contains(r.Sheets, sheetstest.SecondSheet) {
					t.Errorf("rename gave %v", r.Sheets)
				}
			},
		},
		{
			name: "duplicate",
			req:  service.SheetRequest{Action: service.SheetDuplicate, Sheet: sheetstest.SecondSheet, Title: "Oblisk"},
			want: func(t *testing.T, r *service.SheetResult) {
				if r.Sheet != "Oblisk" || len(r.Sheets) != 4 {
					t.Errorf("duplicate gave %+v", r)
				}
			},
		},
		{
			name: "hide",
			req:  service.SheetRequest{Action: service.SheetHide, Sheet: sheetstest.SecondSheet},
			want: func(t *testing.T, r *service.SheetResult) {
				if !strings.Contains(r.Render(), "Hide") {
					t.Errorf("hide gave %q", r.Render())
				}
			},
		},
		{
			// To the end rather than to the front: the fixture's second
			// sheet is already at index 1, and a move that lands where
			// the sheet already was would pass whatever the code did.
			// Live, index 3 landed a sheet at 2.
			name: "reorder puts the sheet where it was asked for",
			req:  service.SheetRequest{Action: service.SheetReorder, Sheet: sheetstest.SecondSheet, Index: ptr(2)},
			want: func(t *testing.T, r *service.SheetResult) {
				if len(r.Sheets) != 3 || r.Sheets[2] != sheetstest.SecondSheet {
					t.Errorf("a move to index 2 gave %v", r.Sheets)
				}
			},
		},
		{
			name: "reorder to the front",
			req:  service.SheetRequest{Action: service.SheetReorder, Sheet: sheetstest.SecondSheet, Index: ptr(0)},
			want: func(t *testing.T, r *service.SheetResult) {
				if r.Sheets[0] != sheetstest.SecondSheet {
					t.Errorf("reorder gave %v", r.Sheets)
				}
			},
		},
		{
			name: "resize grows",
			req:  service.SheetRequest{Action: service.SheetResize, Sheet: sheetstest.SecondSheet, Rows: 500},
			want: func(t *testing.T, r *service.SheetResult) {
				if !strings.Contains(r.Render(), "500 rows") {
					t.Errorf("resize gave %q", r.Render())
				}
			},
		},
		{
			name: "freeze",
			req:  service.SheetRequest{Action: service.SheetFreeze, Sheet: sheetstest.SecondSheet, Rows: 1},
			want: func(t *testing.T, r *service.SheetResult) {
				if !strings.Contains(r.Render(), "Freeze 1 row") {
					t.Errorf("freeze gave %q", r.Render())
				}
			},
		},
		{
			name: "tab colour",
			req:  service.SheetRequest{Action: service.SheetTabColor, Sheet: sheetstest.SecondSheet, Colour: "#4a90d9"},
			want: func(t *testing.T, r *service.SheetResult) {
				if !strings.Contains(r.Render(), "#4a90d9") {
					t.Errorf("tab_color gave %q", r.Render())
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			res, err := svc.ManageSheet(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("ManageSheet: %v", err)
			}
			tc.want(t, res)
		})
	}
}

func TestManageSheetRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.SheetRequest
		want string
	}{
		{"an unknown action", service.SheetRequest{Action: "paint", Sheet: sheetstest.SecondSheet}, "is not one of"},
		{"add with no title", service.SheetRequest{Action: service.SheetAdd}, "needs a title"},
		{
			"a title already taken",
			service.SheetRequest{Action: service.SheetAdd, Title: sheetstest.FirstSheet},
			"already has a sheet",
		},
		{
			"resize that would shrink",
			service.SheetRequest{Action: service.SheetResize, Sheet: sheetstest.SecondSheet, Rows: 2},
			"edit_dimensions",
		},
		{
			"reorder with no index",
			service.SheetRequest{Action: service.SheetReorder, Sheet: sheetstest.SecondSheet},
			"needs index",
		},
		{
			"reorder past the end",
			service.SheetRequest{Action: service.SheetReorder, Sheet: sheetstest.SecondSheet, Index: ptr(9)},
			"outside 0..2",
		},
		{
			"a colour that is not one",
			service.SheetRequest{Action: service.SheetTabColor, Sheet: sheetstest.SecondSheet, Colour: "greenish"},
			"hex colour",
		},
		{
			"copy_to itself",
			service.SheetRequest{
				Action: service.SheetCopyTo, Sheet: sheetstest.SecondSheet, Destination: sheetstest.FixtureID,
			},
			"use duplicate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			_, err := svc.ManageSheet(context.Background(), tc.req)
			if err == nil {
				t.Fatal("it was allowed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, missing %q", err, tc.want)
			}
		})
	}
}

// A spreadsheet must keep one visible sheet, and hiding the last one
// leaves a file nobody can open properly.
func TestHidingTheLastVisibleSheetIsRefused(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	// The fixture has three sheets, one already hidden. Hide one more
	// and the next hide is the last visible.
	if _, err := svc.ManageSheet(ctx, service.SheetRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SheetHide, Sheet: sheetstest.SecondSheet,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ManageSheet(ctx, service.SheetRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SheetHide, Sheet: sheetstest.FirstSheet,
	})
	if err == nil || !strings.Contains(err.Error(), "only visible sheet") {
		t.Fatalf("hiding the last visible sheet gave %v", err)
	}
}

func TestCopyToAnotherSpreadsheet(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageSheet(context.Background(), service.SheetRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SheetCopyTo,
		Sheet: sheetstest.SecondSheet, Destination: sheetstest.SecondFixtureID,
	})
	if err != nil {
		t.Fatalf("copy_to: %v", err)
	}
	if res.Sheet == "" {
		t.Errorf("copy_to did not name the copy: %+v", res)
	}
}

// The metadata cache saves a request and must never cost correctness: a
// rename inside the window would otherwise leave the next call resolving
// a title that no longer exists.
func TestARenameIsVisibleImmediately(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	if _, err := svc.ManageSheet(ctx, service.SheetRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SheetRename,
		Sheet: sheetstest.SecondSheet, Title: "Oblisk",
	}); err != nil {
		t.Fatal(err)
	}
	card, err := svc.Card(ctx, sheetstest.FixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(card.Sheets, "Oblisk") {
		t.Errorf("the card still shows %v after a rename", card.Sheets)
	}
}

func TestManageSheetDryRunSendsNothing(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.ManageSheet(context.Background(), service.SheetRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SheetAdd, Title: "Oblisk", DryRun: true,
	})
	if err != nil {
		t.Fatalf("ManageSheet: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
		t.Errorf("a dry run did not say so:\n%s", res.Render())
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Error("a dry run reached the wire")
		}
	}
}

// Gated, and still needs confirm: nothing in Sheets brings a deleted
// sheet back, so the count comes before the question.
func TestDeleteSheetCountsBeforeItAsks(t *testing.T) {
	srv, svc := destructive(t)
	ctx := context.Background()

	_, err := svc.DeleteSheet(ctx, service.DeleteSheetRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("a delete without confirm gave %v", err)
	}
	if !strings.Contains(err.Error(), "non-empty cell") || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("the refusal does not count or offer the safer route: %q", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("an unconfirmed delete reached the wire")
		}
	}

	res, err := svc.DeleteSheet(ctx, service.DeleteSheetRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Confirm: true,
	})
	if err != nil {
		t.Fatalf("DeleteSheet: %v", err)
	}
	if res.Cells == 0 {
		t.Error("the result does not say what went with it")
	}
	if slices.Contains(res.Sheets, sheetstest.SecondSheet) {
		t.Errorf("the sheet is still listed: %v", res.Sheets)
	}
}

func TestDeleteSheetDryRunSendsNothing(t *testing.T) {
	srv, svc := destructive(t)
	res, err := svc.DeleteSheet(context.Background(), service.DeleteSheetRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, DryRun: true,
	})
	if err != nil {
		t.Fatalf("DeleteSheet: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "cannot undo") {
		t.Errorf("the preview does not say it is irreversible:\n%s", res.Render())
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Error("a dry run reached the wire")
		}
	}
}

func ptr(v int) *int { return &v }
