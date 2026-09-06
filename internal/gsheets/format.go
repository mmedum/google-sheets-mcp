package gsheets

// The formatting half of the wire types: what a cell looks like, what is
// attached to a range, and the batchUpdate members phase 2 sends.
//
// Same rule as the rest of this package. A field here means some code
// reads or writes it, so a struct is never a transcription of the
// reference.

// TextFormat is a cell's font.
//
// The booleans have no pointer and no "unset". A request names the
// fields it changes in its own mask — "userEnteredFormat.textFormat.bold"
// — and a field named in the mask and absent from the body is set to its
// default, which is how bold is turned off. So "leave it alone" is
// expressed by leaving it out of the mask, and the caller's three-way
// choice lives in the tool's input rather than here.
type TextFormat struct {
	ForegroundColorStyle *ColorStyle `json:"foregroundColorStyle,omitempty"`
	FontFamily           string      `json:"fontFamily,omitempty"`
	FontSize             int         `json:"fontSize,omitempty"`
	Bold                 bool        `json:"bold,omitempty"`
	Italic               bool        `json:"italic,omitempty"`
	Strikethrough        bool        `json:"strikethrough,omitempty"`
	Underline            bool        `json:"underline,omitempty"`
}

// Border is one edge of a cell.
//
// Width is absent on purpose: the API documents it as deprecated and
// derives the thickness from Style, so a request carrying both would be
// asking for two things that can disagree. The tool's "1pt solid" reads
// the number and picks the style.
type Border struct {
	Style      string      `json:"style,omitempty"`
	ColorStyle *ColorStyle `json:"colorStyle,omitempty"`
}

// Border styles, as the API spells them.
const (
	BorderNone   = "NONE"
	BorderThin   = "SOLID"
	BorderMedium = "SOLID_MEDIUM"
	BorderThick  = "SOLID_THICK"
	BorderDotted = "DOTTED"
	BorderDashed = "DASHED"
	BorderDouble = "DOUBLE"
)

// Borders are the four edges of one cell, as a cell format carries them.
// The inner edges are an update's business, not a cell's.
type Borders struct {
	Top    *Border `json:"top,omitempty"`
	Bottom *Border `json:"bottom,omitempty"`
	Left   *Border `json:"left,omitempty"`
	Right  *Border `json:"right,omitempty"`
}

// Horizontal and vertical alignment, and the wrap strategies.
const (
	AlignLeft   = "LEFT"
	AlignCentre = "CENTER"
	AlignRight  = "RIGHT"

	AlignTop    = "TOP"
	AlignMiddle = "MIDDLE"
	AlignBottom = "BOTTOM"

	WrapOverflow = "OVERFLOW_CELL"
	WrapLegacy   = "LEGACY_WRAP"
	WrapClip     = "CLIP"
	WrapWrap     = "WRAP"
)

// Number format types, as the API spells them.
const (
	NumberFormatText       = "TEXT"
	NumberFormatNumber     = "NUMBER"
	NumberFormatPercent    = "PERCENT"
	NumberFormatCurrency   = "CURRENCY"
	NumberFormatDate       = "DATE"
	NumberFormatTime       = "TIME"
	NumberFormatDateTime   = "DATE_TIME"
	NumberFormatScientific = "SCIENTIFIC"
)

// ConditionalFormatRule is a rule attached to ranges on one sheet.
// Exactly one of the two rule kinds is set.
type ConditionalFormatRule struct {
	Ranges       []*GridRange  `json:"ranges,omitempty"`
	BooleanRule  *BooleanRule  `json:"booleanRule,omitempty"`
	GradientRule *GradientRule `json:"gradientRule,omitempty"`
}

// BooleanRule applies a format when its condition holds.
type BooleanRule struct {
	Condition *BooleanCondition `json:"condition,omitempty"`
	Format    *CellFormat       `json:"format,omitempty"`
}

