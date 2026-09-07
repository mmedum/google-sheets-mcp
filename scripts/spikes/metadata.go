//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// spikeK answers §15.K: whether developer metadata is the durable anchor
// §6.4 assumes it is.
//
// §6.4 offers "remember this row as invoice-totals" and promises it
// survives the sheet being edited around it. The reference says only
// that metadata attaches to a spreadsheet, a sheet or a single row or
// column. It does not say what happens when that row is moved, sorted
// or deleted — and an anchor that follows a row is a different feature
// from one that holds a row *number*, because the second one silently
// points at somebody else's data after one insert.
//
// So every step here mutates the sheet and then reads the location back
// out of developerMetadata.search. What is printed is where the API says
// the anchor is, never where this code thinks it put it.
func spikeK(ctx context.Context) {
	sec("Spike K: developer metadata as a durable anchor")
	const sheet = "SpikeAnchors"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	// Six rows whose values name their own starting row, so a step that
	// moves rows around shows which row the anchor ended up on rather
	// than only which number it holds.
	if err := put(ctx, q+"!A1:B6", [][]any{
		{"Header", "start"},
		{"Plimth", "row2"},
		{"Quorbin", "row3"},
		{"Vandel", "row4"},
		{"Threnody", "row5"},
		{"Marrowfen", "row6"},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}

	const key = "spike-anchor"
	line("")
	line("  the anchor is created on row 3, which holds %q at the start.", "row3")
	status, body := batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey":   key,
			"metadataValue": "row3",
			"visibility":    "DOCUMENT",
			"location":      rowLocation(sheetID, 3),
		},
	}})
	line("    create on row 3                    -> HTTP %d  %s", status, first120(body))
	if status != 200 {
		line("  nothing else in this spike means anything without an anchor; stopping.")
		return
	}

	// after prints where the API says the anchor is, and what is on that
	// row now. The second half is the point: a location that still reads
	// "row 3" is only durable if row 3 still holds what it did.
	after := func(what string) {
		found := anchors(ctx, key)
		switch len(found) {
		case 0:
			line("    %-34s -> anchor GONE", what)
			return
		case 1:
			line("    %-34s -> %s, holding %q", what, found[0].where(), found[0].MetadataValue)
		default:
			line("    %-34s -> %d anchors share the key:", what, len(found))
			for _, a := range found {
				line("        %s, holding %q", a.where(), a.MetadataValue)
			}
		}
		if len(found) == 1 && found[0].Location.DimensionRange != nil {
			row := found[0].Location.DimensionRange.StartIndex + 1
			line("        row %d now holds %s", row, rowValues(ctx, fmt.Sprintf("%s!A%d:B%d", q, row, row)))
		}
	}
	after("read back, nothing changed yet")

	line("")
	line("  Q1-Q4: does the anchor follow its row, and does it follow the values on it?")
	mutate(ctx, "insert 2 rows above it", map[string]any{"insertDimension": map[string]any{
		"range": dimRange(sheetID, 0, 2), "inheritFromBefore": false,
	}})
	after("after inserting 2 rows above")

	mutate(ctx, "delete 1 row above it", map[string]any{"deleteDimension": map[string]any{
		"range": dimRange(sheetID, 0, 1),
	}})
	after("after deleting 1 row above")

	mutate(ctx, "move the anchored row up by 2", map[string]any{"moveDimension": map[string]any{
		"source": dimRange(sheetID, 3, 4), "destinationIndex": 1,
	}})
	after("after moving the anchored row")

	// Sorting rewrites the values under every row without moving any row
	// as a dimension. If the anchor holds a row number this is where it
	// starts pointing at somebody else's data while looking correct.
	mutate(ctx, "sort the block by column A", map[string]any{"sortRange": map[string]any{
		"range":     a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 7}.GridRange(sheetID),
		"sortSpecs": []any{map[string]any{"dimensionIndex": 0, "sortOrder": "ASCENDING"}},
	}})
	after("after sorting the values under it")

	line("")
	line("  Q5: does an ordinary values write disturb it?")
	// Over the anchored row, not a neighbouring one. Writing to some
	// other row proves nothing about whether a write removes an anchor,
	// and the first run of this spike did exactly that.
	if row := anchorRow(ctx, key); row > 0 {
		line("    writing over row %d, which is the anchored one", row)
		if err := put(ctx, fmt.Sprintf("%s!A%d:B%d", q, row, row), [][]any{{"Quorbin", "rewritten"}}); err != nil {
			line("    values.update failed: %v", err)
		}
	}
	after("after a values.update over it")

	line("")
	line("  Q6: two entries under one key — is a name unique, or must this server make it so?")
	status, body = batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey": key, "metadataValue": "a second entry under the same key",
			"visibility": "DOCUMENT", "location": rowLocation(sheetID, 6),
		},
	}})
	line("    a second anchor, same key          -> HTTP %d  %s", status, first120(body))
	after("searching that key now")

	line("")
	line("  Q7: what a search matches — location type, and exact against intersecting.")
	sheetKey := key + "-sheet"
	ssKey := key + "-spreadsheet"
	status, _ = batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey": sheetKey, "metadataValue": "on the sheet",
			"visibility": "DOCUMENT", "location": map[string]any{"sheetId": sheetID},
		},
	}})
	line("    an anchor on the sheet             -> HTTP %d", status)
	status, _ = batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey": ssKey, "metadataValue": "on the spreadsheet",
			"visibility": "DOCUMENT", "location": map[string]any{"spreadsheet": true},
		},
	}})
	line("    an anchor on the spreadsheet       -> HTTP %d", status)

	// Read where the anchor is now rather than assuming where it was
	// put. Six mutations have moved it since, and a lookup aimed at the
	// row it started on answers a question nobody asked while looking
	// like a verdict on row lookups.
	row := anchorRow(ctx, key)
	line("    the anchor is on row %d, so that is the row these look up", row)
	lookups := []struct {
		what   string
		lookup map[string]any
	}{
		{"key alone, no location", map[string]any{"metadataKey": key}},
		{"locationType ROW alone", map[string]any{"locationType": "ROW"}},
		{"the sheet, INTERSECTING", map[string]any{
			"metadataLocation": map[string]any{"sheetId": sheetID}, "locationMatchingStrategy": "INTERSECTING_LOCATION"}},
		{"the sheet, EXACT", map[string]any{
			"metadataLocation": map[string]any{"sheetId": sheetID}, "locationMatchingStrategy": "EXACT_LOCATION"}},
		{"the anchored row, INTERSECTING", map[string]any{
			"metadataLocation": rowLocation(sheetID, row), "locationMatchingStrategy": "INTERSECTING_LOCATION"}},
		{"the anchored row, EXACT", map[string]any{
			"metadataLocation": rowLocation(sheetID, row), "locationMatchingStrategy": "EXACT_LOCATION"}},
		{"a row with nothing on it", map[string]any{
			"metadataLocation": rowLocation(sheetID, row+1), "locationMatchingStrategy": "EXACT_LOCATION"}},
		{"the spreadsheet, INTERSECTING", map[string]any{
			"metadataLocation": map[string]any{"spreadsheet": true}, "locationMatchingStrategy": "INTERSECTING_LOCATION"}},
	}
	for _, l := range lookups {
		status, found, body := searchRaw(ctx, l.lookup)
		if status != 200 {
			// The message, not just the status: a refused lookup is a
			// rule this server has to know before it sends one.
			line("    %-34s -> HTTP %d  %s", l.what, status, first120(body))
			continue
		}
		line("    %-34s -> %d match(es): %s", l.what, len(found), summarise(found))
	}

	line("")
	line("  Q7a: is there a one-request way to list everything, whatever it is attached to?")
	// Listing per sheet costs a request per sheet. Three shapes could
	// avoid that and the reference settles none of them, so each is
	// asked rather than assumed.
	status, found, body := searchRaw(ctx, map[string]any{})
	line("    an empty lookup                    -> HTTP %d  %d match(es)  %s", status, len(found), first120(body))
	status, found, body = searchRaw(ctx, map[string]any{"locationType": "SPREADSHEET"})
	line("    locationType SPREADSHEET alone     -> HTTP %d  %d match(es)  %s", status, len(found), first120(body))
	// Several filters in one request. If they come back deduplicated,
	// four filters list every location type in one call.
	status, all, _ := searchFilters(ctx, []any{
		map[string]any{"developerMetadataLookup": map[string]any{"locationType": "ROW"}},
		map[string]any{"developerMetadataLookup": map[string]any{"locationType": "COLUMN"}},
		map[string]any{"developerMetadataLookup": map[string]any{"locationType": "SHEET"}},
		map[string]any{"developerMetadataLookup": map[string]any{
			"metadataLocation": map[string]any{"spreadsheet": true}, "locationMatchingStrategy": "EXACT_LOCATION"}},
	})
	line("    four filters in one request        -> HTTP %d  %d entr(ies)", status, len(all))
	seen := map[int]int{}
	for _, e := range all {
		seen[e.MetadataID]++
	}
	dupes := 0
	for _, n := range seen {
		if n > 1 {
			dupes++
		}
	}
	line("      %d distinct id(s), %d returned more than once", len(seen), dupes)
	for _, e := range all {
		line("        %s at %s", e.MetadataKey, e.where())
	}

	line("")
	line("  Q8: PROJECT visibility, with a per-user OAuth client and no service account.")
	status, body = batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey": key + "-project", "metadataValue": "project visible",
			"visibility": "PROJECT", "location": rowLocation(sheetID, 5),
		},
	}})
	line("    create with visibility PROJECT     -> HTTP %d  %s", status, first120(body))
	if status == 200 {
		_, found := searchMeta(ctx, map[string]any{"metadataKey": key + "-project"})
		line("    reading it back                    -> %d match(es): %s", len(found), summarise(found))
	}

	line("")
	line("  Q9: the locations the reference says are refused, and what it says when it refuses.")
	refusals := []struct {
		what     string
		location map[string]any
	}{
		{"two rows at once", map[string]any{"dimensionRange": map[string]any{
			"sheetId": sheetID, "dimension": "ROWS", "startIndex": 1, "endIndex": 3}}},
		{"an unbounded row range", map[string]any{"dimensionRange": map[string]any{
			"sheetId": sheetID, "dimension": "ROWS"}}},
	}
	for _, r := range refusals {
		status, body := batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
			"developerMetadata": map[string]any{
				"metadataKey": key + "-bad", "metadataValue": "should not exist",
				"visibility": "DOCUMENT", "location": r.location,
			},
		}})
		line("    %-34s -> HTTP %d  %s", r.what, status, first120(body))
	}

	line("")
	line("  Q10: does spreadsheets.get carry it, or does every read cost a search?")
	v := url.Values{}
	v.Set("fields", "developerMetadata,sheets(properties(sheetId),developerMetadata)")
	status, body = call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
	line("    card fields, no grid data          -> HTTP %d  %s", status, first120(body))
	// The anchored row, read now. Asking for a fixed row here is how the
	// first run reported an empty rowMetadata and made it look as though
	// spreadsheets.get does not carry row anchors at all.
	row = anchorRow(ctx, key)
	v = url.Values{}
	v.Set("includeGridData", "true")
	v.Add("ranges", fmt.Sprintf("%s!A%d:B%d", q, row, row))
	v.Set("fields", "sheets(data(rowMetadata(developerMetadata)))")
	status, body = call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
	line("    rowMetadata over the anchored row  -> HTTP %d  %s", status, first120(body))

	line("")
	line("  Q11: what happens to an anchor when its row is deleted. Last, because it destroys it.")
	// Two entries share this key by now, so the step names which one it
	// is destroying and counts both sides. Read as "the anchor", the
	// answer is unreadable: one entry surviving looks identical to the
	// deleted one having survived.
	found = anchors(ctx, key)
	var target *metaEntry
	for i := range found {
		if found[i].Location.DimensionRange != nil {
			target = &found[i]
			break
		}
	}
	if target == nil {
		line("    no anchor is on a row any more; nothing to delete under it.")
		return
	}
	start := target.Location.DimensionRange.StartIndex
	line("    %d anchor(s) under this key before: %s", len(found), ids(found))
	line("    deleting row %d, which carries id %d", start+1, target.MetadataID)
	mutate(ctx, "delete the anchored row itself", map[string]any{"deleteDimension": map[string]any{
		"range": dimRange(sheetID, start, start+1),
	}})
	left := anchors(ctx, key)
	line("    %d anchor(s) under this key after:  %s", len(left), ids(left))
	if len(left) < len(found) {
		line("    so deleting a row takes the anchor on it; nothing in the reply said so.")
	} else {
		line("    so the anchor outlived the row it was attached to.")
	}
	after("what is left under the key")
}

