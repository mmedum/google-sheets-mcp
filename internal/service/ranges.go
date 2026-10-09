package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v3/internal/grid"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/plan"
	"github.com/mmedum/google-sheets-mcp/v3/internal/render"
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
	// Color is a banding's base color or a rule's background;
	// TextColor and Bold are the rest of a rule's format.
	Color     string
	TextColor string
	Bold      *bool
	// Gradient is a color scale, which a conditional format rule is
	// instead of a condition and a format: two or three points, lowest
	// first, each "<type> [value] #hex".
	Gradient []string
	// ColumnTypes types a table's columns, each "<column> <type>", with
	// a dropdown's options after a colon.
	ColumnTypes []string
	// Header gives a banding a heading row in a darker shade.
	Header bool
	// Index names a conditional format rule, which is the only one of
	// these the API identifies by position rather than by what it covers.
	Index int
	// Overwrite allows the one act here that destroys something: a table
	// delete takes the conditional format rules over its range.
	Overwrite bool
	DryRun    bool
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
		answered, err := s.settleTableAdd(ctx, ref, props, op, err)
		if err != nil {
			return nil, err
		}
		view.Answered = answered
	}
	s.forget(ref.ID)
	res.Summary = render.OpsDone(view)
	return res, nil
}

// settleTableAdd decides a table add Google answered without saying
// whether it ran: an HTTP 500, or a reply that never came. A table has a
// name and a range, so one fresh read of the card settles it. It returns
// what Google answered when the table is there, and an error when it is
// not. Any other request, and a read that fails too, keep the error the
// send returned.
//
// Spike T Q12, 2026-10-09: in a fresh spreadsheet, four adds with column
// types were taken and the fifth was a 500 that made no table. Deleting a
// table did not let it in, nor did waiting a minute, and an add with no
// column types was then taken. What Google counts is not known (§18).
// [ambiguous_outcome] alone left the caller to read the card and compare.
func (s *Service) settleTableAdd(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	op *gsheets.Request, sendErr error) (string, error) {

	if op.AddTable == nil || op.AddTable.Table == nil || !errors.Is(sendErr, gapi.ErrAmbiguousOutcome) {
		return "", wrap(sendErr)
	}
	s.forget(ref.ID)
	card, err := s.card(ctx, ref.ID)
	if err != nil {
		return "", wrap(sendErr)
	}
	want := op.AddTable.Table
	rect := a1.FromGridRange(want.Range).Clamp(extent(props))
	answered := "The call ended before Google answered"
	var ae *gapi.APIError
	if errors.As(sendErr, &ae) {
		answered = fmt.Sprintf("Google answered HTTP %d %s (%s)", ae.Status, ae.RPC, strings.TrimSuffix(ae.Message, "."))
	}
	for _, t := range sheetOf(card, props.SheetID).Tables {
		if t != nil && t.Name == want.Name && a1.FromGridRange(t.Range).Clamp(extent(props)) == rect {
			return answered, nil
		}
	}
	if ae == nil || len(want.ColumnProperties) == 0 {
		return "", Errorf("unavailable",
			"%s. A read afterwards finds no table called %q on %s, so nothing was added and the call can be repeated",
			answered, want.Name, a1.FormatRect(rect))
	}
	return "", Errorf("unavailable",
		"%s. A read afterwards finds no table called %q on %s, so nothing was added. Google has refused table "+
			"adds with column types in a spreadsheet after about four of them, and deleting a table or waiting "+
			"did not let another in. An add without column_types was still taken; whether its columns can be "+
			"typed after that is not known", answered, want.Name, a1.FormatRect(rect))
}

