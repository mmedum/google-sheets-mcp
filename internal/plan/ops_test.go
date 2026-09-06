package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

// The one arithmetic trap in this package: a band is one-based and
// inclusive the way A1 is, and the API's DimensionRange is zero-based
// and half-open the way every other index it uses is.
func TestDimensionRangesAreZeroBasedAndHalfOpen(t *testing.T) {
	req := plan.InsertDimension(7, gsheets.DimensionRows, 2, 5, true)
	r := req.InsertDimension.Range
	if r.SheetID != 7 || r.Dimension != gsheets.DimensionRows || r.StartIndex != 1 || r.EndIndex != 5 {
		t.Errorf("rows 2:5 became %+v, want start 1 end 5", r)
	}
	// inheritFromBefore cannot be true at the very start: there is
	// nothing there to inherit from.
	if !plan.InsertDimension(7, gsheets.DimensionRows, 1, 2, true).InsertDimension.InheritFromBefore == false {
		t.Error("inheritFromBefore was kept at the first row, where there is nothing before")
	}
}

// The API removes the band and then inserts it, reading the index
// against the sheet before the move. Verified live: rows 1-2 of four
// sent with destinationIndex 3 came back starting at row 2. So a band
// moving down has to be given room for itself, and one moving up does
// not.
func TestMoveDestinationMeansWhereItEndsUp(t *testing.T) {
	for _, tc := range []struct{ first, last, to, sent int }{
		// Rows 1-2 to row 3: two rows come out from above, so the
		// pre-move index has to be two further on.
		{first: 1, last: 2, to: 3, sent: 4},
		{first: 1, last: 2, to: 1, sent: 0},
		// Moving up needs no adjustment: nothing above has shifted.
		{first: 5, last: 6, to: 2, sent: 1},
		{first: 3, last: 3, to: 3, sent: 2},
	} {
		got := plan.MoveDimension(1, gsheets.DimensionRows, tc.first, tc.last, tc.to).MoveDimension.DestinationIndex
		if got != tc.sent {
			t.Errorf("moving rows %d:%d to %d sent index %d, want %d", tc.first, tc.last, tc.to, got, tc.sent)
		}
	}
}

