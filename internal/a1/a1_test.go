package a1

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

func TestParseColumn(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"A", 1, true},
		{"a", 1, true},
		{"Z", 26, true},
		{"AA", 27, true},
		{"AB", 28, true},
		{"AZ", 52, true},
		{"BA", 53, true},
		{"ZZ", 702, true},
		{"AAA", 703, true},
		{"ZZZ", MaxColumns, true},
		// Everything below returned a plausible index in a shipped
		// server, because only the empty string was rejected: "A1" came
		// back as column K and "B2" as column AL.
		{"A1", 0, false},
		{"B2", 0, false},
		{"", 0, false},
		{" ", 0, false},
		{"-", 0, false},
		{"A-", 0, false},
		{"1", 0, false},
		{"AAAA", 0, false},
		{"ZZZZ", 0, false},
		{"A B", 0, false},
	} {
		got, err := ParseColumn(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("ParseColumn(%q) = error %v, want %d", tc.in, err, tc.want)
			} else if got != tc.want {
				t.Errorf("ParseColumn(%q) = %d, want %d", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseColumn(%q) = %d, want an error", tc.in, got)
		} else if !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseColumn(%q) error %v does not wrap ErrInvalid", tc.in, err)
		}
	}
}

func TestColumnNameRoundTrips(t *testing.T) {
	for col := 1; col <= MaxColumns; col++ {
		name, err := ColumnName(col)
		if err != nil {
			t.Fatalf("ColumnName(%d): %v", col, err)
		}
		back, err := ParseColumn(name)
		if err != nil {
			t.Fatalf("ParseColumn(%q) from column %d: %v", name, col, err)
		}
		if back != col {
			t.Fatalf("column %d rendered %q which parsed back as %d", col, name, back)
		}
	}
	if _, err := ColumnName(0); err == nil {
		t.Error("ColumnName(0) should be refused; columns count from 1")
	}
	if _, err := ColumnName(MaxColumns + 1); err == nil {
		t.Error("ColumnName past ZZZ should be refused")
	}
}

func TestCellName(t *testing.T) {
	for _, tc := range []struct {
		col, row int
		want     string
		ok       bool
	}{
		{1, 1, "A1", true},
		{27, 100, "AA100", true},
		{MaxColumns, MaxRows, "ZZZ10000000", true},
		{0, 1, "", false},
		{1, 0, "", false},
		{1, MaxRows + 1, "", false},
	} {
		got, err := CellName(tc.col, tc.row)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("CellName(%d,%d) = %q, %v; want %q", tc.col, tc.row, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("CellName(%d,%d) = %q, want an error", tc.col, tc.row, got)
		}
	}
}

func TestParseRect(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Rect
		ok   bool
	}{
		{"", WholeSheet, true},
		{"A1", Rect{1, 1, 1, 1}, true},
		{"a1", Rect{1, 1, 1, 1}, true},
		{"B2:D40", Rect{2, 2, 4, 40}, true},
		{"D40:B2", Rect{2, 2, 4, 40}, true}, // reversed corners are the same rectangle
		{"B:B", Rect{2, 0, 2, 0}, true},
		{"B:D", Rect{2, 0, 4, 0}, true},
		{"2:5", Rect{0, 2, 0, 5}, true},
		{"5:2", Rect{0, 2, 0, 5}, true},
		{"A5:A", Rect{1, 5, 1, 0}, true},
		{"A:B5", Rect{1, 0, 2, 5}, true},
		{"ZZZ10000000", Rect{MaxColumns, MaxRows, MaxColumns, MaxRows}, true},
		// Rejected. Each of these is a shape some caller will send.
		{"A", Rect{}, false},
		{"5", Rect{}, false},
		{"A:5", Rect{}, false},
		{"1:B", Rect{}, false},
		{"A1:B2:C3", Rect{}, false},
		{"A0", Rect{}, false},
		{"A1:", Rect{}, false},
		{":B2", Rect{}, false},
		{"AAAA1", Rect{}, false},
		{"A1:ZZZZ9", Rect{}, false},
		{"Sheet!A1", Rect{}, false}, // a sheet belongs to Parse, not here
		{"$A$1", Rect{}, false},     // absolute references are a formula spelling
		{"A1 B2", Rect{}, false},
	} {
		got, err := ParseRect(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("ParseRect(%q) = error %v, want %+v", tc.in, err, tc.want)
			} else if got != tc.want {
				t.Errorf("ParseRect(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseRect(%q) = %+v, want an error", tc.in, got)
		}
	}
}

