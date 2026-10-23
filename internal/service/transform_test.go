package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

func transformReq(action, rangeA1 string) service.TransformRequest {
	return service.TransformRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
		Range: rangeA1, Action: action,
	}
}

// A sort key is a sheet column, not an offset into the range. Sorting a
// range that starts at B on "B desc" has to reorder by that column, and
// a server that sent the offset would sort by the wrong one and look
// like it worked.
func TestSortOrdersByTheColumnNamed(t *testing.T) {
	srv := sheetstest.Standard(t)
	// Under the heading row, which is protected: a sort across it would
	// be refused, and this test is about the ordering.
	req := transformReq(service.TransformSort, "A2:D21")
	req.Sheet = sheetstest.FirstSheet
	req.SortBy = "B desc"
	res, err := newService(t, srv).Transform(context.Background(), req)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !strings.Contains(res.Render(), "no longer points where it did") {
		t.Errorf("a sort did not say the addresses moved:\n%s", res.Render())
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet)
	last := 1e18
	for row := 2; row <= 21; row++ {
		cell := sh.At(row, 2)
		if cell == nil || cell.EffectiveValue == nil || cell.EffectiveValue.NumberValue == nil {
			continue
		}
		if *cell.EffectiveValue.NumberValue > last {
			t.Fatalf("row %d is out of order: %v after %v", row, *cell.EffectiveValue.NumberValue, last)
		}
		last = *cell.EffectiveValue.NumberValue
	}
}

// A sort key outside the range is a request Google refuses with a
// message naming an index the caller never typed.
func TestSortKeyMustBeInsideTheRange(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformSort, "A1:B3")
	req.SortBy = "D asc"
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("a sort key outside the range gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a refused sort reached the wire")
	}
}

func TestFindReplaceReportsWhatItChanged(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformFindReplace, "A1:B3")
	req.Find = "Trennow"
	req.Replace = "Grivet"
	res, err := newService(t, srv).Transform(context.Background(), req)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("the result says %d occurrences changed", res.Changed)
	}
	if !strings.Contains(res.Render(), "occurrence(s) replaced") {
		t.Errorf("the count the API reported is missing:\n%s", res.Render())
	}
	cell := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(1, 1)
	if cell == nil || *cell.EffectiveValue.StringValue != "Grivet" {
		t.Errorf("the cell holds %+v", cell)
	}
}

// Replacing inside formula text changes what a cell computes rather than
// what it shows, which is the invisible half of this action.
func TestReplacingInsideFormulasNeedsAcknowledging(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := transformReq(service.TransformFindReplace, "A2:D3")
	req.Sheet = sheetstest.FirstSheet
	req.Find = "B2"
	req.Replace = "B3"
	req.InFormulas = true
	_, err := svc.Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "overwrite_formulas") {
		t.Fatalf("replacing inside formulas gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a refused replacement reached the wire")
	}

	// Without in_formulas the formulas are not touched, so nothing is
	// held back: the guard is about the act, not about the range.
	values := req
	values.InFormulas = false
	if _, err := svc.Transform(context.Background(), values); err != nil {
		t.Errorf("a values-only replacement was refused: %v", err)
	}

	req.Overwrite, req.OverwriteFormulas = true, true
	if _, err := svc.Transform(context.Background(), transformWitnessed(t, svc, req)); err != nil {
		t.Errorf("the acknowledgments did not allow it: %v", err)
	}
}

// transformWitnessed is req with the checkpoint of the cells its guard
// reads, from a dry run of the same call.
func transformWitnessed(t *testing.T, svc *service.Service, req service.TransformRequest) service.TransformRequest {
	t.Helper()
	dry := req
	dry.DryRun = true
	res, err := svc.Transform(context.Background(), dry)
	if err != nil {
		t.Fatalf("a dry run for the checkpoint: %v", err)
	}
	if res.Checkpoint == "" {
		t.Fatal("the dry run gave no checkpoint")
	}
	req.ExpectCheckpoint = res.Checkpoint
	return req
}

