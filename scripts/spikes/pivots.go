//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
)

// spikeM answers §15.M: what a pivot table is on the wire, and what
// this server's own read sees where one sits.
//
// A pivot table is the odd one in phase 4. It is not a batchUpdate
// request at all: it is a field of CellData, written through updateCells
// at one anchor cell, and its output "is computed dynamically based on
// its data" — so its footprint is not stated anywhere in the request
// that made it. Three consequences decide manage_pivot_table and none
// of them is in the reference.
//
// What does the server's own grid mask (gapi.GridFields, which asks for
// no pivotTable field) see over a pivot's output? If those cells look
// empty, the write guard lets a write land on top of a pivot table, and
// hard rule 6 is broken by a tool that has not been written yet. If they
// look full, a refusal can count them but cannot name what they are.
//
// What does a write into that output do — refuse, or take the pivot with
// it? And how is a pivot deleted, given there is no deletePivotTable?
func spikeM(ctx context.Context) {
	sec("Spike M: pivot tables, and what a read sees where one sits")
	const sheet = "SpikePivots"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:C5", [][]any{
		{"Region", "Units", "Returns"},
		{"Plimth", 120, 4},
		{"Quorbin", 85, 11},
		{"Plimth", 143, 2},
		{"Threnody", 67, 9},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	source := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 5}.GridRange(sheetID)

	// pivot groups the four rows by region and sums the units, which is
	// the smallest pivot that has more than one output row.
	pivot := map[string]any{
		"source": source,
		"rows": []any{map[string]any{
			"sourceColumnOffset": 0, "showTotals": true, "sortOrder": "ASCENDING",
		}},
		"values": []any{map[string]any{
			"summarizeFunction": "SUM", "sourceColumnOffset": 1,
		}},
		"valueLayout": "HORIZONTAL",
	}

	line("")
	line("  Q1: what does writing a pivot through updateCells reply with?")
	status, body := writePivot(ctx, sheetID, 0, 4, pivot)
	line("    updateCells with pivotTable at E1  -> HTTP %d  %s", status, first120(body))
	if status != 200 {
		line("  nothing else in this spike means anything without a pivot; stopping.")
		return
	}

	line("")
	line("  Q2: where is the pivot, and where is its output?")
	// The pivot is one field on one cell. Its output is however many
	// cells Google decided to fill, and the only way to learn the
	// second is to read them.
	pivotCells(ctx, "the pivot's own rectangle", q+"!E1:F10")

	line("")
	line("  Q3: what does the server's own grid mask see there?")
	// gapi.GridFields verbatim, not a mask written for this spike: the
	// question is what the write guard is looking at, and a mask that
	// differs by one field answers a question nobody asked.
	gridMaskSees(ctx, q+"!E1:F10")

	line("")
	line("  Q4: does the output move when the source grows?")
	// Two different questions, and the first run of this spike asked
	// only the weaker one: it wrote a row *below* A1:C5 and reported
	// that nothing moved. Of course nothing moved — the source is a
	// fixed rectangle. The question that decides the tool is what a row
	// added *inside* the source does, and whether a new group makes the
	// output taller than the caller's request said anything about.
	if err := put(ctx, q+"!A6:C6", [][]any{{"Quorbin", 200, 1}}); err != nil {
		line("    could not add a row below the source: %v", err)
	} else {
		line("    a row was written below the source rectangle, outside it")
		pivotCells(ctx, "the pivot after a row below it", q+"!E1:F10")
	}
	status, body = batchOne(ctx, map[string]any{"insertDimension": map[string]any{
		"range": map[string]any{
			"sheetId": sheetID, "dimension": "ROWS", "startIndex": 2, "endIndex": 3,
		},
		"inheritFromBefore": false,
	}})
	line("    insert a row inside the source     -> HTTP %d  %s", status, first120(body))
	if err := put(ctx, q+"!A3:C3", [][]any{{"Skerry", 50, 0}}); err != nil {
		line("    could not fill the inserted row: %v", err)
	}
	pivotCells(ctx, "the pivot after a fifth group", q+"!E1:F10")

	line("")
	line("  Q5: what happens to a values write into the output?")
	// The answer decides whether the guard has to refuse this or whether
	// the API already does.
	code, resp := putMode(ctx, q+"!F3", [][]any{{"typed over the pivot"}}, userEntered)
	line("    values.update over an output cell  -> HTTP %d  %s", code, first120(resp))
	pivotCells(ctx, "the pivot after that write", q+"!E1:F10")
	// Whether the damage is reversible decides how a refusal should be
	// worded: "this will break the pivot until you clear the cell" is a
	// different warning from "this will destroy the pivot".
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+"/values/"+
		url.PathEscape(q+"!F3")+":clear", map[string]any{})
	line("    clearing the cell that blocked it  -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after the block went", q+"!E1:F10")

	line("")
	line("  Q6: and an updateCells write over the anchor?")
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{map[string]any{"updateCells": map[string]any{
			"start":  map[string]any{"sheetId": sheetID, "rowIndex": 0, "columnIndex": 4},
			"rows":   []any{map[string]any{"values": []any{map[string]any{"userEnteredValue": map[string]any{"stringValue": "plain text"}}}}},
			"fields": "userEnteredValue",
		}}}})
	line("    updateCells userEnteredValue at E1 -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after that", q+"!E1:F10")

	// Q6 replaced the pivot with a string, so there is nothing left for
	// Q7 to delete. Rebuild it: a question about deleting a pivot table
	// asked where there is no pivot table answers itself wrongly.
	status, body = writePivot(ctx, sheetID, 0, 4, pivot)
	line("    the pivot was written again        -> HTTP %d", status)
	pivotCells(ctx, "the rebuilt pivot", q+"!E1:F10")

	line("")
	line("  Q7: how is a pivot deleted, and does the output go with it?")
	// There is no deletePivotTable request. An updateCells naming the
	// field with no pivot in the cell is the documented shape for
	// clearing a CellData field, and whether it takes the whole output
	// or leaves the computed cells behind is the question.
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{map[string]any{"updateCells": map[string]any{
			"start":  map[string]any{"sheetId": sheetID, "rowIndex": 0, "columnIndex": 4},
			"rows":   []any{map[string]any{"values": []any{map[string]any{}}}},
			"fields": "pivotTable",
		}}}})
	line("    updateCells fields=pivotTable, no pivot -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the rectangle after the delete", q+"!E1:F10")

	line("")
	line("  Q8: what does an invalid pivot say?")
	// Every group here carries a sortOrder. The first run of this spike
	// left it out, and both probes came back "No sort order specified."
	// — the same message for two different questions, neither of which
	// was the one being asked. A validation order this server does not
	// know about answers first.
	group := map[string]any{"sourceColumnOffset": 0, "sortOrder": "ASCENDING"}
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"source": source,
		"rows":   []any{group},
		"values": []any{map[string]any{"sourceColumnOffset": 1}},
	})
	line("    a value with no summarizeFunction  -> HTTP %d  %s", status, first120(body))
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"source": source,
		"rows":   []any{group},
		"values": []any{map[string]any{"summarizeFunction": "SUM", "sourceColumnOffset": 9}},
	})
	line("    an offset past the source          -> HTTP %d  %s", status, first120(body))
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"source": source,
		"rows":   []any{map[string]any{"sourceColumnOffset": 0}},
		"values": []any{map[string]any{"summarizeFunction": "SUM", "sourceColumnOffset": 1}},
	})
	line("    a group with no sortOrder          -> HTTP %d  %s", status, first120(body))
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"source": source,
		"values": []any{map[string]any{"summarizeFunction": "SUM", "sourceColumnOffset": 1}},
	})
	line("    values but no rows or columns      -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "whatever that last one made, at E13", q+"!E13:F18")
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"source": source,
		"rows":   []any{group},
	})
	line("    rows but no values                 -> HTTP %d  %s", status, first120(body))
	status, body = writePivot(ctx, sheetID, 12, 4, map[string]any{
		"rows":   []any{group},
		"values": []any{map[string]any{"summarizeFunction": "SUM", "sourceColumnOffset": 1}},
	})
	line("    no source at all                   -> HTTP %d  %s", status, first120(body))

	line("")
	line("  Q9: may a pivot land on top of its own source, or inside itself?")
	// Both are refusals this server would rather write itself, and both
	// are only worth writing if the API does not.
	status, body = writePivot(ctx, sheetID, 1, 0, pivot)
	line("    anchored at A2, inside the source  -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the source rectangle after that", q+"!A1:C6")

	line("")
	line("  Q10: may the source be on another sheet?")
	other, err := addSheet(ctx, "SpikePivotTarget")
	if err != nil {
		line("    could not add a sheet: %v", err)
	} else {
		status, body = writePivot(ctx, other, 0, 0, pivot)
		line("    a pivot on one sheet over another  -> HTTP %d  %s", status, first120(body))
		pivotCells(ctx, "the other sheet's rectangle", a1.QuoteSheet("SpikePivotTarget")+"!A1:B10")

		line("")
		line("  Q11: and when the source rows are deleted under it?")
		status, body = batchOne(ctx, map[string]any{"deleteDimension": map[string]any{
			"range": map[string]any{
				"sheetId": sheetID, "dimension": "ROWS", "startIndex": 1, "endIndex": 5,
			},
		}})
		line("    delete four of the source rows     -> HTTP %d  %s", status, first120(body))
		pivotCells(ctx, "the pivot over the shortened source", a1.QuoteSheet("SpikePivotTarget")+"!A1:B10")
	}
}

