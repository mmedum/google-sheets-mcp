package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v5/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v5/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v5/internal/grid"
	"github.com/mmedum/google-sheets-mcp/v5/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v5/internal/render"
)

// ReadRequest is what read_range asks for, after the tool has validated
// the shapes.
type ReadRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	// Show and Format are closed enums, parsed here rather than in the
	// tool: a tool validates the shape of what it was given, and which
	// values exist is a rule.
	Show      string
	Format    string
	Formatted bool
	Budget    Budget
	// ContinueFrom is the row a previous truncated read stopped at.
	ContinueFrom      int
	IncludeNotes      bool
	IncludeValidation bool
	IncludeMerges     bool
	// Ranges reads several ranges in one request, in place of Sheet and
	// Range, with the budget shared among them.
	Ranges []SheetRange
}

// SheetRange is one range of a read of several: a sheet and an A1 range
// on it, kept apart so the title is quoted here rather than by the
// caller.
type SheetRange struct {
	Sheet string
	Range string
}

// MaxReadRanges is how many ranges one read takes.
const MaxReadRanges = 20

// Output formats. grid is for reading, the rest for feeding elsewhere.
const (
	FormatGrid = "grid"
	FormatJSON = "json"
	FormatCSV  = "csv"
	FormatTSV  = "tsv"
)