// An overwrite here replaces cells the caller never named, so it is held
// to the cells the guard read: where a split spills, here. Without
// expect_checkpoint it is refused with the checkpoint, the guard's own
// refusal and the dry run hand it over, and a change to those cells
// after it was read is a conflict.
func TestATransformOverwriteIsHeldToWhatItRead(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	doc := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	doc.Set(1, 1, sheetstest.Str("Quorbin,Vandel"))
	req := transformReq(service.TransformTextToColumns, "A1:A2")
	req.Delimiter = "comma"

	dry := req
	dry.DryRun = true
	preview, err := svc.Transform(ctx, dry)
	if err != nil {
		t.Fatal(err)
	}
	ck := preview.Checkpoint
	if !strings.Contains(preview.Render(), "'Ürväl'!B1:B2 reads "+ck+" now, which overwrite needs as expect_checkpoint.\n") {
		t.Errorf("the dry run does not give the checkpoint:\n%s", preview.Render())
	}
	if _, err := svc.Transform(ctx, req); err == nil || !strings.Contains(err.Error(),
		"; pass overwrite to allow it. Pass expect_checkpoint "+ck+" with it: that is 'Ürväl'!B1:B2 as this call read it") {
		t.Errorf("the guard's refusal = %v", err)
	}
	req.Overwrite = true
	want := "[invalid] overwrite needs expect_checkpoint, so that what it replaces is what was read: 'Ürväl'!B1:B2 " +
		"reads " + ck + " now. Pass that, and a change there before the write is refused"
	if _, err := svc.Transform(ctx, req); err == nil || err.Error() != want {
		t.Errorf("an overwrite with no witness:\n got %v\nwant %s", err, want)
	}
	if batched(srv) {
		t.Fatal("a refused split reached the wire")
	}
	doc.Set(1, 2, sheetstest.Str("Skerry"))
	req.ExpectCheckpoint = ck
	if _, err := svc.Transform(ctx, req); err == nil || !strings.HasPrefix(err.Error(), "[conflict]") {
		t.Errorf("a stale witness gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a split over changed cells reached the wire")
	}
}

// An action that reads nothing before it writes has no checkpoint to
// compare, so one passed to it is refused rather than ignored.
func TestATransformThatReadsNothingTakesNoCheckpoint(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformTrim, "A1:B3")
	req.ExpectCheckpoint = "ck_0123456789ab"
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || err.Error() != "[invalid] trim_whitespace reads no cells before it writes, so there is no "+
		"checkpoint to compare; leave expect_checkpoint out" {
		t.Errorf("error = %v", err)
	}
}

// A replacement that would write a formula reaching outside the
// spreadsheet is refused as any other write's is, whether or not it
// searches formulas, and no argument lifts it.
func TestAReplacementCannotWriteAnExternalFormula(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	for _, inFormulas := range []bool{false, true} {
		req := transformReq(service.TransformFindReplace, "A1:B3")
		req.Find, req.Replace, req.InFormulas = "Plimth", `=IMPORTXML("https://example.test/","//p")`, inFormulas
		req.Overwrite, req.OverwriteFormulas = true, true
		_, err := svc.Transform(context.Background(), req)
		if err == nil || !strings.HasPrefix(err.Error(), "[blocked] the replacement text would hold a formula that "+
			"fetches a URL from Google's servers") || strings.Contains(err.Error(), "pass ") {
			t.Errorf("in_formulas %v: %v", inFormulas, err)
		}
	}
	if batched(srv) {
		t.Fatal("a refused replacement reached the wire")
	}
}

