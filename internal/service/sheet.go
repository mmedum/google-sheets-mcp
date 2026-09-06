package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// forget drops a spreadsheet's cached metadata.
//
// Called after every structural write. Without it a rename inside the
// cache window would leave the next call resolving the old title, which
// is the one way the cache could cost correctness rather than a request.
func (s *Service) forget(id string) {
	s.mu.Lock()
	delete(s.cards, id)
	s.mu.Unlock()
}

// CreateRequest is what create_spreadsheet asks for.
type CreateRequest struct {
	Title string
	// Sheets are the tabs to create, in order. Verified live: giving a
	// list replaces the single sheet Google would otherwise make rather
	// than adding to it, so a caller who names one sheet gets one sheet.
	Sheets []string
	// Values seed the first sheet, written as a second request with the
	// same coercion rules as write_values.
	Values   [][]any
	TSV      string
	Input    string
	Locale   string
	TimeZone string
	DryRun   bool
}

// CreateResult is create_spreadsheet's answer.
type CreateResult struct {
	Summary     string     `json:"summary"`
	Spreadsheet string     `json:"spreadsheet,omitempty"`
	Title       string     `json:"title"`
	Link        string     `json:"link,omitempty"`
	Sheets      []string   `json:"sheets,omitempty" jsonschema:"every sheet title, in order, as later calls must name them"`
	Seeded      string     `json:"seeded,omitempty" jsonschema:"the range the seed values were written to"`
	Coerced     []Coercion `json:"coerced,omitempty"`
	Formulas    []string   `json:"formulas_created,omitempty" jsonschema:"seeded cells that now hold a formula"`
	DryRun      bool       `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r CreateResult) Render() string { return r.Summary }

// Create answers create_spreadsheet.
//
// Two requests when there are seed values, and the reason is worth
// stating: spreadsheets.create takes cells already parsed, so seeding
// through it would silently mean literal input and no coercion report.
// The values go through the same values.update every other write uses,
// so `input` means the same thing here as it does there.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, Errorf("invalid", "give the new spreadsheet a title")
	}
	input, typed, err := parseInput(req.Input)
	if err != nil {
		return nil, err
	}
	var seed [][]any
	if len(req.Values) > 0 || req.TSV != "" {
		if seed, err = valuesOf(req.Values, req.TSV); err != nil {
			return nil, err
		}
		var report plan.Report
		plan.CheckValues(&report, seed, typed, plan.Position)
		// A new spreadsheet has nothing to destroy, so only the outgoing
		// half of the guard applies. It still applies: a formula that
		// fetches a URL is no less outbound for being in a fresh file.
		if err := refuse(report, plan.Ack{}); err != nil {
			return nil, err
		}
	}

	res := &CreateResult{Title: title}
	var seedNote string
	if req.DryRun {
		res.DryRun = true
		res.Sheets = req.Sheets
		res.Summary = render.CreatePreview(title, req.Sheets, len(seed))
		return res, nil
	}

	in := &gsheets.NewSpreadsheet{Properties: &gsheets.SpreadsheetProperties{
		Title: title, Locale: strings.TrimSpace(req.Locale), TimeZone: strings.TrimSpace(req.TimeZone),
	}}
	for _, t := range req.Sheets {
		if strings.TrimSpace(t) == "" {
			return nil, Errorf("invalid", "a sheet title cannot be blank")
		}
		in.Sheets = append(in.Sheets, &gsheets.NewSheet{Properties: &gsheets.NewSheetProperties{Title: t}})
	}
	sp, err := s.api.CreateSpreadsheet(ctx, in)
	if err != nil {
		return nil, wrap(err)
	}
	// The Drive lookup is skipped: this call created the spreadsheet, so
	// the owner is the signed-in account and the modification time is
	// now. Asking would cost a request against a per-minute quota to
	// learn what the caller already knows.
	card := s.describe(ctx, sp, Reference{ID: sp.SpreadsheetID, File: &gapi.File{}})
	res.Spreadsheet, res.Link, res.Sheets = card.Spreadsheet, card.Link, card.Sheets

	if len(seed) > 0 {
		res.Seeded, res.Coerced, res.Formulas, seedNote = s.seed(ctx, sp, res.Spreadsheet, seed, input, typed)
	}
	res.Summary = render.CreateDone(card.Card, res.Seeded, seedNote, coercionLines(res.Coerced), res.Formulas)
	return res, nil
}

// seed writes the values a new spreadsheet was asked to start with.
//
// A failure is reported rather than returned: the spreadsheet exists and
// is named in the result, and a caller told only "failed" would not know
// a file had been created.
func (s *Service) seed(ctx context.Context, sp *gsheets.Spreadsheet, id string, values [][]any, input string, typed bool) (seeded string, coerced []Coercion, formulas []string, note string) {
	first := firstSheet(sp)
	if first == nil {
		return "", nil, nil, "the new spreadsheet reported no sheets, so the seed values were not written"
	}
	rect := a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: len(values), LastCol: len(values[0])}
	rangeA1 := a1.Format(first.Title, rect)
	got, err := s.api.UpdateValues(ctx, id, rangeA1, values, gapi.WriteOptions{Input: input})
	if err != nil {
		return "", nil, nil, fmt.Sprintf("the spreadsheet was created and the seed values were not written: %v", wrap(err))
	}
	// The same read-back every other write does. Built by hand here
	// once, it reported a seeded date as the serial with nothing saying
	// the cell shows a date — the one case the display exists for — and
	// named no formulas at all.
	back := s.readBack(ctx, id, first.Title, first.SheetID, rect, values, storedValues(got), typed)
	return rangeA1, back.coerced, back.formulas, ""
}

func firstSheet(sp *gsheets.Spreadsheet) *gsheets.SheetProperties {
	for _, sh := range sp.Sheets {
		if sh.Properties != nil {
			return sh.Properties
		}
	}
	return nil
}

// Sheet actions.
const (
	SheetAdd       = "add"
	SheetRename    = "rename"
	SheetDuplicate = "duplicate"
	SheetCopyTo    = "copy_to"
	SheetHide      = "hide"
	SheetUnhide    = "unhide"
	SheetReorder   = "reorder"
	SheetResize    = "resize"
	SheetFreeze    = "freeze"
	SheetTabColor  = "tab_color"
)

// SheetRequest is what manage_sheet asks for.
type SheetRequest struct {
	Spreadsheet string
	Action      string
	// Sheet names the tab to act on. Not needed by add, which names the
	// tab it is creating in Title.
	Sheet string
	Title string
	// Index is a zero-based position for add and reorder.
	Index *int
	// Rows and Cols are the new grid size for resize, and the frozen
	// counts for freeze.
	Rows int
	Cols int
	// Destination is another spreadsheet, for copy_to.
	Destination string
	// Colour is "#rrggbb", "#rgb" or "none".
	Colour string
	DryRun bool
}

// SheetResult is manage_sheet's answer.
type SheetResult struct {
	Summary     string   `json:"summary"`
	Spreadsheet string   `json:"spreadsheet"`
	Action      string   `json:"action"`
	Sheet       string   `json:"sheet,omitempty" jsonschema:"the sheet the action produced or acted on"`
	SheetID     int      `json:"sheet_id,omitempty" jsonschema:"the numeric sheet id, which survives a rename"`
	Sheets      []string `json:"sheets,omitempty" jsonschema:"every sheet title after the change, in order"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r SheetResult) Render() string { return r.Summary }

