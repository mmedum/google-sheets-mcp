package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v4/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v4/internal/plan"
	"github.com/mmedum/google-sheets-mcp/v4/internal/render"
)

// AppendRequest is what append_rows asks for.
type AppendRequest struct {
	Spreadsheet string
	Sheet       string
	// Range picks which block to append after, and is required: verified
	// live, a range naming a cell in the first block appends after that
	// block while a whole-sheet range appends after the last one, so a
	// default here would be a coin flip whose outcome is invisible until
	// somebody reads the sheet.
	Range  string
	Values [][]any
	TSV    string
	Input  string
	DryRun bool
}

// AppendResult is append_rows' answer.
type AppendResult struct {
	Summary     string `json:"summary"`
	Spreadsheet string `json:"spreadsheet"`
	Sheet       string `json:"sheet"`
	// Range is where the rows actually landed, which the caller cannot
	// predict and this server does not try to.
	Range string `json:"range,omitempty" jsonschema:"where the rows actually landed, as Google chose it"`
	// TableRange is the block Google appended after.
	TableRange string     `json:"table_range,omitempty" jsonschema:"the contiguous block Google found and appended after"`
	Rows       int        `json:"rows"`
	Cells      int        `json:"cells"`
	Coerced    []Coercion `json:"coerced,omitempty"`
	Formulas   []string   `json:"formulas_created,omitempty"`
	// Shifted says rows below the insert point moved down, so an address
	// or a checkpoint the caller is holding is now wrong.
	Shifted bool   `json:"rows_below_shifted,omitempty" jsonschema:"true when rows were inserted, so everything below the insert point moved down"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Grid    string `json:"grid,omitempty" jsonschema:"the rows as they landed, addressed"`
}

// Render is the text half.
func (r AppendResult) Render() string { return r.Summary }

// Append answers append_rows.
//
// Where the rows land is Google's decision and is reported rather than
// predicted. Verified live: values.append finds the contiguous block the
// given range falls in and writes after that one, and reports it as
// tableRange.
//
// It always inserts. OVERWRITE wrote over whatever followed the block,
// which is chosen during the call, so no read could witness it; a write
// over cells the caller knows is write_values, which reads them first
// (§4.3).
func (s *Service) Append(ctx context.Context, req AppendRequest) (*AppendResult, error) {
	input, typed, err := parseInput(req.Input)
	if err != nil {
		return nil, err
	}
	values, err := valuesOf(req.Values, req.TSV)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Range) == "" {
		return nil, Errorf("invalid",
			"name a range to append after. It picks which block of data the rows go below, not where they land: "+
				"a range inside the first block appends after that block, and the whole sheet appends after the last one")
	}
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	sh, err := s.ResolveRange(ctx, ref, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}

	// The value half of the guard only. There is no destination to read:
	// Google picks it, and a guard that read the wrong rectangle would
	// be worse than none, since it would report an all-clear. Inserting
	// destroys nothing, so nothing else needs checking.
	var report plan.Report
	plan.CheckValues(&report, values, typed, plan.Position)
	ack := s.ack(false, false)

	rangeA1 := a1.Format(sh.Props.Title, sh.Rect)
	res := &AppendResult{
		Spreadsheet: ref.ID,
		Sheet:       sh.Props.Title,
		Rows:        len(values),
		Cells:       len(values) * len(values[0]),
		Shifted:     true,
	}
	// The preview answers before the guard refuses, so a caller told to
	// use dry_run gets a preview rather than the same refusal again.
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.AppendPreview(render.Append{
			Rows: res.Rows, Cells: res.Cells, Searched: rangeA1, Inserting: res.Shifted,
			Blockers: blockerLines(report, ack),
		})
		return res, nil
	}
	if err := refuse(report, ack); err != nil {
		return nil, err
	}

	got, err := s.api.AppendValues(ctx, ref.ID, rangeA1, values, gapi.WriteOptions{Input: input, Insert: gapi.InsertRows})
	if err != nil {
		return nil, wrap(err)
	}
	res.TableRange = got.TableRange
	if got.Updates != nil {
		res.Range = got.Updates.UpdatedRange
		res.Cells = got.Updates.UpdatedCells
	}

	// The addresses come from the range Google reports, so a report that
	// named cells would name the ones the rows actually reached.
	if landed, err := a1.Parse(res.Range); err == nil && landed.Rect.Bounded() {
		back := s.readBack(ctx, ref.ID, sh.Props.Title, sh.Props.SheetID, landed.Rect,
			values, storedValues(got.Updates), typed)
		res.Coerced, res.Formulas, res.Grid = back.coerced, back.formulas, back.grid
	}
	res.Summary = render.AppendDone(render.Append{
		Rows: res.Rows, Cells: res.Cells, Landed: res.Range, TableRange: res.TableRange,
		Searched: rangeA1, Inserting: res.Shifted, Grid: res.Grid,
		Coerced: coercionLines(res.Coerced), Formulas: res.Formulas,
	})
	return res, nil
}
