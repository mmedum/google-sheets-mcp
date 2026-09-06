package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func anchorOn(t *testing.T, svc *service.Service, name, rng string) *service.AnchorResult {
	t.Helper()
	res, err := svc.Anchors(context.Background(), service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: name, Sheet: sheetstest.FirstSheet, Range: rng,
	})
	if err != nil {
		t.Fatalf("anchoring %q at %s: %v", name, rng, err)
	}
	return res
}

// The whole feature in one test: an anchor is worth having only because
// it survives what an A1 address does not.
func TestAnchorFollowsItsRowThroughEdits(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	anchorOn(t, svc, "invoice totals", "24:24")

	where := func() string {
		t.Helper()
		res, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorList,
		})
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		for _, a := range res.Anchors {
			if a.Name == "invoice totals" {
				return a.Range
			}
		}
		return "gone"
	}
	start := where()
	if !strings.Contains(start, "24") {
		t.Fatalf("the anchor starts at %q, want row 24", start)
	}

	// Ten rows inserted above it. An address of row 24 now points at
	// somebody else's data; the anchor does not.
	if _, err := svc.EditDimensions(ctx, service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimInsert, Dimension: "rows", Band: "1:10",
	}); err != nil {
		t.Fatalf("inserting rows: %v", err)
	}
	if got := where(); !strings.Contains(got, "34") {
		t.Errorf("after inserting 10 rows above it the anchor is at %q, want row 34. An anchor that keeps a row "+
			"number is an A1 address with extra steps", got)
	}

	// And a read through the anchor lands on the row, not on the number
	// it was created with.
	read, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "invoice totals",
	})
	if err != nil {
		t.Fatalf("reading through the anchor: %v", err)
	}
	if !strings.Contains(read.Range, "34") {
		t.Errorf("the anchor read %q, want row 34", read.Range)
	}
	if !strings.Contains(read.Grid, "Umberly") {
		t.Errorf("the anchor read the wrong row:\n%s", read.Grid)
	}
}

func TestAnchorDiesWithItsRow(t *testing.T) {
	_, svc := destructive(t)
	ctx := context.Background()
	anchorOn(t, svc, "doomed", "24:24")

	// Nothing in the API's reply mentions the anchor, which is why the
	// guard on delete_dimensions has to name it beforehand.
	if _, err := svc.EditDimensions(ctx, service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "24:24", Confirm: true,
	}); err != nil {
		t.Fatalf("deleting the row: %v", err)
	}
	_, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "doomed",
	})
	if err == nil {
		t.Fatal("the anchor outlived the row it was on")
	}
	if !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("err = %v, want [not_found]", err)
	}
}

func TestAnchorNameIsUniqueHereEvenThoughGoogleAllowsTwo(t *testing.T) {
	_, svc := standard(t)
	anchorOn(t, svc, "totals", "24:24")
	_, err := svc.Anchors(context.Background(), service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "totals", Sheet: sheetstest.FirstSheet, Range: "26:26",
	})
	if err == nil {
		t.Fatal("a second anchor took a name already in use; anchor:totals would then have two answers")
	}
	if !strings.Contains(err.Error(), "move") {
		t.Errorf("err = %v, want it to point at action=move", err)
	}
}

func TestAnchorRefusesARectangle(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Anchors(context.Background(), service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "block", Sheet: sheetstest.FirstSheet, Range: "A1:D4",
	})
	if err == nil {
		t.Fatal("a rectangle was anchored; the API refuses one with a message naming a type nobody sent")
	}
	// The refusal has to carry both spellings, or the caller has to
	// guess which one this server wanted.
	for _, want := range []string{"1:1", "A:A"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to show %q", err, want)
		}
	}
}

func TestAnchorTakesASingleCellAsItsRowAndSaysSo(t *testing.T) {
	_, svc := standard(t)
	res := anchorOn(t, svc, "one cell", "B24")
	if len(res.Anchors) != 1 || res.Anchors[0].Scope != "row" {
		t.Fatalf("anchors = %+v, want one row anchor", res.Anchors)
	}
	// Reported, not silent: a caller who meant the column can see it in
	// the answer rather than a week later.
	if !strings.Contains(res.Summary, "24") {
		t.Errorf("the summary does not name the row it took:\n%s", res.Summary)
	}
}

