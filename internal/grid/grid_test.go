package grid

import (
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// data builds a GridData the way the API sends one: trailing empty rows
// and trailing empty cells omitted.
func data(startRow, startCol int, rows ...[]*gsheets.CellData) *gsheets.GridData {
	g := &gsheets.GridData{StartRow: startRow, StartColumn: startCol}
	for _, r := range rows {
		g.RowData = append(g.RowData, &gsheets.RowData{Values: r})
	}
	return g
}

func TestBuildPadsToTheRequestedRectangle(t *testing.T) {
	// Asked for A1:C4; the API answered two rows, the second of them
	// short. Every address in the result still has to be its own.
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 4}
	g := Build("Vandel", 0, rect, data(0, 0,
		[]*gsheets.CellData{sheetstest.Str("Plimth"), sheetstest.Str("Nardle"), sheetstest.Str("Grivet")},
		[]*gsheets.CellData{sheetstest.Str("Skerry")},
	), AsRaw)

	if len(g.Cells) != 4 {
		t.Fatalf("grid has %d rows, want 4", len(g.Cells))
	}
	for i, row := range g.Cells {
		if len(row) != 3 {
			t.Fatalf("row %d has %d cells, want 3", i, len(row))
		}
	}
	if g.Cells[0][2].Display != "Grivet" {
		t.Errorf("C1 = %q", g.Cells[0][2].Display)
	}
	if !g.Cells[1][1].Empty() || !g.Cells[3][0].Empty() {
		t.Error("the padding is not empty")
	}
	if g.DataRows != 2 {
		t.Errorf("DataRows = %d, want 2; the read has to say how far the data reached", g.DataRows)
	}
	if got := g.Address(2, 1); got != "B3" {
		t.Errorf("Address(2,1) = %q, want B3", got)
	}
	if c, ok := g.At(1, 1); !ok || c.Display != "Plimth" {
		t.Errorf("At(1,1) = %+v, %v", c, ok)
	}
	if _, ok := g.At(99, 1); ok {
		t.Error("At outside the grid should say so")
	}
}

func TestBuildHonoursAResponseOffset(t *testing.T) {
	// A response may start further in than the request did.
	rect := a1.Rect{FirstCol: 2, FirstRow: 2, LastCol: 4, LastRow: 4}
	g := Build("Vandel", 0, rect, data(2, 2,
		[]*gsheets.CellData{sheetstest.Str("Quorbin")},
	), AsRaw)
	if g.Cells[1][1].Display != "Quorbin" {
		t.Errorf("C3 = %q; the response's own origin was not honoured", g.Cells[1][1].Display)
	}
	if !g.Cells[0][0].Empty() {
		t.Error("B2 should be empty")
	}
	if g := Build("Vandel", 0, rect, nil, AsRaw); g.DataRows != 0 {
		t.Error("a nil response is an empty grid, not a crash")
	}
}

func TestCellKinds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      *gsheets.CellData
		kind    Kind
		display string
		formula string
	}{
		{"nil", nil, KindEmpty, "", ""},
		{"text", sheetstest.Str("Nardle"), KindText, "Nardle", ""},
		{"number", sheetstest.Num(12.5, "12.50"), KindNumber, "12.5", ""},
		{"bool", sheetstest.Bool(true), KindBool, "TRUE", ""},
		{"formula", sheetstest.Formula("=B2+C2", 30, "30.00"), KindFormula, "30", "=B2+C2"},
		{"error", sheetstest.ErrorCell("=1/0", "DIVIDE_BY_ZERO", "cannot be zero"), KindError, "#DIV/0!", "=1/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cell(tc.in, AsRaw)
			if c.Kind != tc.kind || c.Display != tc.display || c.Formula != tc.formula {
				t.Errorf("cell = %+v, want kind %q display %q formula %q", c, tc.kind, tc.display, tc.formula)
			}
		})
	}

	// Formatted is the other rendering of the same cell: a currency
	// symbol in a number the model may want to compute with is a trap,
	// which is why raw is the default and this is a choice.
	if c := cell(sheetstest.Num(12.5, "£12.50"), AsFormatted); c.Display != "£12.50" {
		t.Errorf("formatted display = %q", c.Display)
	}
	if c := cell(sheetstest.Num(12.5, "£12.50"), AsRaw); c.Display != "12.5" {
		t.Errorf("raw display = %q", c.Display)
	}
}

