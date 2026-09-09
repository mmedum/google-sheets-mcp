package sheetstest

import (
	"errors"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The fake's half of the formatting union.
//
// Mechanical, all of it: a mask applied to a rectangle, a merge recorded
// and its non-anchor cells emptied, a rule inserted at an index. None of
// this is Google's judgement, so implementing it here invents nothing —
// which is the line this package draws. Where the API does exercise
// judgement, the request is validated and the cells are left alone, and
// the comment on it says so.

// applyFormat is the formatting and attached-object half of the union.
func applyFormat(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.RepeatCell != nil:
		return reply(repeatCell(d, req.RepeatCell))
	case req.UpdateBorders != nil:
		return reply(updateBorders(d, req.UpdateBorders))
	case req.MergeCells != nil:
		return reply(mergeCells(d, req.MergeCells))
	case req.UnmergeCells != nil:
		return reply(unmergeCells(d, req.UnmergeCells))
	case req.SetDataValidation != nil:
		return reply(setValidation(d, req.SetDataValidation))
	case req.AddNamedRange != nil:
		return addNamedRange(d, req.AddNamedRange)
	case req.UpdateNamedRange != nil:
		return reply(updateNamedRange(d, req.UpdateNamedRange))
	case req.DeleteNamedRange != nil:
		return reply(deleteNamedRange(d, req.DeleteNamedRange.NamedRangeID))
	case req.AddProtectedRange != nil:
		return addProtected(d, req.AddProtectedRange)
	case req.UpdateProtectedRange != nil:
		return reply(updateProtected(d, req.UpdateProtectedRange))
	case req.DeleteProtectedRange != nil:
		return reply(deleteProtected(d, req.DeleteProtectedRange.ProtectedRangeID))
	case req.AddTable != nil:
		return addTable(d, req.AddTable)
	case req.UpdateTable != nil:
		return reply(updateTable(d, req.UpdateTable))
	case req.DeleteTable != nil:
		return reply(deleteTable(d, req.DeleteTable.TableID))
	case req.AddBanding != nil:
		return addBanding(d, req.AddBanding)
	case req.UpdateBanding != nil:
		return reply(updateBanding(d, req.UpdateBanding))
	case req.DeleteBanding != nil:
		return reply(deleteBanding(d, req.DeleteBanding.BandedRangeID))
	case req.AddConditionalFormatRule != nil:
		return reply(addRule(d, req.AddConditionalFormatRule))
	case req.UpdateConditionalFormatRule != nil:
		return reply(updateRule(d, req.UpdateConditionalFormatRule))
	case req.DeleteConditionalFormatRule != nil:
		return deleteRule(d, req.DeleteConditionalFormatRule)
	}
	return nil, false, nil
}

// reply wraps a request that answers with nothing but success.
func reply(err error) (*gsheets.Reply, bool, error) {
	if err != nil {
		return nil, true, err
	}
	return &gsheets.Reply{}, true, nil
}

// sheetForRange finds the sheet a grid range names, and the rectangle in
// A1's own one-based coordinates.
func sheetForRange(d *Doc, r *gsheets.GridRange) (*Sheet, a1.Rect, error) {
	if r == nil {
		return nil, a1.Rect{}, errors.New("the request needs a range")
	}
	sh := d.FindByID(r.SheetID)
	if sh == nil {
		return nil, a1.Rect{}, errors.New("No sheet with id: " + strconv.Itoa(r.SheetID))
	}
	return sh, clamp(sh, a1.FromGridRange(r)), nil
}

