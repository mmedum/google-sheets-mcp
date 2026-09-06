// Package render turns this server's view of a spreadsheet into the text
// a model reads.
//
// Every read renders an addressed grid: column letters across the top,
// row numbers down the side. That is the whole point of the rendering
// rather than a decoration on it. The model writes back to what it just
// read by naming an address it can see, so there is nothing to remember
// between calls and no handle to go stale. It is also what the person
// sees on their screen, so the two of them are talking about the same
// thing.
package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
)

// Show selects what a read puts in each cell.
type Show string

// Show values.
const (
	ShowValues   Show = "values"
	ShowFormulas Show = "formulas"
	ShowBoth     Show = "both"
)

// MaxCellWidth caps one column's width. A single 40 000-character cell
// would otherwise decide the layout of the whole grid.
const MaxCellWidth = 50

// GridOptions tune a rendering.
type GridOptions struct {
	Show Show
	// MaxChars stops the rendering at a row boundary. Zero means no cut,
	// which is only for callers that have already bounded the input.
	MaxChars int
	// IncludeNotes, IncludeValidation and IncludeMerges add the
	// annotations a values read cannot show, listed under the grid so
	// they do not distort it.
	IncludeNotes      bool
	IncludeValidation bool
	IncludeMerges     bool
	// TotalRows is how far the sheet's allocated grid reaches, for the
	// footer's "of N".
	TotalRows int
}

// GridResult is a rendered grid and what the caller needs to continue.
type GridResult struct {
	Text string
	// ContinueFrom is the first row not shown, or 0 when the whole
	// rectangle was drawn. There is no separate "truncated" flag,
	// because it would be this field compared with zero and two fields
	// that cannot disagree still have to be checked against each other.
	ContinueFrom int
	// LastRow is the last row shown.
	LastRow int
	// Shortened counts cells whose text was capped at MaxCellWidth.
	Shortened int
}

// Grid renders an addressed grid.
func Grid(g *grid.Grid, o GridOptions) GridResult {
	if o.Show == "" {
		o.Show = ShowValues
	}
	rows, cols := len(g.Cells), g.Rect.Cols()
	res := GridResult{}
	if rows == 0 || cols == 0 {
		return GridResult{Text: fmt.Sprintf("%s is empty.\n", a1.Format(g.Sheet, g.Rect))}
	}

	// Two passes: measure, then draw. A column is as wide as its widest
	// cell, its letter, and nothing else.
	letters := make([]string, cols)
	widths := make([]int, cols)
	for j := range cols {
		letters[j], _ = a1.ColumnName(g.Rect.FirstCol + j)
		widths[j] = utf8.RuneCountInString(letters[j])
	}
	body := make([][]string, rows)
	// The formula line exists only for `both`. Building it for every
	// read would allocate a second matrix the size of the budget and
	// clip fifty thousand empty strings to produce nothing.
	var formulas [][]string
	if o.Show == ShowBoth {
		formulas = make([][]string, rows)
	}
	for i := range rows {
		body[i] = make([]string, cols)
		if formulas != nil {
			formulas[i] = make([]string, cols)
		}
		for j := range cols {
			c := g.Cells[i][j]
			value, cut := clip(c.Display)
			if o.Show == ShowFormulas && c.Formula != "" {
				value, cut = clip(c.Formula)
			}
			if cut {
				res.Shortened++
			}
			body[i][j] = value
			widths[j] = max(widths[j], utf8.RuneCountInString(value))
			if formulas == nil || c.Formula == "" {
				continue
			}
			formula, cutF := clip(c.Formula)
			if cutF {
				res.Shortened++
			}
			formulas[i][j] = formula
			widths[j] = max(widths[j], utf8.RuneCountInString(formula))
		}
	}

	gutter := len(fmt.Sprint(g.Rect.FirstRow + rows - 1))
	var b strings.Builder
	// The header line sits over the columns, offset by the gutter and
	// its separator.
	var header strings.Builder
	header.WriteString(strings.Repeat(" ", gutter+3))
	for j := range cols {
		if j > 0 {
			header.WriteString("   ")
		}
		header.WriteString(pad(letters[j], widths[j]))
	}
	b.WriteString(strings.TrimRight(header.String(), " ") + "\n")

	res.LastRow = g.Rect.FirstRow - 1
	blankGutter := strings.Repeat(" ", gutter)
	for i := range rows {
		line := renderRow(fmt.Sprintf("%*d", gutter, g.Rect.FirstRow+i), body[i], widths)
		extra := ""
		if formulas != nil && anyNonEmpty(formulas[i]) {
			extra = renderRow(blankGutter, formulas[i], widths)
		}
		if o.MaxChars > 0 && b.Len()+len(line)+len(extra) > o.MaxChars && i > 0 {
			res.ContinueFrom = g.Rect.FirstRow + i
			break
		}
		b.WriteString(line)
		b.WriteString(extra)
		res.LastRow = g.Rect.FirstRow + i
	}

	// Only the rows that were drawn. Listing a note at A417 under a grid
	// that stops at row 3 points at something the reader cannot see, and
	// the continuation read would list it a second time.
	b.WriteString(annotations(g, o, res.LastRow))
	res.Text = b.String()
	return res
}

