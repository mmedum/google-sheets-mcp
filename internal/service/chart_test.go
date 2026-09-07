package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// addOne is the chart every test here starts from: a column chart on the
// first sheet reading one column of labels and one of values.
func addOne(t *testing.T, svc *service.Service, title string) int {
	t.Helper()
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "column", Title: title,
		Domain: "A1:A6", Series: []string{"B1:B6"}, Anchor: "E2",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.ID == 0 {
		t.Fatal("add returned no id, so nothing else can name the chart")
	}
	return res.ID
}

func TestChartAdd(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "column", Title: "Units",
		Domain: "A1:A6", Series: []string{"B1:B6", "C1:C6"}, Anchor: "E2",
		Legend: "bottom", Stacked: "stacked", Headers: 1,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	text := res.Render()
	for _, want := range []string{"column chart", `"Units"`, "E2", "A1:A6", "B1:B6", "id "} {
		if !strings.Contains(text, want) {
			t.Errorf("the result does not mention %q:\n%s", want, text)
		}
	}
}

// TestChartAddRefusesWhatGoogleAnswersWith500 is the reason manage_chart
// validates a spec at all. Live, a basicChart with neither domains nor
// series comes back HTTP 500 "Internal error encountered", and §6.5
// classes a 500 as retryable — so the only defence is never sending it.
func TestChartAddRefusesWhatGoogleAnswersWith500(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "column", Title: "Nothing to draw", Anchor: "E2",
	})
	if err == nil {
		t.Fatal("a chart with no domain and no series was accepted")
	}
	if !strings.Contains(err.Error(), "[invalid]") {
		t.Errorf("class = %v, want invalid", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("the request was sent; the whole point is that it never leaves this machine")
		}
	}
}

func TestChartAddRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.ChartRequest
		want string
	}{
		{
			name: "no chart type",
			req:  service.ChartRequest{Domain: "A1:A6", Series: []string{"B1:B6"}, Anchor: "E2"},
			want: "chart_type",
		},
		{
			name: "unknown chart type",
			req: service.ChartRequest{ChartType: "sunburst", Domain: "A1:A6",
				Series: []string{"B1:B6"}, Anchor: "E2"},
			want: "sunburst",
		},
		{
			name: "no series",
			req:  service.ChartRequest{ChartType: "line", Domain: "A1:A6", Anchor: "E2"},
			want: "series",
		},
		{
			name: "a series covering a block",
			req: service.ChartRequest{ChartType: "line", Domain: "A1:A6",
				Series: []string{"B1:D6"}, Anchor: "E2"},
			want: "one column or one row",
		},
		{
			name: "no anchor and no new sheet",
			req:  service.ChartRequest{ChartType: "line", Domain: "A1:A6", Series: []string{"B1:B6"}},
			want: "anchor",
		},
		{
			name: "anchor and new sheet at once",
			req: service.ChartRequest{ChartType: "line", Domain: "A1:A6", Series: []string{"B1:B6"},
				Anchor: "E2", NewSheet: true},
			want: "pass one",
		},
		{
			name: "an anchor that is a range",
			req: service.ChartRequest{ChartType: "line", Domain: "A1:A6", Series: []string{"B1:B6"},
				Anchor: "E2:F8"},
			want: "one cell",
		},
		{
			name: "a legend nobody offers",
			req: service.ChartRequest{ChartType: "line", Domain: "A1:A6", Series: []string{"B1:B6"},
				Anchor: "E2", Legend: "sideways"},
			want: "sideways",
		},
		{
			name: "two series on a pie",
			req: service.ChartRequest{ChartType: "pie", Domain: "A1:A6",
				Series: []string{"B1:B6", "C1:C6"}, Anchor: "E2"},
			want: "one series",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.Action = service.ChartAdd
			_, err := svc.ManageChart(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestChartAddOnItsOwnSheet(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "pie", Title: "Share",
		Domain: "A1:A6", Series: []string{"B1:B6"}, NewSheet: true,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(res.Render(), "a sheet of its own") {
		t.Errorf("the result does not say where it went:\n%s", res.Render())
	}
}

// TestChartUpdateKeepsWhatItWasNotAskedToChange is the whole reason the
// spec is round-tripped as raw JSON. updateChartSpec replaces the spec
// rather than merging into it, so a title-only update that rebuilt the
// spec from a struct would silently drop everything the struct does not
// model.
func TestChartUpdateKeepsWhatItWasNotAskedToChange(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Before")

	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartUpdate, ID: id, Title: "After",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *service.ChartRecord
	for i, c := range res.Charts {
		if c.ID == id {
			found = &res.Charts[i]
		}
	}
	if found == nil {
		t.Fatal("the chart is gone after a title change")
	}
	if found.Title != "After" {
		t.Errorf("title = %q, want After", found.Title)
	}
	// The series is the thing a struct round trip would have lost.
	if found.Broken {
		t.Error("the chart lost its series to a title change, which is the silent destroy this design exists to avoid")
	}
	if !strings.Contains(found.Sources, "B1:B6") {
		t.Errorf("sources = %q, want the series to have survived", found.Sources)
	}
}

func TestChartUpdateData(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: id,
		ChartType: "bar", Series: []string{"C1:C6"}, Legend: "right", Stacked: "percent",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, want := range []string{"chart_type", "series", "legend", "stacked"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the result does not name %q as changed:\n%s", want, res.Render())
		}
	}
}

func TestChartUpdateRefusals(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	for _, tc := range []struct {
		name string
		req  service.ChartRequest
		want string
	}{
		{"no id", service.ChartRequest{Action: service.ChartUpdate, Title: "x"}, "id"},
		{"nothing to change", service.ChartRequest{Action: service.ChartUpdate, ID: id}, "nothing to change"},
		{"an id nobody has", service.ChartRequest{Action: service.ChartUpdate, ID: id + 500, Title: "x"}, "no chart with id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Spreadsheet = sheetstest.FixtureID
			_, err := svc.ManageChart(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestChartMove(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartMove, ID: id, Anchor: "H10", Width: 400,
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if !strings.Contains(res.Render(), "H10") {
		t.Errorf("the result does not say where it went:\n%s", res.Render())
	}
}

// TestChartMoveNeedsSomethingToChange is the argument check standing in
// for the API's own: without a field mask the request is refused
// outright, and the mask is built from these arguments.
func TestChartMoveNeedsSomethingToChange(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	_, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartMove, ID: id,
	})
	if err == nil || !strings.Contains(err.Error(), "anchor, width or height") {
		t.Fatalf("error = %v, want the arguments named", err)
	}
}

func TestChartDelete(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartDelete, ID: id,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The reply to a delete is empty, so the result has to say that what
	// it describes came from a read taken first.
	for _, want := range []string{`"Units"`, "reply", "read beforehand"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the result does not mention %q:\n%s", want, res.Render())
		}
	}
	after, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range after.Charts {
		if c.ID == id {
			t.Error("the chart is still there")
		}
	}
}

func TestChartDeleteNamesTheIdsThatExist(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	_, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartDelete, ID: id + 99,
	})
	if err == nil {
		t.Fatal("accepted")
	}
	if !strings.Contains(err.Error(), "[not_found]") || !strings.Contains(err.Error(), "it has") {
		t.Errorf("error = %v, want not_found naming the ids that do exist", err)
	}
}

func TestChartList(t *testing.T) {
	_, svc := standard(t)
	addOne(t, svc, "Units")
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Charts) != 1 {
		t.Fatalf("charts = %d, want 1", len(res.Charts))
	}
	text := res.Render()
	for _, want := range []string{`"Units"`, "column", "E2", "A1:A6"} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing does not mention %q:\n%s", want, text)
		}
	}
}

func TestChartListEmpty(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList, Sheet: sheetstest.SecondSheet,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Render(), "No charts or slicers") {
		t.Errorf("empty listing reads:\n%s", res.Render())
	}
}

// TestChartLosesItsSeriesToAColumnDelete is the finding the whole phase
// turns on: deleting a charted column leaves the chart in place with
// nothing to draw, and the API says nothing at all.
func TestChartLosesItsSeriesToAColumnDelete(t *testing.T) {
	_, svc := destructive(t)
	id := addOne(t, svc, "Units")

	// Refused first, and the refusal has to name the chart: a caller who
	// confirms should know what they are agreeing to.
	_, err := svc.EditDimensions(context.Background(), service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "columns", Band: "B:B",
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went ahead")
	}
	if !strings.Contains(err.Error(), `"Units"`) || !strings.Contains(err.Error(), "nothing to draw") {
		t.Errorf("the refusal does not name the chart:\n%v", err)
	}

	res, err := svc.EditDimensions(context.Background(), service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "columns", Band: "B:B", Confirm: true,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(res.Charts) != 1 || res.Charts[0].Title != "Units" {
		t.Errorf("charts_affected = %+v, want the chart named", res.Charts)
	}
	if res.Charts[0].Lost != 1 || res.Charts[0].Total != 1 {
		t.Errorf("the loss is %+v, want its one series counted", res.Charts[0])
	}
	if !strings.Contains(res.Render(), "still there") {
		t.Errorf("the result does not say the chart survived and stopped drawing:\n%s", res.Render())
	}

	// And the listing reports it, which is the only place a caller can
	// find out later.
	after, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after.Charts) != 1 || after.Charts[0].ID != id || !after.Charts[0].Broken {
		t.Errorf("the listing does not report the chart as broken: %+v", after.Charts)
	}
	if !strings.Contains(after.Render(), "no series left") {
		t.Errorf("the listing does not explain it:\n%s", after.Render())
	}
}