// repeatCell writes one cell's masked fields across a rectangle.
//
// The mask is honoured field by field, as the real API honours it: a
// request naming userEnteredFormat.textFormat.bold sets bold and leaves
// the background alone, and one naming the bare userEnteredFormat
// replaces the whole format, which is how clearing works.
func repeatCell(d *Doc, req *gsheets.RepeatCellRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	if req.Fields == "" {
		return errors.New("repeatCell needs a field mask")
	}
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil {
				cell = &gsheets.CellData{}
				sh.Set(row, col, cell)
			}
			if err := applyFields(cell, req.Cell, req.Fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyFields copies the fields a mask names from one cell onto another.
func applyFields(into, from *gsheets.CellData, mask string) error {
	if from == nil {
		from = &gsheets.CellData{}
	}
	for _, field := range strings.Split(mask, ",") {
		switch strings.TrimSpace(field) {
		case "note":
			into.Note = from.Note
		case "userEnteredFormat":
			// The bare field replaces the whole format, so a nil one
			// clears it. That is what clear_format sends.
			into.UserEnteredFormat = from.UserEnteredFormat
		case "userEnteredFormat.numberFormat":
			format(into).NumberFormat = formatOf(from).NumberFormat
		case "userEnteredFormat.backgroundColorStyle":
			format(into).BackgroundColorStyle = formatOf(from).BackgroundColorStyle
		case "userEnteredFormat.horizontalAlignment":
			format(into).HorizontalAlign = formatOf(from).HorizontalAlign
		case "userEnteredFormat.verticalAlignment":
			format(into).VerticalAlign = formatOf(from).VerticalAlign
		case "userEnteredFormat.wrapStrategy":
			format(into).WrapStrategy = formatOf(from).WrapStrategy
		case "userEnteredFormat.textFormat.bold":
			text(into).Bold = textOf(from).Bold
		case "userEnteredFormat.textFormat.italic":
			text(into).Italic = textOf(from).Italic
		case "userEnteredFormat.textFormat.underline":
			text(into).Underline = textOf(from).Underline
		case "userEnteredFormat.textFormat.strikethrough":
			text(into).Strikethrough = textOf(from).Strikethrough
		case "userEnteredFormat.textFormat.fontSize":
			text(into).FontSize = textOf(from).FontSize
		case "userEnteredFormat.textFormat.fontFamily":
			text(into).FontFamily = textOf(from).FontFamily
		case "userEnteredFormat.textFormat.foregroundColorStyle":
			text(into).ForegroundColorStyle = textOf(from).ForegroundColorStyle
		default:
			return errors.New("this fake does not know the field " + field)
		}
	}
	// The effective format is what the caller sees applied. The fake has
	// no sheet defaults to merge in, so it is the entered format, and a
	// read of it agrees with a read of the other.
	into.EffectiveFormat = into.UserEnteredFormat
	return nil
}

func format(c *gsheets.CellData) *gsheets.CellFormat {
	if c.UserEnteredFormat == nil {
		c.UserEnteredFormat = &gsheets.CellFormat{}
	}
	return c.UserEnteredFormat
}

func formatOf(c *gsheets.CellData) *gsheets.CellFormat {
	if c.UserEnteredFormat == nil {
		return &gsheets.CellFormat{}
	}
	return c.UserEnteredFormat
}

func text(c *gsheets.CellData) *gsheets.TextFormat {
	f := format(c)
	if f.TextFormat == nil {
		f.TextFormat = &gsheets.TextFormat{}
	}
	return f.TextFormat
}

func textOf(c *gsheets.CellData) *gsheets.TextFormat {
	if f := formatOf(c); f.TextFormat != nil {
		return f.TextFormat
	}
	return &gsheets.TextFormat{}
}

// updateBorders draws the edges of a rectangle: the named outer edges on
// the boundary cells, the inner ones on everything between.
func updateBorders(d *Doc, req *gsheets.UpdateBordersRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil {
				cell = &gsheets.CellData{}
				sh.Set(row, col, cell)
			}
			f := format(cell)
			if f.Borders == nil {
				f.Borders = &gsheets.Borders{}
			}
			edge(&f.Borders.Top, req.Top, req.InnerHorizontal, row == rect.FirstRow)
			edge(&f.Borders.Bottom, req.Bottom, req.InnerHorizontal, row == rect.LastRow)
			edge(&f.Borders.Left, req.Left, req.InnerVertical, col == rect.FirstCol)
			edge(&f.Borders.Right, req.Right, req.InnerVertical, col == rect.LastCol)
			cell.EffectiveFormat = cell.UserEnteredFormat
		}
	}
	return nil
}

// edge picks which border applies to this side of this cell, and leaves
// it alone when neither was named.
func edge(into **gsheets.Border, outer, inner *gsheets.Border, onBoundary bool) {
	border := inner
	if onBoundary {
		border = outer
	}
	if border == nil {
		return
	}
	if border.Style == gsheets.BorderNone {
		*into = nil
		return
	}
	copied := *border
	*into = &copied
}