func TestAnchorMoveKeepsTheLabelAndTheNote(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	if _, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "totals", Note: "the quarterly figure", Sheet: sheetstest.FirstSheet, Range: "24:24",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorMove,
		Name: "totals", Sheet: sheetstest.FirstSheet, Range: "26:26",
	}); err != nil {
		t.Fatalf("moving: %v", err)
	}
	res, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorList,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Anchors) != 1 {
		t.Fatalf("anchors = %+v, want one", res.Anchors)
	}
	if !strings.Contains(res.Anchors[0].Range, "26") {
		t.Errorf("the anchor is at %q, want row 26", res.Anchors[0].Range)
	}
	// The update sends a named field mask rather than "*". A star would
	// blank the key and the note from a body that does not carry them.
	if res.Anchors[0].Name != "totals" || res.Anchors[0].Note != "the quarterly figure" {
		t.Errorf("the move rewrote the label: %+v", res.Anchors[0])
	}
}

func TestAnchorRemoveLeavesTheData(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	anchorOn(t, svc, "temporary", "24:24")
	res, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorRemove, Name: "temporary",
	})
	if err != nil {
		t.Fatalf("removing: %v", err)
	}
	if !strings.Contains(res.Summary, "untouched") {
		t.Errorf("the summary does not say the data survives:\n%s", res.Summary)
	}
	read, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A24:B24",
	})
	if err != nil {
		t.Fatalf("reading the row: %v", err)
	}
	if !strings.Contains(read.Grid, "Umberly") {
		t.Errorf("removing the anchor took the row with it:\n%s", read.Grid)
	}
}

func TestAnchorListIsOneRequest(t *testing.T) {
	srv, svc := standard(t)
	ctx := context.Background()
	anchorOn(t, svc, "first", "24:24")
	anchorOn(t, svc, "second", "26:26")
	if _, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "the sheet itself", Sheet: sheetstest.FirstSheet,
	}); err != nil {
		t.Fatal(err)
	}

	srv.Reset()
	res, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorList,
	})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(res.Anchors) != 3 {
		t.Fatalf("listed %d anchors, want 3: %+v", len(res.Anchors), res.Anchors)
	}
	// One search, whatever the anchors are attached to. Listing per
	// sheet would cost a request per sheet and break §4.5 on any
	// spreadsheet with more than one.
	searches := 0
	for _, c := range srv.Calls() {
		if c.Op == "developerMetadata.search" {
			searches++
		}
	}
	if searches != 1 {
		t.Errorf("listing sent %d searches, want 1", searches)
	}
}

func TestAnchorRangeRefusesAnUnknownName(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "nothing here",
	})
	if err == nil {
		t.Fatal("an anchor that does not exist resolved")
	}
	if !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("err = %v, want [not_found]", err)
	}
	// A1 has to be offered, because an anchor is never the only way in.
	if !strings.Contains(err.Error(), "A1") {
		t.Errorf("err = %v, want it to say an A1 range works here", err)
	}
}

func TestAnchorRangeWritesThroughTheGuard(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	anchorOn(t, svc, "totals", "24:24")
	// Row 24 holds values, so the guard refuses without overwrite —
	// resolving through an anchor must not walk past it.
	_, err := svc.Write(ctx, service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "totals",
		Values: [][]any{{"Threnody"}},
	})
	if err == nil {
		t.Fatal("a write through an anchor overwrote a non-empty row unguarded")
	}
	if !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Errorf("err = %v, want [blocked]", err)
	}
}

// The guard's second finding from spike K: a row delete takes the
// anchors on it and the reply says nothing, so the refusal has to.
func TestDeletingRowsNamesTheAnchorsItWouldTake(t *testing.T) {
	_, svc := destructive(t)
	ctx := context.Background()
	if _, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "invoice totals", Sheet: sheetstest.FirstSheet, Range: "24:24",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.EditDimensions(ctx, service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "20:26",
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went through")
	}
	if !strings.Contains(err.Error(), "invoice totals") {
		t.Errorf("err = %v, want it to name the anchor that would go", err)
	}

	res, err := svc.EditDimensions(ctx, service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "20:26", Confirm: true,
	})
	if err != nil {
		t.Fatalf("the confirmed delete: %v", err)
	}
	if len(res.Anchors) != 1 || res.Anchors[0] != "invoice totals" {
		t.Errorf("anchors_removed = %v, want the anchor named in the result too", res.Anchors)
	}
}