// A mask that named the wrong field would change the wrong thing, so
// every builder's mask is asserted rather than assumed.
func TestUpdateMasksNameExactlyWhatIsSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *gsheets.Request
		want string
	}{
		{"rename", plan.RenameSheet(1, "Grivet"), "title"},
		{"reorder", plan.ReorderSheet(1, 0, 2), "index"},
		{"hide", plan.HideSheet(1, true), "hidden"},
		{"colour", plan.TabColour(1, nil), "tabColorStyle"},
		{"resize rows only", plan.ResizeGrid(1, 500, 0), "gridProperties.rowCount"},
		{"resize both", plan.ResizeGrid(1, 500, 10), "gridProperties.rowCount,gridProperties.columnCount"},
		// Both frozen counts, always: a mask naming only the non-zero
		// one could never undo a freeze.
		{"freeze", plan.Freeze(1, 1, 0), "gridProperties.frozenRowCount,gridProperties.frozenColumnCount"},
	} {
		if got := tc.req.UpdateSheetProperties.Fields; got != tc.want {
			t.Errorf("%s: fields = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A sheet index of zero means "first", not "unset". A request that
// serialised it either way would put a new sheet where nobody asked.
func TestAddSheetOmitsAnIndexNobodyGave(t *testing.T) {
	without, _ := json.Marshal(plan.AddSheet("Grivet", nil, 0, 0))
	if strings.Contains(string(without), "index") {
		t.Errorf("an index reached the wire without being asked for: %s", without)
	}
	with, _ := json.Marshal(plan.AddSheet("Grivet", gsheets.Ptr(0), 0, 0))
	if !strings.Contains(string(with), `"index":0`) {
		t.Errorf("index 0 did not reach the wire: %s", with)
	}
}

// A request that creates a sheet must carry no sheetId. The zero value
// serialises as `"sheetId": 0`, which Google reads as a request for id
// 0 — the id the first sheet always has — and answers with "Sheet with
// id 0 already exists". Found live, and fixed by giving the request its
// own type rather than by remembering not to set the field.
func TestAddSheetSendsNoSheetID(t *testing.T) {
	raw, err := json.Marshal(plan.AddSheet("Grivet", nil, 100, 10))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sheetId") {
		t.Errorf("addSheet asked for an id: %s", raw)
	}
	// The rest of the properties still travel.
	for _, want := range []string{`"title":"Grivet"`, `"rowCount":100`, `"columnCount":10`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("addSheet = %s, missing %s", raw, want)
		}
	}
}

// The requests that act on a sheet that exists must carry the id, and
// zero is a real id: the first sheet has it.
func TestRequestsOnAnExistingSheetKeepIDZero(t *testing.T) {
	for name, req := range map[string]*gsheets.Request{
		"rename": plan.RenameSheet(0, "Grivet"),
		"delete": plan.DeleteSheet(0),
		"insert": plan.InsertDimension(0, gsheets.DimensionRows, 1, 2, false),
	} {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"sheetId":0`) {
			t.Errorf("%s dropped sheet id 0, which is the first sheet: %s", name, raw)
		}
	}
}

// The API removes the sheet and then inserts it, reading the index
// against the order before the move. Verified live: a sheet at index 0
// asked for index 3 landed at 2.
func TestReorderIndexMeansWhereItEndsUp(t *testing.T) {
	for _, tc := range []struct{ current, wanted, sent int }{
		{current: 0, wanted: 3, sent: 4},
		{current: 0, wanted: 1, sent: 2},
		{current: 3, wanted: 0, sent: 0},
		{current: 2, wanted: 2, sent: 2},
	} {
		got := *plan.ReorderSheet(1, tc.current, tc.wanted).UpdateSheetProperties.Properties.Index
		if got != tc.sent {
			t.Errorf("moving from %d to %d sent index %d, want %d", tc.current, tc.wanted, got, tc.sent)
		}
	}
}

func TestParseColour(t *testing.T) {
	for _, tc := range []struct {
		in               string
		nilStyle, hasErr bool
		r, g, b          float64
	}{
		{in: "#ffffff", r: 1, g: 1, b: 1},
		{in: "#000000"},
		{in: "#f00", r: 1},
		{in: "4a90d9", r: 74.0 / 255, g: 144.0 / 255, b: 217.0 / 255},
		{in: "none", nilStyle: true},
		{in: "", nilStyle: true},
		{in: "#12345", hasErr: true},
		{in: "#zzzzzz", hasErr: true},
	} {
		got, err := plan.ParseColour(tc.in)
		switch {
		case tc.hasErr:
			if err == nil {
				t.Errorf("ParseColour(%q) was accepted", tc.in)
			}
			continue
		case err != nil:
			t.Errorf("ParseColour(%q): %v", tc.in, err)
			continue
		case tc.nilStyle:
			if got != nil {
				t.Errorf("ParseColour(%q) = %+v, want nil so the colour is cleared", tc.in, got)
			}
			continue
		}
		c := got.RGBColor
		if !close(c.Red, tc.r) || !close(c.Green, tc.g) || !close(c.Blue, tc.b) || c.Alpha != 1 {
			t.Errorf("ParseColour(%q) = %+v, want %g %g %g", tc.in, c, tc.r, tc.g, tc.b)
		}
	}
}

func close(a, b float64) bool { return a-b < 0.001 && b-a < 0.001 }

// dimension and band have to agree. Either reading of a mismatch would
// move somebody's data somewhere they did not ask for, so it is refused
// rather than guessed at.
func TestParseBandChecksTheTwoAgainstEachOther(t *testing.T) {
	for _, tc := range []struct {
		dim, band   string
		wantDim     string
		first, last int
		wantErr     string
	}{
		{dim: "rows", band: "2:5", wantDim: gsheets.DimensionRows, first: 2, last: 5},
		{dim: "columns", band: "B:D", wantDim: gsheets.DimensionColumns, first: 2, last: 4},
		{dim: "col", band: "B:B", wantDim: gsheets.DimensionColumns, first: 2, last: 2},
		{dim: "rows", band: "B:D", wantErr: "names columns"},
		{dim: "columns", band: "2:5", wantErr: "names rows"},
		{dim: "diagonals", band: "2:5", wantErr: "not rows or columns"},
		{dim: "rows", band: "B2:D5", wantErr: "names a rectangle"},
		{dim: "rows", band: "", wantErr: "names no rows or columns"},
	} {
		got, err := plan.ParseBand(tc.dim, tc.band)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ParseBand(%q, %q) error = %v, want one saying %q", tc.dim, tc.band, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseBand(%q, %q): %v", tc.dim, tc.band, err)
			continue
		}
		if got.Dimension != tc.wantDim || got.First != tc.first || got.Last != tc.last {
			t.Errorf("ParseBand(%q, %q) = %+v", tc.dim, tc.band, got)
		}
	}
}

func TestBandReadsBackTheWayItWasGiven(t *testing.T) {
	rows, _ := plan.ParseBand("rows", "2:5")
	if rows.String() != "rows 2:5" || rows.Count() != 4 {
		t.Errorf("rows band = %q, %d", rows, rows.Count())
	}
	cols, _ := plan.ParseBand("columns", "B:D")
	if cols.String() != "columns B:D" || cols.Count() != 3 {
		t.Errorf("columns band = %q, %d", cols, cols.Count())
	}
}

// Exactly one member of the union is ever set. A request with two would
// be a request nobody wrote and Google would apply both.
func TestEachBuilderSetsOneUnionMember(t *testing.T) {
	for name, req := range map[string]*gsheets.Request{
		"AddSheet":             plan.AddSheet("Grivet", nil, 0, 0),
		"DeleteSheet":          plan.DeleteSheet(1),
		"DuplicateSheet":       plan.DuplicateSheet(1, "Copy", nil),
		"RenameSheet":          plan.RenameSheet(1, "Grivet"),
		"InsertDimension":      plan.InsertDimension(1, gsheets.DimensionRows, 1, 2, false),
		"DeleteDimension":      plan.DeleteDimension(1, gsheets.DimensionRows, 1, 2),
		"MoveDimension":        plan.MoveDimension(1, gsheets.DimensionRows, 1, 2, 3),
		"ResizeDimension":      plan.ResizeDimension(1, gsheets.DimensionColumns, 1, 2, 120),
		"AutoResizeDimensions": plan.AutoResizeDimensions(1, gsheets.DimensionColumns, 1, 2),
		"GroupDimensions":      plan.GroupDimensions(1, gsheets.DimensionRows, 1, 2),
		"UngroupDimensions":    plan.UngroupDimensions(1, gsheets.DimensionRows, 1, 2),
	} {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(members) != 1 {
			t.Errorf("%s set %d union members: %s", name, len(members), raw)
		}
	}
}
