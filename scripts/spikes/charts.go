//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// spikeL answers §15.L: what a chart is once it exists, and what it
// survives.
//
// §8 promises manage_chart with add, update, delete and move. Four
// things the reference does not settle decide what those can honestly
// say. A chart's spec is a union of thirteen chart kinds and
// UpdateChartSpecRequest carries no field mask, so an update may be a
// whole-spec replace — which would make "change the title" a call that
// silently discards the series. A chart's source is a GridRange, and
// nothing says what happens to it when the columns underneath are
// deleted. DeleteEmbeddedObjectRequest has no response type at all,
// which is the shape phase 2's deleteTable and phase 3's anchors both
// turned out to have. And the card asks for charts(chartId), so what a
// wider mask would buy is unmeasured.
//
// Every answer here is read back out of the API after the write, never
// inferred from the request that was sent.
func spikeL(ctx context.Context) {
	sec("Spike L: charts, and what a chart survives")
	const sheet = "SpikeCharts"
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
		{"Vandel", 143, 2},
		{"Threnody", 67, 9},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}

	col := func(c int) *gsheets.GridRange {
		return a1.Rect{FirstCol: c, FirstRow: 1, LastCol: c, LastRow: 5}.GridRange(sheetID)
	}
	// A column chart over Region/Units: one domain, one series, both
	// single columns, which is the shape ChartSourceRange demands.
	spec := func(title string) map[string]any {
		return map[string]any{
			"title": title,
			"basicChart": map[string]any{
				"chartType":   "COLUMN",
				"headerCount": 1,
				"domains": []any{map[string]any{
					"domain": map[string]any{"sourceRange": map[string]any{"sources": []any{col(1)}}},
				}},
				"series": []any{map[string]any{
					"series": map[string]any{"sourceRange": map[string]any{"sources": []any{col(2)}}},
				}},
			},
		}
	}

	line("")
	line("  Q1: what does addChart reply with, and does it choose a position?")
	status, body := batchOne(ctx, map[string]any{"addChart": map[string]any{
		"chart": map[string]any{
			"spec": spec("Units by region"),
			"position": map[string]any{"overlayPosition": map[string]any{
				"anchorCell": map[string]any{"sheetId": sheetID, "rowIndex": 1, "columnIndex": 4},
			}},
		},
	}})
	line("    addChart, anchored at E2           -> HTTP %d  %s", status, first120(body))
	if status != 200 {
		line("  nothing else in this spike means anything without a chart; stopping.")
		return
	}
	chartID := addedChartID(body)
	line("    the reply names chart %d", chartID)
	chartFields(ctx, "as created", chartID)

	line("")
	line("  Q2: what does the card's charts(chartId) mask leave on the table?")
	// The card is paid for on every get_spreadsheet, so the question is
	// what a wider mask costs and whether it buys a title worth showing.
	for _, mask := range []string{
		"sheets(charts(chartId))",
		"sheets(charts(chartId,spec(title,altText)))",
		"sheets(charts(chartId,spec(title),position))",
		"sheets(charts)",
	} {
		status, body := call(ctx, http.MethodGet,
			sheetsBase+"/spreadsheets/"+scratchID+"?fields="+url.QueryEscape(mask), nil)
		line("    %-44s -> HTTP %d  %d bytes", mask, status, len(body))
	}

	line("")
	line("  Q3: is updateChartSpec a merge or a whole-spec replace?")
	// The request carries no field mask. If it replaces, "rename this
	// chart" is a call that discards the series, and manage_chart has to
	// read the spec, edit it and send it back whole.
	status, body = batchOne(ctx, map[string]any{"updateChartSpec": map[string]any{
		"chartId": chartID,
		"spec":    map[string]any{"title": "Renamed, and nothing else sent"},
	}})
	line("    a spec carrying only a title       -> HTTP %d  %s", status, first120(body))
	chartFields(ctx, "after the title-only update", chartID)

	// Whatever the last step did, put a whole spec back so the rest of
	// the spike is asking about a chart that has a series.
	status, _ = batchOne(ctx, map[string]any{"updateChartSpec": map[string]any{
		"chartId": chartID, "spec": spec("Units by region"),
	}})
	line("    the whole spec sent again          -> HTTP %d", status)
	chartFields(ctx, "after the whole spec", chartID)

	line("")
	line("  Q4: how does a chart move, and is the fields mask required?")
	status, body = batchOne(ctx, map[string]any{"updateEmbeddedObjectPosition": map[string]any{
		"objectId": chartID,
		"newPosition": map[string]any{"overlayPosition": map[string]any{
			"anchorCell": map[string]any{"sheetId": sheetID, "rowIndex": 7, "columnIndex": 4},
		}},
	}})
	line("    move to E8, no fields given        -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"updateEmbeddedObjectPosition": map[string]any{
		"objectId": chartID,
		"fields":   "anchorCell",
		"newPosition": map[string]any{"overlayPosition": map[string]any{
			"anchorCell": map[string]any{"sheetId": sheetID, "rowIndex": 9, "columnIndex": 4},
		}},
	}})
	line("    move to E10, fields=anchorCell     -> HTTP %d  %s", status, first120(body))
	chartFields(ctx, "after both moves", chartID)

	line("")
	line("  Q5: what does an invalid spec say?")
	// The message is the refusal this server has to write before
	// sending, so the wording matters as much as the status.
	//
	// Each of these carries a valid position. The first run of this
	// spike did not, and both probes came back refused on the position
	// without the spec ever being looked at — an answer about the wrong
	// half of the request, printed under a heading that said "spec".
	somewhere := map[string]any{"overlayPosition": map[string]any{
		"anchorCell": map[string]any{"sheetId": sheetID, "rowIndex": 20, "columnIndex": 0},
	}}
	status, body = batchOne(ctx, map[string]any{"addChart": map[string]any{
		"chart": map[string]any{
			"position": somewhere,
			"spec": map[string]any{
				"title":      "No domain, no series",
				"basicChart": map[string]any{"chartType": "COLUMN"},
			},
		},
	}})
	line("    a basicChart with neither          -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"addChart": map[string]any{
		"chart": map[string]any{
			"position": somewhere,
			"spec":     map[string]any{"title": "No chart kind at all"},
		},
	}})
	line("    a spec naming no chart kind        -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"addChart": map[string]any{
		"chart": map[string]any{"spec": spec("No position at all")},
	}})
	line("    a whole spec with no position      -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"updateChartSpec": map[string]any{
		"chartId": chartID + 99999, "spec": spec("Nobody's chart"),
	}})
	line("    updateChartSpec on a stranger's id -> HTTP %d  %s", status, first120(body))

	line("")
	line("  Q6: where does addChart with newSheet put it, and what is that sheet?")
	status, body = batchOne(ctx, map[string]any{"addChart": map[string]any{
		"chart": map[string]any{
			"spec":     spec("On a sheet of its own"),
			"position": map[string]any{"newSheet": true},
		},
	}})
	line("    addChart, newSheet: true           -> HTTP %d  %s", status, first120(body))
	ownSheet := addedChartID(body)
	if pos := addedChartSheet(body); pos != 0 {
		line("    it made sheet %d; the card calls it:", pos)
		describeSheet(ctx, pos)
	}

	line("")
	line("  Q7: a slicer — same reply shape, same delete?")
	status, body = batchOne(ctx, map[string]any{"addSlicer": map[string]any{
		"slicer": map[string]any{
			"spec": map[string]any{
				"dataRange":   a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 5}.GridRange(sheetID),
				"columnIndex": 0,
				"title":       "Region",
			},
			"position": map[string]any{"overlayPosition": map[string]any{
				"anchorCell": map[string]any{"sheetId": sheetID, "rowIndex": 15, "columnIndex": 0},
			}},
		},
	}})
	line("    addSlicer                          -> HTTP %d  %s", status, first120(body))
	slicerID := addedSlicerID(body)
	line("    the card's slicers:                   %s", slicerSummary(ctx, sheetID))

	line("")
	line("  Q8: does a chart notice when the column it charts is deleted?")
	// This is the guard's question. If the chart survives pointing at a
	// column that now holds something else, a delete_dimensions over a
	// charted range is a silent rewrite of what the chart says.
	before := chartSources(ctx, chartID)
	line("    the series reads %s before the delete", before)
	status, body = batchOne(ctx, map[string]any{"deleteDimension": map[string]any{
		"range": map[string]any{
			"sheetId": sheetID, "dimension": "COLUMNS", "startIndex": 1, "endIndex": 2,
		},
	}})
	line("    delete column B, the series        -> HTTP %d  %s", status, first120(body))
	line("    the series reads %s after it", chartSources(ctx, chartID))
	chartFields(ctx, "after its column went", chartID)

	line("")
	line("  Q9: what does deleteEmbeddedObject say it did?")
	// Two silent deletes have been found on this API already, so the
	// reply body is read rather than the status.
	if slicerID != 0 {
		status, body = batchOne(ctx, map[string]any{"deleteEmbeddedObject": map[string]any{"objectId": slicerID}})
		line("    delete the slicer                  -> HTTP %d  %s", status, first120(body))
		line("    the card's slicers now:               %s", slicerSummary(ctx, sheetID))
	}
	status, body = batchOne(ctx, map[string]any{"deleteEmbeddedObject": map[string]any{"objectId": chartID}})
	line("    delete the chart                   -> HTTP %d  %s", status, first120(body))
	line("    the card's charts now:                %s", chartSummary(ctx, sheetID))
	status, body = batchOne(ctx, map[string]any{"deleteEmbeddedObject": map[string]any{"objectId": chartID}})
	line("    delete the same id again           -> HTTP %d  %s", status, first120(body))

	line("")
	line("  Q10: what happens to the chart on its own sheet when that sheet goes?")
	if ownSheet != 0 {
		if pos := chartSheetOf(ctx, ownSheet); pos != 0 {
			status, body = batchOne(ctx, map[string]any{"deleteSheet": map[string]any{"sheetId": pos}})
			line("    delete the sheet holding it        -> HTTP %d  %s", status, first120(body))
			line("    chart %d is now:                      %s", ownSheet, chartWhereabouts(ctx, ownSheet))
		}
	}
}