// writePivot anchors a pivot table at one zero-based cell.
func writePivot(ctx context.Context, sheetID, row, col int, pivot map[string]any) (int, string) {
	return call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{map[string]any{"updateCells": map[string]any{
			"start":  map[string]any{"sheetId": sheetID, "rowIndex": row, "columnIndex": col},
			"rows":   []any{map[string]any{"values": []any{map[string]any{"pivotTable": pivot}}}},
			"fields": "pivotTable",
		}}}})
}

// pivotCells prints a rectangle cell by cell: which cells carry a pivot
// definition, and which merely carry what one computed. The two are
// different things and a summary that adds them up hides the answer.
func pivotCells(ctx context.Context, what, rangeA1 string) {
	v := url.Values{}
	v.Set("includeGridData", "true")
	v.Add("ranges", rangeA1)
	v.Set("fields", "sheets(data(startRow,startColumn,rowData(values("+
		"userEnteredValue,effectiveValue,formattedValue,pivotTable))))")
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
	if status != 200 {
		line("    %s -> HTTP %d  %s", what, status, first120(body))
		return
	}
	var out struct {
		Sheets []struct {
			Data []struct {
				StartRow    int `json:"startRow"`
				StartColumn int `json:"startColumn"`
				RowData     []*struct {
					Values []*struct {
						UserEnteredValue json.RawMessage `json:"userEnteredValue"`
						EffectiveValue   json.RawMessage `json:"effectiveValue"`
						FormattedValue   string          `json:"formattedValue"`
						PivotTable       json.RawMessage `json:"pivotTable"`
					} `json:"values"`
				} `json:"rowData"`
			} `json:"data"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("    %s -> unreadable: %v", what, err)
		return
	}
	line("    %s:", what)
	anchors, computed, entered := 0, 0, 0
	for _, sh := range out.Sheets {
		for _, d := range sh.Data {
			for i, row := range d.RowData {
				if row == nil {
					continue
				}
				for j, c := range row.Values {
					if c == nil {
						continue
					}
					addr, err := a1.CellName(d.StartColumn+j+1, d.StartRow+i+1)
					if err != nil {
						continue
					}
					var notes []string
					if len(c.PivotTable) > 0 {
						anchors++
						notes = append(notes, fmt.Sprintf("a pivot definition of %d bytes", len(c.PivotTable)))
					}
					if len(c.UserEnteredValue) > 0 {
						entered++
						notes = append(notes, "userEnteredValue "+first120(string(c.UserEnteredValue)))
					}
					if len(c.EffectiveValue) > 0 {
						computed++
						notes = append(notes, "effectiveValue "+first120(string(c.EffectiveValue)))
					}
					if len(notes) == 0 {
						continue
					}
					line("      %-4s %s", addr, strings.Join(notes, ", "))
				}
			}
		}
	}
	line("      %d cell(s) carry a pivot, %d carry a computed value, %d carry an entered one",
		anchors, computed, entered)
}

// gridMaskSees reads a rectangle through the server's own field mask and
// says what the guard would count. Nothing here is a fresh mask: if this
// disagrees with what the guard does in production, the mask is what has
// to change.
func gridMaskSees(ctx context.Context, rangeA1 string) {
	v := url.Values{}
	v.Set("includeGridData", "true")
	v.Add("ranges", rangeA1)
	v.Set("fields", gapi.GridFields)
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
	if status != 200 {
		line("    the server's grid mask -> HTTP %d  %s", status, first120(body))
		return
	}
	var out struct {
		Sheets []struct {
			Data []struct {
				StartRow    int `json:"startRow"`
				StartColumn int `json:"startColumn"`
				RowData     []*struct {
					Values []*struct {
						UserEnteredValue json.RawMessage `json:"userEnteredValue"`
						EffectiveValue   json.RawMessage `json:"effectiveValue"`
						FormattedValue   string          `json:"formattedValue"`
					} `json:"values"`
				} `json:"rowData"`
			} `json:"data"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("    the server's grid mask -> unreadable: %v", err)
		return
	}
	nonEmpty, entered := 0, 0
	var shown []string
	for _, sh := range out.Sheets {
		for _, d := range sh.Data {
			for i, row := range d.RowData {
				if row == nil {
					continue
				}
				for j, c := range row.Values {
					if c == nil || (len(c.EffectiveValue) == 0 && len(c.UserEnteredValue) == 0) {
						continue
					}
					nonEmpty++
					if len(c.UserEnteredValue) > 0 {
						entered++
					}
					if addr, err := a1.CellName(d.StartColumn+j+1, d.StartRow+i+1); err == nil && len(shown) < 6 {
						shown = append(shown, fmt.Sprintf("%s=%q", addr, c.FormattedValue))
					}
				}
			}
		}
	}
	// The guard counts a cell as non-empty from its rendered kind, which
	// comes from effectiveValue. So this pair of numbers is the whole
	// question: how many cells a refusal would report, and how many of
	// them anybody typed.
	line("    the server's grid mask sees %d non-empty cell(s), %d of them with a userEnteredValue",
		nonEmpty, entered)
	line("      %s", strings.Join(shown, "  "))
	line("      it asks for no pivotTable field, so a refusal here can count these and cannot name them")
}
