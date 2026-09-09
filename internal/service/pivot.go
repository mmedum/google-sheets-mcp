package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Pivot actions.
const (
	PivotAdd    = "add"
	PivotUpdate = "update"
	PivotDelete = "delete"
	PivotList   = "list"
)

// summarizeFunctions are the ways a pivot value can be reduced, in the
// spelling a person writes.
var summarizeFunctions = map[string]string{
	"sum": "SUM", "count": "COUNTA", "count_numbers": "COUNT", "count_unique": "COUNTUNIQUE",
	"average": "AVERAGE", "max": "MAX", "min": "MIN", "median": "MEDIAN",
	"product": "PRODUCT", "stdev": "STDEV", "var": "VAR",
}

// PivotRequest is what manage_pivot_table asks for.
type PivotRequest struct {
	Spreadsheet string
	Sheet       string
	Action      string
	// Anchor is the cell the pivot's top-left corner sits on, and the
	// only name a pivot has: the API gives it no id.
	Anchor string
	Source string
	// Rows and Columns are the columns to group by, named in A1 letters
	// or by the heading text in the source's first row.
	Rows    []string
	Columns []string
	// Values are the summaries, each "B sum" or "B sum as Units sold".
	Values []string
	Layout string
	// Range narrows a listing. A pivot has no index in the API, so
	// finding one means reading cells.
	Range  string
	DryRun bool
}

// PivotRecord is one pivot table, as a listing reports it.
type PivotRecord struct {
	Anchor string `json:"anchor" jsonschema:"the cell the pivot is anchored at, which is the only name it has"`
	Sheet  string `json:"sheet"`
	Source string `json:"source,omitempty" jsonschema:"the range it reads"`
	Output string `json:"output,omitempty" jsonschema:"the rectangle its computed cells cover right now, which changes with the data"`
}

