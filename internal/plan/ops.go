package plan

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The builders below are the only way a request reaches the batchUpdate
// union. The API has 69 members; these are the ones phase 1 sends, and
// each later phase adds its own.
//
// Typed rather than free-form maps (§4.8). A map would let a request be
// sent that no code here has read, which is how a server ends up passing
// the caller's JSON through to Google and calling it a feature.

// AddSheet adds a tab. index is optional: nil leaves the placement to
// Google, which puts it last.
func AddSheet(title string, index *int, rows, cols int) *gsheets.Request {
	p := &gsheets.NewSheetProperties{Title: title, Index: index}
	if rows > 0 || cols > 0 {
		p.GridProperties = &gsheets.GridProperties{RowCount: rows, ColumnCount: cols}
	}
	return &gsheets.Request{AddSheet: &gsheets.AddSheetRequest{Properties: p}}
}

// DeleteSheet removes a tab and everything on it.
func DeleteSheet(sheetID int) *gsheets.Request {
	return &gsheets.Request{DeleteSheet: &gsheets.DeleteSheetRequest{SheetID: sheetID}}
}

// DuplicateSheet copies a tab within the same spreadsheet.
func DuplicateSheet(sourceID int, newName string, index *int) *gsheets.Request {
	return &gsheets.Request{DuplicateSheet: &gsheets.DuplicateSheetRequest{
		SourceSheetID: sourceID, NewSheetName: newName, InsertSheetIndex: index,
	}}
}

// updateSheet builds an updateSheetProperties with the mask that names
// exactly the fields being set. An empty mask is refused by the API
// rather than treated as a no-op, so every caller here sets one.
func updateSheet(p *gsheets.SheetProperties, fields ...string) *gsheets.Request {
	return &gsheets.Request{UpdateSheetProperties: &gsheets.UpdateSheetPropertiesRequest{
		Properties: p, Fields: strings.Join(fields, ","),
	}}
}

// RenameSheet changes a tab's title.
func RenameSheet(sheetID int, title string) *gsheets.Request {
	return updateSheet(&gsheets.SheetProperties{SheetID: sheetID, Title: title}, "title")
}

// ReorderSheet moves a tab so it ends up at wanted.
//
// The API removes the sheet and then inserts it, and reads the index
// against the order *before* the move. Verified live: a sheet at index 0
// asked for index 3 in a four-sheet spreadsheet landed at 2, because
// taking it out first shifted everything after it left. It is the same
// convention moveDimension documents for rows and columns.
//
// The conversion is here so the caller's index means the position the
// sheet ends up at, which is the only meaning anybody predicts.
func ReorderSheet(sheetID, current, wanted int) *gsheets.Request {
	index := wanted
	if current < wanted {
		index++
	}
	return updateSheet(&gsheets.SheetProperties{SheetID: sheetID, Index: gsheets.Ptr(index)}, "index")
}

// HideSheet hides or unhides a tab.
func HideSheet(sheetID int, hidden bool) *gsheets.Request {
	return updateSheet(&gsheets.SheetProperties{SheetID: sheetID, Hidden: hidden}, "hidden")
}

// ResizeGrid changes how many rows and columns a sheet has room for.
func ResizeGrid(sheetID, rows, cols int) *gsheets.Request {
	p := &gsheets.SheetProperties{SheetID: sheetID, GridProperties: &gsheets.GridProperties{}}
	var fields []string
	if rows > 0 {
		p.GridProperties.RowCount = rows
		fields = append(fields, "gridProperties.rowCount")
	}
	if cols > 0 {
		p.GridProperties.ColumnCount = cols
		fields = append(fields, "gridProperties.columnCount")
	}
	return updateSheet(p, fields...)
}