func TestChartDryRun(t *testing.T) {
	srv, svc := standard(t)
	for _, tc := range []struct {
		name string
		req  service.ChartRequest
	}{
		{"add", service.ChartRequest{Action: service.ChartAdd, ChartType: "bar", Title: "Preview",
			Domain: "A1:A6", Series: []string{"B1:B6"}, Anchor: "E2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv.Reset()
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			tc.req.DryRun = true
			res, err := svc.ManageChart(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
				t.Errorf("dry run result:\n%s", res.Render())
			}
			for _, c := range srv.Calls() {
				if c.Op == "spreadsheets.batchUpdate" {
					t.Error("a dry run sent a write")
				}
			}
		})
	}
}

func TestChartUnknownAction(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: "rotate",
	})
	if err == nil || !strings.Contains(err.Error(), "rotate") {
		t.Fatalf("error = %v, want the action named", err)
	}
}

func TestSlicer(t *testing.T) {
	_, svc := standard(t)
	added, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, Slicer: true, Title: "By region",
		Range: "A1:C6", Column: "A", Anchor: "E2",
	})
	if err != nil {
		t.Fatalf("add slicer: %v", err)
	}
	if !strings.Contains(added.Render(), "slicer") {
		t.Errorf("the result calls it something else:\n%s", added.Render())
	}

	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, Slicer: true, ID: added.ID, Title: "By area", Column: "B",
	}); err != nil {
		t.Fatalf("update slicer: %v", err)
	}

	listed, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Charts) != 1 || listed.Charts[0].Kind != "slicer" || listed.Charts[0].Title != "By area" {
		t.Errorf("listing = %+v", listed.Charts)
	}

	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartDelete, Slicer: true, ID: added.ID,
	}); err != nil {
		t.Fatalf("delete slicer: %v", err)
	}
}

func TestSlicerRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.ChartRequest
		want string
	}{
		{"no range", service.ChartRequest{Action: service.ChartAdd, Slicer: true, Column: "A", Anchor: "E2"}, "range"},
		{"no column", service.ChartRequest{Action: service.ChartAdd, Slicer: true, Range: "A1:C6", Anchor: "E2"}, "column"},
		{"a column outside the range", service.ChartRequest{Action: service.ChartAdd, Slicer: true,
			Range: "A1:C6", Column: "F", Anchor: "E2"}, "outside"},
		{"not a column at all", service.ChartRequest{Action: service.ChartAdd, Slicer: true,
			Range: "A1:C6", Column: "3", Anchor: "E2"}, "column letter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.FirstSheet
			_, err := svc.ManageChart(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestARefusalCountsTheSeriesItWouldTake is the live run's finding. A
// two-series chart under a one-column delete loses one series and keeps
// the other, and the first version of this refusal told the caller the
// chart would be left with nothing to draw. A refusal that overstates is
// one somebody learns to skip.
func TestARefusalCountsTheSeriesItWouldTake(t *testing.T) {
	_, svc := destructive(t)
	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "column", Title: "Two series",
		Domain: "A1:A6", Series: []string{"B1:B6", "C1:C6"}, Anchor: "E2",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	_, err := svc.EditDimensions(context.Background(), service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "columns", Band: "B:B",
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went ahead")
	}
	if strings.Contains(err.Error(), "nothing to draw") {
		t.Errorf("the refusal claims the chart would be left with nothing, and it keeps a series:\n%v", err)
	}
	if !strings.Contains(err.Error(), "lose 1 of its 2 series") {
		t.Errorf("the refusal does not count what goes:\n%v", err)
	}
}

// TestStackedIsRefusedOnAChartTypeThatCannotStack is the other live
// finding: Google refuses the whole request when stackedType reaches a
// LINE chart, so the pairing is refused here where the message can name
// the types that do take it.
func TestStackedIsRefusedOnAChartTypeThatCannotStack(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "line", Title: "Stacked line",
		Domain: "A1:A6", Series: []string{"B1:B6"}, Anchor: "E2", Stacked: "stacked",
	})
	if err == nil {
		t.Fatal("a stacked line chart was accepted")
	}
	if !strings.Contains(err.Error(), "cannot be stacked") || !strings.Contains(err.Error(), "column") {
		t.Errorf("the refusal does not say which types take it:\n%v", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("the request was sent")
		}
	}

	// And on the update path, checked against the type the chart will
	// have rather than the one it has: turning a line chart into a
	// stacked column chart in one call is a request that works.
	id := addOne(t, svc, "Units")
	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: id, ChartType: "line", Stacked: "stacked",
	}); err == nil {
		t.Error("a line chart was given a stacked type by an update")
	}
	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: id, ChartType: "bar", Stacked: "percent",
	}); err != nil {
		t.Errorf("a bar chart was refused a stacked type: %v", err)
	}
}