// mergeCells records a merge and empties every cell but the one each
// merged block keeps.
//
// The emptying is the API's own behaviour rather than this fake's
// invention, and it is the reason format_cells reads the rectangle
// before it merges: nothing in the response says a value went.
func mergeCells(d *Doc, req *gsheets.MergeCellsRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	blocks, err := mergeBlocks(rect, req.MergeType)
	if err != nil {
		return err
	}
	if err := refusePivotMerge(sh, rect); err != nil {
		return err
	}
	for _, block := range blocks {
		for row := block.FirstRow; row <= block.LastRow; row++ {
			for col := block.FirstCol; col <= block.LastCol; col++ {
				if row == block.FirstRow && col == block.FirstCol {
					continue
				}
				delete(sh.Cells, [2]int{row - 1, col - 1})
			}
		}
		sh.Merges = append(sh.Merges, block.GridRange(sh.Props.SheetID))
	}
	return nil
}

// refusePivotMerge is the API refusing a merge that touches a pivot
// table, in the API's own words (spike Q).
//
// Here because a fake that accepts what Google refuses lets a guard's
// test pass on a request the guard exists to stop — the shape three
// findings in this project have taken (§17a.29). The output counts as
// much as the anchor: live, a merge wholly inside the computed cells got
// the same 400.
func refusePivotMerge(sh *Sheet, rect a1.Rect) error {
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil {
				continue
			}
			// The anchor carries the definition; the cells it draws
			// carry an effective value with nothing entered.
			//
			// Live, Google goes by the pivot's whole footprint and
			// refuses a merge over blank cells inside it too. This fake
			// cannot tell which blanks those are without measuring every
			// pivot, and neither can the server — the guard names what
			// it can see and translates Google's own refusal for the
			// rest, so this models the half a test can assert on.
			if len(cell.PivotTable) > 0 || drawnCell(cell) {
				//nolint:staticcheck // Google's own wording, kept verbatim
				return errors.New("Invalid requests[0].mergeCells: You can't merge cells that are part of a pivot table.")
			}
		}
	}
	return nil
}

// mergeBlocks is the rectangles one merge request produces.
func mergeBlocks(rect a1.Rect, kind string) ([]a1.Rect, error) {
	switch kind {
	case gsheets.MergeAll, "":
		return []a1.Rect{rect}, nil
	case gsheets.MergeRows:
		var out []a1.Rect
		for row := rect.FirstRow; row <= rect.LastRow; row++ {
			out = append(out, a1.Rect{FirstRow: row, LastRow: row, FirstCol: rect.FirstCol, LastCol: rect.LastCol})
		}
		return out, nil
	case gsheets.MergeColumns:
		var out []a1.Rect
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			out = append(out, a1.Rect{FirstRow: rect.FirstRow, LastRow: rect.LastRow, FirstCol: col, LastCol: col})
		}
		return out, nil
	}
	return nil, errors.New("unknown merge type " + kind)
}

// unmergeCells removes every merge the rectangle touches.
func unmergeCells(d *Doc, req *gsheets.UnmergeCellsRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	kept := sh.Merges[:0]
	for _, m := range sh.Merges {
		if !a1.FromGridRange(m).Overlaps(rect) {
			kept = append(kept, m)
		}
	}
	sh.Merges = kept
	return nil
}

// setValidation puts a rule on every cell of a rectangle, or clears it.
func setValidation(d *Doc, req *gsheets.SetDataValidationRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil {
				if req.Rule == nil {
					continue
				}
				cell = &gsheets.CellData{}
				sh.Set(row, col, cell)
			}
			cell.DataValidation = req.Rule
		}
	}
	return nil
}

func addNamedRange(d *Doc, req *gsheets.AddNamedRangeRequest) (*gsheets.Reply, bool, error) {
	nr := req.NamedRange
	if nr == nil || nr.Name == "" {
		return nil, true, errors.New("addNamedRange needs a name")
	}
	for _, other := range d.NamedRanges {
		if other.Name == nr.Name {
			return nil, true, errors.New("A named range with the name \"" + nr.Name + "\" already exists.")
		}
	}
	copied := *nr
	copied.NamedRangeID = "nr" + strconv.Itoa(len(d.NamedRanges)+1)
	d.NamedRanges = append(d.NamedRanges, &copied)
	return &gsheets.Reply{AddNamedRange: &gsheets.AddNamedRangeReply{NamedRange: &copied}}, true, nil
}

