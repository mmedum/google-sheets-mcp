package render

import (
	"bytes"
	"encoding/csv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/grid"
)

// Rows flattens a grid into strings, one slice per row, for the machine
// formats. The addresses are not in the cells, so every caller of this
// gets the range and the first row and column beside it.
func Rows(g *grid.Grid, show Show) [][]string {
	out := make([][]string, len(g.Cells))
	for i, row := range g.Cells {
		out[i] = make([]string, len(row))
		for j, c := range row {
			// One field cannot hold two answers, so `both` gives the
			// value: it is the one a machine format is usually after.
			if show == ShowFormulas && c.Formula != "" {
				out[i][j] = c.Formula
				continue
			}
			out[i][j] = c.Display
		}
	}
	return out
}

// SeparatedWithin renders rows the same way, stopping before a
// character budget rather than after it, and says how many it wrote.
//
// Whole rows, always: cutting the string instead would end mid-field,
// and half a quoted value parses as a different value rather than as an
// error. Here rather than at the caller because the grid renderer's own
// cut is here, and a budget applied above this package is a budget the
// goldens cannot cover.
func SeparatedWithin(rows [][]string, comma rune, maxChars int) (string, int) {
	// One writer over one buffer, and the row that crosses the budget is
	// truncated away rather than searched for. Writing each row through
	// a writer of its own — which is what this did first — allocates a
	// csv.Writer and its 4 KB buffer per row: 3 019 allocations and
	// 4.5 MB on a thousand rows, against 15 and 340 KB here.
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.Comma = comma
	for i, r := range rows {
		before := b.Len()
		_ = w.Write(r)
		// Flushed every row because the length is the decision, and a
		// buffered writer's length is the previous row's.
		w.Flush()
		if b.Len() <= maxChars {
			continue
		}
		if i > 0 {
			b.Truncate(before)
			return b.String(), i
		}
		// The first row alone is over budget. It is kept, because
		// returning nothing describes nothing — and the caller is left
		// to notice the length, which is why SheetCSV checks that rather
		// than only the row count. A single row can reach 1.3 MB: 26
		// cells at the 50 000-character cell limit.
		return b.String(), 1
	}
	w.Flush()
	return b.String(), len(rows)
}

// Separated renders rows as CSV or TSV. Quoting is the encoding
// library's, so a value containing the separator survives.
func Separated(rows [][]string, comma rune) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = comma
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	return b.String()
}
