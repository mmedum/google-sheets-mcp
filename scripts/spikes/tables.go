//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

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
// sending the names as read do to it, to a header in rich text, and to
// one formatted as a whole cell? manage_range refuses the update over a
// formula header; this says whether that refusal can ever fire, and
// whether rich text and a whole-cell format need one.
// Q8. Is an entry with a name and no type taken on update? manage_range
// sends one for a column a read gave no entry.
// Q9. What does an update sending the names as read do to a person chip
// in a header cell? manage_range refuses the update over one.
//
// The run of 2026-10-09 answered most of these (§18) and left four
// owed. Q3 sent no name and was refused, so Q3b sends one column with
// its name. Q5 sent no names and was refused, so it sends them now. Q5's
// and Q7's reads were cut short in the transcript, so every read here
// prints whole. And the live driver's add of three typed columns was a
// 500 where Q1's add of two was a 200, which Q11 bisects.
//
// Q10. What does an add do to a formula in a header cell of a column it
// does not type? manage_range sends that column nothing, and still
// refuses an update over a formula header in case one survives.
// Q11. Which part of the live driver's add draws the 500: its exact
// request, where it ran it, each column type alone, and each with the
// column's name, which manage_range now sends.
//
// The second run answered Q11: no part of the request. The driver's add
// was taken first, and every add after the seventh table in the
// spreadsheet was a 500, the requests just taken included. Q5 answered
// that a word, the text TRUE and an empty cell all become FALSE, and Q7
// that a name sent as read drops rich text's runs; Q7 now formats A1 as a
// whole cell too, which the run did not ask. So:
//
// Q5b. Is a TRUE or FALSE value kept under a boolean typing, on update
// and on add? manage_range lets one through. The add's column holds a
// word and an empty cell too, which only an update has answered.
//
// Q12. Is it a count? In a fresh spreadsheet, the driver's add, a few
// seconds apart, until one fails or twelve are taken; then one table
// goes and the failed add is tried again.
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
		line("    %-52s -> HTTP %d  %s", what, status, whole(body))
		return addedTableID(status, body)
	}
	update := func(what, id string, columns []any) {
		status, body := batchOne(ctx, map[string]any{"updateTable": map[string]any{
			"table":  map[string]any{"tableId": id, "columnProperties": columns},
			"fields": "columnProperties",
		}})
		line("    %-52s -> HTTP %d  %s", what, status, whole(body))
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
	readColumns(ctx, sheetID)
	headers()
	cellsAt(ctx, "  B2 and D2 after the add (Q5)", a1.QuoteSheet(sheet)+"!B2:D2")

	line("")
	line("  Q3: an update that sends one column, with no name")
	update("DATE on B, alone", id, []any{column(1, "DATE", nil)})
	readColumns(ctx, sheetID)
	headers()

	line("")
	line("  Q3b: one column alone, with its name; do the other three keep their types?")
	columns := readColumns(ctx, sheetID)
	update("DATE on B, alone, named as read", id, []any{named(columns, 1, "DATE")})
	columns = readColumns(ctx, sheetID)
	headers()

	line("")
	line("  Q5: a boolean column over a text value and an empty cell")
	update("BOOLEAN on C, the rest as read, names included", id, setColumn(columns, named(columns, 2, "BOOLEAN")))
	readColumns(ctx, sheetID)
	probe(ctx, "  C2:C4 now (maybe, empty, TRUE before)", a1.QuoteSheet(sheet)+"!C2:C4")
	cellsAt(ctx, "  B2 and D2 after the retypes", a1.QuoteSheet(sheet)+"!B2:D2")

	line("")
	line("  Q5b: TRUE and FALSE values under a boolean typing, on update and on add: kept or reset?")
	status, body = putMode(ctx, a1.QuoteSheet(sheet)+"!I1:M4", [][]any{
		{"Name", "Done", "", "Name", "Done"},
		{"Quorbin", "TRUE", "", "Quorbin", "TRUE"},
		{"Skerry", "FALSE", "", "Skerry", "maybe"},
		{"Nardle", "TRUE", "", "Nardle", ""},
	}, "USER_ENTERED")
	line("    %-52s -> HTTP %d  %s", "J2:J4 TRUE, FALSE, TRUE; M2:M4 TRUE, maybe, empty", status, first120(body))
	cellsWhole(ctx, "  J2:J4 and M2:M4 before: values, not text?", a1.QuoteSheet(sheet)+"!J2:M4")
	pace()
	status, body = batchOne(ctx, map[string]any{"addTable": map[string]any{"table": map[string]any{
		"name": "SpikeBoolean", "range": a1.Rect{FirstCol: 9, FirstRow: 1, LastCol: 10, LastRow: 4}.GridRange(sheetID),
	}}})
	line("    %-52s -> HTTP %d  %s", "a plain table over I1:J4", status, first120(body))
	if boolID := addedTableID(status, body); boolID != "" {
		pace()
		update("BOOLEAN on J, both named", boolID, []any{
			map[string]any{"columnIndex": 0, "columnName": "Name"},
			map[string]any{"columnIndex": 1, "columnName": "Done", "columnType": "BOOLEAN"},
		})
		cellsWhole(ctx, "  J2:J4 after: TRUE, FALSE, TRUE kept?", a1.QuoteSheet(sheet)+"!J2:J4")
	}
	pace()
	status, body = batchOne(ctx, map[string]any{"addTable": map[string]any{"table": map[string]any{
		"name": "SpikeBooleanAdd", "range": a1.Rect{FirstCol: 12, FirstRow: 1, LastCol: 13, LastRow: 4}.GridRange(sheetID),
		"columnProperties": []any{map[string]any{"columnIndex": 1, "columnName": "Done", "columnType": "BOOLEAN"}},
	}}})
	line("    %-52s -> HTTP %d  %s", "a table over L1:M4, BOOLEAN on M named Done", status, whole(body))
	cellsWhole(ctx, "  M2:M4 after: TRUE kept, maybe and empty FALSE?", a1.QuoteSheet(sheet)+"!M2:M4")

	line("")
	line("  Q6: the array sent back exactly as it read, names included")
	columns = readColumns(ctx, sheetID)
	update("every column as read", id, columns)
	headers()

	line("")
	line("  Q8: an entry with its name and no type, the rest as read")
	columns = readColumns(ctx, sheetID)
	update("column 0 named Item, no type", id, setColumn(columns, map[string]any{"columnIndex": 0, "columnName": "Item"}))
	readColumns(ctx, sheetID)
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
	status, body = batchOne(ctx, map[string]any{"repeatCell": map[string]any{
		"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}.GridRange(sheetID),
		"cell": map[string]any{"userEnteredFormat": map[string]any{
			"textFormat": map[string]any{"bold": true, "italic": true}, "horizontalAlignment": "CENTER",
		}},
		"fields": "userEnteredFormat(textFormat,horizontalAlignment)",
	}})
	line("    %-52s -> HTTP %d  %s", "A1 bold, italic and centered as a whole cell", status, first120(body))
	headerCells := func(what string) {
		status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
			"?ranges="+url.QueryEscape(a1.QuoteSheet(sheet)+"!A1:C1")+
			"&fields="+url.QueryEscape("sheets(data(rowData(values(userEnteredValue,formattedValue,"+
			"userEnteredFormat(textFormat,horizontalAlignment),textFormatRuns))))"), nil)
		line("  %-50s -> HTTP %d  %s", what, status, whole(body))
	}
	headerCells("  A1:C1 before the update")
	columns = readColumns(ctx, sheetID)
	update("every column as read, names included", id, columns)
	headerCells("  A1:C1 after it: A1's format, C1's runs kept?")

	line("")
	line("  Q9: a person chip in the header, under an update sending the names as read")
	status, body = batchOne(ctx, map[string]any{"updateCells": map[string]any{
		"range": a1.Rect{FirstCol: 4, FirstRow: 1, LastCol: 4, LastRow: 1}.GridRange(sheetID),
		"rows": []any{map[string]any{"values": []any{map[string]any{
			"userEnteredValue": map[string]any{"stringValue": "@"},
			"chipRuns": []any{map[string]any{"chip": map[string]any{
				"personProperties": map[string]any{"email": "janedoe@example.com"},
			}}},
		}}}},
		"fields": "userEnteredValue,chipRuns",
	}})
	line("    %-52s -> HTTP %d  %s", "a person chip into D1", status, first120(body))
	chipCell := func(what string) {
		status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
			"?ranges="+url.QueryEscape(a1.QuoteSheet(sheet)+"!D1")+
			"&fields="+url.QueryEscape("sheets(data(rowData(values(userEnteredValue,formattedValue,chipRuns))))"), nil)
		line("  %-50s -> HTTP %d  %s", what, status, whole(body))
	}
	chipCell("  D1 before the update")
	columns = readColumns(ctx, sheetID)
	update("every column as read, names included", id, columns)
	chipCell("  D1 after it: chip kept?")

	line("")
	line("  Q10: an add over a formula in the header of a column it does not type")
	status, body = putMode(ctx, a1.QuoteSheet(sheet)+"!F1:G3",
		[][]any{{`="Ite"&"m"`, "Cost"}, {"Quorbin", 4}, {"Skerry", 5}}, "USER_ENTERED")
	line("    %-52s -> HTTP %d  %s", "a formula showing Item in F1, Cost in G1", status, whole(body))
	status, body = batchOne(ctx, map[string]any{"addTable": map[string]any{"table": map[string]any{
		"name": "SpikeFormulaHead", "range": a1.Rect{FirstCol: 6, FirstRow: 1, LastCol: 7, LastRow: 3}.GridRange(sheetID),
		"columnProperties": []any{map[string]any{"columnIndex": 1, "columnName": "Cost", "columnType": "DOUBLE"}},
	}}})
	line("    %-52s -> HTTP %d  %s", "a table over F1:G3, DOUBLE on G named Cost", status, whole(body))
	cellsWhole(ctx, "  F1 after the add: formula kept?", a1.QuoteSheet(sheet)+"!F1")

	spikeT11(ctx)
	spikeT12(ctx)
}

