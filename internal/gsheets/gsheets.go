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

// Ptr is the address of a value, for the API's many optional scalars.
//
// The API distinguishes "absent" from "zero" in a dozen places where the
// zero is meaningful — a sheet index, a start row, a frozen row count —
// so the wire types use pointers and the callers need this.
func Ptr[T any](v T) *T { return &v }

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
	// DataSources are the Connected Sheets sources. The card asks for
	// them whatever the configuration: the field mask is accepted under
	// the ordinary scopes and returns nothing where there are none, so
	// reporting that a spreadsheet has one costs no scope and no consent
	// (§17.6a).
	DataSources []*DataSource `json:"dataSources,omitempty"`
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
	Properties         *SheetProperties         `json:"properties,omitempty"`
	Data               []*GridData              `json:"data,omitempty"`
	Merges             []*GridRange             `json:"merges,omitempty"`
	ProtectedRanges    []*ProtectedRange        `json:"protectedRanges,omitempty"`
	FilterViews        []*FilterView            `json:"filterViews,omitempty"`
	Tables             []*Table                 `json:"tables,omitempty"`
	Charts             []*EmbeddedChart         `json:"charts,omitempty"`
	Slicers            []*Slicer                `json:"slicers,omitempty"`
	BandedRanges       []*BandedRange           `json:"bandedRanges,omitempty"`
	ConditionalFormats []*ConditionalFormatRule `json:"conditionalFormats,omitempty"`
}