// mutate sends one batchUpdate request and prints how it went, so a step
// whose anchor did not move is told apart from one whose edit never
// landed.
func mutate(ctx context.Context, what string, req map[string]any) {
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{req}})
	if status != 200 {
		line("    %-34s -> HTTP %d  %s", what, status, first120(body))
		return
	}
	line("    %s", what)
}

// rowLocation is a one-row location, in the 1-based row a person would
// say. The API takes zero-based half-open indices and the conversion is
// the one thing §6.4 must never get wrong twice.
func rowLocation(sheetID, row int) map[string]any {
	return map[string]any{"dimensionRange": dimRange(sheetID, row-1, row)}
}

func dimRange(sheetID, start, end int) map[string]any {
	return map[string]any{
		"sheetId": sheetID, "dimension": "ROWS", "startIndex": start, "endIndex": end,
	}
}

// metaEntry is the half of a developer metadata entry this spike reads.
type metaEntry struct {
	MetadataID    int    `json:"metadataId"`
	MetadataKey   string `json:"metadataKey"`
	MetadataValue string `json:"metadataValue"`
	Visibility    string `json:"visibility"`
	Location      struct {
		LocationType   string `json:"locationType"`
		Spreadsheet    bool   `json:"spreadsheet"`
		SheetID        int    `json:"sheetId"`
		DimensionRange *struct {
			SheetID    int    `json:"sheetId"`
			Dimension  string `json:"dimension"`
			StartIndex int    `json:"startIndex"`
			EndIndex   int    `json:"endIndex"`
		} `json:"dimensionRange"`
	} `json:"location"`
}

