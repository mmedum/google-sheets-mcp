package service

import (
	"context"
	"fmt"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
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
	Cells       int    `json:"cells" jsonschema:"cells the clear removed, which excludes any showing a value nobody typed"`
	Formulas    int    `json:"formulas" jsonschema:"how many of them held formulas"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r ClearResult) Render() string { return r.Summary }

// clearNotes says what a clear does beyond the cells it takes out
// directly, in one place for all three things that report a clear.
//
// One place because the confirm gate used to be the only path that said
// any of it, while its own closing words send the caller to dry_run —
// so a caller who followed the advice was told the destructive call
// would remove nothing. The result afterwards was worse: it announced
// that the cells were still there.
//
// Two findings, and the pivot one comes first because it subsumes the
// other. Verified live (spike Q): `values.clear` over a pivot's anchor
// returns 200 with `clearedRange` naming that one cell, and the pivot's
// definition and every cell of its output are gone — eleven cells for a
// reply that mentions one. That is the sixth silent destroy this
// project has found and the first in a shipped tool, and it is not the
// documented way to delete a pivot table either.
//
// Where no anchor is in the range, what is left to say is about cells
// nobody typed. A clear does not take those out itself — that much is
// live-verified — but it is not the promise of survival this used to
// make: clearing an array formula takes its whole spill with it, and
// the spill cells look exactly like a pivot's output from here. So the
// sentence says what the clear does and what it depends on, and claims
// nothing it cannot know.
func clearNotes(g *grid.Grid, counts grid.Counts, past bool) string {
	if counts.Pivots > 0 {
		anchors := plan.PivotAnchors(g)
		// Its own sentence, after the one about undo rather than inside
		// it. A clause spliced into the middle leaves ", and Sheets
		// cannot undo it" hanging off a full stop, which is the defect
		// phase 4 found by reading a transcript rather than counting it.
		verb := "takes"
		if past {
			verb = "took"
		}
		return fmt.Sprintf(" %s %s a pivot table, and clearing that cell %s the whole table and every cell it "+
			"draws, wherever it reaches — which the count does not include.",
			anchors, anchors.Verb("anchors", "anchor"), verb)
	}
	if counts.Computed == 0 {
		return ""
	}
	verb, dep := "show", "go"
	if past {
		verb, dep = "showed", "went"
	}
	return fmt.Sprintf(" %d further cell(s) %s a value nobody typed. A clear does not take those out itself; "+
		"they %s only if whatever draws them was in the range too.", counts.Computed, verb, dep)
}

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
		Range: a1.Format(props.Title, full),
		// What the clear removes, not what is in the rectangle: the two
		// differ wherever something else draws a cell, and the structured
		// half should not be able to disagree with the sentence.
		Cells:    counts.Removable(),
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
		res.Summary = render.ClearPreview(res.Range, counts, clearNotes(before, counts, false))
		return res, nil
	}
	if !req.Confirm {
		return nil, Errorf("blocked",
			"clearing %s removes %d cell(s), %d of them formulas, and Sheets cannot undo it.%s "+
				"Pass confirm to go ahead, or dry_run to see what is there",
			res.Range, counts.Removable(), counts.Formulas,
			clearNotes(before, counts, false))
	}
	if _, err := s.api.ClearValues(ctx, ref.ID, res.Range); err != nil {
		return nil, wrap(err)
	}
	res.Summary = render.ClearDone(res.Range, counts, clearNotes(before, counts, true))
	return res, nil
}
