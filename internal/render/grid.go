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
	// ShortenedAt names the first few of them, so a caller who wants a
	// value whole knows which cell to read again rather than guessing a
	// narrower range. Capped: the count is the total, this is a sample.
	ShortenedAt []string
}

// minEmptyRun is how many consecutive empty rows are folded into one
// line. Two rows cost less than the sentence describing them.
const minEmptyRun = 3

// maxShortenedAt caps what ShortenedAt collects. A read at the cell
// budget could otherwise clip fifty thousand cells and name them all.
const maxShortenedAt = 5

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

	m := measure(g, o, &res)
	var b strings.Builder
	b.WriteString(m.header())
	m.draw(&b, g, o, &res)

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

// sheet is the measured grid: what each cell prints as, and how wide its
// column has to be. Two passes, measure then draw, because a column is
// as wide as its widest cell and that is not known until every row has
// been looked at.
type sheet struct {
	letters  []string
	widths   []int
	body     [][]string
	formulas [][]string
	gutter   int
}

// measure fills the matrices and the column widths, and counts what it
// had to clip on the way.
func measure(g *grid.Grid, o GridOptions, res *GridResult) sheet {
	rows, cols := len(g.Cells), g.Rect.Cols()
	m := sheet{
		letters: make([]string, cols),
		widths:  make([]int, cols),
		body:    make([][]string, rows),
		gutter:  len(fmt.Sprint(g.Rect.FirstRow + rows - 1)),
	}
	for j := range cols {
		m.letters[j], _ = a1.ColumnName(g.Rect.FirstCol + j)
		m.widths[j] = utf8.RuneCountInString(m.letters[j])
	}
	// The formula line exists only for `both`. Building it for every
	// read would allocate a second matrix the size of the budget and
	// clip fifty thousand empty strings to produce nothing.
	if o.Show == ShowBoth {
		m.formulas = make([][]string, rows)
	}
	for i := range rows {
		m.body[i] = make([]string, cols)
		if m.formulas != nil {
			m.formulas[i] = make([]string, cols)
		}
		for j := range cols {
			m.measureCell(g, o, res, i, j)
		}
	}
	return m
}

func (m sheet) measureCell(g *grid.Grid, o GridOptions, res *GridResult, i, j int) {
	c := g.Cells[i][j]
	value, cut := clip(c.Display)
	if o.Show == ShowFormulas && c.Formula != "" {
		value, cut = clip(c.Formula)
	}
	if cut {
		res.Shortened++
		noteShortened(res, g, i, j)
	}
	m.body[i][j] = value
	m.widths[j] = max(m.widths[j], utf8.RuneCountInString(value))
	if m.formulas == nil || c.Formula == "" {
		return
	}
	formula, cutF := clip(c.Formula)
	if cutF {
		res.Shortened++
		noteShortened(res, g, i, j)
	}
	m.formulas[i][j] = formula
	m.widths[j] = max(m.widths[j], utf8.RuneCountInString(formula))
}

// header sits over the columns, offset by the gutter and its separator.
func (m sheet) header() string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", m.gutter+3))
	for j := range m.letters {
		if j > 0 {
			b.WriteString("   ")
		}
		b.WriteString(pad(m.letters[j], m.widths[j]))
	}
	return strings.TrimRight(b.String(), " ") + "\n"
}

// draw writes the rows, folding runs of empty ones, and stops at a row
// boundary when the character budget runs out.
func (m sheet) draw(b *strings.Builder, g *grid.Grid, o GridOptions, res *GridResult) {
	res.LastRow = g.Rect.FirstRow - 1
	blankGutter := strings.Repeat(" ", m.gutter)
	for i := 0; i < len(m.body); {
		// An empty cell is still padded to its column's width, so a run
		// of empty rows spends the character budget on nothing and
		// pulls ContinueFrom in on exactly the sparse sheets a wide
		// window is reasonable to ask for. One line says the same.
		if run := emptyRun(m.body, m.formulas, i); run >= minEmptyRun {
			line := fmt.Sprintf("… rows %d-%d empty\n", g.Rect.FirstRow+i, g.Rect.FirstRow+i+run-1)
			if m.cut(b, o, res, line, "", i) {
				return
			}
			b.WriteString(line)
			res.LastRow = g.Rect.FirstRow + i + run - 1
			i += run
			continue
		}
		line := renderRow(fmt.Sprintf("%*d", m.gutter, g.Rect.FirstRow+i), m.body[i], m.widths)
		extra := ""
		if m.formulas != nil && anyNonEmpty(m.formulas[i]) {
			extra = renderRow(blankGutter, m.formulas[i], m.widths)
		}
		if m.cut(b, o, res, line, extra, i) {
			return
		}
		b.WriteString(line)
		b.WriteString(extra)
		res.LastRow = g.Rect.FirstRow + i
		i++
	}
}

// cut says whether what comes next would break the character budget.
// The first row is always drawn: a read that returns nothing at all
// tells the caller less than one that overshoots by a row.
func (m sheet) cut(b *strings.Builder, o GridOptions, res *GridResult, line, extra string, i int) bool {
	if o.MaxChars <= 0 || i == 0 || b.Len()+len(line)+len(extra) <= o.MaxChars {
		return false
	}
	res.ContinueFrom = res.LastRow + 1
	return true
}

// emptyRun counts the rows from i that hold nothing at all, in the
// values and in the formulas both.
func emptyRun(body, formulas [][]string, i int) int {
	n := 0
	for ; i+n < len(body); n++ {
		if anyNonEmpty(body[i+n]) {
			break
		}
		if formulas != nil && anyNonEmpty(formulas[i+n]) {
			break
		}
	}
	return n
}

func noteShortened(res *GridResult, g *grid.Grid, i, j int) {
	if len(res.ShortenedAt) >= maxShortenedAt {
		return
	}
	addr := g.Address(i, j)
	// A `both` read clips the value and the formula of one cell
	// separately, and the caller reads the cell once either way.
	if len(res.ShortenedAt) > 0 && res.ShortenedAt[len(res.ShortenedAt)-1] == addr {
		return
	}
	res.ShortenedAt = append(res.ShortenedAt, addr)
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
		at := ""
		if len(res.ShortenedAt) > 0 {
			// The count is the total and the addresses are a sample, so
			// the ellipsis goes on what was left out rather than on the
			// cap: naming five of five is not a sample.
			more := ""
			if res.Shortened > len(res.ShortenedAt) {
				more = ", …"
			}
			at = fmt.Sprintf(" (%s%s)", strings.Join(res.ShortenedAt, ", "), more)
		}
		parts = append(parts, fmt.Sprintf("%d value(s) shortened to %d characters%s", res.Shortened, MaxCellWidth, at))
	}
	if res.ContinueFrom > 0 {
		parts = append(parts, fmt.Sprintf("cut at the character budget; continue at row %d", res.ContinueFrom))
	}
	return strings.Join(parts, "; ")
}