// where says where the API reports the anchor, in the 1-based terms a
// caller would use.
func (e metaEntry) where() string {
	switch {
	case e.Location.DimensionRange != nil:
		d := e.Location.DimensionRange
		unit := "row"
		if d.Dimension == "COLUMNS" {
			unit = "column"
		}
		return fmt.Sprintf("%s %s %d (indices [%d,%d) on sheet %d)",
			e.Location.LocationType, unit, d.StartIndex+1, d.StartIndex, d.EndIndex, d.SheetID)
	case e.Location.Spreadsheet:
		return e.Location.LocationType + " the whole spreadsheet"
	case e.Location.SheetID != 0:
		return fmt.Sprintf("%s sheet %d", e.Location.LocationType, e.Location.SheetID)
	}
	return e.Location.LocationType + " (no location in the reply)"
}

// searchRaw runs one lookup and returns what matched and the body, so a
// refusal can be printed with its reason.
func searchRaw(ctx context.Context, lookup map[string]any) (int, []metaEntry, string) {
	status, found, body := searchFilters(ctx, []any{map[string]any{"developerMetadataLookup": lookup}})
	return status, found, body
}

// searchFilters sends several filters in one request, which is the only
// way to find out whether the API deduplicates across them.
func searchFilters(ctx context.Context, filters []any) (int, []metaEntry, string) {
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+"/developerMetadata:search",
		map[string]any{"dataFilters": filters})
	if status != 200 {
		return status, nil, body
	}
	var out struct {
		Matched []struct {
			DeveloperMetadata metaEntry `json:"developerMetadata"`
		} `json:"matchedDeveloperMetadata"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return status, nil, body
	}
	found := make([]metaEntry, 0, len(out.Matched))
	for _, m := range out.Matched {
		found = append(found, m.DeveloperMetadata)
	}
	return status, found, body
}

// searchMeta runs one lookup and returns what matched.
func searchMeta(ctx context.Context, lookup map[string]any) (int, []metaEntry) {
	status, found, _ := searchRaw(ctx, lookup)
	return status, found
}

// anchors is the search this spike repeats after every mutation.
func anchors(ctx context.Context, key string) []metaEntry {
	_, found := searchMeta(ctx, map[string]any{"metadataKey": key})
	return found
}

// anchorRow is the 1-based row the first row-anchored entry under a key
// sits on now, or 0. Every step that names a row asks for it rather than
// remembering one: the anchor moves during this run, which is the whole
// point of the run.
func anchorRow(ctx context.Context, key string) int {
	for _, e := range anchors(ctx, key) {
		if d := e.Location.DimensionRange; d != nil {
			return d.StartIndex + 1
		}
	}
	return 0
}

// ids lists the metadata ids behind a search, so a count that changed
// says which entry went.
func ids(found []metaEntry) string {
	if len(found) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(found))
	for _, e := range found {
		parts = append(parts, fmt.Sprintf("%d on %s", e.MetadataID, e.where()))
	}
	return strings.Join(parts, "; ")
}

func summarise(found []metaEntry) string {
	if len(found) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(found))
	for _, e := range found {
		parts = append(parts, fmt.Sprintf("%s at %s", e.MetadataKey, e.where()))
	}
	return strings.Join(parts, "; ")
}

// rowValues reads one row so a location can be checked against what is
// actually on it.
func rowValues(ctx context.Context, rangeA1 string) string {
	status, body := getValues(ctx, rangeA1)
	if status != 200 {
		return fmt.Sprintf("(unreadable: HTTP %d)", status)
	}
	var out struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Values) == 0 {
		return "(empty)"
	}
	b, _ := json.Marshal(out.Values[0])
	return string(b)
}
