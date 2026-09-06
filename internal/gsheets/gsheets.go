// Package gsheets holds hand-written wire types for the Google Sheets
// API v4 fields this server reads and writes.
//
// The generated client (google.golang.org/api) is deliberately not a
// dependency: it drags gRPC, OpenTelemetry and the cloud auth stack into
// a binary that needs JSON over HTTP. These structs cover the fields the
// server actually asks for, and nothing else, so a field appearing here
// means some code reads it.
//
// The package imports nothing and knows nothing about HTTP.
package gsheets

import "encoding/json"

// GridRange is a range on one sheet in the API's own coordinates:
// zero-based and half-open, with a missing index meaning unbounded on
// that side. A1 is one-based and inclusive, so every conversion between
// the two lives in internal/a1 and nowhere else.
type GridRange struct {
	SheetID          int  `json:"sheetId"`
	StartRowIndex    *int `json:"startRowIndex,omitempty"`
	EndRowIndex      *int `json:"endRowIndex,omitempty"`
	StartColumnIndex *int `json:"startColumnIndex,omitempty"`
	EndColumnIndex   *int `json:"endColumnIndex,omitempty"`
}

// Spreadsheet is the spreadsheets.get response.
type Spreadsheet struct {
	SpreadsheetID  string                 `json:"spreadsheetId,omitempty"`
	Properties     *SpreadsheetProperties `json:"properties,omitempty"`
	Sheets         []*Sheet               `json:"sheets,omitempty"`
	NamedRanges    []*NamedRange          `json:"namedRanges,omitempty"`
	SpreadsheetURL string                 `json:"spreadsheetUrl,omitempty"`
}

// SpreadsheetProperties are the file-wide settings.
type SpreadsheetProperties struct {
	Title      string `json:"title,omitempty"`
	Locale     string `json:"locale,omitempty"`
	AutoRecalc string `json:"autoRecalc,omitempty"`
	TimeZone   string `json:"timeZone,omitempty"`
}

// Sheet is one tab.
type Sheet struct {
	Properties         *SheetProperties  `json:"properties,omitempty"`
	Data               []*GridData       `json:"data,omitempty"`
	Merges             []*GridRange      `json:"merges,omitempty"`
	ProtectedRanges    []*ProtectedRange `json:"protectedRanges,omitempty"`
	FilterViews        []*FilterView     `json:"filterViews,omitempty"`
	Tables             []*Table          `json:"tables,omitempty"`
	Charts             []*EmbeddedChart  `json:"charts,omitempty"`
	BandedRanges       []*BandedRange    `json:"bandedRanges,omitempty"`
	ConditionalFormats []json.RawMessage `json:"conditionalFormats,omitempty"`
}

// SheetProperties describe one tab.
type SheetProperties struct {
	SheetID        int             `json:"sheetId"`
	Title          string          `json:"title,omitempty"`
	Index          int             `json:"index"`
	SheetType      string          `json:"sheetType,omitempty"`
	GridProperties *GridProperties `json:"gridProperties,omitempty"`
	Hidden         bool            `json:"hidden,omitempty"`
	RightToLeft    bool            `json:"rightToLeft,omitempty"`
	TabColorStyle  *ColorStyle     `json:"tabColorStyle,omitempty"`
}

// GridProperties are a grid sheet's allocated size and frozen bands.
// RowCount and ColumnCount are what the sheet has room for, not how far
// the data reaches.
type GridProperties struct {
	RowCount          int  `json:"rowCount,omitempty"`
	ColumnCount       int  `json:"columnCount,omitempty"`
	FrozenRowCount    int  `json:"frozenRowCount,omitempty"`
	FrozenColumnCount int  `json:"frozenColumnCount,omitempty"`
	HideGridlines     bool `json:"hideGridlines,omitempty"`
}