// GradientRule colours by value between interpolation points.
//
// Empty on purpose. Nothing here builds a gradient and nothing reads its
// points; a formatting read only needs to know that the colour on a cell
// came from a rule rather than from the cell, which is this field being
// present. The points arrive with the code that reads them, as every
// other field in this package does.
type GradientRule struct{}

// BandingProperties are the colours of an alternating band.
type BandingProperties struct {
	HeaderColorStyle     *ColorStyle `json:"headerColorStyle,omitempty"`
	FirstBandColorStyle  *ColorStyle `json:"firstBandColorStyle,omitempty"`
	SecondBandColorStyle *ColorStyle `json:"secondBandColorStyle,omitempty"`
}

// RepeatCellRequest writes one cell's fields across a rectangle. It is
// the workhorse of format_cells: a number format, a font, a background,
// an alignment and a note are all one of these with a different mask.
type RepeatCellRequest struct {
	Range  *GridRange `json:"range,omitempty"`
	Cell   *CellData  `json:"cell,omitempty"`
	Fields string     `json:"fields,omitempty"`
}

// UpdateBordersRequest draws the edges of a rectangle. Separate from a
// cell format because the inner edges belong to the rectangle rather
// than to any one cell.
//
// A nil edge is left alone; an edge set to style NONE is erased. That is
// the API's own rule and the reason "none" is a border style here.
type UpdateBordersRequest struct {
	Range           *GridRange `json:"range,omitempty"`
	Top             *Border    `json:"top,omitempty"`
	Bottom          *Border    `json:"bottom,omitempty"`
	Left            *Border    `json:"left,omitempty"`
	Right           *Border    `json:"right,omitempty"`
	InnerHorizontal *Border    `json:"innerHorizontal,omitempty"`
	InnerVertical   *Border    `json:"innerVertical,omitempty"`
}

// Merge types, as the API spells them.
const (
	MergeAll     = "MERGE_ALL"
	MergeColumns = "MERGE_COLUMNS"
	MergeRows    = "MERGE_ROWS"
)

// MergeCellsRequest joins cells. Sheets keeps the top-left value and
// discards the rest, which is why the guard reads the rectangle first.
type MergeCellsRequest struct {
	Range     *GridRange `json:"range,omitempty"`
	MergeType string     `json:"mergeType,omitempty"`
}

// UnmergeCellsRequest splits every merge the rectangle covers.
type UnmergeCellsRequest struct {
	Range *GridRange `json:"range,omitempty"`
}

// AddNamedRangeRequest names a rectangle.
type AddNamedRangeRequest struct {
	NamedRange *NamedRange `json:"namedRange,omitempty"`
}

// AddNamedRangeReply carries the id Google assigned.
type AddNamedRangeReply struct {
	NamedRange *NamedRange `json:"namedRange,omitempty"`
}

// UpdateNamedRangeRequest moves or renames one.
type UpdateNamedRangeRequest struct {
	NamedRange *NamedRange `json:"namedRange,omitempty"`
	Fields     string      `json:"fields,omitempty"`
}

// DeleteNamedRangeRequest removes one. The cells are untouched.
type DeleteNamedRangeRequest struct {
	NamedRangeID string `json:"namedRangeId,omitempty"`
}

// AddProtectedRangeRequest protects a rectangle.
type AddProtectedRangeRequest struct {
	ProtectedRange *ProtectedRange `json:"protectedRange,omitempty"`
}

// AddProtectedRangeReply carries the id.
type AddProtectedRangeReply struct {
	ProtectedRange *ProtectedRange `json:"protectedRange,omitempty"`
}

// UpdateProtectedRangeRequest changes one.
type UpdateProtectedRangeRequest struct {
	ProtectedRange *ProtectedRange `json:"protectedRange,omitempty"`
	Fields         string          `json:"fields,omitempty"`
}

// DeleteProtectedRangeRequest removes one.
type DeleteProtectedRangeRequest struct {
	ProtectedRangeID int `json:"protectedRangeId"`
}

// SetDataValidationRequest puts a rule on a rectangle, or clears it when
// the rule is absent.
type SetDataValidationRequest struct {
	Range *GridRange          `json:"range,omitempty"`
	Rule  *DataValidationRule `json:"rule,omitempty"`
}

