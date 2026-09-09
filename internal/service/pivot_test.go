package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// addPivot is the pivot every test here starts from: the first sheet's
// block summed by its first column, anchored clear of the data.
func addPivot(t *testing.T, svc *service.Service, anchor string) *service.PivotResult {
	t.Helper()
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: anchor,
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return res
}

func TestPivotAdd(t *testing.T) {
	_, svc := standard(t)
	res := addPivot(t, svc, "F1")
	text := res.Render()
	for _, want := range []string{"F1", "A1:C6", "grouped by", "covers"} {
		if !strings.Contains(text, want) {
			t.Errorf("the result does not mention %q:\n%s", want, text)
		}
	}
	// The rectangle is read back rather than derived: it is in no
	// request and no reply, so a result that stated it from the
	// arguments would be describing something nobody measured.
	if !strings.Contains(text, "computed from the data") {
		t.Errorf("the result does not say the rectangle is computed:\n%s", text)
	}
}

func TestPivotAddByHeading(t *testing.T) {
	_, svc := standard(t)
	// The source's first row holds the headings, so a caller may name a
	// column by what it says rather than by its letter.
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1", Source: "A1:C6",
		Rows: []string{headingOf(t, svc, 1)}, Values: []string{headingOf(t, svc, 2) + " sum"},
	})
	if err != nil {
		t.Fatalf("add by heading: %v", err)
	}
	if res.Anchor != "F1" {
		t.Errorf("anchor = %q", res.Anchor)
	}
}

// headingOf reads one heading out of the fixture, so this test names
// what the fixture holds rather than repeating it.
func headingOf(t *testing.T, svc *service.Service, col int) string {
	t.Helper()
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:C1",
		Format: "json",
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) < col {
		t.Fatal("the fixture has no heading row to name")
	}
	return res.Rows[0][col-1]
}

// TestPivotRefusesAnAnchorInsideItsSource is a refusal Google does not
// make: it takes the request with a 200 and evaluates the pivot to
// "Circular dependency detected", which is a broken spreadsheet reported
// as a success.
func TestPivotRefusesAnAnchorInsideItsSource(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "B2",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"},
	})
	if err == nil {
		t.Fatal("an anchor inside the source was accepted")
	}
	if !strings.Contains(err.Error(), "inside the source") {
		t.Errorf("error = %v", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("the request was sent")
		}
	}
}

// TestPivotRefusesAColumnOutsideTheSource is the other refusal this
// server makes alone: an offset past the source's width is accepted with
// a 200 and produces a pivot that reads nothing.
func TestPivotRefusesAColumnOutsideTheSource(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"Z sum"},
	})
	if err == nil {
		t.Fatal("a column outside the source was accepted")
	}
	if !strings.Contains(err.Error(), "outside the source") {
		t.Errorf("error = %v", err)
	}
}

func TestPivotAddRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.PivotRequest
		want string
	}{
		{"no anchor", service.PivotRequest{Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"}}, "anchor"},
		{"an anchor that is a range", service.PivotRequest{Anchor: "F1:G2", Source: "A1:C6",
			Rows: []string{"A"}, Values: []string{"B sum"}}, "one cell"},
		{"no source", service.PivotRequest{Anchor: "F1", Rows: []string{"A"}, Values: []string{"B sum"}}, "source"},
		{"an unbounded source", service.PivotRequest{Anchor: "F1", Source: "A:C",
			Rows: []string{"A"}, Values: []string{"B sum"}}, "no end"},
		{"no values", service.PivotRequest{Anchor: "F1", Source: "A1:C6", Rows: []string{"A"}}, "values"},
		{"no groups", service.PivotRequest{Anchor: "F1", Source: "A1:C6", Values: []string{"B sum"}}, "group_rows"},
		{"a value with no function", service.PivotRequest{Anchor: "F1", Source: "A1:C6",
			Rows: []string{"A"}, Values: []string{"B"}}, "how to summarise"},
		{"a function nobody offers", service.PivotRequest{Anchor: "F1", Source: "A1:C6",
			Rows: []string{"A"}, Values: []string{"B mode"}}, "mode"},
		{"a layout nobody offers", service.PivotRequest{Anchor: "F1", Source: "A1:C6",
			Rows: []string{"A"}, Values: []string{"B sum"}, Layout: "diagonal"}, "horizontal or vertical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.Action = service.PivotAdd
			_, err := svc.ManagePivotTable(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPivotUpdate(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotUpdate, Anchor: "F1", Values: []string{"C sum as Returns"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(res.Render(), "values") {
		t.Errorf("the result does not name what changed:\n%s", res.Render())
	}
}

func TestPivotUpdateRefusals(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	for _, tc := range []struct {
		name string
		req  service.PivotRequest
		want string
	}{
		{"nothing to change", service.PivotRequest{Action: service.PivotUpdate, Anchor: "F1"}, "nothing to change"},
		{"no pivot there", service.PivotRequest{Action: service.PivotUpdate, Anchor: "H20",
			Values: []string{"B sum"}}, "no pivot table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			_, err := svc.ManagePivotTable(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestPivotDeleteTakesTheWholeOutput is the shape of the delete: there
// is no deletePivotTable, and naming the field with an empty cell takes
// every computed cell with it.
func TestPivotDeleteTakesTheWholeOutput(t *testing.T) {
	_, svc := standard(t)
	added := addPivot(t, svc, "F1")
	if added.Render() == "" {
		t.Fatal("no result")
	}
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotDelete, Anchor: "F1",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(res.Render(), "cleared") {
		t.Errorf("the result does not say what went:\n%s", res.Render())
	}
	after, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after.Pivots) != 0 {
		t.Errorf("pivots after the delete = %+v", after.Pivots)
	}
	// And nothing it drew is left behind.
	read, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1:H10",
		Format: "json",
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, row := range read.Rows {
		for _, cell := range row {
			if cell != "" {
				t.Errorf("a cell of the output survived the delete: %q", cell)
			}
		}
	}
}

func TestPivotDeleteWhereThereIsNone(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotDelete, Anchor: "H20",
	})
	if err == nil || !strings.Contains(err.Error(), "[not_found]") {
		t.Fatalf("error = %v, want not_found", err)
	}
}

func TestPivotList(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Pivots) != 1 {
		t.Fatalf("pivots = %+v", res.Pivots)
	}
	if res.Pivots[0].Anchor != "F1" {
		t.Errorf("anchor = %q, want F1", res.Pivots[0].Anchor)
	}
	if res.Pivots[0].Output == "" {
		t.Error("the listing does not say how far the pivot reaches, which is what a caller has to avoid writing over")
	}
	if !strings.Contains(res.Render(), "F1") {
		t.Errorf("listing:\n%s", res.Render())
	}
}

func TestPivotListEmpty(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Render(), "No pivot tables") {
		t.Errorf("empty listing reads:\n%s", res.Render())
	}
}

// TestPivotOutputIsSeenByTheWriteGuard is the half of spike M that
// matters most: a pivot's output carries an effectiveValue and no
// userEnteredValue, so the guard has to count those cells as occupied.
// If it does not, a write lands on them and the pivot stops drawing.
func TestPivotOutputIsSeenByTheWriteGuard(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F2",
		Values: [][]any{{"over the pivot"}},
	})
	if err == nil {
		t.Fatal("a write over a pivot's output was allowed without overwrite")
	}
	if !strings.Contains(err.Error(), "[blocked]") {
		t.Errorf("error = %v, want blocked", err)
	}
}

// TestAWriteOverAPivotAnchorSaysWhatItWouldDo is the half the first live
// run showed missing. The guard already refused a write into a pivot's
// output — it counts as non-empty — but the refusal said only "I3 is not
// empty", which tells a caller to pass overwrite and says nothing about
// what overwrite would cost.
func TestAWriteOverAPivotAnchorSaysWhatItWouldDo(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1",
		Values: [][]any{{"over the anchor"}},
	})
	if err == nil {
		t.Fatal("a write over a pivot's anchor was allowed without overwrite")
	}
	for _, want := range []string{"[blocked]", "pivot table", "everything it draws"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%v", want, err)
		}
	}
}