func renderRow(gutter string, cells []string, widths []int) string {
	var b strings.Builder
	b.WriteString(gutter)
	for j, c := range cells {
		b.WriteString(" | ")
		b.WriteString(pad(c, widths[j]))
	}
	// Trailing padding on the last column is noise in a diff and in a
	// terminal both.
	return strings.TrimRight(b.String(), " ") + "\n"
}

func anyNonEmpty(xs []string) bool {
	for _, x := range xs {
		if x != "" {
			return true
		}
	}
	return false
}

func clip(s string) (string, bool) {
	// A newline inside a cell would break the grid's alignment, and the
	// alignment is what makes the addresses trustworthy.
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "⏎"), "\n", "⏎")
	if utf8.RuneCountInString(s) <= MaxCellWidth {
		return s, false
	}
	r := []rune(s)
	return string(r[:MaxCellWidth-1]) + "…", true
}

// spaces is sliced for padding, so drawing a grid does not call
// strings.Repeat once per cell.
const spaces = "                                                                "

func pad(s string, w int) string {
	n := w - utf8.RuneCountInString(s)
	switch {
	case n <= 0:
		return s
	case n <= len(spaces):
		return s + spaces[:n]
	}
	return s + strings.Repeat(" ", n)
}

// annotations list what a values read cannot show, under the grid rather
// than inside it.
func annotations(g *grid.Grid, o GridOptions, lastRow int) string {
	shown := lastRow - g.Rect.FirstRow + 1
	if shown < 0 {
		shown = 0
	}
	var b strings.Builder
	if o.IncludeNotes {
		writeCellList(&b, g, "notes", shown, func(c grid.Cell) string { return c.Note })
	}
	if o.IncludeValidation {
		writeCellList(&b, g, "validation", shown, func(c grid.Cell) string { return c.Validation })
	}
	if o.IncludeMerges && len(g.Merges) > 0 {
		b.WriteString("merges: ")
		parts := make([]string, 0, len(g.Merges))
		for _, m := range g.Merges {
			parts = append(parts, a1.FormatRect(m))
		}
		b.WriteString(strings.Join(parts, ", ") + "\n")
	}
	for _, p := range g.Protected {
		desc := p.Description
		if desc == "" {
			desc = "protected"
		}
		fmt.Fprintf(&b, "protected: %s (%s; %s)\n", a1.FormatRect(p.Rect), desc, ProtectionState(p.CanEdit, p.WarningOnly))
	}
	return b.String()
}

func writeCellList(b *strings.Builder, g *grid.Grid, label string, shown int, get func(grid.Cell) string) {
	var parts []string
	for i, row := range g.Cells[:min(shown, len(g.Cells))] {
		for j, c := range row {
			if v := get(c); v != "" {
				short, _ := clip(v)
				parts = append(parts, g.Address(i, j)+" "+short)
			}
		}
	}
	if len(parts) == 0 {
		return
	}
	b.WriteString(label + ": " + strings.Join(parts, "; ") + "\n")
}

// Footer is the line under a read: what was shown, out of what, and how
// to continue.
func Footer(g *grid.Grid, res GridResult, o GridOptions) string {
	var parts []string
	shown := fmt.Sprintf("rows %d-%d", g.Rect.FirstRow, res.LastRow)
	if o.TotalRows > 0 {
		shown += fmt.Sprintf(" of %d", o.TotalRows)
	}
	parts = append(parts, shown)
	parts = append(parts, fmt.Sprintf("columns %s", a1.FormatRect(a1.Rect{
		FirstCol: g.Rect.FirstCol, LastCol: g.Rect.LastCol,
	})))
	switch o.Show {
	case ShowFormulas:
		parts = append(parts, "formulas instead of values")
	case ShowBoth:
		parts = append(parts, "formulas under values")
	}
	if g.DataRows > 0 && g.Rect.FirstRow+g.DataRows-1 < res.LastRow {
		parts = append(parts, fmt.Sprintf("data ends at row %d", g.Rect.FirstRow+g.DataRows-1))
	}
	if res.Shortened > 0 {
		parts = append(parts, fmt.Sprintf("%d value(s) shortened to %d characters", res.Shortened, MaxCellWidth))
	}
	if res.ContinueFrom > 0 {
		parts = append(parts, fmt.Sprintf("cut at the character budget; continue at row %d", res.ContinueFrom))
	}
	return strings.Join(parts, "; ")
}