// TestQuotedSheetTrap is the case that fails silently rather than
// loudly: "A1" is a cell of the first visible sheet and "'A1'" is the
// whole sheet named A1.
//
// The related trap, verified live rather than taken from the guide: a
// bare reference with no "!" resolves as a *named range* first, so a
// spreadsheet with a named range called Data makes "Data" mean the named
// range and "'Data'" mean the sheet. Format quotes everything, which is
// what keeps the whole-sheet form meaning the sheet.
func TestQuotedSheetTrap(t *testing.T) {
	cell, err := Parse("A1")
	if err != nil {
		t.Fatalf("Parse(\"A1\"): %v", err)
	}
	if cell.HasSheet || cell.Rect != (Rect{1, 1, 1, 1}) {
		t.Errorf("Parse(\"A1\") = %+v, want the cell A1 with no sheet", cell)
	}

	sheet, err := Parse("'A1'")
	if err != nil {
		t.Fatalf("Parse(\"'A1'\"): %v", err)
	}
	if !sheet.HasSheet || sheet.Sheet != "A1" || sheet.Rect != WholeSheet {
		t.Errorf("Parse(\"'A1'\") = %+v, want the whole sheet named A1", sheet)
	}

	// Whatever a title contains, Format quotes it, so the reference this
	// server sends can never be resolved to a named range instead.
	for _, title := range []string{"Data", "A1", "Sheet1", "Página1", "My Custom Sheet", "it's", "a!b"} {
		got := Format(title, Rect{1, 1, 2, 2})
		if !strings.HasPrefix(got, "'") {
			t.Errorf("Format(%q, ...) = %q, which is not quoted", title, got)
		}
		back, err := Parse(got)
		if err != nil {
			t.Fatalf("Parse(%q): %v", got, err)
		}
		if back.Sheet != title {
			t.Errorf("Format then Parse turned sheet %q into %q (%q)", title, back.Sheet, got)
		}
	}
}