func updateNamedRange(d *Doc, req *gsheets.UpdateNamedRangeRequest) error {
	nr := req.NamedRange
	if nr == nil || req.Fields == "" {
		return errors.New("updateNamedRange needs a named range and a field mask")
	}
	for _, other := range d.NamedRanges {
		if other.NamedRangeID != nr.NamedRangeID {
			continue
		}
		for _, field := range strings.Split(req.Fields, ",") {
			switch strings.TrimSpace(field) {
			case "name":
				other.Name = nr.Name
			case "range":
				other.Range = nr.Range
			default:
				return errors.New("this fake does not know the field " + field)
			}
		}
		return nil
	}
	return errors.New("No named range with id: " + nr.NamedRangeID)
}

func deleteNamedRange(d *Doc, id string) error {
	for i, nr := range d.NamedRanges {
		if nr.NamedRangeID == id {
			d.NamedRanges = append(d.NamedRanges[:i], d.NamedRanges[i+1:]...)
			return nil
		}
	}
	return errors.New("No named range with id: " + id)
}

func addProtected(d *Doc, req *gsheets.AddProtectedRangeRequest) (*gsheets.Reply, bool, error) {
	p := req.ProtectedRange
	sh, _, err := sheetForRange(d, protectedRange(p))
	if err != nil {
		return nil, true, err
	}
	copied := *p
	copied.ProtectedRangeID = nextProtectedID(d)
	// The owner is always an editor, which is why a protected-range 403
	// cannot be observed with one account (§15.E).
	copied.RequestingUserCanEdit = true
	sh.Protected = append(sh.Protected, &copied)
	return &gsheets.Reply{AddProtectedRange: &gsheets.AddProtectedRangeReply{ProtectedRange: &copied}}, true, nil
}

func protectedRange(p *gsheets.ProtectedRange) *gsheets.GridRange {
	if p == nil {
		return nil
	}
	return p.Range
}

func nextProtectedID(d *Doc) int {
	next := 1
	for _, sh := range d.Sheets {
		for _, p := range sh.Protected {
			if p.ProtectedRangeID >= next {
				next = p.ProtectedRangeID + 1
			}
		}
	}
	return next
}

func updateProtected(d *Doc, req *gsheets.UpdateProtectedRangeRequest) error {
	p := req.ProtectedRange
	if p == nil || req.Fields == "" {
		return errors.New("updateProtectedRange needs a protected range and a field mask")
	}
	for _, sh := range d.Sheets {
		for _, other := range sh.Protected {
			if other.ProtectedRangeID != p.ProtectedRangeID {
				continue
			}
			for _, field := range strings.Split(req.Fields, ",") {
				switch strings.TrimSpace(field) {
				case "description":
					other.Description = p.Description
				case "warningOnly":
					other.WarningOnly = p.WarningOnly
				case "editors":
					other.Editors = p.Editors
				default:
					return errors.New("this fake does not know the field " + field)
				}
			}
			return nil
		}
	}
	return errors.New("No protected range with id: " + strconv.Itoa(p.ProtectedRangeID))
}

func deleteProtected(d *Doc, id int) error {
	for _, sh := range d.Sheets {
		for i, p := range sh.Protected {
			if p.ProtectedRangeID == id {
				sh.Protected = append(sh.Protected[:i], sh.Protected[i+1:]...)
				return nil
			}
		}
	}
	return errors.New("No protected range with id: " + strconv.Itoa(id))
}

func addTable(d *Doc, req *gsheets.AddTableRequest) (*gsheets.Reply, bool, error) {
	t := req.Table
	if t == nil || t.Name == "" {
		return nil, true, errors.New("addTable needs a name")
	}
	sh, _, err := sheetForRange(d, t.Range)
	if err != nil {
		return nil, true, err
	}
	copied := *t
	copied.TableID = "tbl" + strconv.Itoa(len(sh.Tables)+1)
	sh.Tables = append(sh.Tables, &copied)
	return &gsheets.Reply{AddTable: &gsheets.AddTableReply{Table: &copied}}, true, nil
}

func updateTable(d *Doc, req *gsheets.UpdateTableRequest) error {
	t := req.Table
	if t == nil || req.Fields == "" {
		return errors.New("updateTable needs a table and a field mask")
	}
	for _, sh := range d.Sheets {
		for _, other := range sh.Tables {
			if other.TableID != t.TableID {
				continue
			}
			for _, field := range strings.Split(req.Fields, ",") {
				switch strings.TrimSpace(field) {
				case "name":
					other.Name = t.Name
				case "range":
					other.Range = t.Range
				default:
					return errors.New("this fake does not know the field " + field)
				}
			}
			return nil
		}
	}
	return errors.New("No table with id: " + t.TableID)
}

