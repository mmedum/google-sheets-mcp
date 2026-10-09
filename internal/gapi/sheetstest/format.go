package sheetstest

import (
	"errors"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// The fake's half of the formatting union.
//
// Mechanical, all of it: a mask applied to a rectangle, a merge recorded
// and its non-anchor cells emptied, a rule inserted at an index. None of
// this is Google's judgment, so implementing it here invents nothing —
// which is the line this package draws. Where the API does exercise
// judgment, the request is validated and the cells are left alone, and
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
// The mask is honored field by field, as the real API honors it: a
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
// The emptying is the API's own behavior rather than this fake's
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
	rect := clamp(sh, a1.FromGridRange(t.Range))
	if err := checkColumns("addTable", t.ColumnProperties, rect.Cols()); err != nil {
		return nil, true, err
	}
	copied := *t
	copied.TableID = "tbl" + strconv.Itoa(len(sh.Tables)+1)
	sent := map[int]*gsheets.TableColumn{}
	for _, c := range t.ColumnProperties {
		sent[c.ColumnIndex] = c
	}
	// Spike T Q1, 2026-10-09: an add typing columns 1 and 3 of four, with
	// no names, read back an entry for every column. The two left out
	// were named by their header cells. The two typed ones were named
	// "Column 1" and "Column 2", counted along the typed columns sent
	// with no name, and Google wrote those names into the header cells
	// over "Amount" and "Status". A name sent is written into its header
	// cell, as an update writes one (Q4).
	copied.ColumnProperties = nil
	unnamed := 0
	for i := range rect.Cols() {
		column := gsheets.TableColumn{ColumnIndex: i}
		if c, ok := sent[i]; ok {
			column = *c
		}
		header := sh.At(rect.FirstRow, rect.FirstCol+i)
		switch {
		case column.ColumnName != "":
			writeHeader(sh, rect.FirstRow, rect.FirstCol+i, column.ColumnName)
		case column.ColumnType != "":
			unnamed++
			column.ColumnName = "Column " + strconv.Itoa(unnamed)
			writeHeader(sh, rect.FirstRow, rect.FirstCol+i, column.ColumnName)
		case header != nil:
			column.ColumnName = header.FormattedValue
		}
		copied.ColumnProperties = append(copied.ColumnProperties, &column)
	}
	checkboxes(sh, rect, copied.ColumnProperties)
	sh.Tables = append(sh.Tables, &copied)
	return &gsheets.Reply{AddTable: &gsheets.AddTableReply{Table: &copied}}, true, nil
}

// checkboxes is what typing a column boolean does to the cells under its
// header: text and empty cells become FALSE.
//
// Spike T Q5, 2026-10-09: an update typing a column BOOLEAN turned
// "maybe", the text "TRUE" and an empty cell into FALSE. What it does to
// a number, a formula or a cell already TRUE is not known, so those are
// left as they are. An add is believed to do the same; nothing has
// answered it, since Q11's boolean add was a 500.
func checkboxes(sh *Sheet, rect a1.Rect, columns []*gsheets.TableColumn) {
	for _, c := range columns {
		if c.ColumnType != gsheets.ColumnBoolean {
			continue
		}
		col := rect.FirstCol + c.ColumnIndex
		for row := rect.FirstRow + 1; row <= rect.LastRow; row++ {
			cell, unchecked := sh.At(row, col), Bool(false)
			switch {
			case cell == nil:
				sh.Set(row, col, unchecked)
			case cell.UserEnteredValue == nil || cell.UserEnteredValue.StringValue != nil:
				cell.UserEnteredValue, cell.EffectiveValue = unchecked.UserEnteredValue, unchecked.EffectiveValue
				cell.FormattedValue = unchecked.FormattedValue
			}
		}
	}
}

// writeHeader writes a column's name into its header cell as plain
// text, which is what Google does with a name it is sent: the text
// stays, and a smart chip and rich text's runs go (spike T Q4, Q7 and
// Q9). The cell's whole-cell format and its note are believed to stay;
// nothing has asked.
func writeHeader(sh *Sheet, row, col int, name string) {
	cell := sh.At(row, col)
	if cell == nil {
		sh.Set(row, col, Str(name))
		return
	}
	text := Str(name)
	cell.UserEnteredValue, cell.EffectiveValue, cell.FormattedValue = text.UserEnteredValue, text.EffectiveValue, name
	cell.ChipRuns, cell.TextFormatRuns = nil, nil
}

