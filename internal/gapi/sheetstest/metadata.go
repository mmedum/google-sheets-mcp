package sheetstest

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Developer metadata in the fake.
//
// The behaviour here is spike K's transcript rather than a reading of
// the reference, for the reason §13 gives about the coercion table: a
// fake built from the documentation inherits the documentation's
// errors. What the live run established, and what this reproduces:
//
//   - A row anchor follows its row through an insert above, a delete
//     above, a move, and a sort of the values under it.
//   - Deleting the anchored row deletes the anchor, and the reply says
//     nothing about it.
//   - A key is not unique; two entries may share one.
//   - An empty lookup matches everything.
//   - A sheet location with INTERSECTING reaches the rows and columns on
//     that sheet; with EXACT it reaches only the sheet's own entry.
//   - A spreadsheet location with INTERSECTING is refused.
//
// A fake that left anchors where they were put would agree with any code
// that assumed an anchor is a row number, which is the one thing this
// feature must not be.

// metadataSearch answers developerMetadata.search.
func (s *Server) metadataSearch(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	var req gsheets.SearchDeveloperMetadataRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.DataFilters) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "At least one data filter must be specified.")
		return
	}
	var out gsheets.SearchDeveloperMetadataResponse
	seen := map[int]bool{}
	for _, f := range req.DataFilters {
		if f == nil || f.DeveloperMetadataLookup == nil {
			continue
		}
		lookup := f.DeveloperMetadataLookup
		if lookup.MetadataLocation != nil && lookup.MetadataLocation.Spreadsheet &&
			lookup.LocationMatchingStrategy == gsheets.MatchIntersecting {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"DeveloperMetadataLookup.spreadsheet is true, but locationMatchingStrategy was specified as INTERSECTING.")
			return
		}
		for _, md := range d.Metadata {
			// Deduplicated across filters, which is what the API does:
			// four filters over four entries came back as four.
			if seen[md.MetadataID] || !matchesLookup(lookup, md) {
				continue
			}
			seen[md.MetadataID] = true
			entry := *md
			out.MatchedDeveloperMetadata = append(out.MatchedDeveloperMetadata,
				&gsheets.MatchedDeveloperMetadata{DeveloperMetadata: &entry})
		}
	}
	writeJSON(w, &out)
}

// matchesLookup applies one lookup. Every field narrows and an empty lookup
// matches everything.
func matchesLookup(l *gsheets.DeveloperMetadataLookup, md *gsheets.DeveloperMetadata) bool {
	if l.MetadataID != 0 && l.MetadataID != md.MetadataID {
		return false
	}
	if l.MetadataKey != "" && l.MetadataKey != md.MetadataKey {
		return false
	}
	if l.MetadataValue != "" && l.MetadataValue != md.MetadataValue {
		return false
	}
	if l.Visibility != "" && l.Visibility != md.Visibility {
		return false
	}
	if l.LocationType != "" && l.LocationType != locationType(md.Location) {
		return false
	}
	if l.MetadataLocation == nil {
		return true
	}
	return locationMatches(l.MetadataLocation, l.LocationMatchingStrategy, md.Location)
}

// locationMatches is where EXACT and INTERSECTING differ. Intersecting
// is the default, and a sheet location intersects everything on that
// sheet.
func locationMatches(want *gsheets.DeveloperMetadataLocation, strategy string, have *gsheets.DeveloperMetadataLocation) bool {
	if have == nil {
		return false
	}
	exact := strategy == gsheets.MatchExact
	switch {
	case want.Spreadsheet:
		return have.Spreadsheet
	case want.SheetID != nil:
		if have.SheetID != nil && *have.SheetID == *want.SheetID {
			return true
		}
		if exact {
			return false
		}
		return have.DimensionRange != nil && have.DimensionRange.SheetID == *want.SheetID
	case want.DimensionRange != nil:
		h := have.DimensionRange
		if h == nil {
			return false
		}
		w := want.DimensionRange
		return h.SheetID == w.SheetID && h.Dimension == w.Dimension &&
			h.StartIndex == w.StartIndex && h.EndIndex == w.EndIndex
	}
	return false
}

// locationType is the read-only field the API fills in from whichever
// location was set.
func locationType(loc *gsheets.DeveloperMetadataLocation) string {
	switch {
	case loc == nil:
		return ""
	case loc.DimensionRange != nil:
		if loc.DimensionRange.Dimension == gsheets.DimensionColumns {
			return gsheets.LocationColumn
		}
		return gsheets.LocationRow
	case loc.SheetID != nil:
		return gsheets.LocationSheet
	case loc.Spreadsheet:
		return gsheets.LocationSpreadsheet
	}
	return ""
}

