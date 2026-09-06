package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// What transform_range does.
const (
	TransformSort          = "sort"
	TransformFindReplace   = "find_replace"
	TransformTrim          = "trim_whitespace"
	TransformDedupe        = "remove_duplicates"
	TransformTextToColumns = "text_to_columns"
	TransformRandomize     = "randomize"
	TransformAutoFill      = "auto_fill"
	TransformCopyPaste     = "copy_paste"
	TransformCutPaste      = "cut_paste"
)

// splitWindow is how far to the right of a text_to_columns source this
// server looks for cells the split would land on.
//
// A window rather than the whole sheet, because with the delimiter left
// to Google to detect there is no knowing how wide the split will be,
// and a guard has to read something finite. The refusal says so.
const splitWindow = 25

// TransformRequest is what transform_range asks for.
type TransformRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	Action      string

	// SortBy is "B asc" or "B asc, C desc".
	SortBy string
	// Find and Replace, with the switches that decide what matches.
	Find            string
	Replace         string
	MatchCase       bool
	MatchEntireCell bool
	Regex           bool
	// InFormulas replaces inside formula text, which changes what a cell
	// computes.
	InFormulas bool
	// Columns are the ones remove_duplicates compares, as "B,D". Empty
	// compares every column in the range.
	Columns string
	// Delimiter is what text_to_columns splits on. Empty asks Google to
	// detect it.
	Delimiter string
	// FillRows fills downwards rather than across, and FillLength is how
	// far.
	FillRows   bool
	FillLength int
	// Destination is where a copy or a cut lands, in A1. It may name
	// another sheet in the same spreadsheet.
	Destination string
	Paste       string
	Transpose   bool

	Overwrite         bool
	OverwriteFormulas bool
	DryRun            bool
}

// Ack is what the guard is told.
func (r TransformRequest) Ack() plan.Ack {
	return plan.Ack{Overwrite: r.Overwrite, OverwriteFormulas: r.OverwriteFormulas}
}