// Freeze pins rows at the top and columns at the left. Zero unfreezes,
// which is why both are always in the mask: a mask that named only the
// non-zero one could not undo a freeze.
func Freeze(sheetID, rows, cols int) *gsheets.Request {
	return updateSheet(&gsheets.SheetProperties{
		SheetID:        sheetID,
		GridProperties: &gsheets.GridProperties{FrozenRowCount: rows, FrozenColumnCount: cols},
	}, "gridProperties.frozenRowCount", "gridProperties.frozenColumnCount")
}

// TabColour sets or clears a tab's colour. A nil style clears it, which
// the mask makes possible.
func TabColour(sheetID int, style *gsheets.ColorStyle) *gsheets.Request {
	return updateSheet(&gsheets.SheetProperties{SheetID: sheetID, TabColorStyle: style}, "tabColorStyle")
}

// dimensionRange converts a one-based inclusive band of rows or columns
// into the API's zero-based half-open form. The arithmetic is a1's, as
// every conversion between A1 and the API's indices has to be.
func dimensionRange(sheetID int, dimension string, first, last int) *gsheets.DimensionRange {
	start, end := a1.BandIndices(first, last)
	return &gsheets.DimensionRange{SheetID: sheetID, Dimension: dimension, StartIndex: start, EndIndex: end}
}

// InsertDimension makes room for rows or columns before first.
//
// inheritFromBefore takes the formatting of the band above or to the
// left. It cannot be true at the very start, since there is nothing
// there to inherit from, so the caller's wish is applied where it can be.
func InsertDimension(sheetID int, dimension string, first, last int, inheritFromBefore bool) *gsheets.Request {
	return &gsheets.Request{InsertDimension: &gsheets.InsertDimensionRequest{
		Range:             dimensionRange(sheetID, dimension, first, last),
		InheritFromBefore: inheritFromBefore && first > 1,
	}}
}

// DeleteDimension removes rows or columns and the data on them.
func DeleteDimension(sheetID int, dimension string, first, last int) *gsheets.Request {
	return &gsheets.Request{DeleteDimension: &gsheets.DeleteDimensionRequest{
		Range: dimensionRange(sheetID, dimension, first, last),
	}}
}

// MoveDimension moves a band so it ends up starting at destination.
//
// The API removes the band and then inserts it, reading
// destinationIndex against the sheet *before* the move — the same
// convention a sheet's index follows, and verified the same way: rows
// 1-2 of four sent with destinationIndex 3 came back starting at row 2,
// not row 4.
//
// So a band moving down has to be given room for itself. Moving up needs
// no adjustment, because nothing above it has shifted.
func MoveDimension(sheetID int, dimension string, first, last, destination int) *gsheets.Request {
	from, want := a1.ZeroBased(first), a1.ZeroBased(destination)
	index := want
	if want > from {
		index = want + (last - first + 1)
	}
	return &gsheets.Request{MoveDimension: &gsheets.MoveDimensionRequest{
		Source:           dimensionRange(sheetID, dimension, first, last),
		DestinationIndex: index,
	}}
}

// ResizeDimension sets a band's size in pixels, or hides it.
func ResizeDimension(sheetID int, dimension string, first, last, pixels int) *gsheets.Request {
	return &gsheets.Request{UpdateDimensionProperties: &gsheets.UpdateDimensionPropertiesRequest{
		Range:      dimensionRange(sheetID, dimension, first, last),
		Properties: &gsheets.DimensionProperties{PixelSize: pixels},
		Fields:     "pixelSize",
	}}
}

// AutoResizeDimensions sizes a band to fit what is on it.
func AutoResizeDimensions(sheetID int, dimension string, first, last int) *gsheets.Request {
	return &gsheets.Request{AutoResizeDimensions: &gsheets.AutoResizeDimensionsRequest{
		Dimensions: dimensionRange(sheetID, dimension, first, last),
	}}
}

// GroupDimensions adds a collapsible group over a band.
func GroupDimensions(sheetID int, dimension string, first, last int) *gsheets.Request {
	return &gsheets.Request{AddDimensionGroup: &gsheets.DimensionGroupRequest{
		Range: dimensionRange(sheetID, dimension, first, last),
	}}
}

