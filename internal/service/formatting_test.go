package service_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// batched reports whether a structural write reached the fake.
func batched(srv *sheetstest.Server) bool {
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			return true
		}
	}
	return false
}

// requests counts the calls that reached the fake, which is what the
// claim "an ordinary formatting call costs one request" is about.
func requests(srv *sheetstest.Server, op string) int {
	n := 0
	for _, c := range srv.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

func boolPtr(v bool) *bool { return &v }

// A format repeated down a column is one fact, and a thousand lines of
// it is none. The answer is per block.
func TestFormattingGroupsCellsIntoBlocks(t *testing.T) {
	srv := sheetstest.Standard(t)
	res, err := newService(t, srv).Formatting(context.Background(), service.FormattingRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D21",
	})
	if err != nil {
		t.Fatalf("Formatting: %v", err)
	}
	byRange := map[string]string{}
	for _, b := range res.Blocks {
		byRange[b.Range] = b.Format
	}
	// The heading row is four identically formatted cells, so it is one
	// block and not four.
	if got, ok := byRange["A1:D1"]; !ok {
		t.Errorf("the heading row is not one block; blocks were %v", byRange)
	} else if !strings.Contains(got, "bold") || !strings.Contains(got, "background") {
		t.Errorf("the heading block reads as %q", got)
	}
	// The money column is twenty rows of one format, and it runs
	// downwards rather than across.
	if got, ok := byRange["B2:B21"]; !ok {
		t.Errorf("the money column is not one block; blocks were %v", byRange)
	} else if !strings.Contains(got, "currency") {
		t.Errorf("the money block reads as %q", got)
	}
	// Cells with no format of their own are counted, not listed: they
	// are the background the blocks stand against.
	if !strings.Contains(res.Render(), "carry no format of their own") {
		t.Errorf("the footer does not count the unformatted cells:\n%s", res.Render())
	}
}

// A cell can be coloured by something that is not on the cell. An answer
// that listed only cell formats would describe a green column as plain.
func TestFormattingReportsWhatIsAttachedToTheRange(t *testing.T) {
	srv := sheetstest.Standard(t)
	res, err := newService(t, srv).Formatting(context.Background(), service.FormattingRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D26",
	})
	if err != nil {
		t.Fatalf("Formatting: %v", err)
	}
	text := res.Render()
	for what, want := range map[string]string{
		"the merge":         "A26:C26",
		"the banding":       "Banding",
		"the validation":    "one of list",
		"the note":          "Notes",
		"the protection":    "Protected",
		"the rule's test":   "number greater 500",
		"the rule's format": "index 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s is missing from the answer (%q):\n%s", what, want, text)
		}
	}
	// The index is how manage_range names a rule, so a rule reported
	// without one cannot be updated or deleted.
	if len(res.Rules) != 1 || !strings.HasPrefix(res.Rules[0], "index 0") {
		t.Errorf("rules = %v", res.Rules)
	}
}

// The cell budget bounds the read, and a caller who was cut short is
// told where it stopped rather than left with a partial answer that
// looks whole.
func TestFormattingSaysWhenTheBudgetCutIt(t *testing.T) {
	srv := sheetstest.Standard(t)
	res, err := newService(t, srv).Formatting(context.Background(), service.FormattingRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D26",
		Budget: service.Budget{Cells: 20},
	})
	if err != nil {
		t.Fatalf("Formatting: %v", err)
	}
	if !res.Truncated {
		t.Fatal("a 104-cell range read under a 20-cell budget did not report itself truncated")
	}
	if !strings.Contains(res.Render(), "cell budget stopped this") {
		t.Errorf("the footer does not say the budget cut it:\n%s", res.Render())
	}
}

func TestFormattingRefusesARangePastTheEnd(t *testing.T) {
	srv := sheetstest.Standard(t)
	_, err := newService(t, srv).Formatting(context.Background(), service.FormattingRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A500:B501",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a range past the end gave %v", err)
	}
}