func TestPivotDryRun(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1", Source: "A1:C6",
		Rows: []string{"A"}, Values: []string{"B sum"}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
		t.Errorf("dry run result:\n%s", res.Render())
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Error("a dry run sent a write")
		}
	}
}

func TestPivotUnknownAction(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Action: "summarise",
	})
	if err == nil || !strings.Contains(err.Error(), "summarise") {
		t.Fatalf("error = %v, want the action named", err)
	}
}

// TestPivotAddRefusesAnOccupiedAnchor is finding 7 of the review pass.
// An add onto an anchor that already holds a pivot is an updateCells
// like any other: it discards the definition and the whole output, and
// the reply says nothing.
func TestPivotAddRefusesAnOccupiedAnchor(t *testing.T) {
	srv, svc := standard(t)
	addPivot(t, svc, "F1")
	srv.Reset()
	_, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"C sum"},
	})
	if err == nil {
		t.Fatal("an add replaced a pivot table that was already there")
	}
	if !strings.Contains(err.Error(), "[blocked]") || !strings.Contains(err.Error(), "action=update") {
		t.Errorf("the refusal does not point at the tool that changes one:\n%v", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("the request was sent")
		}
	}
}

// TestPivotOutputStopsAtTheFirstEmptyRow is finding 3. The rectangle was
// "the furthest computed cell anywhere below and right of the anchor",
// so a second pivot table on the same sheet joined the first one's
// reported footprint — and the result hands that footprint to the caller
// as cells a write would break.
func TestPivotOutputStopsAtTheFirstEmptyRow(t *testing.T) {
	_, svc := standard(t)
	first := addPivot(t, svc, "F1")
	// A second pivot well below and to the right of the first.
	second, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "H20",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"C sum"},
	})
	if err != nil {
		t.Fatalf("the second pivot: %v", err)
	}
	if second.Anchor != "H20" {
		t.Fatalf("anchor = %q", second.Anchor)
	}
	listed, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, p := range listed.Pivots {
		if p.Anchor != "F1" {
			continue
		}
		// It must not reach row 20 or column H: those belong to the
		// other pivot, and a caller told to keep clear of them is being
		// steered away from cells that are free.
		if strings.Contains(p.Output, "20") || strings.Contains(p.Output, "H") {
			t.Errorf("the F1 pivot reports covering %q, which swallows the one at H20", p.Output)
		}
	}
	_ = first
}

// TestPivotOffsetAgainstAnUnboundedSource is finding 9. A pivot built in
// the Sheets interface over a whole-sheet range comes back with no
// startColumnIndex, which reads as column 0 — so the offset came out one
// too high and the bounds check could not fire either, which is the
// exact "accepted with a 200 and reads nothing" outcome the check exists
// to prevent.
//
// The pivot sits on the second sheet, because a whole-sheet source and
// an anchor on that same sheet is genuinely circular and this server
// refuses it first.
func TestPivotOffsetAgainstAnUnboundedSource(t *testing.T) {
	srv, svc := standard(t)
	doc := srv.Doc(sheetstest.FixtureID)
	if doc == nil {
		t.Fatal("no fixture")
	}
	source := doc.Find(sheetstest.FirstSheet)
	target := doc.Find(sheetstest.SecondSheet)
	if source == nil || target == nil {
		t.Fatal("no sheets")
	}
	// A source naming no column bounds at all: the whole sheet, which is
	// what the interface writes.
	target.Set(1, 6, &gsheets.CellData{PivotTable: json.RawMessage(
		`{"source":{"sheetId":` + strconv.Itoa(source.Props.SheetID) + `},` +
			`"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING","showTotals":true}],` +
			`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`)})

	// Column B is the second column, so its offset is 1. Before the fix
	// it came out 2, and nothing refused it.
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
		Action: service.PivotUpdate, Anchor: "F1", Values: []string{"B sum"},
	}); err != nil {
		t.Fatalf("update over an unbounded source: %v", err)
	}
	found, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(found.Pivots) == 0 {
		t.Fatal("the pivot is gone")
	}
}