// chartFields prints what a full read of one chart carries: which fields
// the object has, and which arm of the spec union is set. The point is
// what survived, so it names the fields rather than dumping the spec.
func chartFields(ctx context.Context, what string, chartID int) {
	c := chartByID(ctx, chartID)
	if c == nil {
		line("    %-34s -> chart GONE", what)
		return
	}
	var spec map[string]json.RawMessage
	_ = json.Unmarshal(c.Spec, &spec)
	kinds := make([]string, 0, len(spec))
	for k := range spec {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	line("    %-34s -> spec fields: %s", what, strings.Join(kinds, ","))
	var basic struct {
		Series  []json.RawMessage `json:"series"`
		Domains []json.RawMessage `json:"domains"`
	}
	if raw, ok := spec["basicChart"]; ok {
		_ = json.Unmarshal(raw, &basic)
		line("        basicChart: %d domain(s), %d series", len(basic.Domains), len(basic.Series))
	}
	if c.Position != nil {
		line("        position: %s", positionOf(c.Position))
	}
}

// rawChart is the half of an EmbeddedChart these questions read. The
// spec stays raw: the question is which fields came back, not what is
// in them, and unmarshalling into a typed spec would answer it wrongly
// by dropping whatever this file did not think to declare.
type rawChart struct {
	ChartID  int             `json:"chartId"`
	Spec     json.RawMessage `json:"spec"`
	Position json.RawMessage `json:"position"`
}

func chartsOn(ctx context.Context, sheetID int) []rawChart {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields="+
			url.QueryEscape("sheets(properties(sheetId),charts)"), nil)
	if status != 200 {
		return nil
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			Charts []rawChart `json:"charts"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID == sheetID {
			return sh.Charts
		}
	}
	return nil
}

// chartByID finds a chart anywhere in the spreadsheet, because half
// these questions are about whether it is still where it was put.
func chartByID(ctx context.Context, chartID int) *rawChart {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields="+url.QueryEscape("sheets(charts)"), nil)
	if status != 200 {
		return nil
	}
	var out struct {
		Sheets []struct {
			Charts []rawChart `json:"charts"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil
	}
	for _, sh := range out.Sheets {
		for i, c := range sh.Charts {
			if c.ChartID == chartID {
				return &sh.Charts[i]
			}
		}
	}
	return nil
}

