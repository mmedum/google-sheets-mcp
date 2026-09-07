// Package grid is the server's view of a rectangle of cells.
//
// The API returns a cell as a dozen optional fields, several of which
// render identically: a formula and its result, a number and the string
// that displays it, an empty cell and one holding "". This package
// resolves that into one Cell per address, keeps what a values read
// cannot show — the formula underneath, the note beside, the validation
// rule on top — and pads the rectangle back out, because the API omits
// trailing empties and a grid whose addresses have shifted is worse than
// no grid at all.
package grid

import (
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Kind says what a cell holds. A formula is a kind of its own because it
// is the one a value write destroys invisibly.
type Kind string

// Cell kinds.
const (
	KindEmpty   Kind = "empty"
	KindText    Kind = "text"
	KindNumber  Kind = "number"
	KindBool    Kind = "bool"
	KindFormula Kind = "formula"
	KindError   Kind = "error"
)

// Cell is one address's worth of everything.
type Cell struct {
	// Display is what a person sees: the formatted value, or the raw one
	// when the read asked for values unformatted.
	Display string
	// Raw is always the unformatted value, whatever Display carries.
	//
	// It exists for the checkpoint, which has to hash what the cell
	// stores rather than what it shows. Hashing Display meant a read
	// with formatted=true produced a checkpoint no write could match —
	// a write reads raw — so every date or currency in the range made
	// expect_checkpoint report a conflict that had not happened.
	Raw string
	// Formula is the text under the value, "" when the cell was typed
	// rather than computed.
	Formula string
	Kind    Kind
	// Error is the error a formula produced, such as "#REF!".
	Error string

	// Note, Validation and Hyperlink are the three things a values read
	// cannot show and a write can destroy without saying so.
	Note       string
	Validation string
	// Pivot says this cell anchors a pivot table. Only the anchor
	// carries one: the cells it draws are ordinary computed values, so
	// this marks where a pivot starts and not how far it reaches.
	Pivot     bool
	Hyperlink string

	// Format is what the cell was explicitly given, and nil unless the
	// read asked for it: a write's pre-read does not, and
	// read_formatting does.
	//
	// The cell's own format, not the one that applies to it. The API
	// offers both and this asks for one, because both questions this
	// server answers are about what somebody set: clear_format removes
	// exactly this and nothing else, and a formatting read that
	// described the sheet's defaults would report every cell of every
	// spreadsheet as formatted. The effective format was carried here
	// for a while with nothing reading it, which doubled the response of
	// the largest read this server does.
	Format *gsheets.CellFormat
}

// Empty reports whether the cell holds no value.
//
// A note or a validation rule does not make it non-empty. That is the
// opposite of what this said before: the claim was that a write over
// such a cell loses the note, and a live probe says it does not —
// values.update keeps the note and the rule, the same way values.clear
// documents that it does. Refusing a write into a blank cell for a loss
// that never happens is a guard getting in the way for nothing.
func (c Cell) Empty() bool { return c.Kind == KindEmpty }

// HasFormula reports whether a formula is what the cell holds.
//
// Kind is not the test. A formula that evaluated to an error is
// KindError, so a check on the kind misses it — and a write over
// `=IMPORTRANGE(...)` showing #REF! would then need only `overwrite`,
// which is the exact loss `overwrite_formulas` exists to prevent. Found
// by reading a live transcript where a sheet full of formulas reported
// none.
func (c Cell) HasFormula() bool { return c.Formula != "" }

// Protection is a protected range as it applies to this rectangle.
type Protection struct {
	Rect        a1.Rect
	Description string
	WarningOnly bool
	CanEdit     bool
}

// Grid is a rectangle of cells with its address kept.
type Grid struct {
	Sheet   string
	SheetID int
	// Rect is the rectangle asked for, and the size Cells has.
	Rect a1.Rect
	// Cells is Rect.Rows() by Rect.Cols(), padded past where the data
	// ended so every address in the grid is the address it looks like.
	Cells [][]Cell
	// DataRows is how many rows from the top actually held anything, so
	// a read can say how far the data reached rather than implying the
	// whole rectangle is populated.
	DataRows  int
	Merges    []a1.Rect
	Protected []Protection
}

// Formatted selects which of the API's renderings Display carries.
type Formatted bool

// Display options.
const (
	AsFormatted Formatted = true
	AsRaw       Formatted = false
)

// Build turns one sheet's grid data into a Grid over rect.
//
// rect is what the caller asked for and what the result is padded to.
// The API omits trailing empty rows and trailing empty cells within a
// row, so a response is routinely smaller than the range it answers.
func Build(sheet string, sheetID int, rect a1.Rect, data *gsheets.GridData, formatted Formatted) *Grid {
	g := &Grid{Sheet: sheet, SheetID: sheetID, Rect: rect}
	rows, cols := rect.Rows(), rect.Cols()
	// One backing array re-sliced per row. A 50 000-cell read otherwise
	// allocates a slice per row and writes Kind into every cell; this
	// makes it two allocations and a memmove, and leaves the garbage
	// collector one object to track instead of two thousand.
	flat := make([]Cell, rows*cols)
	empty := make([]Cell, cols)
	for j := range empty {
		empty[j].Kind = KindEmpty
	}
	g.Cells = make([][]Cell, rows)
	for i := range g.Cells {
		g.Cells[i] = flat[i*cols : (i+1)*cols : (i+1)*cols]
		copy(g.Cells[i], empty)
	}
	if data == nil {
		return g
	}
	// The response says where its own rectangle starts, which need not
	// be where the request asked it to. The conversion is a1's.
	rowOffset, colOffset := rect.OffsetOf(data.StartRow, data.StartColumn)
	for i, row := range data.RowData {
		r := i + rowOffset
		if r < 0 || r >= rows || row == nil {
			continue
		}
		for j, cd := range row.Values {
			c := j + colOffset
			if c < 0 || c >= cols {
				continue
			}
			g.Cells[r][c] = cell(cd, formatted)
			if !g.Cells[r][c].Empty() && r+1 > g.DataRows {
				g.DataRows = r + 1
			}
		}
	}
	return g
}

func cell(cd *gsheets.CellData, formatted Formatted) Cell {
	if cd == nil {
		return Cell{Kind: KindEmpty}
	}
	c := Cell{
		Kind:      KindEmpty,
		Note:      cd.Note,
		Hyperlink: cd.Hyperlink,
		Format:    cd.UserEnteredFormat,
	}
	if cd.DataValidation != nil {
		c.Validation = describeValidation(cd.DataValidation)
	}
	c.Pivot = len(cd.PivotTable) > 0
	if v := cd.UserEnteredValue; v != nil && v.FormulaValue != nil {
		c.Formula = *v.FormulaValue
		c.Kind = KindFormula
	}
	eff := cd.EffectiveValue
	switch {
	case eff == nil:
	case eff.ErrorValue != nil:
		c.Kind = KindError
		c.Error = eff.ErrorValue.Display()
		c.Display = c.Error
	case eff.StringValue != nil:
		if c.Kind != KindFormula {
			c.Kind = KindText
		}
		c.Display = *eff.StringValue
	case eff.NumberValue != nil:
		if c.Kind != KindFormula {
			c.Kind = KindNumber
		}
		c.Display = strconv.FormatFloat(*eff.NumberValue, 'f', -1, 64)
	case eff.BoolValue != nil:
		if c.Kind != KindFormula {
			c.Kind = KindBool
		}
		if *eff.BoolValue {
			c.Display = "TRUE"
		} else {
			c.Display = "FALSE"
		}
	}
	c.Raw = c.Display
	if formatted == AsFormatted && cd.FormattedValue != "" {
		c.Display = cd.FormattedValue
	}
	return c
}

// describeValidation summarises a rule in a few words. The whole rule is
// read_formatting's business; here it only has to say the cell has one,
// so a write can be told it is about to remove it.
func describeValidation(r *gsheets.DataValidationRule) string {
	if r.Condition == nil {
		return "validated"
	}
	kind := strings.ToLower(strings.ReplaceAll(r.Condition.Type, "_", " "))
	if len(r.Condition.Values) == 0 {
		return kind
	}
	vals := make([]string, 0, len(r.Condition.Values))
	for _, v := range r.Condition.Values {
		if v.UserEnteredValue != "" {
			vals = append(vals, v.UserEnteredValue)
		} else if v.RelativeDate != "" {
			vals = append(vals, strings.ToLower(v.RelativeDate))
		}
	}
	if len(vals) == 0 {
		return kind
	}
	return kind + ": " + strings.Join(vals, ", ")
}

// At returns the cell at a one-based sheet address, and whether it is
// inside this grid.
func (g *Grid) At(row, col int) (Cell, bool) {
	i, j := row-g.Rect.FirstRow, col-g.Rect.FirstCol
	if i < 0 || j < 0 || i >= len(g.Cells) || j >= len(g.Cells[i]) {
		return Cell{}, false
	}
	return g.Cells[i][j], true
}

// Address is the A1 address of a cell, given its position in the grid.
func (g *Grid) Address(i, j int) string {
	name, err := a1.CellName(g.Rect.FirstCol+j, g.Rect.FirstRow+i)
	if err != nil {
		return ""
	}
	return name
}

// Counts summarises what a rectangle holds, which is what a guard
// refusal and a dry run both report.
type Counts struct {
	NonEmpty   int
	Formulas   int
	Errors     int
	Notes      int
	Validation int
	Pivots     int
}

// Count walks the grid.
func (g *Grid) Count() Counts {
	var c Counts
	for _, row := range g.Cells {
		for _, cell := range row {
			if !cell.Empty() {
				c.NonEmpty++
			}
			if cell.HasFormula() {
				c.Formulas++
			}
			if cell.Kind == KindError {
				c.Errors++
			}
			if cell.Note != "" {
				c.Notes++
			}
			if cell.Pivot {
				c.Pivots++
			}
			if cell.Validation != "" {
				c.Validation++
			}
		}
	}
	return c
}

// FromValues builds a Grid out of a values response rather than out of
// cell data.
//
// A write asks for its stored values back in the same response (§4.4),
// rendered as formulas. That is enough to describe the region it
// produced and to hash a new checkpoint over it, without a second read.
//
// The reconstruction has to agree with what Build would make of the same
// cells, or two checkpoints over one rectangle would differ by which
// call produced them. It does: a formula hashes as its formula either
// way, a number as the same digits, a boolean as TRUE or FALSE, and an
// omitted trailing cell as empty. The one thing a values response cannot
// say is whether a string beginning with "=" is a formula or the text of
// one, and the input option decides that — under RAW nothing is
// evaluated, verified live.
func FromValues(sheet string, sheetID int, rect a1.Rect, values [][]any, formulasEvaluated bool) *Grid {
	g := &Grid{Sheet: sheet, SheetID: sheetID, Rect: rect}
	rows, cols := rect.Rows(), rect.Cols()
	flat := make([]Cell, rows*cols)
	g.Cells = make([][]Cell, rows)
	for i := range g.Cells {
		g.Cells[i] = flat[i*cols : (i+1)*cols : (i+1)*cols]
		for j := range g.Cells[i] {
			g.Cells[i][j].Kind = KindEmpty
		}
	}
	for i, row := range values {
		if i >= rows {
			break
		}
		for j, v := range row {
			if j >= cols {
				break
			}
			g.Cells[i][j] = valueCell(v, formulasEvaluated)
			if !g.Cells[i][j].Empty() && i+1 > g.DataRows {
				g.DataRows = i + 1
			}
		}
	}
	return g
}

func valueCell(v any, formulasEvaluated bool) Cell {
	switch t := v.(type) {
	case string:
		switch {
		case t == "":
			return Cell{Kind: KindEmpty}
		case formulasEvaluated && strings.HasPrefix(t, "="):
			return Cell{Kind: KindFormula, Formula: t, Display: t, Raw: t}
		default:
			return Cell{Kind: KindText, Display: t, Raw: t}
		}
	case bool:
		if t {
			return Cell{Kind: KindBool, Display: "TRUE", Raw: "TRUE"}
		}
		return Cell{Kind: KindBool, Display: "FALSE", Raw: "FALSE"}
	case float64:
		n := strconv.FormatFloat(t, 'f', -1, 64)
		return Cell{Kind: KindNumber, Display: n, Raw: n}
	case int:
		n := strconv.Itoa(t)
		return Cell{Kind: KindNumber, Display: n, Raw: n}
	}
	return Cell{Kind: KindEmpty}
}