// ManageSheet answers manage_sheet.
//
// Every action but copy_to compiles to a single batchUpdate, which is
// atomic and counts once against quota. copy_to is its own endpoint
// because it writes to a second spreadsheet.
func (s *Service) ManageSheet(ctx context.Context, req SheetRequest) (*SheetResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	res := &SheetResult{Spreadsheet: ref.ID, Action: req.Action}

	// add names the sheet it creates; every other action names one that
	// has to exist already.
	var props *gsheets.SheetProperties
	if req.Action != SheetAdd {
		if props, err = s.findSheet(sp, strings.TrimSpace(req.Sheet), ref); err != nil {
			return nil, err
		}
		res.Sheet, res.SheetID = props.Title, props.SheetID
	}

	if req.Action == SheetCopyTo {
		return s.copySheet(ctx, ref, props, req, res)
	}

	op, act, err := sheetRequest(req, sp, props)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.SheetPreview(act)
		return res, nil
	}
	got, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{Requests: []*gsheets.Request{op}})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	if p := addedSheet(got); p != nil {
		res.Sheet, res.SheetID = p.Title, p.SheetID
	}
	after, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	res.Sheets, _ = sheetTitles(after)
	res.Summary = render.SheetDone(act, res.Sheets)
	return res, nil
}

