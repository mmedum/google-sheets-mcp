package plan_test

import (
	"fmt"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

// The guard reads every cell of the target before a write, so its cost
// is on the path of every write and grows with the rectangle. §11 has no
// number for it because none was measured; these are the numbers.
//
// The shape that matters is the one where the guard has most to say: a
// rectangle full of formulas, where every cell lands in two of the
// report's sets. A guard that was fast only on empty cells would be fast
// only where nothing is at stake.

func guardGrid(rows, cols int, formulas bool) *grid.Grid {
	nums := sheetstest.Numbers(9, rows*cols)
	data := &gsheets.GridData{RowData: make([]*gsheets.RowData, rows)}
	for r := range rows {
		values := make([]*gsheets.CellData, cols)
		for c := range cols {
			v := nums[r*cols+c]
			if formulas {
				values[c] = sheetstest.Formula(fmt.Sprintf("=B%d*2", r+1), v, "0")
				continue
			}
			values[c] = sheetstest.Num(v, "0")
		}
		data.RowData[r] = &gsheets.RowData{Values: values}
	}
	rect := a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: rows, LastCol: cols}
	return grid.Build("Bractal", 0, rect, data, grid.AsRaw)
}

func BenchmarkGuard(b *testing.B) {
	for _, tc := range []struct {
		name     string
		rows     int
		formulas bool
	}{
		{"500x10 values", 500, false},
		{"500x10 formulas", 500, true},
		{"5000x10 formulas", 5000, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			g := guardGrid(tc.rows, 10, tc.formulas)
			values := make([][]any, tc.rows)
			for i := range values {
				values[i] = make([]any, 10)
				for j := range values[i] {
					values[i][j] = "Quorbin"
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				r := plan.Check(g, values, true)
				if r.NonEmpty.Total != tc.rows*10 {
					b.Fatalf("the guard saw %d non-empty cells", r.NonEmpty.Total)
				}
			}
		})
	}
}

// CheckDestination is the cheap half — merges and protections only — and
// is what every formatting call pays. It must not cost what the full
// guard costs, or "make this bold" pays for a read it never needed.
func BenchmarkCheckDestination(b *testing.B) {
	g := guardGrid(5000, 10, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = plan.CheckDestination(g)
	}
}
