package service

import (
	"context"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// ClearRequest is what clear_values asks for.
type ClearRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	Confirm     bool
	DryRun      bool
}

// ClearResult is clear_values' answer.
type ClearResult struct {
	Summary     string `json:"summary"`
	Spreadsheet string `json:"spreadsheet"`
	Sheet       string `json:"sheet"`
	Range       string `json:"range" jsonschema:"the range cleared"`
	Cells       int    `json:"cells" jsonschema:"non-empty cells that were cleared"`
	Formulas    int    `json:"formulas" jsonschema:"how many of them held formulas"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r ClearResult) Render() string { return r.Summary }

// Clear empties a range's values and leaves its formatting.
//
// Registered only with GSHEETS_ENABLE_DESTRUCTIVE=true and still needs
// confirm on the call: nothing in Sheets undoes it. The range is read
// first so the confirmation says what is there, and so a caller who
// meant a narrower rectangle finds out before rather than after.
func (s *Service) Clear(ctx context.Context, req ClearRequest) (*ClearResult, error) {
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	ref, props, full := at.ref, at.props, at.rect
	// Refused rather than read in part. The clear is sent for the whole
	// range, so a guard that read only as much as the budget allowed
	// would pass a range with a protected block in the part it never
	// looked at — and the count it reported would be a floor it did not
	// say was one.
	if cells, _ := full.Cells(); cells > MaxWriteCells {
		return nil, Errorf("invalid",
			"%s is %d cells and one clear covers at most %d, because the whole range is read first to say what is "+
				"in it; clear it in parts",
			a1.FormatRect(full), cells, MaxWriteCells)
	}
	before, err := s.readTarget(ctx, target{ref: ref, props: props, rect: full})
	if err != nil {
		return nil, err
	}
	counts := before.Count()

	res := &ClearResult{
		Spreadsheet: ref.ID, Sheet: props.Title,
		Range:    a1.Format(props.Title, full),
		Cells:    counts.NonEmpty,
		Formulas: counts.Formulas,
	}
	// A protected range refuses a clear as surely as it refuses a write,
	// and saying so before the call is the difference between a refusal
	// the caller can act on and one from Google that names an id.
	//
	// CheckDestination rather than Check: these are the refusals that
	// apply to any write to a rectangle, and they are the only ones a
	// clear has. Asking the full guard and then muting the rest with
	// acknowledgements this tool does not offer is what this used to do,
	// and it would have applied a later blocker to a clear silently.
	if err := refuse(plan.CheckDestination(before), plan.Ack{}); err != nil {
		return nil, err
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ClearPreview(res.Range, counts)
		return res, nil
	}
	if !req.Confirm {
		return nil, Errorf("blocked",
			"clearing %s removes %d non-empty cell(s), %d of them formulas, and Sheets cannot undo it. "+
				"Pass confirm to go ahead, or dry_run to see what is there",
			res.Range, counts.NonEmpty, counts.Formulas)
	}
	if _, err := s.api.ClearValues(ctx, ref.ID, res.Range); err != nil {
		return nil, wrap(err)
	}
	res.Summary = render.ClearDone(res.Range, counts)
	return res, nil
}