// A column delete must not report row anchors, and the other way round:
// the two axes are independent and a guard that confuses them cries wolf
// on every delete.
func TestDeletingColumnsIgnoresRowAnchors(t *testing.T) {
	_, svc := destructive(t)
	ctx := context.Background()
	if _, err := svc.Anchors(ctx, service.AnchorRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
		Name: "a row", Sheet: sheetstest.FirstSheet, Range: "24:24",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.EditDimensions(ctx, service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "columns", Band: "H:J",
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went through")
	}
	if strings.Contains(err.Error(), "a row") {
		t.Errorf("err = %v; a column delete named a row anchor", err)
	}
}

// The other two ways a row moves, both recorded from spike K. The fake
// models them because a fake that left anchors on their old row numbers
// would agree with any code treating an anchor as a row number, which is
// the one thing this feature must not be.
func TestAnchorFollowsAMoveAndASort(t *testing.T) {
	ctx := context.Background()

	t.Run("moveDimension", func(t *testing.T) {
		_, svc := standard(t)
		anchorOn(t, svc, "moved", "24:24")
		if _, err := svc.EditDimensions(ctx, service.DimensionRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimMove, Dimension: "rows", Band: "24:24", To: 2,
		}); err != nil {
			t.Fatalf("moving the row: %v", err)
		}
		read, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "moved",
		})
		if err != nil {
			t.Fatalf("reading through the anchor: %v", err)
		}
		// The values are what settles it. A location that still reads
		// row 24 would be an anchor that did not move; one that reads
		// row 2 and holds the wrong data would be worse.
		if !strings.Contains(read.Grid, "Umberly") {
			t.Errorf("the anchor lost its row across a move:\n%s", read.Grid)
		}
	})

	t.Run("sortRange", func(t *testing.T) {
		_, svc := standard(t)
		anchorOn(t, svc, "sorted", "24:24")
		if _, err := svc.Transform(ctx, service.TransformRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Range: "A2:D26", Action: "sort", SortBy: "A asc", Overwrite: true,
		}); err != nil {
			t.Fatalf("sorting: %v", err)
		}
		read, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: sheetstest.FixtureID, Range: service.AnchorPrefix + "sorted",
		})
		if err != nil {
			t.Fatalf("reading through the anchor: %v", err)
		}
		if !strings.Contains(read.Grid, "Umberly") {
			t.Errorf("after a sort the anchor points at another row's values:\n%s", read.Grid)
		}
	})
}

