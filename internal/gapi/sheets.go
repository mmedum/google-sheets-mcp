package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// CardFields is the field mask behind the spreadsheet card: everything
// get_spreadsheet reports and no cell data at all, so the call costs the
// same on a spreadsheet of ten cells and one of ten million.
const CardFields = "spreadsheetId,spreadsheetUrl," +
	"properties(title,locale,timeZone,autoRecalc)," +
	"sheets(properties(sheetId,title,index,sheetType,hidden,rightToLeft,gridProperties,tabColorStyle)," +
	"merges,protectedRanges(protectedRangeId,range,namedRangeId,description,warningOnly,requestingUserCanEdit,editors)," +
	"filterViews(filterViewId,title,range),tables(tableId,name,range,columnProperties)," +
	"charts(chartId),bandedRanges(bandedRangeId,range))," +
	"namedRanges"

// GridFields is the field mask behind a read of cells. It asks for what
// a values read cannot show: the entered value under a formatted one,
// the note, the validation rule.
//
// userEnteredValue and effectiveValue are both here because a formula
// and its result render identically, and the difference between them is
// what the write guard is built on.
const GridFields = "spreadsheetId," +
	"sheets(properties(sheetId,title,index,gridProperties),merges," +
	"protectedRanges(protectedRangeId,range,description,warningOnly,requestingUserCanEdit)," +
	"data(startRow,startColumn,rowData(values(userEnteredValue,effectiveValue,formattedValue,note,dataValidation,hyperlink))))"

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
