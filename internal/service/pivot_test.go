package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/service"
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
			Rows: []string{"A"}, Values: []string{"B"}}, "how to summarize"},
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

// seedPivot anchors a pivot table at F1 on the first sheet exactly as
// given, so a test can start from columns this server would not build.
func seedPivot(t *testing.T, srv *sheetstest.Server, pivot string) {
	t.Helper()
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet)
	if sh == nil {
		t.Fatal("no first sheet")
	}
	sh.Set(1, 6, &gsheets.CellData{PivotTable: json.RawMessage(pivot)})
}

// The old sources the tests below start from, on the first sheet.
const (
	sourceAC = `"source":{"sheetId":0,"startRowIndex":0,"endRowIndex":6,"startColumnIndex":0,"endColumnIndex":3}`
	sourceAD = `"source":{"sheetId":0,"startRowIndex":0,"endRowIndex":6,"startColumnIndex":0,"endColumnIndex":4}`
)

// TestPivotUpdateRefusesASourceTooNarrowForWhatItKeeps is the update
// that changes source. A group, value or filter it was not given keeps
// its offset, and Google accepts an offset past the new source's edge
// with a 200 and a pivot that reads nothing there (spike M).
func TestPivotUpdateRefusesASourceTooNarrowForWhatItKeeps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pivot string
		req   service.PivotRequest
		want  string
	}{
		{"a kept value",
			`{` + sourceAC + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":2,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "A1:B6"},
			`[invalid] the new source 'Vandel'!A1:B6 is 2 columns wide, and the pivot table would keep values on C ` +
				`from the old source 'Vandel'!A1:C6, past its right edge. Google accepts that and the pivot reads nothing ` +
				`there. Pass values again, named against the new source, or choose a source at least 3 columns wide.`},
		{"kept groups",
			`{` + sourceAD + `,"rows":[{"sourceColumnOffset":3,"sortOrder":"ASCENDING"}],` +
				`"columns":[{"sourceColumnOffset":2,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "A1:B6", Values: []string{"B sum"}},
			`[invalid] the new source 'Vandel'!A1:B6 is 2 columns wide, and the pivot table would keep group_rows on D ` +
				`and group_columns on C from the old source 'Vandel'!A1:D6, past its right edge. Google accepts that and ` +
				`the pivot reads nothing there. Pass group_rows and group_columns again, named against the new source, ` +
				`or choose a source at least 4 columns wide.`},
		{"a kept filter",
			`{` + sourceAD + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}],` +
				`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{"visibleValues":["Skerry"]}}]}`,
			service.PivotRequest{Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"}},
			`[invalid] the new source 'Vandel'!A1:C6 is 3 columns wide, and the pivot table would keep filters on D ` +
				`from the old source 'Vandel'!A1:D6, past its right edge. Google accepts that and the pivot reads nothing ` +
				`there. Pass filters again, named against the new source, or choose a source at least 4 columns wide.`},
		{"a kept filter in the older form",
			`{` + sourceAD + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}],` +
				`"criteria":{"3":{"visibleValues":["Skerry"]}}}`,
			service.PivotRequest{Source: "A1:C6", Rows: []string{"A"}, Values: []string{"B sum"}},
			`[invalid] the new source 'Vandel'!A1:C6 is 3 columns wide, and the pivot table would keep filters on D ` +
				`from the old source 'Vandel'!A1:D6, past its right edge. Google accepts that and the pivot reads nothing ` +
				`there. Pass filters again, named against the new source, or choose a source at least 4 columns wide.`},
		// A pivot built in the Sheets interface over a whole sheet has no
		// left edge, and its offsets count from column A.
		{"a kept value under a whole-sheet source",
			`{"source":{"sheetId":0},"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "A1:C6"},
			`[invalid] the new source 'Vandel'!A1:C6 is 3 columns wide, and the pivot table would keep values on D ` +
				`from the old source 'Vandel', past its right edge. Google accepts that and the pivot reads nothing ` +
				`there. Pass values again, named against the new source, or choose a source at least 4 columns wide.`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			seedPivot(t, srv, tc.pivot)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.Action = service.PivotUpdate
			tc.req.Anchor = "F1"
			_, err := svc.ManagePivotTable(context.Background(), tc.req)
			if err == nil {
				t.Fatal("a source too narrow for the kept columns was accepted")
			}
			if err.Error() != tc.want {
				t.Errorf("error =\n%v\nwant\n%s", err, tc.want)
			}
			for _, c := range srv.Calls() {
				if c.Op == "spreadsheets.batchUpdate" {
					t.Fatal("the request was sent")
				}
			}
		})
	}
}

