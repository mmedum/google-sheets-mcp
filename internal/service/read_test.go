package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestReadShowsAddresses(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D3",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(res.Grid, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("the grid has %d lines:\n%s", len(lines), res.Grid)
	}
	// Column letters across the top, row numbers down the side. That is
	// what lets a model write back to what it just read without
	// counting, so it is asserted rather than assumed.
	header := lines[0]
	for _, col := range []string{"A", "B", "C", "D"} {
		if !strings.Contains(header, col) {
			t.Errorf("the header line has no column %s: %q", col, header)
		}
	}
	for i, want := range []string{"1 |", "2 |", "3 |"} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("row line %d = %q, want a %q gutter", i+1, lines[i+1], want)
		}
	}
	if !strings.Contains(res.Grid, "Plimth") {
		t.Errorf("the heading row is missing:\n%s", res.Grid)
	}
	// The range the server reports is the one it sent: quoted, and with
	// the sheet resolved.
	if res.Range != a1.Format(sheetstest.FirstSheet, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 3}) {
		t.Errorf("Range = %q", res.Range)
	}
	if !strings.HasPrefix(res.Checkpoint, "ck_") {
		t.Errorf("no checkpoint: %q", res.Checkpoint)
	}
	if !strings.Contains(res.Render(), res.Grid) {
		t.Error("the text half does not carry the grid")
	}
	if !strings.Contains(res.Render(), res.Checkpoint) {
		t.Error("the text half does not carry the checkpoint; a text-only client could not use one")
	}
}

func TestReadShowsFormulasUnderValues(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	// A formula and its result render identically, so a values read
	// cannot tell them apart. That is the whole reason for `both`.
	values, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(values.Grid, "=B2+C2") {
		t.Errorf("a values read showed a formula:\n%s", values.Grid)
	}

	both, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3", Show: "both",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(both.Grid, "=B2+C2") {
		t.Errorf("show=both did not show the formula:\n%s", both.Grid)
	}

	formulas, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3", Show: "formulas",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formulas.Grid, "=B2+C2") {
		t.Errorf("show=formulas did not show the formula:\n%s", formulas.Grid)
	}
}

func TestReadShowsErrorCells(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D22:D22",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Grid, "#DIV/0!") {
		t.Errorf("an error cell rendered as %q", res.Grid)
	}
}

// TestOpenEndedRangeIsResolvedBeforeTheCall is the bug this design is
// avoiding rather than repeating: a shipped server clamped what it
// displayed to 50 rows while still fetching A:Z whole, and its
// maintainer's own note is that the memory was unbounded the entire
// time. The window is resolved against the sheet's real extent and a
// finite range is what goes on the wire.
func TestOpenEndedRangeIsResolvedBeforeTheCall(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(5000, 20)
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:T", Budget: service.Budget{Cells: 200},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	var sent string
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.get" && c.Query.Get("includeGridData") == "true" {
			sent = c.Query.Get("ranges")
		}
	}
	if sent == "" {
		t.Fatal("no grid read reached the fake")
	}
	ref, err := a1.Parse(sent)
	if err != nil {
		t.Fatalf("the range sent does not parse: %q", sent)
	}
	if !ref.Rect.Bounded() {
		t.Fatalf("an unbounded range reached the wire: %q", sent)
	}
	cells, _ := ref.Rect.Cells()
	if cells > 200 {
		t.Errorf("the request asked for %d cells against a budget of 200 (%q)", cells, sent)
	}
	if !res.Truncated || res.ContinueFrom == 0 {
		t.Errorf("a budgeted read must say it was cut and where to continue: %+v", res)
	}
	if !strings.Contains(res.Footer, "continue at row") {
		t.Errorf("the footer does not say how to continue: %q", res.Footer)
	}
}