// PivotResult is manage_pivot_table's answer.
type PivotResult struct {
	Summary     string        `json:"summary"`
	Spreadsheet string        `json:"spreadsheet"`
	Action      string        `json:"action"`
	Pivots      []PivotRecord `json:"pivot_tables,omitempty"`
	Anchor      string        `json:"anchor,omitempty"`
	DryRun      bool          `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r PivotResult) Render() string { return r.Summary }

// ManagePivotTable answers manage_pivot_table.
//
// A pivot table is the odd structure in this API: no request of its own,
// no id, and an output whose size is computed rather than stated. Three
// things follow, and all three are spike M's.
//
// Columns are named in A1 and converted here, because the API groups by
// an offset into the source and accepts an offset past its end with a
// 200 — so a caller counting from one would get a pivot that means
// nothing and no error at all.
//
// An anchor inside the source is refused here, because the API takes it
// and evaluates to a circular reference.
//
// Every result reads the anchor's rectangle back afterwards, because the
// output's size is in no request and no reply.
func (s *Service) ManagePivotTable(ctx context.Context, req PivotRequest) (*PivotResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	res := &PivotResult{Spreadsheet: ref.ID, Action: req.Action}
	switch req.Action {
	case PivotList:
		return s.listPivots(ctx, ref, req, res)
	case PivotAdd, PivotUpdate:
		return s.writePivot(ctx, ref, req, res)
	case PivotDelete:
		return s.deletePivot(ctx, ref, req, res)
	}
	return nil, Errorf("invalid", "action %q is not one of add, update, delete, list", req.Action)
}

// writePivot handles add and update, which differ in where the pivot
// they send comes from.
func (s *Service) writePivot(ctx context.Context, ref Reference, req PivotRequest, res *PivotResult) (*PivotResult, error) {
	anchor, props, err := s.pivotAnchor(ctx, ref, req)
	if err != nil {
		return nil, err
	}
	var pivot map[string]any
	if req.Action == PivotUpdate {
		existing, err := s.pivotAt(ctx, ref, props, anchor)
		if err != nil {
			return nil, err
		}
		pivot = existing
	} else {
		// An add onto an occupied anchor is an updateCells like any
		// other: it discards the pivot that was there and everything it
		// drew, and the reply says nothing. There is no overwrite on
		// this tool because there is nothing a caller could want here
		// that update does not do better — so it is a refusal that
		// points at the tool that changes a pivot in place.
		if found, err := s.pivotsIn(ctx, ref, props, anchor); err != nil {
			return nil, err
		} else if len(found) > 0 {
			return nil, Errorf("blocked",
				"a pivot table is already anchored at %s on %q, and adding one there would replace it and "+
					"everything it draws. Use action=update to change it, or delete it first",
				anchorName(anchor), props.Title)
		}
		pivot = map[string]any{}
	}

	source, sourceName, err := s.pivotSource(ctx, ref, props, req, pivot)
	if err != nil {
		return nil, err
	}
	// The refusal the API does not make. Anchored inside its own source
	// a pivot is accepted with a 200 and evaluates to "Circular
	// dependency detected", which is a broken spreadsheet reported as a
	// success.
	if source.Props.SheetID == props.SheetID && source.Rect.Contains(anchor) {
		return nil, Errorf("invalid",
			"the anchor %s is inside the source %s, which makes the pivot read its own output. "+
				"Anchor it outside the source, or on another sheet",
			anchorName(anchor), a1.Format(source.Props.Title, source.Rect))
	}

	changed, err := s.editPivot(ctx, ref, source, req, pivot)
	if err != nil {
		return nil, err
	}
	if req.Action == PivotUpdate && len(changed) == 0 {
		return nil, Errorf("invalid", "update was given nothing to change; pass source, group_rows, group_columns or values")
	}
	body, err := json.Marshal(pivot)
	if err != nil {
		return nil, Errorf("invalid", "the pivot table could not be built: %v", err)
	}

	act := render.PivotAct{
		Action: req.Action, Anchor: anchorName(anchor), Sheet: props.Title,
		Source: sourceName, Changed: changed,
		Groups: append(append([]string{}, req.Rows...), req.Columns...), Values: req.Values,
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.PivotPreview(act)
		return res, nil
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.WritePivot(props.SheetID, anchor.FirstCol, anchor.FirstRow, body)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	// The output is computed, so what it covers is only knowable by
	// looking. Reporting the request's own arguments here would describe
	// a rectangle nobody can rely on.
	act.Output = s.pivotOutput(ctx, ref, props, anchor)
	res.Anchor = act.Anchor
	res.Summary = render.PivotDone(act)
	return res, nil
}

// deletePivot removes a pivot and its whole output.
func (s *Service) deletePivot(ctx context.Context, ref Reference, req PivotRequest, res *PivotResult) (*PivotResult, error) {
	anchor, props, err := s.pivotAnchor(ctx, ref, req)
	if err != nil {
		return nil, err
	}
	existing, err := s.pivotAt(ctx, ref, props, anchor)
	if err != nil {
		return nil, err
	}
	act := render.PivotAct{
		Action: PivotDelete, Anchor: anchorName(anchor), Sheet: props.Title,
		Source: pivotSourceName(existing, props.Title),
		Output: s.pivotOutput(ctx, ref, props, anchor),
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.PivotPreview(act)
		return res, nil
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.ClearPivot(props.SheetID, anchor.FirstCol, anchor.FirstRow)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.Anchor = act.Anchor
	res.Summary = render.PivotDone(act)
	return res, nil
}

// listPivots finds the pivots in a range, or on a sheet.
func (s *Service) listPivots(ctx context.Context, ref Reference, req PivotRequest, res *PivotResult) (*PivotResult, error) {
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	props, err := s.findSheet(sp, strings.TrimSpace(req.Sheet), ref)
	if err != nil {
		return nil, err
	}
	rect := a1.WholeSheet
	if strings.TrimSpace(req.Range) != "" {
		target, err := s.ResolveRange(ctx, ref, props.Title, req.Range)
		if err != nil {
			return nil, err
		}
		props, rect = target.Props, target.Rect
	}
	rows, cols := extent(props)
	window := rect.Clamp(rows, cols)
	found, err := s.pivotsIn(ctx, ref, props, window)
	if err != nil {
		return nil, err
	}
	res.Pivots = found
	res.Summary = render.PivotList(pivotRows(found), props.Title, a1.FormatRect(window))
	return res, nil
}

// pivotsIn reads a rectangle for pivot anchors.
//
// The mask asks for the pivot and nothing else — no values, no formats.
// There is no pivot index in this API, so a listing is a grid read, and
// the only thing that keeps it cheap is asking for one field.
//
// Two requests whatever it finds. The extents used to be measured one
// pivot at a time, so a sheet with ten pivot tables turned one listing
// into eleven round trips; one read of the window measures all of them.
func (s *Service) pivotsIn(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	window a1.Rect) ([]PivotRecord, error) {

	found, err := s.pivotAnchors(ctx, ref, props, window)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	// Where each one reaches, measured once for all of them: a listing
	// that says where a pivot starts and not how far it goes describes
	// half of what a caller has to avoid writing over.
	extents := s.pivotExtents(ctx, ref, props, found)
	out := make([]PivotRecord, 0, len(found))
	for i, p := range found {
		var pivot map[string]any
		_ = json.Unmarshal(p.raw, &pivot)
		out = append(out, PivotRecord{
			Anchor: a1.FormatRect(p.at), Sheet: props.Title,
			Source: pivotSourceName(pivot, props.Title),
			Output: extentName(extents[i]),
		})
	}
	return out, nil
}

// pivotAt reads the pivot anchored at a cell, refusing where there is
// none — an update or a delete aimed at an empty cell is a mistake worth
// naming rather than a write that quietly does nothing.
//
// One request. It used to ask pivotsIn whether a pivot was there, which
// measured that pivot's whole output to build a record nothing read, and
// then re-read the same cell to get the definition: three round trips to
// answer one question, on an API that allows sixty a minute.
func (s *Service) pivotAt(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	anchor a1.Rect) (map[string]any, error) {

	found, err := s.pivotAnchors(ctx, ref, props, anchor)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, Errorf("not_found",
			"no pivot table is anchored at %s on %q. manage_pivot_table list reports the ones that are",
			anchorName(anchor), props.Title)
	}
	var pivot map[string]any
	if err := json.Unmarshal(found[0].raw, &pivot); err != nil {
		return nil, Errorf("unsupported",
			"the pivot table at %s could not be read back, so changing it would lose what it holds",
			anchorName(anchor))
	}
	return pivot, nil
}

// pivotAnchors reads a rectangle for the cells carrying a pivot table.
//
// One place for the mask, the range and the walk, because three callers
// want the same three: the listing, the update that has to read a pivot
// before replacing it, and the look behind a refusal. The mask asks for
// the pivot and nothing else — no values, no formats — since there is no
// pivot index in this API and a listing is a grid read.
func (s *Service) pivotAnchors(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	window a1.Rect) ([]foundPivot, error) {

	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields:          gapi.PivotFields,
		Ranges:          []string{a1.Format(props.Title, window)},
		IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	return pivotCells(sp, props.SheetID), nil
}

// foundPivot is one anchor a read turned up, with the raw definition on
// it. The rectangle travels beside the address, so nothing has to format
// a cell name and parse it back to measure what it draws.
type foundPivot struct {
	at  a1.Rect
	raw json.RawMessage
}

// pivotCells walks a response for the cells carrying a pivot table.
//
// One walk, used by both readers. The two had a copy each, four levels
// deep, and a change to one of them would have been invisible in the
// other.
func pivotCells(sp *gsheets.Spreadsheet, sheetID int) []foundPivot {
	var out []foundPivot
	for _, sh := range sp.Sheets {
		if sh.Properties == nil || sh.Properties.SheetID != sheetID {
			continue
		}
		for _, d := range sh.Data {
			for i, row := range d.RowData {
				if row == nil {
					continue
				}
				for j, cell := range row.Values {
					if cell == nil || len(cell.PivotTable) == 0 {
						continue
					}
					r, c := d.StartRow+i+1, d.StartColumn+j+1
					out = append(out, foundPivot{
						at:  a1.Rect{FirstRow: r, FirstCol: c, LastRow: r, LastCol: c},
						raw: cell.PivotTable,
					})
				}
			}
		}
	}
	return out
}

// pivotOutput measures what one pivot draws right now, as an address.
func (s *Service) pivotOutput(ctx context.Context, ref Reference, props *gsheets.SheetProperties, anchor a1.Rect) string {
	return extentName(s.pivotExtents(ctx, ref, props, []foundPivot{{at: anchor}})[0])
}

// extentName is a measured extent as an address, and "" where the
// measurement found nothing. A pivot that draws nothing has no
// rectangle, and the zero one means the whole sheet.
func extentName(rect a1.Rect) string {
	if !rect.Bounded() {
		return ""
	}
	return a1.FormatRect(rect)
}

// pivotExtents measures what each pivot draws, from one read.
//
// Read rather than derived. The size is computed from the data, so a
// group added inside the source makes it taller with nothing in any
// request saying so, and a rectangle taken from the arguments would be
// wrong the moment somebody edits a row.
//
// Each stops at its first empty row and first empty column, which is the
// difference between measuring a pivot and measuring the sheet. The
// first version took the furthest computed cell anywhere below and right
// of the anchor: a second pivot table, an ARRAYFORMULA's spill or an
// imported range all joined the rectangle, and the result handed that
// back as "a write into it stops the pivot drawing" — steering a caller
// away from cells that were never the pivot's.
//
// A row rather than a cell, because a pivot grouped by columns leaves
// its own top-left cell empty: live, an H1 pivot with a column grouping
// draws nothing at H1 and something at I1.
// It returns no error: a measurement that fails is a rectangle nobody
// can state, not a call that failed. The write already happened.
func (s *Service) pivotExtents(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	pivots []foundPivot) []a1.Rect {

	out := make([]a1.Rect, len(pivots))
	if len(pivots) == 0 {
		return out
	}
	rows, cols := extent(props)
	// One window covering every anchor, so ten pivot tables cost one
	// read rather than ten.
	first := pivots[0].at
	window := a1.Rect{FirstRow: first.FirstRow, FirstCol: first.FirstCol, LastRow: rows, LastCol: cols}
	for _, p := range pivots[1:] {
		window.FirstRow = min(window.FirstRow, p.at.FirstRow)
		window.FirstCol = min(window.FirstCol, p.at.FirstCol)
	}
	window, _ = fit(window, s.cfg.MaxCells)
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields:          gapi.PivotExtentFields,
		Ranges:          []string{a1.Format(props.Title, window)},
		IncludeGridData: true,
	})
	if err != nil {
		return out
	}

	// drawn[row][col], one-based, for the cells a pivot computes: an
	// effective value and nothing entered.
	drawn := map[int]map[int]bool{}
	for _, sh := range sp.Sheets {
		for _, d := range sh.Data {
			for i, row := range d.RowData {
				if row == nil {
					continue
				}
				for j, cell := range row.Values {
					// The same predicate the write guard triggers on, so
					// the cells it asks about and the cells measured here
					// cannot come apart.
					if !grid.Computed(cell) {
						continue
					}
					r, c := d.StartRow+i+1, d.StartColumn+j+1
					if drawn[r] == nil {
						drawn[r] = map[int]bool{}
					}
					drawn[r][c] = true
				}
			}
		}
	}
	anchors := make([]a1.Rect, len(pivots))
	for i, p := range pivots {
		anchors[i] = p.at
	}
	for i, p := range pivots {
		out[i] = extentFrom(drawn, anchors, p.at, window)
	}
	return out
}

// extentFrom grows a rectangle from one anchor until a row and then a
// column has nothing drawn in it, then pulls it back off any other
// anchor it swallowed. The zero rectangle means the pivot draws nothing.
//
// The pull-back is the second half of the lesson the first half carries.
// Stopping at empty separates a pivot from a spill beside it, and it
// cannot separate two pivots with no gap between them: side by side,
// every row of the left one has something drawn to its right, so the
// left one grew over the right one and the listing said so. Harmless
// while it only decorated a listing; a refusal naming the wrong table
// and promising to break it is not.
func extentFrom(drawn map[int]map[int]bool, anchors []a1.Rect, anchor, window a1.Rect) a1.Rect {
	lastRow := anchor.FirstRow - 1
	for r := anchor.FirstRow; r <= window.LastRow; r++ {
		if !anyFrom(drawn[r], anchor.FirstCol) {
			break
		}
		lastRow = r
	}
	if lastRow < anchor.FirstRow {
		return a1.Rect{}
	}
	lastCol := anchor.FirstCol - 1
	for c := anchor.FirstCol; c <= window.LastCol; c++ {
		used := false
		for r := anchor.FirstRow; r <= lastRow; r++ {
			if drawn[r][c] {
				used = true
				break
			}
		}
		if !used {
			break
		}
		lastCol = c
	}
	if lastCol < anchor.FirstCol {
		return a1.Rect{}
	}
	lastRow, lastCol = clipToNeighbours(anchors, anchor, lastRow, lastCol)
	if lastRow < anchor.FirstRow || lastCol < anchor.FirstCol {
		return a1.Rect{}
	}
	return a1.Rect{
		FirstRow: anchor.FirstRow, FirstCol: anchor.FirstCol, LastRow: lastRow, LastCol: lastCol,
	}
}

// clipToNeighbours pulls a measured rectangle back off any other pivot's
// anchor inside it. Two pivots never overlap, so an anchor within this
// rectangle is proof the walk went too far.
//
// Which side to give up is read off where the other anchor sits. One
// starting on this pivot's own first row is beside it, so the columns
// are what went too far; one in this pivot's own first column is below
// it, so the rows are. An anchor strictly inside on both axes says only
// that the rectangle is too big and not which way, and both are pulled
// back — the rectangle then understates what the pivot draws, which
// costs a sentence, where overstating it names a table the caller would
// not have broken.
func clipToNeighbours(anchors []a1.Rect, anchor a1.Rect, lastRow, lastCol int) (int, int) {
	for _, o := range anchors {
		if o == anchor || o.FirstRow < anchor.FirstRow || o.FirstCol < anchor.FirstCol {
			continue
		}
		if o.FirstRow > lastRow || o.FirstCol > lastCol {
			continue
		}
		if o.FirstRow > anchor.FirstRow {
			lastRow = min(lastRow, o.FirstRow-1)
		}
		if o.FirstCol > anchor.FirstCol {
			lastCol = min(lastCol, o.FirstCol-1)
		}
	}
	return lastRow, lastCol
}

// anyFrom says whether a row has a drawn cell at or right of a column.
func anyFrom(row map[int]bool, from int) bool {
	for c := range row {
		if c >= from {
			return true
		}
	}
	return false
}

// pivotAnchor resolves the anchor argument to one cell.
func (s *Service) pivotAnchor(ctx context.Context, ref Reference, req PivotRequest) (a1.Rect, *gsheets.SheetProperties, error) {
	anchor := strings.TrimSpace(req.Anchor)
	if anchor == "" {
		return a1.Rect{}, nil, Errorf("invalid",
			"a pivot table is named by the cell it is anchored at, so anchor is needed; "+
				"manage_pivot_table list reports the anchors that exist")
	}
	target, err := s.ResolveRange(ctx, ref, req.Sheet, anchor)
	if err != nil {
		return a1.Rect{}, nil, err
	}
	r := target.Rect
	if !r.OneCell() {
		return a1.Rect{}, nil, Errorf("invalid", "anchor is one cell, such as E1, and %q is a range", anchor)
	}
	return r, target.Props, nil
}

// pivotSource resolves the source range, falling back to the one the
// pivot already has when an update does not name a new one.
func (s *Service) pivotSource(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req PivotRequest, pivot map[string]any) (SheetRef, string, error) {

	if strings.TrimSpace(req.Source) != "" {
		target, err := s.ResolveRange(ctx, ref, props.Title, req.Source)
		if err != nil {
			return SheetRef{}, "", err
		}
		if !target.Rect.Bounded() {
			return SheetRef{}, "", Errorf("invalid",
				"the source %s has no end, and a pivot reads a rectangle; give it one, such as A1:C200", req.Source)
		}
		return target, a1.Format(target.Props.Title, target.Rect), nil
	}
	if req.Action == PivotAdd {
		return SheetRef{}, "", Errorf("invalid", "add needs source, the block of data to summarise")
	}
	// An update that leaves the source alone still needs it, because
	// every column it names is an offset into that rectangle.
	g, ok := gridRangeOf(pivot)
	if !ok {
		return SheetRef{}, "", Errorf("invalid",
			"this pivot table has no source range this server could read, so a column cannot be resolved against it")
	}
	sheet := s.sheetByID(ctx, ref.ID, g.SheetID)
	if sheet == nil {
		sheet = props
	}
	rect := a1.FromGridRange(g)
	return SheetRef{Props: sheet, Rect: rect}, a1.Format(sheet.Title, rect), nil
}

// gridRangeOf reads a raw pivot's source rectangle.
//
// One decode for the two readers of it: they had drifted into different
// error handling for the same three lines, which is how a message ends
// up describing what the other one does.
func gridRangeOf(pivot map[string]any) (*gsheets.GridRange, bool) {
	raw, ok := pivot["source"]
	if !ok {
		return nil, false
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var g gsheets.GridRange
	if err := json.Unmarshal(body, &g); err != nil {
		return nil, false
	}
	return &g, true
}

// editPivot applies the arguments, and says what it changed.
func (s *Service) editPivot(ctx context.Context, ref Reference, source SheetRef,
	req PivotRequest, pivot map[string]any) ([]string, error) {

	var changed []string
	if strings.TrimSpace(req.Source) != "" || req.Action == PivotAdd {
		pivot["source"] = source.Rect.GridRange(source.Props.SheetID)
		changed = append(changed, "source")
	}
	headers, err := s.pivotHeaders(ctx, ref, source, req)
	if err != nil {
		return nil, err
	}
	for _, group := range []struct {
		field string
		names []string
		arg   string
	}{
		{"rows", req.Rows, "group_rows"},
		{"columns", req.Columns, "group_columns"},
	} {
		if len(group.names) == 0 {
			continue
		}
		var built []any
		for _, name := range group.names {
			offset, err := pivotOffset(name, source.Rect, headers, group.arg)
			if err != nil {
				return nil, err
			}
			built = append(built, &gsheets.PivotGroup{
				SourceColumnOffset: offset, ShowTotals: true, SortOrder: "ASCENDING",
			})
		}
		pivot[group.field] = built
		changed = append(changed, group.arg)
	}
	if len(req.Values) > 0 {
		var built []any
		for _, spec := range req.Values {
			value, err := pivotValue(spec, source.Rect, headers)
			if err != nil {
				return nil, err
			}
			built = append(built, value)
		}
		pivot["values"] = built
		changed = append(changed, "values")
	}
	if layout := strings.ToLower(strings.TrimSpace(req.Layout)); layout != "" {
		switch layout {
		case "horizontal", "vertical":
			pivot["valueLayout"] = strings.ToUpper(layout)
			changed = append(changed, "layout")
		default:
			return nil, Errorf("invalid", "layout %q is horizontal or vertical", req.Layout)
		}
	}
	if req.Action == PivotAdd {
		if _, ok := pivot["values"]; !ok {
			return nil, Errorf("invalid",
				"add needs values, at least one column to summarise, such as \"B sum\"")
		}
		_, hasRows := pivot["rows"]
		_, hasCols := pivot["columns"]
		if !hasRows && !hasCols {
			return nil, Errorf("invalid",
				"add needs group_rows or group_columns, the column(s) to group by. Without one the pivot is a "+
					"single total, which a formula says more clearly")
		}
	}
	return changed, nil
}

// pivotHeaders reads the source's first row, so a column can be named by
// its heading rather than by a letter.
//
// Only when something asks for it: every name given as a column letter
// resolves without a read, and this call is skipped entirely.
func (s *Service) pivotHeaders(ctx context.Context, ref Reference, source SheetRef, req PivotRequest) (map[string]int, error) {
	wanted := append(append(append([]string{}, req.Rows...), req.Columns...), req.Values...)
	needed := false
	for _, name := range wanted {
		field, _, _ := strings.Cut(strings.TrimSpace(name), " ")
		if _, err := a1.ParseColumn(field); err != nil {
			needed = true
		}
	}
	if !needed {
		return nil, nil
	}
	head := source.Rect
	head.LastRow = head.FirstRow
	values, err := s.api.GetValues(ctx, ref.ID, a1.Format(source.Props.Title, head), gapi.ValueOptions{})
	if err != nil {
		return nil, wrap(err)
	}
	out := map[string]int{}
	if len(values.Values) == 0 {
		return out, nil
	}
	for i, cell := range values.Values[0] {
		text := strings.TrimSpace(fmt.Sprint(cell))
		if text == "" {
			continue
		}
		if _, seen := out[strings.ToLower(text)]; !seen {
			out[strings.ToLower(text)] = i
		}
	}
	return out, nil
}

// pivotOffset turns a column letter or a heading into an offset into the
// source, and refuses one outside it.
//
// The refusal is this server's alone: an offset past the source's width
// is accepted by the API with a 200, and produces a pivot that reads
// nothing (spike M).
func pivotOffset(name string, source a1.Rect, headers map[string]int, arg string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, Errorf("invalid", "%s has an empty column in it", arg)
	}
	if _, err := a1.ParseColumn(name); err == nil {
		// columnOffset treats a rectangle with no left edge as starting
		// at column A. Subtracting a zero gave an offset one too high
		// *and* disabled the bounds check, which is the exact failure
		// this refusal exists for; it reaches here through an update
		// whose pivot was built over a whole-sheet range in the Sheets
		// interface.
		offset, ok := columnOffset(name, source)
		if !ok {
			return 0, Errorf("invalid",
				"column %s is outside the source %s, so it is not a column this pivot table can group or summarise",
				strings.ToUpper(name), a1.FormatRect(source))
		}
		return offset, nil
	}
	if offset, ok := headers[strings.ToLower(name)]; ok {
		return offset, nil
	}
	if len(headers) == 0 {
		return 0, Errorf("invalid", "%q is neither a column letter nor a heading in the source's first row", name)
	}
	var names []string
	for k := range headers {
		names = append(names, fmt.Sprintf("%q", k))
	}
	return 0, Errorf("not_found", "no column %q in the source; its first row holds %s", name, join(names))
}

// pivotValue parses "B sum" or "B sum as Units sold".
func pivotValue(spec string, source a1.Rect, headers map[string]int) (*gsheets.PivotValue, error) {
	spec = strings.TrimSpace(spec)
	body, name, hasName := strings.Cut(spec, " as ")
	field, fn, ok := strings.Cut(strings.TrimSpace(body), " ")
	if !ok {
		return nil, Errorf("invalid",
			"%q does not say how to summarise the column; write it as \"B sum\", and add \"as Total\" to name it",
			spec)
	}
	summarize, ok := summarizeFunctions[strings.ToLower(strings.TrimSpace(fn))]
	if !ok {
		return nil, Errorf("invalid", "%q is not a summary this server offers: %s",
			strings.TrimSpace(fn), join(summarizeNames()))
	}
	offset, err := pivotOffset(field, source, headers, "values")
	if err != nil {
		return nil, err
	}
	value := &gsheets.PivotValue{SourceColumnOffset: offset, SummarizeFunction: summarize}
	if hasName {
		value.Name = strings.TrimSpace(name)
	}
	return value, nil
}

func summarizeNames() []string {
	out := make([]string, 0, len(summarizeFunctions))
	for k := range summarizeFunctions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pivotSourceName reads the source out of a raw pivot, for a message.
func pivotSourceName(pivot map[string]any, sheet string) string {
	g, ok := gridRangeOf(pivot)
	if !ok {
		return ""
	}
	return a1.Format(sheet, a1.FromGridRange(g))
}

// anchorName is a one-cell rectangle in A1.
func anchorName(anchor a1.Rect) string { return a1.FormatRect(anchor) }

// pivotRows hands the renderer its own view of a listing.
func pivotRows(found []PivotRecord) []render.PivotRow {
	out := make([]render.PivotRow, 0, len(found))
	for _, p := range found {
		out = append(out, render.PivotRow{Anchor: p.Anchor, Source: p.Source, Output: p.Output})
	}
	return out
}

// pivotsBehind names the pivot tables a refused write would land in the
// output of.
//
// A pivot's output cells say nothing about it: on the wire they are
// ordinary computed values, and the definition sits on the anchor
// alone, up and to the left of what the guard read. So naming one means
// a second read, and this pays for it only where the write is already
// being refused — once per refusal rather than once per write, which is
// the shape §17a.27 settled on.
//
// It answers with what it found and never with an error. The write is
// refused either way, so a look that fails costs a sentence and nothing
// else; turning a [blocked] into a transport error would take the
// refusal the caller has to read and replace it with one about the
// server.
func (s *Service) pivotsBehind(ctx context.Context, t target, r *plan.Report, ack plan.Ack) {
	// Three tests, and none of them is enough alone. A refusal over
	// cells somebody typed has no pivot behind it. A write nothing is
	// refusing has nothing to explain. And a caller who has already
	// passed overwrite would be told to pass it again, since that is the
	// only flag this finding can ask for — so the reads are skipped
	// rather than paid for a sentence Blockers then withholds.
	if !r.Computed || ack.Overwrite || !t.rect.Bounded() || len(r.Blockers(ack)) == 0 {
		return
	}
	for _, p := range s.pivotsCovering(ctx, t) {
		r.AddDrawnBy(p.Anchor, p.Output, p.Hit)
	}
}

// pivotsCovering is the lookup itself: the pivot tables anchored outside
// a rectangle whose output reaches into it.
//
// Separate from pivotsBehind's gating because two callers want the same
// two reads for different reasons. A values write asks only where it is
// already being refused, because the answer improves a message. A merge
// asks whenever the rectangle holds a cell nobody typed, because Sheets
// refuses that merge outright and the caller has to be told which table
// is in the way.
//
// It answers with what it found and never with an error. The call it
// serves is refused either way, so a look that fails costs a sentence
// and nothing else; turning a [blocked] into a transport error would
// take the refusal the caller has to read and replace it with one about
// the server.
func (s *Service) pivotsCovering(ctx context.Context, t target) []plan.PivotOutput {
	if !t.rect.Bounded() {
		return nil
	}
	anchors, err := s.pivotAnchors(ctx, t.ref, t.props, lookback(t.rect, s.cfg.MaxCells))
	if err != nil {
		s.log.DebugContext(ctx, "pivot lookup behind a refusal failed", "spreadsheet", gapi.ShortID(t.ref.ID))
		return nil
	}
	var found []foundPivot
	for _, p := range anchors {
		// An anchor inside the rectangle is already visible to whoever
		// read it, and is named from there. Naming it again here would
		// put two sentences about one pivot table in one refusal.
		if !t.rect.Contains(p.at) {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return nil
	}
	// The extents, because an anchor up and to the left is not yet a
	// pivot that reaches this rectangle: a table two columns wide says
	// nothing about a write ten columns along, and claiming it did would
	// steer a caller away from cells that were never the pivot's.
	var out []plan.PivotOutput
	for i, rect := range s.pivotExtents(ctx, t.ref, t.props, found) {
		if !rect.Bounded() {
			continue
		}
		hit, ok := rect.Intersect(t.rect)
		if !ok {
			continue
		}
		out = append(out, plan.PivotOutput{
			Anchor: a1.FormatRect(found[i].at),
			Output: a1.FormatRect(rect),
			Hit:    a1.FormatRect(hit),
		})
	}
	return out
}

// lookback is where the anchor of a pivot drawing into a write can be:
// the first row and the first column up to the write's far corner,
// since a pivot grows down and to the right of its anchor.
//
// Bounded by the read budget from the write's end rather than the
// sheet's start. A pivot anchored further above than the budget reaches
// draws an output at least that tall, so what the bound gives up is a
// pivot taller than a whole read — and the tools that list one measure
// it under the same budget.
func lookback(rect a1.Rect, maxCells int) a1.Rect {
	window := a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: rect.LastRow, LastCol: rect.LastCol}
	window, _ = window.LimitRowsFromEnd(max(maxCells/window.Cols(), 1))
	return window
}