// TestPivotUpdateRefusesASourceThatMovesWhatItKeeps is the update whose
// new source starts somewhere else. Every kept offset counts from the
// source's first column, so each one would read other data, and Google
// has no way to know that is not what was meant.
func TestPivotUpdateRefusesASourceThatMovesWhatItKeeps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pivot string
		req   service.PivotRequest
		want  string
	}{
		{"one column to the right",
			`{` + sourceAC + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":2,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "B1:D6"},
			`[invalid] the new source 'Vandel'!B1:D6 starts at column B, and the old source 'Vandel'!A1:C6 at ` +
				`column A. Each column the pivot table keeps counts from the source's first column, so group_rows ` +
				`on A would read B and values on C would read D. Pass group_rows and values again, named against ` +
				`the new source, or choose a source that starts at column A.`},
		// What the update names again is left out: only the filter is
		// kept, and the move pushes it past the new edge.
		{"a kept filter moved past the edge",
			`{` + sourceAD + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}],` +
				`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{"visibleValues":["Skerry"]}}]}`,
			service.PivotRequest{Source: "B1:D6", Rows: []string{"B"}, Values: []string{"C sum"}},
			`[invalid] the new source 'Vandel'!B1:D6 starts at column B, and the old source 'Vandel'!A1:D6 at ` +
				`column A. Each column the pivot table keeps counts from the source's first column, so filters ` +
				`on D would read nothing, past its right edge. Pass filters again, named against the new source, ` +
				`or choose a source that starts at column A.`},
		// A pivot made in the Sheets interface sits on a sheet of its own
		// and reads another. This tool reads a source on the anchor's
		// sheet, so it cannot offer the old one back.
		{"a source on another sheet",
			`{"source":{"sheetId":1837,"startRowIndex":0,"endRowIndex":6,"startColumnIndex":0,"endColumnIndex":3},` +
				`"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":2,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "A1:C6", Rows: []string{"A"}},
			`[invalid] the new source 'Vandel'!A1:C6 is on another sheet than the old source 'Ürväl'!A1:C6. ` +
				`Each column the pivot table keeps counts from the source's first column, so values on 'Ürväl'!C ` +
				`would read 'Vandel'!C. Pass values again, named against the new source.`},
		{"an old source that cannot be read",
			`{"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
				`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`,
			service.PivotRequest{Source: "A1:C6"},
			`[invalid] the pivot table's old source could not be read, so this server cannot tell what the ` +
				`group_rows and values it keeps would read in the new source 'Vandel'!A1:C6. Pass group_rows and ` +
				`values again, named against the new source.`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			seedPivot(t, srv, tc.pivot)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.Action = service.PivotUpdate
			tc.req.Anchor = "F1"
			_, err := svc.ManagePivotTable(context.Background(), tc.req)
			if err == nil {
				t.Fatal("a source that moves the kept columns was accepted")
			}
			if err.Error() != tc.want {
				t.Errorf("error =\n%v\nwant\n%s", err, tc.want)
			}
			for _, c := range srv.Calls() {
				if c.Op == "spreadsheets.batchUpdate" {
					t.Fatal("the request was sent")
				}
			}
		})
	}
}

