//go:build live

package main

import (
	"fmt"
	"strings"
)

// The chart and pivot steps, on a sheet of their own.
//
// Its own sheet for the reason the anchor steps have one: these delete a
// column to watch a chart lose its series, and write a pivot table whose
// output rectangle is computed rather than chosen. Both would move what
// every other step asserts about.
//
// What is under test is not that the calls succeed. Every one of these
// checks reads something back — the chart's own listing, the pivot's
// rectangle — because phase 2, 3 and 4 each found a defect that a green
// count hid and a transcript showed.
const chartSheet = "Livesheet charts"

// The runs are split where an id is: a step's arguments are built when
// the slice is, so the step that learns a chart's id and the step that
// uses it cannot be in the same slice. Phase 1 shipped a `sheetId: 0` on
// every create by getting this exact thing wrong one level down.
func (d *driver) chartAll() {
	sec("manage_chart")
	d.run(d.chartSetupSteps()...)
	d.run(d.chartAddSteps()...)
	d.run(d.chartEditSteps()...)
	d.run(d.chartOwnSheetSteps()...)
	d.run(d.chartOwnSheetEndSteps()...)
	d.run(d.slicerAddSteps()...)
	d.run(d.slicerEditSteps()...)
	sec("manage_pivot_table")
	d.run(d.pivotSteps()...)
	// Last, because it destroys a column the steps above are built on.
	d.run(d.chartLossSteps()...)
}

func (d *driver) chartSetupSteps() []step {
	return []step{
		{
			name: "add a sheet for the chart steps",
			why:  "these delete a charted column, which would move what every other step asserts about",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "title": chartSheet, "rows": 60, "cols": 10,
			},
		},
		{
			name: "fill it with something worth charting",
			why:  "a chart over an empty range draws nothing and proves nothing",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "range": "A1:C6",
				"values": [][]any{
					{"Region", "Units", "Returns"},
					{"Plimth", 120, 4},
					{"Quorbin", 85, 11},
					{"Plimth", 143, 2},
					{"Threnody", 67, 9},
					{"Marrowfen", 51, 6},
				},
			},
		},
	}
}

func (d *driver) chartAddSteps() []step {
	return []step{
		{
			name: "a dry run charts nothing",
			why:  "a preview that added a chart would be a preview that wrote",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"chart_type": "column", "title": "Preview only", "domain": "A1:A6",
				"series": []any{"B1:B6"}, "anchor": "E2", "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing")
				}
				if dry, _ := s["dry_run"].(bool); !dry {
					return fmt.Errorf("the structured half does not say it was a dry run")
				}
				return nil
			},
		},
		{
			name: "the preview left nothing behind",
			why:  "the claim above is about the spreadsheet, not about the wording of the reply",
			tool: "manage_chart",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				if strings.Contains(text, "Preview only") {
					return fmt.Errorf("the dry run added a chart")
				}
				return nil
			},
		},
		{
			name: "a chart with neither domain nor series is refused here",
			why:  "Google answers that shape with HTTP 500, which §6.5 classes retryable; the defence is never sending it",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"chart_type": "column", "title": "Nothing to draw", "anchor": "E2",
			},
			expectError: "invalid",
		},
		{
			name: "add a column chart with two series",
			why:  "the shape a caller actually asks for, and the one every later step names",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"chart_type": "column", "title": "Units by region", "subtitle": "from the live driver",
				"domain": "A1:A6", "series": []any{"B1:B6", "C1:C6"}, "headers": 1,
				"legend": "bottom", "stacked": "stacked", "axis_title": "Units",
				"anchor": "E2", "width": 500, "height": 300,
			},
			check: func(text string, s map[string]any) error {
				id, ok := s["id"].(float64)
				if !ok {
					return fmt.Errorf("the result carries no chart id, so nothing can name it again")
				}
				d.chartID = int(id)
				if !strings.Contains(text, "E2") {
					return fmt.Errorf("the result does not say where the chart went: %q", text)
				}
				return nil
			},
		},
	}
}

