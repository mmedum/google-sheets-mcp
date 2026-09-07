package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

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