// Everything set in one call is one atomic batch, so a header row that
// is bold, centred and shaded costs one request rather than three.
func TestFormatCellsSendsOneBatch(t *testing.T) {
	srv := sheetstest.Standard(t)
	res, err := newService(t, srv).FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
		Bold: boolPtr(true), Background: "#d9e2f3", Horizontal: "centre",
		NumberFormat: "currency", Borders: "1pt solid #cccccc", BorderSides: "outer",
	})
	if err != nil {
		t.Fatalf("FormatCells: %v", err)
	}
	if n := requests(srv, "spreadsheets.batchUpdate"); n != 1 {
		t.Errorf("%d batches were sent, and everything in one call belongs in one", n)
	}
	// The pre-read is the cost of the guard, and none of these ops can
	// destroy anything, so none is paid for here.
	if n := requests(srv, "spreadsheets.get"); n != 1 {
		t.Errorf("%d gets: an ordinary formatting call should cost the card read and nothing else", n)
	}
	for _, want := range []string{"bold — on", "background — #d9e2f3", "number format — currency", "borders — 1pt solid"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the result does not name %q:\n%s", want, res.Render())
		}
	}
}

// A merge keeps the top-left value and drops the rest, with nothing in
// the response saying so. It is the one formatting op that loses data.
func TestMergingOverValuesIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B2", Merge: "all",
	}
	_, err := svc.FormatCells(context.Background(), req)
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("merging over values gave %v", err)
	}
	if !strings.Contains(err.Error(), "B1") || !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("the refusal does not name the cells or the argument: %q", err)
	}
	if batched(srv) {
		t.Fatal("a refused merge reached the wire")
	}

	req.Overwrite = true
	if _, err := svc.FormatCells(context.Background(), req); err != nil {
		t.Fatalf("overwrite did not allow the merge: %v", err)
	}
	// Sheets discards the values it does not keep, so the fake does too:
	// asserting on the state afterwards is what catches a merge that was
	// reported and not made.
	doc := srv.Doc(sheetstest.FixtureID)
	sh := doc.Find(sheetstest.SecondSheet)
	if len(sh.Merges) != 1 {
		t.Errorf("%d merges after merging A1:B2", len(sh.Merges))
	}
	if sh.At(1, 2) != nil {
		t.Error("the merge kept a cell it should have discarded")
	}
}

// Clearing takes formatting Sheets cannot bring back, and only from the
// cells that carry one of their own.
func TestClearingFormatIsRefusedOverFormattedCells(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A2:D3", ClearFormat: true,
	}
	_, err := svc.FormatCells(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "formatting of its own") {
		t.Fatalf("clearing a formatted range gave %v", err)
	}
	req.Overwrite = true
	if _, err := svc.FormatCells(context.Background(), req); err != nil {
		t.Fatalf("overwrite did not allow the clear: %v", err)
	}
	if cell := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet).At(2, 2); cell.UserEnteredFormat != nil {
		t.Error("the clear left the format in place")
	}

	// A range whose cells carry no format of their own has nothing to
	// lose, so the guard stays out of the way.
	if _, err := svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1", ClearFormat: true,
	}); err != nil {
		t.Errorf("clearing an unformatted range was refused: %v", err)
	}
}

// A note is invisible in a values read, so replacing one is a loss the
// caller could not have seen coming.
func TestReplacingANoteIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A2", Note: "Vandel now",
	}
	_, err := svc.FormatCells(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "already has a note") {
		t.Fatalf("replacing a note gave %v", err)
	}
	req.Overwrite = true
	if _, err := svc.FormatCells(context.Background(), req); err != nil {
		t.Fatalf("overwrite did not allow it: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet).At(2, 1).Note; got != "Vandel now" {
		t.Errorf("the note is %q", got)
	}
	// Removing takes a note no read would have shown either, so it is
	// guarded the same way. Leaving it out is how a call clearing a
	// column's notes went through in silence.
	remove := service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A2", ClearNote: true,
	}
	if _, err := svc.FormatCells(context.Background(), remove); err == nil {
		t.Fatal("removing a note went through unacknowledged")
	}
	remove.Overwrite = true
	if _, err := svc.FormatCells(context.Background(), remove); err != nil {
		t.Fatalf("overwrite did not allow the removal: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet).At(2, 1).Note; got != "" {
		t.Errorf("the note survived removal: %q", got)
	}
	// Asking for both is a caller who meant one of them, and clear_note
	// used to win in silence while the guard checked the other.
	_, err = svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A3",
		Note: "Skerry", ClearNote: true,
	})
	if err == nil || !strings.Contains(err.Error(), "opposite things") {
		t.Errorf("note with clear_note gave %v", err)
	}
}

// A protection refuses formatting as surely as it refuses a write, and
// saying so before the call is the difference between a refusal the
// caller can act on and one from Google that names an id.
func TestFormattingAProtectedRangeIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	_, err := newService(t, srv).FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D1", Bold: boolPtr(false),
	})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("formatting a protected range gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a refused format reached the wire")
	}
}

