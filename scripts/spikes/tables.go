//go:build live

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
)

// spikeT answers what the reference leaves open about a table's typed
// columns, which manage_range writes with column_types.
//
// The tables guide gives one addTable example, which types two of five
// columns and names each, and says a dropdown "must" carry a ONE_OF_LIST
// rule. It says nothing about updateTable's columnProperties. manage_range
// is built on beliefs about both, and the fake refuses what they say
// Google refuses. Each is asked here.
//
// Q1. Is a sparse columnProperties taken on add — columns 1 and 3 of
// four, no names — and what reads back for the columns left out? Do the
// header cells survive?
// Q2. Is a dropdown with no rule refused, and a rule on a number column?
// Q3. Does an update with fields=columnProperties replace the whole
// array? It sends one column, with no name; what do the other three read
// back as, and do the header cells survive a name left out?
// Q4. Does a columnName sent on update rewrite the header cell?
// Q5. What does a type change do to the cells: a number format set
// before, a per-cell validation set before, and a boolean column over a
// text value and an empty cell, which the guide says fill with FALSE.
// Q6. Is the array taken back exactly as it read, names included? This
// is what manage_range sends on update.
// Q7. Can a table's header cell hold a formula, and what does an update
// sending the names as read do to it, and to a header in rich text?
// manage_range refuses the update over a formula header; this says
// whether that refusal can ever fire, and whether rich text needs one.
func spikeT(ctx context.Context) {
	sec("Spike T: typed table columns, and what an update does to them")
	const sheet = "SpikeTables"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	block := a1.QuoteSheet(sheet) + "!A1:D4"
	if err := put(ctx, block, [][]any{
		{"Item", "Amount", "Flag", "Status"},
		{"Quorbin", 1.5, "maybe", "x"},
		{"Skerry", 2, "", "y"},
		{"Nardle", 3, "TRUE", ""},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	tableRange := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 4}.GridRange(sheetID)
	// Set before any table exists, so Q5 can see what a type change
	// does to each: a number format on B and a per-cell list on D.
	status, body := batchOne(ctx, map[string]any{"repeatCell": map[string]any{
		"range": a1.Rect{FirstCol: 2, FirstRow: 2, LastCol: 2, LastRow: 4}.GridRange(sheetID),
		"cell": map[string]any{"userEnteredFormat": map[string]any{
			"numberFormat": map[string]any{"type": "NUMBER", "pattern": "0.000"},
		}},
		"fields": "userEnteredFormat.numberFormat",
	}})
	line("    %-52s -> HTTP %d  %s", "number format 0.000 on B2:B4", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"setDataValidation": map[string]any{
		"range": a1.Rect{FirstCol: 4, FirstRow: 2, LastCol: 4, LastRow: 4}.GridRange(sheetID),
		"rule":  map[string]any{"condition": oneOf("x", "y", "z"), "strict": true, "showCustomUi": true},
	}})
	line("    %-52s -> HTTP %d  %s", "a per-cell list x, y, z on D2:D4", status, first120(body))

	add := func(what string, columns []any) string {
		status, body := batchOne(ctx, map[string]any{"addTable": map[string]any{"table": map[string]any{
			"name": "SpikeTyped", "range": tableRange, "columnProperties": columns,
		}}})
		line("    %-52s -> HTTP %d  %s", what, status, first120(body))
		if status != 200 {
			return ""
		}
		var reply struct {
			Replies []struct {
				AddTable struct {
					Table struct {
						TableID string `json:"tableId"`
					} `json:"table"`
				} `json:"addTable"`
			} `json:"replies"`
		}
		_ = json.Unmarshal([]byte(body), &reply)
		if len(reply.Replies) == 0 {
			return ""
		}
		return reply.Replies[0].AddTable.Table.TableID
	}
	update := func(what, id string, columns []any) {
		status, body := batchOne(ctx, map[string]any{"updateTable": map[string]any{
			"table":  map[string]any{"tableId": id, "columnProperties": columns},
			"fields": "columnProperties",
		}})
		line("    %-52s -> HTTP %d  %s", what, status, first120(body))
	}
	drop := func(id string) {
		status, body := batchOne(ctx, map[string]any{"deleteTable": map[string]any{"tableId": id}})
		line("    %-52s -> HTTP %d  %s", "  deleted it, so the next add has the range", status, first120(body))
	}
	headers := func() { probe(ctx, "  the header row now", a1.QuoteSheet(sheet)+"!A1:D1") }

	line("")
	line("  Q2: a dropdown with no rule, and a rule on a number column")
	if id := add("DROPDOWN on D with no rule", []any{column(3, "DROPDOWN", nil)}); id != "" {
		drop(id)
	}
	if id := add("DOUBLE on B with a ONE_OF_LIST rule", []any{column(1, "DOUBLE", oneOf("x"))}); id != "" {
		drop(id)
	}

	line("")
	line("  Q1: columns 1 and 3 of four, with no names; what reads back?")
	id := add("CURRENCY on B, DROPDOWN x, y, z on D", []any{
		column(1, "CURRENCY", nil), column(3, "DROPDOWN", oneOf("x", "y", "z")),
	})
	if id == "" {
		return
	}
	columns := readColumns(ctx, sheetID)
	headers()
	cellsAt(ctx, "  B2 and D2 after the add (Q5)", a1.QuoteSheet(sheet)+"!B2:D2")

	line("")
	line("  Q3: an update that sends one column, with no name")
	update("DATE on B, alone", id, []any{column(1, "DATE", nil)})
	columns = readColumns(ctx, sheetID)
	headers()

	line("")
	line("  Q5: a boolean column over a text value and an empty cell")
	retyped := withoutNames(columns)
	retyped = setColumn(retyped, column(2, "BOOLEAN", nil))
	update("BOOLEAN on C, the rest as read, no names", id, retyped)
	readColumns(ctx, sheetID)
	probe(ctx, "  C2:C4 now (maybe, empty, TRUE before)", a1.QuoteSheet(sheet)+"!C2:C4")
	cellsAt(ctx, "  B2 and D2 after the retypes", a1.QuoteSheet(sheet)+"!B2:D2")

	line("")
	line("  Q6: the array sent back exactly as it read, names included")
	columns = readColumns(ctx, sheetID)
	update("every column as read", id, columns)
	headers()

	line("")
	line("  Q4: a column name sent on update")
	renamed := setName(columns, 0, "SPIKE-RENAMED")
	update("column 0 named SPIKE-RENAMED", id, renamed)
	readColumns(ctx, sheetID)
	headers()

	line("")
	line("  Q7: a formula and rich text in the header, under an update sending the names as read")
	status, body = putMode(ctx, a1.QuoteSheet(sheet)+"!B1", [][]any{{`="Amo"&"unt"`}}, "USER_ENTERED")
	line("    %-52s -> HTTP %d  %s", "a formula showing Amount, into B1", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"updateCells": map[string]any{
		"range": a1.Rect{FirstCol: 3, FirstRow: 1, LastCol: 3, LastRow: 1}.GridRange(sheetID),
		"rows": []any{map[string]any{"values": []any{map[string]any{
			"userEnteredValue": map[string]any{"stringValue": "Flag"},
			"textFormatRuns": []any{
				map[string]any{"startIndex": 0, "format": map[string]any{"bold": true}},
				map[string]any{"startIndex": 2, "format": map[string]any{}},
			},
		}}}},
		"fields": "userEnteredValue,textFormatRuns",
	}})
	line("    %-52s -> HTTP %d  %s", "Flag in C1, its first two letters bold", status, first120(body))
	headerCells := func(what string) {
		status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
			"?ranges="+url.QueryEscape(a1.QuoteSheet(sheet)+"!B1:C1")+
			"&fields="+url.QueryEscape("sheets(data(rowData(values(userEnteredValue,formattedValue,textFormatRuns))))"), nil)
		line("  %-50s -> HTTP %d  %s", what, status, first120(body))
	}
	headerCells("  B1 and C1 before the update")
	columns = readColumns(ctx, sheetID)
	update("every column as read, names included", id, columns)
	headerCells("  B1 and C1 after it: formula and runs kept?")
}