// TestPivotUpdateTakesANewSourceThatFits is the other side of the same
// check: what the update names again, and what still fits, goes through.
func TestPivotUpdateTakesANewSourceThatFits(t *testing.T) {
	kept := `{` + sourceAC + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
		`"values":[{"sourceColumnOffset":2,"summarizeFunction":"SUM"}]}`
	for _, tc := range []struct {
		name string
		req  service.PivotRequest
		want string
	}{
		// The last kept offset is the new source's last column.
		{"the same width, more rows", service.PivotRequest{Source: "A1:C20"},
			`{"sheetId":0,"startRowIndex":0,"endRowIndex":20,"startColumnIndex":0,"endColumnIndex":3}`},
		{"the value given again", service.PivotRequest{Source: "A1:B6", Values: []string{"B sum"}},
			`{"sheetId":0,"startRowIndex":0,"endRowIndex":6,"startColumnIndex":0,"endColumnIndex":2}`},
		// A move keeps nothing it was not given, so naming both again is
		// the way to take one.
		{"a moved source with everything given again",
			service.PivotRequest{Source: "B1:D6", Rows: []string{"B"}, Values: []string{"D sum"}},
			`{"sheetId":0,"startRowIndex":0,"endRowIndex":6,"startColumnIndex":1,"endColumnIndex":4}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			seedPivot(t, srv, kept)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.Action = service.PivotUpdate
			tc.req.Anchor = "F1"
			if _, err := svc.ManagePivotTable(context.Background(), tc.req); err != nil {
				t.Fatalf("update: %v", err)
			}
			var stored struct {
				Source json.RawMessage `json:"source"`
			}
			cell := srv.Doc(sheetstest.FixtureID).Find(sheetstest.FirstSheet).At(1, 6)
			if cell == nil || json.Unmarshal(cell.PivotTable, &stored) != nil {
				t.Fatal("no pivot table at F1 after the update")
			}
			if string(stored.Source) != tc.want {
				t.Errorf("source = %s, want %s", stored.Source, tc.want)
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
		Spreadsheet: sheetstest.FixtureID, Action: "summarize",
	})
	if err == nil || !strings.Contains(err.Error(), "summarize") {
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
		t.Errorf("the refusal blames the neighbor it would not have touched:\n%s", err)
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
		t.Errorf("the refusal offers a flag the API will not honor:\n%s", err)
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
	_, err := svc.Clear(accepted(), service.ClearRequest{
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
	dry, err := svc.Clear(accepted(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "F1:F2", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(dry.Render(), "F1 anchors a pivot table") {
		t.Errorf("the dry run does not name the pivot the gate warned about:\n%s", dry.Render())
	}
	done, err := svc.Clear(accepted(), service.ClearRequest{
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
	_, err := svc.Clear(accepted(), service.ClearRequest{
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
	_, err := svc.Clear(accepted(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:C3",
	})
	if err == nil {
		t.Fatal("an unconfirmed clear was allowed")
	}
	if strings.Contains(err.Error(), "does not remove") {
		t.Errorf("the gate promises survival it cannot know about:\n%s", err)
	}
}

// salesService is a fake with the sales block at A10:E16 of the second
// sheet, widened for a pivot at H10. "Age" there is a heading that also
// reads as a column letter, far outside the block.
func salesService(t *testing.T) (*sheetstest.Server, *service.Service) {
	t.Helper()
	srv, svc := standard(t)
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	sh.Props.GridProperties.ColumnCount = 20
	sheetstest.SalesBlock(sh, 10)
	return srv, svc
}

// salesPivot is a request against the sales block, anchored at H10.
func salesPivot(action string) service.PivotRequest {
	req := service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Action: action, Anchor: "H10",
	}
	if action == service.PivotAdd {
		req.Source = "A10:E16"
	}
	return req
}

// sentPivot is the pivot the last batchUpdate carried, as JSON.
func sentPivot(t *testing.T, srv *sheetstest.Server) string {
	t.Helper()
	var body string
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			body = c.Body
		}
	}
	var req gsheets.BatchUpdateSpreadsheetRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil || len(req.Requests) != 1 || req.Requests[0].UpdateCells == nil {
		t.Fatalf("no pivot write was sent: %s", body)
	}
	return string(req.Requests[0].UpdateCells.Rows[0].Values[0].PivotTable)
}

// canonical is JSON with its keys in one order, for comparing two
// pivots that differ only in how they were serialized.
func canonical(t *testing.T, text string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %s", text)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// richPivot is an add using every rule this tool writes: a date rule,
// a histogram, a list to show and a condition, and both kinds of
// calculated value.
func richPivot() service.PivotRequest {
	req := salesPivot(service.PivotAdd)
	req.Rows = []string{"Day by year_month"}
	req.Columns = []string{"Age every 10 from 20 to 70"}
	req.Values = []string{"Revenue sum", "=Revenue-Cost sum as Margin", "=SUM(Revenue)/SUM(Cost) as Ratio"}
	req.Filters = []string{"Region show East, West", "Revenue number_greater 50"}
	return req
}

// richPivotJSON is what richPivot sends. A calculated value carries no
// offset, since the two are a union Google refuses both of. A condition
// alone is visible by default, the one way it shows what meets it.
const richPivotJSON = `{"columns":[{"sourceColumnOffset":2,"showTotals":true,"sortOrder":"ASCENDING",` +
	`"groupRule":{"histogramRule":{"interval":10,"start":20,"end":70}}}],` +
	`"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"visibleValues":["East","West"]}},` +
	`{"columnOffsetIndex":3,"filterCriteria":{"condition":{"type":"NUMBER_GREATER","values":[{"userEnteredValue":"50"}]},` +
	`"visibleByDefault":true}}],` +
	`"rows":[{"sourceColumnOffset":1,"showTotals":true,"sortOrder":"ASCENDING",` +
	`"groupRule":{"dateTimeRule":{"type":"YEAR_MONTH"}}}],` +
	`"source":{"sheetId":1837,"startRowIndex":9,"endRowIndex":16,"startColumnIndex":0,"endColumnIndex":5},` +
	`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"},` +
	`{"formula":"=Revenue-Cost","summarizeFunction":"SUM","name":"Margin"},` +
	`{"formula":"=SUM(Revenue)/SUM(Cost)","summarizeFunction":"CUSTOM","name":"Ratio"}]}`

func TestPivotAddSendsRulesFiltersAndCalculatedValues(t *testing.T) {
	srv, svc := salesService(t)
	res, err := svc.ManagePivotTable(context.Background(), richPivot())
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := sentPivot(t, srv); got != richPivotJSON {
		t.Errorf("sent\n%s\nwant\n%s", got, richPivotJSON)
	}
	if want := "filtered by Region show East, West and Revenue number_greater 50"; !strings.Contains(res.Render(), want) {
		t.Errorf("the result does not say %q:\n%s", want, res.Render())
	}
}

// TestPivotListReadsTheRulesBackAsTheyAreWritten is the round trip list
// promises: what it reports, sent back as it reads, is the same pivot.
func TestPivotListReadsTheRulesBackAsTheyAreWritten(t *testing.T) {
	srv, svc := salesService(t)
	ctx := context.Background()
	if _, err := svc.ManagePivotTable(ctx, richPivot()); err != nil {
		t.Fatalf("add: %v", err)
	}
	list := salesPivot(service.PivotList)
	list.Range = "H10:L30"
	res, err := svc.ManagePivotTable(ctx, list)
	if err != nil || len(res.Pivots) != 1 {
		t.Fatalf("list: %v %+v", err, res)
	}
	got := res.Pivots[0]
	want := service.PivotRecord{
		Rows:    []string{"B by year_month"},
		Columns: []string{"C every 10 from 20 to 70"},
		Values:  []string{"D sum", "=Revenue-Cost sum as Margin", "=SUM(Revenue)/SUM(Cost) as Ratio"},
		Filters: []string{"A show East, West", "D number_greater 50"},
	}
	for _, field := range []struct {
		name      string
		got, want []string
	}{
		{"rows", got.Rows, want.Rows}, {"columns", got.Columns, want.Columns},
		{"values", got.Values, want.Values}, {"filters", got.Filters, want.Filters},
	} {
		if strings.Join(field.got, "|") != strings.Join(field.want, "|") {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	for _, line := range []string{
		`group_rows ["B by year_month"]`,
		`filters ["A show East, West", "D number_greater 50"]`,
	} {
		if !strings.Contains(res.Render(), line) {
			t.Errorf("the listing does not say %s:\n%s", line, res.Render())
		}
	}

	update := salesPivot(service.PivotUpdate)
	update.Rows, update.Columns, update.Values, update.Filters = got.Rows, got.Columns, got.Values, got.Filters
	if _, err := svc.ManagePivotTable(ctx, update); err != nil {
		t.Fatalf("sending the listing back: %v", err)
	}
	// The stored pivot carries criteria beside filterSpecs, as a
	// response does, and its source in the stored key order; the update
	// sends filterSpecs alone, and the same source.
	if sent := sentPivot(t, srv); canonical(t, sent) != canonical(t, richPivotJSON) {
		t.Errorf("the listing sent back is another pivot:\n%s\nwant\n%s", sent, richPivotJSON)
	}
}

// TestPivotListReadsWhatOnlySheetsWrites is a pivot made in the Sheets
// interface: a grouping by hand, a summary this server has no word for,
// and filters in the older criteria map alone. Each is said, and the
// hand grouping in words the parser refuses, so it is not lost quietly.
func TestPivotListReadsWhatOnlySheetsWrites(t *testing.T) {
	srv, svc := standard(t)
	seedPivot(t, srv, `{`+sourceAC+`,`+
		`"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING","groupRule":{"manualRule":{"groups":`+
		`[{"groupName":{"stringValue":"Grouped"},"items":[{"stringValue":"Skerry"}]}]}}}],`+
		`"values":[{"sourceColumnOffset":1,"summarizeFunction":"NONE","name":"Spread","calculatedDisplayType":"PERCENT_OF_GRAND_TOTAL"}],`+
		`"criteria":{"2":{"visibleValues":["Skerry"]},"0":{"visibleByDefault":true,"condition":{"type":"TEXT_STARTS_WITH",`+
		`"values":[{"userEnteredValue":"Sk"}]}}}}`)
	res, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotList, Range: "F1:H5",
	})
	if err != nil || len(res.Pivots) != 1 {
		t.Fatalf("list: %v %+v", err, res)
	}
	p := res.Pivots[0]
	if got := strings.Join(append(append(p.Rows, p.Values...), p.Filters...), "|"); got !=
		"A by hand|B none as Spread|A text_starts_with Sk|C show Skerry" {
		t.Errorf("read back as %q", got)
	}
}

// TestPivotFiltersReplaceAndClearBothForms is the trap in the
// reference: a response carries criteria beside filterSpecs, and a
// request's filterSpecs win only where it sends both. So a write that
// changes filters, or clears them, takes criteria out too.
func TestPivotFiltersReplaceAndClearBothForms(t *testing.T) {
	both := `{` + sourceAC + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
		`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}],` +
		`"filterSpecs":[{"columnOffsetIndex":2,"filterCriteria":{"visibleValues":["9"]}}],` +
		`"criteria":{"2":{"visibleValues":["9"]}}}`
	for _, tc := range []struct {
		name string
		edit func(*service.PivotRequest)
		want string
	}{
		{"replaced", func(r *service.PivotRequest) { r.Filters = []string{"A show Skerry"} },
			`"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"visibleValues":["Skerry"]}}]`},
		{"cleared", func(r *service.PivotRequest) { r.ClearFilters = true }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			seedPivot(t, srv, both)
			req := service.PivotRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotUpdate, Anchor: "F1",
			}
			tc.edit(&req)
			if _, err := svc.ManagePivotTable(context.Background(), req); err != nil {
				t.Fatalf("update: %v", err)
			}
			sent := sentPivot(t, srv)
			if strings.Contains(sent, "criteria") {
				t.Errorf("the update sent criteria, which would bring the old filters back: %s", sent)
			}
			if tc.want == "" && strings.Contains(sent, "filterSpecs") || tc.want != "" && !strings.Contains(sent, tc.want) {
				t.Errorf("sent %s, want filters %q", sent, tc.want)
			}
		})
	}
}

func TestPivotRuleAndFilterRefusals(t *testing.T) {
	byHandInColumns := `{` + sourceAC + `,"rows":[{"sourceColumnOffset":1,"sortOrder":"ASCENDING"}],` +
		`"columns":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING","groupRule":{"manualRule":{"groups":[]}}}],` +
		`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`
	for _, tc := range []struct {
		name string
		seed string // a pivot at F1 of the first sheet; "" for the sales block
		edit func(*service.PivotRequest)
		want string
	}{
		{"two rules on one column", "", func(r *service.PivotRequest) {
			r.Rows, r.Values = []string{"Day by year", "Day by month"}, []string{"Revenue sum"}
		}, "[invalid] column B is grouped by a rule twice, and Google allows one grouping rule per source column. " +
			"Group it once with a rule; a second group without one is allowed"},
		{"a rule beside a kept one made by hand", byHandInColumns, func(r *service.PivotRequest) {
			r.Rows = []string{"A by year"}
		}, "[invalid] column A is grouped by a rule twice, and Google allows one grouping rule per source column. " +
			"Group it once with a rule; a second group without one is allowed"},
		{"a group by hand", "", func(r *service.PivotRequest) {
			r.Rows, r.Values = []string{"Region by hand"}, []string{"Revenue sum"}
		}, `[invalid] "Region by hand" is a grouping made by hand in Sheets, which this tool cannot set; an update ` +
			`that leaves the groups out keeps it`},
		{"a calculated value with no name", "", func(r *service.PivotRequest) {
			r.Rows, r.Values = []string{"Region"}, []string{"=Revenue-Cost sum"}
		}, `[invalid] "=Revenue-Cost sum" is a calculated value, which needs a name: add "as <name>", such as ` +
			`"=Revenue-Cost as Margin"`},
		{"filters and clear_filters", sourceAC, func(r *service.PivotRequest) {
			r.Filters, r.ClearFilters = []string{"A show Skerry"}, true
		}, "[invalid] filters replaces the filters and clear_filters removes them; pass one of the two"},
		{"clear_filters on add", "", func(r *service.PivotRequest) {
			r.Rows, r.Values, r.ClearFilters = []string{"Region"}, []string{"Revenue sum"}, true
		}, "[invalid] clear_filters removes the filters of a pivot table that exists, so it is for update"},
		{"a filter outside the source", "", func(r *service.PivotRequest) {
			r.Rows, r.Values, r.Filters = []string{"Region"}, []string{"Revenue sum"}, []string{"G show x"}
		}, "[invalid] column G is outside the source A10:E16, so it is not a column this pivot table can read"},
		{"two lists for one column", "", func(r *service.PivotRequest) {
			r.Rows, r.Values = []string{"Region"}, []string{"Revenue sum"}
			r.Filters = []string{"Region show East", "A show West"}
		}, `[invalid] filters gives column A two of a kind, as "Region show East" and "A show West"; a column takes ` +
			`one list of values to show and one condition`},
		{"a condition only data validation takes", "", func(r *service.PivotRequest) {
			r.Rows, r.Values, r.Filters = []string{"Region"}, []string{"Revenue sum"}, []string{"Region one_of_list East"}
		}, `[invalid] filter "Region one_of_list East" uses one_of_list, which only data validation takes; a pivot ` +
			`table filter takes blank, custom_formula, date_after, date_before, not_blank, number_between, number_eq, ` +
			`number_greater, number_greater_eq, number_less, number_less_eq, number_not_between, number_not_eq, ` +
			`text_contains, text_ends_with, text_eq, text_not_contains, text_starts_with`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := salesService(t)
			req := salesPivot(service.PivotAdd)
			if tc.seed != "" {
				seed := tc.seed
				if seed == sourceAC {
					seed = `{` + sourceAC + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
						`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`
				}
				seedPivot(t, srv, seed)
				req = service.PivotRequest{
					Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotUpdate, Anchor: "F1",
				}
			}
			tc.edit(&req)
			_, err := svc.ManagePivotTable(context.Background(), req)
			if err == nil || err.Error() != tc.want {
				t.Errorf("error =\n%v\nwant\n%s", err, tc.want)
			}
			if batched(srv) {
				t.Error("a refused pivot reached the wire")
			}
		})
	}
}

// TestPivotUpdateTakesANewSourceWithItsFiltersReplaced is filters as an
// argument: passed again, or cleared, they keep nothing at their old
// offsets, so a new source too narrow for the old ones goes through.
func TestPivotUpdateTakesANewSourceWithItsFiltersReplaced(t *testing.T) {
	filtered := `{` + sourceAD + `,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],` +
		`"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}],` +
		`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{"visibleValues":["Skerry"]}}]}`
	for name, edit := range map[string]func(*service.PivotRequest){
		"filters again":   func(r *service.PivotRequest) { r.Filters = []string{"C number_greater 1"} },
		"filters cleared": func(r *service.PivotRequest) { r.ClearFilters = true },
	} {
		t.Run(name, func(t *testing.T) {
			srv, svc := standard(t)
			seedPivot(t, srv, filtered)
			req := service.PivotRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotUpdate,
				Anchor: "F1", Source: "A1:C6",
			}
			edit(&req)
			if _, err := svc.ManagePivotTable(context.Background(), req); err != nil {
				t.Fatalf("update: %v", err)
			}
		})
	}
}

