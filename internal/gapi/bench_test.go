package gapi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Decoding a response is where a read's time actually goes.
//
// §11's target was written about rendering — "a 5 000-cell read rendered
// under 20 ms" — and rendering turns out to be the cheap half by an
// order of magnitude. This is the expensive half, and it is the one
// nothing here can make much faster: it is the shape of the wire.
// Measured so the target says what it means.
func BenchmarkDecodeGrid(b *testing.B) {
	for _, cells := range []int{5000, 50000} {
		b.Run(fmt.Sprintf("%dcells", cells), func(b *testing.B) {
			rows := cells / 10
			nums := sheetstest.Numbers(9, cells)
			data := &gsheets.GridData{RowData: make([]*gsheets.RowData, rows)}
			for r := range rows {
				values := make([]*gsheets.CellData, 10)
				for c := range 10 {
					v := nums[r*10+c]
					values[c] = sheetstest.Num(v, fmt.Sprintf("%.2f", v))
				}
				data.RowData[r] = &gsheets.RowData{Values: values}
			}
			body, err := json.Marshal(&gsheets.Spreadsheet{
				SpreadsheetID: "bench",
				Sheets:        []*gsheets.Sheet{{Data: []*gsheets.GridData{data}}},
			})
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var sp gsheets.Spreadsheet
				if err := json.Unmarshal(body, &sp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
