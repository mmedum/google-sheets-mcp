package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Dimension actions.
const (
	DimInsert     = "insert"
	DimMove       = "move"
	DimResize     = "resize"
	DimAutoResize = "auto_resize"
	DimGroup      = "group"
	DimUngroup    = "ungroup"
	DimDelete     = "delete"
)

// DimensionRequest is what edit_dimensions asks for.
type DimensionRequest struct {
	Spreadsheet string
	Sheet       string
	Action      string
	// Dimension is rows or columns; Band is the A1 band, "2:5" or "B:D".
	// The two are checked against each other: a caller who says rows and
	// writes B:D meant one thing and typed another.
	Dimension string
	Band      string
	// To is the destination for move, one-based like the band.
	To int
	// Pixels is the new size for resize.
	Pixels int
	// Inherit takes the formatting of the band before, for insert.
	Inherit bool
	Confirm bool
	DryRun  bool
}

// DimensionResult is edit_dimensions' answer.
type DimensionResult struct {
	Summary     string `json:"summary"`
	Spreadsheet string `json:"spreadsheet"`
	Sheet       string `json:"sheet"`
	Action      string `json:"action"`
	Band        string `json:"band" jsonschema:"the rows or columns acted on"`
	// Shifted says addresses below or right of the change moved, so a
	// checkpoint or an address from before this call is now wrong.
	Shifted bool `json:"addresses_shifted,omitempty" jsonschema:"true when rows or columns were inserted, moved or deleted, so addresses after the band changed"`
	// Anchors are the durable labels a delete takes with the band. The
	// API's reply never mentions them.
	Anchors []string `json:"anchors_removed,omitempty" jsonschema:"the anchors that were on the deleted rows or columns and went with them"`
	// Charts are the charts reading the band. A chart is not deleted
	// with its data: it stays where it is and loses the series, and
	// nothing in the API's reply says so (spike L).
	Charts []render.ChartLoss `json:"charts_affected,omitempty" jsonschema:"the charts reading the deleted rows or columns, which stay in place and lose those series"`
	// Cells and Formulas are what a delete took with it.
	Cells    int  `json:"cells,omitempty"`
	Formulas int  `json:"formulas,omitempty"`
	DryRun   bool `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r DimensionResult) Render() string { return r.Summary }

// EditDimensions answers edit_dimensions.
//
// The delete action is absent from the schema unless the destructive
// flag is set, and needs confirm when it is there: deleting a column
// takes its data with it and nothing in Sheets brings it back. An action
// a model cannot see is one it cannot reach, which is the unregistered
// tool rule applied one level down (§7.4).
func (s *Service) EditDimensions(ctx context.Context, req DimensionRequest) (*DimensionResult, error) {
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
	band, err := s.resolveBand(ctx, ref.ID, props, req.Dimension, req.Band)
	if err != nil {
		return nil, err
	}
	if err := bandFits(band, props); err != nil {
		return nil, err
	}
	if req.Action == DimDelete && !s.cfg.EnableDestructive {
		return nil, Errorf("unsupported",
			"deleting rows or columns is off in this server; start it with GSHEETS_ENABLE_DESTRUCTIVE=true to turn it on")
	}

	act := render.DimensionAct{
		Action: req.Action, Sheet: props.Title,
		Band: render.Band{Rows: band.Rows(), First: band.First, Last: band.Last},
		To:   req.To, Pixels: req.Pixels,
		Shifted: req.Action == DimInsert || req.Action == DimMove || req.Action == DimDelete,
	}
	res := &DimensionResult{
		Spreadsheet: ref.ID, Sheet: props.Title, Action: req.Action, Band: act.Band.String(),
		Shifted: act.Shifted,
	}

	// A delete is counted before it is confirmed, so the confirmation
	// says what it costs rather than asking in the abstract.
	if req.Action == DimDelete {
		counts, err := s.countBand(ctx, ref, props, band)
		if err != nil {
			return nil, err
		}
		res.Cells, res.Formulas = counts.NonEmpty, counts.Formulas
		act.Cells, act.Formulas = counts.NonEmpty, counts.Formulas
		// The anchors on the band go with it, and the API's reply says
		// nothing at all about them (spike K). Same shape as spike J's
		// deleteTable finding: what a delete takes silently is exactly
		// what this server has to name beforehand.
		doomed, err := s.anchorsOnBand(ctx, ref.ID, props.SheetID, band)
		if err != nil {
			return nil, err
		}
		res.Anchors, act.Anchors = doomed, doomed
		// A chart reading the band is the other silent loss, and it is
		// worse than the anchors: the chart survives the delete, keeps
		// its place and its title, and simply stops drawing anything.
		// Live, deleting a charted column left a chart with one domain
		// and zero series and the reply was `{}` (spike L).
		charts, err := s.chartsOnBand(ctx, ref, props, band)
		if err != nil {
			return nil, err
		}
		res.Charts, act.Charts = charts, charts
	}

	op, err := dimensionRequest(req, band, props)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.DimensionPreview(act)
		return res, nil
	}
	if req.Action == DimDelete && !req.Confirm {
		return nil, Errorf("blocked",
			"deleting %s on %q takes %d non-empty cell(s) and %d formula(s) with them%s%s, and Sheets cannot undo it. "+
				"Pass confirm to go ahead",
			act.Band, props.Title, res.Cells, res.Formulas,
			render.AnchorsTaken(res.Anchors), chartClause(res.Charts))
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{op},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.Summary = render.DimensionDone(act)
	return res, nil
}

// dimensionRequest compiles one action into one union member.
//
// It no longer says what the action does: the words are the renderer's,
// which is what keeps a preview, a result and a refusal describing the
// same act in the same terms (§17a.9).
func dimensionRequest(req DimensionRequest, b plan.Band, props *gsheets.SheetProperties) (*gsheets.Request, error) {
	switch req.Action {
	case DimInsert:
		return plan.InsertDimension(props.SheetID, b.Dimension, b.First, b.Last, req.Inherit), nil
	case DimDelete:
		return plan.DeleteDimension(props.SheetID, b.Dimension, b.First, b.Last), nil
	case DimMove:
		if req.To < 1 {
			return nil, Errorf("invalid", "move needs to, a one-based row or column to move the band in front of")
		}
		return plan.MoveDimension(props.SheetID, b.Dimension, b.First, b.Last, req.To), nil
	case DimResize:
		if req.Pixels < 1 {
			return nil, Errorf("invalid", "resize needs pixels, a size greater than zero")
		}
		return plan.ResizeDimension(props.SheetID, b.Dimension, b.First, b.Last, req.Pixels), nil
	case DimAutoResize:
		return plan.AutoResizeDimensions(props.SheetID, b.Dimension, b.First, b.Last), nil
	case DimGroup:
		return plan.GroupDimensions(props.SheetID, b.Dimension, b.First, b.Last), nil
	case DimUngroup:
		return plan.UngroupDimensions(props.SheetID, b.Dimension, b.First, b.Last), nil
	}
	return nil, Errorf("invalid",
		"action %q is not one of insert, move, resize, auto_resize, group, ungroup", req.Action)
}

// bandFits refuses a band past the sheet's allocated size, with the
// size, rather than letting Google refuse it with a message that names
// the grid limits and not the sheet.
func bandFits(b plan.Band, props *gsheets.SheetProperties) error {
	rows, cols := extent(props)
	limit, what := rows, "rows"
	if !b.Rows() {
		limit, what = cols, "columns"
	}
	named := render.Band{Rows: b.Rows(), First: b.First, Last: b.Last}
	if b.First < 1 {
		return Errorf("invalid", "%s starts before the first %s", named, strings.TrimSuffix(what, "s"))
	}
	if b.Last > limit {
		return Errorf("invalid", "%s is past the end of %q, which has %d %s", named, props.Title, limit, what)
	}
	return nil
}

// countBand reads the rows or columns a delete would remove, to say what
// goes with them.
func (s *Service) countBand(ctx context.Context, ref Reference, props *gsheets.SheetProperties, b plan.Band) (grid.Counts, error) {
	rows, cols := extent(props)
	rect := a1.WholeSheet.Clamp(rows, cols)
	if b.Rows() {
		rect.FirstRow, rect.LastRow = b.First, b.Last
	} else {
		rect.FirstCol, rect.LastCol = b.First, b.Last
	}
	window, _ := fit(rect, s.cfg.MaxCells)
	return s.count(ctx, ref, props, window)
}

// chartClause is the refusal's chart sentence. The renderer owns the
// words; this owns where they sit in the sentence around them, which is
// the join the first version got wrong — a clause written for one
// sentence read as a run-on in the other.
func chartClause(losses []render.ChartLoss) string {
	if len(losses) == 0 {
		return ""
	}
	return "; the chart(s) stay where they are and " + render.ChartsAffected(losses)
}
