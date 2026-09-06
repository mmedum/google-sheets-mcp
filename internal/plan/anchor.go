package plan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// MaxAnchorName is how long a label may be.
//
// Not the API's limit, which is generous: this is a name a person types
// and a model repeats, and one that does not fit on a line is one that
// gets retyped wrong.
const MaxAnchorName = 100

// anchorNamePattern is deliberately wider than a named range's. A named
// range is Sheets' own identifier and has to survive being written into
// a formula; an anchor is only ever a key in a lookup here, so a space
// and a hyphen are fine and "invoice totals" is a better label than
// "invoice_totals".
var anchorNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]*$`)

// CheckAnchorName refuses a label before a request is built.
func CheckAnchorName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("an anchor needs a name")
	case name != strings.TrimSpace(name):
		return fmt.Errorf("%q starts or ends with a space, which is invisible in a result and impossible to "+
			"retype reliably", name)
	case len(name) > MaxAnchorName:
		return fmt.Errorf("%q is %d characters; an anchor name is at most %d", name, len(name), MaxAnchorName)
	case !anchorNamePattern.MatchString(name):
		return fmt.Errorf("%q cannot be an anchor name: use letters, digits, spaces, and . _ -, starting with a "+
			"letter or a digit", name)
	}
	return nil
}

// AnchorRow is the location of one row, zero-based and half-open the way
// the API wants it, from the 1-based row a person says.
//
// The API refuses anything else, in these words: two rows at once is
// "DimensionRange must represent a single row or column", and an
// unbounded range is "DimensionRange must specify both a startIndex and
// an endIndex" (spike K). Nothing here can build either.
func AnchorRow(sheetID, row int) *gsheets.DeveloperMetadataLocation {
	return &gsheets.DeveloperMetadataLocation{
		DimensionRange: dimensionRange(sheetID, gsheets.DimensionRows, row, row),
	}
}

// AnchorColumn is the same for a column, from a 1-based column number.
func AnchorColumn(sheetID, col int) *gsheets.DeveloperMetadataLocation {
	return &gsheets.DeveloperMetadataLocation{
		DimensionRange: dimensionRange(sheetID, gsheets.DimensionColumns, col, col),
	}
}

// AnchorSheet is a whole sheet.
func AnchorSheet(sheetID int) *gsheets.DeveloperMetadataLocation {
	return &gsheets.DeveloperMetadataLocation{SheetID: gsheets.Ptr(sheetID)}
}

// AnchorAdd creates one entry.
//
// Always DOCUMENT: PROJECT ties an entry to the OAuth client that made
// it, and this server is run under a client id its user provides.
func AnchorAdd(name, value string, at *gsheets.DeveloperMetadataLocation) *gsheets.Request {
	return &gsheets.Request{CreateDeveloperMetadata: &gsheets.CreateDeveloperMetadataRequest{
		DeveloperMetadata: &gsheets.DeveloperMetadata{
			MetadataKey: name, MetadataValue: value,
			Location: at, Visibility: gsheets.VisibilityDocument,
		},
	}}
}

// AnchorDelete removes the entry with this id.
//
// By id, never by key. The request takes a filter and deletes everything
// it matches, and a key is not unique — two entries can share one, which
// spike K confirmed by making two. A delete by key would take both and
// report it afterwards.
func AnchorDelete(id int) *gsheets.Request {
	return &gsheets.Request{DeleteDeveloperMetadata: &gsheets.DeleteDeveloperMetadataRequest{
		DataFilter: &gsheets.DataFilter{
			DeveloperMetadataLookup: &gsheets.DeveloperMetadataLookup{MetadataID: id},
		},
	}}
}

// AnchorRetarget moves an existing entry to a new location, by id, and
// replaces its note when one is given.
//
// The mask names its fields rather than using "*". A star would rewrite
// the key and the value from a body that does not carry them, which is
// how a move erases the label it was moving — so `metadataValue` is in
// the mask only when there is a note to put there, and a move with no
// note leaves the stored one alone.
func AnchorRetarget(id int, at *gsheets.DeveloperMetadataLocation, note string) *gsheets.Request {
	md := &gsheets.DeveloperMetadata{Location: at}
	fields := "location"
	if note != "" {
		md.MetadataValue, fields = note, "location,metadataValue"
	}
	return &gsheets.Request{UpdateDeveloperMetadata: &gsheets.UpdateDeveloperMetadataRequest{
		DataFilters: []*gsheets.DataFilter{{
			DeveloperMetadataLookup: &gsheets.DeveloperMetadataLookup{MetadataID: id},
		}},
		DeveloperMetadata: md,
		Fields:            fields,
	}}
}

// ByName looks an anchor up by its label alone. No location is needed,
// which is what makes a name resolvable without knowing where it points.
func ByName(name string) []*gsheets.DataFilter {
	return []*gsheets.DataFilter{{
		DeveloperMetadataLookup: &gsheets.DeveloperMetadataLookup{MetadataKey: name},
	}}
}

// OnSheet lists everything anchored anywhere on one sheet: the rows, the
// columns, and the sheet itself. One request, because an intersecting
// sheet lookup reaches the rows and columns inside it (spike K).
func OnSheet(sheetID int) []*gsheets.DataFilter {
	return []*gsheets.DataFilter{{
		DeveloperMetadataLookup: &gsheets.DeveloperMetadataLookup{
			MetadataLocation:         AnchorSheet(sheetID),
			LocationMatchingStrategy: gsheets.MatchIntersecting,
		},
	}}
}

// Everything lists every anchor in the spreadsheet, whatever it is
// attached to, in one request.
//
// An empty lookup, which the reference does not describe and spike K
// established: it matched all four entries across all four location
// types. The alternative shapes cost more — a filter per location type,
// or a request per sheet — and the sheet-by-sheet one would break §4.5
// on any spreadsheet with more than one sheet. The empty lookup is not a
// missing filter here; it is the filter.
func Everything() []*gsheets.DataFilter {
	return []*gsheets.DataFilter{{DeveloperMetadataLookup: &gsheets.DeveloperMetadataLookup{}}}
}

// AnchorAt describes where an entry sits, in the terms a caller uses.
// The zero value of every field means the entry has no location this
// server can address.
type AnchorAt struct {
	SheetID int
	// Row and Col are 1-based; exactly one is set for a dimension
	// anchor, and neither for a sheet or spreadsheet one.
	Row  int
	Col  int
	Kind string
}

// Where reads an entry's location back.
func Where(md *gsheets.DeveloperMetadata) AnchorAt {
	if md == nil || md.Location == nil {
		return AnchorAt{}
	}
	loc := md.Location
	switch {
	case loc.DimensionRange != nil:
		at := AnchorAt{SheetID: loc.DimensionRange.SheetID, Kind: gsheets.LocationRow}
		if loc.DimensionRange.Dimension == gsheets.DimensionColumns {
			at.Kind = gsheets.LocationColumn
			at.Col = a1.OneBased(loc.DimensionRange.StartIndex)
			return at
		}
		at.Row = a1.OneBased(loc.DimensionRange.StartIndex)
		return at
	case loc.SheetID != nil:
		return AnchorAt{SheetID: *loc.SheetID, Kind: gsheets.LocationSheet}
	case loc.Spreadsheet:
		return AnchorAt{Kind: gsheets.LocationSpreadsheet}
	}
	return AnchorAt{}
}

// Location is the wire form of a resolved position: the inverse of
// Where, and here beside it so the two cannot drift apart. The service
// used to hold this half, which meant a location was turned into an
// AnchorAt and back again to answer one question about it.
func (a AnchorAt) Location(sheetID int) *gsheets.DeveloperMetadataLocation {
	switch {
	case a.Row > 0:
		return AnchorRow(sheetID, a.Row)
	case a.Col > 0:
		return AnchorColumn(sheetID, a.Col)
	}
	return AnchorSheet(sheetID)
}

// Rect is the rectangle an anchor stands for on a sheet of this size: a
// whole row or a whole column. ok is false for anything else.
func (a AnchorAt) Rect(rows, cols int) (a1.Rect, bool) {
	switch {
	case a.Row > 0:
		return a1.Rect{FirstRow: a.Row, LastRow: a.Row, FirstCol: 1, LastCol: cols}, true
	case a.Col > 0:
		return a1.Rect{FirstRow: 1, LastRow: rows, FirstCol: a.Col, LastCol: a.Col}, true
	}
	return a1.Rect{}, false
}
