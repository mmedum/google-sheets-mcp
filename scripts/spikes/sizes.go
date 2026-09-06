//go:build live

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// spikeG answers §15.G: what the platform does at its own edges.
//
// Three of them, and each decides a refusal this server writes rather
// than forwards. The character limit was answered early and for free
// during phase 1 — 60 004 characters is refused, and §18 carries the
// message — so what is left is the boundary itself: exactly 50 000,
// which the reference says is the most a cell takes, and one more.
func spikeG(ctx context.Context) {
	sec("Spike G: size behaviour")
	const sheet = "SpikeSizes"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)

	line("")
	line("  A cell at the character limit, and one past it:")
	for _, n := range []int{50000, 50001} {
		status, body := putMode(ctx, q+"!A1", [][]any{{strings.Repeat("q", n)}}, "RAW")
		line("    %-38s -> HTTP %d  %s", fmt.Sprintf("%d characters", n), status, first120(body))
	}
	// What came back, so the round trip is read rather than assumed: a
	// write that reported success and stored a truncated string would
	// look identical from the response alone.
	status, body := getValues(ctx, q+"!A1")
	line("    %-38s -> HTTP %d  %d characters back", "reading the accepted cell", status, len(body))

	line("")
	line("  Past the last column a spreadsheet has (ZZZ is 18 278):")
	for _, ref := range []string{"AAAA1", "ZZZ1"} {
		status, body := putMode(ctx, q+"!"+ref, [][]any{{"Quorbin"}}, "RAW")
		line("    %-38s -> HTTP %d  %s", ref, status, first120(body))
	}
	// A grid that is merely bigger than the sheet, rather than bigger
	// than a spreadsheet: this is the refusal write_values gives before
	// the request is built, and it is worth seeing Google's own.
	status, body = putMode(ctx, q+"!A2000", [][]any{{"Vandel"}}, "RAW")
	line("    %-38s -> HTTP %d  %s", "a row past the sheet's own extent", status, first120(body))

	line("")
	line("  Past the ten-million-cell ceiling:")
	for _, size := range []struct {
		what       string
		rows, cols int
	}{
		{"10 000 001 rows by 1 column", 10000001, 1},
		{"1 000 000 rows by 100 columns", 1000000, 100},
	} {
		status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
			map[string]any{"requests": []any{
				map[string]any{"updateSheetProperties": map[string]any{
					"properties": map[string]any{
						"sheetId": sheetID,
						"gridProperties": map[string]any{
							"rowCount": size.rows, "columnCount": size.cols,
						},
					},
					"fields": "gridProperties.rowCount,gridProperties.columnCount",
				}},
			}})
		line("    %-38s -> HTTP %d  %s", size.what, status, first120(body))
	}

	line("")
	line("  And the column ceiling as a grid size rather than as a range:")
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{
			map[string]any{"updateSheetProperties": map[string]any{
				"properties": map[string]any{
					"sheetId":        sheetID,
					"gridProperties": map[string]any{"columnCount": 18279},
				},
				"fields": "gridProperties.columnCount",
			}},
		}})
	line("    %-38s -> HTTP %d  %s", "18 279 columns, one past ZZZ", status, first120(body))
}