// TestPivotGroupByAHeadingThatReadsAsARule is a heading with a rule's
// words in it: named whole, it is the column, and no rule is sent.
func TestPivotGroupByAHeadingThatReadsAsARule(t *testing.T) {
	srv, svc := salesService(t)
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(10, 5, sheetstest.Str("Paid by month"))
	req := salesPivot(service.PivotAdd)
	req.Rows, req.Values = []string{"Paid by month"}, []string{"Revenue sum"}
	if _, err := svc.ManagePivotTable(context.Background(), req); err != nil {
		t.Fatalf("add: %v", err)
	}
	if sent := sentPivot(t, srv); !strings.Contains(sent, `"rows":[{"sourceColumnOffset":4,"showTotals":true,"sortOrder":"ASCENDING"}]`) {
		t.Errorf("sent %s", sent)
	}
}

// TestPivotHeadingThatIsAlsoALetter is "Age", which reads as the column
// AGE, far outside the source. A letter outside the source is looked up
// among the headings, so it has to read them.
func TestPivotHeadingThatIsAlsoALetter(t *testing.T) {
	srv, svc := salesService(t)
	req := salesPivot(service.PivotAdd)
	req.Rows, req.Values = []string{"Age every 10"}, []string{"D sum"}
	if _, err := svc.ManagePivotTable(context.Background(), req); err != nil {
		t.Fatalf("add: %v", err)
	}
	if sent := sentPivot(t, srv); !strings.Contains(sent, `"rows":[{"sourceColumnOffset":2,`) {
		t.Errorf("sent %s", sent)
	}
}

