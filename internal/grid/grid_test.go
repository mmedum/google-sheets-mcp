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

// A cell carrying only a note holds no value, so a write into it
// destroys nothing.
//
// This test asserted the opposite, on the reasoning that a write would
// take the note with it. A live probe says values.update leaves the note
// and the validation rule alone, exactly as values.clear documents — so
// the old behaviour refused a write into a blank cell for a loss that
// never happened, and said so in the refusal.
func TestACellWithOnlyAnAnnotationIsEmpty(t *testing.T) {
	c := cell(sheetstest.WithNote(&gsheets.CellData{}, "Quorbin reconciliation"), AsRaw)
	if !c.Empty() {
		t.Error("a cell carrying only a note counts as occupied")
	}
	if c.Note == "" {
		t.Error("the note is not reported at all, so a result could not mention it")
	}
	v := cell(sheetstest.WithValidation(&gsheets.CellData{}, "Skerry", "Plimth"), AsRaw)
	if !v.Empty() {
		t.Error("a cell carrying only a validation rule counts as occupied")
	}
	if !strings.Contains(v.Validation, "Skerry") {
		t.Errorf("validation described as %q", v.Validation)
	}
	// A value still makes it occupied, annotation or not.
	if cell(sheetstest.WithNote(sheetstest.Str("Plimth"), "a note"), AsRaw).Empty() {
		t.Error("a cell with a value and a note is empty")
	}
	if !cell(&gsheets.CellData{}, AsRaw).Empty() {
		t.Error("a bare cell is not empty")
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
	// Two formulas, not one: the error cell holds "=1/0", and a count of
	// what a delete would take has to include a formula that happens to
	// be broken. Errors are counted separately as well.
	if c.NonEmpty != 4 || c.Formulas != 2 || c.Errors != 1 || c.Notes != 1 || c.Validation != 1 {
		t.Errorf("Count = %+v", c)
	}
}

// A formula that evaluated to an error is KindError, so a check on the
// kind misses it — and a write over `=IMPORTRANGE(...)` showing #REF!
// would need only `overwrite`, which is the exact loss the second
// acknowledgement exists to prevent.
func TestAnErroredFormulaIsStillAFormula(t *testing.T) {
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}
	g := Build("Vandel", 0, rect, data(0, 0,
		[]*gsheets.CellData{sheetstest.ErrorCell("=IMPORTRANGE(\"x\",\"y\")", "REF", "")},
	), AsRaw)
	cell, _ := g.At(1, 1)
	if cell.Kind != KindError {
		t.Fatalf("Kind = %q, want the error kind this test is about", cell.Kind)
	}
	if !cell.HasFormula() {
		t.Error("a cell holding a formula that errored did not report one")
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

// A checkpoint is over what the cells store, not over what they show.
//
// Read with formatted=true, a currency cell displays "£1,234.50" where
// it stores 1234.5. Hashing the display made such a checkpoint fail
// against every write — a write reads raw — so expect_checkpoint
// reported a conflict on a range nobody had touched.
func TestCheckpointIgnoresFormatting(t *testing.T) {
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 1}
	cells := []*gsheets.CellData{sheetstest.Num(1234.5, "£1,234.50"), sheetstest.Str("Plimth")}
	raw := Build("Vandel", 0, rect, data(0, 0, cells), AsRaw)
	shown := Build("Vandel", 0, rect, data(0, 0, cells), AsFormatted)

	if shown.Cells[0][0].Display == raw.Cells[0][0].Display {
		t.Fatal("the fixture does not format differently, so this test proves nothing")
	}
	if got, want := Checkpoint("id", shown), Checkpoint("id", raw); got != want {
		t.Errorf("a formatted read gave checkpoint %s and a raw read %s; a write could never match the first", got, want)
	}
}