// UngroupDimensions removes one.
func UngroupDimensions(sheetID int, dimension string, first, last int) *gsheets.Request {
	return &gsheets.Request{DeleteDimensionGroup: &gsheets.DimensionGroupRequest{
		Range: dimensionRange(sheetID, dimension, first, last),
	}}
}

// ParseColour reads "#rrggbb" or "#rgb" into the API's colour union, and
// "none" into nil, which clears.
//
// Hex because that is what a person has in their hand. The API wants
// three floats between 0 and 1, which nobody types.
func ParseColour(s string) (*gsheets.ColorStyle, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") {
		return nil, nil
	}
	hex := strings.TrimPrefix(s, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return nil, fmt.Errorf("colour %q is not a hex colour; write #rrggbb, #rgb, or none to clear", s)
	}
	var c gsheets.Color
	for i, part := range []*float64{&c.Red, &c.Green, &c.Blue} {
		n, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("colour %q is not a hex colour; write #rrggbb, #rgb, or none to clear", s)
		}
		*part = float64(n) / 255
	}
	c.Alpha = 1
	return &gsheets.ColorStyle{RGBColor: &c}, nil
}

// Band is a run of rows or columns named the way a person says it: one
// based and inclusive, the same as A1.
type Band struct {
	Dimension string
	First     int
	Last      int
}

// ParseDimension reads the axis word, in the spellings a person uses.
// Separate from ParseBand because an anchor names a band without any A1
// in it, and the axis still has to agree with what the anchor is on.
func ParseDimension(dimension string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "rows", "row":
		return gsheets.DimensionRows, nil
	case "columns", "column", "cols", "col":
		return gsheets.DimensionColumns, nil
	}
	return "", fmt.Errorf("dimension %q is not rows or columns", dimension)
}

// ParseBand reads "rows" or "columns" and an A1 band such as "2:5" or
// "B:D", and checks the two agree.
//
// The check is the point. "rows" with "B:D" is a caller who meant one
// thing and typed another, and applying either reading would move
// somebody's data somewhere they did not ask for.
func ParseBand(dimension, band string) (Band, error) {
	dim, err := ParseDimension(dimension)
	if err != nil {
		return Band{}, err
	}
	rect, err := a1.ParseRect(strings.TrimSpace(band))
	if err != nil {
		return Band{}, err
	}
	hasRows := rect.FirstRow != 0 && rect.LastRow != 0
	hasCols := rect.FirstCol != 0 && rect.LastCol != 0
	switch {
	case hasRows && hasCols:
		return Band{}, fmt.Errorf("%q names a rectangle; write whole rows (2:5) or whole columns (B:D)", band)
	case !hasRows && !hasCols:
		return Band{}, fmt.Errorf("%q names no rows or columns; write whole rows (2:5) or whole columns (B:D)", band)
	case dim == gsheets.DimensionRows && !hasRows:
		return Band{}, fmt.Errorf("%q names columns and the dimension says rows; write a row band such as 2:5", band)
	case dim == gsheets.DimensionColumns && !hasCols:
		return Band{}, fmt.Errorf("%q names rows and the dimension says columns; write a column band such as B:D", band)
	case dim == gsheets.DimensionRows:
		return Band{Dimension: dim, First: rect.FirstRow, Last: rect.LastRow}, nil
	default:
		return Band{Dimension: dim, First: rect.FirstCol, Last: rect.LastCol}, nil
	}
}

// Rows reports whether the band runs down rather than across.
func (b Band) Rows() bool { return b.Dimension == gsheets.DimensionRows }

// A band has no String, Unit, Start or Count. It used to, and they are
// render.Band's now: this package is below the renderer, and phrasing
// decided below it is phrasing the goldens cannot cover (§17a.9). The
// count went with them because counting is what the phrasing wanted it
// for.