// sheetRequest compiles one action into one union member, and returns
// the parts of the sentence describing it. The sentence itself is the
// renderer's, so a preview and a result cannot phrase the same act
// differently (§17a.9).
//
// Split three ways by what the action touches: the sheet's identity, its
// place among the others, and the grid on it.
func sheetRequest(req SheetRequest, sp *gsheets.Spreadsheet, props *gsheets.SheetProperties) (*gsheets.Request, render.SheetAct, error) {
	switch req.Action {
	case SheetAdd, SheetRename, SheetDuplicate:
		return namingRequest(req, sp, props)
	case SheetHide, SheetUnhide, SheetReorder:
		return placementRequest(req, sp, props)
	case SheetResize, SheetFreeze, SheetTabColor:
		return gridRequest(req, props)
	}
	return nil, render.SheetAct{}, Errorf("invalid",
		"action %q is not one of add, rename, duplicate, copy_to, hide, unhide, reorder, resize, freeze, tab_color",
		req.Action)
}

// namingRequest is the actions that give a sheet a name.
func namingRequest(req SheetRequest, sp *gsheets.Spreadsheet, props *gsheets.SheetProperties) (*gsheets.Request, render.SheetAct, error) {
	title := strings.TrimSpace(req.Title)
	act := render.SheetAct{Action: req.Action, Title: title}
	if props != nil {
		act.Sheet = props.Title
	}
	if req.Action != SheetDuplicate && title == "" {
		return nil, act, Errorf("invalid", "%s needs a title for the new sheet", req.Action)
	}
	if title != "" {
		if err := titleIsFree(sp, title); err != nil {
			return nil, act, err
		}
	}
	switch req.Action {
	case SheetAdd:
		return plan.AddSheet(title, req.Index, req.Rows, req.Cols), act, nil
	case SheetRename:
		return plan.RenameSheet(props.SheetID, title), act, nil
	default:
		return plan.DuplicateSheet(props.SheetID, title, req.Index), act, nil
	}
}

// placementRequest is the actions that move a sheet or take it out of
// the way.
func placementRequest(req SheetRequest, sp *gsheets.Spreadsheet, props *gsheets.SheetProperties) (*gsheets.Request, render.SheetAct, error) {
	act := render.SheetAct{Action: req.Action, Sheet: props.Title}
	if req.Action == SheetReorder {
		if req.Index == nil {
			return nil, act, Errorf("invalid", "reorder needs index, a zero-based position")
		}
		if *req.Index < 0 || *req.Index >= len(sp.Sheets) {
			return nil, act, Errorf("invalid", "index %d is outside 0..%d, which is what this spreadsheet has room for",
				*req.Index, len(sp.Sheets)-1)
		}
		act.Index = *req.Index
		return plan.ReorderSheet(props.SheetID, deref(props.Index), *req.Index), act, nil
	}
	hide := req.Action == SheetHide
	// A spreadsheet must keep one visible sheet, and a file where every
	// tab is hidden is one nobody can open properly.
	if hide && visibleSheets(sp) < 2 {
		return nil, act, Errorf("invalid",
			"%q is the only visible sheet and a spreadsheet must have one; add another before hiding this", props.Title)
	}
	return plan.HideSheet(props.SheetID, hide), act, nil
}