func TestTrimAndDedupeReportTheirCounts(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	seed := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	seed.Set(4, 1, sheetstest.Str("  Skerry   spaced  "))
	seed.Set(5, 1, sheetstest.Str("Trennow"))
	seed.Set(5, 2, sheetstest.Str("Bractal"))

	trimmed, err := svc.Transform(ctx, transformReq(service.TransformTrim, "A1:B5"))
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if trimmed.Changed != 1 {
		t.Errorf("trim changed %d cells", trimmed.Changed)
	}
	// Read back after the call: a batch is applied to a copy and swapped
	// in, so the sheet from before it is the state from before it.
	if got := *srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(4, 1).EffectiveValue.StringValue; got != "Skerry spaced" {
		t.Errorf("the trimmed cell holds %q", got)
	}

	// Row 5 now repeats row 1, so a de-duplication over A:B removes one.
	deduped, err := svc.Transform(ctx, transformReq(service.TransformDedupe, "A1:B5"))
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}
	if deduped.Changed != 1 {
		t.Errorf("dedupe removed %d rows", deduped.Changed)
	}
	if !strings.Contains(deduped.Render(), "The rows moved") {
		t.Errorf("a dedupe did not say the addresses moved:\n%s", deduped.Render())
	}
}

func TestDedupeColumnsMustBeInsideTheRange(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformDedupe, "A1:B3")
	req.Columns = "D"
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("a comparison column outside the range gave %v", err)
	}
}

// A split spills into the columns to its right, which the caller never
// named. That is the destination this guard exists for.
func TestTextToColumnsGuardsWhatIsToTheRight(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 1, sheetstest.Str("Quorbin,Vandel"))

	req := transformReq(service.TransformTextToColumns, "A1:A2")
	req.Delimiter = "comma"
	_, err := svc.Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "B1") {
		t.Fatalf("a split over an occupied neighbor gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a refused split reached the wire")
	}

	req.Overwrite = true
	if _, err := svc.Transform(context.Background(), transformWitnessed(t, svc, req)); err != nil {
		t.Fatalf("overwrite did not allow the split: %v", err)
	}
	if got := *srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(1, 2).EffectiveValue.StringValue; got != "Vandel" {
		t.Errorf("the second column holds %q", got)
	}
}

// Nothing in the column holds the delimiter, so the split lands nowhere
// but where it started and the guard has nothing to say.
func TestASplitThatCannotSpillIsNotBlocked(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformTextToColumns, "A1:A2")
	req.Delimiter = "|"
	if _, err := newService(t, srv).Transform(context.Background(), req); err != nil {
		t.Fatalf("a split with no delimiter in the data was refused: %v", err)
	}
}

func TestTextToColumnsSplitsOneColumnOnly(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformTextToColumns, "A1:B2")
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "splits one column") {
		t.Fatalf("a two-column split gave %v", err)
	}
}

// A paste lands on cells the caller never named, so the destination is
// read and refused first.
func TestCopyPasteGuardsItsDestination(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := transformReq(service.TransformCopyPaste, "A1:B2")
	req.Destination = "A2"
	_, err := svc.Transform(context.Background(), req)
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("a paste onto occupied cells gave %v", err)
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("the refusal does not say what is in the way: %q", err)
	}

	req.Destination = "D10"
	res, err := svc.Transform(context.Background(), req)
	if err != nil {
		t.Fatalf("a paste onto empty cells was refused: %v", err)
	}
	if res.Destination != "'"+sheetstest.SecondSheet+"'!D10:E11" {
		t.Errorf("the result says it landed on %q", res.Destination)
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if sh.At(10, 4) == nil || *sh.At(10, 4).EffectiveValue.StringValue != "Trennow" {
		t.Errorf("the copy did not land: %+v", sh.At(10, 4))
	}
	// The source is left alone: that is what makes it a copy.
	if sh.At(1, 1) == nil {
		t.Error("a copy emptied its source")
	}
}

// A cut empties the cells it came from, and the result says so.
func TestCutPasteMovesAndSaysSo(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformCutPaste, "A1:B1")
	req.Destination = "D10"
	res, err := newService(t, srv).Transform(context.Background(), req)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !strings.Contains(res.Render(), "is now blank") {
		t.Errorf("a cut did not say it emptied its source:\n%s", res.Render())
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if sh.At(1, 1) != nil {
		t.Error("the source was not emptied")
	}
	if sh.At(10, 4) == nil {
		t.Error("the cells did not arrive")
	}
}

