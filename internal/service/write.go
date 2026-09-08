package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Input options as the caller spells them. The server never substitutes
// one for the other: which is right depends on the data, and only the
// caller knows whether 007 is a number or a product code (§17.3).
const (
	InputTyped   = "typed"
	InputLiteral = "literal"
)

// WriteRequest is what write_values asks for.
type WriteRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	// Values is rows of scalars; TSV is the same thing as bulk text.
	// Exactly one.
	Values [][]any
	TSV    string
	Input  string
	// The acknowledgements the guard requires, named as the tool names
	// them. Plain fields rather than plan's own type, so the tool layer
	// describes the arguments and the service translates.
	Overwrite             bool
	OverwriteFormulas     bool
	AllowExternalFormulas bool
	ExpectCheckpoint      string
	DryRun                bool
}

// Ack is what the guard is told.
func (r WriteRequest) Ack() plan.Ack {
	return plan.Ack{
		Overwrite: r.Overwrite, OverwriteFormulas: r.OverwriteFormulas,
		AllowExternalFormulas: r.AllowExternalFormulas,
	}
}

// Coercion is one cell Google did not store as it was sent.
type Coercion struct {
	Address   string `json:"address"`
	Sent      string `json:"sent"`
	SentKind  string `json:"sent_kind" jsonschema:"what it was: text, number, boolean or empty"`
	Stored    string `json:"stored"`
	Kind      string `json:"kind" jsonschema:"what it became: number, boolean, text or empty"`
	Displayed string `json:"displayed,omitempty" jsonschema:"what the cell shows, when it differs from what is stored: a date is stored as a serial number and displayed as a date"`
}