func TestFormatCellsDryRunSendsNothingAndSaysWhatWouldStopIt(t *testing.T) {
	srv := sheetstest.Standard(t)
	res, err := newService(t, srv).FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B2",
		Merge: "all", DryRun: true,
	})
	if err != nil {
		t.Fatalf("a dry run was refused rather than answered: %v", err)
	}
	if !res.DryRun || batched(srv) {
		t.Fatal("a dry run reached the wire")
	}
	if !strings.Contains(res.Render(), "would be refused") || !strings.Contains(res.Render(), "overwrite") {
		t.Errorf("the preview does not say what would stop it:\n%s", res.Render())
	}
}

func TestFormatCellsRefusesWhatItCannotBuild(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	for name, req := range map[string]service.FormatRequest{
		"nothing to do":     {Range: "A1"},
		"a bare pattern":    {Range: "A1", NumberFormat: "#,##0.00"},
		"merge and unmerge": {Range: "A1:B2", Merge: "all", Unmerge: true},
		"a bad colour":      {Range: "A1", Background: "puce"},
		"a bad border":      {Range: "A1", Borders: "4pt solid"},
		"a bad side":        {Range: "A1", Borders: "1pt solid", BorderSides: "sideways"},
		"a bad alignment":   {Range: "A1", Horizontal: "top"},
		"a bad wrap":        {Range: "A1", Wrap: "fold"},
		"a bad merge type":  {Range: "A1:B2", Merge: "some"},
	} {
		req.Spreadsheet = sheetstest.FixtureID
		req.Sheet = sheetstest.SecondSheet
		_, err := svc.FormatCells(context.Background(), req)
		if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if batched(srv) {
		t.Error("a request that could not be built still reached the wire")
	}
}

// Clearing is applied before anything else in the same call, so "clear
// this and then make it bold" is one call that does both rather than a
// clear that undoes the bold.
func TestClearingComesBeforeSetting(t *testing.T) {
	srv := sheetstest.Standard(t)
	// The money column rather than the heading row: the headings are
	// protected, and a refusal there would pass this test for the wrong
	// reason.
	if _, err := newService(t, srv).FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "B2:B3",
		ClearFormat: true, Bold: boolPtr(true), Overwrite: true,
	}); err != nil {
		t.Fatalf("FormatCells: %v", err)
	}
	cell := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet).At(2, 2)
	if cell.UserEnteredFormat == nil || !cell.UserEnteredFormat.TextFormat.Bold {
		t.Error("the bold was cleared by the clear that was supposed to come first")
	}
	if cell.UserEnteredFormat.NumberFormat != nil {
		t.Error("the clear did not remove the number format that was there")
	}
}

// A method check rather than a count: the formatting path must never
// reach values.update, whose repeatability rules are different.
func TestFormattingNeverWritesValues(t *testing.T) {
	srv := sheetstest.Standard(t)
	if _, err := newService(t, srv).FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1", Italic: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range srv.Calls() {
		if c.Method == http.MethodPut {
			t.Errorf("formatting sent a %s to %s", c.Method, c.Op)
		}
	}
}

// Sheets refuses a merge that spans the edge of a frozen band, with a
// message that says the rule and not where the edge is. The counts are
// on the card this call has already read, so the refusal says which
// column the freeze ends at and how to undo it.
func TestMergingAcrossAFrozenEdgeIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	// The fixture's first sheet freezes one row.
	_, err := svc.FormatCells(ctx, service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A2:A3",
		Merge: "all", Overwrite: true,
	})
	if err != nil {
		t.Fatalf("a merge below the frozen row was refused: %v", err)
	}
	// Columns E and F, clear of the protected heading row, so this is
	// about the freeze and nothing else.
	_, err = svc.FormatCells(ctx, service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "E1:F3",
		Merge: "all", Overwrite: true,
	})
	if err == nil || !strings.Contains(err.Error(), "frozen row") {
		t.Fatalf("a merge spanning the frozen row gave %v", err)
	}
	if !strings.Contains(err.Error(), "manage_sheet freeze") {
		t.Errorf("the refusal does not say how to undo the freeze: %q", err)
	}
	// Merging by rows keeps every row separate, so it never spans the
	// frozen row edge and is not held back.
	if _, err := svc.FormatCells(ctx, service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "E1:F3",
		Merge: "rows", Overwrite: true,
	}); err != nil {
		t.Errorf("a row-wise merge across the frozen edge was refused: %v", err)
	}
}