// GridData is a rectangle of cells. StartRow and StartColumn are
// zero-based offsets into the sheet.
type GridData struct {
	StartRow    int        `json:"startRow,omitempty"`
	StartColumn int        `json:"startColumn,omitempty"`
	RowData     []*RowData `json:"rowData,omitempty"`
}

// RowData is one row of a GridData. Trailing empty cells are omitted by
// the API, so a row may be shorter than the range asked for.
type RowData struct {
	Values []*CellData `json:"values,omitempty"`
}

// CellData is everything one cell carries. A formula and its result
// render identically in a values read, which is why the write guard
// reads UserEnteredValue rather than the formatted string.
type CellData struct {
	UserEnteredValue  *ExtendedValue      `json:"userEnteredValue,omitempty"`
	EffectiveValue    *ExtendedValue      `json:"effectiveValue,omitempty"`
	FormattedValue    string              `json:"formattedValue,omitempty"`
	UserEnteredFormat *CellFormat         `json:"userEnteredFormat,omitempty"`
	EffectiveFormat   *CellFormat         `json:"effectiveFormat,omitempty"`
	DataValidation    *DataValidationRule `json:"dataValidation,omitempty"`
	Note              string              `json:"note,omitempty"`
	Hyperlink         string              `json:"hyperlink,omitempty"`
	TextFormatRuns    []json.RawMessage   `json:"textFormatRuns,omitempty"`
	ChipRuns          []json.RawMessage   `json:"chipRuns,omitempty"`
	PivotTable        json.RawMessage     `json:"pivotTable,omitempty"`
	DataSourceFormula json.RawMessage     `json:"dataSourceFormula,omitempty"`
}

// ExtendedValue is the API's cell value union: exactly one field is set.
type ExtendedValue struct {
	NumberValue  *float64    `json:"numberValue,omitempty"`
	StringValue  *string     `json:"stringValue,omitempty"`
	BoolValue    *bool       `json:"boolValue,omitempty"`
	FormulaValue *string     `json:"formulaValue,omitempty"`
	ErrorValue   *ErrorValue `json:"errorValue,omitempty"`
}

// ErrorValue is a cell holding a formula error such as #REF! or #N/A.
type ErrorValue struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

// Display is what Sheets shows for an error type.
//
// It lives beside the type rather than in whichever package needed it
// first, because two copies of this table diverge: the fake and the
// renderer had different fallbacks, so an error type outside the list
// rendered one way in a formatted read and another way in a raw one,
// and the tests agreed with both answers.
func (e *ErrorValue) Display() string {
	switch e.Type {
	case "REF":
		return "#REF!"
	case "NAME":
		return "#NAME?"
	case "DIVIDE_BY_ZERO":
		return "#DIV/0!"
	case "N_A":
		return "#N/A"
	case "VALUE":
		return "#VALUE!"
	case "NUM":
		return "#NUM!"
	case "ERROR":
		return "#ERROR!"
	case "NULL_VALUE":
		return "#NULL!"
	}
	// An error type this build has not met still renders as one, and
	// says which, rather than as a generic error that hides it.
	return "#" + e.Type
}

// CellFormat is the subset of a cell's format phase 0 reports.
type CellFormat struct {
	NumberFormat    *NumberFormat `json:"numberFormat,omitempty"`
	HorizontalAlign string        `json:"horizontalAlignment,omitempty"`
	VerticalAlign   string        `json:"verticalAlignment,omitempty"`
	WrapStrategy    string        `json:"wrapStrategy,omitempty"`
}

