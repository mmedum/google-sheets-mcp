package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Value input options, as the API spells them. The server never
// substitutes one for the other: `typed` is what a person typing would
// get and `literal` is what they typed, and which one is right depends
// on the data (§17.3).
const (
	InputUserEntered = "USER_ENTERED"
	InputRaw         = "RAW"
)

// Insert options for an append.
const (
	InsertRows      = "INSERT_ROWS"
	InsertOverwrite = "OVERWRITE"
)

// WriteOptions are what every value write carries.
type WriteOptions struct {
	// Input is InputUserEntered or InputRaw.
	Input string
	// Insert applies to an append only.
	Insert string
}

// readBack is the pair of parameters behind §4.4: ask for the stored
// values in the same response, rendered as formulas, so the coercion
// diff costs no extra request and sees what Google actually kept rather
// than what it displays.
func readBack(v url.Values) {
	v.Set("includeValuesInResponse", "true")
	v.Set("responseValueRenderOption", RenderFormula)
}

// UpdateValues writes one rectangle through values.update.
//
// A PUT, so it may be retried: sending the same rectangle twice leaves
// the same values. That is the whole reason a single-range write does
// not go through values.batchUpdate, which is a POST and would have to
// report an ambiguous outcome on a failure this can simply repeat.
func (c *Client) UpdateValues(ctx context.Context, id, a1Range string, values [][]any, o WriteOptions) (*gsheets.UpdateValuesResponse, error) {
	if o.Input == "" {
		return nil, fmt.Errorf("%w: a value write needs an explicit input option", ErrInvalid)
	}
	body, err := json.Marshal(&gsheets.ValueRange{Range: a1Range, MajorDimension: "ROWS", Values: values})
	if err != nil {
		return nil, fmt.Errorf("%w: the values could not be encoded", ErrInvalid)
	}
	v := url.Values{}
	v.Set("valueInputOption", o.Input)
	readBack(v)
	out, err := c.do(ctx, request{
		op:          "values.update",
		spreadsheet: id,
		method:      http.MethodPut,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1Range) + "?" + v.Encode(),
		body:        body,
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.UpdateValuesResponse]("values.update", out)
}

// AppendValues adds rows after the block Google finds in the range.
//
// A POST that is never retried: repeating it appends the rows a second
// time, and an ambiguous failure is reported as one (hard rule 8).
func (c *Client) AppendValues(ctx context.Context, id, a1Range string, values [][]any, o WriteOptions) (*gsheets.AppendValuesResponse, error) {
	if o.Input == "" || o.Insert == "" {
		return nil, fmt.Errorf("%w: an append needs an explicit input and insert option", ErrInvalid)
	}
	body, err := json.Marshal(&gsheets.ValueRange{Range: a1Range, MajorDimension: "ROWS", Values: values})
	if err != nil {
		return nil, fmt.Errorf("%w: the values could not be encoded", ErrInvalid)
	}
	v := url.Values{}
	v.Set("valueInputOption", o.Input)
	v.Set("insertDataOption", o.Insert)
	readBack(v)
	out, err := c.do(ctx, request{
		op:          "values.append",
		spreadsheet: id,
		method:      http.MethodPost,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1Range) + ":append?" + v.Encode(),
		body:        body,
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.AppendValuesResponse]("values.append", out)
}

// ClearValues empties a rectangle and leaves its formatting.
func (c *Client) ClearValues(ctx context.Context, id, a1Range string) (*gsheets.ClearValuesResponse, error) {
	out, err := c.do(ctx, request{
		op:          "values.clear",
		spreadsheet: id,
		method:      http.MethodPost,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1Range) + ":clear",
		body:        []byte("{}"),
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.ClearValuesResponse]("values.clear", out)
}

// CreateSpreadsheet makes a new spreadsheet in My Drive's root.
//
// Moving it elsewhere is a Drive operation this server does not offer,
// and the tool description says so.
func (c *Client) CreateSpreadsheet(ctx context.Context, in *gsheets.NewSpreadsheet) (*gsheets.Spreadsheet, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("%w: the spreadsheet could not be encoded", ErrInvalid)
	}
	out, err := c.do(ctx, request{
		op:     "spreadsheets.create",
		method: http.MethodPost,
		url:    c.sheets + "/spreadsheets",
		body:   body,
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.Spreadsheet]("spreadsheets.create", out)
}

// BatchUpdate applies a batch of typed requests atomically.
//
// The discovery document is explicit that nothing is applied if any
// request is invalid, and that the whole batch counts once against
// quota. So an ops-shaped tool compiles to one of these rather than to
// several calls, and a partial application cannot happen.
func (c *Client) BatchUpdate(ctx context.Context, id string, in *gsheets.BatchUpdateSpreadsheetRequest) (*gsheets.BatchUpdateSpreadsheetResponse, error) {
	if in == nil || len(in.Requests) == 0 {
		return nil, fmt.Errorf("%w: a batchUpdate with no requests changes nothing", ErrInvalid)
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("%w: the batch could not be encoded", ErrInvalid)
	}
	out, err := c.do(ctx, request{
		op:          "spreadsheets.batchUpdate",
		spreadsheet: id,
		method:      http.MethodPost,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + ":batchUpdate",
		body:        body,
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.BatchUpdateSpreadsheetResponse]("spreadsheets.batchUpdate", out)
}

// CopySheetTo copies one sheet into another spreadsheet.
func (c *Client) CopySheetTo(ctx context.Context, id string, sheetID int, destination string) (*gsheets.SheetProperties, error) {
	body, err := json.Marshal(&gsheets.CopySheetToAnotherSpreadsheetRequest{DestinationSpreadsheetID: destination})
	if err != nil {
		return nil, fmt.Errorf("%w: the request could not be encoded", ErrInvalid)
	}
	out, err := c.do(ctx, request{
		op:          "sheets.copyTo",
		spreadsheet: id,
		method:      http.MethodPost,
		url: c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/sheets/" +
			strconv.Itoa(sheetID) + ":copyTo",
		body: body,
	})
	if err != nil {
		return nil, err
	}
	return decode[gsheets.SheetProperties]("sheets.copyTo", out)
}

// decode turns a response body into a wire type, naming the operation
// rather than quoting the body: a Sheets response carries cell values,
// and an error message is not the place for them.
func decode[T any](op string, body []byte) (*T, error) {
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: %s returned something this server cannot read", ErrUnavailable, op)
	}
	return &out, nil
}