// TestAPieChartCanBeUpdated is finding 5 of the phase's review pass. A
// pie is one of the two kinds this server builds, and the update path
// refused every pie with "this server can retitle, move and delete but
// not rebuild" — a sentence that was true for a treemap and false for
// the chart the tool had just made.
func TestAPieChartCanBeUpdated(t *testing.T) {
	_, svc := standard(t)
	added, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "pie", Title: "Share",
		Domain: "A1:A6", Series: []string{"B1:B6"}, Anchor: "E2",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: added.ID, Legend: "top", Series: []string{"C1:C6"},
	})
	if err != nil {
		t.Fatalf("update a pie: %v", err)
	}
	for _, want := range []string{"legend", "series"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the result does not name %q as changed:\n%s", want, res.Render())
		}
	}
	// And the arguments a pie has no room for are refused by name,
	// rather than silently ignored or blamed on the chart kind.
	for _, tc := range []struct{ req service.ChartRequest }{
		{service.ChartRequest{Stacked: "stacked"}},
		{service.ChartRequest{AxisTitle: "Units"}},
		{service.ChartRequest{Headers: 1}},
	} {
		req := tc.req
		req.Spreadsheet, req.Sheet = sheetstest.FixtureID, sheetstest.FirstSheet
		req.Action, req.ID = service.ChartUpdate, added.ID
		_, err := svc.ManageChart(context.Background(), req)
		if err == nil {
			t.Errorf("a pie accepted %+v", tc.req)
			continue
		}
		if !strings.Contains(err.Error(), "a pie chart has no") {
			t.Errorf("error = %v, want the argument named", err)
		}
	}
}

// TestUpdateAppliesHeadersAndAxisTitle is finding 6: both were read on
// add and silently dropped on update, so a call passing only one of them
// was told there was nothing to change.
func TestUpdateAppliesHeadersAndAxisTitle(t *testing.T) {
	_, svc := standard(t)
	id := addOne(t, svc, "Units")
	res, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: id, Headers: 1, AxisTitle: "Units sold",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, want := range []string{"headers", "axis_title"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the result does not name %q as changed:\n%s", want, res.Render())
		}
	}
}

// TestUpdateResolvesRangesAgainstASheetThatHasCells is finding 4. A
// chart on its own sheet sits on a sheetType OBJECT sheet with no cells,
// and an unqualified range in an update was being resolved against it.
func TestUpdateResolvesRangesAgainstASheetThatHasCells(t *testing.T) {
	_, svc := standard(t)
	added, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartAdd, ChartType: "column", Title: "On its own",
		Domain: "A1:A6", Series: []string{"B1:B6"}, NewSheet: true,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	_, err = svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartUpdate,
		ID: added.ID, Series: []string{"C1:C6"},
	})
	if err == nil {
		t.Fatal("an unqualified range resolved against a sheet with no cells")
	}
	if !strings.Contains(err.Error(), "holds no cells") {
		t.Errorf("error = %v, want it to say why", err)
	}
	// Naming the sheet is the way through, and so is qualifying the
	// range — both say which cells are meant.
	if _, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.ChartUpdate, ID: added.ID, Series: []string{"C1:C6"},
	}); err != nil {
		t.Errorf("naming the sheet did not help: %v", err)
	}
}

// TestChartListResolvesItsSheet is finding 2: the listing compared the
// sheet argument with the title, so a numeric sheet id — which the tool
// offers — matched nothing, and a typo came back as "no charts" instead
// of a refusal naming the sheets that exist.
func TestChartListResolvesItsSheet(t *testing.T) {
	_, svc := standard(t)
	addOne(t, svc, "Units")
	byID, err := svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList, Sheet: "0",
	})
	if err != nil {
		t.Fatalf("list by sheet id: %v", err)
	}
	if len(byID.Charts) != 1 {
		t.Errorf("a numeric sheet id found %d chart(s)", len(byID.Charts))
	}
	_, err = svc.ManageChart(context.Background(), service.ChartRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.ChartList, Sheet: "Vandeel",
	})
	if err == nil || !strings.Contains(err.Error(), "[not_found]") {
		t.Errorf("a misspelled sheet = %v, want not_found naming the sheets that exist", err)
	}
}
