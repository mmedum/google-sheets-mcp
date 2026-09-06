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

// The kinds of thing that attach to a range.
const (
	RangeNamed      = "named_range"
	RangeProtected  = "protected_range"
	RangeValidation = "data_validation"
	RangeTable      = "table"
	RangeBanding    = "banding"
	RangeRule       = "conditional_format"
)

// What manage_range does to one of them.
const (
	RangeAdd    = "add"
	RangeUpdate = "update"
	RangeDelete = "delete"
)

// RangeRequest is what manage_range asks for.
type RangeRequest struct {
	Spreadsheet string
	Sheet       string
	Range       string
	Kind        string
	Action      string

	// Name is a named range's or a table's name.
	Name string
	// Description and WarningOnly belong to a protection. WarningOnly is
	// three-valued so an update can leave it as it is.
	Description string
	WarningOnly *bool
	// Condition and Values are a validation rule's or a conditional
	// format rule's test; Strict and Message are the validation's own.
	Condition string
	Values    []string
	Strict    *bool
	Message   string
	// Colour is a banding's base colour or a rule's background;
	// TextColour and Bold are the rest of a rule's format.
	Colour     string
	TextColour string
	Bold       *bool
	// Header gives a banding a heading row in a darker shade.
	Header bool
	// Index names a conditional format rule, which is the only one of
	// these the API identifies by position rather than by what it covers.
	Index  int
	DryRun bool
}

