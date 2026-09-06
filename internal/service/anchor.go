package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Anchor actions.
const (
	AnchorAdd    = "add"
	AnchorList   = "list"
	AnchorMove   = "move"
	AnchorRemove = "remove"
)

// AnchorRequest is what manage_anchor asks for.
type AnchorRequest struct {
	Spreadsheet string
	Action      string
	Name        string
	Note        string
	Sheet       string
	Range       string
	DryRun      bool
}

// AnchorResult is its answer.
type AnchorResult struct {
	Summary     string         `json:"summary" jsonschema:"what happened, or what would have"`
	Spreadsheet string         `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Action      string         `json:"action" jsonschema:"the action taken"`
	Anchors     []AnchorRecord `json:"anchors,omitempty" jsonschema:"the anchors this call added, moved, removed or listed"`
	DryRun      bool           `json:"dry_run,omitempty" jsonschema:"true when nothing was sent"`
}

// AnchorRecord is one anchor as a caller sees it.
type AnchorRecord struct {
	Name string `json:"name" jsonschema:"the label"`
	Note string `json:"note,omitempty" jsonschema:"what was recorded alongside the label"`
	// Range is the A1 the anchor stands for now — a whole row or a whole
	// column — and it changes as the sheet is edited. That is the point
	// of an anchor and the reason it is reported on every call.
	Range string `json:"range,omitempty" jsonschema:"where the anchor points now, in A1. A row anchor moves as rows are inserted, deleted, moved and sorted above it"`
	Sheet string `json:"sheet,omitempty" jsonschema:"the sheet it is on, when it is on one"`
	Scope string `json:"scope" jsonschema:"row, column, sheet or spreadsheet"`
}

// Render is the text half.
func (r AnchorResult) Render() string { return r.Summary }

// Anchors answers manage_anchor.
//
// An anchor is developer metadata, which is the only thing Google keeps
// attached to a location while the sheet is edited around it (§6.4). It
// is a label rather than an address: "invoice totals" survives twenty
// rows being inserted above it, where row 40 does not.
//
// It is never the only way to reach anything. Every tool that takes an
// anchor takes A1 as well, and reading anchors needs a write-capable
// scope, so a read-only server offers none of this.
func (s *Service) Anchors(ctx context.Context, req AnchorRequest) (*AnchorResult, error) {
	action, err := parseAnchorAction(req.Action)
	if err != nil {
		return nil, err
	}
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	res := &AnchorResult{Spreadsheet: ref.ID, Action: action, DryRun: req.DryRun}

	switch action {
	case AnchorList:
		found, err := s.listAnchors(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		// A listing sends a search whatever the flag says, and the flag
		// means "nothing was sent". Reporting it on a read would be a
		// small lie in the one field a client uses to decide whether
		// anything happened.
		res.DryRun = false
		res.Anchors = found
		res.Summary = render.AnchorList(anchorViews(found))
		return res, nil
	case AnchorRemove:
		return s.removeAnchor(ctx, ref.ID, req, res)
	}
	return s.placeAnchor(ctx, ref, req, action, res)
}

// placeAnchor adds an anchor or moves an existing one.
func (s *Service) placeAnchor(ctx context.Context, ref Reference, req AnchorRequest, action string, res *AnchorResult) (*AnchorResult, error) {
	if err := plan.CheckAnchorName(req.Name); err != nil {
		return nil, Errorf("invalid", "%s", err)
	}
	at, props, err := s.anchorTarget(ctx, ref, req)
	if err != nil {
		return nil, err
	}
	existing, err := s.findAnchor(ctx, ref.ID, req.Name)
	if err != nil {
		return nil, err
	}
	// A name is not unique to Google — two entries may share a key, and
	// spike K made two to be sure — so uniqueness is this server's to
	// keep. Without it, "read anchor:totals" is a question with two
	// answers and no way to choose.
	switch {
	case action == AnchorAdd && existing != nil:
		return nil, Errorf("invalid",
			"this spreadsheet already has an anchor called %q, on %s. Use action=move to point it somewhere else, "+
				"or choose another name", req.Name, s.anchorWhere(ctx, ref.ID, existing))
	case action == AnchorMove && existing == nil:
		return nil, Errorf("not_found",
			"this spreadsheet has no anchor called %q; action=add creates one, and action=list shows what it has",
			req.Name)
	}

	// The note reported is the one that will be stored, which for a move
	// with no note of its own is the one already there. Reporting the
	// request's empty note would say a note had gone when it had not,
	// and reporting a new note that the field mask never wrote would say
	// one had landed when it had not — hard rule 7 either way.
	note := req.Note
	if action == AnchorMove && note == "" && existing != nil {
		note = existing.MetadataValue
	}
	record := AnchorRecord{
		Name: req.Name, Note: note, Sheet: props.Title,
		Range: anchorSpan(at, props), Scope: strings.ToLower(at.Kind),
	}
	res.Anchors = []AnchorRecord{record}

	if req.DryRun {
		res.Summary = render.AnchorAct(render.AnchorActed{
			Action: action, Preview: true, Anchor: anchorView(record),
		})
		return res, nil
	}
	loc := at.Location(props.SheetID)
	op := plan.AnchorAdd(req.Name, req.Note, loc)
	if action == AnchorMove {
		op = plan.AnchorRetarget(existing.MetadataID, loc, req.Note)
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{op},
	}); err != nil {
		return nil, wrap(err)
	}
	res.Summary = render.AnchorAct(render.AnchorActed{Action: action, Anchor: anchorView(record)})
	return res, nil
}

// removeAnchor deletes one by name.
func (s *Service) removeAnchor(ctx context.Context, id string, req AnchorRequest, res *AnchorResult) (*AnchorResult, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, Errorf("invalid", "name the anchor to remove; action=list shows what this spreadsheet has")
	}
	existing, err := s.findAnchor(ctx, id, req.Name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, Errorf("not_found", "this spreadsheet has no anchor called %q", req.Name)
	}
	record := s.anchorRecord(ctx, id, existing)
	res.Anchors = []AnchorRecord{record}
	if req.DryRun {
		res.Summary = render.AnchorAct(render.AnchorActed{
			Action: AnchorRemove, Preview: true, Anchor: anchorView(record),
		})
		return res, nil
	}
	// Removing an anchor destroys a label and no data: the row it named
	// is untouched, and re-anchoring it costs one call. So it is a plain
	// write rather than a guarded one — the guard exists for what cannot
	// be put back.
	if _, err := s.api.BatchUpdate(ctx, id, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.AnchorDelete(existing.MetadataID)},
	}); err != nil {
		return nil, wrap(err)
	}
	res.Summary = render.AnchorAct(render.AnchorActed{Action: AnchorRemove, Anchor: anchorView(record)})
	return res, nil
}

// anchorTarget turns the caller's range into the position an anchor can
// attach to: a single row, a single column, or the sheet.
//
// The API refuses anything else, and says so in words that name a type
// the caller never mentioned — "DimensionRange must represent a single
// row or column" — so the refusal is written here, where the range that
// caused it can be quoted back.
func (s *Service) anchorTarget(ctx context.Context, ref Reference, req AnchorRequest) (plan.AnchorAt, *gsheets.SheetProperties, error) {
	sh, err := s.ResolveRange(ctx, ref, req.Sheet, req.Range)
	if err != nil {
		return plan.AnchorAt{}, nil, err
	}
	props := sh.Props
	if strings.TrimSpace(req.Range) == "" {
		return plan.AnchorAt{SheetID: props.SheetID, Kind: gsheets.LocationSheet}, props, nil
	}
	rows, cols := extent(props)
	rect := sh.Rect.Clamp(rows, cols)
	switch {
	case rect.Rows() == 1:
		// One row wins a tie. A single cell is one row and one column
		// at once, and "remember this row" is what §6.4 offers; the
		// result names the row it took, so a caller who meant the
		// column can see that in the answer rather than later.
		return plan.AnchorAt{SheetID: props.SheetID, Row: rect.FirstRow, Kind: gsheets.LocationRow}, props, nil
	case rect.Cols() == 1:
		return plan.AnchorAt{SheetID: props.SheetID, Col: rect.FirstCol, Kind: gsheets.LocationColumn}, props, nil
	}
	// Both spellings, built by a1 rather than spelled here: the column
	// half used to go through a helper that swallowed a1.ColumnName's
	// error and substituted "A", in a message whose whole job is to name
	// the right column.
	row := a1.Rect{FirstRow: rect.FirstRow, LastRow: rect.FirstRow}
	col := a1.Rect{FirstCol: rect.FirstCol, LastCol: rect.FirstCol}
	return plan.AnchorAt{}, nil, Errorf("invalid",
		"%s is %d rows by %d columns, and an anchor attaches to one row, one column or a whole sheet — never a "+
			"rectangle. Anchor the row as %s, a column as %s, or leave range empty for the sheet",
		a1.FormatRect(rect), rect.Rows(), rect.Cols(), a1.FormatRect(row), a1.FormatRect(col))
}

// findAnchor resolves a name to at most one entry, refusing a name that
// matches several.
func (s *Service) findAnchor(ctx context.Context, id, name string) (*gsheets.DeveloperMetadata, error) {
	res, err := s.api.SearchDeveloperMetadata(ctx, id, plan.ByName(name))
	if err != nil {
		return nil, wrap(err)
	}
	found := entries(res)
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], nil
	}
	// Another application can write metadata into the same spreadsheet
	// under any key it likes, so this is reachable however careful this
	// server is with its own writes.
	where := make([]string, 0, len(found))
	for _, e := range found {
		where = append(where, s.anchorWhere(ctx, id, e))
	}
	return nil, Errorf("ambiguous", "%d anchors in this spreadsheet are called %q: %s. Remove the ones you do not "+
		"want, or address the row in A1 instead", len(found), name, strings.Join(where, ", "))
}

// listAnchors reads every anchor in the spreadsheet, in one request.
func (s *Service) listAnchors(ctx context.Context, id string) ([]AnchorRecord, error) {
	res, err := s.api.SearchDeveloperMetadata(ctx, id, plan.Everything())
	if err != nil {
		return nil, wrap(err)
	}
	found := entries(res)
	out := make([]AnchorRecord, 0, len(found))
	for _, e := range found {
		out = append(out, s.anchorRecord(ctx, id, e))
	}
	// Google returns them in no order anybody can rely on, and a listing
	// that reorders itself between calls cannot be diffed by eye.
	slices.SortFunc(out, func(a, b AnchorRecord) int {
		if c := strings.Compare(a.Sheet, b.Sheet); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// anchorRecord turns an entry into what a caller sees, resolving the
// sheet id to the title the rest of this server speaks in.
func (s *Service) anchorRecord(ctx context.Context, id string, md *gsheets.DeveloperMetadata) AnchorRecord {
	rec := AnchorRecord{Name: md.MetadataKey, Note: md.MetadataValue, Scope: "spreadsheet"}
	at := plan.Where(md)
	if at.Kind == gsheets.LocationSpreadsheet || at.Kind == "" {
		return rec
	}
	props := s.sheetByID(ctx, id, at.SheetID)
	if props == nil {
		// The sheet is gone, so the anchor is unreachable. Saying so
		// beats printing a bare number nothing else here accepts.
		rec.Scope = strings.ToLower(at.Kind)
		rec.Sheet = fmt.Sprintf("(sheet id %d, no longer in this spreadsheet)", at.SheetID)
		return rec
	}
	rec.Sheet, rec.Scope = props.Title, strings.ToLower(at.Kind)
	rec.Range = anchorSpan(at, props)
	return rec
}

// anchorWhere is a one-line description of an entry, for a refusal.
//
// Through the renderer's own phrasing rather than a second cascade
// beside it: a refusal and a listing that describe the same anchor
// differently is the shape §17a.9 moved the structural tools' English
// here to stop.
func (s *Service) anchorWhere(ctx context.Context, id string, md *gsheets.DeveloperMetadata) string {
	return anchorView(s.anchorRecord(ctx, id, md)).Where()
}

// sheetByID finds a sheet's properties from the cached card.
func (s *Service) sheetByID(ctx context.Context, id string, sheetID int) *gsheets.SheetProperties {
	sp, err := s.card(ctx, id)
	if err != nil {
		return nil
	}
	for _, sh := range sp.Sheets {
		if sh.Properties != nil && sh.Properties.SheetID == sheetID {
			return sh.Properties
		}
	}
	return nil
}

// anchorSpan is the A1 an anchor covers on this sheet. A row anchor
// spans the sheet's columns and a column anchor its rows, because that
// is what the anchor actually names; a sheet anchor covers no rectangle
// and gets no range.
func anchorSpan(at plan.AnchorAt, props *gsheets.SheetProperties) string {
	rows, cols := extent(props)
	rect, ok := at.Rect(rows, cols)
	if !ok {
		return ""
	}
	return a1.Format(props.Title, rect)
}

func entries(res *gsheets.SearchDeveloperMetadataResponse) []*gsheets.DeveloperMetadata {
	if res == nil {
		return nil
	}
	out := make([]*gsheets.DeveloperMetadata, 0, len(res.MatchedDeveloperMetadata))
	for _, m := range res.MatchedDeveloperMetadata {
		if m != nil && m.DeveloperMetadata != nil {
			out = append(out, m.DeveloperMetadata)
		}
	}
	return out
}

func anchorViews(recs []AnchorRecord) []render.Anchor {
	out := make([]render.Anchor, 0, len(recs))
	for _, r := range recs {
		out = append(out, anchorView(r))
	}
	return out
}

func anchorView(r AnchorRecord) render.Anchor {
	return render.Anchor{Name: r.Name, Note: r.Note, Range: r.Range, Sheet: r.Sheet, Scope: r.Scope}
}

func parseAnchorAction(v string) (string, error) {
	switch v {
	case AnchorAdd, AnchorList, AnchorMove, AnchorRemove:
		return v, nil
	case "":
		return "", Errorf("invalid", "name an action: add, list, move or remove")
	}
	return "", Errorf("invalid", "action %q is not one of add, list, move, remove", v)
}

// AnchorPrefix marks a range that names an anchor instead of an address.
//
// A prefix on the existing range argument rather than an argument of its
// own. Every tool that takes a range gets anchors from one change in
// ResolveRange, and none of them grows a nineteenth argument — §17a.12
// is already the complaint that transform_range takes twenty-one.
//
// The string itself lives in a1, because it is range syntax and because
// the renderer says it to the caller and cannot import this package.
const AnchorPrefix = a1.AnchorPrefix

// resolveAnchorRange turns anchor:<name> into the rectangle it points at
// now.
func (s *Service) resolveAnchorRange(ctx context.Context, ref Reference, sheet, name string) (SheetRef, error) {
	name = strings.TrimSpace(name)
	at, err := s.anchorPosition(ctx, ref.ID, name)
	if err != nil {
		return SheetRef{}, err
	}
	if at.Kind == gsheets.LocationSpreadsheet || at.Kind == "" {
		return SheetRef{}, Errorf("invalid",
			"the anchor %q is on the whole spreadsheet, which is not a range to read or write. Anchor a row or a "+
				"column, or pass an A1 range", name)
	}
	props := s.sheetByID(ctx, ref.ID, at.SheetID)
	if props == nil {
		return SheetRef{}, Errorf("not_found",
			"the anchor %q points at a sheet this spreadsheet no longer has; manage_anchor action=remove clears it", name)
	}
	// A sheet named twice, disagreeing, is the same mistake the A1 path
	// refuses: the caller has two ideas about where this is.
	//
	// Resolved through findSheet rather than compared as strings, so the
	// three ways to name a sheet all work here — a title, a numeric id,
	// and the gid from a URL. Comparing strings missed the gid, which
	// meant a URL carrying #gid=N plus an anchor on another sheet was
	// accepted and quietly read the anchor's sheet, where the A1 path
	// refuses the same conflict.
	if want := strings.TrimSpace(sheet); want != "" || ref.HasGid {
		sp, err := s.card(ctx, ref.ID)
		if err != nil {
			return SheetRef{}, err
		}
		named, err := s.findSheet(sp, want, ref)
		if err != nil {
			return SheetRef{}, err
		}
		if named.SheetID != props.SheetID {
			return SheetRef{}, Errorf("invalid",
				"the anchor %q is on sheet %q and the call names sheet %q; pass the sheet once, or leave it out",
				name, props.Title, named.Title)
		}
	}
	rows, cols := extent(props)
	rect, ok := at.Rect(rows, cols)
	if !ok {
		// A sheet anchor stands for the whole sheet, which is what a
		// bare range means everywhere else here.
		rect = a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: rows, LastCol: cols}
	}
	return SheetRef{Props: props, Rect: rect}, nil
}

// anchorsOnSheet names every anchor a sheet delete would take: the
// sheet's own, and every row and column anchor on it.
//
// The same principle as a band delete, one level up. Deleting a sheet
// takes all of them and the reply mentions none, so the confirmation
// has to — otherwise `manage_anchor list` afterwards is quietly shorter
// and nothing said why.
func (s *Service) anchorsOnSheet(ctx context.Context, id string, sheetID int) ([]string, error) {
	res, err := s.api.SearchDeveloperMetadata(ctx, id, plan.OnSheet(sheetID))
	if err != nil {
		return nil, wrap(err)
	}
	var names []string
	for _, md := range entries(res) {
		names = append(names, md.MetadataKey)
	}
	slices.Sort(names)
	return names, nil
}

// anchorsOnBand names the anchors a delete would take with the rows or
// columns it removes.
//
// Deleting a row deletes the anchor on it, and nothing in Google's reply
// mentions it (spike K). That makes it the same class as spike J's
// `deleteTable`: an act whose cost is invisible in the response, which
// is the definition of what this server's guard is for. One search, and
// only on a delete.
func (s *Service) anchorsOnBand(ctx context.Context, id string, sheetID int, b plan.Band) ([]string, error) {
	res, err := s.api.SearchDeveloperMetadata(ctx, id, plan.OnSheet(sheetID))
	if err != nil {
		return nil, wrap(err)
	}
	var names []string
	for _, md := range entries(res) {
		at := plan.Where(md)
		pos := at.Row
		if !b.Rows() {
			pos = at.Col
		}
		// A row anchor is untouched by a column delete and the other way
		// round. The axis needs no separate check: plan.Where fills Row
		// only for a row anchor and Col only for a column one, so a
		// non-zero position on this axis is already the right kind.
		if pos == 0 || pos < b.First || pos > b.Last {
			continue
		}
		names = append(names, md.MetadataKey)
	}
	slices.Sort(names)
	return names, nil
}

// resolveBand turns the caller's band into a plan.Band, accepting an
// anchor wherever an A1 band works.
//
// This is the other half of the promise `anchor:` makes. Ranges go
// through ResolveRange, which is one chokepoint for seven tools; a band
// is a different argument on a different path, and without this the two
// tools that take one would refuse an anchor with an A1 syntax error —
// on `delete_dimensions`, which is exactly the tool that destroys
// anchors and now names them. "Delete the row anchored as totals" is the
// sentence the feature is for.
func (s *Service) resolveBand(ctx context.Context, id string, props *gsheets.SheetProperties, dimension, band string) (plan.Band, error) {
	name, ok := strings.CutPrefix(strings.TrimSpace(band), AnchorPrefix)
	if !ok {
		b, err := plan.ParseBand(dimension, band)
		if err != nil {
			return plan.Band{}, Errorf("invalid", "%s", err)
		}
		return b, nil
	}
	at, err := s.anchorPosition(ctx, id, strings.TrimSpace(name))
	if err != nil {
		return plan.Band{}, err
	}
	// Before the sheet comparison, because a spreadsheet-scoped entry
	// has no sheet and reports SheetID 0 — which is a real sheet id, and
	// commonly the first sheet's, so the comparison below would pass on
	// one spreadsheet and give a message about "a whole sheet" on all of
	// them. Another application can write one of these, so it is
	// reachable however careful this server is.
	if at.Kind == gsheets.LocationSpreadsheet || at.Kind == "" {
		return plan.Band{}, Errorf("invalid",
			"the anchor %q is on the whole spreadsheet, which is not a band of rows or columns", name)
	}
	if at.SheetID != props.SheetID {
		return plan.Band{}, Errorf("invalid",
			"the anchor %q is not on %q; a band acts on one sheet, so name the sheet the anchor is on",
			name, props.Title)
	}
	// The dimension and the anchor have to agree, for the same reason
	// ParseBand refuses "rows" with "B:D": a caller who meant one and
	// typed the other would have somebody's data moved somewhere they
	// did not ask for.
	dim, err := plan.ParseDimension(dimension)
	if err != nil {
		return plan.Band{}, Errorf("invalid", "%s", err)
	}
	switch {
	case dim == gsheets.DimensionRows && at.Row > 0:
		return plan.Band{Dimension: dim, First: at.Row, Last: at.Row}, nil
	case dim == gsheets.DimensionColumns && at.Col > 0:
		return plan.Band{Dimension: dim, First: at.Col, Last: at.Col}, nil
	case at.Row == 0 && at.Col == 0:
		return plan.Band{}, Errorf("invalid",
			"the anchor %q is on a whole sheet, which is not a band of rows or columns", name)
	}

	kind := "a row"
	if at.Col > 0 {
		kind = "a column"
	}
	return plan.Band{}, Errorf("invalid",
		"the anchor %q is on %s and the dimension says %s; pass the dimension the anchor actually names",
		name, kind, strings.ToLower(strings.TrimSpace(dimension)))
}

// anchorPosition resolves a name to where it points now, refusing the
// same things a range resolution refuses.
func (s *Service) anchorPosition(ctx context.Context, id, name string) (plan.AnchorAt, error) {
	if name == "" {
		return plan.AnchorAt{}, Errorf("invalid", "name an anchor after %q", AnchorPrefix)
	}
	md, err := s.findAnchor(ctx, id, name)
	if err != nil {
		return plan.AnchorAt{}, err
	}
	if md == nil {
		return plan.AnchorAt{}, Errorf("not_found",
			"this spreadsheet has no anchor called %q. manage_anchor with action=list shows the ones it has, and "+
				"any A1 band works here instead", name)
	}
	return plan.Where(md), nil
}

// anchorNote is the line a delete adds to its own account of itself when
// anchors went with what it deleted.
func anchorNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "It" + render.AnchorsTaken(names) + "; Google's reply does not mention them.\n"
}