// spikeT11 bisects the live driver's 500. The driver typed three of four
// columns of a block with a header row, numbers, dates and words, on a
// sheet with one row and one column frozen; spike T Q1 typed two, on a
// plain sheet, and was taken. Each add here is on a block of its own,
// seeded the way the driver seeded it, so no add sees another's types.
func spikeT11(ctx context.Context) {
	line("")
	line("  Q11: which part of the live driver's add draws its 500")
	const sheet = "SpikeTypes"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("    setup failed: %v", err)
		return
	}
	seed := typedSeed
	options := oneOf("Open", "In progress", "Done")
	names := []string{"Item", "Amount", "Due", "Status"}
	type typed struct {
		index int
		kind  string
	}
	driver := []typed{{1, "DOUBLE"}, {2, "DATE"}, {3, "DROPDOWN"}}
	cases := []struct {
		what    string
		columns []typed
	}{
		{"the driver's request: DOUBLE on B, DATE on C, DROPDOWN on D", driver},
		{"CURRENCY on B alone, which Q1 took", []typed{{1, "CURRENCY"}}},
		{"DOUBLE on B alone", []typed{{1, "DOUBLE"}}},
		{"DATE on C alone, over date serials", []typed{{2, "DATE"}}},
		{"TEXT on A alone", []typed{{0, "TEXT"}}},
		{"PERCENT on B alone", []typed{{1, "PERCENT"}}},
		{"TIME on C alone", []typed{{2, "TIME"}}},
		{"DATE_TIME on C alone", []typed{{2, "DATE_TIME"}}},
		{"BOOLEAN on D alone, over Open, Done and an empty cell", []typed{{3, "BOOLEAN"}}},
		{"DROPDOWN Open, In progress, Done on D alone", []typed{{3, "DROPDOWN"}}},
	}
	build := func(columns []typed, withNames bool) []any {
		out := make([]any, 0, len(columns))
		for _, c := range columns {
			var condition map[string]any
			if c.kind == "DROPDOWN" {
				condition = options
			}
			entry := column(c.index, c.kind, condition)
			if withNames {
				entry["columnName"] = names[c.index]
			}
			out = append(out, entry)
		}
		return out
	}

	// One block for every case twice, six rows apart, written at once.
	var rows [][]any
	for range 2 * len(cases) {
		rows = append(rows, seed...)
		rows = append(rows, []any{"", "", "", ""}, []any{"", "", "", ""})
	}
	if status, body := putMode(ctx, a1.QuoteSheet(sheet)+"!A1:D"+strconv.Itoa(len(rows)), rows, "USER_ENTERED"); status != 200 {
		line("    setup failed: HTTP %d  %s", status, first120(body))
		return
	}
	block := 0
	try := func(on string, onID, firstRow int, what string, columns []any) {
		block++
		rect := a1.Rect{FirstCol: 1, FirstRow: firstRow, LastCol: 4, LastRow: firstRow + 3}
		pace()
		status, body := batchOne(ctx, map[string]any{"addTable": map[string]any{"table": map[string]any{
			"name": "SpikeTypes" + strconv.Itoa(block), "range": rect.GridRange(onID), "columnProperties": columns,
		}}})
		line("    %-62s -> HTTP %d  %s", what, status, whole(body))
		if status != 200 {
			return
		}
		pace()
		status, body = getValues(ctx, a1.Format(on, rect))
		line("      the block after it -> HTTP %d  %s", status, whole(body))
	}
	for i, withNames := range []bool{false, true} {
		line("")
		if withNames {
			line("    each again, every column named by its header, as manage_range now sends it")
		} else {
			line("    with no names, as manage_range sent it on the live run")
		}
		for j, c := range cases {
			try(sheet, sheetID, 1+6*(i*len(cases)+j), c.what, build(c.columns, withNames))
		}
	}

	line("")
	line("    the driver's request where the driver sent it: A50:D53 of a sheet with a row and a column frozen")
	const frozen = "SpikeFrozen"
	frozenID, err := addSheet(ctx, frozen)
	if err != nil {
		line("    setup failed: %v", err)
		return
	}
	status, body := batchOne(ctx, map[string]any{"updateSheetProperties": map[string]any{
		"properties": map[string]any{"sheetId": frozenID, "gridProperties": map[string]any{
			"frozenRowCount": 1, "frozenColumnCount": 1,
		}},
		"fields": "gridProperties.frozenRowCount,gridProperties.frozenColumnCount",
	}})
	line("    %-62s -> HTTP %d  %s", "freeze one row and one column", status, whole(body))
	pace()
	if status, body := putMode(ctx, a1.QuoteSheet(frozen)+"!A50:D59", append(append(append([][]any{}, seed...),
		[]any{"", "", "", ""}, []any{"", "", "", ""}), seed...), "USER_ENTERED"); status != 200 {
		line("    setup failed: HTTP %d  %s", status, first120(body))
		return
	}
	try(frozen, frozenID, 50, "the driver's request, no names", build(driver, false))
	try(frozen, frozenID, 56, "the driver's request, every column named", build(driver, true))
}