// applyMetadata handles the three metadata requests in the batchUpdate
// union.
func applyMetadata(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.CreateDeveloperMetadata != nil:
		md := req.CreateDeveloperMetadata.DeveloperMetadata
		if md == nil || md.MetadataKey == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
			return nil, true, errors.New("Developer metadata must always have a key specified.")
		}
		if md.Visibility == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return nil, true, errors.New("Developer metadata must always have visibility specified.")
		}
		if err := checkLocation(d, md.Location); err != nil {
			return nil, true, err
		}
		stored := *md
		if stored.MetadataID == 0 {
			stored.MetadataID = nextMetadataID(d)
		}
		stored.Location = withType(md.Location)
		d.Metadata = append(d.Metadata, &stored)
		out := stored
		return &gsheets.Reply{CreateDeveloperMetadata: &gsheets.CreateDeveloperMetadataReply{
			DeveloperMetadata: &out,
		}}, true, nil

	case req.UpdateDeveloperMetadata != nil:
		u := req.UpdateDeveloperMetadata
		if u.Fields == "" {
			return nil, true, errors.New("updateDeveloperMetadata needs a field mask")
		}
		var changed []*gsheets.DeveloperMetadata
		for _, md := range d.Metadata {
			if !anyFilter(u.DataFilters, md) {
				continue
			}
			// The mask is honoured field by field, not treated as one
			// value. A fake that recognised only the exact string it had
			// been shown would agree with any caller that widened the
			// mask and never wrote the extra field.
			want := u.DeveloperMetadata
			if want == nil {
				return nil, true, errors.New("updateDeveloperMetadata needs the metadata to update to")
			}
			for _, f := range strings.Split(u.Fields, ",") {
				switch strings.TrimSpace(f) {
				case "location":
					if err := checkLocation(d, want.Location); err != nil {
						return nil, true, err
					}
					md.Location = withType(want.Location)
				case "metadataValue":
					md.MetadataValue = want.MetadataValue
				case "metadataKey":
					md.MetadataKey = want.MetadataKey
				default:
					return nil, true, errors.New("this fake does not know the field " + f)
				}
			}
			out := *md
			changed = append(changed, &out)
		}
		return &gsheets.Reply{UpdateDeveloperMetadata: &gsheets.UpdateDeveloperMetadataReply{
			DeveloperMetadata: changed,
		}}, true, nil

	case req.DeleteDeveloperMetadata != nil:
		f := req.DeleteDeveloperMetadata.DataFilter
		if f == nil || f.DeveloperMetadataLookup == nil {
			return nil, true, errors.New("deleteDeveloperMetadata needs a data filter")
		}
		var kept, gone []*gsheets.DeveloperMetadata
		for _, md := range d.Metadata {
			if matchesLookup(f.DeveloperMetadataLookup, md) {
				out := *md
				gone = append(gone, &out)
				continue
			}
			kept = append(kept, md)
		}
		d.Metadata = kept
		return &gsheets.Reply{DeleteDeveloperMetadata: &gsheets.DeleteDeveloperMetadataReply{
			DeletedDeveloperMetadata: gone,
		}}, true, nil
	}
	return nil, false, nil
}

func anyFilter(filters []*gsheets.DataFilter, md *gsheets.DeveloperMetadata) bool {
	for _, f := range filters {
		if f != nil && f.DeveloperMetadataLookup != nil && matchesLookup(f.DeveloperMetadataLookup, md) {
			return true
		}
	}
	return false
}

// checkLocation refuses what the API refuses, in the API's own words.
func checkLocation(d *Doc, loc *gsheets.DeveloperMetadataLocation) error {
	if loc == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return errors.New("Developer metadata must always have a location specified.")
	}
	if r := loc.DimensionRange; r != nil {
		if r.EndIndex-r.StartIndex != 1 {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return errors.New("DimensionRange must represent a single row or column.")
		}
		if d.FindByID(r.SheetID) == nil {
			return errors.New("No sheet with id: " + strconv.Itoa(r.SheetID))
		}
	}
	if loc.SheetID != nil && d.FindByID(*loc.SheetID) == nil {
		return errors.New("No sheet with id: " + strconv.Itoa(*loc.SheetID))
	}
	return nil
}