// rangeOp compiles one kind and action into one union member.
func (s *Service) rangeOp(ctx context.Context, req RangeRequest, action string, ref Reference,
	card *gsheets.Spreadsheet, props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	// Deleting a table takes the conditional format rules over its range
	// with it. Verified live: one rule before, none after, and nothing
	// in the reply says so — adding a table leaves them alone, so it is
	// the delete that takes them.
	if strings.EqualFold(strings.TrimSpace(req.Kind), RangeTable) && action == RangeDelete && !req.Overwrite {
		rules, err := s.rulesOver(ctx, ref, props.SheetID, rect)
		if err != nil {
			return nil, nil, err
		}
		if len(rules) > 0 {
			return nil, nil, Errorf("blocked",
				"deleting the table on %s takes %d conditional format rule(s) over that range with it, "+
					"and nothing in Sheets brings them back: %s. Pass overwrite to go ahead",
				a1.FormatRect(rect), len(rules), join(rules))
		}
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if len(req.Gradient) > 0 && kind != RangeRule {
		return nil, nil, Errorf("invalid", "gradient is a color scale, which only kind conditional_format takes")
	}
	if len(req.ColumnTypes) > 0 && (kind != RangeTable || action == RangeDelete) {
		return nil, nil, Errorf("invalid", "column_types types a table's columns, which only kind table takes, on add or update")
	}
	switch kind {
	case RangeNamed:
		return namedRangeOp(req, action, card, props, rect)
	case RangeProtected:
		return protectedOp(req, action, card, props, rect)
	case RangeValidation:
		return validationOp(req, action, card, props, rect)
	case RangeTable:
		return s.tableOp(ctx, req, action, ref, card, props, rect)
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
	if err := plan.CheckNamedRange(name); err != nil {
		return nil, nil, Errorf("invalid", "%s", err)
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

func validationOp(req RangeRequest, action string, card *gsheets.Spreadsheet, props *gsheets.SheetProperties,
	rect a1.Rect) (*gsheets.Request, []render.Applied, error) {
	if action == RangeDelete {
		return plan.Validation(props.SheetID, rect, nil),
			[]render.Applied{{Kind: "validation removed from", Value: a1.FormatRect(rect)}}, nil
	}
	cond, err := plan.ParseCondition(req.Condition, req.Values)
	if err != nil {
		return nil, nil, Errorf("invalid", "%s", err)
	}
	if err := dropdownColumnRule(card, props, rect); err != nil {
		return nil, nil, err
	}
	// Strict by default: a rule that only warns is a rule a paste walks
	// straight through, and the caller who wanted the softer one can say
	// so.
	strict := req.Strict == nil || *req.Strict
	rule := plan.ValidationRule(cond, strict, req.Message)
	return plan.Validation(props.SheetID, rect, rule),
		[]render.Applied{{Kind: "validation set on", Value: a1.FormatRect(rect) + ": " + render.ConditionText(cond)}}, nil
}

// dropdownColumnRule refuses a data validation rule on the cells under a
// dropdown column's header. Google refuses one: "This operation is not
// allowed on cells in typed columns." (spike T Q13). The tables come from
// the card, so the check costs no request.
//
// Only a dropdown column is refused, the type Q13 asked. A column of
// another type, a header cell and a rule's removal go to Google as sent;
// nothing has asked what it does with them.
func dropdownColumnRule(card *gsheets.Spreadsheet, props *gsheets.SheetProperties, rect a1.Rect) error {
	for _, t := range sheetOf(card, props.SheetID).Tables {
		if t == nil || t.Range == nil {
			continue
		}
		table := a1.FromGridRange(t.Range).Clamp(extent(props))
		for _, c := range t.ColumnProperties {
			if c == nil || c.ColumnType != gsheets.ColumnDropdown || table.Rows() < 2 {
				continue
			}
			cells, inside := columnBody(table, c.ColumnIndex).Intersect(rect)
			if !inside {
				continue
			}
			column := c.ColumnName
			if column == "" {
				column, _ = a1.ColumnName(table.FirstCol + c.ColumnIndex)
			}
			return Errorf("invalid",
				"%s is in the column %q of the table %q, which is typed dropdown, and Google sets no data "+
					"validation rule on a cell in a typed column: \"This operation is not allowed on cells in typed "+
					"columns.\" To change the column's list, use kind table, action update and column_types",
				a1.FormatRect(cells), column, t.Name)
		}
	}
	return nil
}

func (s *Service) tableOp(ctx context.Context, req RangeRequest, action string, ref Reference,
	card *gsheets.Spreadsheet, props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	sheet := sheetOf(card, props.SheetID)
	name := strings.TrimSpace(req.Name)
	if action == RangeAdd {
		if name == "" {
			return nil, nil, Errorf("invalid", "a table needs name")
		}
		added := render.Applied{Kind: "table added", Value: name}
		table := rect.Clamp(extent(props))
		_, header, err := s.readHeader(ctx, ref, props, table)
		if err != nil {
			return nil, nil, err
		}
		if err := formulaHeader(header, table); err != nil {
			return nil, nil, err
		}
		if len(req.ColumnTypes) == 0 {
			return plan.TableAdd(name, props.SheetID, rect, nil), []render.Applied{added}, nil
		}
		columns, typed, err := s.typedColumns(ctx, ref, props, rect, req.ColumnTypes, header)
		if err != nil {
			return nil, nil, err
		}
		named, err := nameTyped(header, table, columns)
		if err != nil {
			return nil, nil, err
		}
		applied := make([]render.Applied, 0, 1+len(typed)+len(named))
		applied = append(append(append(applied, added), typed...), named...)
		return plan.TableAdd(name, props.SheetID, rect, columns), applied, nil
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
	if name == "" && len(req.ColumnTypes) == 0 {
		return nil, nil, Errorf("invalid", "updating a table needs name, column_types, or both")
	}
	var applied []render.Applied
	if name != "" {
		applied = append(applied, render.Applied{Kind: "table renamed to", Value: name})
	}
	var columns []*gsheets.TableColumn
	if len(req.ColumnTypes) > 0 {
		now, err := s.readTable(ctx, ref, props, target.TableID, rect)
		if err != nil {
			return nil, nil, err
		}
		changed, typed, err := s.typedColumns(ctx, ref, props, rect, req.ColumnTypes, now.header)
		if err != nil {
			return nil, nil, err
		}
		columns = now.merge(changed)
		applied = append(applied, typed...)
	}
	return plan.TableUpdate(target.TableID, name, columns), applied, nil
}

// typedColumns resolves column_types against a table's range: each
// column, by letter or by header, to its place in the table, counted
// from the table's first column. header is the table's first row, read
// by the caller.
func (s *Service) typedColumns(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	rect a1.Rect, entries []string, header *grid.Grid) ([]*gsheets.TableColumn, []render.Applied, error) {

	specs := make([]plan.ColumnSpec, 0, len(entries))
	table := rect.Clamp(extent(props))
	for _, entry := range entries {
		spec, err := plan.ParseColumnType(entry)
		if err != nil {
			return nil, nil, Errorf("invalid", "%s", err)
		}
		specs = append(specs, spec)
	}
	headers := headings(header)
	at := columnPlace{arg: "column_types", what: "the table"}
	named := map[int]string{}
	columns := make([]*gsheets.TableColumn, 0, len(specs))
	applied := make([]render.Applied, 0, len(specs))
	for _, spec := range specs {
		index, err := headedColumn(spec.Column, table, headers, at)
		if err != nil {
			return nil, nil, err
		}
		if earlier, twice := named[index]; twice {
			return nil, nil, Errorf("invalid", "column_types names one column twice, as %q and %q", earlier, spec.Column)
		}
		named[index] = spec.Column
		column := &gsheets.TableColumn{ColumnIndex: index, ColumnType: spec.Type, DataValidationRule: spec.Rule}
		columns = append(columns, column)
		applied = append(applied, render.Applied{Kind: "column typed", Value: render.ColumnText(spec.Column, column)})
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].ColumnIndex < columns[j].ColumnIndex })
	if err := s.checkColumnCells(ctx, ref, props, table, columns); err != nil {
		return nil, nil, err
	}
	return columns, applied, nil
}

// checkColumnCells refuses a boolean or a dropdown type over cells it
// would change. Every such column is read in one request, after the
// header: a column named by its heading is known only once that read is
// back.
//
// A boolean column shows checkboxes. Spike T Q5 typed one over "maybe",
// the text "TRUE" and an empty cell, and all three became FALSE: a word
// is lost with nothing said, and text that reads TRUE is not a true
// value. A TRUE or FALSE value was kept, on update and on add (Q5b), and
// is let through. What it does to a number or a formula is not known, so
// they are refused too.
//
// A dropdown column brings the table's list. Spike T Q1 typed one over
// cells with a list of their own, and the cells' own rule was gone after,
// with nothing said. So a dropdown is refused while a cell under its
// header has a rule of its own. The table's list is not on its cells (Q1
// read none there), so retyping a dropdown column is not refused over it.
// An update drops the rule as the add did (spike T Q13). A dropdown
// column's cells cannot gain a rule after it is typed, since Google
// refuses one there (Q13), so a column sent back unchanged is not read.
func (s *Service) checkColumnCells(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	table a1.Rect, columns []*gsheets.TableColumn) error {

	var checked []*gsheets.TableColumn
	var ranges []string
	for _, c := range columns {
		if (c.ColumnType != gsheets.ColumnBoolean && c.ColumnType != gsheets.ColumnDropdown) || table.Rows() < 2 {
			continue
		}
		body := columnBody(table, c.ColumnIndex)
		if err := readable(body, "the column is read first to see what its type would replace"); err != nil {
			return err
		}
		checked = append(checked, c)
		ranges = append(ranges, a1.Format(props.Title, body))
	}
	if len(checked) == 0 {
		return nil
	}
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.GridFields, Ranges: ranges, IncludeGridData: true,
	})
	if err != nil {
		return wrap(err)
	}
	// One GridData comes back per range, each saying where it starts.
	byColumn := map[int]*gsheets.GridData{}
	for _, sh := range sp.Sheets {
		if sh.Properties == nil || sh.Properties.SheetID != props.SheetID {
			continue
		}
		for _, data := range sh.Data {
			if data != nil {
				byColumn[data.StartColumn] = data
			}
		}
	}
	var other, ruled plan.Cells
	for _, c := range checked {
		body := columnBody(table, c.ColumnIndex)
		cells := grid.Build(props.Title, props.SheetID, body, byColumn[body.FirstCol-1], grid.AsRaw)
		for i, row := range cells.Cells {
			switch cell := row[0]; {
			case c.ColumnType == gsheets.ColumnBoolean && !cell.Empty() && cell.Kind != grid.KindBool:
				other.AddCell(cells, i, 0)
			case c.ColumnType == gsheets.ColumnDropdown && cell.Validation != "":
				ruled.AddCell(cells, i, 0)
			}
		}
	}
	if other.Any() {
		return Errorf("blocked",
			"%s %s something other than a TRUE or FALSE value, and Google turns such a cell into FALSE when its "+
				"column is typed boolean, text reading TRUE included, so what is there would be lost. Write TRUE or "+
				"FALSE with input typed, which stores a true or false value rather than text, or clear %s, first; "+
				"an empty cell becomes FALSE",
			other, other.Verb("holds", "hold"), other.Verb("it", "them"))
	}
	if ruled.Any() {
		return Errorf("blocked",
			"%s %s, and the table's dropdown would replace %s: Google drops a cell's own rule when its column is "+
				"typed dropdown, and says nothing. To use the table's list, remove the rule from %s first, with "+
				"kind data_validation and action delete",
			ruled, ruled.Verb("has a data validation rule of its own", "have data validation rules of their own"),
			ruled.Verb("it", "them"), ruled.Verb("that cell", "those cells"))
	}
	return nil
}

// columnBody is the cells under a table column's header.
func columnBody(table a1.Rect, index int) a1.Rect {
	col := table.FirstCol + index
	return a1.Rect{FirstRow: table.FirstRow + 1, FirstCol: col, LastRow: table.LastRow, LastCol: col}
}

// formulaHeader refuses a table add over a formula anywhere in its header
// row, in a column the add types or not. Google refuses one: "Formulas
// are not supported in a table header row." (spike T Q10). Refused here,
// the cells are named and a dry run says so too.
func formulaHeader(header *grid.Grid, table a1.Rect) error {
	var formulas plan.Cells
	for j, cell := range header.Cells[0] {
		if cell.HasFormula() {
			formulas.AddCell(header, 0, j)
		}
	}
	if !formulas.Any() {
		return nil
	}
	return Errorf("invalid",
		"%s in the header row of %s %s a formula, and Google takes no table over one: \"Formulas are not "+
			"supported in a table header row.\" Replace %s with the text %s first",
		formulas, a1.FormatRect(table), formulas.Verb("holds", "hold"), formulas.Verb("it", "them"),
		formulas.Verb("it shows", "they show"))
}

// tableNow is a table's columns and its header row, from one read.
type tableNow struct {
	columns []*gsheets.TableColumn
	header  *grid.Grid
}

// readHeader reads the first row of a table's range as it is now: the
// text each cell shows, and whether it holds a formula, a smart chip or
// rich text.
// The tables come in the same read, for an update to find its own.
func (s *Service) readHeader(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	rect a1.Rect) (*gsheets.Spreadsheet, *grid.Grid, error) {

	head := rect
	head.LastRow = head.FirstRow
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.TableFields, Ranges: []string{a1.Format(props.Title, head)}, IncludeGridData: true,
	})
	if err != nil {
		return nil, nil, wrap(err)
	}
	data, _, _ := sheetData(sp, props.SheetID)
	return sp, grid.Build(props.Title, props.SheetID, head, data, grid.AsFormatted), nil
}