// TestPivotUpdateKeepsACalculatedValueAcrossANewSource is a kept value
// with no column: a calculated value names its columns inside the
// formula, so a new source that starts elsewhere moves nothing of it.
func TestPivotUpdateKeepsACalculatedValueAcrossANewSource(t *testing.T) {
	srv, svc := standard(t)
	seedPivot(t, srv, `{`+sourceAC+`,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],`+
		`"values":[{"formula":"=1","summarizeFunction":"CUSTOM","name":"One"}]}`)
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotUpdate,
		Anchor: "F1", Source: "B1:D6", Rows: []string{"B"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if sent := sentPivot(t, srv); !strings.Contains(sent, `"values":[{"formula":"=1","name":"One","summarizeFunction":"CUSTOM"}]`) {
		t.Errorf("the calculated value was not kept: %s", sent)
	}
}

// TestPivotUpdateKeepsAGroupingMadeByHand is what an update that leaves
// the groups out promises: a grouping made in Sheets goes back as read.
func TestPivotUpdateKeepsAGroupingMadeByHand(t *testing.T) {
	srv, svc := standard(t)
	manual := `{"groups":[{"groupName":{"stringValue":"Grouped"},"items":[{"stringValue":"Skerry"}]}]}`
	seedPivot(t, srv, `{`+sourceAC+`,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING","groupRule":`+
		`{"manualRule":`+manual+`}}],"values":[{"sourceColumnOffset":1,"summarizeFunction":"SUM"}]}`)
	if _, err := svc.ManagePivotTable(context.Background(), service.PivotRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Action: service.PivotUpdate,
		Anchor: "F1", Values: []string{"C max"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if sent := sentPivot(t, srv); !strings.Contains(sent, `"groupRule":{"manualRule":`+manual+`}`) {
		t.Errorf("the grouping by hand was not kept: %s", sent)
	}
}