// TestPivotUpdateCostsFewRequests holds the request count, because the
// API allows sixty reads a minute and one round trip is about a second.
func TestPivotUpdateCostsFewRequests(t *testing.T) {
	srv, svc := standard(t)
	addPivot(t, svc, "F1")
	srv.Reset()
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotUpdate, Anchor: "F1", Values: []string{"C sum"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	reads, writes := 0, 0
	for _, c := range srv.Calls() {
		switch c.Op {
		case "spreadsheets.get":
			reads++
		case "spreadsheets.batchUpdate":
			writes++
		}
	}
	t.Logf("an update costs %d read(s) and %d write(s)", reads, writes)
	if reads > 3 {
		t.Errorf("an update costs %d reads; it used to cost four and the point of the change was fewer", reads)
	}
}

// TestWriteIntoPivotOutputNamesThePivot is §17a.27. A pivot's output
// cells are ordinary computed values on the wire, so the guard sees them
// as "not empty" and nothing else; naming the table means looking up and
// left of the write, after the refusal.
func TestWriteIntoPivotOutputNamesThePivot(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3",
		Values: [][]any{{"1"}},
	})
	if err == nil {
		t.Fatal("a write into a pivot's output was allowed")
	}
	for _, want := range []string{"[blocked]", "pivot table anchored at F1", "G3", "covers F1:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
}

// TestWriteIntoPivotOutputPreviewSaysTheSame keeps the preview and the
// refusal from disagreeing about what is in the way.
func TestWriteIntoPivotOutputPreviewSaysTheSame(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3",
		Values: [][]any{{"1"}}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(res.Render(), "pivot table anchored at F1") {
		t.Errorf("the preview does not name the pivot:\n%s", res.Render())
	}
}

// TestWriteOverPivotAnchorNamesItOnce. The anchor is already named by
// the finding that a write over it takes the whole table, so the second
// look must not say it again.
func TestWriteOverPivotAnchorNamesItOnce(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1",
		Values: [][]any{{"1"}},
	})
	if err == nil {
		t.Fatal("a write over a pivot's anchor was allowed")
	}
	if n := strings.Count(err.Error(), "pivot table"); n != 1 {
		t.Errorf("the refusal mentions a pivot table %d times:\n%s", n, err)
	}
	if !strings.Contains(err.Error(), "anchors a pivot table") {
		t.Errorf("the refusal does not say the write takes the whole table:\n%s", err)
	}
}

// TestWriteBesidePivotOutputSaysNothing. An anchor up and to the left is
// not yet a pivot that reaches the write: measuring the extent is what
// keeps the refusal from steering a caller away from cells that were
// never the pivot's.
func TestWriteBesidePivotOutputSaysNothing(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	// The pivot covers F1:G7. J3 holds a value nobody typed, so the
	// cheap test passes and the extent is what refuses the claim.
	if _, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "J3",
		Values: [][]any{{"=1+1"}}, Overwrite: true,
	}); err != nil {
		t.Fatalf("seeding J3: %v", err)
	}
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "J3",
		Values: [][]any{{"1"}},
	})
	if err == nil {
		t.Fatal("a write over a formula was allowed")
	}
	if strings.Contains(err.Error(), "pivot table") {
		t.Errorf("a write outside every pivot's output was blamed on one:\n%s", err)
	}
}