// The other half of the promise the tool description makes. Ranges go
// through ResolveRange; a band is a different argument on a different
// path, and without its own hook `anchor:` failed with an A1 syntax
// error on the two tools that destroy anchors.
func TestAnchorWorksWhereABandIsTaken(t *testing.T) {
	ctx := context.Background()

	t.Run("delete the row an anchor names", func(t *testing.T) {
		_, svc := destructive(t)
		anchorOn(t, svc, "the doomed row", "24:24")
		res, err := svc.EditDimensions(ctx, service.DimensionRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimDelete, Dimension: "rows",
			Band: service.AnchorPrefix + "the doomed row", Confirm: true,
		})
		if err != nil {
			t.Fatalf("deleting through an anchor: %v", err)
		}
		if !strings.Contains(res.Band, "24") {
			t.Errorf("the delete acted on %q, want row 24", res.Band)
		}
		if len(res.Anchors) != 1 {
			t.Errorf("anchors_removed = %v, want the anchor it was named by", res.Anchors)
		}
	})

	t.Run("the dimension has to agree with the anchor", func(t *testing.T) {
		_, svc := standard(t)
		anchorOn(t, svc, "a row", "24:24")
		// The same mistake ParseBand refuses for "rows" with "B:D": a
		// caller who meant one axis and typed the other.
		_, err := svc.EditDimensions(ctx, service.DimensionRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimResize, Dimension: "columns",
			Band: service.AnchorPrefix + "a row", Pixels: 100,
		})
		if err == nil {
			t.Fatal("a row anchor was accepted as a column band")
		}
		if !strings.HasPrefix(err.Error(), "[invalid]") {
			t.Errorf("err = %v, want [invalid]", err)
		}
	})

	t.Run("a sheet anchor is not a band", func(t *testing.T) {
		_, svc := standard(t)
		if _, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
			Name: "the whole sheet", Sheet: sheetstest.FirstSheet,
		}); err != nil {
			t.Fatal(err)
		}
		_, err := svc.EditDimensions(ctx, service.DimensionRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimGroup, Dimension: "rows",
			Band: service.AnchorPrefix + "the whole sheet",
		})
		if err == nil {
			t.Fatal("a sheet anchor was accepted as a band of rows")
		}
	})

	t.Run("a name nobody set is refused", func(t *testing.T) {
		_, svc := standard(t)
		_, err := svc.EditDimensions(ctx, service.DimensionRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimGroup, Dimension: "rows", Band: service.AnchorPrefix + "nothing here",
		})
		if err == nil {
			t.Fatal("an anchor that does not exist resolved to a band")
		}
		if !strings.HasPrefix(err.Error(), "[not_found]") {
			t.Errorf("err = %v, want [not_found]", err)
		}
	})
}

// A URL carrying a gid and an anchor on another sheet is the conflict
// the A1 path refuses; the anchor path used to accept it and quietly
// read the anchor's sheet, because it compared the sheet argument as a
// string and never consulted the gid.
func TestAnchorRefusesAGidThatDisagreesWithIt(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	anchorOn(t, svc, "on the first sheet", "24:24")

	url := "https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit#gid=1837"
	_, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: url, Range: service.AnchorPrefix + "on the first sheet",
	})
	if err == nil {
		t.Fatal("the URL points at one sheet and the anchor at another, and the read went ahead")
	}
	if !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("err = %v, want [invalid] naming both sheets", err)
	}
}

// The result has to say what Google stored, not what the request asked
// for (hard rule 7). A move used to report the note it was handed while
// its field mask never wrote one, so the answer and the spreadsheet
// disagreed and only a later list would have shown it.
func TestAnchorMoveReportsTheNoteItActuallyStored(t *testing.T) {
	ctx := context.Background()

	stored := func(t *testing.T, svc *service.Service) string {
		t.Helper()
		res, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorList,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Anchors) != 1 {
			t.Fatalf("anchors = %+v, want one", res.Anchors)
		}
		return res.Anchors[0].Note
	}

	t.Run("a new note is written and reported", func(t *testing.T) {
		_, svc := standard(t)
		if _, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
			Name: "totals", Note: "the old note", Sheet: sheetstest.FirstSheet, Range: "24:24",
		}); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorMove,
			Name: "totals", Note: "the new note", Sheet: sheetstest.FirstSheet, Range: "26:26",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Anchors[0].Note; got != "the new note" {
			t.Errorf("the move reported %q", got)
		}
		if got := stored(t, svc); got != "the new note" {
			t.Errorf("the spreadsheet holds %q; the result said otherwise", got)
		}
	})

	t.Run("no note leaves the stored one alone and reports it", func(t *testing.T) {
		_, svc := standard(t)
		if _, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorAdd,
			Name: "totals", Note: "the old note", Sheet: sheetstest.FirstSheet, Range: "24:24",
		}); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Anchors(ctx, service.AnchorRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.AnchorMove,
			Name: "totals", Sheet: sheetstest.FirstSheet, Range: "26:26",
		})
		if err != nil {
			t.Fatal(err)
		}
		// Reporting "" here would say the note had gone when it had not.
		if got := res.Anchors[0].Note; got != "the old note" {
			t.Errorf("the move reported %q, want the note that is still there", got)
		}
		if got := stored(t, svc); got != "the old note" {
			t.Errorf("the move erased the note: %q", got)
		}
	})
}