func TestReadContinues(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(200, 4)
	srv.Add(doc, file)
	svc := newService(t, srv)
	ctx := context.Background()

	first, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:D200", Budget: service.Budget{Cells: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || first.ContinueFrom != 11 {
		t.Fatalf("first read: truncated=%v continue_from=%d, want true and 11", first.Truncated, first.ContinueFrom)
	}
	second, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:D200", Budget: service.Budget{Cells: 40}, ContinueFrom: first.ContinueFrom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.Grid, "11 |") {
		t.Errorf("the continuation did not start at row 11:\n%s", second.Grid)
	}
	if second.ContinueFrom != 21 {
		t.Errorf("second read continues at %d, want 21", second.ContinueFrom)
	}

	// Continuing past the end is stale rather than empty: an empty grid
	// looks like an answer.
	if _, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:D200", ContinueFrom: 5000,
	}); err == nil || !strings.HasPrefix(err.Error(), "[stale]") {
		t.Errorf("continuing past the end gave %v", err)
	}
	if _, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A10:D200", ContinueFrom: 2,
	}); err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("continuing before the start gave %v", err)
	}
}

func TestRaggedResponsesKeepTheirAddresses(t *testing.T) {
	// The API omits trailing empty rows and trailing empty cells, so a
	// response is routinely smaller than the range it answers. If the
	// grid were not padded back out, every address after a gap would be
	// wrong, which is worse than not answering.
	_, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:C5",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(res.Grid, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("want a header and five rows, got %d lines:\n%s", len(lines), res.Grid)
	}
	// Row 3 has nothing in column A and something in column B.
	row3 := lines[3]
	if !strings.Contains(row3, "3 |") || !strings.Contains(row3, "7.5") {
		t.Errorf("row 3 = %q", row3)
	}
	if !strings.Contains(res.Footer, "data ends at row 3") {
		t.Errorf("the footer does not say where the data ended: %q", res.Footer)
	}
}

func TestReadAnnotationsAreListedNotHidden(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D4",
		IncludeNotes: true, IncludeValidation: true, IncludeMerges: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"notes: A2", "validation: A3", "protected:"} {
		if !strings.Contains(res.Grid, want) {
			t.Errorf("the grid does not carry %q:\n%s", want, res.Grid)
		}
	}
	if !strings.Contains(res.Grid, "you may not edit it") {
		t.Errorf("a protected range the account cannot edit must say so:\n%s", res.Grid)
	}
}

func TestReadFormats(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	for _, tc := range []struct {
		format string
		want   string
	}{
		{service.FormatCSV, "Plimth,Nardle"},
		{service.FormatTSV, "Plimth\tNardle"},
		{service.FormatJSON, `["Plimth","Nardle"`},
	} {
		res, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2", Format: tc.format,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if !strings.Contains(res.Render(), tc.want) {
			t.Errorf("%s text half = %q, want %q in it", tc.format, res.Render(), tc.want)
		}
		if len(res.Rows) == 0 {
			t.Errorf("%s did not fill rows for a structured client", tc.format)
		}
		// The structured half still carries the addressed grid: that is
		// what stops a client that shows only structure from losing the
		// addresses.
		if !strings.Contains(res.Grid, "A") || !strings.Contains(res.Grid, "1 |") {
			t.Errorf("%s lost the addressed grid from the structured half:\n%s", tc.format, res.Grid)
		}
	}
	if _, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2", Format: "xml",
	}); err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("an unknown format gave %v", err)
	}
}

func TestFormattedIsOptOut(t *testing.T) {
	// A currency symbol inside a number the model may want to compute
	// with is a trap, so unformatted is the default and formatted is a
	// choice the caller makes.
	_, svc := standard(t)
	ctx := context.Background()
	raw, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "B2:B2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.Grid, "42") {
		t.Errorf("raw read = %q", raw.Grid)
	}
	formatted, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A3:B3", Formatted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted.Grid, "7.50") {
		t.Errorf("formatted read = %q", formatted.Grid)
	}
}

func TestCharacterBudgetCutsAtARowBoundary(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(100, 6)
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:F100", Budget: service.Budget{Chars: 400},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.ContinueFrom == 0 {
		t.Fatalf("the character budget did not cut: %+v", res)
	}
	if len(res.Grid) > 600 {
		t.Errorf("the rendering is %d characters against a budget of 400", len(res.Grid))
	}
	// Whole rows only: half a row would have addresses pointing at
	// values that are not there.
	for _, line := range strings.Split(strings.TrimRight(res.Grid, "\n"), "\n")[1:] {
		if strings.Count(line, "|") != 6 {
			t.Errorf("a partial row survived the cut: %q", line)
		}
	}
}

