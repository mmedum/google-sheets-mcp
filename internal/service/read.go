package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
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
	MaxCells  int
	MaxChars  int
	// ContinueFrom is the row a previous truncated read stopped at.
	ContinueFrom      int
	IncludeNotes      bool
	IncludeValidation bool
	IncludeMerges     bool
}

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
	Grid         string     `json:"grid" jsonschema:"the range as an addressed grid: column letters across the top, row numbers down the side"`
	Footer       string     `json:"footer" jsonschema:"what was shown, out of what exists, and how to continue"`
	Spreadsheet  string     `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Sheet        string     `json:"sheet" jsonschema:"the sheet title"`
	Range        string     `json:"range" jsonschema:"the range actually read, in A1 with the sheet quoted"`
	Checkpoint   string     `json:"checkpoint" jsonschema:"pass this to a later write as expect_checkpoint to have the server re-read and refuse if these values changed; best effort, since Sheets offers no atomic guard"`
	Truncated    bool       `json:"truncated,omitempty" jsonschema:"true when a budget stopped the read short of the range asked for"`
	ContinueFrom int        `json:"continue_from,omitempty" jsonschema:"the first row not shown; pass it back as continue_from to read on"`
	Rows         [][]string `json:"rows,omitempty" jsonschema:"the values as arrays, present only when format is json, csv or tsv"`
	// text is what the text block carries, which is the requested
	// format. It is not serialised: the structured half already has the
	// grid and the rows.
	text string
}

// Render is the text half.
func (r ReadResult) Render() string { return r.text }

// Read answers read_range.
//
// The window is resolved against the sheet's real extent and a finite
// range is sent, always. Clamping what is displayed while fetching an
// open-ended range whole is the bug this avoids rather than repeats: a
// shipped server showed 50 rows of an A:Z read and still pulled the
// whole column into memory, and its maintainer's own note is that the
// memory was unbounded the entire time.
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
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	sh, err := s.ResolveRange(ctx, ref, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	rowCount, colCount := extent(sh.Props)
	// Clamp pulls a rectangle back inside the sheet, which is right for
	// one that overlaps it and wrong for one that misses entirely: a
	// read of A50:B60 on a ten-row sheet would come back holding row
	// ten's data, which is a plausible answer to a question nobody
	// asked. Refused with the size, so the caller can see why.
	if sh.Rect.FirstRow > rowCount || sh.Rect.FirstCol > colCount {
		return nil, Errorf("not_found", "%s is past the end of %q, which has %d rows and %d columns",
			a1.FormatRect(sh.Rect), sh.Props.Title, rowCount, colCount)
	}
	full := sh.Rect.Clamp(rowCount, colCount)

	window := full
	if req.ContinueFrom > 0 {
		if req.ContinueFrom > full.LastRow {
			return nil, Errorf("stale", "continue_from is row %d, past row %d where %s ends; the read is already complete",
				req.ContinueFrom, full.LastRow, a1.Format(sh.Props.Title, full))
		}
		if req.ContinueFrom < full.FirstRow {
			return nil, Errorf("invalid", "continue_from is row %d, before row %d where %s starts",
				req.ContinueFrom, full.FirstRow, a1.Format(sh.Props.Title, full))
		}
		window.FirstRow = req.ContinueFrom
	}

	maxCells := budget(req.MaxCells, s.cfg.MaxCells)
	window, cellsCut := fit(window, maxCells)
	rangeA1 := a1.Format(sh.Props.Title, window)

	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.GridFields, Ranges: []string{rangeA1}, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	data, merges, protected := sheetData(got, sh.Props.SheetID)
	formatted := grid.AsRaw
	if req.Formatted {
		formatted = grid.AsFormatted
	}
	g := grid.Build(sh.Props.Title, sh.Props.SheetID, window, data, formatted)
	g.Merges = overlapping(merges, window)
	g.Protected = protections(protected, window)

	opts := render.GridOptions{
		Show:              show,
		MaxChars:          budget(req.MaxChars, s.cfg.MaxChars),
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
	case cellsCut:
		continueFrom = window.LastRow + 1
	}

	res := &ReadResult{
		Grid:        rendered.Text,
		Spreadsheet: ref.ID,
		Sheet:       sh.Props.Title,
		Range:       rangeA1,
		Checkpoint:  grid.Checkpoint(ref.ID, g),
		Truncated:   continueFrom > 0,
	}
	res.ContinueFrom = continueFrom

	parts := []string{render.Footer(g, rendered, opts)}
	if continueFrom > 0 && rendered.ContinueFrom == 0 {
		parts = append(parts, fmt.Sprintf("the cell budget stopped here; continue at row %d", continueFrom))
	}
	parts = append(parts, "checkpoint "+res.Checkpoint)
	res.Footer = strings.Join(parts, "; ")

	if format == FormatGrid {
		res.text = rendered.Text + res.Footer + "\n"
		return res, nil
	}
	res.Rows = render.Rows(g, show)
	if n := shown - window.FirstRow + 1; n >= 0 && n < len(res.Rows) {
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

// fit trims a bounded rectangle to a cell budget, whole rows at a time.
func fit(r a1.Rect, maxCells int) (a1.Rect, bool) {
	cols := r.Cols()
	if cols <= 0 {
		return r, false
	}
	maxRows := max(maxCells/cols, 1)
	if r.Rows() <= maxRows {
		return r, false
	}
	r.LastRow = r.FirstRow + maxRows - 1
	return r, true
}

func budget(asked, dflt int) int {
	if asked > 0 {
		return asked
	}
	return dflt
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