func TestSplitSheet(t *testing.T) {
	for _, tc := range []struct {
		in       string
		sheet    string
		rest     string
		hasSheet bool
		ok       bool
	}{
		{"A1:B2", "", "A1:B2", false, true},
		{"Data!A1", "Data", "A1", true, true},
		{"'My Custom Sheet'!A:A", "My Custom Sheet", "A:A", true, true},
		{"'it''s'!A1", "it's", "A1", true, true},
		{"'A1'", "A1", "", true, true},
		{"'Data'", "Data", "", true, true},
		{"'a!b'!A1", "a!b", "A1", true, true},
		{"'unclosed!A1", "", "", false, false},
		{"'closed' A1", "", "", false, false},
		{"!A1", "", "", false, false},
	} {
		sheet, rest, has, err := SplitSheet(tc.in)
		if !tc.ok {
			if err == nil {
				t.Errorf("SplitSheet(%q) = %q,%q,%v; want an error", tc.in, sheet, rest, has)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitSheet(%q): %v", tc.in, err)
			continue
		}
		if sheet != tc.sheet || rest != tc.rest || has != tc.hasSheet {
			t.Errorf("SplitSheet(%q) = %q,%q,%v; want %q,%q,%v", tc.in, sheet, rest, has, tc.sheet, tc.rest, tc.hasSheet)
		}
	}
}

func TestFormatRect(t *testing.T) {
	for _, tc := range []struct {
		in   Rect
		want string
	}{
		{WholeSheet, ""},
		{Rect{1, 1, 1, 1}, "A1"},
		{Rect{2, 2, 4, 40}, "B2:D40"},
		{Rect{2, 0, 2, 0}, "B:B"},
		{Rect{0, 2, 0, 5}, "2:5"},
		{Rect{1, 5, 1, 0}, "A5:A"},
		{Rect{0, 0, 3, 0}, "A:C"},
		{Rect{0, 0, 0, 5}, "1:5"},
		{Rect{2, 0, 0, 0}, "B:ZZZ"},
		{Rect{0, 2, 0, 0}, "2:10000000"},
	} {
		if got := FormatRect(tc.in); got != tc.want {
			t.Errorf("FormatRect(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatParseRoundTrip is the guard on the conversion this server
// does most often: every rectangle it renders must parse back to itself.
func TestFormatParseRoundTrip(t *testing.T) {
	for _, r := range []Rect{
		{1, 1, 1, 1},
		{2, 2, 4, 40},
		{2, 0, 2, 0},
		{2, 0, 4, 0},
		{0, 2, 0, 5},
		{1, 5, 1, 0},
		{MaxColumns, MaxRows, MaxColumns, MaxRows},
	} {
		s := FormatRect(r)
		back, err := ParseRect(s)
		if err != nil {
			t.Fatalf("ParseRect(%q) from %+v: %v", s, r, err)
		}
		if back != r {
			t.Errorf("%+v rendered %q which parsed back as %+v", r, s, back)
		}
	}
}

func TestGridRangeConversion(t *testing.T) {
	// The discovery document's own example: Sheet1!A1:A1 is
	// startRowIndex 0, endRowIndex 1. Half open, zero based, both.
	g := Rect{1, 1, 1, 1}.GridRange(7)
	if g.SheetID != 7 || *g.StartRowIndex != 0 || *g.EndRowIndex != 1 || *g.StartColumnIndex != 0 || *g.EndColumnIndex != 1 {
		t.Fatalf("A1:A1 became %+v", g)
	}

	for _, r := range []Rect{
		WholeSheet,
		{1, 1, 1, 1},
		{2, 2, 4, 40},
		{2, 0, 2, 0},
		{0, 2, 0, 5},
		{1, 5, 1, 0},
		{MaxColumns, MaxRows, MaxColumns, MaxRows},
	} {
		if back := FromGridRange(r.GridRange(1)); back != r {
			t.Errorf("%+v round-tripped through GridRange as %+v", r, back)
		}
	}

	// An unbounded side is a missing index, not a zero one: sending
	// startRowIndex 0 for "no start" would be row 1 and mean something.
	whole := WholeSheet.GridRange(3)
	if whole.StartRowIndex != nil || whole.EndRowIndex != nil || whole.StartColumnIndex != nil || whole.EndColumnIndex != nil {
		t.Errorf("a whole sheet became %+v; unbounded sides must be absent", whole)
	}
	if FromGridRange(nil) != WholeSheet {
		t.Error("a nil GridRange is a whole sheet")
	}
	// The API sends merges and protected ranges this way round.
	col := &gsheets.GridRange{SheetID: 1, StartColumnIndex: gsheets.Ptr(1), EndColumnIndex: gsheets.Ptr(2)}
	if got := FromGridRange(col); got != (Rect{2, 0, 2, 0}) {
		t.Errorf("a whole-column GridRange became %+v", got)
	}
}

func TestClampResolvesTheWindowBeforeTheCall(t *testing.T) {
	// A read of "A:Z" on a 1000-row sheet must become a finite range
	// before anything is sent. Clamping the rendering while fetching the
	// open range whole is the bug this avoids, not repeats.
	got := Rect{1, 0, 26, 0}.Clamp(1000, 26)
	if got != (Rect{1, 1, 26, 1000}) {
		t.Errorf("A:Z clamped to %+v, want A1:Z1000", got)
	}
	if cells, ok := got.Cells(); !ok || cells != 26000 {
		t.Errorf("Cells() = %d,%v; want 26000,true", cells, ok)
	}
	if _, ok := (Rect{1, 0, 26, 0}).Cells(); ok {
		t.Error("an unbounded rectangle must not report a cell count")
	}

	for _, tc := range []struct {
		in         Rect
		rows, cols int
		want       Rect
	}{
		{WholeSheet, 100, 5, Rect{1, 1, 5, 100}},
		{Rect{1, 1, 999, 999}, 10, 3, Rect{1, 1, 3, 10}},
		{Rect{1, 50, 1, 0}, 10, 3, Rect{1, 10, 1, 10}},
		{WholeSheet, 0, 0, Rect{1, 1, 1, 1}},
	} {
		if got := tc.in.Clamp(tc.rows, tc.cols); got != tc.want {
			t.Errorf("%+v.Clamp(%d,%d) = %+v, want %+v", tc.in, tc.rows, tc.cols, got, tc.want)
		}
	}
}

func TestOverlapsAndContains(t *testing.T) {
	for _, tc := range []struct {
		a, b     Rect
		overlaps bool
		contains bool
	}{
		{Rect{1, 1, 4, 4}, Rect{2, 2, 3, 3}, true, true},
		{Rect{1, 1, 4, 4}, Rect{5, 5, 6, 6}, false, false},
		{Rect{1, 1, 4, 4}, Rect{4, 4, 6, 6}, true, false},
		{Rect{2, 0, 2, 0}, Rect{2, 7, 2, 7}, true, true},  // whole column B contains B7
		{Rect{2, 7, 2, 7}, Rect{2, 0, 2, 0}, true, false}, // but not the other way round
		{WholeSheet, Rect{9, 9, 9, 9}, true, true},
		{Rect{0, 2, 0, 5}, Rect{1, 6, 1, 6}, false, false}, // rows 2-5 miss row 6
	} {
		if got := tc.a.Overlaps(tc.b); got != tc.overlaps {
			t.Errorf("%+v.Overlaps(%+v) = %v, want %v", tc.a, tc.b, got, tc.overlaps)
		}
		if got := tc.a.Contains(tc.b); got != tc.contains {
			t.Errorf("%+v.Contains(%+v) = %v, want %v", tc.a, tc.b, got, tc.contains)
		}
	}
}

func TestRowsAndCols(t *testing.T) {
	r := Rect{2, 3, 5, 10}
	if r.Cols() != 4 || r.Rows() != 8 {
		t.Errorf("Rect{2,3,5,10} is %d cols by %d rows", r.Cols(), r.Rows())
	}
	if (Rect{2, 0, 2, 0}).Rows() != 0 || (Rect{0, 2, 0, 5}).Cols() != 0 {
		t.Error("an unbounded side has no size")
	}
	if !r.Bounded() || (Rect{2, 0, 2, 0}).Bounded() {
		t.Error("Bounded is wrong")
	}
}

func TestParseKeepsSheetAndRect(t *testing.T) {
	ref, err := Parse("'Página1'!B2:D40")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ref.Sheet != "Página1" || !ref.HasSheet || ref.Rect != (Rect{2, 2, 4, 40}) {
		t.Errorf("Parse gave %+v", ref)
	}
	if _, err := Parse("'Data'!nonsense"); err == nil {
		t.Error("a bad range after a good sheet must still fail")
	}
}

// The one-axis conversion. A1 counts rows and columns from one and
// includes both ends; the API counts from zero and excludes the far one,
// and getting that wrong deletes the wrong row.
func TestBandIndices(t *testing.T) {
	for _, tc := range []struct {
		first, last, start, end int
	}{
		{first: 1, last: 1, start: 0, end: 1},
		{first: 2, last: 5, start: 1, end: 5},
		{first: 10, last: 10, start: 9, end: 10},
	} {
		start, end := BandIndices(tc.first, tc.last)
		if start != tc.start || end != tc.end {
			t.Errorf("BandIndices(%d, %d) = %d, %d, want %d, %d",
				tc.first, tc.last, start, end, tc.start, tc.end)
		}
		// The count survives: a band of n rows is n indices wide.
		if end-start != tc.last-tc.first+1 {
			t.Errorf("BandIndices(%d, %d) covers %d, want %d",
				tc.first, tc.last, end-start, tc.last-tc.first+1)
		}
	}
}

func TestZeroBased(t *testing.T) {
	for one, want := range map[int]int{1: 0, 2: 1, 100: 99} {
		if got := ZeroBased(one); got != want {
			t.Errorf("ZeroBased(%d) = %d, want %d", one, got, want)
		}
	}
}

// The band conversion and the rectangle conversion are the same
// arithmetic, and a test that says so is what keeps them from drifting
// apart the day one of them is "fixed".
func TestBandIndicesAgreesWithGridRange(t *testing.T) {
	for first := 1; first <= 4; first++ {
		for last := first; last <= 6; last++ {
			start, end := BandIndices(first, last)
			g := Rect{FirstRow: first, LastRow: last}.GridRange(0)
			if start != *g.StartRowIndex || end != *g.EndRowIndex {
				t.Errorf("rows %d:%d: band gave %d,%d and GridRange gave %d,%d",
					first, last, start, end, *g.StartRowIndex, *g.EndRowIndex)
			}
		}
	}
}

func TestIntersect(t *testing.T) {
	for _, tc := range []struct {
		a, b  Rect
		want  Rect
		share bool
	}{
		{Rect{1, 1, 4, 4}, Rect{2, 2, 6, 6}, Rect{2, 2, 4, 4}, true},
		{Rect{1, 1, 4, 4}, Rect{5, 5, 6, 6}, Rect{}, false},
		{Rect{1, 1, 4, 4}, Rect{4, 4, 4, 4}, Rect{4, 4, 4, 4}, true}, // a shared corner
		// An unbounded side reaches the edge of the sheet, so it takes
		// the other rectangle's edge and stays unbounded only where both
		// are.
		{Rect{2, 0, 2, 0}, Rect{2, 2, 3, 7}, Rect{2, 2, 2, 7}, true},
		{WholeSheet, Rect{9, 9, 9, 9}, Rect{9, 9, 9, 9}, true},
		{WholeSheet, WholeSheet, WholeSheet, true},
	} {
		got, share := tc.a.Intersect(tc.b)
		if share != tc.share || got != tc.want {
			t.Errorf("%+v.Intersect(%+v) = %+v, %v; want %+v, %v", tc.a, tc.b, got, share, tc.want, tc.share)
		}
	}
}

func TestLimitRows(t *testing.T) {
	r := Rect{FirstCol: 1, FirstRow: 10, LastCol: 3, LastRow: 100}
	// Which end survives is the whole difference between the two.
	if got, cut := r.LimitRows(5); !cut || got != (Rect{FirstCol: 1, FirstRow: 10, LastCol: 3, LastRow: 14}) {
		t.Errorf("LimitRows(5) = %+v, cut %v", got, cut)
	}
	if got, cut := r.LimitRowsFromEnd(5); !cut || got != (Rect{FirstCol: 1, FirstRow: 96, LastCol: 3, LastRow: 100}) {
		t.Errorf("LimitRowsFromEnd(5) = %+v, cut %v", got, cut)
	}
	open := Rect{FirstCol: 1, LastCol: 3}
	for name, f := range map[string]func(int) (Rect, bool){
		"LimitRows": r.LimitRows, "LimitRowsFromEnd": r.LimitRowsFromEnd,
	} {
		if got, cut := f(1000); cut || got != r {
			t.Errorf("%s(1000) = %+v, cut %v; a rectangle already inside the limit is untouched", name, got, cut)
		}
		if got, cut := f(0); cut || got != r {
			t.Errorf("%s(0) = %+v, cut %v; no limit is no trim", name, got, cut)
		}
	}
	// An unbounded side has no row count, so there is nothing to trim.
	for name, f := range map[string]func(int) (Rect, bool){
		"LimitRows": open.LimitRows, "LimitRowsFromEnd": open.LimitRowsFromEnd,
	} {
		if got, cut := f(5); cut || got != open {
			t.Errorf("%s on an unbounded rectangle = %+v, cut %v", name, got, cut)
		}
	}
}
