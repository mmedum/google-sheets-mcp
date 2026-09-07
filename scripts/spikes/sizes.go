//go:build live

package main

import (
	"context"
	"encoding/json"
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
	//
	// The value's length, not the body's. The first run of this printed
	// len(body) and called it "characters back", which is the string
	// plus the JSON around it — a number that looks like an answer and
	// is not one.
	status, body := getValues(ctx, q+"!A1")
	line("    %-38s -> HTTP %d  %d characters stored", "reading the accepted cell", status, storedLen(body))

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
	line("  And the column ceiling on its own.")
	line("  The sheet is shrunk to one row first: at 1 000 rows, 18 279 columns is")
	line("  18.3 million cells, so the cell ceiling answers before the column one and")
	line("  the first run of this probe could not tell them apart.")
	if !resize(ctx, sheetID, "gridProperties.rowCount", map[string]any{"rowCount": 1}) {
		line("    could not shrink the sheet; the two ceilings stay confounded")
		return
	}
	for _, cols := range []int{18278, 18279} {
		status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
			map[string]any{"requests": []any{
				map[string]any{"updateSheetProperties": map[string]any{
					"properties": map[string]any{
						"sheetId":        sheetID,
						"gridProperties": map[string]any{"columnCount": cols},
					},
					"fields": "gridProperties.columnCount",
				}},
			}})
		line("    %-38s -> HTTP %d  %s", fmt.Sprintf("%d columns, on a one-row sheet", cols), status, first120(body))
	}
}

// resize applies one grid-property change and says whether it took.
func resize(ctx context.Context, sheetID int, mask string, grid map[string]any) bool {
	status, _ := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{
			map[string]any{"updateSheetProperties": map[string]any{
				"properties": map[string]any{"sheetId": sheetID, "gridProperties": grid},
				"fields":     mask,
			}},
		}})
	return status == 200
}

// storedLen is how long the string in the first cell of a values reply
// is, rather than how long the reply is.
func storedLen(body string) int {
	var vr struct {
		Values [][]string `json:"values"`
	}
	if err := json.Unmarshal([]byte(body), &vr); err != nil || len(vr.Values) == 0 || len(vr.Values[0]) == 0 {
		return -1
	}
	return len(vr.Values[0][0])
}

// spikeJ answers a question the live driver raised rather than the plan:
// a conditional format rule over a range vanished across an addTable and
// a deleteTable on the same range, and the driver could only see that it
// was gone by the end.
//
// Which of the two took it decides what manage_range has to say. Counted
// after every step, because "it is gone now" is not an answer to "what
// removed it".
func spikeJ(ctx context.Context) {
	sec("Spike J: what a table does to the rules over its range")
	const sheet = "SpikeTables"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:B3", [][]any{
		{"Plimth", "Nardle"}, {"Quorbin", 1}, {"Vandel", 2},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	rect := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}

	step := func(what string, req map[string]any) {
		status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
			map[string]any{"requests": []any{req}})
		line("    %-34s -> HTTP %d  %s", what, status, first120(body))
		line("      rules on the sheet now: %d", ruleCount(ctx, sheetID))
	}

	line("")
	line("  rules on the sheet at the start: %d", ruleCount(ctx, sheetID))
	step("add a conditional format rule", map[string]any{"addConditionalFormatRule": map[string]any{
		"index": 0,
		"rule": map[string]any{
			"ranges": []any{rect.GridRange(sheetID)},
			"booleanRule": map[string]any{
				"condition": map[string]any{"type": "NOT_BLANK"},
				"format":    map[string]any{"backgroundColorStyle": map[string]any{"rgbColor": map[string]any{"red": 0.85, "green": 0.92, "blue": 0.83}}},
			},
		},
	}})
	step("add a table over the same range", map[string]any{"addTable": map[string]any{
		"table": map[string]any{"name": "SpikeRuleTable", "range": rect.GridRange(sheetID)},
	}})
	// The id the add returned is needed to delete it, so the count after
	// the add is read first and the table is found by listing.
	id := tableID(ctx, sheetID)
	if id == "" {
		line("    could not find the table that was just added; the delete half is unanswered")
		return
	}
	step("delete the table", map[string]any{"deleteTable": map[string]any{"tableId": id}})
}

// ruleCount reads how many conditional format rules a sheet carries.
func ruleCount(ctx context.Context, sheetID int) int {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields=sheets(properties(sheetId),conditionalFormats)", nil)
	if status != 200 {
		return -1
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			ConditionalFormats []json.RawMessage `json:"conditionalFormats"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return -1
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID == sheetID {
			return len(sh.ConditionalFormats)
		}
	}
	return -1
}

// tableID is the id of the one table on a sheet.
func tableID(ctx context.Context, sheetID int) string {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields=sheets(properties(sheetId),tables(tableId))", nil)
	if status != 200 {
		return ""
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			Tables []struct {
				TableID string `json:"tableId"`
			} `json:"tables"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return ""
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID == sheetID && len(sh.Tables) > 0 {
			return sh.Tables[0].TableID
		}
	}
	return ""
}