// chartSources says which ranges the first series reads, in A1, which is
// the only form a caller of this server ever sees.
func chartSources(ctx context.Context, chartID int) string {
	c := chartByID(ctx, chartID)
	if c == nil {
		return "(the chart is gone)"
	}
	var spec struct {
		BasicChart struct {
			Series []struct {
				Series struct {
					SourceRange struct {
						Sources []*gsheets.GridRange `json:"sources"`
					} `json:"sourceRange"`
				} `json:"series"`
			} `json:"series"`
		} `json:"basicChart"`
	}
	if err := json.Unmarshal(c.Spec, &spec); err != nil || len(spec.BasicChart.Series) == 0 {
		return "(no series)"
	}
	var parts []string
	for _, s := range spec.BasicChart.Series[0].Series.SourceRange.Sources {
		parts = append(parts, gridString(s))
	}
	if len(parts) == 0 {
		return "(no source range)"
	}
	return strings.Join(parts, " + ")
}

// gridString prints a GridRange the way a person reads one. The wire
// indices are zero-based and half-open, and a spike that prints them raw
// invites the reader to compare them with an A1 address and get the
// answer off by one — so the conversion goes through internal/a1, which
// is the only place in this repository allowed to do it.
func gridString(g *gsheets.GridRange) string {
	if g == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s on sheet %d", a1.FormatRect(a1.FromGridRange(g)), g.SheetID)
}