// AddTableRequest makes a native table.
type AddTableRequest struct {
	Table *Table `json:"table,omitempty"`
}

// AddTableReply carries the table Google made, with its id.
type AddTableReply struct {
	Table *Table `json:"table,omitempty"`
}

// UpdateTableRequest changes one.
type UpdateTableRequest struct {
	Table  *Table `json:"table,omitempty"`
	Fields string `json:"fields,omitempty"`
}

// DeleteTableRequest removes the table and leaves the cells.
type DeleteTableRequest struct {
	TableID string `json:"tableId,omitempty"`
}

// AddBandingRequest adds alternating colours.
type AddBandingRequest struct {
	BandedRange *BandedRange `json:"bandedRange,omitempty"`
}

// AddBandingReply carries the id.
type AddBandingReply struct {
	BandedRange *BandedRange `json:"bandedRange,omitempty"`
}

// UpdateBandingRequest changes one.
type UpdateBandingRequest struct {
	BandedRange *BandedRange `json:"bandedRange,omitempty"`
	Fields      string       `json:"fields,omitempty"`
}

// DeleteBandingRequest removes one.
type DeleteBandingRequest struct {
	BandedRangeID int `json:"bandedRangeId"`
}

// AddConditionalFormatRuleRequest inserts a rule at an index. Rules are
// evaluated in order and the index is how a caller says which wins.
type AddConditionalFormatRuleRequest struct {
	Rule  *ConditionalFormatRule `json:"rule,omitempty"`
	Index int                    `json:"index"`
}

// UpdateConditionalFormatRuleRequest replaces the rule at an index.
type UpdateConditionalFormatRuleRequest struct {
	Rule  *ConditionalFormatRule `json:"rule,omitempty"`
	Index int                    `json:"index"`
	// SheetID is required when the rule is being moved rather than
	// replaced; this server only replaces, so it names the sheet the
	// index counts within.
	SheetID int `json:"sheetId"`
}

// DeleteConditionalFormatRuleRequest removes the rule at an index.
type DeleteConditionalFormatRuleRequest struct {
	Index   int `json:"index"`
	SheetID int `json:"sheetId"`
}

// DeleteConditionalFormatRuleReply carries the rule that was removed, so
// a result can say what went rather than only that something did.
type DeleteConditionalFormatRuleReply struct {
	Rule *ConditionalFormatRule `json:"rule,omitempty"`
}

// SortSpec is one column of a sort, in the API's zero-based numbering.
type SortSpec struct {
	DimensionIndex int    `json:"dimensionIndex"`
	SortOrder      string `json:"sortOrder,omitempty"`
}

// Sort orders, as the API spells them.
const (
	SortAscending  = "ASCENDING"
	SortDescending = "DESCENDING"
)

// SortRangeRequest sorts a rectangle in place.
type SortRangeRequest struct {
	Range     *GridRange  `json:"range,omitempty"`
	SortSpecs []*SortSpec `json:"sortSpecs,omitempty"`
}

// FindReplaceRequest replaces text. Exactly one of Range, SheetID and
// AllSheets scopes it; this server always sends a range.
type FindReplaceRequest struct {
	Find            string     `json:"find,omitempty"`
	Replacement     string     `json:"replacement"`
	Range           *GridRange `json:"range,omitempty"`
	MatchCase       bool       `json:"matchCase,omitempty"`
	MatchEntireCell bool       `json:"matchEntireCell,omitempty"`
	SearchByRegex   bool       `json:"searchByRegex,omitempty"`
	IncludeFormulas bool       `json:"includeFormulas,omitempty"`
	AllSheets       bool       `json:"allSheets,omitempty"`
	SheetID         *int       `json:"sheetId,omitempty"`
}

// FindReplaceReply counts what changed.
type FindReplaceReply struct {
	ValuesChanged      int `json:"valuesChanged,omitempty"`
	FormulasChanged    int `json:"formulasChanged,omitempty"`
	RowsChanged        int `json:"rowsChanged,omitempty"`
	SheetsChanged      int `json:"sheetsChanged,omitempty"`
	OccurrencesChanged int `json:"occurrencesChanged,omitempty"`
}