// readTable reads a table's columns and its header row as they are now,
// and refuses a header cell that holds a formula, a smart chip or rich
// text.
//
// Fresh rather than from the card. The update sends the whole array
// back, so a cached one would undo a change somebody made in between.
//
// The refusal is there because the update sends every column's name
// (merge says why), and Google writes a name sent into its header cell
// as plain text (spike T Q4), which erases a chip (Q9) and drops rich
// text's runs (Q7). A formula is checked too, though no header written
// through the API holds one: Google replaces one written into a header
// (Q7), write_values refuses to write one there, and Google takes no
// table added over one (Q10). A header made some other way might, and the
// check costs no request: it reads the cells the chip check reads.
func (s *Service) readTable(ctx context.Context, ref Reference, props *gsheets.SheetProperties, tableID string,
	rect a1.Rect) (*tableNow, error) {

	sp, header, err := s.readHeader(ctx, ref, props, rect)
	if err != nil {
		return nil, err
	}
	var table *gsheets.Table
	for _, t := range sheetOf(sp, props.SheetID).Tables {
		if t != nil && t.TableID == tableID {
			table = t
		}
	}
	if table == nil {
		return nil, Errorf("not_found", "the table on %s is gone since this call began; get_spreadsheet lists the tables there are",
			a1.FormatRect(rect))
	}
	// The header read is the caller's first row. A table moved since the
	// card was read has another one, which the formula check below would
	// not have seen.
	if now := a1.FromGridRange(table.Range).Clamp(extent(props)); now != rect {
		return nil, Errorf("not_found", "the table on %s covers %s since this call began; name it by that range",
			a1.FormatRect(rect), a1.FormatRect(now))
	}
	err = headerLoss(header, rect, func(int) bool { return true },
		"Changing a column type sends every column's name back")
	if err != nil {
		return nil, err
	}
	return &tableNow{columns: table.ColumnProperties, header: header}, nil
}