// TransformResult is transform_range's answer.
type TransformResult struct {
	Summary     string   `json:"summary"`
	Spreadsheet string   `json:"spreadsheet"`
	Sheet       string   `json:"sheet"`
	Range       string   `json:"range" jsonschema:"the range the transform read from"`
	Action      string   `json:"action"`
	Destination string   `json:"destination,omitempty" jsonschema:"where a copy or a cut landed"`
	Changed     int      `json:"changed,omitempty" jsonschema:"what the API reported afterwards: cells changed, rows removed, occurrences replaced"`
	Applied     []string `json:"applied,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r TransformResult) Render() string { return r.Summary }

// transform is one compiled action: the request, the words for it, and
// the rectangle the guard has to read.
type transform struct {
	op      *gsheets.Request
	applied render.Applied
	// destination is the rectangle the action writes into when that is
	// somewhere other than the range itself.
	destination a1.Rect
	destSheet   *gsheets.SheetProperties
	// shifted says rows moved, and emptied names the range a cut left
	// blank. Parts rather than sentences: the words are render's, which
	// is what §17a.9 settled for the structural tools and what this tool
	// undid by writing them here.
	shifted bool
	emptied string
	// delimiter is what a split was parsed to, for the guard that reads
	// how far it would reach.
	delimiter plan.Delimiter
}

// Transform answers transform_range.
//
// These are the operations that move data without the caller naming its
// new address, which is why each one runs the guard over a destination
// the caller cannot see: a copy lands on cells nobody looked at, and a
// split spills into the columns to its right.
func (s *Service) Transform(ctx context.Context, req TransformRequest) (*TransformResult, error) {
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	ref, props, rect := at.ref, at.props, at.rect

	t, err := s.compile(ctx, req, ref, props, rect)
	if err != nil {
		return nil, err
	}
	res := &TransformResult{
		Spreadsheet: ref.ID, Sheet: props.Title, Range: a1.Format(props.Title, rect),
		Action: req.Action, Applied: appliedLines([]render.Applied{t.applied}),
	}
	if t.destSheet != nil {
		res.Destination = a1.Format(t.destSheet.Title, t.destination)
	}

	report, err := s.transformGuard(ctx, req, ref, props, rect, t)
	if err != nil {
		return nil, err
	}
	view := render.Ops{
		Range: res.Range, Applied: []render.Applied{t.applied},
		Shifted: t.shifted, Emptied: t.emptied,
	}
	if req.DryRun {
		res.DryRun = true
		view.Blockers = blockerLines(report, req.Ack())
		res.Summary = render.OpsPreview(view)
		return res, nil
	}
	if err := refuse(report, req.Ack()); err != nil {
		return nil, err
	}
	got, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{t.op},
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	view.Counted = transformCount(got)
	res.Changed = view.Counted.Total()
	res.Summary = render.OpsDone(view)
	return res, nil
}

// transformCount reads what the API said it did, as numbers.
//
// Three of the nine actions answer with a count and the rest with
// nothing, and a result that said "done" without the number would be
// hiding the one fact the caller cannot see. The sentence they read as
// is render's.
func transformCount(got *gsheets.BatchUpdateSpreadsheetResponse) *render.Counts {
	for _, reply := range got.Replies {
		switch {
		case reply == nil:
		case reply.FindReplace != nil:
			r := reply.FindReplace
			return &render.Counts{
				Kind: render.CountReplaced, Occurrences: r.OccurrencesChanged,
				Cells: r.ValuesChanged, Formulas: r.FormulasChanged, Rows: r.RowsChanged,
			}
		case reply.TrimWhitespace != nil:
			return &render.Counts{Kind: render.CountTrimmed, Cells: reply.TrimWhitespace.CellsChangedCount}
		case reply.DeleteDuplicates != nil:
			return &render.Counts{Kind: render.CountDeduped, Cells: reply.DeleteDuplicates.DuplicatesRemovedCount}
		}
	}
	return nil
}

// compile turns one action into one union member.
func (s *Service) compile(ctx context.Context, req TransformRequest, ref Reference,
	props *gsheets.SheetProperties, rect a1.Rect,
) (transform, error) {
	id := props.SheetID
	switch req.Action {
	case TransformSort:
		specs, err := plan.ParseSort(req.SortBy)
		if err != nil {
			return transform{}, Errorf("invalid", "%s", err)
		}
		if err := sortColumnsInside(specs, rect); err != nil {
			return transform{}, err
		}
		return transform{
			op: plan.Sort(id, rect, specs), applied: render.Applied{Kind: "sorted by", Value: req.SortBy},
			shifted: true,
		}, nil

	case TransformFindReplace:
		if req.Find == "" {
			return transform{}, Errorf("invalid", "find is what to look for, and it cannot be empty")
		}
		opts := plan.FindReplaceOptions{
			MatchCase: req.MatchCase, MatchEntireCell: req.MatchEntireCell,
			Regex: req.Regex, InFormulas: req.InFormulas,
		}
		return transform{
			op:      plan.FindReplace(id, rect, req.Find, req.Replace, opts),
			applied: render.Applied{Kind: "replaced", Value: fmt.Sprintf("%q with %q", req.Find, req.Replace)},
		}, nil

	case TransformTrim:
		return transform{op: plan.Trim(id, rect), applied: render.Applied{Kind: "trimmed whitespace in", Value: a1.FormatRect(rect)}}, nil

	case TransformDedupe:
		columns, err := plan.ParseColumns(req.Columns)
		if err != nil {
			return transform{}, Errorf("invalid", "%s", err)
		}
		for _, col := range columns {
			if col < rect.FirstCol || col > rect.LastCol {
				name, _ := a1.ColumnName(col)
				return transform{}, Errorf("invalid",
					"column %s is not inside %s, and remove_duplicates compares columns of the range it is given",
					name, a1.FormatRect(rect))
			}
		}
		what := "every column"
		if req.Columns != "" {
			what = req.Columns
		}
		return transform{
			op: plan.Dedupe(id, rect, columns), applied: render.Applied{Kind: "removed duplicate rows comparing", Value: what},
			shifted: true,
		}, nil

	case TransformTextToColumns:
		if rect.Cols() != 1 {
			return transform{}, Errorf("invalid",
				"text_to_columns splits one column; %s is %d wide", a1.FormatRect(rect), rect.Cols())
		}
		delimiter, err := plan.ParseDelimiter(req.Delimiter)
		if err != nil {
			return transform{}, Errorf("invalid", "%s", err)
		}
		named := req.Delimiter
		if named == "" {
			named = "a delimiter Google detects"
		}
		return transform{
			op:      plan.TextToColumns(id, rect, delimiter),
			applied: render.Applied{Kind: "split on", Value: named},
			// Kept for the guard, which has to work out how far the
			// split would reach. Parsed once, here, rather than read a
			// second time from the caller's word.
			delimiter: delimiter,
		}, nil

	case TransformRandomize:
		return transform{
			op: plan.Randomize(id, rect), applied: render.Applied{Kind: "shuffled the rows of", Value: a1.FormatRect(rect)},
			shifted: true,
		}, nil

	case TransformAutoFill:
		if req.FillLength < 1 {
			return transform{}, Errorf("invalid", "auto_fill needs fill_length, how many rows or columns to fill")
		}
		rows, cols := extent(props)
		dest, err := fillDestination(rect, req.FillRows, req.FillLength, props.Title, rows, cols)
		if err != nil {
			return transform{}, err
		}
		return transform{
			op:          plan.AutoFill(id, rect, req.FillRows, req.FillLength),
			applied:     render.Applied{Kind: "continued the series in", Value: a1.FormatRect(rect)},
			destination: dest, destSheet: props,
		}, nil

	case TransformCopyPaste, TransformCutPaste:
		return s.compilePaste(ctx, req, ref, props, rect)
	}
	return transform{}, Errorf("invalid",
		"action %q is not one of sort, find_replace, trim_whitespace, remove_duplicates, text_to_columns, "+
			"randomize, auto_fill, copy_paste, cut_paste", req.Action)
}

// compilePaste resolves a copy or a cut and works out where it lands.
func (s *Service) compilePaste(ctx context.Context, req TransformRequest, ref Reference,
	props *gsheets.SheetProperties, rect a1.Rect,
) (transform, error) {
	if strings.TrimSpace(req.Destination) == "" {
		return transform{}, Errorf("invalid", "%s needs destination, the cell the top-left corner lands on", req.Action)
	}
	// Resolved through the same path as any other range, so a
	// destination on another sheet is named the way every range is and
	// a missing sheet is refused with the titles that exist.
	//
	// A destination that names no sheet means this one. That is the one
	// place in this server where a sheet is defaulted, and it is not a
	// guess: the caller named a sheet on the same call, and the
	// alternative is making them write it twice to copy A1:B2 to D10.
	dest, err := s.ResolveRange(ctx, ref, defaultSheet(props.Title, req.Destination), req.Destination)
	if err != nil {
		return transform{}, err
	}
	paste, err := plan.ParsePaste(req.Paste)
	if err != nil {
		return transform{}, Errorf("invalid", "%s", err)
	}
	rows, cols := rect.Rows(), rect.Cols()
	if req.Transpose && req.Action == TransformCopyPaste {
		rows, cols = cols, rows
	}
	anchor := dest.Rect
	if anchor.FirstRow < 1 || anchor.FirstCol < 1 {
		return transform{}, Errorf("invalid",
			"destination %q has to name a cell, such as F1; a whole row or column has no corner to land on", req.Destination)
	}
	landing := a1.Rect{
		FirstRow: anchor.FirstRow, FirstCol: anchor.FirstCol,
		LastRow: anchor.FirstRow + rows - 1, LastCol: anchor.FirstCol + cols - 1,
	}
	destRows, destCols := extent(dest.Props)
	if landing.LastRow > destRows || landing.LastCol > destCols {
		return transform{}, Errorf("invalid",
			"%s would land on %s, past the end of %q, which has %d rows and %d columns; manage_sheet resize grows it first",
			a1.FormatRect(rect), a1.FormatRect(landing), dest.Props.Title, destRows, destCols)
	}
	if req.Action == TransformCopyPaste {
		what := "copied to"
		if req.Transpose {
			what = "copied, transposed, to"
		}
		return transform{
			op: plan.CopyPaste(props.SheetID, rect, landing, dest.Props.SheetID, paste, req.Transpose),
			applied: render.Applied{Kind: what,
				Value: a1.Format(dest.Props.Title, landing) + " as " + pasteName(req.Paste)},
			destination: landing, destSheet: dest.Props,
		}, nil
	}
	return transform{
		op: plan.CutPaste(props.SheetID, rect, dest.Props.SheetID, anchor.FirstRow, anchor.FirstCol, paste),
		applied: render.Applied{Kind: "moved to",
			Value: a1.Format(dest.Props.Title, landing) + " as " + pasteName(req.Paste)},
		destination: landing, destSheet: dest.Props,
		emptied: a1.FormatRect(rect),
	}, nil
}

func pasteName(paste string) string {
	if strings.TrimSpace(paste) == "" {
		return "normal"
	}
	return strings.ToLower(strings.TrimSpace(paste))
}

// fillDestination is the rectangle an auto-fill writes into: the cells
// past the source, in the direction it is filling.
//
// Checked against the sheet, not against a spreadsheet's limits. Filling
// past a 1000-row sheet is the ordinary way to get this wrong, and the
// refusal that names the sheet's size and the tool that grows it is
// worth more than Google's "exceeds grid limits" — which is what the
// caller got while this only knew about the ten-million-cell ceiling.
func fillDestination(source a1.Rect, down bool, length int, sheet string, rows, cols int) (a1.Rect, error) {
	dest := source
	if down {
		dest.FirstRow, dest.LastRow = source.LastRow+1, source.LastRow+length
	} else {
		dest.FirstCol, dest.LastCol = source.LastCol+1, source.LastCol+length
	}
	if dest.LastRow > rows || dest.LastCol > cols {
		return a1.Rect{}, Errorf("invalid",
			"filling that far reaches %s, past the end of %q, which has %d rows and %d columns; "+
				"manage_sheet resize grows it first",
			a1.FormatRect(dest), sheet, rows, cols)
	}
	return dest, nil
}

// readable refuses a guard read larger than one request can cover, with
// the reason that read exists. Three of these before it was one helper,
// and two of the three had no cap at all.
func readable(rect a1.Rect, why string) error {
	cells, _ := rect.Cells()
	if cells <= MaxWriteCells {
		return nil
	}
	return Errorf("invalid", "%s is %d cells and %s, which covers at most %d; do it in parts",
		a1.FormatRect(rect), cells, why, MaxWriteCells)
}

// sortColumnsInside refuses a sort key outside the range.
//
// The API's dimensionIndex is the sheet's own column, not an offset into
// the range, so a key outside the range is a request Google refuses with
// a message naming an index the caller never typed.
func sortColumnsInside(specs []*gsheets.SortSpec, rect a1.Rect) error {
	for _, spec := range specs {
		col := spec.DimensionIndex + 1
		if col < rect.FirstCol || col > rect.LastCol {
			name, _ := a1.ColumnName(col)
			return Errorf("invalid", "column %s is not inside %s, and a sort key has to be a column of the range",
				name, a1.FormatRect(rect))
		}
	}
	return nil
}

// transformGuard reads what the action would destroy.
//
// Three shapes of destination, and each is read only when the action has
// one: a paste lands on cells the caller never named, a split spills
// into the columns to its right, and a replacement inside formulas
// rewrites what a cell computes rather than what it shows.
func (s *Service) transformGuard(ctx context.Context, req TransformRequest, ref Reference,
	props *gsheets.SheetProperties, rect a1.Rect, t transform,
) (plan.Report, error) {
	card, err := s.card(ctx, ref.ID)
	if err != nil {
		return plan.Report{}, err
	}
	sheet := sheetOf(card, props.SheetID)
	report := plan.CheckDestination(&grid.Grid{
		Sheet: props.Title, SheetID: props.SheetID, Rect: rect,
		Merges:    overlapping(sheet.Merges, rect),
		Protected: protections(sheet.ProtectedRanges, rect),
	})

	switch req.Action {
	case TransformCopyPaste, TransformCutPaste, TransformAutoFill:
		if t.destSheet == nil {
			return report, nil
		}
		if err := readable(t.destination, "the destination is read first to say what it would replace"); err != nil {
			return plan.Report{}, err
		}
		landing, err := s.readTarget(ctx, target{ref: ref, props: t.destSheet, rect: t.destination})
		if err != nil {
			return plan.Report{}, err
		}
		return merged(report, plan.Check(landing, nil, false)), nil

	case TransformTextToColumns:
		return s.splitGuard(ctx, ref, props, rect, t.delimiter, report)

	case TransformFindReplace:
		if !req.InFormulas {
			return report, nil
		}
		// The same cap the paste branch has. Without it a find_replace
		// with in_formulas over a whole sheet — which is what an
		// unbounded range clamps to — reads every cell on it to find the
		// formulas.
		if err := readable(rect, "the range is read first to find the formulas in it"); err != nil {
			return plan.Report{}, err
		}
		source, err := s.readTarget(ctx, target{ref: ref, props: props, rect: rect})
		if err != nil {
			return plan.Report{}, err
		}
		// Only the formulas. A replacement inside formula text changes
		// what the cell computes, which is the invisible half of this
		// action; the values it also rewrites are what the caller asked
		// for.
		for i, row := range source.Cells {
			for j, cell := range row {
				if cell.HasFormula() {
					report.Formulas.Add(source.Address(i, j))
				}
			}
		}
		return report, nil
	}
	return report, nil
}

// splitGuard reads the columns a text_to_columns would spill into.
//
// How wide the split lands is not knowable before the call unless the
// delimiter is given: with Google detecting it, this looks at a window
// to the right and treats all of it as at risk, and the refusal says
// that is what it is doing.
func (s *Service) splitGuard(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	rect a1.Rect, delimiter plan.Delimiter, report plan.Report,
) (plan.Report, error) {
	_, cols := extent(props)
	if rect.LastCol >= cols {
		return report, nil
	}
	// The source is read only when the delimiter is known, because that
	// is the only branch that looks at it. With Google detecting the
	// delimiter — which is the default, and so the likeliest call — this
	// used to fetch up to fifty thousand cells and discard them.
	width := splitWindow
	if delimiter.Sep != "" {
		if err := readable(rect, "the column is read first to see how far the split would reach"); err != nil {
			return plan.Report{}, err
		}
		source, err := s.readTarget(ctx, target{ref: ref, props: props, rect: rect})
		if err != nil {
			return plan.Report{}, err
		}
		width = 0
		for _, row := range source.Cells {
			for _, cell := range row {
				width = max(width, strings.Count(cell.Raw, delimiter.Sep))
			}
		}
		if width == 0 {
			// Nothing in the column holds the delimiter, so the split
			// lands nowhere but where it started.
			return report, nil
		}
	}
	spill := a1.Rect{
		FirstRow: rect.FirstRow, FirstCol: rect.LastCol + 1,
		LastRow: rect.LastRow, LastCol: min(rect.LastCol+width, cols),
	}
	landing, err := s.readTarget(ctx, target{ref: ref, props: props, rect: spill})
	if err != nil {
		return plan.Report{}, err
	}
	return merged(report, plan.Check(landing, nil, false)), nil
}

// merged folds the findings over a destination into the findings over
// the source, so one refusal names everything in the way.
//
// plan.Report.Merge does the folding, because it is the one place that
// knows every field there is. The version here covered four of eleven,
// so a paste onto cells carrying notes or validation reported neither.
func merged(into, from plan.Report) plan.Report {
	into.Merge(from)
	return into
}

// defaultSheet is the sheet a destination range means when it names
// none: the one the call is already about. A destination that does name
// a sheet is left to speak for itself, because ResolveRange refuses a
// range and a sheet argument that disagree.
func defaultSheet(sheet, rangeA1 string) string {
	if ref, err := a1.Parse(rangeA1); err == nil && ref.HasSheet {
		return ""
	}
	return sheet
}