func deleteTable(d *Doc, id string) error {
	for _, sh := range d.Sheets {
		for i, t := range sh.Tables {
			if t.TableID == id {
				sh.Tables = append(sh.Tables[:i], sh.Tables[i+1:]...)
				return nil
			}
		}
	}
	return errors.New("No table with id: " + id)
}

func addBanding(d *Doc, req *gsheets.AddBandingRequest) (*gsheets.Reply, bool, error) {
	b := req.BandedRange
	sh, _, err := sheetForRange(d, bandingRange(b))
	if err != nil {
		return nil, true, err
	}
	copied := *b
	copied.BandedRangeID = len(sh.Bandings) + 1
	sh.Bandings = append(sh.Bandings, &copied)
	return &gsheets.Reply{AddBanding: &gsheets.AddBandingReply{BandedRange: &copied}}, true, nil
}

func bandingRange(b *gsheets.BandedRange) *gsheets.GridRange {
	if b == nil {
		return nil
	}
	return b.Range
}

func updateBanding(d *Doc, req *gsheets.UpdateBandingRequest) error {
	b := req.BandedRange
	if b == nil || req.Fields == "" {
		return errors.New("updateBanding needs a banded range and a field mask")
	}
	for _, sh := range d.Sheets {
		for _, other := range sh.Bandings {
			if other.BandedRangeID != b.BandedRangeID {
				continue
			}
			for _, field := range strings.Split(req.Fields, ",") {
				switch strings.TrimSpace(field) {
				case "rowProperties":
					other.RowProperties = b.RowProperties
				case "columnProperties":
					other.ColumnProperties = b.ColumnProperties
				default:
					return errors.New("this fake does not know the field " + field)
				}
			}
			return nil
		}
	}
	return errors.New("No banded range with id: " + strconv.Itoa(b.BandedRangeID))
}

func deleteBanding(d *Doc, id int) error {
	for _, sh := range d.Sheets {
		for i, b := range sh.Bandings {
			if b.BandedRangeID == id {
				sh.Bandings = append(sh.Bandings[:i], sh.Bandings[i+1:]...)
				return nil
			}
		}
	}
	return errors.New("No banded range with id: " + strconv.Itoa(id))
}

func addRule(d *Doc, req *gsheets.AddConditionalFormatRuleRequest) error {
	if req.Rule == nil || len(req.Rule.Ranges) == 0 {
		return errors.New("addConditionalFormatRule needs a rule with a range")
	}
	sh, _, err := sheetForRange(d, req.Rule.Ranges[0])
	if err != nil {
		return err
	}
	if req.Index < 0 || req.Index > len(sh.Conditional) {
		return errors.New("index " + strconv.Itoa(req.Index) + " is out of range")
	}
	sh.Conditional = append(sh.Conditional, nil)
	copy(sh.Conditional[req.Index+1:], sh.Conditional[req.Index:])
	sh.Conditional[req.Index] = req.Rule
	return nil
}

func updateRule(d *Doc, req *gsheets.UpdateConditionalFormatRuleRequest) error {
	sh := d.FindByID(req.SheetID)
	if sh == nil {
		return errors.New("No sheet with id: " + strconv.Itoa(req.SheetID))
	}
	if req.Index < 0 || req.Index >= len(sh.Conditional) {
		return errors.New("index " + strconv.Itoa(req.Index) + " is out of range")
	}
	sh.Conditional[req.Index] = req.Rule
	return nil
}

func deleteRule(d *Doc, req *gsheets.DeleteConditionalFormatRuleRequest) (*gsheets.Reply, bool, error) {
	sh := d.FindByID(req.SheetID)
	if sh == nil {
		return nil, true, errors.New("No sheet with id: " + strconv.Itoa(req.SheetID))
	}
	if req.Index < 0 || req.Index >= len(sh.Conditional) {
		return nil, true, errors.New("index " + strconv.Itoa(req.Index) + " is out of range")
	}
	gone := sh.Conditional[req.Index]
	sh.Conditional = append(sh.Conditional[:req.Index], sh.Conditional[req.Index+1:]...)
	return &gsheets.Reply{DeleteConditionalFormatRule: &gsheets.DeleteConditionalFormatRuleReply{Rule: gone}}, true, nil
}