// RangeResult is manage_range's answer.
type RangeResult struct {
	Summary     string   `json:"summary"`
	Spreadsheet string   `json:"spreadsheet"`
	Sheet       string   `json:"sheet"`
	Range       string   `json:"range"`
	Kind        string   `json:"kind"`
	Action      string   `json:"action"`
	Applied     []string `json:"applied,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r RangeResult) Render() string { return r.Summary }

// ManageRange answers manage_range: the things attached to a range
// rather than written into it.
//
// Existing objects are named by the range they cover, not by an id. A
// caller who can say "the protection on B2:B10" has not had to fetch an
// id first, and A1 is this server's contract everywhere else (§17.1).
// Where a range matches more than one, the refusal lists them rather
// than picking.
func (s *Service) ManageRange(ctx context.Context, req RangeRequest) (*RangeResult, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case RangeAdd, RangeUpdate, RangeDelete:
	default:
		return nil, Errorf("invalid", "action %q is not add, update or delete", req.Action)
	}
	at, err := s.locateRect(ctx, req.Spreadsheet, req.Sheet, req.Range)
	if err != nil {
		return nil, err
	}
	ref, props, rect := at.ref, at.props, at.rect
	card, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}

	op, applied, err := s.rangeOp(ctx, req, action, ref, card, props, rect)
	if err != nil {
		return nil, err
	}
	res := &RangeResult{
		Spreadsheet: ref.ID, Sheet: props.Title, Range: a1.Format(props.Title, rect),
		Kind: req.Kind, Action: action,
	}
	res.Applied = appliedLines(applied)
	view := render.Ops{Range: res.Range, Applied: applied}

	// A protection over the range refuses everything written into it —
	// except a change to the protection itself, which is how one is
	// lifted. Skipping the check there is the difference between a
	// protection and a trap.
	var report plan.Report
	if req.Kind != RangeProtected {
		sheet := sheetOf(card, props.SheetID)
		report = plan.CheckDestination(&grid.Grid{
			Sheet: props.Title, SheetID: props.SheetID, Rect: rect,
			Merges:    overlapping(sheet.Merges, rect),
			Protected: protections(sheet.ProtectedRanges, rect),
		})
		// A merged range the rectangle only half covers is not in this
		// write's way. The refusal exists because a value write into
		// half a merge is applied to the anchor and silently dropped
		// elsewhere; nothing here writes values. A name, a protection, a
		// banding and a table do not touch cells at all, and a
		// validation rule applies to the range whether or not a merge
		// crosses it, discarding nothing either way.
		report.Merges = nil
	}
	if req.DryRun {
		res.DryRun = true
		view.Blockers = blockerLines(report, plan.Ack{})
		res.Summary = render.OpsPreview(view)
		return res, nil
	}
	if err := refuse(report, plan.Ack{}); err != nil {
		return nil, err
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{op},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.Summary = render.OpsDone(view)
	return res, nil
}

// rangeOp compiles one kind and action into one union member.
func (s *Service) rangeOp(ctx context.Context, req RangeRequest, action string, ref Reference,
	card *gsheets.Spreadsheet, props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case RangeNamed:
		return namedRangeOp(req, action, card, props, rect)
	case RangeProtected:
		return protectedOp(req, action, card, props, rect)
	case RangeValidation:
		return validationOp(req, action, props, rect)
	case RangeTable:
		return tableOp(req, action, card, props, rect)
	case RangeBanding:
		return bandingOp(req, action, card, props, rect)
	case RangeRule:
		return s.ruleOp(ctx, req, action, ref, props, rect)
	}
	return nil, nil, Errorf("invalid",
		"kind %q is not one of named_range, protected_range, data_validation, table, banding, conditional_format",
		req.Kind)
}

func namedRangeOp(req RangeRequest, action string, card *gsheets.Spreadsheet,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, nil, Errorf("invalid", "a named range needs name")
	}
	existing := findNamedRange(card, name)
	switch action {
	case RangeAdd:
		if existing != nil {
			return nil, nil, Errorf("invalid",
				"this spreadsheet already has a named range called %q; update moves it", name)
		}
		return plan.NamedRangeAdd(name, props.SheetID, rect),
			[]render.Applied{{Kind: "named range added", Value: name}}, nil
	case RangeUpdate:
		if existing == nil {
			return nil, nil, notNamed(card, name)
		}
		return plan.NamedRangeMove(existing.NamedRangeID, props.SheetID, rect),
			[]render.Applied{{Kind: "named range moved", Value: name}}, nil
	default:
		if existing == nil {
			return nil, nil, notNamed(card, name)
		}
		return plan.NamedRangeDelete(existing.NamedRangeID),
			[]render.Applied{{Kind: "named range deleted", Value: name}}, nil
	}
}

func notNamed(card *gsheets.Spreadsheet, name string) error {
	names := make([]string, 0, len(card.NamedRanges))
	for _, nr := range card.NamedRanges {
		names = append(names, nr.Name)
	}
	if len(names) == 0 {
		return Errorf("not_found", "no named range called %q; this spreadsheet has none", name)
	}
	return Errorf("not_found", "no named range called %q; it has %s", name, join(names))
}

func findNamedRange(card *gsheets.Spreadsheet, name string) *gsheets.NamedRange {
	for _, nr := range card.NamedRanges {
		if nr != nil && nr.Name == name {
			return nr
		}
	}
	return nil
}

func protectedOp(req RangeRequest, action string, card *gsheets.Spreadsheet,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	sheet := sheetOf(card, props.SheetID)
	if action == RangeAdd {
		warning := req.WarningOnly != nil && *req.WarningOnly
		what := "protected"
		if warning {
			what = "protected with a warning only"
		}
		return plan.ProtectedAdd(props.SheetID, rect, req.Description, warning),
			[]render.Applied{{Kind: what, Value: a1.FormatRect(rect)}}, nil
	}
	found, err := matchOne("protection", rect, rectsOf(sheet.ProtectedRanges, func(p *gsheets.ProtectedRange) *gsheets.GridRange { return p.Range }, props))
	if err != nil {
		return nil, nil, err
	}
	target := sheet.ProtectedRanges[found]
	if action == RangeDelete {
		return plan.ProtectedDelete(target.ProtectedRangeID),
			[]render.Applied{{Kind: "protection removed from", Value: a1.FormatRect(rect)}}, nil
	}
	if req.Description == "" && req.WarningOnly == nil {
		return nil, nil, Errorf("invalid", "updating a protection needs description, warning_only, or both")
	}
	warning := req.WarningOnly != nil && *req.WarningOnly
	return plan.ProtectedUpdate(target.ProtectedRangeID, req.Description, warning, req.WarningOnly != nil),
		[]render.Applied{{Kind: "protection updated on", Value: a1.FormatRect(rect)}}, nil
}

func validationOp(req RangeRequest, action string, props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	if action == RangeDelete {
		return plan.Validation(props.SheetID, rect, nil),
			[]render.Applied{{Kind: "validation removed from", Value: a1.FormatRect(rect)}}, nil
	}
	cond, err := plan.ParseCondition(req.Condition, req.Values)
	if err != nil {
		return nil, nil, Errorf("invalid", "%s", err)
	}
	// Strict by default: a rule that only warns is a rule a paste walks
	// straight through, and the caller who wanted the softer one can say
	// so.
	strict := req.Strict == nil || *req.Strict
	rule := plan.ValidationRule(cond, strict, req.Message)
	return plan.Validation(props.SheetID, rect, rule),
		[]render.Applied{{Kind: "validation set on", Value: a1.FormatRect(rect) + ": " + render.ConditionText(cond)}}, nil
}

func tableOp(req RangeRequest, action string, card *gsheets.Spreadsheet,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	sheet := sheetOf(card, props.SheetID)
	name := strings.TrimSpace(req.Name)
	if action == RangeAdd {
		if name == "" {
			return nil, nil, Errorf("invalid", "a table needs name")
		}
		return plan.TableAdd(name, props.SheetID, rect),
			[]render.Applied{{Kind: "table added", Value: name}}, nil
	}
	found, err := matchOne("table", rect, rectsOf(sheet.Tables, func(t *gsheets.Table) *gsheets.GridRange { return t.Range }, props))
	if err != nil {
		return nil, nil, err
	}
	target := sheet.Tables[found]
	if action == RangeDelete {
		return plan.TableDelete(target.TableID),
			[]render.Applied{{Kind: "table removed from", Value: a1.FormatRect(rect)}}, nil
	}
	if name == "" {
		return nil, nil, Errorf("invalid", "updating a table needs name, the new name for it")
	}
	return plan.TableRename(target.TableID, name),
		[]render.Applied{{Kind: "table renamed to", Value: name}}, nil
}

func bandingOp(req RangeRequest, action string, card *gsheets.Spreadsheet,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	sheet := sheetOf(card, props.SheetID)
	colours := func() (*gsheets.BandingProperties, error) {
		colour, err := plan.ParseColour(req.Colour)
		if err != nil {
			return nil, Errorf("invalid", "%s", err)
		}
		if colour == nil {
			return nil, Errorf("invalid", "banding needs colour, a hex colour such as #d9e2f3")
		}
		return plan.Banding(colour, req.Header), nil
	}
	if action == RangeAdd {
		shades, err := colours()
		if err != nil {
			return nil, nil, err
		}
		return plan.BandingAdd(props.SheetID, rect, shades),
			[]render.Applied{{Kind: "banding added to", Value: a1.FormatRect(rect) + " in " + req.Colour}}, nil
	}
	found, err := matchOne("banding", rect, rectsOf(sheet.BandedRanges, func(b *gsheets.BandedRange) *gsheets.GridRange { return b.Range }, props))
	if err != nil {
		return nil, nil, err
	}
	existing := sheet.BandedRanges[found]
	if action == RangeDelete {
		return plan.BandingDelete(existing.BandedRangeID),
			[]render.Applied{{Kind: "banding removed from", Value: a1.FormatRect(rect)}}, nil
	}
	shades, err := colours()
	if err != nil {
		return nil, nil, err
	}
	// The axis is the one the banding already has. Writing rowProperties
	// onto a column banding leaves it carrying both sets, which the API
	// rejects.
	return plan.BandingUpdate(existing.BandedRangeID, columnBanding(existing), shades),
		[]render.Applied{{Kind: "banding recoloured on", Value: a1.FormatRect(rect)}}, nil
}

// ruleOp acts on a conditional format rule, which the API identifies by
// its position in the sheet's list rather than by what it covers.
//
// The list is read first so an index past the end is refused with how
// many there are, rather than with the API's own message, which names
// neither the sheet nor the count.
func (s *Service) ruleOp(ctx context.Context, req RangeRequest, action string, ref Reference,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	if action != RangeAdd {
		count, err := s.ruleCount(ctx, ref, props.SheetID)
		if err != nil {
			return nil, nil, err
		}
		if req.Index < 0 || req.Index >= count {
			return nil, nil, Errorf("invalid",
				"index %d is outside 0..%d; %q has %d conditional format rule(s), and read_formatting lists them with "+
					"their indexes", req.Index, count-1, props.Title, count)
		}
	}
	if action == RangeDelete {
		return plan.RuleDelete(req.Index, props.SheetID),
			[]render.Applied{{Kind: "conditional rule deleted", Value: fmt.Sprintf("index %d", req.Index)}}, nil
	}
	cond, err := plan.ParseCondition(req.Condition, req.Values)
	if err != nil {
		return nil, nil, Errorf("invalid", "%s", err)
	}
	format, err := ruleFormat(req)
	if err != nil {
		return nil, nil, err
	}
	// One rule, described by the same function read_formatting uses. The
	// two used to compose the sentence separately, so what manage_range
	// said it had written and what read_formatting said was there could
	// drift — and the service half is the one no golden covers.
	rule := plan.Rule(props.SheetID, rect, cond, format)
	text := render.RuleText(rule)
	if action == RangeAdd {
		return plan.RuleAdd(req.Index, rule),
			[]render.Applied{{Kind: "conditional rule added", Value: text}}, nil
	}
	return plan.RuleUpdate(req.Index, props.SheetID, rule),
		[]render.Applied{{Kind: fmt.Sprintf("conditional rule %d replaced", req.Index), Value: text}}, nil
}

// ruleFormat is what a rule applies when its condition holds.
func ruleFormat(req RangeRequest) (*gsheets.CellFormat, error) {
	format := &gsheets.CellFormat{}
	if req.Colour != "" {
		colour, err := plan.ParseColour(req.Colour)
		if err != nil {
			return nil, Errorf("invalid", "%s", err)
		}
		format.BackgroundColorStyle = colour
	}
	if req.TextColour != "" || (req.Bold != nil && *req.Bold) {
		text := &gsheets.TextFormat{Bold: req.Bold != nil && *req.Bold}
		if req.TextColour != "" {
			colour, err := plan.ParseColour(req.TextColour)
			if err != nil {
				return nil, Errorf("invalid", "%s", err)
			}
			text.ForegroundColorStyle = colour
		}
		format.TextFormat = text
	}
	if format.BackgroundColorStyle == nil && format.TextFormat == nil {
		return nil, Errorf("invalid",
			"a conditional format rule needs a format to apply: colour, text_colour, bold, or several")
	}
	return format, nil
}

// ruleCount reads how many conditional format rules a sheet has.
//
// Its own mask, naming the rules and the sheet ids and nothing else.
// FormatFields was used here first and was wrong: `includeGridData` is
// ignored when a field mask is set, so a mask naming `data(` fetches
// cells whether or not the option asks for them — and with no range,
// every cell of every sheet.
func (s *Service) ruleCount(ctx context.Context, ref Reference, sheetID int) (int, error) {
	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.RuleFields})
	if err != nil {
		return 0, wrap(err)
	}
	return len(sheetOf(got, sheetID).ConditionalFormats), nil
}

// matchOne finds the one object covering exactly this rectangle.
//
// Exactly, not overlapping. A protection over a column and one over a
// cell inside it are different objects, and a call meaning the second
// must not reach the first — so the range is the identifier, and where
// it is not unique the caller is asked to say which.
func matchOne(what string, rect a1.Rect, rects []a1.Rect) (int, error) {
	var found []int
	for i, r := range rects {
		if r == rect {
			found = append(found, i)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		var covering []string
		for _, r := range rects {
			if r.Overlaps(rect) {
				covering = append(covering, a1.FormatRect(r))
			}
		}
		if len(covering) > 0 {
			return 0, Errorf("not_found",
				"no %s covers exactly %s; the one(s) nearby cover %s, and the range has to match",
				what, a1.FormatRect(rect), join(covering))
		}
		return 0, Errorf("not_found", "no %s covers %s", what, a1.FormatRect(rect))
	}
	return 0, Errorf("ambiguous",
		"%d %ss cover exactly %s; get_spreadsheet lists them, and this server names one by the range it covers",
		len(found), what, a1.FormatRect(rect))
}

// columnBanding reports which axis a banding runs along.
func columnBanding(b *gsheets.BandedRange) bool {
	return b != nil && b.ColumnProperties != nil
}

// rectsOf is where every attached object's range comes from, in the
// coordinates the caller's range is in.
//
// The clamp is part of it rather than a second pass, because the two
// have to happen together: a stored range that is unbounded — what the
// Sheets interface writes for a whole sheet — matches nothing a caller
// could type until it is pulled into the same coordinates.
func rectsOf[T any](xs []T, rangeOf func(T) *gsheets.GridRange, props *gsheets.SheetProperties) []a1.Rect {
	rows, cols := extent(props)
	out := make([]a1.Rect, 0, len(xs))
	for _, x := range xs {
		out = append(out, a1.FromGridRange(rangeOf(x)).Clamp(rows, cols))
	}
	return out
}