// chartEditSteps read d.chartID, so they are built after the add ran.
func (d *driver) chartEditSteps() []step {
	return []step{
		{
			name: "the listing reports it, with what it reads",
			why:  "an id and a title are what every later call needs, and the ranges are what a caller checks",
			tool: "manage_chart",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"Units by region", "A1:A6", "B1:B6"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the listing does not mention %q: %q", want, text)
					}
				}
				return nil
			},
		},
		{
			name: "renaming it keeps the series",
			why: "updateChartSpec replaces the spec whole, so a rename that rebuilt it from this server's own " +
				"struct would silently drop the chart's data",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"id": d.chartID, "title": "Units by region, renamed",
			},
		},
		{
			name: "and the listing proves it",
			why:  "the claim above is about what survived, which only a read can answer",
			tool: "manage_chart",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Units by region, renamed") {
					return fmt.Errorf("the rename did not land: %q", text)
				}
				if strings.Contains(text, "NO SERIES") {
					return fmt.Errorf("the rename dropped the chart's series: %q", text)
				}
				if !strings.Contains(text, "B1:B6") {
					return fmt.Errorf("the series is gone from the listing: %q", text)
				}
				return nil
			},
		},
		{
			name: "change what it draws",
			why:  "the other half of update: the data, not just the words",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"id": d.chartID, "chart_type": "bar", "series": []any{"B1:B6"},
				"legend": "right", "stacked": "none", "subtitle": "one series now",
			},
		},
		{
			name: "stacking a line chart is refused here",
			why: "Google refuses the whole request when stackedType reaches a LINE chart, so the refusal is " +
				"made where it can name the types that do take it",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"id": d.chartID, "chart_type": "line", "stacked": "stacked",
			},
			expectError: "invalid",
		},
		{
			name: "move and resize it",
			why:  "the API refuses this request without a field mask, which this server builds from the arguments",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "move",
				"id": d.chartID, "anchor": "G10", "width": 420, "height": 260,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "G10") {
					return fmt.Errorf("the result does not say where it landed: %q", text)
				}
				return nil
			},
		},
		{
			name: "a move with nothing to change is refused",
			why:  "without a field mask the API refuses outright, and the refusal should name the arguments",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "move", "id": d.chartID,
			},
			expectError: "invalid",
		},
	}
}

func (d *driver) chartOwnSheetSteps() []step {
	return []step{
		{
			name: "a chart on a sheet of its own",
			why:  "that sheet comes back sheetType OBJECT with no grid at all, which the card has to tolerate",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"chart_type": "pie", "title": "Share of units", "domain": "A1:A6",
				"series": []any{"B1:B6"}, "new_sheet": true,
			},
			check: func(text string, s map[string]any) error {
				id, ok := s["id"].(float64)
				if !ok {
					return fmt.Errorf("the result carries no chart id")
				}
				d.ownSheetChart = int(id)
				if !strings.Contains(text, "sheet of its own") {
					return fmt.Errorf("the result does not say where it went: %q", text)
				}
				return nil
			},
		},
	}
}

// chartOwnSheetEndSteps read d.ownSheetChart.
func (d *driver) chartOwnSheetEndSteps() []step {
	return []step{
		{
			name: "the card survives a sheet with no grid",
			why:  "an OBJECT sheet has no gridProperties, and everything that reads a card has to expect that",
			tool: "get_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet},
		},
		{
			name: "delete the chart on its own sheet",
			why:  "the reply to a delete is empty, so the result comes from a read taken first",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "delete", "id": d.ownSheetChart,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Share of units") {
					return fmt.Errorf("the result does not name what went: %q", text)
				}
				return nil
			},
		},
		{
			name: "deleting it again is a clean refusal",
			why:  "the second delete is the only feedback the API gives about the first",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "delete", "id": d.ownSheetChart,
			},
			expectError: "not_found",
		},
	}
}

func (d *driver) slicerAddSteps() []step {
	return []step{
		{
			name: "add a slicer over the block",
			why:  "a slicer is the other embedded object, and it shares the delete and the move with charts",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"slicer": true, "title": "By region", "range": "A1:C6", "column": "A", "anchor": "E20",
			},
			check: func(_ string, s map[string]any) error {
				id, ok := s["id"].(float64)
				if !ok {
					return fmt.Errorf("the result carries no slicer id")
				}
				d.slicerID = int(id)
				return nil
			},
		},
	}
}

// slicerEditSteps read d.slicerID.
func (d *driver) slicerEditSteps() []step {
	return []step{
		{
			name: "retitle it and filter on another column",
			why:  "a slicer's spec does take a field mask, so its update is a different path from a chart's",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"slicer": true, "id": d.slicerID, "title": "By returns", "column": "C",
			},
		},
		{
			name: "delete it",
			why:  "one request deletes both kinds, and the result has to name the right one",
			tool: "manage_chart",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "delete", "slicer": true, "id": d.slicerID,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "slicer") {
					return fmt.Errorf("the result calls it something other than a slicer: %q", text)
				}
				return nil
			},
		},
	}
}

