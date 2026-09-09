package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// CardFields is the field mask behind the spreadsheet card: everything
// get_spreadsheet reports and no cell data at all, so the call costs the
// same on a spreadsheet of ten cells and one of ten million.
// spreadsheetUrl is deliberately absent. Google's own URL carries an
// `?ouid=` query holding the signed-in account's obfuscated id, which
// nothing needs to open the spreadsheet — SpreadsheetURL builds a link
// that works without it. Found by reading a live transcript, where the
// redactor had been quietly catching it in every card.
const CardFields = "spreadsheetId," +
	"properties(title,locale,timeZone,autoRecalc)," +
	"sheets(properties(sheetId,title,index,sheetType,hidden,rightToLeft,gridProperties,tabColorStyle)," +
	"merges,protectedRanges(protectedRangeId,range,namedRangeId,description,warningOnly,requestingUserCanEdit,editors)," +
	"filterViews(filterViewId,title,range),tables(tableId,name,range,columnProperties)," +
	"charts(chartId,spec(title)),slicers(slicerId,spec(title))," +
	"bandedRanges(bandedRangeId,range),conditionalFormats(ranges))," +
	"dataSources(dataSourceId,sheetId,spec(bigQuery(projectId)))," +
	"namedRanges"

// GridFields is the field mask behind a read of cells. It asks for what
// a values read cannot show: the entered value under a formatted one,
// the note, the validation rule.
//
// userEnteredValue and effectiveValue are both here because a formula
// and its result render identically, and the difference between them is
// what the write guard is built on.
const GridFields = sheetHead +
	"data(startRow,startColumn,rowData(values(userEnteredValue,effectiveValue,formattedValue,note,dataValidation," +
	"hyperlink,pivotTable(source)))))"

// sheetHead is what every mask that reads cells asks for around them:
// the sheet's identity and size, its merges, and its protected ranges.
//
// Written out three times before this. A field the guard needs, added to
// two of the three, produces a zero value rather than an error —
// CheckDestination would simply stop seeing protections on the path that
// was missed.
const sheetHead = "spreadsheetId," +
	"sheets(properties(sheetId,title,index,gridProperties),merges," +
	"protectedRanges(protectedRangeId,range,description,warningOnly,requestingUserCanEdit),"

// ChartFields is the field mask behind manage_chart.
//
// The whole spec, because an update replaces it whole and has to send
// back what it read (spike L). It is deliberately not the card's mask:
// live, one chart's full spec is about 2 KB against the 72 bytes a title
// costs, so the card carries the title and this carries the rest.
const ChartFields = "spreadsheetId,sheets(properties(sheetId,title),charts,slicers)"

// PivotFields is the field mask behind manage_pivot_table.
//
// A pivot table has no index in the API: the only way to find one is to
// read cells and look for the field. So this asks for the pivot and
// nothing else — no values, no formats — over a range the caller named.
const PivotFields = "spreadsheetId,sheets(properties(sheetId,title)," +
	"data(startRow,startColumn,rowData(values(pivotTable))))"

// PivotExtentFields is the field mask behind measuring what a pivot
// draws: the two value fields and nothing else.
//
// Its own mask rather than GridFields, which asks for six more fields
// per cell. The measurement reads only whether a cell has an effective
// value and no entered one, over a rectangle up to the cell budget — so
// every other field is fetched and dropped on the largest read in the
// pivot path.
const PivotExtentFields = "spreadsheetId,sheets(properties(sheetId)," +
	"data(startRow,startColumn,rowData(values(userEnteredValue,effectiveValue))))"

// FormatFields is the field mask behind read_formatting: what a cell
// looks like, and the things attached to the sheet that decide it.
//
// No values at all. A formatting read answers a different question from
// a values read, and asking for both would double a 5 000-cell response
// to carry data the summary never prints.
//
// The entered format only. The effective format is what applies once
// Google has merged the sheet's defaults in, and it was asked for here
// until nothing turned out to read it: every answer this server gives is
// about what somebody set, so the second format doubled the per-cell
// response of the largest read there is for no reader at all.
const FormatFields = sheetHead +
	"bandedRanges(bandedRangeId,range,rowProperties,columnProperties),conditionalFormats," +
	"data(startRow,startColumn,rowData(values(userEnteredFormat,note,dataValidation))))"

// FormatTargetFields is the field mask behind a formatting write's own
// read of its target.
//
// Values as well as formats, because the guard needs both: a merge
// discards every value but the top-left one, and clearing a format takes
// only what the cell was explicitly given.
//
// And the pivot, for the reason GridFields carries it. Sheets refuses a
// merge over any cell of a pivot table outright (spike Q), so the guard
// has to see the anchor to say which table it is refusing for — a mask
// without it made format_cells the one write path that could not name
// what it had run into.
const FormatTargetFields = sheetHead +
	"data(startRow,startColumn,rowData(values(userEnteredValue,effectiveValue,userEnteredFormat,note," +
	"dataValidation,pivotTable(source)))))"