// WriteResult is write_values' answer.
type WriteResult struct {
	Summary     string `json:"summary" jsonschema:"what happened, as text: the region written, what was coerced, what was lost"`
	Spreadsheet string `json:"spreadsheet"`
	Sheet       string `json:"sheet"`
	Range       string `json:"range" jsonschema:"the range actually written, in A1 with the sheet quoted"`
	Cells       int    `json:"cells" jsonschema:"how many cells the write covered"`
	Checkpoint  string `json:"checkpoint,omitempty" jsonschema:"a checkpoint over what was just written, to hand to a later write"`
	// Coerced, Formulas and Lost are the three things a caller cannot see
	// from the values they sent.
	Coerced  []Coercion `json:"coerced,omitempty" jsonschema:"cells Google stored differently from how they were sent"`
	Formulas []string   `json:"formulas_created,omitempty" jsonschema:"cells that now hold a formula"`
	Kept     []string   `json:"kept,omitempty" jsonschema:"notes and validation rules the write left in place; a rule that survives still applies to the value that replaced the old one"`
	// TailLeft names the part of the range the values did not reach,
	// which still holds whatever it held: values.update skips cells with
	// no data rather than clearing them.
	TailLeft string `json:"tail_left,omitempty" jsonschema:"the part of the range the values did not cover, which was left as it was"`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"true when nothing was sent"`
	Grid     string `json:"grid,omitempty" jsonschema:"the region after the write, as an addressed grid"`
}

// Render is the text half.
func (r WriteResult) Render() string { return r.Summary }

// Write answers write_values.
//
// The path is fixed (§7.3): resolve, read the target, guard, compare the
// checkpoint, send one request, diff what came back, report. The read
// before the write is what makes the guard possible, and it is the only
// extra request an ordinary write costs.
func (s *Service) Write(ctx context.Context, req WriteRequest) (*WriteResult, error) {
	input, typed, err := parseInput(req.Input)
	if err != nil {
		return nil, err
	}
	values, err := valuesOf(req.Values, req.TSV)
	if err != nil {
		return nil, err
	}
	tgt, err := s.locate(ctx, req.Spreadsheet, req.Sheet, req.Range, len(values), len(values[0]))
	if err != nil {
		return nil, err
	}

	before, err := s.readTarget(ctx, tgt)
	if err != nil {
		return nil, err
	}
	if err := checkpointMatches(req.ExpectCheckpoint, tgt.ref.ID, before, tgt.rect); err != nil {
		return nil, err
	}

	report := plan.Check(before, values, typed)
	// Only where the guard has already refused, and it reads to answer,
	// so it goes here rather than inside the guard: plan is a pure
	// function over what was read.
	s.pivotsBehind(ctx, tgt, &report, req.Ack())
	res := &WriteResult{
		Spreadsheet: tgt.ref.ID,
		Sheet:       tgt.props.Title,
		Range:       a1.Format(tgt.props.Title, tgt.rect),
		Cells:       len(values) * len(values[0]),
		Kept:        report.Keeps(),
		TailLeft:    tailLeft(tgt),
	}
	// The preview comes before the refusal, not after it.
	//
	// The refusal's own last sentence is "dry_run shows what would change
	// without sending anything", and with the guard first a caller who
	// followed that advice got the same refusal again. A dry run is the
	// one call that should always answer: it sends nothing, so there is
	// nothing to guard.
	if req.DryRun {
		res.DryRun = true
		res.Grid = renderRegion(before)
		res.Summary = render.WritePreview(render.Write{
			Range: res.Range, Cells: res.Cells, Kept: res.Kept, TailLeft: res.TailLeft,
			Grid: res.Grid,
			// From the report rather than a second walk of the grid: it
			// counted the same cells a moment ago.
			Counts:   grid.Counts{NonEmpty: report.NonEmpty.Total, Formulas: report.Formulas.Total},
			Blockers: blockerLines(report, req.Ack()),
		})
		return res, nil
	}
	if err := refuse(report, req.Ack()); err != nil {
		return nil, err
	}

	got, err := s.api.UpdateValues(ctx, tgt.ref.ID, res.Range, values, gapi.WriteOptions{Input: input})
	if err != nil {
		return nil, wrap(err)
	}
	// Google's own count, not the size of what was sent: the two agree
	// today and the result should not be able to disagree with itself.
	res.Cells = got.UpdatedCells
	back := s.readBack(ctx, tgt.ref.ID, tgt.props.Title, tgt.props.SheetID, tgt.rect,
		values, storedValues(got), typed)
	res.Coerced, res.Formulas, res.Grid = back.coerced, back.formulas, back.grid
	res.Checkpoint = back.checkpoint
	res.Summary = render.WriteDone(render.Write{
		Range: res.Range, Cells: res.Cells, Kept: res.Kept, TailLeft: res.TailLeft,
		Grid: res.Grid, Checkpoint: res.Checkpoint,
		Coerced: coercionLines(res.Coerced), Formulas: res.Formulas,
	})
	return res, nil
}

// readBack is what every write does with the values Google handed back:
// diff them against what was sent, name the formulas, render the region
// and hash a checkpoint over it.
//
// One place, because write, append and create each did the same six
// steps and the display read-back's policy — when to pay for it, and
// what a failure costs — is worth having in one of them rather than
// three.
type readBackResult struct {
	coerced    []Coercion
	formulas   []string
	grid       string
	checkpoint string
}

func (s *Service) readBack(ctx context.Context, id, sheet string, sheetID int, rect a1.Rect,
	sent, stored [][]any, typed bool,
) readBackResult {
	var out readBackResult
	changes := plan.Diff(rect, sent, stored)
	// The display is read back only when something was coerced, and only
	// then because that is the one case where the stored value alone
	// misleads: a date is stored as 46270 and displayed as 2026-09-05,
	// and a report naming only the number reads like data loss.
	if len(changes) > 0 {
		changes = plan.WithDisplay(changes, s.displayed(ctx, id, sheet, rect))
	}
	for _, c := range changes {
		out.coerced = append(out.coerced, Coercion{
			Address: c.Address, Sent: c.Sent, SentKind: c.SentKind,
			Stored: c.Stored, Kind: c.Kind, Displayed: c.Displayed,
		})
	}
	out.formulas = plan.Formulas(rect, stored, typed)
	after := grid.FromValues(sheet, sheetID, rect, stored, typed)
	out.grid = renderRegion(after)
	out.checkpoint = grid.Checkpoint(id, after)
	return out
}

// target is a resolved write destination: which spreadsheet, which
// sheet, the rectangle the values cover, and the range the caller named.
type target struct {
	ref   Reference
	props *gsheets.SheetProperties
	// rect is what the write covers: the values' shape anchored at the
	// named range's start. named is what the caller asked for, which may
	// be larger.
	rect  a1.Rect
	named a1.Rect
}

// locate resolves a write's destination and checks it fits, twice: once
// against the range the caller named and once against the sheet.
//
// Both checks are here rather than left to Google, because its own
// messages name neither the shape that was sent nor the size that was
// available. Verified live, a range smaller than the array comes back as
// "Requested writing within range [...], but tried writing to row [3]".
func (s *Service) locate(ctx context.Context, spreadsheet, sheet, rangeA1 string, rows, cols int) (target, error) {
	ref, err := s.Resolve(ctx, spreadsheet)
	if err != nil {
		return target{}, err
	}
	sh, err := s.ResolveRange(ctx, ref, sheet, rangeA1)
	if err != nil {
		return target{}, err
	}
	named := sh.Rect
	firstRow, firstCol := max(named.FirstRow, 1), max(named.FirstCol, 1)
	rect := a1.Rect{
		FirstRow: firstRow, FirstCol: firstCol,
		LastRow: firstRow + rows - 1, LastCol: firstCol + cols - 1,
	}
	// A single cell is where the write starts, not how big it may be.
	// Verified live: values.update given A1 and a three-by-two array
	// wrote A1:B3, so a server that read A1 as a one-cell cap would
	// refuse what Google accepts and force the caller to compute the
	// far corner it is trying not to make them compute.
	if anchorOnly(named) {
		named = rect
	}
	if (named.LastRow != 0 && rect.LastRow > named.LastRow) || (named.LastCol != 0 && rect.LastCol > named.LastCol) {
		return target{}, Errorf("invalid",
			"the values are %d row(s) by %d column(s) and %s has room for %d by %d; widen the range or send fewer values",
			rows, cols, a1.FormatRect(named), max(named.Rows(), 1), max(named.Cols(), 1))
	}
	rowCount, colCount := extent(sh.Props)
	if rect.LastRow > rowCount || rect.LastCol > colCount {
		return target{}, Errorf("invalid",
			"%s would write past the end of %q, which has %d rows and %d columns; manage_sheet resize grows it first",
			a1.FormatRect(rect), sh.Props.Title, rowCount, colCount)
	}
	if cells, _ := rect.Cells(); cells > MaxWriteCells {
		return target{}, Errorf("invalid",
			"%s is %d cells and one write covers at most %d; send it in parts",
			a1.FormatRect(rect), cells, MaxWriteCells)
	}
	return target{ref: ref, props: sh.Props, rect: rect, named: named}, nil
}

// anchorOnly reports whether a range names one cell, which the API
// treats as the corner to start from rather than as the room available.
func anchorOnly(r a1.Rect) bool {
	return r.Bounded() && r.Rows() == 1 && r.Cols() == 1
}

// readTarget reads the rectangle a write would replace, with the field
// mask that carries what a values read cannot show. One request, and the
// only one an ordinary write adds.
func (s *Service) readTarget(ctx context.Context, t target) (*grid.Grid, error) {
	return s.readTargetFields(ctx, t, gapi.GridFields)
}

// readTargetFields is the same read with the mask the caller needs.
// A formatting write asks for the cell formats as well, because the
// difference between a cell's own format and the one it inherits is what
// decides whether clearing takes anything away.
func (s *Service) readTargetFields(ctx context.Context, t target, fields string) (*grid.Grid, error) {
	rangeA1 := a1.Format(t.props.Title, t.rect)
	got, err := s.api.GetSpreadsheet(ctx, t.ref.ID, gapi.GetOptions{
		Fields: fields, Ranges: []string{rangeA1}, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	data, merges, protected := sheetData(got, t.props.SheetID)
	g := grid.Build(t.props.Title, t.props.SheetID, t.rect, data, grid.AsRaw)
	g.Merges = overlapping(merges, t.rect)
	g.Protected = protections(protected, t.rect)
	return g, nil
}

// displayed reads back what a written rectangle shows. A failure costs
// the display and nothing else, so it is not an error.
func (s *Service) displayed(ctx context.Context, id, sheet string, rect a1.Rect) [][]any {
	vr, err := s.api.GetValues(ctx, id, a1.Format(sheet, rect), gapi.ValueOptions{Render: gapi.RenderFormatted})
	if err != nil {
		s.log.DebugContext(ctx, "coercion display read failed", "spreadsheet", gapi.ShortID(id))
		return nil
	}
	return vr.Values
}

// blockerLines is what a preview says would stop the write. The same
// findings the refusal is built from, so the two cannot disagree about
// what is in the way.
func blockerLines(r plan.Report, ack plan.Ack) []string {
	blockers := r.Blockers(ack)
	out := make([]string, 0, len(blockers))
	for _, b := range blockers {
		line := b.Why
		if b.Allow != "" {
			line += "; pass " + b.Allow + " to allow it"
		}
		out = append(out, line)
	}
	return out
}

// refuse turns the guard's findings into the one [blocked] a caller
// reads. Every blocker is listed, not just the first: a caller told
// about the formulas and not the protection would pass a flag and be
// refused again.
func refuse(r plan.Report, ack plan.Ack) error {
	blockers := r.Blockers(ack)
	if len(blockers) == 0 {
		return nil
	}
	var lines []string
	allowable := true
	for _, b := range blockers {
		line := b.Why
		if b.Allow != "" {
			line += "; pass " + b.Allow + " to allow it"
		} else {
			allowable = false
		}
		lines = append(lines, line)
	}
	msg := strings.Join(lines, ". ")
	if allowable {
		msg += ". dry_run shows what would change without sending anything"
	}
	return Errorf("blocked", "%s", msg)
}

// checkpointMatches compares a caller's checkpoint against the target as
// it stands.
//
// Best effort and said to be (§4.7). Sheets has no atomic guard, so this
// narrows the window between a read and a write and cannot close it. The
// refusal names the range, because a checkpoint is over a rectangle and
// one taken from a read of a different rectangle can never match.
func checkpointMatches(expect, id string, g *grid.Grid, rect a1.Rect) error {
	if expect == "" {
		return nil
	}
	if !grid.IsCheckpoint(expect) {
		return Errorf("invalid", "expect_checkpoint %q is not a checkpoint; read_range returns one, and it looks like ck_ followed by twelve hex digits", expect)
	}
	if got := grid.Checkpoint(id, g); got != expect {
		return Errorf("conflict",
			"the checkpoint does not match %s as it stands now (%s). Either the cells changed since you read them, "+
				"or the checkpoint came from a read of a different range — read %s again and pass the checkpoint it returns",
			a1.Format(g.Sheet, rect), got, a1.Format(g.Sheet, rect))
	}
	return nil
}

// tailLeft names the part of the caller's range the values did not
// reach. It is not cleared: the API skips cells with no data, so
// whatever was there is still there, and a caller who thought they had
// replaced the range needs telling.
func tailLeft(t target) string {
	if !t.named.Bounded() || t.named == t.rect {
		return ""
	}
	// Both parts, not whichever is checked first. A one-cell write into
	// A20:C22 leaves a column tail beside the values and a block of rows
	// under them, and naming only one told the caller that the other had
	// been replaced.
	var parts []string
	if t.rect.LastCol < t.named.LastCol {
		parts = append(parts, a1.FormatRect(a1.Rect{
			FirstRow: t.rect.FirstRow, FirstCol: t.rect.LastCol + 1,
			LastRow: t.rect.LastRow, LastCol: t.named.LastCol,
		}))
	}
	if t.rect.LastRow < t.named.LastRow {
		parts = append(parts, a1.FormatRect(a1.Rect{
			FirstRow: t.rect.LastRow + 1, FirstCol: t.named.FirstCol,
			LastRow: t.named.LastRow, LastCol: t.named.LastCol,
		}))
	}
	return strings.Join(parts, " and ")
}

// storedValues pulls the read-back values out of a write's own response.
func storedValues(r *gsheets.UpdateValuesResponse) [][]any {
	if r == nil || r.UpdatedData == nil {
		return nil
	}
	return r.UpdatedData.Values
}

func coercionLines(cs []Coercion) []render.Coercion {
	out := make([]render.Coercion, 0, len(cs))
	for _, c := range cs {
		out = append(out, render.Coercion(c))
	}
	return out
}

// renderRegion is the addressed grid a write result carries, so the
// caller sees what the cells hold now rather than what they sent.
func renderRegion(g *grid.Grid) string {
	return render.Grid(g, render.GridOptions{
		Show: render.ShowValues, MaxChars: writeGridChars, TotalRows: g.Rect.LastRow,
	}).Text
}

// writeGridChars bounds the region a write echoes back. Smaller than a
// read's budget on purpose: the substance of a write result is what
// changed, and the grid is there to orient rather than to be read whole.
const writeGridChars = 4000

// parseInput closes the input enum before anything is sent, and returns
// the one thing every layer below needs to know about it: whether a
// string beginning with "=" becomes a formula.
func parseInput(v string) (option string, formulasEvaluated bool, err error) {
	switch v {
	case "", InputTyped:
		return gapi.InputUserEntered, true, nil
	case InputLiteral:
		return gapi.InputRaw, false, nil
	}
	return "", false, Errorf("invalid",
		"input %q is not typed or literal. typed parses as a person typing, so 007 becomes 7 and 1-2 becomes a date; "+
			"literal stores exactly what you send", v)
}

// valuesOf takes the rows from whichever form the caller used, and
// refuses a ragged array.
//
// Refused rather than sent: verified live, a short row leaves the cells
// beyond it holding their previous values, so a caller who believed they
// had replaced a rectangle would have replaced most of one. Neither
// outcome is guessable from a result.
func valuesOf(values [][]any, tsv string) ([][]any, error) {
	switch {
	case len(values) > 0 && tsv != "":
		return nil, Errorf("invalid", "give values or tsv, not both")
	case tsv != "":
		values = parseTSV(tsv)
	case len(values) == 0:
		return nil, Errorf("invalid", "give the values to write, as values (rows of scalars) or tsv")
	}
	width := len(values[0])
	if width == 0 {
		return nil, Errorf("invalid", "the first row has no values")
	}
	for i, row := range values {
		if len(row) != width {
			return nil, Errorf("invalid",
				"row %d has %d value(s) and row 1 has %d; a write is a rectangle. Pad the short rows with empty "+
					"strings to clear those cells, or write a narrower range to leave them alone",
				i+1, len(row), width)
		}
	}
	return values, nil
}

// parseTSV splits bulk text into rows and cells. Tabs and newlines only:
// anything cleverer would be a CSV parser, and a caller with quoting to
// do has values.
func parseTSV(tsv string) [][]any {
	tsv = strings.TrimSuffix(strings.ReplaceAll(tsv, "\r\n", "\n"), "\n")
	lines := strings.Split(tsv, "\n")
	out := make([][]any, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		row := make([]any, len(fields))
		for i, f := range fields {
			row[i] = f
		}
		out = append(out, row)
	}
	return out
}