// headerWritten is what Google does with a value written into a table's
// header cell: the column takes the cell's text as its name, and a
// formula is replaced by a name of Google's own.
//
// Spike T, 2026-10-09: a formula written into B1 of a table over A1:D4
// was answered 200 and read back as "Column 2", B's place in the table,
// and the column took that name (Q7); a person chip written into D1
// named its column after the chip's text (Q9).
func headerWritten(sh *Sheet, written a1.Rect) {
	for _, t := range sh.Tables {
		rect := clamp(sh, a1.FromGridRange(t.Range))
		row := rect.FirstRow
		if row < written.FirstRow || row > written.LastRow {
			continue
		}
		for col := max(rect.FirstCol, written.FirstCol); col <= min(rect.LastCol, written.LastCol); col++ {
			cell := sh.At(row, col)
			if cell == nil {
				continue
			}
			index := col - rect.FirstCol
			if cell.UserEnteredValue != nil && cell.UserEnteredValue.FormulaValue != nil {
				writeHeader(sh, row, col, "Column "+strconv.Itoa(index+1))
			}
			if cell.FormattedValue != "" {
				nameColumn(t, index, cell.FormattedValue)
			}
		}
	}
}

// nameColumn gives a table's column a name, adding its entry if the
// table has none for it.
func nameColumn(t *gsheets.Table, index int, name string) {
	for _, c := range t.ColumnProperties {
		if c.ColumnIndex == index {
			c.ColumnName = name
			return
		}
	}
	t.ColumnProperties = append(t.ColumnProperties, &gsheets.TableColumn{ColumnIndex: index, ColumnName: name})
}

// columnTypes is the API's column type enum, chips included.
var columnTypes = map[string]bool{
	gsheets.ColumnText: true, gsheets.ColumnDouble: true, gsheets.ColumnCurrency: true,
	gsheets.ColumnPercent: true, gsheets.ColumnDate: true, gsheets.ColumnTime: true,
	gsheets.ColumnDateTime: true, gsheets.ColumnBoolean: true, gsheets.ColumnDropdown: true,
	gsheets.ColumnFiles: true, gsheets.ColumnPeople: true, gsheets.ColumnFinance: true,
	gsheets.ColumnPlace: true, gsheets.ColumnRatings: true,
}

// checkColumns makes the refusals a table's columns meet. op is the
// request's name, which Google's messages carry.
//
// Spike T Q2, 2026-10-09: a dropdown with no rule and a rule on a
// number column are refused in the words below. An entry with a name
// and no type is taken (Q8). A rule that is not a list, a column index
// past the table's width and one given twice are refused in wording of
// this fake's own: nothing has asked Google.
func checkColumns(op string, columns []*gsheets.TableColumn, width int) error {
	seen := map[int]bool{}
	for _, c := range columns {
		if c == nil {
			return errors.New("invalid TableColumnProperties: an empty entry")
		}
		if c.ColumnType != "" && !columnTypes[c.ColumnType] {
			return errors.New("Invalid value at 'column_type': " + strconv.Quote(c.ColumnType))
		}
		if c.ColumnIndex < 0 || c.ColumnIndex >= width {
			return errors.New("invalid TableColumnProperties: column index " + strconv.Itoa(c.ColumnIndex) +
				" is outside the table")
		}
		if seen[c.ColumnIndex] {
			return errors.New("invalid TableColumnProperties: column index " + strconv.Itoa(c.ColumnIndex) +
				" is given twice")
		}
		seen[c.ColumnIndex] = true
		rule := c.DataValidationRule
		switch {
		case c.ColumnType == gsheets.ColumnDropdown && (rule == nil || rule.Condition == nil):
			return errors.New("Invalid requests[0]." + op + ": Condition must be set for dropdown column type.")
		case c.ColumnType != gsheets.ColumnDropdown && rule != nil:
			return errors.New("Invalid requests[0]." + op + ": Cannot set condition for non-dropdown column type.")
		case rule != nil && (rule.Condition.Type != "ONE_OF_LIST" || len(rule.Condition.Values) == 0):
			return errors.New("invalid TableColumnDataValidationRule: the condition must be ONE_OF_LIST with values")
		}
	}
	return nil
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
				case "columnProperties":
					columns, err := replaceColumns(sh, other, t.ColumnProperties)
					if err != nil {
						return err
					}
					other.ColumnProperties = columns
				default:
					return errors.New("this fake does not know the field " + field)
				}
			}
			return nil
		}
	}
	return errors.New("No table with id: " + t.TableID)
}

// replaceColumns is columnProperties under a mask: the whole list is
// replaced, and a column the request leaves out loses its type and its
// dropdown's list, and keeps its header's text as its name.
//
// Spike T, 2026-10-09: an entry with no name is refused, in the words
// below (Q3, Q5), and a name sent is written into its header cell (Q4).
// Q3b sent column 1 alone, named and typed DATE, over a table whose
// column 1 was CURRENCY and column 3 a dropdown with its list; four
// entries read back, the three left out named by their headers with no
// type, and the dropdown's list gone.
func replaceColumns(sh *Sheet, table *gsheets.Table, columns []*gsheets.TableColumn) ([]*gsheets.TableColumn, error) {
	rect := clamp(sh, a1.FromGridRange(table.Range))
	if err := checkColumns("updateTable", columns, rect.Cols()); err != nil {
		return nil, err
	}
	sent := map[int]*gsheets.TableColumn{}
	for _, c := range columns {
		if c.ColumnName == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return nil, errors.New("Invalid requests[0].updateTable: Table header row cell must have a value.")
		}
		sent[c.ColumnIndex] = c
	}
	out := make([]*gsheets.TableColumn, 0, rect.Cols())
	for i := range rect.Cols() {
		column := gsheets.TableColumn{ColumnIndex: i}
		if c, ok := sent[i]; ok {
			column = *c
			writeHeader(sh, rect.FirstRow, rect.FirstCol+i, c.ColumnName)
		} else if header := sh.At(rect.FirstRow, rect.FirstCol+i); header != nil {
			column.ColumnName = header.FormattedValue
		}
		out = append(out, &column)
	}
	checkboxes(sh, rect, out)
	return out, nil
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

