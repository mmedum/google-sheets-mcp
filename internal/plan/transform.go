package plan

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The transforms: the operations that change where data is without the
// caller naming its new address. Each one is a single union member, and
// each is why transform_range runs the guard over a destination the
// caller cannot see.

// Sort orders the rows of a rectangle.
func Sort(sheetID int, rect a1.Rect, specs []*gsheets.SortSpec) *gsheets.Request {
	return &gsheets.Request{SortRange: &gsheets.SortRangeRequest{
		Range: rect.GridRange(sheetID), SortSpecs: specs,
	}}
}

// ParseSort reads "B asc, C desc" into sort specs.
//
// The column is absolute — the sheet's own column, not an offset into
// the range — which is what the API's dimensionIndex means. Writing it
// as a letter keeps the caller in A1 and keeps the conversion here, the
// same as everywhere else in this server.
func ParseSort(s string) ([]*gsheets.SortSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("say which column to sort by, such as \"B asc\" or \"B asc, C desc\"")
	}
	var out []*gsheets.SortSpec
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) == 0 || len(fields) > 2 {
			return nil, fmt.Errorf("%q is not a column and an order; write \"B asc\" or \"B desc\"", strings.TrimSpace(part))
		}
		col, err := a1.ParseColumn(fields[0])
		if err != nil {
			return nil, fmt.Errorf("%q is not a column letter", fields[0])
		}
		order := gsheets.SortAscending
		if len(fields) == 2 {
			switch strings.ToLower(fields[1]) {
			case "asc", "ascending":
			case "desc", "descending":
				order = gsheets.SortDescending
			default:
				return nil, fmt.Errorf("sort order %q is not asc or desc", fields[1])
			}
		}
		out = append(out, &gsheets.SortSpec{DimensionIndex: a1.ZeroBased(col), SortOrder: order})
	}
	return out, nil
}

// FindReplaceOptions are the switches a replacement carries.
type FindReplaceOptions struct {
	MatchCase       bool
	MatchEntireCell bool
	Regex           bool
	// InFormulas replaces inside formula text rather than in the values
	// it produced. It is separate because replacing inside a formula
	// changes what a cell computes, which a values-only replacement
	// never does.
	InFormulas bool
}

// FindReplace replaces text inside a rectangle.
func FindReplace(sheetID int, rect a1.Rect, find, replacement string, o FindReplaceOptions) *gsheets.Request {
	return &gsheets.Request{FindReplace: &gsheets.FindReplaceRequest{
		Find: find, Replacement: replacement, Range: rect.GridRange(sheetID),
		MatchCase: o.MatchCase, MatchEntireCell: o.MatchEntireCell,
		SearchByRegex: o.Regex, IncludeFormulas: o.InFormulas,
	}}
}

// Trim strips leading, trailing and repeated whitespace.
func Trim(sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{TrimWhitespace: &gsheets.TrimWhitespaceRequest{Range: rect.GridRange(sheetID)}}
}

// Dedupe removes duplicate rows. With no columns named, every column in
// the rectangle is compared, which is the API's own default.
func Dedupe(sheetID int, rect a1.Rect, columns []int) *gsheets.Request {
	req := &gsheets.DeleteDuplicatesRequest{Range: rect.GridRange(sheetID)}
	for _, col := range columns {
		start, end := a1.BandIndices(col, col)
		req.ComparisonColumns = append(req.ComparisonColumns, &gsheets.DimensionRange{
			SheetID: sheetID, Dimension: gsheets.DimensionColumns, StartIndex: start, EndIndex: end,
		})
	}
	return &gsheets.Request{DeleteDuplicates: req}
}

// ParseColumns reads "B,D" into one-based column numbers.
func ParseColumns(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		col, err := a1.ParseColumn(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("%q is not a column letter", strings.TrimSpace(part))
		}
		out = append(out, col)
	}
	return out, nil
}

// TextToColumns splits one column of text across several columns.
func TextToColumns(sheetID int, rect a1.Rect, d Delimiter) *gsheets.Request {
	return &gsheets.Request{TextToColumns: &gsheets.TextToColumnsRequest{
		Source: rect.GridRange(sheetID), DelimiterType: d.Kind, Delimiter: d.Custom,
	}}
}