// column is one columnProperties entry with no name.
func column(index int, kind string, condition map[string]any) map[string]any {
	c := map[string]any{"columnIndex": index, "columnType": kind}
	if condition != nil {
		c["dataValidationRule"] = map[string]any{"condition": condition}
	}
	return c
}

// oneOf is a ONE_OF_LIST condition.
func oneOf(options ...string) map[string]any {
	values := make([]any, 0, len(options))
	for _, o := range options {
		values = append(values, map[string]any{"userEnteredValue": o})
	}
	return map[string]any{"type": "ONE_OF_LIST", "values": values}
}

// readColumns prints the first table's columns on a sheet, one a line,
// and returns them as read.
func readColumns(ctx context.Context, sheetID int) []any {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
		"?fields="+url.QueryEscape("sheets(properties(sheetId),tables(tableId,columnProperties))"), nil)
	if status != 200 {
		line("      read back -> HTTP %d  %s", status, first120(body))
		return nil
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			Tables []struct {
				ColumnProperties []json.RawMessage `json:"columnProperties"`
			} `json:"tables"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("      read back: unreadable reply")
		return nil
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID != sheetID || len(sh.Tables) == 0 {
			continue
		}
		var columns []any
		line("      %d column(s) read back:", len(sh.Tables[0].ColumnProperties))
		for _, raw := range sh.Tables[0].ColumnProperties {
			line("        %s", first120(string(raw)))
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			columns = append(columns, c)
		}
		return columns
	}
	line("      read back: no table on the sheet")
	return nil
}

// withoutNames is a read-back array with every columnName taken out,
// which asks whether a name left out clears its header.
func withoutNames(columns []any) []any {
	out := make([]any, 0, len(columns))
	for _, c := range columns {
		m, _ := c.(map[string]any)
		copied := map[string]any{}
		for k, v := range m {
			if k != "columnName" {
				copied[k] = v
			}
		}
		out = append(out, copied)
	}
	return out
}

// setColumn replaces the entry with the same index, or adds it.
func setColumn(columns []any, c map[string]any) []any {
	out := make([]any, 0, len(columns)+1)
	replaced := false
	for _, existing := range columns {
		m, _ := existing.(map[string]any)
		if index, _ := m["columnIndex"].(float64); int(index) == c["columnIndex"] {
			out, replaced = append(out, c), true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, c)
	}
	return out
}

// setName gives one entry of a read-back array a new name.
func setName(columns []any, index int, name string) []any {
	out := make([]any, 0, len(columns))
	for _, existing := range columns {
		m, _ := existing.(map[string]any)
		copied := map[string]any{}
		for k, v := range m {
			copied[k] = v
		}
		if i, _ := m["columnIndex"].(float64); int(i) == index {
			copied["columnName"] = name
		}
		out = append(out, copied)
	}
	return out
}

// cellsAt prints the number format and the validation rule of a range's
// cells, which is what Q5 watches across a type change.
func cellsAt(ctx context.Context, what, rangeA1 string) {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
		"?ranges="+url.QueryEscape(rangeA1)+
		"&fields="+url.QueryEscape("sheets(data(rowData(values(userEnteredValue,userEnteredFormat(numberFormat),dataValidation))))"), nil)
	line("  %-50s -> HTTP %d  %s", what, status, first120(body))
}
