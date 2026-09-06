package render

import (
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