// A destination on another sheet is named the way every range is, so a
// missing sheet is refused with the titles that exist rather than with
// an id.
func TestAPasteCanCrossSheets(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	req := transformReq(service.TransformCopyPaste, "A1:B1")
	req.Destination = a1.QuoteSheet(sheetstest.ApostropheName) + "!C5"
	if _, err := svc.Transform(context.Background(), req); err != nil {
		t.Fatalf("a cross-sheet copy was refused: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.ApostropheName).At(5, 3); got == nil {
		t.Error("the copy did not reach the other sheet")
	}
	req.Destination = "'Nardle'!A1"
	_, err := svc.Transform(context.Background(), req)
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a destination on a sheet that does not exist gave %v", err)
	}
}

func TestAPastePastTheEndIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformCopyPaste, "A1:B2")
	req.Destination = "H49"
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("a paste past the end gave %v", err)
	}
}

func TestTransformRefusesWhatItCannotBuild(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	for name, req := range map[string]service.TransformRequest{
		"an unknown action":          {Action: "pivot", Range: "A1:B2"},
		"a sort with no key":         {Action: service.TransformSort, Range: "A1:B2"},
		"a replacement with no find": {Action: service.TransformFindReplace, Range: "A1:B2"},
		"a copy with no destination": {Action: service.TransformCopyPaste, Range: "A1:B2"},
		"a fill with no length":      {Action: service.TransformAutoFill, Range: "A1:B2"},
		"a bad paste type": {
			Action: service.TransformCopyPaste, Range: "A1:B2", Destination: "D10", Paste: "everything",
		},
		"a whole column as a destination": {
			Action: service.TransformCopyPaste, Range: "A1:B2", Destination: "D:D",
		},
		"a bad delimiter": {Action: service.TransformTextToColumns, Range: "A1:A2", Delimiter: "::"},
	} {
		req.Spreadsheet = sheetstest.FixtureID
		req.Sheet = sheetstest.SecondSheet
		_, err := svc.Transform(context.Background(), req)
		if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if batched(srv) {
		t.Error("a request that could not be built still reached the wire")
	}
}

func TestAutoFillAndRandomizeReachTheWire(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	fill := transformReq(service.TransformAutoFill, "A1:A2")
	fill.FillRows = true
	fill.FillLength = 3
	if _, err := svc.Transform(ctx, fill); err != nil {
		t.Fatalf("auto_fill: %v", err)
	}
	if _, err := svc.Transform(ctx, transformReq(service.TransformRandomize, "A1:B3")); err != nil {
		t.Fatalf("randomize: %v", err)
	}
	if n := requests(srv, "spreadsheets.batchUpdate"); n != 2 {
		t.Errorf("%d batches for two transforms", n)
	}
}

// An autofill writes into the cells past its source, which the caller
// never named: the same destination guard a paste gets.
func TestAutoFillGuardsWhereItWouldLand(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformAutoFill, "A1:A1")
	req.FillRows = true
	req.FillLength = 2
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a fill over occupied cells gave %v", err)
	}
}

func TestTransformDryRunSendsNothing(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformCopyPaste, "A1:B2")
	req.Destination = "A2"
	req.DryRun = true
	res, err := newService(t, srv).Transform(context.Background(), req)
	if err != nil {
		t.Fatalf("a dry run was refused rather than answered: %v", err)
	}
	if !res.DryRun || batched(srv) {
		t.Fatal("a dry run reached the wire")
	}
	if !strings.Contains(res.Render(), "would be refused") {
		t.Errorf("the preview does not say what would stop it:\n%s", res.Render())
	}
}