// withType fills in the read-only locationType, as the API does on the
// way back.
func withType(loc *gsheets.DeveloperMetadataLocation) *gsheets.DeveloperMetadataLocation {
	if loc == nil {
		return nil
	}
	out := *loc
	out.LocationType = locationType(loc)
	return &out
}

func nextMetadataID(d *Doc) int {
	next := 1
	for _, md := range d.Metadata {
		if md.MetadataID >= next {
			next = md.MetadataID + 1
		}
	}
	return next
}

// shiftMetadata moves anchors when a band is inserted or deleted, and
// drops the ones whose row or column went with it.
//
// n is positive for an insert and negative for a delete. The drop is the
// half worth having: spike K watched an anchor disappear with its row
// and the reply say nothing at all.
func shiftMetadata(d *Doc, sheetID int, dim string, start, n int) {
	var kept []*gsheets.DeveloperMetadata
	for _, md := range d.Metadata {
		r := anchoredAt(md, sheetID, dim)
		if r == nil {
			kept = append(kept, md)
			continue
		}
		switch {
		case r.StartIndex < start:
			// Before the band: unmoved.
		case n < 0 && r.StartIndex < start-n:
			// Inside a deleted band: gone with it.
			continue
		default:
			r.StartIndex += n
			r.EndIndex += n
		}
		kept = append(kept, md)
	}
	d.Metadata = kept
}

// moveMapping says where each index ends up when a band moves.
//
// One function, used for the cells and for the anchors on them, because
// a fake in which the two disagreed would let an anchor arrive somewhere
// its row did not — which is precisely the bug the anchors exist to
// avoid, reproduced in the thing meant to catch it.
//
// destinationIndex is read against the sheet *before* the move, which is
// spike I's finding for moveDimension and holds for whatever rides on
// it.
func moveMapping(start, end, destination int) func(int) int {
	n := end - start
	return func(at int) int {
		switch {
		case at >= start && at < end:
			to := destination + (at - start)
			if destination > start {
				to -= n
			}
			return to
		case destination <= at && at < start:
			return at + n
		case end <= at && at < destination:
			return at - n
		}
		return at
	}
}

// moveMetadata follows a moveDimension: an anchor travels with its row.
func moveMetadata(d *Doc, sheetID int, dim string, start, end, destination int) {
	move := moveMapping(start, end, destination)
	for _, md := range d.Metadata {
		r := anchoredAt(md, sheetID, dim)
		if r == nil {
			continue
		}
		to := move(r.StartIndex)
		r.StartIndex, r.EndIndex = to, to+1
	}
}

// moveCells moves what is on the band, so a step that reads a moved row
// back finds the values that were on it.
//
// The fake used to leave the cells where they were and validate the
// range only, which made every assertion about a move an assertion about
// nothing. A test of the anchors found it: the anchor arrived at the
// destination and the destination held another row's data.
func moveCells(sh *Sheet, r *gsheets.DimensionRange, destination int) {
	move := moveMapping(r.StartIndex, r.EndIndex, destination)
	axis := 0
	if r.Dimension == gsheets.DimensionColumns {
		axis = 1
	}
	moved := make(map[[2]int]*gsheets.CellData, len(sh.Cells))
	for key, cell := range sh.Cells {
		k := key
		k[axis] = move(key[axis])
		moved[k] = cell
	}
	sh.Cells = moved
}

// permuteMetadata follows a sort: an anchor lands where its row's values
// landed. order[i] is the row that ended up at position i.
func permuteMetadata(d *Doc, sheetID, firstRow int, order []int) {
	// Both sides are zero-based indices, which is what a location holds,
	// and firstRow is the rect's one-based first row. Getting that
	// conversion wrong by one put every anchor on its neighbour's row
	// after a sort — found by a test that read the values back rather
	// than the location.
	to := make(map[int]int, len(order))
	for i, from := range order {
		to[firstRow+from-2] = firstRow + i - 1
	}
	for _, md := range d.Metadata {
		r := anchoredAt(md, sheetID, gsheets.DimensionRows)
		if r == nil {
			continue
		}
		if landed, ok := to[r.StartIndex]; ok {
			r.StartIndex, r.EndIndex = landed, landed+1
		}
	}
}

// anchoredAt is the dimension range of an entry on this sheet and axis,
// or nil.
func anchoredAt(md *gsheets.DeveloperMetadata, sheetID int, dim string) *gsheets.DimensionRange {
	if md.Location == nil || md.Location.DimensionRange == nil {
		return nil
	}
	r := md.Location.DimensionRange
	if r.SheetID != sheetID || r.Dimension != dim {
		return nil
	}
	return r
}