// addedTableID is the id an addTable reply gives its table, or "" for a
// refusal.
func addedTableID(status int, body string) string {
	var reply struct {
		Replies []struct {
			AddTable struct {
				Table struct {
					TableID string `json:"tableId"`
				} `json:"table"`
			} `json:"addTable"`
		} `json:"replies"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &reply) != nil || len(reply.Replies) == 0 {
		return ""
	}
	return reply.Replies[0].AddTable.Table.TableID
}

// typedSeed is the live driver's block: a header row, then numbers, dates
// and words, written as a person types them.
var typedSeed = [][]any{
	{"Item", "Amount", "Due", "Status"},
	{"Quorbin", "12.5", "2026-10-01", "Open"},
	{"Skerry", "3", "2026-10-02", "Done"},
	{"Nardle", "40", "2026-10-03", ""},
}

// spikeT12 tells a count from timing. In Q11's spreadsheet every add
// after the seventh table was a 500; the live run's add was a 500 after
// one table, added and deleted. Here a fresh spreadsheet takes the
// driver's add, each column named as manage_range sends it, three seconds
// apart, until one fails or twelve are taken. Then the first table goes
// and the failed add is tried again: taken means a count. If not, it is
// tried once more a minute later, which is timing if taken. A plain add
// with no types comes last, to say whether typing matters.
func spikeT12(ctx context.Context) {
	line("")
	line("  Q12: how many adds a fresh spreadsheet takes, and whether one table fewer lets the next in")
	title := "spike scratch tables " + time.Now().UTC().Format("2006-01-02 15:04:05")
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets", map[string]any{
		"properties": map[string]any{"title": title},
		"sheets":     []any{map[string]any{"properties": map[string]any{"title": "SpikeCount"}}},
	})
	var created struct {
		SpreadsheetID string `json:"spreadsheetId"`
		Sheets        []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &created) != nil || len(created.Sheets) == 0 {
		line("    setup failed: HTTP %d  %s", status, first120(body))
		return
	}
	line("    NOTE: a second spreadsheet, left behind; remove %q by hand.", title)
	// Every helper writes to scratchID, so it points at the fresh
	// spreadsheet for this question, and back after.
	defer func(was string) { scratchID = was }(scratchID)
	scratchID = created.SpreadsheetID
	sheetID := created.Sheets[0].Properties.SheetID

	const most = 12
	var rows [][]any
	for range most + 1 {
		rows = append(rows, typedSeed...)
		rows = append(rows, []any{"", "", "", ""}, []any{"", "", "", ""})
	}
	if status, body := putMode(ctx, "SpikeCount!A1:D"+strconv.Itoa(len(rows)), rows, "USER_ENTERED"); status != 200 {
		line("    setup failed: HTTP %d  %s", status, first120(body))
		return
	}
	driver := []any{
		map[string]any{"columnIndex": 1, "columnName": "Amount", "columnType": "DOUBLE"},
		map[string]any{"columnIndex": 2, "columnName": "Due", "columnType": "DATE"},
		map[string]any{"columnIndex": 3, "columnName": "Status", "columnType": "DROPDOWN",
			"dataValidationRule": map[string]any{"condition": oneOf("Open", "In progress", "Done")}},
	}
	start := time.Now()
	add := func(block int, what string, columns []any) (string, bool) {
		time.Sleep(3 * time.Second)
		rect := a1.Rect{FirstCol: 1, FirstRow: 1 + 6*block, LastCol: 4, LastRow: 4 + 6*block}
		table := map[string]any{"name": "SpikeCount" + strconv.Itoa(block+1), "range": rect.GridRange(sheetID)}
		if columns != nil {
			table["columnProperties"] = columns
		}
		status, body := batchOne(ctx, map[string]any{"addTable": map[string]any{"table": table}})
		shown := first120(body)
		if status != 200 {
			shown = whole(body)
		}
		line("    %-56s -> HTTP %d  %s", fmt.Sprintf("%s, at %ds", what, int(time.Since(start).Seconds())), status, shown)
		id := addedTableID(status, body)
		return id, id != ""
	}

	var firstID string
	failed := -1
	for i := range most {
		id, ok := add(i, fmt.Sprintf("add %d, with %d table(s) there", i+1, i), driver)
		if !ok {
			failed = i
			break
		}
		if firstID == "" {
			firstID = id
		}
	}
	if failed < 0 {
		line("    COUNT: all %d taken, three seconds apart; no limit at or under %d tables", most, most)
		return
	}
	line("    COUNT: %d add(s) taken, then add %d failed", failed, failed+1)
	if firstID != "" {
		status, body := batchOne(ctx, map[string]any{"deleteTable": map[string]any{"tableId": firstID}})
		line("    %-56s -> HTTP %d  %s", "the first table deleted", status, whole(body))
		if _, ok := add(failed, fmt.Sprintf("add %d again, one table fewer", failed+1), driver); !ok {
			time.Sleep(time.Minute)
			add(failed, fmt.Sprintf("add %d again, a minute later", failed+1), driver)
		}
	}
	add(most, "a plain add, no column types", nil)
}

// named is one entry of a read-back array with a new type, carrying the
// name the read gave it, as manage_range sends an update.
func named(columns []any, index int, kind string) map[string]any {
	entry := map[string]any{"columnIndex": index, "columnType": kind}
	for _, c := range columns {
		m, _ := c.(map[string]any)
		if i, _ := m["columnIndex"].(float64); int(i) == index {
			entry["columnName"] = m["columnName"]
		}
	}
	return entry
}

// cellsWhole prints what a range's cells hold and show, formulas
// included, uncut.
func cellsWhole(ctx context.Context, what, rangeA1 string) {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
		"?ranges="+url.QueryEscape(rangeA1)+
		"&fields="+url.QueryEscape("sheets(data(rowData(values(userEnteredValue,formattedValue))))"), nil)
	line("  %-50s -> HTTP %d  %s", what, status, whole(body))
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
			line("        %s", whole(string(raw)))
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			columns = append(columns, c)
		}
		return columns
	}
	line("      read back: no table on the sheet")
	return nil
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
	line("  %-50s -> HTTP %d  %s", what, status, whole(body))
}