func positionOf(raw json.RawMessage) string {
	var p struct {
		SheetID         int  `json:"sheetId"`
		NewSheet        bool `json:"newSheet"`
		OverlayPosition *struct {
			AnchorCell struct {
				SheetID     int `json:"sheetId"`
				RowIndex    int `json:"rowIndex"`
				ColumnIndex int `json:"columnIndex"`
			} `json:"anchorCell"`
			WidthPixels  int `json:"widthPixels"`
			HeightPixels int `json:"heightPixels"`
		} `json:"overlayPosition"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "(unreadable)"
	}
	if p.OverlayPosition == nil {
		return fmt.Sprintf("on its own sheet %d", p.SheetID)
	}
	a := p.OverlayPosition.AnchorCell
	cell, err := a1.CellName(a.ColumnIndex+1, a.RowIndex+1)
	if err != nil {
		cell = fmt.Sprintf("row %d col %d", a.RowIndex+1, a.ColumnIndex+1)
	}
	return fmt.Sprintf("overlaid at %s on sheet %d, %dx%d px",
		cell, a.SheetID, p.OverlayPosition.WidthPixels, p.OverlayPosition.HeightPixels)
}

func addedChartID(body string) int {
	var out struct {
		Replies []struct {
			AddChart struct {
				Chart struct {
					ChartID int `json:"chartId"`
				} `json:"chart"`
			} `json:"addChart"`
		} `json:"replies"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Replies) == 0 {
		return 0
	}
	return out.Replies[0].AddChart.Chart.ChartID
}