// TestPivotLookupIsPaidOnlyOnARefusal is the cost §17a.27 turned on:
// once per refusal rather than once per write.
func TestPivotLookupIsPaidOnlyOnARefusal(t *testing.T) {
	srv, svc := standard(t)
	addPivot(t, svc, "F1")
	srv.Reset()
	if _, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3",
		Values: [][]any{{"1"}}, Overwrite: true,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The lookup's own mask, not any mask naming a pivot: a guarded
	// write's own read asks for the anchor field too.
	for _, c := range srv.Calls() {
		if c.Query.Get("fields") == gapi.PivotFields {
			t.Errorf("an allowed write paid for the pivot lookup: %s %s", c.Op, c.Query.Get("ranges"))
		}
	}
}

// TestPivotLookbackIsBoundedByTheReadBudget is the cost of the shape
// §17a.27 chose. The window that could hold the anchor is bounded from
// the write's end, so a pivot anchored further above than the budget
// reaches is not named and the refusal says only what the guard saw.
func TestPivotLookbackIsBoundedByTheReadBudget(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newServiceBudget(t, srv, 2)
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.PivotAdd, Anchor: "F1",
		Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3",
		Values: [][]any{{"1"}},
	})
	if err == nil {
		t.Fatal("a write into a pivot's output was allowed")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("the refusal lost the finding it always had:\n%s", err)
	}
	if strings.Contains(err.Error(), "pivot table") {
		t.Errorf("the lookup reached past its budget:\n%s", err)
	}
}

// TestPivotOutputNamesOnlyTheCellsItDraws. A write can straddle a
// pivot's edge, and the refusal has to say which part of it is the
// pivot's: a rectangle that took in the whole write would send a caller
// looking for a pivot in cells no pivot draws.
func TestPivotOutputNamesOnlyTheCellsItDraws(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	// F1:G7 is the pivot. This write runs from inside it out to J3.
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3:J3",
		Values: [][]any{{"1", "2", "3", "4"}},
	})
	if err == nil {
		t.Fatal("a write into a pivot's output was allowed")
	}
	if !strings.Contains(err.Error(), "G3 is inside the output") {
		t.Errorf("the refusal does not name the part of the write the pivot draws:\n%s", err)
	}
	if strings.Contains(err.Error(), "G3:J3 is inside") {
		t.Errorf("the refusal claims the whole write is the pivot's:\n%s", err)
	}
}

// TestTwoPivotsAreBothNamed. Nothing says a write lands in one.
func TestTwoPivotsAreBothNamed(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	addPivot(t, svc, "J1")
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3:K3",
		Values: [][]any{{"1", "2", "3", "4", "5"}},
	})
	if err == nil {
		t.Fatal("a write across two pivots' output was allowed")
	}
	for _, want := range []string{"anchored at F1", "anchored at J1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the pivot %q:\n%s", want, err)
		}
	}
}

// TestAdjacentPivotsAreMeasuredApart. Two pivot tables side by side have
// no empty column between them, so the walk that stops at empty grew the
// left one over the right one — which made a listing wrong and, once a
// refusal quoted it, made the refusal name a table the write would not
// have touched.
func TestAdjacentPivotsAreMeasuredApart(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	addPivot(t, svc, "H1")
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[string]string{"F1": "F1:G7", "H1": "H1:I7"}
	for _, p := range res.Pivots {
		if p.Output != want[p.Anchor] {
			t.Errorf("the pivot at %s covers %q, want %q", p.Anchor, p.Output, want[p.Anchor])
		}
	}
	// And the refusal blames one table rather than both.
	_, err = svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "H3",
		Values: [][]any{{"1"}},
	})
	if err == nil {
		t.Fatal("a write into a pivot's output was allowed")
	}
	if !strings.Contains(err.Error(), "anchored at H1") {
		t.Errorf("the refusal does not name the pivot the cell belongs to:\n%s", err)
	}
	if strings.Contains(err.Error(), "anchored at F1") {
		t.Errorf("the refusal blames the neighbour it would not have touched:\n%s", err)
	}
}

