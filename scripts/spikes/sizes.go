//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v4/internal/a1"
)

// askSizeLimits asks what a write past the size limits answers: what
// the platform does at its own edges.
//
// Three of them, and each decides a refusal this server writes rather
// than forwards. Google refuses 60 004 characters in a cell, so what is
// left is the boundary itself: exactly 50 000, which the reference says
// is the most a cell takes, and one more.
func askSizeLimits(ctx context.Context) {
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
	// The value's length, not the body's: len(body) is the string plus
	// the JSON around it, a number that looks like an answer and is not
	// one.
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
	line("  18.3 million cells, so the cell ceiling would answer before the column one")
	line("  and the two could not be told apart.")
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

// askTableRules asks what a table does to the rules over its range. A
// conditional format rule over a range is gone after an addTable and a
// deleteTable on the same range, and the end state alone cannot say
// which of the two took it.
//
// Which of the two took it decides what manage_range has to say. Counted
// after every step, because "it is gone now" is not an answer to "what
// removed it".
func askTableRules(ctx context.Context) {
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

// askTextLimitUnit asks in what unit Google counts the two text limits
// this server checks before it writes: a cell's 50 000 and a comment's
// 2048. Each is written at the limit and one past it in three alphabets,
// x (one byte, one UTF-16 unit), é (two bytes, one unit) and an emoji
// (four bytes, two units), and the limits that hold say which unit it
// is.
//
// A cell counts UTF-16 units, under RAW and USER_ENTERED alike: 50 000 é
// (100 000 bytes) and 25 000 emoji are taken whole, and 50 001 é and
// 25 001 emoji refused, "Your input contains more than the maximum of
// 50000 characters in a single cell". A comment counts bytes: 2048 x,
// 1024 é and 512 emoji are taken, and one more of each refused,
// "content must not exceed 2048 UTF-8 code units". Aside: on a cell
// holding a 100 000-byte value, every comment at the limit fails with
// "An error occurred while saving comments"; why is not known.
func askTextLimitUnit(ctx context.Context) {
	const sheet = "SpikeUnits"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	runs := []struct {
		unit string
		cell int
		post int
	}{{"x", 50000, 2048}, {"é", 50000, 1024}, {"😀", 25000, 512}}

	line("")
	line("  A cell at 50 000 x, 50 000 é and 25 000 emoji (50 000 UTF-16 units), and one more of each:")
	for _, r := range runs {
		for _, n := range []int{r.cell, r.cell + 1} {
			text := strings.Repeat(r.unit, n)
			status, body := putMode(ctx, q+"!A1", [][]any{{text}}, "RAW")
			what := fmt.Sprintf("%d x %q (%d bytes)", n, r.unit, len(text))
			if status != http.StatusOK {
				line("    %-38s -> HTTP %d  %s", what, status, first120(body))
				continue
			}
			_, back := getValues(ctx, q+"!A1")
			line("    %-38s -> HTTP %d, stored whole: %v", what, status, storedLen(back) == len(text))
		}
	}

	// The same with input=typed, which Google parses as a person typing:
	// the unit could differ, and the guard checks both options alike.
	line("")
	line("  The same cells with USER_ENTERED:")
	for _, r := range runs[1:] {
		for _, n := range []int{r.cell, r.cell + 1} {
			text := strings.Repeat(r.unit, n)
			status, body := putMode(ctx, q+"!A2", [][]any{{text}}, "USER_ENTERED")
			what := fmt.Sprintf("%d x %q (%d bytes)", n, r.unit, len(text))
			if status != http.StatusOK {
				line("    %-38s -> HTTP %d  %s", what, status, first120(body))
				continue
			}
			_, back := getValues(ctx, q+"!A2")
			line("    %-38s -> HTTP %d, stored whole: %v", what, status, storedLen(back) == len(text))
		}
	}

	// The comments go on an empty cell, B5, with a short one first: on A1
	// beside a 100 000-byte value, every one at the limit fails with "An
	// error occurred while saving comments", which says nothing about the
	// limit.
	line("")
	line("  A comment on an empty cell: a short one, then one under, at and past 2048 bytes in each alphabet:")
	insert(ctx, "a short comment", sheetID, 4, 1, "control", "")
	for _, r := range runs {
		for _, n := range []int{r.post - 1, r.post, r.post + 1} {
			text := strings.Repeat(r.unit, n)
			insert(ctx, fmt.Sprintf("%d x %q (%d bytes)", n, r.unit, len(text)), sheetID, 4, 1, text, "")
		}
	}
}