// nameTyped names each typed column of an add after its header cell's
// text, and refuses a header cell the name would replace a formula, a
// smart chip or rich text in.
//
// A typed column sent with no name has Google write "Column 1", "Column
// 2" and so on into its header cell, over what was there (spike T Q1).
// The name sent is the text the cell shows, so the header reads as it
// did. An empty header cell gets one of Google's names, which takes
// nothing; the result says so.
func nameTyped(header *grid.Grid, table a1.Rect, columns []*gsheets.TableColumn) ([]render.Applied, error) {
	typed := map[int]bool{}
	for _, c := range columns {
		typed[c.ColumnIndex] = true
	}
	err := headerLoss(header, table, func(j int) bool { return typed[j] },
		"Typing a column sends its header's text as the column's name")
	if err != nil {
		return nil, err
	}
	var applied []render.Applied
	for _, c := range columns {
		if text := header.Cells[0][c.ColumnIndex].Display; text != "" {
			c.ColumnName = text
			continue
		}
		applied = append(applied, render.Applied{Kind: "empty header",
			Value: header.Address(0, c.ColumnIndex) + `: Google writes a name such as "Column 1" into it`})
	}
	return applied, nil
}

// headerLoss refuses a header cell whose content a column name written
// over it would lose: a formula, a smart chip or rich text. Google writes
// a name sent into its header cell as plain text (spike T Q4): that
// erases a person chip (Q9), and drops the runs that made two letters of
// "Flag" bold, though the name sent was "Flag" (Q7). named says which of
// the row's cells get a name.
func headerLoss(header *grid.Grid, table a1.Rect, named func(int) bool, sends string) error {
	var formulas, chips, rich plan.Cells
	for j, cell := range header.Cells[0] {
		switch {
		case !named(j):
		case cell.HasFormula():
			formulas.AddCell(header, 0, j)
		case cell.Chip:
			chips.AddCell(header, 0, j)
		case cell.RichText:
			rich.AddCell(header, 0, j)
		}
	}
	var held, lost []string
	if formulas.Any() {
		held = append(held, fmt.Sprintf("%s %s a formula", formulas, formulas.Verb("holds", "hold")))
		lost = append(lost, formulas.Verb("the formula", "the formulas"))
	}
	if chips.Any() {
		held = append(held, fmt.Sprintf("%s %s a smart chip (a person or a file link)", chips, chips.Verb("holds", "hold")))
		lost = append(lost, chips.Verb("the chip", "the chips"))
	}
	if rich.Any() {
		held = append(held, fmt.Sprintf("%s %s text with part of it formatted on its own, such as a bold word",
			rich, rich.Verb("holds", "hold")))
		lost = append(lost, "that formatting")
	}
	if len(held) == 0 {
		return nil
	}
	them := "it"
	if formulas.Total+chips.Total+rich.Total > 1 {
		them = "them"
	}
	return Errorf("blocked",
		"in the header row of the table on %s, %s. %s, and Google writes each name into its header cell as "+
			"plain text, so %s would be lost. Make %s plain text first, or set the type in Sheets",
		a1.FormatRect(table), strings.Join(held, " and "), sends, strings.Join(lost, " and "), them)
}