// gridRequest is the actions that change the sheet itself rather than
// its name or its place.
func gridRequest(req SheetRequest, props *gsheets.SheetProperties) (*gsheets.Request, render.SheetAct, error) {
	rows, cols := extent(props)
	act := render.SheetAct{Action: req.Action, Sheet: props.Title, Rows: req.Rows, Cols: req.Cols}
	switch req.Action {
	case SheetResize:
		act.Rows, act.Cols = or(req.Rows, rows), or(req.Cols, cols)
		if req.Rows <= 0 && req.Cols <= 0 {
			return nil, act, Errorf("invalid", "resize needs rows, cols, or both")
		}
		if req.Rows > a1.MaxRows || req.Cols > a1.MaxColumns {
			return nil, act, Errorf("invalid", "a sheet has at most %d rows and %d columns", a1.MaxRows, a1.MaxColumns)
		}
		// Shrinking takes the data on the rows or columns it removes,
		// and nothing in Sheets brings it back. Refused here rather than
		// gated: manage_sheet is not a destructive tool, and
		// edit_dimensions delete is where removing lives.
		if (req.Rows > 0 && req.Rows < rows) || (req.Cols > 0 && req.Cols < cols) {
			return nil, act, Errorf("blocked",
				"%q is %d by %d and this would shrink it, taking whatever is on the rows or columns it removes. "+
					"Growing is fine; to remove rows or columns use edit_dimensions, which says what is on them first",
				props.Title, rows, cols)
		}
		return plan.ResizeGrid(props.SheetID, req.Rows, req.Cols), act, nil
	case SheetFreeze:
		if req.Rows < 0 || req.Cols < 0 {
			return nil, act, Errorf("invalid", "freeze takes rows and cols of zero or more; zero unfreezes")
		}
		return plan.Freeze(props.SheetID, req.Rows, req.Cols), act, nil
	default:
		style, err := plan.ParseColour(req.Colour)
		if err != nil {
			return nil, act, Errorf("invalid", "%s", err)
		}
		if style != nil {
			act.Colour = strings.TrimSpace(req.Colour)
		}
		return plan.TabColour(props.SheetID, style), act, nil
	}
}

// copySheet copies a tab into another spreadsheet.
func (s *Service) copySheet(ctx context.Context, ref Reference, props *gsheets.SheetProperties, req SheetRequest, res *SheetResult) (*SheetResult, error) {
	dest, err := s.Resolve(ctx, req.Destination)
	if err != nil {
		return nil, err
	}
	if dest.ID == ref.ID {
		return nil, Errorf("invalid", "copy_to copies into another spreadsheet; to copy inside this one, use duplicate")
	}
	// Named the way the caller named it. A truncated id is what a log
	// line needs; a result should hand back the reference the caller
	// used, which is the one they will recognise.
	act := render.SheetAct{Action: SheetCopyTo, Sheet: props.Title, Destination: strings.TrimSpace(req.Destination)}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.SheetPreview(act)
		return res, nil
	}
	copied, err := s.api.CopySheetTo(ctx, ref.ID, props.SheetID, dest.ID)
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(dest.ID)
	res.Sheet, res.SheetID = copied.Title, copied.SheetID
	res.Summary = render.CopyDone(act, copied.Title)
	return res, nil
}

// DeleteSheetRequest is what delete_sheet asks for.
type DeleteSheetRequest struct {
	Spreadsheet string
	Sheet       string
	Confirm     bool
	DryRun      bool
}