// SheetProperties describe one tab.
//
// Index is a pointer because the API's field is optional and the zero
// value means "first", not "unset": an addSheet carrying index 0 puts
// the new tab at the front of the spreadsheet. A struct that cannot
// express "leave it where Google would put it" is how a sheet lands in
// the wrong place with nobody having asked for it.
type SheetProperties struct {
	SheetID        int             `json:"sheetId"`
	Title          string          `json:"title,omitempty"`
	Index          *int            `json:"index,omitempty"`
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

// CellFormat is a cell's format: what read_formatting reports and what
// format_cells writes.
type CellFormat struct {
	NumberFormat         *NumberFormat `json:"numberFormat,omitempty"`
	BackgroundColorStyle *ColorStyle   `json:"backgroundColorStyle,omitempty"`
	Borders              *Borders      `json:"borders,omitempty"`
	TextFormat           *TextFormat   `json:"textFormat,omitempty"`
	HorizontalAlign      string        `json:"horizontalAlignment,omitempty"`
	VerticalAlign        string        `json:"verticalAlignment,omitempty"`
	WrapStrategy         string        `json:"wrapStrategy,omitempty"`
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

// BandedRange is alternating-colour banding over a range. Exactly one
// of the two property sets is used: banding runs down rows or across
// columns, not both.
type BandedRange struct {
	BandedRangeID    int                `json:"bandedRangeId,omitempty"`
	Range            *GridRange         `json:"range,omitempty"`
	RowProperties    *BandingProperties `json:"rowProperties,omitempty"`
	ColumnProperties *BandingProperties `json:"columnProperties,omitempty"`
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

// Everything below is the write half: the request and response shapes
// for the calls that change a spreadsheet. They are separate from the
// read types above only in what uses them; the API returns the same
// SheetProperties it accepts.

// UpdateValuesResponse is what values.update returns, and what each
// entry of a values.batchUpdate response carries.
//
// UpdatedData is present only when the request asked for the values
// back, which every write here does: it is the half the coercion report
// is built from (§4.4).
type UpdateValuesResponse struct {
	SpreadsheetID  string      `json:"spreadsheetId,omitempty"`
	UpdatedRange   string      `json:"updatedRange,omitempty"`
	UpdatedRows    int         `json:"updatedRows,omitempty"`
	UpdatedColumns int         `json:"updatedColumns,omitempty"`
	UpdatedCells   int         `json:"updatedCells,omitempty"`
	UpdatedData    *ValueRange `json:"updatedData,omitempty"`
}

// AppendValuesResponse is what values.append returns.
//
// TableRange is the block Google decided to append after, which is not
// predictable from the range given: verified live, a range naming a cell
// in the first block appends after that block, and a whole-sheet range
// appends after the last one. So it is reported rather than assumed.
type AppendValuesResponse struct {
	SpreadsheetID string                `json:"spreadsheetId,omitempty"`
	TableRange    string                `json:"tableRange,omitempty"`
	Updates       *UpdateValuesResponse `json:"updates,omitempty"`
}

// ClearValuesResponse is what values.clear returns.
type ClearValuesResponse struct {
	SpreadsheetID string `json:"spreadsheetId,omitempty"`
	ClearedRange  string `json:"clearedRange,omitempty"`
}

// BatchUpdateSpreadsheetRequest is the structural write: one atomic
// batch of typed requests.
//
// The discovery document is explicit that the batch is all or nothing —
// "If any request is not valid then the entire request will fail and
// nothing will be applied" — and that a batch counts once against quota.
// So ops compile into one of these rather than into several calls.
//
// `includeSpreadsheetInResponse` is deliberately absent. It would bring
// the state after the write back in the same response and save the card
// re-read that follows every structural change here, and the fields to
// use it are not written until something uses them: this package's rule
// is that a field appearing in it means some code reads it. §17a carries
// the entry, including the live probe it needs first.
type BatchUpdateSpreadsheetRequest struct {
	Requests []*Request `json:"requests,omitempty"`
}

// BatchUpdateSpreadsheetResponse is that request's answer. Replies line
// up with the requests that produced them, one for one.
type BatchUpdateSpreadsheetResponse struct {
	SpreadsheetID string   `json:"spreadsheetId,omitempty"`
	Replies       []*Reply `json:"replies,omitempty"`
}

// Request is one member of the batchUpdate union. The API has 69; this
// is the set phase 1 builds, and each later phase adds its own rather
// than the whole union arriving as free-form maps.
//
// Exactly one field is set. Nothing here accepts a raw map: a typed
// builder is what stops a request being sent that no code has read.
type Request struct {
	AddSheet                  *AddSheetRequest                  `json:"addSheet,omitempty"`
	DeleteSheet               *DeleteSheetRequest               `json:"deleteSheet,omitempty"`
	DuplicateSheet            *DuplicateSheetRequest            `json:"duplicateSheet,omitempty"`
	UpdateSheetProperties     *UpdateSheetPropertiesRequest     `json:"updateSheetProperties,omitempty"`
	InsertDimension           *InsertDimensionRequest           `json:"insertDimension,omitempty"`
	DeleteDimension           *DeleteDimensionRequest           `json:"deleteDimension,omitempty"`
	MoveDimension             *MoveDimensionRequest             `json:"moveDimension,omitempty"`
	UpdateDimensionProperties *UpdateDimensionPropertiesRequest `json:"updateDimensionProperties,omitempty"`
	AutoResizeDimensions      *AutoResizeDimensionsRequest      `json:"autoResizeDimensions,omitempty"`
	AddDimensionGroup         *DimensionGroupRequest            `json:"addDimensionGroup,omitempty"`
	DeleteDimensionGroup      *DimensionGroupRequest            `json:"deleteDimensionGroup,omitempty"`

	// Phase 2: formatting, the objects attached to a range, and the
	// transforms that move data without the caller naming its address.
	RepeatCell                  *RepeatCellRequest                  `json:"repeatCell,omitempty"`
	UpdateBorders               *UpdateBordersRequest               `json:"updateBorders,omitempty"`
	MergeCells                  *MergeCellsRequest                  `json:"mergeCells,omitempty"`
	UnmergeCells                *UnmergeCellsRequest                `json:"unmergeCells,omitempty"`
	AddNamedRange               *AddNamedRangeRequest               `json:"addNamedRange,omitempty"`
	UpdateNamedRange            *UpdateNamedRangeRequest            `json:"updateNamedRange,omitempty"`
	DeleteNamedRange            *DeleteNamedRangeRequest            `json:"deleteNamedRange,omitempty"`
	AddProtectedRange           *AddProtectedRangeRequest           `json:"addProtectedRange,omitempty"`
	UpdateProtectedRange        *UpdateProtectedRangeRequest        `json:"updateProtectedRange,omitempty"`
	DeleteProtectedRange        *DeleteProtectedRangeRequest        `json:"deleteProtectedRange,omitempty"`
	SetDataValidation           *SetDataValidationRequest           `json:"setDataValidation,omitempty"`
	AddTable                    *AddTableRequest                    `json:"addTable,omitempty"`
	UpdateTable                 *UpdateTableRequest                 `json:"updateTable,omitempty"`
	DeleteTable                 *DeleteTableRequest                 `json:"deleteTable,omitempty"`
	AddBanding                  *AddBandingRequest                  `json:"addBanding,omitempty"`
	UpdateBanding               *UpdateBandingRequest               `json:"updateBanding,omitempty"`
	DeleteBanding               *DeleteBandingRequest               `json:"deleteBanding,omitempty"`
	AddConditionalFormatRule    *AddConditionalFormatRuleRequest    `json:"addConditionalFormatRule,omitempty"`
	UpdateConditionalFormatRule *UpdateConditionalFormatRuleRequest `json:"updateConditionalFormatRule,omitempty"`
	DeleteConditionalFormatRule *DeleteConditionalFormatRuleRequest `json:"deleteConditionalFormatRule,omitempty"`
	SortRange                   *SortRangeRequest                   `json:"sortRange,omitempty"`
	FindReplace                 *FindReplaceRequest                 `json:"findReplace,omitempty"`
	TrimWhitespace              *TrimWhitespaceRequest              `json:"trimWhitespace,omitempty"`
	DeleteDuplicates            *DeleteDuplicatesRequest            `json:"deleteDuplicates,omitempty"`
	TextToColumns               *TextToColumnsRequest               `json:"textToColumns,omitempty"`
	RandomizeRange              *RandomizeRangeRequest              `json:"randomizeRange,omitempty"`
	AutoFill                    *AutoFillRequest                    `json:"autoFill,omitempty"`
	CopyPaste                   *CopyPasteRequest                   `json:"copyPaste,omitempty"`
	CutPaste                    *CutPasteRequest                    `json:"cutPaste,omitempty"`

	// Phase 3: developer metadata, the durable anchors of §6.4.
	CreateDeveloperMetadata *CreateDeveloperMetadataRequest `json:"createDeveloperMetadata,omitempty"`
	UpdateDeveloperMetadata *UpdateDeveloperMetadataRequest `json:"updateDeveloperMetadata,omitempty"`
	DeleteDeveloperMetadata *DeleteDeveloperMetadataRequest `json:"deleteDeveloperMetadata,omitempty"`

	// Phase 4: charts, slicers and pivot tables. A pivot table has no
	// request of its own — it is a field of a cell, so it goes through
	// updateCells like any other cell write.
	AddChart                     *AddChartRequest                     `json:"addChart,omitempty"`
	UpdateChartSpec              *UpdateChartSpecRequest              `json:"updateChartSpec,omitempty"`
	AddSlicer                    *AddSlicerRequest                    `json:"addSlicer,omitempty"`
	UpdateSlicerSpec             *UpdateSlicerSpecRequest             `json:"updateSlicerSpec,omitempty"`
	DeleteEmbeddedObject         *DeleteEmbeddedObjectRequest         `json:"deleteEmbeddedObject,omitempty"`
	UpdateEmbeddedObjectPosition *UpdateEmbeddedObjectPositionRequest `json:"updateEmbeddedObjectPosition,omitempty"`
	UpdateCells                  *UpdateCellsRequest                  `json:"updateCells,omitempty"`

	// Connected Sheets, behind GSHEETS_ENABLE_DATA_SOURCES (§17.6a).
	AddDataSource           *AddDataSourceRequest           `json:"addDataSource,omitempty"`
	RefreshDataSource       *RefreshDataSourceRequest       `json:"refreshDataSource,omitempty"`
	CancelDataSourceRefresh *CancelDataSourceRefreshRequest `json:"cancelDataSourceRefresh,omitempty"`
	DeleteDataSource        *DeleteDataSourceRequest        `json:"deleteDataSource,omitempty"`
}

// Reply is one member of the reply union, in the same order as the
// requests. Only the replies this server reads are here.
type Reply struct {
	AddSheet       *AddSheetReply       `json:"addSheet,omitempty"`
	DuplicateSheet *DuplicateSheetReply `json:"duplicateSheet,omitempty"`

	AddNamedRange               *AddNamedRangeReply               `json:"addNamedRange,omitempty"`
	AddProtectedRange           *AddProtectedRangeReply           `json:"addProtectedRange,omitempty"`
	AddTable                    *AddTableReply                    `json:"addTable,omitempty"`
	AddBanding                  *AddBandingReply                  `json:"addBanding,omitempty"`
	DeleteConditionalFormatRule *DeleteConditionalFormatRuleReply `json:"deleteConditionalFormatRule,omitempty"`
	FindReplace                 *FindReplaceReply                 `json:"findReplace,omitempty"`
	TrimWhitespace              *TrimWhitespaceReply              `json:"trimWhitespace,omitempty"`
	DeleteDuplicates            *DeleteDuplicatesReply            `json:"deleteDuplicates,omitempty"`

	CreateDeveloperMetadata *CreateDeveloperMetadataReply `json:"createDeveloperMetadata,omitempty"`
	UpdateDeveloperMetadata *UpdateDeveloperMetadataReply `json:"updateDeveloperMetadata,omitempty"`
	DeleteDeveloperMetadata *DeleteDeveloperMetadataReply `json:"deleteDeveloperMetadata,omitempty"`

	AddChart                     *AddChartReply                     `json:"addChart,omitempty"`
	AddSlicer                    *AddSlicerReply                    `json:"addSlicer,omitempty"`
	UpdateEmbeddedObjectPosition *UpdateEmbeddedObjectPositionReply `json:"updateEmbeddedObjectPosition,omitempty"`

	AddDataSource     *AddDataSourceReply     `json:"addDataSource,omitempty"`
	RefreshDataSource *RefreshDataSourceReply `json:"refreshDataSource,omitempty"`
}

// NewSheetProperties is a sheet that does not exist yet.
//
// It has no sheetId, and that absence is the type's whole reason for
// being: SheetProperties carries one, the zero value serialises as
// `"sheetId": 0`, and Google reads that as a request for id 0 — which
// the first sheet always has. Live, an addSheet built from
// SheetProperties came back as "Sheet with id 0 already exists", and a
// create came back with the requested sheet and nothing else.
//
// A field that cannot be set wrongly beats a rule about not setting it.
type NewSheetProperties struct {
	Title          string          `json:"title,omitempty"`
	Index          *int            `json:"index,omitempty"`
	GridProperties *GridProperties `json:"gridProperties,omitempty"`
}

// NewSheet is a sheet in a spreadsheets.create request.
type NewSheet struct {
	Properties *NewSheetProperties `json:"properties,omitempty"`
}

// NewSpreadsheet is the spreadsheets.create request body. Separate from
// Spreadsheet for the same reason NewSheetProperties is separate from
// SheetProperties: nothing here has an id yet.
type NewSpreadsheet struct {
	Properties *SpreadsheetProperties `json:"properties,omitempty"`
	Sheets     []*NewSheet            `json:"sheets,omitempty"`
}

// AddSheetRequest adds a tab.
type AddSheetRequest struct {
	Properties *NewSheetProperties `json:"properties,omitempty"`
}

// AddSheetReply carries the properties of the sheet that was added,
// including the id Google assigned it.
type AddSheetReply struct {
	Properties *SheetProperties `json:"properties,omitempty"`
}

// DeleteSheetRequest removes a tab and everything on it.
type DeleteSheetRequest struct {
	SheetID int `json:"sheetId"`
}

// DuplicateSheetRequest copies a tab within the same spreadsheet.
type DuplicateSheetRequest struct {
	SourceSheetID    int    `json:"sourceSheetId"`
	InsertSheetIndex *int   `json:"insertSheetIndex,omitempty"`
	NewSheetName     string `json:"newSheetName,omitempty"`
}

// DuplicateSheetReply names the copy.
type DuplicateSheetReply struct {
	Properties *SheetProperties `json:"properties,omitempty"`
}

// UpdateSheetPropertiesRequest changes the fields its mask names, and
// only those. An empty mask changes nothing, which the API treats as an
// error rather than a no-op.
type UpdateSheetPropertiesRequest struct {
	Properties *SheetProperties `json:"properties,omitempty"`
	Fields     string           `json:"fields,omitempty"`
}

// Dimensions, as the API spells them.
const (
	DimensionRows    = "ROWS"
	DimensionColumns = "COLUMNS"
)

// DimensionRange is a band of rows or columns, zero-based and half-open
// like every other index the API uses.
type DimensionRange struct {
	SheetID    int    `json:"sheetId"`
	Dimension  string `json:"dimension,omitempty"`
	StartIndex int    `json:"startIndex"`
	EndIndex   int    `json:"endIndex"`
}

// InsertDimensionRequest makes room. InheritFromBefore decides which
// neighbour's formatting the new band takes; it cannot be true at index
// zero, since there is nothing before it.
type InsertDimensionRequest struct {
	Range             *DimensionRange `json:"range,omitempty"`
	InheritFromBefore bool            `json:"inheritFromBefore,omitempty"`
}

// DeleteDimensionRequest removes a band and the data in it.
type DeleteDimensionRequest struct {
	Range *DimensionRange `json:"range,omitempty"`
}

// MoveDimensionRequest moves a band. DestinationIndex is where the band
// starts *before* the move, which is the API's own convention and the
// one arithmetic trap in this request.
type MoveDimensionRequest struct {
	Source           *DimensionRange `json:"source,omitempty"`
	DestinationIndex int             `json:"destinationIndex"`
}

// DimensionProperties is a band's size. Hiding a row or a column is
// phase 2's, and the field arrives with the code that sets it.
type DimensionProperties struct {
	PixelSize int `json:"pixelSize,omitempty"`
}

// UpdateDimensionPropertiesRequest resizes or hides a band.
type UpdateDimensionPropertiesRequest struct {
	Range      *DimensionRange      `json:"range,omitempty"`
	Properties *DimensionProperties `json:"properties,omitempty"`
	Fields     string               `json:"fields,omitempty"`
}

// AutoResizeDimensionsRequest sizes a band to its contents.
type AutoResizeDimensionsRequest struct {
	Dimensions *DimensionRange `json:"dimensions,omitempty"`
}

// DimensionGroupRequest adds or removes a collapsible group.
type DimensionGroupRequest struct {
	Range *DimensionRange `json:"range,omitempty"`
}

// CopySheetToAnotherSpreadsheetRequest is the body of sheets.copyTo.
type CopySheetToAnotherSpreadsheetRequest struct {
	DestinationSpreadsheetID string `json:"destinationSpreadsheetId,omitempty"`
}