// headings is a header row as typedColumns resolves a column name
// against it.
func headings(header *grid.Grid) map[string]int {
	texts := make([]string, 0, len(header.Cells[0]))
	for _, cell := range header.Cells[0] {
		texts = append(texts, cell.Display)
	}
	return headingIndex(texts)
}

// merge is the whole array an update sends: an entry for every column
// of the table, as read, with the named ones retyped, and each with its
// name.
//
// The name is the one this read gave the column, or its header cell's
// text where the read gave none. Google refuses an entry with no name,
// "Table header row cell must have a value" (spike T Q3), and writes a
// name sent into the header cell (Q4); a name sent as read leaves the
// header showing what it did. Google reads back an entry for every
// column (Q1), so one the read left out is not expected; it is named by
// its header all the same, with no type. Google replaces the whole list:
// one column sent alone left the others with no type, and a dropdown
// with no list (Q3b). So every column goes back. A formula, a chip or
// rich text would lose what is under its text, so readTable refused one.
func (t *tableNow) merge(changed []*gsheets.TableColumn) []*gsheets.TableColumn {
	header := t.header.Cells[0]
	byIndex := map[int]*gsheets.TableColumn{}
	for _, c := range t.columns {
		if c != nil {
			kept := *c
			byIndex[c.ColumnIndex] = &kept
		}
	}
	for _, c := range changed {
		retyped := *c
		if was, ok := byIndex[c.ColumnIndex]; ok {
			retyped.ColumnName = was.ColumnName
		}
		byIndex[c.ColumnIndex] = &retyped
	}
	out := make([]*gsheets.TableColumn, 0, len(header))
	for i, cell := range header {
		c, ok := byIndex[i]
		if !ok {
			c = &gsheets.TableColumn{ColumnIndex: i}
		}
		if c.ColumnName == "" {
			c.ColumnName = cell.Display
		}
		out = append(out, c)
	}
	return out
}

