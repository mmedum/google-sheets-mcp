package sheetstest

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The fake's pivot tables.
//
// It computes an output, which a fake need not usually do — but a pivot
// is the one structure whose whole result is a rectangle nobody stated,
// and a fake that stored the definition and drew nothing would let the
// server report "it covers nothing" on every call and call that tested.
//
// What it draws is the layout the live run printed, cell for cell: the
// group column's heading, then "SUM of <heading>" for each value, then a
// row per group in ascending order, then Grand Total. One row grouping,
// which is what manage_pivot_table's own examples build; a second one is
// stored faithfully and left undrawn, and the comment on drawPivot says
// so rather than the fake inventing a nesting Google might not share.
//
// Output cells carry an effectiveValue and no userEnteredValue, which is
// how the live API returns them and how everything above tells a pivot's
// own output apart from something a person typed.

// applyCells is the fake's updateCells, which is a pivot write and
// nothing else here: this server sends the request for no other purpose.
func applyCells(d *Doc, req *gsheets.UpdateCellsRequest) (*gsheets.Reply, bool, error) {
	if req.Fields != "pivotTable" {
		return nil, true, errors.New("this fake only knows updateCells for pivotTable")
	}
	if req.Start == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, true, errors.New("Invalid requests[0].updateCells: no start")
	}
	sh := d.FindByID(req.Start.SheetID)
	if sh == nil {
		return nil, true, errors.New("No sheet with id: " + strconv.Itoa(req.Start.SheetID))
	}
	row, col := a1.OneBased(req.Start.RowIndex), a1.OneBased(req.Start.ColumnIndex)

	// Whatever was there is cleared first, output and all. A delete and
	// a replacement are the same act to the API, and doing it in one
	// place is what makes the second one leave nothing behind.
	clearPivotOutput(sh, row, col)

	var pivot json.RawMessage
	if len(req.Rows) > 0 && len(req.Rows[0].Values) > 0 {
		pivot = req.Rows[0].Values[0].PivotTable
	}
	if len(pivot) == 0 {
		return &gsheets.Reply{}, true, nil
	}
	if err := validatePivot(pivot); err != nil {
		return nil, true, err
	}
	anchor := sh.At(row, col)
	if anchor == nil {
		anchor = &gsheets.CellData{}
	}
	cell := *anchor
	cell.PivotTable = append(json.RawMessage(nil), pivot...)
	sh.Set(row, col, &cell)
	drawPivot(d, sh, row, col, pivot)
	return &gsheets.Reply{}, true, nil
}

// validatePivot makes the refusals the live API makes, and none of the
// ones it does not.
//
// The order matters and was measured: a group with no sortOrder is
// refused before a value with no summarizeFunction, which is how the
// first run of spike M got the same message for two different questions.
// An offset past the source's width is *not* refused — the API takes it
// with a 200 — and neither is it refused here, because that acceptance
// is the whole reason manage_pivot_table checks it first.
func validatePivot(raw json.RawMessage) error {
	var pivot gsheets.PivotTable
	if err := json.Unmarshal(raw, &pivot); err != nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return errors.New("Invalid requests[0].updateCells: unreadable pivot table")
	}
	if pivot.Source == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return errors.New("Invalid requests[0].updateCells: At least one source data must be specified")
	}
	for _, g := range append(append([]*gsheets.PivotGroup{}, pivot.Rows...), pivot.Columns...) {
		if g.SortOrder == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return errors.New("Invalid requests[0].updateCells: No sort order specified.")
		}
	}
	for _, v := range pivot.Values {
		if v.SummarizeFunction == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return errors.New("Invalid requests[0].updateCells: No PivotValue.summarizeFunction specified.")
		}
	}
	return nil
}

// clearPivotOutput removes a pivot and everything it drew.
//
// Everything: the live delete takes the whole output with it, leaving
// not one computed cell behind, and a fake that left a header row would
// let a "did the delete clear it" test pass on a delete that did not.
func clearPivotOutput(sh *Sheet, row, col int) {
	anchor := sh.At(row, col)
	if anchor == nil || len(anchor.PivotTable) == 0 {
		return
	}
	for key, cell := range sh.Cells {
		if cell == nil || key[0] < row-1 || key[1] < col-1 {
			continue
		}
		// A computed cell is one with an effective value and nothing
		// entered, which is exactly what a pivot draws.
		if cell.EffectiveValue != nil && cell.UserEnteredValue == nil {
			delete(sh.Cells, key)
		}
	}
	// What is left of the anchor is what somebody entered there, never
	// what the pivot computed into it: the anchor cell carries both the
	// definition and the output's first heading, and keeping the second
	// would leave a delete looking like it had missed a cell.
	cleared := *anchor
	cleared.PivotTable = nil
	cleared.EffectiveValue = cleared.UserEnteredValue
	cleared.FormattedValue = ""
	if cleared.UserEnteredValue == nil && cleared.Note == "" && cleared.UserEnteredFormat == nil {
		delete(sh.Cells, [2]int{row - 1, col - 1})
		return
	}
	sh.Set(row, col, &cleared)
}