func (d *driver) pivotSteps() []step {
	return []step{
		{
			name: "a dry run summarises nothing",
			why:  "a preview that wrote a pivot table would be a preview that wrote",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"anchor": "H1", "source": "A1:C6", "group_rows": []any{"A"},
				"values": []any{"B sum"}, "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing")
				}
				if dry, _ := s["dry_run"].(bool); !dry {
					return fmt.Errorf("the structured half does not say it was a dry run")
				}
				return nil
			},
		},
		{
			name: "an anchor inside the source is refused here",
			why:  "Google accepts it with a 200 and evaluates it to a circular reference",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"anchor": "B2", "source": "A1:C6", "group_rows": []any{"A"}, "values": []any{"B sum"},
			},
			expectError: "invalid",
		},
		{
			name: "a column outside the source is refused here",
			why:  "an offset past the source's width is accepted with a 200 and summarises nothing",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"anchor": "H1", "source": "A1:C6", "group_rows": []any{"A"}, "values": []any{"Z sum"},
			},
			expectError: "invalid",
		},
		{
			name: "add a pivot table, naming its columns by heading",
			why:  "the API groups by an offset into the source; a caller should never have to count",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "add",
				"anchor": "H1", "source": "A1:C6", "group_rows": []any{"Region"},
				"values": []any{"Units sum as Total units"}, "layout": "horizontal",
			},
			check: func(text string, s map[string]any) error {
				// The rectangle is the whole point: it is in no request
				// and no reply, and a caller who writes beside where they
				// think it ends writes into it.
				if !strings.Contains(text, "It covers") {
					return fmt.Errorf("the result does not say what the pivot covers: %q", text)
				}
				if anchor, _ := s["anchor"].(string); anchor != "H1" {
					return fmt.Errorf("the result's anchor is %q", anchor)
				}
				return nil
			},
		},
		{
			name: "the pivot's output is really there",
			why:  "the rectangle above is this server's reading of the sheet; a values read is somebody else's",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "range": "H1:I8",
			},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"Region", "Total units", "Grand Total"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the pivot did not draw %q: %q", want, text)
					}
				}
				return nil
			},
		},
		{
			name: "a write into the output is refused without overwrite",
			why: "the output carries an effectiveValue and no userEnteredValue, and if the guard could not see " +
				"that, a write would land on a pivot table and stop it drawing",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "range": "I3",
				"values": [][]any{{"over the pivot"}},
			},
			expectError: "blocked",
		},
		{
			name: "list what is anchored where",
			why:  "a pivot table has no id, so the anchor is its whole name and a listing is the only index",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "list", "range": "E1:K30",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "H1") {
					return fmt.Errorf("the listing does not find the pivot at H1: %q", text)
				}
				return nil
			},
		},
		{
			name: "group across as well as down",
			why:  "columns and rows take the same names and different arms of the request; only one is the default",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"anchor": "H1", "group_columns": []any{"Returns"},
			},
		},
		{
			name: "change what it summarises",
			why:  "an update replaces the pivot whole, so what it was not asked to change has to survive",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "update",
				"anchor": "H1", "values": []any{"Units sum as Total units", "Returns sum as Total returns"},
			},
		},
		{
			name: "the second value is drawn",
			why:  "the update's claim is about the sheet, not about its own wording",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "range": "H1:J8",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Total returns") {
					return fmt.Errorf("the second value is not in the output: %q", text)
				}
				return nil
			},
		},
		{
			name: "delete it, and everything it drew",
			why:  "there is no deletePivotTable: naming the field with an empty cell takes the whole output",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "delete", "anchor": "H1",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "cleared") {
					return fmt.Errorf("the result does not say what went: %q", text)
				}
				return nil
			},
		},
		{
			name: "nothing it drew is left behind",
			why:  "a delete that left a header row would look like a delete and be a half one",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "range": "H1:J8",
			},
			check: func(text string, _ map[string]any) error {
				for _, gone := range []string{"Grand Total", "Total units", "Total returns"} {
					if strings.Contains(text, gone) {
						return fmt.Errorf("%q survived the delete: %q", gone, text)
					}
				}
				return nil
			},
		},
		{
			name: "a delete where there is no pivot table says so",
			why:  "an update or a delete aimed at an empty cell is a mistake worth naming",
			tool: "manage_pivot_table",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "delete", "anchor": "J20",
			},
			expectError: "not_found",
		},
	}
}

// chartLossSteps are last, and they are the phase's finding: deleting a
// charted column leaves the chart in place with nothing to draw, and the
// API's reply says nothing at all.
func (d *driver) chartLossSteps() []step {
	return []step{
		{
			name: "an unconfirmed delete names the chart it would break",
			why:  "a caller who confirms should know what they are agreeing to",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "dimension": "columns", "band": "B:B",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing to draw") {
					return fmt.Errorf("the refusal does not say what happens to the chart: %q", text)
				}
				return nil
			},
		},
		{
			name: "the confirmed delete says what it did to the chart",
			why:  "Google's reply mentions none of this, so the result is the only place it is said",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": chartSheet, "dimension": "columns", "band": "B:B",
				"confirm": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "still there") {
					return fmt.Errorf("the result does not say the chart survived and stopped drawing: %q", text)
				}
				affected, _ := s["charts_affected"].([]any)
				if len(affected) == 0 {
					return fmt.Errorf("the structured half does not name the chart")
				}
				return nil
			},
		},
		{
			name: "and the listing shows the chart short of a series",
			why: "the chart above had one series by then, so this is a chart with nothing left; the first " +
				"version of this step asserted the same thing about a two-series chart and was simply wrong",
			tool: "manage_chart",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": chartSheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "no series left") {
					return fmt.Errorf("the listing does not report the chart as broken: %q", text)
				}
				return nil
			},
		},
		{
			name: "delete the chart that has nothing to draw",
			why:  "leaving it would leave the spreadsheet in the state this step exists to warn about",
			tool: "manage_chart",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "delete", "id": d.chartID},
		},
	}
}