// TestTheBudgetBindsEveryFormat is the half a format could escape. The
// text half, the machine rows and the footer all have to describe the
// same window: a json read that returns every row under a footer saying
// it was cut is a result contradicting its own description, and a caller
// that believed either half would be wrong about the other.
func TestTheBudgetBindsEveryFormat(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(200, 4)
	srv.Add(doc, file)
	svc := newService(t, srv)
	ctx := context.Background()

	for _, format := range []string{service.FormatGrid, service.FormatJSON, service.FormatCSV, service.FormatTSV} {
		t.Run(format, func(t *testing.T) {
			res, err := svc.Read(ctx, service.ReadRequest{
				Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:D200", Budget: service.Budget{Chars: 300}, Format: format,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Truncated || res.ContinueFrom == 0 {
				t.Fatalf("%s was not reported as cut: %+v", format, res)
			}
			shown := res.ContinueFrom - 1
			if format == service.FormatGrid {
				if len(res.Rows) != 0 {
					t.Errorf("grid carried machine rows nobody asked for")
				}
				return
			}
			if len(res.Rows) != shown {
				t.Errorf("%s carried %d rows while the footer says the read stopped after row %d",
					format, len(res.Rows), shown)
			}
			// And the structured grid describes the same window.
			if strings.Contains(res.Grid, fmt.Sprintf("\n%d |", res.ContinueFrom)) {
				t.Errorf("%s: the grid shows row %d, which the footer calls the first row not shown", format, res.ContinueFrom)
			}
		})
	}
}

func TestFormatIsRefusedBeforeAnythingIsSent(t *testing.T) {
	// A typo should not cost a request against a per-minute quota.
	srv, svc := standard(t)
	srv.Reset()
	_, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2", Format: "xml",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("an unknown format gave %v", err)
	}
	if n := len(srv.Calls()); n != 0 {
		t.Errorf("a refused format still cost %d request(s)", n)
	}
}

// TestARangePastTheEndIsRefusedNotClamped is the difference between an
// answer and a plausible answer. Clamping pulls a rectangle back inside
// the sheet, which is right when it overlaps and wrong when it misses:
// A50:B60 on a ten-row sheet would come back holding row ten.
func TestARangePastTheEndIsRefusedNotClamped(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(10, 3)
	srv.Add(doc, file)
	svc := newService(t, srv)
	ctx := context.Background()

	_, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A50:B60",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a range past the last row gave %v", err)
	}
	if !strings.Contains(err.Error(), "10 rows") {
		t.Errorf("the refusal does not say how big the sheet is: %v", err)
	}
	if _, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "Z1:Z5",
	}); err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("a range past the last column gave %v", err)
	}

	// A rectangle that overlaps the sheet is still clamped, because it
	// has an answer.
	res, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A8:C60",
	})
	if err != nil {
		t.Fatalf("an overlapping range was refused: %v", err)
	}
	if !strings.Contains(res.Range, "C10") {
		t.Errorf("the overlapping range was not clamped to the sheet: %q", res.Range)
	}
}

// TestAnnotationsCoverOnlyTheRowsShown keeps a note at A417 from being
// listed under a grid that stops at row 3 — and from being listed again
// by the continuation that does show it.
func TestAnnotationsCoverOnlyTheRowsShown(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D10",
		IncludeNotes: true, IncludeValidation: true, Budget: service.Budget{Chars: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("the read was not cut, so this proves nothing")
	}
	// The note is on A2 and the validation on A3; a cut at row 2 must
	// not list the validation.
	if res.ContinueFrom <= 3 && strings.Contains(res.Grid, "validation: A3") {
		t.Errorf("row 3's validation is listed under a grid that stops at row %d:\n%s", res.ContinueFrom-1, res.Grid)
	}
}
