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

// maxBlocks is how many formatting blocks a read lists before it starts
// counting instead.
//
// A formatting answer is per block, not per cell, so the cell budget
// already bounds the read. This bounds the one case the cell budget does
// not: a range where every cell is formatted differently, which would
// otherwise answer a question about a rectangle with a list as long as
// the rectangle.
const maxBlocks = 300

// FormattingRequest is what read_formatting asks for.
type FormattingRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	Budget      Budget
}

// FormatBlock is one rectangle of identically formatted cells.
type FormatBlock struct {
	Range  string `json:"range"`
	Format string `json:"format" jsonschema:"what those cells look like, in words"`
}

// FormattingResult is read_formatting's answer.
type FormattingResult struct {
	Summary     string        `json:"summary" jsonschema:"the whole answer as text: the blocks, then what is attached to the range"`
	Spreadsheet string        `json:"spreadsheet"`
	Sheet       string        `json:"sheet"`
	Range       string        `json:"range" jsonschema:"the range actually read, in A1 with the sheet quoted"`
	Blocks      []FormatBlock `json:"blocks,omitempty" jsonschema:"rectangles of cells that share a format, in reading order. A cell with no format of its own is not in a block"`
	Merges      []string      `json:"merges,omitempty"`
	Rules       []string      `json:"conditional_rules,omitempty" jsonschema:"the rules on this sheet that reach this range, with the index manage_range needs to update or delete one"`
	Truncated   bool          `json:"truncated,omitempty" jsonschema:"true when the cell budget stopped the read short of the range asked for"`
}

// Render is the text half.
func (r FormattingResult) Render() string { return r.Summary }

// Formatting answers read_formatting.
//
// The other half of a read: read_range says what the cells hold, this
// says what they look like. It is summarised per block rather than per
// cell, because a format repeated down a column is one fact and a
// thousand lines of it is none.
func (s *Service) Formatting(ctx context.Context, req FormattingRequest) (*FormattingResult, error) {
	bud, err := s.budget(req.Budget)
	if err != nil {
		return nil, err
	}
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	ref, props, full := at.ref, at.props, at.rect
	window, cut := fit(full, bud.Cells)
	rangeA1 := a1.Format(props.Title, window)

	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.FormatFields, Ranges: []string{rangeA1}, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	sheet := sheetOf(got, props.SheetID)
	var data *gsheets.GridData
	if len(sheet.Data) > 0 {
		data = sheet.Data[0]
	}
	g := grid.Build(props.Title, props.SheetID, window, data, grid.AsRaw)

	res := &FormattingResult{
		Spreadsheet: ref.ID, Sheet: props.Title, Range: rangeA1, Truncated: cut,
	}
	view := render.Formatting{Range: rangeA1}

	plain := 0
	// Grouped on the style itself, which is a struct built to be
	// compared. Grouping on the sentence describing it built one per
	// cell — fifty thousand of them at the largest read — to print at
	// most a few hundred.
	for _, run := range grid.Runs(g, func(c grid.Cell) render.Style { return render.StyleOf(c.Format) }) {
		if run.Sig == (render.Style{}) {
			// Counted rather than listed: these are the background
			// against which the formatted blocks are the answer.
			cells, _ := run.Rect.Cells()
			plain += cells
			continue
		}
		if len(res.Blocks) < maxBlocks {
			block := FormatBlock{Range: a1.FormatRect(run.Rect), Format: run.Sig.Describe()}
			res.Blocks = append(res.Blocks, block)
			view.Blocks = append(view.Blocks, render.Block{Range: block.Range, Style: block.Format})
		}
	}

	view.Merges = formatRects(overlapping(sheet.Merges, window))
	res.Merges = view.Merges
	view.Rules, res.Rules = rulesOver(sheet.ConditionalFormats, window)
	view.Bandings = bandingsOver(sheet.BandedRanges, window)
	view.Validation = runsOf(g, func(c grid.Cell) string { return c.Validation })
	view.Notes = runsOf(g, func(c grid.Cell) string { return firstLine(c.Note) })
	for _, p := range protections(sheet.ProtectedRanges, window) {
		view.Protected = append(view.Protected, render.NamedItem{
			Range: a1.FormatRect(p.Rect), Name: p.Description,
			Detail: render.ProtectionState(p.CanEdit, p.WarningOnly),
		})
	}
	view.Footer = formattingFooter(plain, len(res.Blocks), cut, full, window)
	res.Summary = render.Formats(view)
	return res, nil
}