// ReadResult is read_range's answer.
//
// Both halves carry the addressed grid: the structured half in `grid`,
// the text half as its content. A client that shows the model only the
// structured half and one that shows only the text half both get the
// column letters and row numbers, which are what let the model write
// back to what it just read.
type ReadResult struct {
	Grid         string      `json:"grid" jsonschema:"the range as an addressed grid: column letters across the top, row numbers down the side"`
	Footer       string      `json:"footer" jsonschema:"what was shown, out of what exists, and how to continue"`
	Spreadsheet  string      `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Sheet        string      `json:"sheet" jsonschema:"the sheet title"`
	Range        string      `json:"range" jsonschema:"the range actually read, in A1 with the sheet quoted"`
	Checkpoint   string      `json:"checkpoint" jsonschema:"pass this to a later write as checkpoint to have the server re-read and refuse if these values changed; best effort, since Sheets offers no atomic guard"`
	Truncated    bool        `json:"truncated,omitempty" jsonschema:"true when a budget stopped the read short of the range asked for"`
	ContinueFrom int         `json:"continue_from,omitempty" jsonschema:"the first row not shown; pass it back as continue_from to read on"`
	Rows         [][]string  `json:"rows,omitempty" jsonschema:"the values as arrays, present only when format is json, csv or tsv"`
	Reads        []RangeRead `json:"reads,omitempty" jsonschema:"one entry per range when ranges was given, in the order given, each with its own grid, footer, checkpoint, continue_from and, for json, csv or tsv, rows. Then grid holds every range under its address, and sheet, range, checkpoint and rows are empty: take a checkpoint from its range's entry"`
	// text is what the text block carries, which is the requested
	// format. It is not serialized: the structured half already has the
	// grid and the rows.
	text string
}

// Render is the text half.
func (r ReadResult) Render() string { return r.text }

// RangeRead is one range of a read of several, with the fields a read
// of one range answers with.
type RangeRead struct {
	Grid         string     `json:"grid" jsonschema:"the range as an addressed grid"`
	Footer       string     `json:"footer" jsonschema:"what was shown, out of what exists, and how to continue"`
	Sheet        string     `json:"sheet" jsonschema:"the sheet title"`
	Range        string     `json:"range" jsonschema:"the range actually read, in A1 with the sheet quoted"`
	Checkpoint   string     `json:"checkpoint" jsonschema:"pass this to a later write to this range as checkpoint"`
	Truncated    bool       `json:"truncated,omitempty" jsonschema:"true when the range's share of the budget stopped it short"`
	ContinueFrom int        `json:"continue_from,omitempty" jsonschema:"the first row not shown; read on with this range alone and continue_from"`
	Rows         [][]string `json:"rows,omitempty" jsonschema:"the values as arrays, present only when format is json, csv or tsv"`
}

// Read answers read_range.
//
// The window is resolved against the sheet's real extent and a finite
// range is sent, always. Clamping what is displayed while fetching an
// open-ended range whole is the bug this avoids rather than repeats: a
// shipped server showed 50 rows of an A:Z read and still pulled the
// whole column into memory, and its maintainer's own note is that the
// memory was unbounded the entire time. Checked by the live step "an
// open-ended range is bounded before the call".
// https://github.com/taylorwilsdon/google_workspace_mcp/pull/986
func (s *Service) Read(ctx context.Context, req ReadRequest) (*ReadResult, error) {
	// Before anything is sent. A format nobody supports is a typo, and a
	// typo should not cost a request against a per-minute quota.
	format, err := parseFormat(req.Format)
	if err != nil {
		return nil, err
	}
	show, err := parseShow(req.Show)
	if err != nil {
		return nil, err
	}
	bud, err := s.budget(req.Budget)
	if err != nil {
		return nil, err
	}
	if len(req.Ranges) > 0 {
		switch {
		case req.Sheet != "" || req.Range != "":
			return nil, Errorf("invalid", "give ranges, or sheet and range, not both")
		case req.ContinueFrom > 0:
			return nil, Errorf("invalid", "continue_from resumes one range; read a range that was cut short on its own")
		case len(req.Ranges) > MaxReadRanges:
			return nil, Errorf("invalid", "ranges takes at most %d; it was given %d", MaxReadRanges, len(req.Ranges))
		}
		return s.readMany(ctx, req, format, show, bud)
	}
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	w, err := window(at, req.ContinueFrom, bud)
	if err != nil {
		return nil, err
	}
	got, err := s.fetchGrid(ctx, at.ref.ID, []string{w.rangeA1})
	if err != nil {
		return nil, err
	}
	return present(at, w, got, req, show, format, bud)
}

// fetchGrid reads the cells of ranges in one request.
func (s *Service) fetchGrid(ctx context.Context, id string, ranges []string) (*gsheets.Spreadsheet, error) {
	got, err := s.api.GetSpreadsheet(ctx, id, gapi.GetOptions{
		Fields: gapi.ReadFields, Ranges: ranges, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	return got, nil
}

// readWindow is what one range of a read fetches.
type readWindow struct {
	// full is the range asked for, clamped to the sheet; rect is the
	// part of it the budget lets this read fetch.
	full, rect a1.Rect
	fetchCut   bool
	rangeA1    string
}

// window resolves the rows a read fetches, under its budget and from
// continueFrom.
func window(at area, continueFrom int, bud Budget) (readWindow, error) {
	sh, full := at.props, at.rect
	rect := full
	if continueFrom > 0 {
		if continueFrom > full.LastRow {
			return readWindow{}, Errorf("stale", "continue_from is row %d, past row %d where %s ends; the read is already complete",
				continueFrom, full.LastRow, a1.Format(sh.Title, full))
		}
		if continueFrom < full.FirstRow {
			return readWindow{}, Errorf("invalid", "continue_from is row %d, before row %d where %s starts",
				continueFrom, full.FirstRow, a1.Format(sh.Title, full))
		}
		rect.FirstRow = continueFrom
	}

	rect, fetchCut := fit(rect, bud.Cells)
	// The rendering stops at the character budget, at a row boundary,
	// and a row with something in it draws at least " |" for each cell
	// and a newline. Rows past what the budget could show at that rate are
	// not fetched; a fetch bound by cells alone read rows no rendering
	// reached. A run of empty rows draws one line, so a sparse range can
	// come back shorter than the budget allows, and continue_from still
	// says where to read on.
	if bud.Chars > 0 {
		var charsCut bool
		rect, charsCut = rect.LimitRows(bud.Chars/render.MinRowChars(rect.Cols()) + 1)
		fetchCut = fetchCut || charsCut
	}
	return readWindow{full: full, rect: rect, fetchCut: fetchCut, rangeA1: a1.Format(sh.Title, rect)}, nil
}

// present renders the cells a reply holds for one read's rectangle.
func present(at area, w readWindow, got *gsheets.Spreadsheet, req ReadRequest,
	show render.Show, format string, bud Budget) (*ReadResult, error) {
	ref, sh, full, rect := at.ref, at.props, w.full, w.rect
	data, merges, protected := sheetDataFor(got, sh.SheetID, rect)
	formatted := grid.AsRaw
	if req.Formatted {
		formatted = grid.AsFormatted
	}
	g := grid.Build(sh.Title, sh.SheetID, rect, data, formatted)
	g.Merges = overlapping(merges, rect)
	g.Protected = protections(protected, rect)

	opts := render.GridOptions{
		Show:              show,
		MaxChars:          bud.Chars,
		IncludeNotes:      req.IncludeNotes,
		IncludeValidation: req.IncludeValidation,
		IncludeMerges:     req.IncludeMerges,
		TotalRows:         full.LastRow,
	}
	rendered := render.Grid(g, opts)

	// One window decides everything below.
	//
	// The character budget is applied by the grid renderer, which stops
	// at a row boundary; the cell budget was applied before the request.
	// Whichever bit first, `rendered.LastRow` is the last row this read
	// actually answers for, so the machine formats are cut to it too.
	// Taking the rows from the whole grid instead would hand back every
	// row under a footer saying the read was cut — the structured half
	// contradicting its own description.
	shown := rendered.LastRow
	continueFrom := 0
	switch {
	case rendered.ContinueFrom > 0:
		continueFrom = rendered.ContinueFrom
	case w.fetchCut:
		continueFrom = rect.LastRow + 1
	}

	res := &ReadResult{
		Grid:        rendered.Text,
		Spreadsheet: ref.ID,
		Sheet:       sh.Title,
		Range:       w.rangeA1,
		Checkpoint:  grid.Checkpoint(ref.ID, g),
		Truncated:   continueFrom > 0,
	}
	res.ContinueFrom = continueFrom

	parts := []string{render.Footer(g, rendered, opts)}
	if continueFrom > 0 && rendered.ContinueFrom == 0 {
		parts = append(parts, fmt.Sprintf("the read budget stopped here; continue at row %d", continueFrom))
	}
	if hint := narrowerRange(g, rect, full, shown, sh.Title, continueFrom); hint != "" {
		parts = append(parts, hint)
	}
	parts = append(parts, "checkpoint "+res.Checkpoint)
	res.Footer = strings.Join(parts, "; ")

	if format == FormatGrid {
		res.text = rendered.Text + res.Footer + "\n"
		return res, nil
	}
	res.Rows = render.Rows(g, show)
	if n := shown - rect.FirstRow + 1; n >= 0 && n < len(res.Rows) {
		res.Rows = res.Rows[:n]
	}
	if format == FormatJSON {
		b, err := json.Marshal(res.Rows)
		if err != nil {
			return nil, Errorf("unavailable", "the values could not be encoded as JSON: %v", err)
		}
		res.text = string(b) + "\n" + res.Footer + "\n"
		return res, nil
	}
	comma := ','
	if format == FormatTSV {
		comma = '\t'
	}
	res.text = render.Separated(res.Rows, comma) + res.Footer + "\n"
	return res, nil
}

// readMany answers a read of several ranges with one request (D-81).
// Each range gets an equal share of the budget, and an answer of its own.
//
// Google returns one GridData per range asked of a sheet (the discovery
// document on Sheet.data). Each is matched to its range by where it
// starts rather than by position, so neither the order Google keeps nor
// a range asked twice can hand one range another's cells. Checked by
// the live step "several ranges in one read".
// https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/sheets#Sheet
func (s *Service) readMany(ctx context.Context, req ReadRequest, format string, show render.Show, bud Budget) (*ReadResult, error) {
	n := len(req.Ranges)
	share := Budget{Cells: max(1, bud.Cells/n), Chars: max(1, bud.Chars/n)}
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	type planned struct {
		at area
		w  readWindow
	}
	plans := make([]planned, n)
	ranges := make([]string, n)
	for i, r := range req.Ranges {
		at, err := s.locateIn(ctx, ref, r.Sheet, r.Range)
		if err != nil {
			return nil, err
		}
		w, err := window(at, 0, share)
		if err != nil {
			return nil, err
		}
		plans[i], ranges[i] = planned{at, w}, w.rangeA1
	}
	got, err := s.fetchGrid(ctx, ref.ID, ranges)
	if err != nil {
		return nil, err
	}
	res := &ReadResult{Spreadsheet: ref.ID}
	texts, grids := make([]string, n), make([]string, n)
	for i, p := range plans {
		one, err := present(p.at, p.w, got, req, show, format, share)
		if err != nil {
			return nil, err
		}
		res.Reads = append(res.Reads, one.asRange())
		res.Truncated = res.Truncated || one.Truncated
		texts[i] = "== " + one.Range + "\n" + one.text
		grids[i] = "== " + one.Range + "\n" + one.Grid
	}
	res.Grid = strings.Join(grids, "\n")
	res.Footer = fmt.Sprintf("%d ranges in one request, each with its own footer and checkpoint above", n)
	if res.Truncated {
		res.Footer += "; a range cut short reads on alone, with its continue_from"
	}
	res.text = strings.Join(texts, "\n") + res.Footer + "\n"
	return res, nil
}

// asRange is one range's answer, as an entry of a read of several.
func (r *ReadResult) asRange() RangeRead {
	return RangeRead{
		Grid: r.Grid, Footer: r.Footer, Sheet: r.Sheet, Range: r.Range, Checkpoint: r.Checkpoint,
		Truncated: r.Truncated, ContinueFrom: r.ContinueFrom, Rows: r.Rows,
	}
}

// parseShow closes the show enum.
func parseShow(v string) (render.Show, error) {
	switch render.Show(v) {
	case "", render.ShowValues:
		return render.ShowValues, nil
	case render.ShowFormulas:
		return render.ShowFormulas, nil
	case render.ShowBoth:
		return render.ShowBoth, nil
	}
	return "", Errorf("invalid", "show %q is not one of values, formulas, both", v)
}

// parseFormat closes the format enum, before any request is built.
func parseFormat(v string) (string, error) {
	switch v {
	case "", FormatGrid:
		return FormatGrid, nil
	case FormatJSON, FormatCSV, FormatTSV:
		return v, nil
	}
	return "", Errorf("invalid", "format %q is not one of grid, json, csv, tsv", v)
}

// area is a resolved rectangle on a resolved sheet: what every tool
// taking a spreadsheet, a sheet and a range starts from.
type area struct {
	ref   Reference
	props *gsheets.SheetProperties
	// rect is the caller's range, clamped to the sheet.
	rect a1.Rect
}

// locateRect resolves a spreadsheet, a sheet and a range, and clamps the
// range to the sheet.
//
// Clamp pulls a rectangle back inside the sheet, which is right for one
// that overlaps it and wrong for one that misses entirely: a read of
// A50:B60 on a ten-row sheet would come back holding row ten's data,
// which is a plausible answer to a question nobody asked. Refused with
// the size, so the caller can see why.
//
// Five tools opened with these fifteen lines before this existed, and
// the fifth copy is what made it worth having: the refusal is the same
// sentence each time, and a sentence in five places is a sentence that
// will not stay the same.
func (s *Service) locateRect(ctx context.Context, spreadsheet, sheet, rangeA1 string) (area, error) {
	ref, err := s.Resolve(ctx, spreadsheet)
	if err != nil {
		return area{}, err
	}
	return s.locateIn(ctx, ref, sheet, rangeA1)
}

// locateIn is locateRect in a spreadsheet already resolved.
func (s *Service) locateIn(ctx context.Context, ref Reference, sheet, rangeA1 string) (area, error) {
	sh, err := s.ResolveRange(ctx, ref, sheet, rangeA1)
	if err != nil {
		return area{}, err
	}
	rows, cols := extent(sh.Props)
	// A rectangle that misses the sheet is refused rather than clamped.
	// Google answers one with 400 "exceeds grid limits. Max rows: 1000,
	// max columns: 26", so this says the sheet's size too, before
	// sending. Checked by the live step "what values.get makes of a
	// range".
	// https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/get
	if sh.Rect.FirstRow > rows || sh.Rect.FirstCol > cols {
		return area{}, Errorf("not_found", "%s is past the end of %q, which has %d rows and %d columns",
			a1.FormatRect(sh.Rect), sh.Props.Title, rows, cols)
	}
	return area{ref: ref, props: sh.Props, rect: sh.Rect.Clamp(rows, cols)}, nil
}

// extent is the sheet's allocated size, which is what an open-ended
// range resolves against.
func extent(p *gsheets.SheetProperties) (rows, cols int) {
	rows, cols = 1000, 26
	if p != nil && p.GridProperties != nil {
		if p.GridProperties.RowCount > 0 {
			rows = p.GridProperties.RowCount
		}
		if p.GridProperties.ColumnCount > 0 {
			cols = p.GridProperties.ColumnCount
		}
	}
	return rows, cols
}

// narrowerRange says, for a read cut short, when every row it showed
// ends before the window's last column, and names the rest of the range
// with only the columns that held data, which reads more rows a call. It
// is a hint and not a resize, because a later row that reaches further
// right would fall outside a window sized by the rows shown so far.
func narrowerRange(g *grid.Grid, window, full a1.Rect, shown int, sheet string, continueFrom int) string {
	// Only where the data runs on to the last row shown: a read whose
	// data ended earlier gains nothing from more rows a call, and the hint
	// would name a column for data that is not there. The test is the one
	// the footer's "data ends at row" makes, so the two never disagree; a
	// blank run at the cut reads as the end to both.
	if continueFrom == 0 || window.FirstRow+g.DataRows-1 < shown {
		return ""
	}
	last := 0
	for i := 0; i < len(g.Cells) && window.FirstRow+i <= shown; i++ {
		for j := len(g.Cells[i]) - 1; j >= 0; j-- {
			if !g.Cells[i][j].Empty() {
				last = max(last, window.FirstCol+j)
				break
			}
		}
	}
	if last == 0 || last >= window.LastCol {
		return ""
	}
	letter, err := a1.ColumnName(last)
	if err != nil {
		return ""
	}
	rest := a1.Rect{FirstRow: continueFrom, FirstCol: window.FirstCol, LastRow: full.LastRow, LastCol: last}
	return fmt.Sprintf("every row shown ends by column %s, so %s reads more rows a call, "+
		"if no later row reaches further right", letter, a1.Format(sheet, rest))
}

// fit trims a bounded rectangle to a cell budget, whole rows at a time.
func fit(r a1.Rect, maxCells int) (a1.Rect, bool) {
	cols := r.Cols()
	if cols <= 0 {
		return r, false
	}
	return r.LimitRows(max(maxCells/cols, 1))
}

// sheetData pulls one sheet's grid data, merges and protected ranges out
// of a response.
func sheetData(sp *gsheets.Spreadsheet, sheetID int) (*gsheets.GridData, []*gsheets.GridRange, []*gsheets.ProtectedRange) {
	for _, sh := range sp.Sheets {
		if sh.Properties == nil || sh.Properties.SheetID != sheetID {
			continue
		}
		var data *gsheets.GridData
		if len(sh.Data) > 0 {
			data = sh.Data[0]
		}
		return data, sh.Merges, sh.ProtectedRanges
	}
	return nil, nil, nil
}

// sheetDataFor is sheetData for the range that starts where rect does,
// when several were asked of one sheet. GridData's start is zero-based.
func sheetDataFor(sp *gsheets.Spreadsheet, sheetID int, rect a1.Rect) (*gsheets.GridData, []*gsheets.GridRange, []*gsheets.ProtectedRange) {
	data, merges, protected := sheetData(sp, sheetID)
	for _, sh := range sp.Sheets {
		if sh.Properties == nil || sh.Properties.SheetID != sheetID {
			continue
		}
		for _, d := range sh.Data {
			if d != nil && d.StartRow == rect.FirstRow-1 && d.StartColumn == rect.FirstCol-1 {
				return d, merges, protected
			}
		}
	}
	return data, merges, protected
}

func overlapping(ranges []*gsheets.GridRange, window a1.Rect) []a1.Rect {
	var out []a1.Rect
	for _, g := range ranges {
		r := a1.FromGridRange(g)
		if r.Overlaps(window) {
			out = append(out, r)
		}
	}
	return out
}

func protections(ranges []*gsheets.ProtectedRange, window a1.Rect) []grid.Protection {
	var out []grid.Protection
	for _, p := range ranges {
		r := a1.FromGridRange(p.Range)
		if !r.Overlaps(window) {
			continue
		}
		out = append(out, grid.Protection{
			Rect: r, Description: p.Description, WarningOnly: p.WarningOnly, CanEdit: p.RequestingUserCanEdit,
		})
	}
	return out
}