// addedChartSheet is the sheet a newSheet chart landed on, which the
// reply is the only cheap place to learn.
func addedChartSheet(body string) int {
	var out struct {
		Replies []struct {
			AddChart struct {
				Chart struct {
					Position struct {
						SheetID int `json:"sheetId"`
					} `json:"position"`
				} `json:"chart"`
			} `json:"addChart"`
		} `json:"replies"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Replies) == 0 {
		return 0
	}
	return out.Replies[0].AddChart.Chart.Position.SheetID
}

func addedSlicerID(body string) int {
	var out struct {
		Replies []struct {
			AddSlicer struct {
				Slicer struct {
					SlicerID int `json:"slicerId"`
				} `json:"slicer"`
			} `json:"addSlicer"`
		} `json:"replies"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Replies) == 0 {
		return 0
	}
	return out.Replies[0].AddSlicer.Slicer.SlicerID
}

func chartSummary(ctx context.Context, sheetID int) string {
	found := chartsOn(ctx, sheetID)
	if len(found) == 0 {
		return "none"
	}
	var ids []string
	for _, c := range found {
		ids = append(ids, fmt.Sprintf("%d", c.ChartID))
	}
	return strings.Join(ids, ", ")
}

// slicerSummary reads slicers, which the card does not ask for at all.
// Whether they are reachable through the same mask as charts decides
// whether manage_chart can list them beside the charts.
func slicerSummary(ctx context.Context, sheetID int) string {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields="+
			url.QueryEscape("sheets(properties(sheetId),slicers)"), nil)
	if status != 200 {
		return fmt.Sprintf("HTTP %d %s", status, first120(body))
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			Slicers []struct {
				SlicerID int `json:"slicerId"`
				Spec     struct {
					Title string `json:"title"`
				} `json:"spec"`
			} `json:"slicers"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "(unreadable)"
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID != sheetID {
			continue
		}
		if len(sh.Slicers) == 0 {
			return "none"
		}
		var parts []string
		for _, s := range sh.Slicers {
			parts = append(parts, fmt.Sprintf("%d titled %q", s.SlicerID, s.Spec.Title))
		}
		return strings.Join(parts, ", ")
	}
	return "none"
}

// chartSheetOf is the id of the sheet a chart lives on, for a chart that
// was given a sheet of its own.
func chartSheetOf(ctx context.Context, chartID int) int {
	c := chartByID(ctx, chartID)
	if c == nil {
		return 0
	}
	var p struct {
		SheetID int `json:"sheetId"`
	}
	if err := json.Unmarshal(c.Position, &p); err != nil {
		return 0
	}
	return p.SheetID
}

func chartWhereabouts(ctx context.Context, chartID int) string {
	if c := chartByID(ctx, chartID); c != nil {
		return "still here, " + positionOf(c.Position)
	}
	return "gone with the sheet"
}

// describeSheet says what kind of sheet the API made, which is the
// question a chart sheet raises: a caller listing sheets should not be
// shown a 1000x26 grid that is really a chart.
func describeSheet(ctx context.Context, sheetID int) {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields="+
			url.QueryEscape("sheets(properties(sheetId,title,index,sheetType,gridProperties))"), nil)
	if status != 200 {
		line("        HTTP %d %s", status, first120(body))
		return
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID        int    `json:"sheetId"`
				Title          string `json:"title"`
				Index          int    `json:"index"`
				SheetType      string `json:"sheetType"`
				GridProperties *struct {
					RowCount    int `json:"rowCount"`
					ColumnCount int `json:"columnCount"`
				} `json:"gridProperties"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("        (unreadable)")
		return
	}
	for _, sh := range out.Sheets {
		p := sh.Properties
		if p.SheetID != sheetID {
			continue
		}
		grid := "no gridProperties"
		if p.GridProperties != nil {
			grid = fmt.Sprintf("%d rows x %d columns", p.GridProperties.RowCount, p.GridProperties.ColumnCount)
		}
		line("        title %q, index %d, sheetType %q, %s", p.Title, p.Index, p.SheetType, grid)
		return
	}
	line("        the card does not list sheet %d", sheetID)
}