// A guard read has to fit in one request, wherever it is. Two of the
// three had no cap: an unbounded range clamps to the whole sheet, so a
// find_replace with in_formulas and a split on a tall column both read
// every cell on it to decide what to refuse.
func TestEveryGuardReadIsCapped(t *testing.T) {
	// Tall and narrow, so one column on its own is past what a single
	// read covers — which is the shape an unbounded range takes.
	srv := sheetstest.New(t)
	srv.Add(sheetstest.Large(60000, 2))
	svc := newService(t, srv)
	large := "1SyntheticFixtureLargeSheetIdXXXXXXXXXXXXXXXX"

	for name, req := range map[string]service.TransformRequest{
		"a replacement inside formulas over a whole sheet": {
			Action: service.TransformFindReplace, Range: "A:B",
			Find: "Quorbin", Replace: "Vandel", InFormulas: true,
		},
		"a split down a whole column": {
			Action: service.TransformTextToColumns, Range: "A:A", Delimiter: "comma",
		},
		"a paste onto a whole sheet": {
			Action: service.TransformCopyPaste, Range: "A:B", Destination: "A1",
		},
	} {
		req.Spreadsheet = large
		req.Sheet = "Bractal"
		_, err := svc.Transform(context.Background(), req)
		if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
			t.Errorf("%s gave %v", name, err)
		} else if !strings.Contains(err.Error(), "in parts") && !strings.Contains(err.Error(), "past the end") {
			t.Errorf("%s did not say what to do instead: %q", name, err)
		}
	}
}

// A fill lands on the rows past its source, and a sheet ends before a
// spreadsheet does. Checked against the sheet's own size, the refusal
// names it and the tool that grows it; checked against the ten-million
// ceiling, the caller got Google's "exceeds grid limits" instead.
func TestAutoFillStopsAtTheEndOfTheSheet(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := transformReq(service.TransformAutoFill, "A49:A50")
	req.FillRows = true
	req.FillLength = 5
	_, err := newService(t, srv).Transform(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "past the end of") {
		t.Fatalf("a fill past the last row gave %v", err)
	}
	if !strings.Contains(err.Error(), "manage_sheet resize") {
		t.Errorf("the refusal does not say what grows the sheet: %q", err)
	}
	if batched(srv) {
		t.Fatal("a refused fill reached the wire")
	}
}

// A paste lands on cells the caller never named, so it is the other
// place a write can arrive in a pivot table's output (§17a.27).
func TestCopyPasteIntoPivotOutputNamesThePivot(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	req := transformReq(service.TransformCopyPaste, "A1:A1")
	req.Destination = "'" + sheetstest.FirstSheet + "'!G3"
	_, err := svc.Transform(context.Background(), req)
	if err == nil {
		t.Fatal("a paste into a pivot's output was allowed")
	}
	if !strings.Contains(err.Error(), "pivot table anchored at F1") {
		t.Errorf("the refusal does not name the pivot:\n%s", err)
	}
}

// A paste, a cut, a fill and a split each land on cells the caller never
// named. A smart chip there reads as its text, so "not empty" alone would
// not say that overwrite erases a person or a file link.
func TestALandingOnASmartChipNamesIt(t *testing.T) {
	for _, tc := range []struct {
		action   string
		rangeA1  string
		row, col int
		set      func(*service.TransformRequest)
		want     string
	}{
		{service.TransformCopyPaste, "A1:B2", 10, 4, func(r *service.TransformRequest) { r.Destination = "D10" }, "D10 holds a smart chip"},
		{service.TransformCutPaste, "A1:B2", 10, 4, func(r *service.TransformRequest) { r.Destination = "D10" }, "D10 holds a smart chip"},
		{service.TransformAutoFill, "B3:B3", 4, 2, func(r *service.TransformRequest) { r.FillRows, r.FillLength = true, 2 }, "B4 holds a smart chip"},
		{service.TransformTextToColumns, "A1:A2", 1, 2, func(r *service.TransformRequest) { r.Delimiter = "comma" }, "B1 holds a smart chip"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			srv := sheetstest.Standard(t)
			sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
			sh.Set(1, 1, sheetstest.Str("Quorbin,Vandel"))
			chip := sheetstest.Str("Jane Doe")
			chip.ChipRuns = []gsheets.ChipRun{{Chip: &gsheets.Chip{
				PersonProperties: &gsheets.PersonProperties{Email: "janedoe@example.test"},
			}}}
			sh.Set(tc.row, tc.col, chip)

			req := transformReq(tc.action, tc.rangeA1)
			tc.set(&req)
			_, err := newService(t, srv).Transform(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s onto a chip gave %v", tc.action, err)
			}
			if batched(srv) {
				t.Fatal("a refused write reached the wire")
			}
		})
	}
}