// formattingFooter says what was covered and what was not.
func formattingFooter(plain, blocks int, cut bool, full, window a1.Rect) string {
	parts := []string{fmt.Sprintf("%d cell(s) carry no format of their own", plain)}
	if blocks >= maxBlocks {
		parts = append(parts, fmt.Sprintf("only the first %d blocks are listed", maxBlocks))
	}
	if cut {
		parts = append(parts, fmt.Sprintf("the cell budget stopped this at row %d of %s; raise max_cells or read the rest separately",
			window.LastRow, a1.FormatRect(full)))
	}
	return strings.Join(parts, "; ")
}

// rulesOver picks the conditional format rules that reach a window, and
// keeps each one's index: the index is how manage_range names a rule,
// and a rule described without one cannot be updated or deleted.
func rulesOver(rules []*gsheets.ConditionalFormatRule, window a1.Rect) ([]render.NamedItem, []string) {
	var items []render.NamedItem
	var lines []string
	for i, rule := range rules {
		if rule == nil {
			continue
		}
		for _, r := range rule.Ranges {
			rect := a1.FromGridRange(r)
			if !rect.Overlaps(window) {
				continue
			}
			text := render.RuleText(rule)
			items = append(items, render.NamedItem{
				Range: a1.FormatRect(rect), Name: fmt.Sprintf("index %d", i), Detail: text,
			})
			lines = append(lines, fmt.Sprintf("index %d on %s: %s", i, a1.FormatRect(rect), text))
			break
		}
	}
	return items, lines
}

// bandingsOver picks the bandings that reach a window.
func bandingsOver(bandings []*gsheets.BandedRange, window a1.Rect) []render.NamedItem {
	var out []render.NamedItem
	for _, b := range bandings {
		if b == nil {
			continue
		}
		rect := a1.FromGridRange(b.Range)
		if !rect.Overlaps(window) {
			continue
		}
		detail := "rows"
		props := b.RowProperties
		if b.ColumnProperties != nil {
			detail, props = "columns", b.ColumnProperties
		}
		if props != nil {
			if hex := render.HexColour(props.SecondBandColorStyle); hex != "" {
				detail += " " + hex
			}
		}
		out = append(out, render.NamedItem{Range: a1.FormatRect(rect), Detail: detail})
	}
	return out
}

// runsOf groups the cells whose description is non-empty into blocks, so
// a validation rule down a column is one line rather than a hundred.
func runsOf(g *grid.Grid, describe func(grid.Cell) string) []render.NamedItem {
	var out []render.NamedItem
	for _, run := range grid.Runs(g, describe) {
		if run.Sig == "" || len(out) >= maxBlocks {
			continue
		}
		out = append(out, render.NamedItem{Range: a1.FormatRect(run.Rect), Detail: run.Sig})
	}
	return out
}

func formatRects(rects []a1.Rect) []string {
	out := make([]string, 0, len(rects))
	for _, r := range rects {
		out = append(out, a1.FormatRect(r))
	}
	return out
}

// firstLine keeps a note to one line in a summary. The whole note is
// read_range's business, with include_notes.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + "…"
	}
	return s
}

// FormatRequest is what format_cells asks for.
//
// Every field is optional and each one that is set is one op. They
// travel together because a batchUpdate is atomic and counts once
// against quota, so a header row that is bold, centred and shaded is one
// call rather than three.
type FormatRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string

	NumberFormat string
	// The font switches are three-valued: nil leaves them, true and
	// false set them. A field mask that named bold and carried nothing
	// would turn it off, so "leave it alone" has to be expressible.
	Bold          *bool
	Italic        *bool
	Underline     *bool
	Strikethrough *bool
	FontSize      int
	FontFamily    string
	TextColour    string
	Background    string

	Borders     string
	BorderSides string
	Horizontal  string
	Vertical    string
	Wrap        string

	Merge       string
	Unmerge     bool
	ClearFormat bool
	Note        string
	ClearNote   bool

	Overwrite bool
	DryRun    bool
}