func bandingOp(req RangeRequest, action string, card *gsheets.Spreadsheet,
	props *gsheets.SheetProperties, rect a1.Rect,
) (*gsheets.Request, []render.Applied, error) {
	sheet := sheetOf(card, props.SheetID)
	colors := func() (*gsheets.BandingProperties, error) {
		color, err := plan.ParseColor(req.Color)
		if err != nil {
			return nil, Errorf("invalid", "%s", err)
		}
		if color == nil {
			return nil, Errorf("invalid", "banding needs color, a hex color such as #d9e2f3")
		}
		return plan.Banding(color, req.Header), nil
	}
	if action == RangeAdd {
		shades, err := colors()
		if err != nil {
			return nil, nil, err
		}
		return plan.BandingAdd(props.SheetID, rect, shades),
			[]render.Applied{{Kind: "banding added to", Value: a1.FormatRect(rect) + " in " + req.Color}}, nil
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
	shades, err := colors()
	if err != nil {
		return nil, nil, err
	}
	// The axis is the one the banding already has. Writing rowProperties
	// onto a column banding leaves it carrying both sets, which the API
	// rejects.
	return plan.BandingUpdate(existing.BandedRangeID, columnBanding(existing), shades),
		[]render.Applied{{Kind: "banding recolored on", Value: a1.FormatRect(rect)}}, nil
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
	rule, err := buildRule(req, props.SheetID, rect)
	if err != nil {
		return nil, nil, err
	}
	// One rule, described by the same function read_formatting uses. The
	// two used to compose the sentence separately, so what manage_range
	// said it had written and what read_formatting said was there could
	// drift — and the service half is the one no golden covers.
	text := render.RuleText(rule)
	if action == RangeAdd {
		return plan.RuleAdd(req.Index, rule),
			[]render.Applied{{Kind: "conditional rule added", Value: text}}, nil
	}
	return plan.RuleUpdate(req.Index, props.SheetID, rule),
		[]render.Applied{{Kind: fmt.Sprintf("conditional rule %d replaced", req.Index), Value: text}}, nil
}

// buildRule is the rule an add or an update sends whole: a color scale,
// or a condition and the format it applies.
func buildRule(req RangeRequest, sheetID int, rect a1.Rect) (*gsheets.ConditionalFormatRule, error) {
	if len(req.Gradient) == 0 {
		if strings.TrimSpace(req.Condition) == "" {
			return nil, Errorf("invalid",
				"a conditional format rule needs condition and a format to apply, or gradient for a color scale")
		}
		cond, err := plan.ParseCondition(req.Condition, req.Values)
		if err != nil {
			return nil, Errorf("invalid", "%s", err)
		}
		format, err := ruleFormat(req)
		if err != nil {
			return nil, err
		}
		return plan.Rule(sheetID, rect, cond, format), nil
	}
	// A color scale has no condition, and its colors are its own. Taking
	// a format beside it would leave one of the two unsent.
	var extra []string
	for _, arg := range []struct {
		name string
		set  bool
	}{
		{"condition", strings.TrimSpace(req.Condition) != ""},
		{"values", len(req.Values) > 0},
		{"color", req.Color != ""},
		{"text_color", req.TextColor != ""},
		{"bold", req.Bold != nil},
	} {
		if arg.set {
			extra = append(extra, arg.name)
		}
	}
	if len(extra) > 0 {
		return nil, Errorf("invalid",
			"gradient is a color scale, a rule of its own with its colors in its points, so it does not take %s. "+
				"Pass gradient alone, or a condition and a format", join(extra))
	}
	scale, err := plan.ParseGradient(req.Gradient)
	if err != nil {
		return nil, Errorf("invalid", "%s", err)
	}
	return plan.Gradient(sheetID, rect, scale), nil
}

// ruleFormat is what a rule applies when its condition holds.
func ruleFormat(req RangeRequest) (*gsheets.CellFormat, error) {
	format := &gsheets.CellFormat{}
	if req.Color != "" {
		color, err := plan.ParseColor(req.Color)
		if err != nil {
			return nil, Errorf("invalid", "%s", err)
		}
		format.BackgroundColorStyle = color
	}
	if req.TextColor != "" || (req.Bold != nil && *req.Bold) {
		text := &gsheets.TextFormat{Bold: req.Bold != nil && *req.Bold}
		if req.TextColor != "" {
			color, err := plan.ParseColor(req.TextColor)
			if err != nil {
				return nil, Errorf("invalid", "%s", err)
			}
			text.ForegroundColorStyle = color
		}
		format.TextFormat = text
	}
	if format.BackgroundColorStyle == nil && format.TextFormat == nil {
		return nil, Errorf("invalid",
			"a conditional format rule needs a format to apply: color, text_color, bold, or several")
	}
	return format, nil
}

// rulesOver describes the conditional format rules that reach a
// rectangle, for the one request that destroys them.
func (s *Service) rulesOver(ctx context.Context, ref Reference, sheetID int, rect a1.Rect) ([]string, error) {
	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.RuleFields})
	if err != nil {
		return nil, wrap(err)
	}
	var out []string
	for _, rule := range sheetOf(got, sheetID).ConditionalFormats {
		if rule == nil {
			continue
		}
		for _, r := range rule.Ranges {
			if a1.FromGridRange(r).Overlaps(rect) {
				out = append(out, render.RuleText(rule))
				break
			}
		}
	}
	return out, nil
}

// ruleCount reads how many conditional format rules a sheet has.
//
// From the card, which carries the rules' ranges and is cached. It had a
// mask of its own before that, for a reason worth keeping written down:
// FormatFields was used here first and was wrong, because
// `includeGridData` is ignored when a field mask is set — so a mask
// naming `data(` fetches cells whether or not the option asks for them,
// and with no range, every cell of every sheet.
//
// rulesOver next door still needs RuleFields: it renders each rule, and
// the card carries only where they reach.
func (s *Service) ruleCount(ctx context.Context, ref Reference, sheetID int) (int, error) {
	// §17a.15: a request of its own made a rule update three where two
	// will do. Measured before the mask was widened — 294 bytes for one
	// rule's ranges against the 651 a whole rule costs.
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return 0, err
	}
	return len(sheetOf(sp, sheetID).ConditionalFormats), nil
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