// checkRule makes the refusals spike S saw Google make of a color scale,
// in Google's words. op is the request's name, which they carry.
//
// Spike S, 2026-10-09: a number, percent or percentile point with no
// value is refused (Q3), and so are a scale with no maxpoint and a point
// with no type, both as a point with no type. A rule with both kinds is
// refused by the request's parser. Under de_DE, a number written with a
// decimal point is refused and one with a decimal comma taken (Q4). A
// rule of neither kind, an unknown type, and the other comma locales
// are refused or not in wording of this fake's own: nothing asked Google.
func checkRule(op, locale string, rule *gsheets.ConditionalFormatRule) error {
	invalid := func(why string) error { return errors.New("Invalid requests[0]." + op + ": " + why) }
	switch {
	case rule.BooleanRule == nil && rule.GradientRule == nil:
		return errors.New("invalid ConditionalFormatRule: exactly one of booleanRule and gradientRule is required")
	case rule.BooleanRule != nil && rule.GradientRule != nil:
		return errors.New("Invalid value at 'requests[0]." + snakeOps[op] + ".rule' (oneof), oneof field 'rule' " +
			"is already set. Cannot set 'gradientRule'")
	case rule.GradientRule == nil:
		return nil
	}
	scale := rule.GradientRule
	for i, p := range []*gsheets.InterpolationPoint{scale.Minpoint, scale.Midpoint, scale.Maxpoint} {
		switch {
		case i == 1 && p == nil:
			// The midpoint is optional.
		case p == nil || p.Type == "":
			return invalid("No interpolationPointType specified.")
		case p.Type == gsheets.PointMin || p.Type == gsheets.PointMax:
		case p.Type == gsheets.PointNumber || p.Type == gsheets.PointPercent || p.Type == gsheets.PointPercentile:
			if p.Value == "" {
				return invalid("InterpolationPoint.value is required.")
			}
			if _, err := strconv.ParseFloat(p.Value, 64); err == nil && commaLocales[locale] && strings.Contains(p.Value, ".") {
				return invalid("Invalid InterpolationPoint.value: " + p.Value)
			}
		default:
			return errors.New("invalid InterpolationPoint: unknown type " + strconv.Quote(p.Type))
		}
	}
	return nil
}

// snakeOps are the requests' names as the request parser spells them.
var snakeOps = map[string]string{
	"addConditionalFormatRule":    "add_conditional_format_rule",
	"updateConditionalFormatRule": "update_conditional_format_rule",
}

// commaLocales write a decimal with a comma. Only de_DE was asked
// (spike S Q4); the others are not modeled.
var commaLocales = map[string]bool{"de_DE": true}

// stored is a rule as Google keeps it: a min or max point loses the
// value it was sent with, which the reference calls unused (spike S Q3).
func stored(rule *gsheets.ConditionalFormatRule) *gsheets.ConditionalFormatRule {
	if rule.GradientRule == nil {
		return rule
	}
	copied, scale := *rule, *rule.GradientRule
	for _, p := range []**gsheets.InterpolationPoint{&scale.Minpoint, &scale.Midpoint, &scale.Maxpoint} {
		if *p != nil && ((*p).Type == gsheets.PointMin || (*p).Type == gsheets.PointMax) {
			kept := **p
			kept.Value = ""
			*p = &kept
		}
	}
	copied.GradientRule = &scale
	return &copied
}

func addRule(d *Doc, req *gsheets.AddConditionalFormatRuleRequest) error {
	if req.Rule == nil || len(req.Rule.Ranges) == 0 {
		return errors.New("addConditionalFormatRule needs a rule with a range")
	}
	if err := checkRule("addConditionalFormatRule", d.Locale, req.Rule); err != nil {
		return err
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
	sh.Conditional[req.Index] = stored(req.Rule)
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
	if req.Rule == nil {
		sh.Conditional[req.Index] = nil
		return nil
	}
	if err := checkRule("updateConditionalFormatRule", d.Locale, req.Rule); err != nil {
		return err
	}
	sh.Conditional[req.Index] = stored(req.Rule)
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