// TestTheFormattedAndRawReadsAgreeOnAnError is the divergence that was
// there: the fake and the renderer each had their own error table with
// different fallbacks, so an unfamiliar error type rendered one way
// formatted and another way raw, and both tests passed.
func TestTheFormattedAndRawReadsAgreeOnAnError(t *testing.T) {
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}
	for _, kind := range []string{"REF", "NAME", "DIVIDE_BY_ZERO", "N_A", "VALUE", "NUM", "ERROR", "NULL_VALUE", "SOMETHING_NEW"} {
		cd := sheetstest.ErrorCell("=x", kind, "")
		raw := Build("Vandel", 0, rect, data(0, 0, []*gsheets.CellData{cd}), AsRaw)
		formatted := Build("Vandel", 0, rect, data(0, 0, []*gsheets.CellData{cd}), AsFormatted)
		if raw.Cells[0][0].Display != formatted.Cells[0][0].Display {
			t.Errorf("%s renders as %q raw and %q formatted",
				kind, raw.Cells[0][0].Display, formatted.Cells[0][0].Display)
		}
		if raw.Cells[0][0].Kind != KindError || raw.Cells[0][0].Error == "" {
			t.Errorf("%s did not come through as an error cell: %+v", kind, raw.Cells[0][0])
		}
	}
	// An error type this build has not met still says which it was.
	if got := (&gsheets.ErrorValue{Type: "SOMETHING_NEW"}).Display(); got != "#SOMETHING_NEW" {
		t.Errorf("an unfamiliar error type rendered as %q, hiding which one it was", got)
	}
}

func TestACellWithOnlyANoteIsNotEmpty(t *testing.T) {
	// A write over it destroys the note, and a values read does not show
	// it, which is exactly the case the guard exists for.
	c := cell(sheetstest.WithNote(&gsheets.CellData{}, "Quorbin reconciliation"), AsRaw)
	if c.Empty() {
		t.Error("a cell carrying a note counts as occupied")
	}
	v := cell(sheetstest.WithValidation(&gsheets.CellData{}, "Skerry", "Plimth"), AsRaw)
	if v.Empty() {
		t.Error("a cell carrying a validation rule counts as occupied")
	}
	if !strings.Contains(v.Validation, "Skerry") {
		t.Errorf("validation described as %q", v.Validation)
	}
	link := cell(&gsheets.CellData{Hyperlink: "https://example.test/a"}, AsRaw)
	if link.Empty() {
		t.Error("a cell carrying a hyperlink counts as occupied")
	}
	if cell(&gsheets.CellData{}, AsRaw).Empty() != true {
		t.Error("a bare cell is empty")
	}
}

func TestDescribeValidation(t *testing.T) {
	for _, tc := range []struct {
		in   *gsheets.DataValidationRule
		want string
	}{
		{&gsheets.DataValidationRule{}, "validated"},
		{&gsheets.DataValidationRule{Condition: &gsheets.BooleanCondition{Type: "NUMBER_GREATER"}}, "number greater"},
		{&gsheets.DataValidationRule{Condition: &gsheets.BooleanCondition{
			Type: "ONE_OF_LIST", Values: []*gsheets.ConditionValue{{UserEnteredValue: "Skerry"}, {UserEnteredValue: "Plimth"}},
		}}, "one of list: Skerry, Plimth"},
		{&gsheets.DataValidationRule{Condition: &gsheets.BooleanCondition{
			Type: "DATE_AFTER", Values: []*gsheets.ConditionValue{{RelativeDate: "PAST_MONTH"}},
		}}, "date after: past_month"},
		{&gsheets.DataValidationRule{Condition: &gsheets.BooleanCondition{
			Type: "CUSTOM_FORMULA", Values: []*gsheets.ConditionValue{{}},
		}}, "custom formula"},
	} {
		if got := describeValidation(tc.in); got != tc.want {
			t.Errorf("describeValidation = %q, want %q", got, tc.want)
		}
	}
}

