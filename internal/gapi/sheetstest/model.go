// Package sheetstest is an in-memory Google Sheets, and the one Drive
// endpoint this server calls, behind httptest.
//
// It exists so the unit tests never touch the network, and so the
// awkward shapes — a formula whose result renders identically, a ragged
// response with trailing empties omitted, a protected range, a 429 —
// are things a test can ask for rather than wait for.
//
// Two rules it keeps deliberately.
//
// The fake does not invent Google's parser. USER_ENTERED coercion is
// recorded from a live probe and replayed; it is never simulated. A fake
// built from the documentation inherits the documentation's errors, and
// a sibling project shipped a search bug its whole suite agreed with.
// Phase 0 reads only, so nothing here parses input yet, and the day it
// does the table comes from spike A.
//
// Every fixture is generated. Titles come from an invented vocabulary
// and numbers from a seeded generator, so there is no path by which
// somebody's spreadsheet reaches testdata/.
package sheetstest

import (
	"math/rand/v2"
	"strconv"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// FixtureID is the spreadsheet id every fixture uses. It says in its own
// text that it is invented, which is what keeps the leak gate's
// exception list from growing with every test.
const FixtureID = "1SyntheticFixtureSpreadsheetIdXXXXXXXXXXXXXXX"

// SecondFixtureID is a second spreadsheet, for the ambiguity cases.
const SecondFixtureID = "1SyntheticFixtureSpreadsheetIdYYYYYYYYYYYYYYY"

// Doc is a spreadsheet held in memory.
type Doc struct {
	ID          string
	Title       string
	Locale      string
	TimeZone    string
	AutoRecalc  string
	Sheets      []*Sheet
	NamedRanges []*gsheets.NamedRange
}

// Sheet is one tab of a Doc. Cells are keyed by zero-based row and
// column, which is the API's own numbering; nothing outside this package
// sees those indices.
type Sheet struct {
	Props       gsheets.SheetProperties
	Cells       map[[2]int]*gsheets.CellData
	Merges      []*gsheets.GridRange
	Protected   []*gsheets.ProtectedRange
	FilterViews []*gsheets.FilterView
	Tables      []*gsheets.Table
	Charts      []*gsheets.EmbeddedChart
	Bandings    []*gsheets.BandedRange
	// Conditional are the sheet's conditional format rules, in the order
	// they are evaluated. The order is the API's identifier for them, so
	// the fake keeps a slice rather than a map.
	Conditional []*gsheets.ConditionalFormatRule
}

// Find returns the sheet with this title.
func (d *Doc) Find(title string) *Sheet {
	for _, s := range d.Sheets {
		if s.Props.Title == title {
			return s
		}
	}
	return nil
}

// FindByID returns the sheet with this id.
func (d *Doc) FindByID(id int) *Sheet {
	for _, s := range d.Sheets {
		if s.Props.SheetID == id {
			return s
		}
	}
	return nil
}

// Set puts a cell at a one-based row and column, the way A1 counts.
func (s *Sheet) Set(row, col int, c *gsheets.CellData) *Sheet {
	if s.Cells == nil {
		s.Cells = map[[2]int]*gsheets.CellData{}
	}
	s.Cells[[2]int{row - 1, col - 1}] = c
	return s
}

// At returns the cell at a one-based row and column, or nil.
func (s *Sheet) At(row, col int) *gsheets.CellData { return s.Cells[[2]int{row - 1, col - 1}] }

// Cell constructors. Each builds what the API would return for a cell
// somebody typed that value into.

// Str is a text cell.
func Str(v string) *gsheets.CellData {
	return &gsheets.CellData{
		UserEnteredValue: &gsheets.ExtendedValue{StringValue: &v},
		EffectiveValue:   &gsheets.ExtendedValue{StringValue: &v},
		FormattedValue:   v,
	}
}

// Num is a number cell. formatted is what Sheets would display, which is
// not the same string as the number.
func Num(v float64, formatted string) *gsheets.CellData {
	if formatted == "" {
		formatted = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return &gsheets.CellData{
		UserEnteredValue: &gsheets.ExtendedValue{NumberValue: &v},
		EffectiveValue:   &gsheets.ExtendedValue{NumberValue: &v},
		FormattedValue:   formatted,
	}
}

// Bool is a TRUE/FALSE cell.
func Bool(v bool) *gsheets.CellData {
	formatted := "FALSE"
	if v {
		formatted = "TRUE"
	}
	return &gsheets.CellData{
		UserEnteredValue: &gsheets.ExtendedValue{BoolValue: &v},
		EffectiveValue:   &gsheets.ExtendedValue{BoolValue: &v},
		FormattedValue:   formatted,
	}
}

// Formula is a cell holding a formula and the value it computed to. The
// two render identically in a values read, which is the whole reason the
// write guard reads userEnteredValue.
func Formula(formula string, result float64, formatted string) *gsheets.CellData {
	if formatted == "" {
		formatted = strconv.FormatFloat(result, 'f', -1, 64)
	}
	return &gsheets.CellData{
		UserEnteredValue: &gsheets.ExtendedValue{FormulaValue: &formula},
		EffectiveValue:   &gsheets.ExtendedValue{NumberValue: &result},
		FormattedValue:   formatted,
	}
}

// ErrorCell is a formula that produced an error, such as #REF! or #N/A.
func ErrorCell(formula, kind, message string) *gsheets.CellData {
	err := &gsheets.ErrorValue{Type: kind, Message: message}
	return &gsheets.CellData{
		UserEnteredValue: &gsheets.ExtendedValue{FormulaValue: &formula},
		EffectiveValue:   &gsheets.ExtendedValue{ErrorValue: err},
		// The same table the renderer reads, so a formatted read and a
		// raw read of the same cell cannot disagree.
		FormattedValue: err.Display(),
	}
}

// WithNote attaches a note, which no values read shows.
func WithNote(c *gsheets.CellData, note string) *gsheets.CellData {
	c.Note = note
	return c
}

// WithValidation attaches a one-of-list validation rule.
func WithValidation(c *gsheets.CellData, values ...string) *gsheets.CellData {
	rule := &gsheets.DataValidationRule{
		Condition: &gsheets.BooleanCondition{Type: "ONE_OF_LIST"},
		Strict:    true,
	}
	for _, v := range values {
		rule.Condition.Values = append(rule.Condition.Values, &gsheets.ConditionValue{UserEnteredValue: v})
	}
	c.DataValidation = rule
	return c
}

// Vocabulary is where every word in a fixture comes from. It is
// invented: no fixture is ever a real spreadsheet trimmed down, and
// nothing here is a word somebody would use for a business of their own.
var Vocabulary = []string{
	"Quorbin", "Vandel", "Skerry", "Plimth", "Nardle", "Grivet",
	"Oblisk", "Trennow", "Yalmic", "Bractal", "Umberly", "Zephrin",
}

// Numbers returns a deterministic run of values. A seeded generator
// rather than typed-out figures, so a large fixture costs no more to
// write than a small one and carries nobody's data either way.
func Numbers(seed uint64, n int) []float64 {
	r := rand.New(rand.NewPCG(seed, 0x5EED))
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(r.IntN(90000)+1000) / 100
	}
	return out
}

// WithFormat attaches a cell format, which is what read_formatting
// summarises and what clear_format takes away.
func WithFormat(c *gsheets.CellData, f *gsheets.CellFormat) *gsheets.CellData {
	c.UserEnteredFormat = f
	return c
}