// FormatResult is format_cells' answer.
type FormatResult struct {
	Summary     string   `json:"summary"`
	Spreadsheet string   `json:"spreadsheet"`
	Sheet       string   `json:"sheet"`
	Range       string   `json:"range" jsonschema:"the range the formatting was applied to"`
	Applied     []string `json:"applied,omitempty" jsonschema:"what was changed, one entry per op"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r FormatResult) Render() string { return r.Summary }

// FormatCells answers format_cells.
//
// Formatting destroys nothing, with three exceptions, and those are the
// ones the guard is for: a merge keeps the top-left value and discards
// the rest, clearing a format takes formatting Sheets cannot bring back,
// and a note replaces one no values read would have shown the caller.
// The cells are only read when one of those three is asked for, so an
// ordinary "make it bold" costs one request.
func (s *Service) FormatCells(ctx context.Context, req FormatRequest) (*FormatResult, error) {
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	ref, props, rect := at.ref, at.props, at.rect

	plan2, err := formatOps(req, props.SheetID, rect)
	if err != nil {
		return nil, err
	}
	ops, applied := plan2.ops, plan2.applied
	if len(ops) == 0 {
		return nil, Errorf("invalid",
			"say what to change: a number_format, a font switch, a colour, borders, an alignment, a wrap, "+
				"merge or unmerge, clear_format, or a note")
	}

	res := &FormatResult{
		Spreadsheet: ref.ID, Sheet: props.Title, Range: a1.Format(props.Title, rect),
	}
	res.Applied = appliedLines(applied)

	report, err := s.formatGuard(ctx, ref, props, rect, plan2)
	if err != nil {
		return nil, err
	}
	ack := plan.Ack{Overwrite: req.Overwrite}
	view := render.Ops{Range: res.Range, Applied: applied}
	if req.DryRun {
		res.DryRun = true
		view.Blockers = blockerLines(report, ack)
		res.Summary = render.OpsPreview(view)
		return res, nil
	}
	if err := refuse(report, ack); err != nil {
		return nil, err
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{Requests: ops}); err != nil {
		return nil, mergedIntoPivot(err)
	}
	// A merge, an unmerge and a protection all change what the card
	// says, and the card is what the next call resolves a range against.
	s.forget(ref.ID)
	res.Summary = render.OpsDone(view)
	return res, nil
}

// formatGuard reads what the ops would destroy.
//
// The protections and merges come from the card, which is already in
// hand, so a formatting call that destroys nothing costs no extra
// request. The cells are read only when a merge, a clear or a note is
// asked for, because those are the three ops that take something away.
func (s *Service) formatGuard(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	rect a1.Rect, compiled formatPlan,
) (plan.Report, error) {
	card, err := s.card(ctx, ref.ID)
	if err != nil {
		return plan.Report{}, err
	}
	sheet := sheetOf(card, props.SheetID)
	g := &grid.Grid{Sheet: props.Title, SheetID: props.SheetID, Rect: rect}
	g.Merges = overlapping(sheet.Merges, rect)
	g.Protected = protections(sheet.ProtectedRanges, rect)

	// A note op of either kind: setting one replaces what was there and
	// removing one takes it, and neither is visible in a values read.
	// Leaving clear_note out of this list is how a call clearing a
	// column's notes went through with no read, no blocker and nothing
	// in the result saying what went.
	// What the ops themselves say they take away, rather than a second
	// reading of the request. The list used to be re-derived here, and
	// a missing entry in it is exactly how clear_note shipped
	// unguarded — the compiler knew, and the guard was asking the
	// request again.
	if !compiled.destroys() {
		return plan.CheckDestination(g), nil
	}
	if err := readable(rect, "this change reads the range first to say what it would take away"); err != nil {
		return plan.Report{}, err
	}
	read, err := s.readTargetFields(ctx, target{ref: ref, props: props, rect: rect}, gapi.FormatTargetFields)
	if err != nil {
		return plan.Report{}, err
	}
	report := plan.CheckDestination(read)
	if compiled.merge != "" {
		if err := frozenBoundary(props, rect, compiled.merge); err != nil {
			return plan.Report{}, err
		}
		if err := s.pivotBoundary(ctx, target{ref: ref, props: props, rect: rect}, read); err != nil {
			return plan.Report{}, err
		}
		plan.CheckMerge(&report, read, compiled.merge)
		// A merge over an existing merge is how one is widened, so the
		// partial-merge check is not asked for here. Nothing else in
		// this tool writes into cells, so nothing else needs it either.
	}
	if compiled.clearsFormat {
		plan.CheckClearFormat(&report, read)
	}
	if compiled.notes {
		plan.CheckNoteReplace(&report, read, compiled.note)
	}
	return report, nil
}

// formatPlan is what one format_cells call compiles to: the requests,
// the words for them, and what each op takes away.
//
// The last part is the point. The guard used to read the request a
// second time to decide whether to read the cells, which made its
// coverage a list kept by hand beside the compiler — and a missing entry
// in that list is not a compile error, it is an unguarded write.
type formatPlan struct {
	ops     []*gsheets.Request
	applied []render.Applied
	// merge is the parsed merge type, empty when there is none.
	merge string
	// clearsFormat and notes say the other two ops that take something
	// away are present; note is what a note op would write.
	clearsFormat bool
	notes        bool
	note         string
}

// destroys reports whether any compiled op takes something away, which
// is what decides whether the cells are read before sending.
func (p formatPlan) destroys() bool {
	return p.merge != "" || p.clearsFormat || p.notes
}

// formatOps compiles the request into requests and the words for them.
//
// The order is the order they are applied in, and it matters once:
// clearing comes first, so "clear this and then make it bold" is one
// call that does both rather than a clear that undoes the bold.
func formatOps(req FormatRequest, sheetID int, rect a1.Rect) (formatPlan, error) {
	var p formatPlan
	add := func(op *gsheets.Request, kind, value string) {
		p.ops = append(p.ops, op)
		p.applied = append(p.applied, render.Applied{Kind: kind, Value: value})
	}
	invalid := func(err error) (formatPlan, error) {
		return formatPlan{}, Errorf("invalid", "%s", err)
	}

	if req.Merge != "" && req.Unmerge {
		return formatPlan{}, Errorf("invalid", "merge and unmerge ask for opposite things; send one of them")
	}
	// The same shape, and it used to be silent: clear_note won and the
	// guard checked the note that was never going to be written, so a
	// call could be refused for a replacement it would not have made.
	if req.Note != "" && req.ClearNote {
		return formatPlan{}, Errorf("invalid", "note and clear_note ask for opposite things; send one of them")
	}
	if req.ClearFormat {
		p.clearsFormat = true
		add(plan.ClearFormat(sheetID, rect), "clear formatting", "")
	}

	patch, patched, err := formatPatch(req)
	if err != nil {
		return formatPlan{}, err
	}
	p.applied = append(p.applied, patched...)
	p.notes, p.note = req.Note != "" || req.ClearNote, req.Note
	if !patch.Empty() {
		p.ops = append(p.ops, patch.Request(sheetID, rect))
	}

	if req.Borders != "" {
		border, err := plan.ParseBorder(req.Borders)
		if err != nil {
			return invalid(err)
		}
		sides, err := plan.ParseSides(req.BorderSides)
		if err != nil {
			return invalid(err)
		}
		add(plan.Borders(sheetID, rect, border, sides), "borders", req.Borders)
	}
	if req.Merge != "" {
		kind, err := plan.ParseMerge(req.Merge)
		if err != nil {
			return invalid(err)
		}
		p.merge = kind
		add(plan.Merge(sheetID, rect, kind), "merge", req.Merge)
	}
	if req.Unmerge {
		add(plan.Unmerge(sheetID, rect), "unmerge", "")
	}
	return p, nil
}

// formatPatch is the half of the request that compiles into one
// repeatCell: everything that lives on a cell rather than between cells.
func formatPatch(req FormatRequest) (plan.Patch, []render.Applied, error) {
	var patch plan.Patch
	var applied []render.Applied
	invalid := func(err error) (plan.Patch, []render.Applied, error) {
		return plan.Patch{}, nil, Errorf("invalid", "%s", err)
	}
	if req.NumberFormat != "" {
		kind, pattern, err := plan.ParseNumberFormat(req.NumberFormat)
		if err != nil {
			return invalid(err)
		}
		patch.NumberFormat(kind, pattern)
		applied = append(applied, render.Applied{Kind: "number format", Value: req.NumberFormat})
	}
	for _, sw := range []struct {
		value *bool
		name  string
		set   func(bool)
	}{
		{req.Bold, "bold", patch.Bold}, {req.Italic, "italic", patch.Italic},
		{req.Underline, "underline", patch.Underline}, {req.Strikethrough, "strikethrough", patch.Strikethrough},
	} {
		if sw.value == nil {
			continue
		}
		sw.set(*sw.value)
		applied = append(applied, render.Applied{Kind: sw.name, Value: onOff(*sw.value)})
	}
	if req.FontSize > 0 {
		patch.FontSize(req.FontSize)
		applied = append(applied, render.Applied{Kind: "font size", Value: fmt.Sprintf("%dpt", req.FontSize)})
	}
	if req.FontFamily != "" {
		patch.FontFamily(req.FontFamily)
		applied = append(applied, render.Applied{Kind: "font", Value: req.FontFamily})
	}
	if req.TextColour != "" {
		style, err := plan.ParseColour(req.TextColour)
		if err != nil {
			return invalid(err)
		}
		patch.TextColour(style)
		applied = append(applied, render.Applied{Kind: "text colour", Value: req.TextColour})
	}
	if req.Background != "" {
		style, err := plan.ParseColour(req.Background)
		if err != nil {
			return invalid(err)
		}
		patch.Background(style)
		applied = append(applied, render.Applied{Kind: "background", Value: req.Background})
	}
	if req.Horizontal != "" {
		align, err := plan.ParseAlign(req.Horizontal, false)
		if err != nil {
			return invalid(err)
		}
		patch.HorizontalAlign(align)
		applied = append(applied, render.Applied{Kind: "horizontal alignment", Value: req.Horizontal})
	}
	if req.Vertical != "" {
		align, err := plan.ParseAlign(req.Vertical, true)
		if err != nil {
			return invalid(err)
		}
		patch.VerticalAlign(align)
		applied = append(applied, render.Applied{Kind: "vertical alignment", Value: req.Vertical})
	}
	if req.Wrap != "" {
		wrap, err := plan.ParseWrap(req.Wrap)
		if err != nil {
			return invalid(err)
		}
		patch.Wrap(wrap)
		applied = append(applied, render.Applied{Kind: "wrap", Value: req.Wrap})
	}
	switch {
	case req.ClearNote:
		patch.Note("")
		applied = append(applied, render.Applied{Kind: "note", Value: "removed"})
	case req.Note != "":
		patch.Note(req.Note)
		applied = append(applied, render.Applied{Kind: "note", Value: "set"})
	}
	return patch, applied, nil
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// appliedLines is the structured half of what a batch did: the same ops
// the renderer prints, as flat strings.
//
// One place, because format_cells, manage_range and transform_range all
// report the same shape, and three copies of a join is three ways for
// the structured half and the text half to describe different work.
func appliedLines(applied []render.Applied) []string {
	out := make([]string, 0, len(applied))
	for _, a := range applied {
		out = append(out, strings.TrimSuffix(a.Kind+" "+a.Value, " "))
	}
	return out
}

// frozenBoundary refuses a merge that spans the edge of a frozen band.
//
// Verified live: Sheets answers "You can't merge frozen and non-frozen
// columns", which says the rule and not where the edge is — and a caller
// who froze the first column two calls ago is not thinking about it. The
// counts are on the card this call already read, so saying it here costs
// nothing.
//
// Merging entirely inside the frozen band, or entirely outside it, is
// fine; only crossing is refused.
func frozenBoundary(props *gsheets.SheetProperties, rect a1.Rect, kind string) error {
	grid := props.GridProperties
	if grid == nil {
		return nil
	}
	// A merge by rows keeps every row separate, so it cannot span the
	// frozen row edge; by columns, the same for columns.
	rows, cols := kind != gsheets.MergeRows, kind != gsheets.MergeColumns
	if rows && crosses(grid.FrozenRowCount, rect.FirstRow, rect.LastRow) {
		return Errorf("blocked",
			"%s spans the edge of the %d frozen row(s) on %q, and Sheets cannot merge frozen with non-frozen. "+
				"Merge inside the frozen rows or below them, or unfreeze with manage_sheet freeze rows=0",
			a1.FormatRect(rect), grid.FrozenRowCount, props.Title)
	}
	if cols && crosses(grid.FrozenColumnCount, rect.FirstCol, rect.LastCol) {
		name, _ := a1.ColumnName(grid.FrozenColumnCount)
		return Errorf("blocked",
			"%s spans the edge of the %d frozen column(s) on %q, which end at column %s, and Sheets cannot merge "+
				"frozen with non-frozen. Merge inside them or beyond them, or unfreeze with manage_sheet freeze cols=0",
			a1.FormatRect(rect), grid.FrozenColumnCount, props.Title, name)
	}
	return nil
}

// crosses reports whether a band of frozen leading rows or columns ends
// inside first..last.
func crosses(frozen, first, last int) bool {
	return frozen > 0 && first <= frozen && last > frozen
}

// pivotMergeRefusal is what Sheets says when a merge touches a pivot
// table, quoted so the translation is anchored to the string rather than
// to a status code every other bad merge shares.
const pivotMergeRefusal = "part of a pivot table"

// mergedIntoPivot turns that 400 into this server's own sentence.
//
// The backstop to pivotBoundary, and it exists because Google goes by
// the pivot's *footprint* rather than by its cells. Verified live: a
// merge over F13:G13, two cells that are blank in the response and
// blank on the sheet, is refused because the rectangle they sit in
// belongs to a pivot table (spike Q). The guard cannot see that — a
// blank cell inside a footprint looks like any other blank cell — and
// finding out would mean measuring every pivot on the sheet before
// every merge, which is the cost §17a.27 refused for a message.
//
// So the cheap check answers first where it can, naming the table and
// what it covers, and this catches the rest. A caller never sees
// Google's untranslated wording either way, and no merge pays a read it
// did not need.
func mergedIntoPivot(err error) error {
	if err == nil || !strings.Contains(err.Error(), pivotMergeRefusal) {
		return wrap(err)
	}
	return Errorf("blocked",
		"Sheets refuses a merge over any cell of a pivot table, including the blank ones inside the rectangle "+
			"it covers. manage_pivot_table list says where each one reaches; merge cells outside them")
}

// pivotBoundary refuses a merge that touches a pivot table.
//
// Sheets refuses it itself: `400 Invalid requests[0].mergeCells: You
// can't merge cells that are part of a pivot table`, over the output as
// surely as over the anchor, with the pivot untouched (spike Q). So
// nothing is being prevented here — what is being fixed is the sentence.
// The guard used to answer first with "merging would keep the top-left
// value and discard F3, which is not empty", which describes a loss that
// cannot happen, and offered `overwrite` to get past itself; a caller
// who passed it reached Google's 400 instead.
//
// Nothing acknowledges this one. The API will not do it whatever the
// caller says, so an argument that claimed otherwise would be a lie in
// the schema.
func (s *Service) pivotBoundary(ctx context.Context, t target, read *grid.Grid) error {
	// The anchors inside the rectangle, straight out of what was read.
	if anchors := plan.PivotAnchors(read); anchors.Any() {
		return Errorf("blocked",
			"%s %s a pivot table, and Sheets refuses a merge over any cell of one. Delete the pivot table with "+
				"manage_pivot_table, or merge cells outside it",
			anchors, anchors.Verb("carries", "carry"))
	}
	// And the ones drawing into it from outside, which cost a read and
	// are asked for only where the rectangle holds a cell nobody typed.
	if !read.AnyComputed() {
		return nil
	}
	// Every one of them, not the first. Two pivots side by side and a
	// merge across the seam is refused by both, and a message naming one
	// says "merge cells outside it" about cells inside the other.
	hits := s.pivotsCovering(ctx, t)
	if len(hits) == 0 {
		return nil
	}
	where := make([]string, 0, len(hits))
	for _, p := range hits {
		where = append(where, fmt.Sprintf("%s is inside the output of the pivot table anchored at %s, which "+
			"covers %s as it stands", p.Hit, p.Anchor, p.Output))
	}
	return Errorf("blocked",
		"%s, and Sheets refuses a merge over any cell of a pivot table. Merge cells outside %s",
		strings.Join(where, "; "), map[bool]string{true: "it", false: "them"}[len(hits) == 1])
}