// TrimWhitespaceRequest strips leading, trailing and repeated spaces.
type TrimWhitespaceRequest struct {
	Range *GridRange `json:"range,omitempty"`
}

// TrimWhitespaceReply counts the cells it changed.
type TrimWhitespaceReply struct {
	CellsChangedCount int `json:"cellsChangedCount,omitempty"`
}

// DeleteDuplicatesRequest removes duplicate rows, comparing the columns
// it is given.
type DeleteDuplicatesRequest struct {
	Range             *GridRange        `json:"range,omitempty"`
	ComparisonColumns []*DimensionRange `json:"comparisonColumns,omitempty"`
}

// DeleteDuplicatesReply counts the rows it removed.
type DeleteDuplicatesReply struct {
	DuplicatesRemovedCount int `json:"duplicatesRemovedCount,omitempty"`
}

// TextToColumnsRequest splits one column into several.
type TextToColumnsRequest struct {
	Source        *GridRange `json:"source,omitempty"`
	Delimiter     string     `json:"delimiter,omitempty"`
	DelimiterType string     `json:"delimiterType,omitempty"`
}

// Delimiter types, as the API spells them.
const (
	DelimiterComma      = "COMMA"
	DelimiterSemicolon  = "SEMICOLON"
	DelimiterPeriod     = "PERIOD"
	DelimiterSpace      = "SPACE"
	DelimiterCustom     = "CUSTOM"
	DelimiterAutodetect = "AUTODETECT"
)

// RandomizeRangeRequest shuffles the rows of a rectangle.
type RandomizeRangeRequest struct {
	Range *GridRange `json:"range,omitempty"`
}

// AutoFillRequest continues a series into the rest of a rectangle.
//
// SourceAndDestination is the explicit form: it names the source rows or
// columns and how far to fill. The bare Range form asks Google to guess
// which part is the source, which is one guess too many for a write.
type AutoFillRequest struct {
	SourceAndDestination *SourceAndDestination `json:"sourceAndDestination,omitempty"`
}

// SourceAndDestination is autofill's explicit form.
type SourceAndDestination struct {
	Source     *GridRange `json:"source,omitempty"`
	Dimension  string     `json:"dimension,omitempty"`
	FillLength int        `json:"fillLength"`
}

// Paste types and orientations, as the API spells them.
const (
	PasteNormal         = "PASTE_NORMAL"
	PasteValues         = "PASTE_VALUES"
	PasteFormat         = "PASTE_FORMAT"
	PasteFormula        = "PASTE_FORMULA"
	PasteDataValidation = "PASTE_DATA_VALIDATION"
	PasteConditional    = "PASTE_CONDITIONAL_FORMATTING"

	PasteNormalOrientation = "NORMAL"
	PasteTranspose         = "TRANSPOSE"
)

// CopyPasteRequest copies a rectangle somewhere else and leaves the
// source alone.
type CopyPasteRequest struct {
	Source           *GridRange `json:"source,omitempty"`
	Destination      *GridRange `json:"destination,omitempty"`
	PasteType        string     `json:"pasteType,omitempty"`
	PasteOrientation string     `json:"pasteOrientation,omitempty"`
}

// CutPasteRequest moves a rectangle, emptying the source.
//
// It has no orientation: the API offers none, because a cut is a move
// and a transposed move has no meaning for the cells left behind.
type CutPasteRequest struct {
	Source      *GridRange      `json:"source,omitempty"`
	Destination *GridCoordinate `json:"destination,omitempty"`
	PasteType   string          `json:"pasteType,omitempty"`
}

// GridCoordinate is one cell, zero-based, which is where a cut lands.
type GridCoordinate struct {
	SheetID     int `json:"sheetId"`
	RowIndex    int `json:"rowIndex"`
	ColumnIndex int `json:"columnIndex"`
}
