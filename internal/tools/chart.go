package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// ManageChartInput is what manage_chart takes.
type ManageChartInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id. For add it is the sheet the chart sits on; for list it narrows the listing"`
	Action      string `json:"action" jsonschema:"add, update, move, delete or list"`
	Slicer      bool   `json:"slicer,omitempty" jsonschema:"act on a slicer rather than a chart. A slicer is a filter control over a block of data, and it shares delete and move with charts"`
	ID          int    `json:"id,omitempty" jsonschema:"which chart or slicer, for update, move and delete. list reports the ids"`

	Title     string   `json:"title,omitempty" jsonschema:"the chart's title, or the slicer's"`
	Subtitle  string   `json:"subtitle,omitempty" jsonschema:"the line under the title"`
	ChartType string   `json:"chart_type,omitempty" jsonschema:"for add: column, bar, line, area, scatter, stepped_area, pie or doughnut"`
	Domain    string   `json:"domain,omitempty" jsonschema:"the labels along the category axis, as one range: a single column such as A1:A20, or a single row"`
	Series    []string `json:"series,omitempty" jsonschema:"the values to draw, one range per series, each a single column or a single row. B1:B20 draws one line; [\"B1:B20\", \"C1:C20\"] draws two"`
	Headers   int      `json:"headers,omitempty" jsonschema:"how many rows of the ranges are headings rather than data. Left out, Google guesses"`
	Legend    string   `json:"legend,omitempty" jsonschema:"where the key goes: bottom, left, right, top or none"`
	Stacked   string   `json:"stacked,omitempty" jsonschema:"for bar, column, area and stepped_area: none, stacked or percent"`
	AxisTitle string   `json:"axis_title,omitempty" jsonschema:"a title for the value axis"`

	Anchor   string `json:"anchor,omitempty" jsonschema:"the cell the top-left corner sits on, such as E2. Charts float above the grid rather than filling cells, so this does not overwrite anything"`
	NewSheet bool   `json:"new_sheet,omitempty" jsonschema:"for add: give the chart a sheet of its own instead of an anchor. That sheet has no grid at all and is deleted with delete_sheet"`
	Width    int    `json:"width,omitempty" jsonschema:"width in pixels; Google's default is 600"`
	Height   int    `json:"height,omitempty" jsonschema:"height in pixels; Google's default is 371"`

	Range  string `json:"range,omitempty" jsonschema:"for a slicer: the block of data it filters"`
	Column string `json:"column,omitempty" jsonschema:"for a slicer: which column it filters on, as a letter such as B"`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

func registerChart(s *mcp.Server, d Deps) {
	add(s, d, Def[ManageChartInput, *service.ChartResult]{
		Name: "manage_chart",
		Description: "Add, update, move, delete or list the charts and slicers on a spreadsheet. " +
			"A chart floats above the grid rather than filling cells, so adding one overwrites nothing; anchor is " +
			"the cell its top-left corner sits on. Its data is named in A1, one range per series. " +
			"update changes a chart in place, and it reads the whole chart first and sends it back with your change " +
			"applied — Google's own update replaces a chart's definition rather than merging into it, so anything " +
			"this server did not send would be lost. A chart kind this server cannot build can still be retitled, " +
			"moved and deleted, and says so rather than being rebuilt as something else. " +
			"list is worth calling first: it reports each chart's id, what it reads, and whether it has lost its " +
			"series, which is what deleting a charted column does and what nothing in Sheets tells you.",
		Kind: Write,
		Handle: func(ctx context.Context, in ManageChartInput) (*service.ChartResult, error) {
			return d.Service.ManageChart(ctx, service.ChartRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Action: in.Action,
				Slicer: in.Slicer, ID: in.ID,
				Title: in.Title, Subtitle: in.Subtitle, ChartType: in.ChartType,
				Domain: in.Domain, Series: in.Series, Headers: in.Headers,
				Legend: in.Legend, Stacked: in.Stacked, AxisTitle: in.AxisTitle,
				Anchor: in.Anchor, NewSheet: in.NewSheet, Width: in.Width, Height: in.Height,
				Range: in.Range, Column: in.Column,
				DryRun: in.DryRun,
			})
		},
	})
}

// ManagePivotTableInput is what manage_pivot_table takes.
type ManagePivotTableInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Action      string `json:"action" jsonschema:"add, update, delete or list"`
	Anchor      string `json:"anchor,omitempty" jsonschema:"the cell the pivot table's top-left corner sits on, such as F1. A pivot table has no id in the API, so this is its whole name: update and delete take the same anchor add was given"`

	Source       string   `json:"source,omitempty" jsonschema:"the block of data to summarise, such as A1:C200. Its first row is read as headings"`
	GroupRows    []string `json:"group_rows,omitempty" jsonschema:"the column(s) whose values become the rows of the summary, each a column letter inside source or a heading from its first row"`
	GroupColumns []string `json:"group_columns,omitempty" jsonschema:"the column(s) whose values become the columns of the summary, named the same way"`
	Values       []string `json:"values,omitempty" jsonschema:"what to work out for each group, as \"B sum\" or \"Units sum\", one per entry. Add \"as Total units\" to name the column. The summaries are sum, count, count_numbers, count_unique, average, max, min, median, product, stdev and var"`
	Layout       string   `json:"layout,omitempty" jsonschema:"horizontal puts several values side by side, vertical stacks them"`

	Range  string `json:"range,omitempty" jsonschema:"for list: where to look. A pivot table has no index in the API, so a listing reads cells; the default is the whole sheet"`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

func registerPivot(s *mcp.Server, d Deps) {
	add(s, d, Def[ManagePivotTableInput, *service.PivotResult]{
		Name: "manage_pivot_table",
		Description: "Add, update, delete or list pivot tables: the summary of a block of data, grouped by one or " +
			"more of its columns. " +
			"Columns are named in A1 or by their heading, never by counting. " +
			"A pivot table is anchored at one cell and has no id, so that cell is its name for every later call. " +
			"How far it reaches is not something you choose: the rectangle is computed from the data and changes " +
			"as the data does, so every result reports what it covers right now. " +
			"Anchor it clear of the source and of anything else — a write into the rectangle it draws stops the " +
			"pivot table drawing until that cell is cleared again, and this server refuses an anchor inside the " +
			"source, which Google accepts and turns into a circular reference.",
		Kind: Write,
		Handle: func(ctx context.Context, in ManagePivotTableInput) (*service.PivotResult, error) {
			return d.Service.ManagePivotTable(ctx, service.PivotRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Action: in.Action, Anchor: in.Anchor,
				Source: in.Source, Rows: in.GroupRows, Columns: in.GroupColumns,
				Values: in.Values, Layout: in.Layout, Range: in.Range, DryRun: in.DryRun,
			})
		},
	})
}