// TestPivotIsNotNamedOnceOverwriteIsPassed. Every finding beside this one
// is withheld when the flag that clears it has been passed, and a caller
// told to pass overwrite twice has been told nothing the second time.
func TestPivotIsNotNamedOnceOverwriteIsPassed(t *testing.T) {
	srv, svc := standard(t)
	addPivot(t, svc, "F1")
	// A formula outside the pivot, so the write is still refused — for
	// something overwrite alone does not allow.
	if _, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "H3",
		Values: [][]any{{"=1+1"}}, Overwrite: true,
	}); err != nil {
		t.Fatalf("seeding H3: %v", err)
	}
	srv.Reset()
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3:H3",
		Values: [][]any{{"1", "2"}}, Overwrite: true,
	})
	if err == nil {
		t.Fatal("a write over a formula was allowed")
	}
	if strings.Contains(err.Error(), "pivot table") {
		t.Errorf("the refusal offers overwrite to a caller who passed it:\n%s", err)
	}
	// And the reads behind that sentence are not paid either.
	for _, c := range srv.Calls() {
		if c.Query.Get("fields") == gapi.PivotFields {
			t.Errorf("the lookup ran for a finding that would have been withheld: %s", c.Query.Get("ranges"))
		}
	}
}

// TestMergeOverPivotOutputIsRefusedWithTheReason is §17a.31. Sheets
// refuses a merge over any cell of a pivot table itself, so nothing is
// being prevented here — what is fixed is a refusal that described a
// loss which cannot happen and offered a flag that could not help.
func TestMergeOverPivotOutputIsRefusedWithTheReason(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F1")
	_, err := svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F2:G3",
		Merge: "all",
	})
	if err == nil {
		t.Fatal("a merge over a pivot's output was allowed")
	}
	for _, want := range []string{"[blocked]", "pivot table anchored at F1", "refuses a merge"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
	// And it offers nothing to get past it, because nothing does.
	if strings.Contains(err.Error(), "overwrite") {
		t.Errorf("the refusal offers a flag the API will not honour:\n%s", err)
	}
}

// TestMergeOverPivotAnchorIsRefused. The anchor is in the rectangle the
// guard already read, so this one costs no extra call.
func TestMergeOverPivotAnchorIsRefused(t *testing.T) {
	srv, svc := standard(t)
	// Below the fixture's frozen row, so this reaches the pivot check
	// rather than the frozen-boundary one that runs before it.
	addPivot(t, svc, "F2")
	srv.Reset()
	_, err := svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F2:G3",
		Merge: "all",
	})
	if err == nil {
		t.Fatal("a merge over a pivot's anchor was allowed")
	}
	if !strings.Contains(err.Error(), "F2 carries a pivot table") {
		t.Errorf("the refusal does not name the anchor:\n%s", err)
	}
	for _, c := range srv.Calls() {
		if c.Query.Get("fields") == gapi.PivotFields {
			t.Errorf("an anchor already in the rectangle cost a second read: %s", c.Query.Get("ranges"))
		}
	}
}

// TestClearOverPivotAnchorSaysWhatItTakes is the sixth silent destroy.
// values.clear over the anchor returns 200 naming one cell and takes the
// definition and every cell of the output with it, so the confirm gate
// is the only place a caller can find that out.
func TestClearOverPivotAnchorSaysWhatItTakes(t *testing.T) {
	_, svc := destructive(t)
	addPivot(t, svc, "F1")
	_, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1:F2",
	})
	if err == nil {
		t.Fatal("an unconfirmed clear was allowed")
	}
	for _, want := range []string{"F1 anchors a pivot table", "every cell it draws", "which the count does not include"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the confirm gate does not say %q:\n%s", want, err)
		}
	}

	// The gate sends the caller to dry_run, so the dry run has to say it
	// too — and so does the result, which used to announce that the
	// pivot's cells had survived a call that had just destroyed them.
	dry, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1:F2", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(dry.Render(), "F1 anchors a pivot table") {
		t.Errorf("the dry run does not name the pivot the gate warned about:\n%s", dry.Render())
	}
	done, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1:F2", Confirm: true,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !strings.Contains(done.Render(), "took the whole table") {
		t.Errorf("the result does not say the pivot went:\n%s", done.Render())
	}
	// And it really did go, in the fake as live.
	after, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after.Pivots) != 0 {
		t.Errorf("the pivot survived a clear of its anchor: %+v", after.Pivots)
	}
}

