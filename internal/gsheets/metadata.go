package gsheets

// Developer metadata: the only anchor Google keeps attached to a
// location while the sheet is edited around it (§6.4).
//
// Verified live as spike K, and what it verified is narrow: an entry
// attached to a row follows that row through an insert, a delete, a move
// and a sort, and dies with the row when the row is deleted. It attaches
// to a spreadsheet, a sheet, or a single row or column, and never to a
// rectangle.

// Visibility values. DOCUMENT is what this server writes.
//
// PROJECT scopes an entry to the OAuth client that created it, and this
// server is distributed for people to run under a client id of their
// own — so a PROJECT anchor would vanish on a re-install with nothing to
// say why. Both were confirmed to work live; the choice is the
// consequence of how this is installed, not of what the API allows.
const (
	VisibilityDocument = "DOCUMENT"
	VisibilityProject  = "PROJECT"
)

// Location types, as the API reports them. The field is read-only: it
// is derived from which location field was set.
const (
	LocationRow         = "ROW"
	LocationColumn      = "COLUMN"
	LocationSheet       = "SHEET"
	LocationSpreadsheet = "SPREADSHEET"
)

// Location matching strategies for a lookup.
//
// A sheet location with INTERSECTING returns every row and column anchor
// on that sheet as well as the sheet's own, which is what makes listing
// one request. A spreadsheet location with INTERSECTING is refused by
// the API: "DeveloperMetadataLookup.spreadsheet is true, but
// locationMatchingStrategy was specified as INTERSECTING".
const (
	MatchExact        = "EXACT_LOCATION"
	MatchIntersecting = "INTERSECTING_LOCATION"
)

// DeveloperMetadata is one entry.
type DeveloperMetadata struct {
	MetadataID    int                        `json:"metadataId,omitempty"`
	MetadataKey   string                     `json:"metadataKey,omitempty"`
	MetadataValue string                     `json:"metadataValue,omitempty"`
	Location      *DeveloperMetadataLocation `json:"location,omitempty"`
	Visibility    string                     `json:"visibility,omitempty"`
}

// DeveloperMetadataLocation is where an entry is attached. Exactly one
// of the three is set on the way out; LocationType is filled by the API
// on the way back.
type DeveloperMetadataLocation struct {
	LocationType string `json:"locationType,omitempty"`
	Spreadsheet  bool   `json:"spreadsheet,omitempty"`
	SheetID      *int   `json:"sheetId,omitempty"`
	// DimensionRange must be a single bounded row or column. Two rows is
	// "DimensionRange must represent a single row or column"; an
	// unbounded one is "DimensionRange must specify both a startIndex
	// and an endIndex". Both are refused here before they are sent.
	DimensionRange *DimensionRange `json:"dimensionRange,omitempty"`
}

// DeveloperMetadataLookup selects entries. Every field narrows, and all
// of them are optional: a key alone is a valid lookup and so is a
// location type alone.
type DeveloperMetadataLookup struct {
	MetadataID               int                        `json:"metadataId,omitempty"`
	MetadataKey              string                     `json:"metadataKey,omitempty"`
	MetadataValue            string                     `json:"metadataValue,omitempty"`
	MetadataLocation         *DeveloperMetadataLocation `json:"metadataLocation,omitempty"`
	LocationType             string                     `json:"locationType,omitempty"`
	LocationMatchingStrategy string                     `json:"locationMatchingStrategy,omitempty"`
	Visibility               string                     `json:"visibility,omitempty"`
}

// DataFilter selects by range or by metadata. Only the metadata half is
// used here; the two range forms belong to the batch-by-filter calls
// this server does not make.
type DataFilter struct {
	DeveloperMetadataLookup *DeveloperMetadataLookup `json:"developerMetadataLookup,omitempty"`
}

// SearchDeveloperMetadataRequest is the body of developerMetadata.search
// — a POST that only reads (§11).
type SearchDeveloperMetadataRequest struct {
	DataFilters []*DataFilter `json:"dataFilters,omitempty"`
}

// SearchDeveloperMetadataResponse is what it answers with.
type SearchDeveloperMetadataResponse struct {
	MatchedDeveloperMetadata []*MatchedDeveloperMetadata `json:"matchedDeveloperMetadata,omitempty"`
}

// MatchedDeveloperMetadata is one hit and the filters that caught it.
type MatchedDeveloperMetadata struct {
	DeveloperMetadata *DeveloperMetadata `json:"developerMetadata,omitempty"`
}

// CreateDeveloperMetadataRequest adds an entry.
type CreateDeveloperMetadataRequest struct {
	DeveloperMetadata *DeveloperMetadata `json:"developerMetadata,omitempty"`
}

// CreateDeveloperMetadataReply carries the entry as stored, including
// the id the API assigned.
type CreateDeveloperMetadataReply struct {
	DeveloperMetadata *DeveloperMetadata `json:"developerMetadata,omitempty"`
}

// UpdateDeveloperMetadataRequest changes every entry its filters match.
type UpdateDeveloperMetadataRequest struct {
	DataFilters       []*DataFilter      `json:"dataFilters,omitempty"`
	DeveloperMetadata *DeveloperMetadata `json:"developerMetadata,omitempty"`
	Fields            string             `json:"fields,omitempty"`
}

// UpdateDeveloperMetadataReply lists what changed.
type UpdateDeveloperMetadataReply struct {
	DeveloperMetadata []*DeveloperMetadata `json:"developerMetadata,omitempty"`
}

// DeleteDeveloperMetadataRequest removes every entry its filter matches.
type DeleteDeveloperMetadataRequest struct {
	DataFilter *DataFilter `json:"dataFilter,omitempty"`
}

// DeleteDeveloperMetadataReply lists what went, which is the only place
// the count is available: the request names a filter, not an entry.
type DeleteDeveloperMetadataReply struct {
	DeletedDeveloperMetadata []*DeveloperMetadata `json:"deletedDeveloperMetadata,omitempty"`
}
