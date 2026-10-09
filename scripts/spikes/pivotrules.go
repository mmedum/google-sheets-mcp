//go:build live

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
)

// spikeU asks Google what manage_pivot_table refuses before sending, or
// writes on a belief: grouping rules, filters and calculated values.
// The live driver cannot see these, because the tool refuses most of
// them first. §18 records each belief this settles.
//
// U1. Is a value with both sourceColumnOffset and formula refused, and
// with what words? A formula summarized by AVERAGE? CUSTOM on a column?
// A formula value with no name: what heading is drawn?
// U2. Are two groups with a rule on one source column refused? A plain
// group beside a rule on the same column taken?
// U3. A histogram with interval 0, and one with start at or past end:
// refused? Is a start of 0 read back, or dropped as unset?
// U4. What labels do YEAR_MONTH and a histogram draw, with and without
// start and end, and where does a value equal to end go?
// U5. A condition alone with visibleByDefault false and no visible
// values: does it show nothing, as the reference reads, or every value
// meeting it? And with visibleByDefault true?
// U6. Is a condition only data validation takes refused on a filter?
// U7. Sent filterSpecs alone, does criteria read back too? Sent
// criteria alone, filterSpecs? Sent a pivot with neither over one that
// had both, does any filter survive?
func spikeU(ctx context.Context) {
	sec("Spike U: pivot grouping rules, filters and calculated values")
	const sheet = "SpikePivotRules"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if status, body := putMode(ctx, q+"!A1:E7", [][]any{
		{"Region", "Day", "Age", "Revenue", "Cost"},
		{"East", "2026-01-01", 23, 100, 60},
		{"West", "2026-02-01", 37, 200, 150},
		{"East", "2026-02-02", 41, 50, 10},
		{"North", "2026-01-01", 70, 300, 100},
		{"West", "2026-04-01", 55, 80, 40},
		{"East", "2026-04-02", 29, 20, 30},
	}, "USER_ENTERED"); status != 200 {
		line("  setup failed: HTTP %d  %s", status, first120(body))
		return
	}
	source := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 5, LastRow: 7}.GridRange(sheetID)
	group := func(offset int, rule map[string]any) map[string]any {
		g := map[string]any{"sourceColumnOffset": offset, "showTotals": true, "sortOrder": "ASCENDING"}
		if rule != nil {
			g["groupRule"] = rule
		}
		return g
	}
	sum := map[string]any{"sourceColumnOffset": 3, "summarizeFunction": "SUM"}
	pivot := func(rows []any, values []any, extra map[string]any) map[string]any {
		p := map[string]any{"source": source, "rows": rows, "values": values}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	try := func(what string, p map[string]any) bool {
		status, body := writePivot(ctx, sheetID, 0, 6, p)
		line("    %-60s -> HTTP %d  %s", what, status, first120(body))
		return status == 200
	}
	show := func(what string) {
		probe(ctx, "  "+what, q+"!G1:J12")
		definition(ctx, q+"!G1")
	}
	histogram := func(interval float64, bounds ...float64) map[string]any {
		h := map[string]any{"interval": interval}
		if len(bounds) > 0 {
			h["start"] = bounds[0]
		}
		if len(bounds) > 1 {
			h["end"] = bounds[1]
		}
		return map[string]any{"histogramRule": h}
	}
	month := map[string]any{"dateTimeRule": map[string]any{"type": "YEAR_MONTH"}}

	line("")
	line("  U1: calculated values")
	try("a value with an offset and a formula", pivot([]any{group(0, nil)},
		[]any{map[string]any{"sourceColumnOffset": 3, "formula": "=Revenue", "summarizeFunction": "SUM"}}, nil))
	try("a formula averaged", pivot([]any{group(0, nil)},
		[]any{map[string]any{"formula": "=Revenue", "summarizeFunction": "AVERAGE"}}, nil))
	try("CUSTOM on a column", pivot([]any{group(0, nil)},
		[]any{map[string]any{"sourceColumnOffset": 3, "summarizeFunction": "CUSTOM"}}, nil))
	if try("a formula with no name, summed per row", pivot([]any{group(0, nil)},
		[]any{map[string]any{"formula": "=Revenue-Cost", "summarizeFunction": "SUM"}}, nil)) {
		show("its heading and Margin per region (East 70, North 200, West 90)")
	}

	line("")
	line("  U2: one rule per source column")
	try("two rules on Day", pivot([]any{group(1, month),
		group(1, map[string]any{"dateTimeRule": map[string]any{"type": "YEAR"}})}, []any{sum}, nil))
	try("a plain group and a rule on Day", pivot([]any{group(1, nil), group(1, month)}, []any{sum}, nil))

	line("")
	line("  U3: histogram bounds")
	try("interval 0", pivot([]any{group(2, histogram(0))}, []any{sum}, nil))
	try("start 70, end 20", pivot([]any{group(2, histogram(10, 70, 20))}, []any{sum}, nil))
	if try("start 0, interval 10", pivot([]any{group(2, histogram(10, 0))}, []any{sum}, nil)) {
		show("is start 0 read back?")
	}

	line("")
	line("  U4: labels")
	if try("YEAR_MONTH on Day", pivot([]any{group(1, month)}, []any{sum}, nil)) {
		show("month labels")
	}
	if try("Age every 20 from 25 to 70 (70 is the end itself)", pivot([]any{group(2, histogram(20, 25, 70))}, []any{sum}, nil)) {
		show("bucket labels, and where 70 went")
	}
	if try("Age every 10, no bounds", pivot([]any{group(2, histogram(10))}, []any{sum}, nil)) {
		show("bucket labels with no start")
	}

	line("")
	line("  U5: a condition alone")
	over60 := map[string]any{"type": "NUMBER_GREATER", "values": []any{map[string]any{"userEnteredValue": "60"}}}
	for _, byDefault := range []bool{false, true} {
		criteria := map[string]any{"condition": over60}
		if byDefault {
			criteria["visibleByDefault"] = true
		}
		label := "Revenue over 60, visibleByDefault " + map[bool]string{false: "false", true: "true"}[byDefault]
		if try(label, pivot([]any{group(0, nil)}, []any{sum}, map[string]any{
			"filterSpecs": []any{map[string]any{"columnOffsetIndex": 3, "filterCriteria": criteria}},
		})) {
			show("which regions show (all four over 60 would be East 100, North 300, West 280)")
		}
	}

	line("")
	line("  U6: a condition only data validation takes")
	try("ONE_OF_LIST on Region", pivot([]any{group(0, nil)}, []any{sum}, map[string]any{
		"filterSpecs": []any{map[string]any{"columnOffsetIndex": 0, "filterCriteria": map[string]any{
			"visibleByDefault": true,
			"condition":        map[string]any{"type": "ONE_OF_LIST", "values": []any{map[string]any{"userEnteredValue": "East"}}},
		}}},
	}))

	line("")
	line("  U7: the two filter forms")
	east := map[string]any{"visibleValues": []any{"East"}}
	if try("filterSpecs alone", pivot([]any{group(0, nil)}, []any{sum}, map[string]any{
		"filterSpecs": []any{map[string]any{"columnOffsetIndex": 0, "filterCriteria": east}},
	})) {
		show("does criteria read back too?")
	}
	if try("criteria alone", pivot([]any{group(0, nil)}, []any{sum}, map[string]any{
		"criteria": map[string]any{"0": east},
	})) {
		show("does filterSpecs read back too?")
	}
	if try("neither, over the pivot that had both", pivot([]any{group(0, nil)}, []any{sum}, nil)) {
		show("does any filter survive?")
	}
}

// definition prints the pivot definition on one cell, as Google stores
// it.
func definition(ctx context.Context, cell string) {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
		"?ranges="+url.QueryEscape(cell)+"&fields="+url.QueryEscape("sheets(data(rowData(values(pivotTable))))"), nil)
	// Whole, not cut: the filter forms and the rule are what is asked.
	line("      the definition -> HTTP %d  %s", status, strings.Join(strings.Fields(body), " "))
}
