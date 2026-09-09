//go:build live

package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// spikeQ answers §15.Q: what a merge and a clear do to a pivot table.
//
// §17a.27 gave the values write a refusal that names the pivot it is
// protecting. Two other tools reach the same cells and still say
// nothing about them. `format_cells merge` refuses with "merging would
// keep the top-left value and discard C20, which is not empty", and
// `clear_values` counts those cells into "removes 4 non-empty cell(s)"
// before its confirm gate.
//
// Neither refusal can be improved until this runs. The wording §17a.27
// uses — the whole pivot stops drawing, collapses to #REF! at the
// anchor, and clearing the cell brings all of it back — is spike M's
// finding about `values.update`, and nothing says a merge or a clear
// behaves the same way. A guard that assumed it would be this project
// putting an unverified claim in front of a caller, which is what rule
// 12 exists to stop.
//
// So: what does each do, is the damage reversible, and does the anchor
// differ from the output — the split that made the values write two
// different refusals rather than one.
func spikeQ(ctx context.Context) {
	sec("Spike Q: what a merge and a clear do to a pivot table")
	const sheet = "SpikePivotGuards"
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
	rebuild := func(what string) bool {
		status, body := writePivot(ctx, sheetID, 0, 4, pivot)
		line("    %s -> HTTP %d  %s", what, status, first120(body))
		return status == 200
	}
	if !rebuild("the pivot is written at E1") {
		line("  nothing else here means anything without a pivot; stopping.")
		return
	}
	pivotCells(ctx, "the pivot as it starts", q+"!E1:F10")

	line("")
	line("  Q1: what does merging cells of the output do?")
	// Wholly inside the output and not touching the anchor, which is
	// the case format_cells refuses today as "discard ... not empty".
	status, body := mergeCells(ctx, sheetID, a1.Rect{FirstCol: 5, FirstRow: 2, LastCol: 6, LastRow: 3})
	line("    mergeCells over E2:F3             -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after the merge", q+"!E1:F10")

	line("")
	line("  Q2: and is that reversible, the way a values write is?")
	// The values write's damage is total and completely reversible, and
	// the refusal says so. If a merge is not, the refusal has to say
	// something else.
	status, body = unmergeCells(ctx, sheetID, a1.Rect{FirstCol: 5, FirstRow: 2, LastCol: 6, LastRow: 3})
	line("    unmergeCells over the same range  -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after the unmerge", q+"!E1:F10")

	line("")
	line("  Q3: does a merge that takes in the anchor differ?")
	// The values write splits here: over the output it stops the pivot
	// drawing, over the anchor it replaces the pivot outright. Whether a
	// merge splits the same way decides whether the guard needs one
	// sentence or two.
	if !rebuild("the pivot is rebuilt first") {
		return
	}
	status, body = mergeCells(ctx, sheetID, a1.Rect{FirstCol: 5, FirstRow: 1, LastCol: 6, LastRow: 2})
	line("    mergeCells over E1:F2, the anchor -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after that merge", q+"!E1:F10")
	status, body = unmergeCells(ctx, sheetID, a1.Rect{FirstCol: 5, FirstRow: 1, LastCol: 6, LastRow: 2})
	line("    unmergeCells over the anchor      -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after the unmerge", q+"!E1:F10")

	line("")
	line("  Q3a: is a blank cell inside the footprint 'part of a pivot table'?")
	// The guard looks for a cell nobody typed, which is what a pivot
	// draws. A pivot grouped by columns leaves cells of its own
	// rectangle blank — live, the anchor row draws nothing at all in the
	// anchor's own column — so whether Google counts those as part of
	// the table decides whether the guard has a hole a merge falls
	// through into an untranslated 400.
	//
	// A second pivot, well clear of the first, and grouped across as
	// well as down so the blanks exist at all. The candidates below are
	// tried on their own rectangles: a merge leaves a merge behind, and
	// the first version of this probe overlapped its own first answer
	// and got "You must select all cells in a merged range" for its
	// second, which answers a question nobody asked.
	wide := map[string]any{
		"source": source,
		"rows": []any{map[string]any{
			"sourceColumnOffset": 0, "showTotals": true, "sortOrder": "ASCENDING",
		}},
		"columns": []any{map[string]any{
			"sourceColumnOffset": 2, "showTotals": false, "sortOrder": "ASCENDING",
		}},
		"values": []any{map[string]any{
			"summarizeFunction": "SUM", "sourceColumnOffset": 1,
		}},
		"valueLayout": "HORIZONTAL",
	}
	status, body = writePivot(ctx, sheetID, 9, 4, wide)
	line("    a pivot grouped across too, at E10 -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "where its blanks are", q+"!E10:M20")
	// One row each, so no attempt can trip over the merge another left.
	for _, at := range []a1.Rect{
		{FirstCol: 6, FirstRow: 11, LastCol: 7, LastRow: 11},
		{FirstCol: 6, FirstRow: 13, LastCol: 7, LastRow: 13},
		{FirstCol: 8, FirstRow: 15, LastCol: 9, LastRow: 15},
	} {
		status, body = mergeCells(ctx, sheetID, at)
		line("    mergeCells over %-7s inside it -> HTTP %d  %s", a1.FormatRect(at), status, first120(body))
		if status == 200 {
			_, _ = unmergeCells(ctx, sheetID, at)
		}
	}

	line("")
	line("  Q4: what does clearing an output cell do?")
	// clear_values counts these cells and warns that Sheets cannot undo
	// it. Nobody typed them, so there may be nothing there to clear —
	// and a confirm gate that says "removes 4 non-empty cells" about
	// cells a clear cannot touch is telling the caller the wrong thing.
	if !rebuild("the pivot is rebuilt first") {
		return
	}
	status, body = clearValues(ctx, q+"!F3")
	line("    values.clear over F3, in the output -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after the clear", q+"!E1:F10")

	line("")
	line("  Q5: and clearing the anchor?")
	// A pivot is deleted by an updateCells naming the pivotTable field.
	// values.clear names no field, so whether it takes the definition
	// with the value is exactly the kind of thing the reference does not
	// say and a guard has to know.
	status, body = clearValues(ctx, q+"!E1")
	line("    values.clear over E1, the anchor  -> HTTP %d  %s", status, first120(body))
	pivotCells(ctx, "the pivot after that clear", q+"!E1:F10")
}

// mergeCells and unmergeCells are the two requests format_cells compiles
// a merge into, sent bare so the answer is the API's rather than this
// server's reading of it.
func mergeCells(ctx context.Context, sheetID int, rect a1.Rect) (int, string) {
	return batchOne(ctx, map[string]any{"mergeCells": map[string]any{
		"range": rect.GridRange(sheetID), "mergeType": "MERGE_ALL",
	}})
}

func unmergeCells(ctx context.Context, sheetID int, rect a1.Rect) (int, string) {
	return batchOne(ctx, map[string]any{"unmergeCells": map[string]any{
		"range": rect.GridRange(sheetID),
	}})
}

// clearValues is what clear_values sends.
func clearValues(ctx context.Context, rangeA1 string) (int, string) {
	return call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+"/values/"+
		url.PathEscape(rangeA1)+":clear", map[string]any{})
}