// DeleteSheetResult is delete_sheet's answer.
type DeleteSheetResult struct {
	Summary     string   `json:"summary"`
	Spreadsheet string   `json:"spreadsheet"`
	Sheet       string   `json:"sheet"`
	Cells       int      `json:"cells" jsonschema:"non-empty cells that went with it"`
	Formulas    int      `json:"formulas"`
	Sheets      []string `json:"sheets,omitempty" jsonschema:"the sheets left, in order"`
	// Anchors are the durable labels that went with the sheet. The API's
	// reply never mentions them.
	Anchors []string `json:"anchors_removed,omitempty" jsonschema:"the anchors that were on the sheet and went with it"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r DeleteSheetResult) Render() string { return r.Summary }

// DeleteSheet removes a tab and everything on it.
//
// Registered only with GSHEETS_ENABLE_DESTRUCTIVE=true and still needs
// confirm, because nothing in Sheets brings a deleted sheet back. It
// counts what goes with it first, so the confirmation is informed.
func (s *Service) DeleteSheet(ctx context.Context, req DeleteSheetRequest) (*DeleteSheetResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	props, err := s.findSheet(sp, strings.TrimSpace(req.Sheet), ref)
	if err != nil {
		return nil, err
	}
	if visibleSheets(sp) < 2 {
		return nil, Errorf("invalid", "%q is the only sheet and a spreadsheet must have one", props.Title)
	}
	counts, err := s.countSheet(ctx, ref, props)
	if err != nil {
		return nil, err
	}
	res := &DeleteSheetResult{
		Spreadsheet: ref.ID, Sheet: props.Title,
		Cells: counts.NonEmpty, Formulas: counts.Formulas,
	}
	charts := len(sheetOf(sp, props.SheetID).Charts)
	// The anchors on the sheet, which go with it and which the API's
	// reply never mentions — the same silent loss `delete_dimensions`
	// names for a band.
	doomed, err := s.anchorsOnSheet(ctx, ref.ID, props.SheetID)
	if err != nil {
		return nil, err
	}
	res.Anchors = doomed
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.DeletePreview(props.Title, counts.NonEmpty, counts.Formulas, charts) +
			anchorNote(doomed)
		return res, nil
	}
	if !req.Confirm {
		return nil, Errorf("blocked",
			"deleting %q takes %d non-empty cell(s), %d formula(s) and %d chart(s) with it%s, and Sheets cannot undo "+
				"it. Pass confirm to go ahead, or duplicate the sheet first",
			props.Title, counts.NonEmpty, counts.Formulas, charts, render.AnchorsTaken(doomed))
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.DeleteSheet(props.SheetID)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	after, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	res.Sheets, _ = sheetTitles(after)
	res.Summary = render.DeleteDone(props.Title, counts.NonEmpty, counts.Formulas, charts, res.Sheets) +
		anchorNote(doomed)
	return res, nil
}

// countSheet reads a whole sheet to say what deleting it would cost.
//
// Bounded like every other read: past the budget it reports what it saw
// and says the count is a floor, rather than pulling ten million cells
// into the process to answer a question about a confirmation prompt.
func (s *Service) countSheet(ctx context.Context, ref Reference, props *gsheets.SheetProperties) (grid.Counts, error) {
	rows, cols := extent(props)
	window, _ := fit(a1.WholeSheet.Clamp(rows, cols), s.cfg.MaxCells)
	return s.count(ctx, ref, props, window)
}

// count reads a rectangle and says what is in it. The read is
// readTarget's, so the field mask and the response's shape are known in
// one place rather than in three.
func (s *Service) count(ctx context.Context, ref Reference, props *gsheets.SheetProperties, window a1.Rect) (grid.Counts, error) {
	g, err := s.readTarget(ctx, target{ref: ref, props: props, rect: window})
	if err != nil {
		return grid.Counts{}, err
	}
	return g.Count(), nil
}

func sheetOf(sp *gsheets.Spreadsheet, id int) *gsheets.Sheet {
	for _, sh := range sp.Sheets {
		if sh.Properties != nil && sh.Properties.SheetID == id {
			return sh
		}
	}
	return &gsheets.Sheet{}
}

// titleIsFree refuses a duplicate title before the request is built. The
// API's own refusal names the conflict, but only after a round trip.
func titleIsFree(sp *gsheets.Spreadsheet, title string) error {
	for _, sh := range sp.Sheets {
		if sh.Properties != nil && sh.Properties.Title == title {
			return Errorf("invalid", "this spreadsheet already has a sheet called %q", title)
		}
	}
	return nil
}

func visibleSheets(sp *gsheets.Spreadsheet) int {
	n := 0
	for _, sh := range sp.Sheets {
		if sh.Properties != nil && !sh.Properties.Hidden {
			n++
		}
	}
	return n
}

func addedSheet(r *gsheets.BatchUpdateSpreadsheetResponse) *gsheets.SheetProperties {
	for _, reply := range r.Replies {
		if reply == nil {
			continue
		}
		if reply.AddSheet != nil {
			return reply.AddSheet.Properties
		}
		if reply.DuplicateSheet != nil {
			return reply.DuplicateSheet.Properties
		}
	}
	return nil
}

func or(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