// ParseDelimiter reads a named delimiter or a single character to split
// on. An empty string means let Google detect it.
//
// The single character is read before anything is trimmed, because a
// space is one of the characters somebody splits on: trimming first
// turned " " into "" and quietly asked Google to detect a delimiter
// instead of using the one that was given.
func ParseDelimiter(s string) (d Delimiter, err error) {
	if len([]rune(s)) == 1 {
		switch s {
		case ",":
			return Delimiter{Kind: gsheets.DelimiterComma, Sep: ","}, nil
		case ";":
			return Delimiter{Kind: gsheets.DelimiterSemicolon, Sep: ";"}, nil
		case ".":
			return Delimiter{Kind: gsheets.DelimiterPeriod, Sep: "."}, nil
		case " ":
			return Delimiter{Kind: gsheets.DelimiterSpace, Sep: " "}, nil
		}
		return Delimiter{Kind: gsheets.DelimiterCustom, Custom: s, Sep: s}, nil
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return Delimiter{Kind: gsheets.DelimiterAutodetect}, nil
	case "comma":
		return Delimiter{Kind: gsheets.DelimiterComma, Sep: ","}, nil
	case "semicolon":
		return Delimiter{Kind: gsheets.DelimiterSemicolon, Sep: ";"}, nil
	case "period":
		return Delimiter{Kind: gsheets.DelimiterPeriod, Sep: "."}, nil
	case "space":
		return Delimiter{Kind: gsheets.DelimiterSpace, Sep: " "}, nil
	}
	return Delimiter{}, fmt.Errorf("delimiter %q is not comma, semicolon, period, space, or a single character", s)
}

// Delimiter is what a caller asked to split on, in both the forms the
// callers need: the API's enum and its custom field for the request, and
// the character itself for the guard that has to work out how far a
// split would reach.
//
// Both from one parse. The guard used to read the caller's word a second
// time, with a comment saying the two had to agree about what a space
// means — which they had already failed to do once.
type Delimiter struct {
	Kind string
	// Custom is set only for a character the enum has no name for, which
	// is the one case the request carries it.
	Custom string
	// Sep is the character itself, empty when Google is detecting it.
	Sep string
}

// Randomize shuffles the rows of a rectangle.
func Randomize(sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{RandomizeRange: &gsheets.RandomizeRangeRequest{Range: rect.GridRange(sheetID)}}
}

// AutoFill continues the series in a rectangle for a further length.
//
// The explicit form, always: the bare range form asks Google to work out
// which part of the rectangle is the source, and a write has one guess
// too many in it already.
func AutoFill(sheetID int, source a1.Rect, rows bool, length int) *gsheets.Request {
	dimension := gsheets.DimensionColumns
	if rows {
		dimension = gsheets.DimensionRows
	}
	return &gsheets.Request{AutoFill: &gsheets.AutoFillRequest{
		SourceAndDestination: &gsheets.SourceAndDestination{
			Source: source.GridRange(sheetID), Dimension: dimension, FillLength: length,
		},
	}}
}

// CopyPaste copies a rectangle somewhere else and leaves the source.
func CopyPaste(sheetID int, source, destination a1.Rect, destSheetID int, paste string, transpose bool) *gsheets.Request {
	orientation := gsheets.PasteNormalOrientation
	if transpose {
		orientation = gsheets.PasteTranspose
	}
	return &gsheets.Request{CopyPaste: &gsheets.CopyPasteRequest{
		Source: source.GridRange(sheetID), Destination: destination.GridRange(destSheetID),
		PasteType: paste, PasteOrientation: orientation,
	}}
}

// CutPaste moves a rectangle, emptying the source.
//
// The destination is one cell rather than a rectangle: a cut lands where
// it is put and keeps its shape, and the API says so in its own types.
func CutPaste(sheetID int, source a1.Rect, destSheetID, row, col int, paste string) *gsheets.Request {
	return &gsheets.Request{CutPaste: &gsheets.CutPasteRequest{
		Source: source.GridRange(sheetID),
		Destination: &gsheets.GridCoordinate{
			SheetID: destSheetID, RowIndex: a1.ZeroBased(row), ColumnIndex: a1.ZeroBased(col),
		},
		PasteType: paste,
	}}
}

// pasteTypes are what a caller may ask to be pasted.
var pasteTypes = map[string]string{
	"normal":      gsheets.PasteNormal,
	"values":      gsheets.PasteValues,
	"format":      gsheets.PasteFormat,
	"formula":     gsheets.PasteFormula,
	"validation":  gsheets.PasteDataValidation,
	"conditional": gsheets.PasteConditional,
}

// ParsePaste reads what should be carried across.
func ParsePaste(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return gsheets.PasteNormal, nil
	}
	if v, ok := pasteTypes[strings.ToLower(strings.TrimSpace(s))]; ok {
		return v, nil
	}
	return "", fmt.Errorf("paste %q is not one of normal, values, format, formula, validation, conditional", s)
}