// TestClearCountsOnlyWhatItCanRemove. Clearing a cell a pivot computed
// returns 200, names the range and changes nothing, so counting it into
// the loss named damage that does not happen.
func TestClearCountsOnlyWhatItCanRemove(t *testing.T) {
	_, svc := destructive(t)
	addPivot(t, svc, "F1")
	// G2:G3 is output and nothing else: nobody typed either cell.
	_, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G2:G3",
	})
	if err == nil {
		t.Fatal("an unconfirmed clear was allowed")
	}
	if !strings.Contains(err.Error(), "removes 0 cell(s)") {
		t.Errorf("the gate counts cells a clear cannot remove:\n%s", err)
	}
	// And it says what that depends on rather than promising survival:
	// clearing an array formula takes its whole spill, and a spill looks
	// exactly like a pivot's output from here.
	for _, want := range []string{"nobody typed", "does not take those out itself", "was in the range too"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the gate does not say %q:\n%s", want, err)
		}
	}
}

// TestMergeOverBlanksInsideAPivotIsTranslated is the hole the guard
// cannot see. Google goes by the pivot's footprint, not its cells:
// verified live, a merge over two cells that are blank in the response
// and blank on the sheet is refused because the rectangle they sit in
// belongs to a pivot table. The guard has no way to know that without
// measuring every pivot before every merge, so what matters is that the
// caller never sees Google's untranslated wording.
func TestMergeOverBlanksInsideAPivotIsTranslated(t *testing.T) {
	srv, svc := standard(t)
	addPivot(t, svc, "F2")
	// The fake stands in for Google here: whatever reaches the wire
	// comes back with the API's own message.
	srv.Fail("spreadsheets.batchUpdate", sheetstest.Failure{
		Status: 400,
		Body: `{"error":{"code":400,"message":"Invalid requests[0].mergeCells: ` +
			`You can't merge cells that are part of a pivot table.","status":"INVALID_ARGUMENT"}}`,
	})
	_, err := svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "H20:I20",
		Merge: "all",
	})
	if err == nil {
		t.Fatal("the merge was reported as done")
	}
	if !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Errorf("the refusal is not classed as blocked:\n%s", err)
	}
	for _, want := range []string{"blank ones inside the rectangle", "manage_pivot_table list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "requests[0]") {
		t.Errorf("Google's own wording reached the caller:\n%s", err)
	}
}

// TestMergeAcrossTwoPivotsNamesBoth. "Merge cells outside it" pointing
// at cells inside a second pivot is advice that gets the caller refused
// again.
func TestMergeAcrossTwoPivotsNamesBoth(t *testing.T) {
	_, svc := standard(t)
	addPivot(t, svc, "F2")
	addPivot(t, svc, "H2")
	_, err := svc.FormatCells(context.Background(), service.FormatRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "G3:H3",
		Merge: "all",
	})
	if err == nil {
		t.Fatal("a merge across two pivots was allowed")
	}
	for _, want := range []string{"anchored at F2", "anchored at H2", "outside them"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
}

// TestClearOfAnArrayFormulaDoesNotPromiseSurvival. A spill looks exactly
// like a pivot's output from here, and clearing the formula takes it —
// so the sentence about cells nobody typed says what it depends on
// rather than promising they stay.
func TestClearOfAnArrayFormulaDoesNotPromiseSurvival(t *testing.T) {
	_, svc := destructive(t)
	_, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:C3",
	})
	if err == nil {
		t.Fatal("an unconfirmed clear was allowed")
	}
	if strings.Contains(err.Error(), "does not remove") {
		t.Errorf("the gate promises survival it cannot know about:\n%s", err)
	}
}