// RuleFields is the field mask for reading a sheet's conditional format
// rules and nothing else.
//
// Its own mask rather than FormatFields, and the difference matters:
// `includeGridData` is ignored when a field mask is set, so a mask that
// names `data(...)` returns cells whether or not the option asked for
// them. FormatFields names them, so using it without a range would have
// fetched the entered and effective format of every cell of every sheet
// — the whole-spreadsheet grid read §4.6 forbids, walking straight past
// the guard in GetSpreadsheet, which can only see the option.
const RuleFields = "spreadsheetId,sheets(properties(sheetId),conditionalFormats)"

// GetOptions select what spreadsheets.get returns.
type GetOptions struct {
	// Fields is the field mask. It is never empty in this server: an
	// unmasked get on a large spreadsheet returns the whole grid.
	Fields string
	// Ranges scope the grid data. Every one is a finite A1 range,
	// resolved before the call.
	Ranges []string
	// IncludeGridData asks for cells. Without Ranges it would ask for
	// every cell in the spreadsheet, so the two travel together.
	IncludeGridData bool
}

// GetSpreadsheet reads a spreadsheet's metadata, and its cells when the
// options ask for a range.
func (c *Client) GetSpreadsheet(ctx context.Context, id string, o GetOptions) (*gsheets.Spreadsheet, error) {
	if o.Fields == "" {
		return nil, fmt.Errorf("%w: a spreadsheets.get without a field mask would fetch the whole grid", ErrInvalid)
	}
	if o.IncludeGridData && len(o.Ranges) == 0 {
		return nil, fmt.Errorf("%w: grid data was asked for without a range, which is the whole spreadsheet", ErrInvalid)
	}
	// The option is not the only way to ask for cells. `includeGridData`
	// is ignored when a field mask is set, so a mask naming `data(` is a
	// grid read however the option is set — and without a range that is
	// every cell of every sheet. The guard above could not see it: it
	// reads the option, and the mask is what decides.
	if len(o.Ranges) == 0 && strings.Contains(o.Fields, "data(") {
		return nil, fmt.Errorf("%w: the field mask asks for cell data and no range was given, which is the whole spreadsheet", ErrInvalid)
	}
	v := url.Values{}
	v.Set("fields", o.Fields)
	for _, r := range o.Ranges {
		v.Add("ranges", r)
	}
	if o.IncludeGridData {
		v.Set("includeGridData", "true")
	}
	body, err := c.do(ctx, request{
		op:          "spreadsheets.get",
		spreadsheet: id,
		method:      http.MethodGet,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "?" + v.Encode(),
	})
	if err != nil {
		return nil, err
	}
	var s gsheets.Spreadsheet
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("%w: spreadsheets.get returned something this server cannot read", ErrUnavailable)
	}
	return &s, nil
}

// ValueRender options, as the API spells them.
const (
	RenderFormatted   = "FORMATTED_VALUE"
	RenderUnformatted = "UNFORMATTED_VALUE"
	RenderFormula     = "FORMULA"
)

// ValueOptions select how values.get renders what it returns.
type ValueOptions struct {
	// Render is one of the Render* constants. Empty means the API
	// default, FORMATTED_VALUE.
	Render string
	// MajorDimension is ROWS or COLUMNS. Empty means ROWS.
	MajorDimension string
}

func (o ValueOptions) apply(v url.Values) {
	if o.Render != "" {
		v.Set("valueRenderOption", o.Render)
	}
	if o.MajorDimension != "" {
		v.Set("majorDimension", o.MajorDimension)
	}
	// A serial number is unreadable and a formatted date is not
	// computable; the string is what a person sees, which is what a
	// model should be shown alongside the address.
	v.Set("dateTimeRenderOption", "FORMATTED_STRING")
}

// GetValues reads one range through spreadsheets.values.get.
func (c *Client) GetValues(ctx context.Context, id, a1Range string, o ValueOptions) (*gsheets.ValueRange, error) {
	v := url.Values{}
	o.apply(v)
	body, err := c.do(ctx, request{
		op:          "values.get",
		spreadsheet: id,
		method:      http.MethodGet,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1Range) + "?" + v.Encode(),
	})
	if err != nil {
		return nil, err
	}
	var out gsheets.ValueRange
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: values.get returned something this server cannot read", ErrUnavailable)
	}
	return &out, nil
}

// BatchGetValues reads several ranges in one request. A batch counts
// once against quota, so several ranges cost what one does.
func (c *Client) BatchGetValues(ctx context.Context, id string, ranges []string, o ValueOptions) (*gsheets.BatchGetValuesResponse, error) {
	if len(ranges) == 0 {
		return nil, fmt.Errorf("%w: values.batchGet needs at least one range", ErrInvalid)
	}
	v := url.Values{}
	o.apply(v)
	for _, r := range ranges {
		v.Add("ranges", r)
	}
	body, err := c.do(ctx, request{
		op:          "values.batchGet",
		spreadsheet: id,
		method:      http.MethodGet,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values:batchGet?" + v.Encode(),
	})
	if err != nil {
		return nil, err
	}
	var out gsheets.BatchGetValuesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: values.batchGet returned something this server cannot read", ErrUnavailable)
	}
	return &out, nil
}

// SpreadsheetURL is the link a person opens. Built here so one place
// knows the shape.
func SpreadsheetURL(id string) string {
	return "https://docs.google.com/spreadsheets/d/" + id + "/edit"
}