// drawPivot computes the output.
//
// One row grouping. A second grouping is kept in the definition and not
// drawn, because how Google nests them is not something this repository
// has watched, and a fake that guessed would teach the tests a layout
// the API might not produce.
func drawPivot(d *Doc, sh *Sheet, row, col int, raw json.RawMessage) {
	var pivot gsheets.PivotTable
	if err := json.Unmarshal(raw, &pivot); err != nil {
		return
	}
	source := d.FindByID(pivot.Source.SheetID)
	if source == nil || len(pivot.Values) == 0 {
		return
	}
	rect := a1.FromGridRange(pivot.Source)
	headings := sourceRow(source, rect, rect.FirstRow)

	// The heading row: the group's own heading, then one per value.
	heading := ""
	if len(pivot.Rows) > 0 {
		heading = headingAt(headings, pivot.Rows[0].SourceColumnOffset)
	}
	setComputed(sh, row, col, heading)
	for i, v := range pivot.Values {
		name := v.Name
		if name == "" {
			name = summaryLabel(v.SummarizeFunction) + " of " + headingAt(headings, v.SourceColumnOffset)
		}
		setComputed(sh, row, col+1+i, name)
	}
	if len(pivot.Rows) == 0 {
		return
	}

	groups, order := groupRows(source, rect, pivot.Rows[0].SourceColumnOffset)
	at := row
	for _, key := range order {
		at++
		setComputed(sh, at, col, key)
		for i, v := range pivot.Values {
			setComputedNumber(sh, at, col+1+i, summarise(v.SummarizeFunction, columnOf(source, rect, groups[key], v.SourceColumnOffset)))
		}
	}
	if !pivot.Rows[0].ShowTotals {
		return
	}
	at++
	setComputed(sh, at, col, "Grand Total")
	var every []int
	for _, rows := range groups {
		every = append(every, rows...)
	}
	sort.Ints(every)
	for i, v := range pivot.Values {
		setComputedNumber(sh, at, col+1+i, summarise(v.SummarizeFunction, columnOf(source, rect, every, v.SourceColumnOffset)))
	}
}

// groupRows buckets the source's data rows by the value in one column,
// and returns the keys in the ascending order a pivot sorts them into.
func groupRows(source *Sheet, rect a1.Rect, offset int) (map[string][]int, []string) {
	groups := map[string][]int{}
	var order []string
	last := rect.LastRow
	if last == 0 {
		last = rect.FirstRow
	}
	// The first row is headings, which is what headerCount defaults to
	// for the shapes this server builds.
	for r := rect.FirstRow + 1; r <= last; r++ {
		key := textAt(source, r, rect.FirstCol+offset)
		if key == "" {
			continue
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}
	sort.Strings(order)
	return groups, order
}

// columnOf reads one column's numbers out of a set of rows.
func columnOf(source *Sheet, rect a1.Rect, rows []int, offset int) []float64 {
	var out []float64
	for _, r := range rows {
		cell := source.At(r, rect.FirstCol+offset)
		if cell == nil || cell.EffectiveValue == nil || cell.EffectiveValue.NumberValue == nil {
			continue
		}
		out = append(out, *cell.EffectiveValue.NumberValue)
	}
	return out
}

func summarise(fn string, values []float64) float64 {
	switch fn {
	case "COUNT", "COUNTA", "COUNTUNIQUE":
		return float64(len(values))
	case "MAX":
		if len(values) == 0 {
			return 0
		}
		out := values[0]
		for _, v := range values {
			if v > out {
				out = v
			}
		}
		return out
	case "MIN":
		if len(values) == 0 {
			return 0
		}
		out := values[0]
		for _, v := range values {
			if v < out {
				out = v
			}
		}
		return out
	case "AVERAGE":
		if len(values) == 0 {
			return 0
		}
		return sum(values) / float64(len(values))
	default:
		return sum(values)
	}
}

func sum(values []float64) float64 {
	var out float64
	for _, v := range values {
		out += v
	}
	return out
}

// summaryLabel is the word Google puts in the heading, which is the
// function's own name.
func summaryLabel(fn string) string {
	if fn == "" {
		return "SUM"
	}
	return strings.ToUpper(fn)
}

func headingAt(headings []string, offset int) string {
	if offset < 0 || offset >= len(headings) {
		return ""
	}
	return headings[offset]
}

func sourceRow(sh *Sheet, rect a1.Rect, row int) []string {
	last := rect.LastCol
	if last == 0 {
		last = rect.FirstCol
	}
	var out []string
	for c := rect.FirstCol; c <= last; c++ {
		out = append(out, textAt(sh, row, c))
	}
	return out
}

func textAt(sh *Sheet, row, col int) string {
	cell := sh.At(row, col)
	if cell == nil {
		return ""
	}
	if cell.FormattedValue != "" {
		return cell.FormattedValue
	}
	if v := cell.EffectiveValue; v != nil {
		switch {
		case v.StringValue != nil:
			return *v.StringValue
		case v.NumberValue != nil:
			return strconv.FormatFloat(*v.NumberValue, 'f', -1, 64)
		}
	}
	return ""
}

// setComputed writes an output cell: an effective value and nothing
// entered, which is what a pivot's own cells look like on the wire.
//
// The pivot definition on the cell survives, because the anchor carries
// both — live, E1 holds the pivot and the output's first heading — and
// overwriting it here would leave a pivot that had just been written and
// could not be found again.
func setComputed(sh *Sheet, row, col int, text string) {
	if text == "" {
		return
	}
	value := text
	cell := keepPivot(sh, row, col)
	cell.EffectiveValue = &gsheets.ExtendedValue{StringValue: &value}
	cell.FormattedValue = text
	sh.Set(row, col, cell)
}

func setComputedNumber(sh *Sheet, row, col int, n float64) {
	value := n
	cell := keepPivot(sh, row, col)
	cell.EffectiveValue = &gsheets.ExtendedValue{NumberValue: &value}
	cell.FormattedValue = strconv.FormatFloat(n, 'f', -1, 64)
	sh.Set(row, col, cell)
}

// keepPivot is a fresh output cell carrying whatever pivot definition
// the cell already had.
func keepPivot(sh *Sheet, row, col int) *gsheets.CellData {
	cell := &gsheets.CellData{}
	if existing := sh.At(row, col); existing != nil {
		cell.PivotTable = existing.PivotTable
	}
	return cell
}