// NumberFormat is a cell's number format.
type NumberFormat struct {
	Type    string `json:"type,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

// ColorStyle is the API's colour union.
type ColorStyle struct {
	RGBColor   *Color `json:"rgbColor,omitempty"`
	ThemeColor string `json:"themeColor,omitempty"`
}

// Color is an RGBA colour with components in 0..1.
type Color struct {
	Red   float64 `json:"red,omitempty"`
	Green float64 `json:"green,omitempty"`
	Blue  float64 `json:"blue,omitempty"`
	Alpha float64 `json:"alpha,omitempty"`
}

// NamedRange is a name attached to a rectangle. A named range shadows a
// sheet of the same name in an unquoted A1 reference, which is why
// internal/a1 always quotes.
type NamedRange struct {
	NamedRangeID string     `json:"namedRangeId,omitempty"`
	Name         string     `json:"name,omitempty"`
	Range        *GridRange `json:"range,omitempty"`
}

// ProtectedRange refuses edits from accounts that are not editors.
type ProtectedRange struct {
	ProtectedRangeID      int          `json:"protectedRangeId,omitempty"`
	Range                 *GridRange   `json:"range,omitempty"`
	NamedRangeID          string       `json:"namedRangeId,omitempty"`
	Description           string       `json:"description,omitempty"`
	WarningOnly           bool         `json:"warningOnly,omitempty"`
	RequestingUserCanEdit bool         `json:"requestingUserCanEdit,omitempty"`
	Editors               *Editors     `json:"editors,omitempty"`
	UnprotectedRanges     []*GridRange `json:"unprotectedRanges,omitempty"`
}

// Editors are the accounts allowed to edit a protected range.
type Editors struct {
	Users              []string `json:"users,omitempty"`
	Groups             []string `json:"groups,omitempty"`
	DomainUsersCanEdit bool     `json:"domainUsersCanEdit,omitempty"`
}

// FilterView is a saved view over a range.
type FilterView struct {
	FilterViewID int        `json:"filterViewId,omitempty"`
	Title        string     `json:"title,omitempty"`
	Range        *GridRange `json:"range,omitempty"`
}

// Table is a native Sheets table.
type Table struct {
	TableID          string         `json:"tableId,omitempty"`
	Name             string         `json:"name,omitempty"`
	Range            *GridRange     `json:"range,omitempty"`
	ColumnProperties []*TableColumn `json:"columnProperties,omitempty"`
}

// TableColumn is one column of a table.
type TableColumn struct {
	ColumnIndex int    `json:"columnIndex,omitempty"`
	ColumnName  string `json:"columnName,omitempty"`
	ColumnType  string `json:"columnType,omitempty"`
}

// BandedRange is alternating-colour banding over a range.
type BandedRange struct {
	BandedRangeID int        `json:"bandedRangeId,omitempty"`
	Range         *GridRange `json:"range,omitempty"`
}

// EmbeddedChart is a chart on a sheet. Phase 0 reports only that one
// exists; phase 4 builds them.
type EmbeddedChart struct {
	ChartID int `json:"chartId,omitempty"`
}

// DataValidationRule is a cell's validation rule.
type DataValidationRule struct {
	Condition    *BooleanCondition `json:"condition,omitempty"`
	InputMessage string            `json:"inputMessage,omitempty"`
	Strict       bool              `json:"strict,omitempty"`
	ShowCustomUI bool              `json:"showCustomUi,omitempty"`
}

// BooleanCondition is a validation or conditional-format condition.
type BooleanCondition struct {
	Type   string            `json:"type,omitempty"`
	Values []*ConditionValue `json:"values,omitempty"`
}

// ConditionValue is one operand of a BooleanCondition.
type ConditionValue struct {
	RelativeDate     string `json:"relativeDate,omitempty"`
	UserEnteredValue string `json:"userEnteredValue,omitempty"`
}

// ValueRange is one range of values, as spreadsheets.values.* speaks it.
type ValueRange struct {
	Range          string  `json:"range,omitempty"`
	MajorDimension string  `json:"majorDimension,omitempty"`
	Values         [][]any `json:"values,omitempty"`
}

// BatchGetValuesResponse is the values.batchGet response.
type BatchGetValuesResponse struct {
	SpreadsheetID string        `json:"spreadsheetId,omitempty"`
	ValueRanges   []*ValueRange `json:"valueRanges,omitempty"`
}
