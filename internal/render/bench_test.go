package render_test

import (
	"fmt"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// §11's two rendering targets: a 5 000-cell read rendered well under
// 20 ms, and a 10 000-row grid rendered with a flat memory profile.
//
// "Flat" is the claim worth measuring rather than asserting. It means
// the renderer's cost tracks the window it was given and not the sheet
// behind it — every read here is windowed before the request, so a
// number that grew with the sheet would mean a window that was not being
// applied.

// gridOf builds a grid of rows by cols from the seeded generator, the
// way a response would arrive.
func gridOf(rows, cols int) *grid.Grid {
	nums := sheetstest.Numbers(9, rows*cols)
	data := &gsheets.GridData{RowData: make([]*gsheets.RowData, rows)}
	for r := range rows {
		values := make([]*gsheets.CellData, cols)
		for c := range cols {
			v := nums[r*cols+c]
			// One text column, so the width measuring has something
			// other than numbers to do.
			if c == 0 {
				values[c] = sheetstest.Str(sheetstest.Vocabulary[r%len(sheetstest.Vocabulary)])
				continue
			}
			values[c] = sheetstest.Num(v, fmt.Sprintf("%.2f", v))
		}
		data.RowData[r] = &gsheets.RowData{Values: values}
	}
	rect := a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: rows, LastCol: cols}
	return grid.Build("Bractal", 0, rect, data, grid.AsRaw)
}

// BenchmarkGrid is the read that §11 puts a number on: 5 000 cells, the
// default budget, rendered as the addressed grid a model reads.
func BenchmarkGrid(b *testing.B) {
	for _, tc := range []struct{ rows, cols int }{{500, 10}, {1000, 10}, {10000, 10}} {
		b.Run(fmt.Sprintf("%dx%d", tc.rows, tc.cols), func(b *testing.B) {
			g := gridOf(tc.rows, tc.cols)
			// The character budget is what a real read applies, and
			// leaving it at the default would stop the 10 000-row case
			// after a few hundred rows and measure nothing.
			opts := render.GridOptions{MaxChars: 1 << 30, TotalRows: tc.rows}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if res := render.Grid(g, opts); res.Text == "" {
					b.Fatal("rendered nothing")
				}
			}
		})
	}
}

// The machine formats travel the same grid, and CSV is what the sheet
// resource hands back.
func BenchmarkRows(b *testing.B) {
	g := gridOf(10000, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rows := render.Rows(g, render.ShowValues)
		if len(rows) != 10000 {
			b.Fatalf("rendered %d rows", len(rows))
		}
	}
}

// Building the grid from a response is the step before either
// rendering, and the one that allocates the backing array.
func BenchmarkBuild(b *testing.B) {
	nums := sheetstest.Numbers(9, 100000)
	data := &gsheets.GridData{RowData: make([]*gsheets.RowData, 10000)}
	for r := range 10000 {
		values := make([]*gsheets.CellData, 10)
		for c := range 10 {
			values[c] = sheetstest.Num(nums[r*10+c], "0")
		}
		data.RowData[r] = &gsheets.RowData{Values: values}
	}
	rect := a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 10000, LastCol: 10}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if g := grid.Build("Bractal", 0, rect, data, grid.AsRaw); g.DataRows != 10000 {
			b.Fatalf("built %d rows", g.DataRows)
		}
	}
}