func TestCount(t *testing.T) {
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 3}
	g := Build("Vandel", 0, rect, data(0, 0,
		[]*gsheets.CellData{
			sheetstest.WithNote(sheetstest.Str("Plimth"), "a note"),
			sheetstest.Formula("=A1", 1, "1"),
			sheetstest.ErrorCell("=1/0", "DIVIDE_BY_ZERO", ""),
		},
		[]*gsheets.CellData{sheetstest.WithValidation(sheetstest.Str("Skerry"), "Skerry")},
	), AsRaw)
	c := g.Count()
	if c.NonEmpty != 4 || c.Formulas != 1 || c.Errors != 1 || c.Notes != 1 || c.Validation != 1 {
		t.Errorf("Count = %+v", c)
	}
}

func TestCheckpointChangesWithTheContent(t *testing.T) {
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 2}
	build := func(v string) *Grid {
		return Build("Vandel", 0, rect, data(0, 0,
			[]*gsheets.CellData{sheetstest.Str(v), sheetstest.Str("Nardle")},
		), AsRaw)
	}
	a := Checkpoint(sheetstest.FixtureID, build("Plimth"))
	b := Checkpoint(sheetstest.FixtureID, build("Plimth"))
	c := Checkpoint(sheetstest.FixtureID, build("Grivet"))
	if a != b {
		t.Errorf("the same content gave two checkpoints: %s and %s", a, b)
	}
	if a == c {
		t.Error("a changed value did not change the checkpoint")
	}
	if !IsCheckpoint(a) || len(a) != len(CheckpointPrefix)+12 {
		t.Errorf("checkpoint %q does not have the documented shape", a)
	}
	// The same values in a different spreadsheet, sheet or range are a
	// different checkpoint: passing one back must not match elsewhere.
	other := Checkpoint(sheetstest.SecondFixtureID, build("Plimth"))
	if other == a {
		t.Error("two spreadsheets share a checkpoint")
	}
	g := build("Plimth")
	g.Sheet = "Ürväl"
	if Checkpoint(sheetstest.FixtureID, g) == a {
		t.Error("two sheets share a checkpoint")
	}
}

func TestCheckpointFollowsTheFormulaNotItsResult(t *testing.T) {
	// A recalculation that changes a number without anybody editing the
	// sheet is not a conflict; an edited formula is.
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}
	one := Build("Vandel", 0, rect, data(0, 0, []*gsheets.CellData{sheetstest.Formula("=NOW()", 1, "1")}), AsRaw)
	two := Build("Vandel", 0, rect, data(0, 0, []*gsheets.CellData{sheetstest.Formula("=NOW()", 2, "2")}), AsRaw)
	if Checkpoint("id", one) != Checkpoint("id", two) {
		t.Error("a recalculated result changed the checkpoint")
	}
	three := Build("Vandel", 0, rect, data(0, 0, []*gsheets.CellData{sheetstest.Formula("=TODAY()", 1, "1")}), AsRaw)
	if Checkpoint("id", one) == Checkpoint("id", three) {
		t.Error("an edited formula did not change the checkpoint")
	}
}

func TestIsCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"ck_0123456789ab", true},
		{"ck_0123456789AB", true},
		{"ck_0123456789", false},
		{"ck_zzzzzzzzzzzz", false},
		{"0123456789ab", false},
		{"", false},
	} {
		if got := IsCheckpoint(tc.in); got != tc.ok {
			t.Errorf("IsCheckpoint(%q) = %v", tc.in, got)
		}
	}
}
