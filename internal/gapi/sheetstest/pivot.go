package sheetstest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
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
// stored faithfully and left undrawn, and the comment on computePivot
// says so rather than the fake inventing a nesting Google might not
// share.
//
// Filters, grouping rules and calculated values are drawn too, by the
// reference's definitions: a filter keeps the rows it shows before
// anything is summed, a date rule buckets by the part of the date its
// enum names and labels it as the enum's description shows, a histogram
// buckets as the reference's example does, a grouping made by hand puts
// each listed item under its group's name and leaves the rest on their
// own, and a calculated value is worked out per row and summed (SUM) or
// once per group (CUSTOM). What
// the fake cannot work out — a date or formula filter, a formula beyond
// arithmetic and five functions — it refuses by name rather than drawing
// a number Google might not.
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

	var raw json.RawMessage
	if len(req.Rows) > 0 && len(req.Rows[0].Values) > 0 {
		raw = req.Rows[0].Values[0].PivotTable
	}
	var stored json.RawMessage
	var layout [][]any
	if len(raw) > 0 {
		pivot, err := validatePivot(raw)
		if err != nil {
			return nil, true, err
		}
		if stored, err = bothFilterForms(raw, pivot); err != nil {
			return nil, true, err
		}
		if layout, err = computePivot(d, pivot); err != nil {
			return nil, true, err
		}
	}

	// Whatever was there is cleared first, output and all. A delete and
	// a replacement are the same act to the API, and doing it in one
	// place is what makes the second one leave nothing behind.
	clearPivotOutput(sh, row, col)
	if len(stored) == 0 {
		return &gsheets.Reply{}, true, nil
	}
	anchor := sh.At(row, col)
	if anchor == nil {
		anchor = &gsheets.CellData{}
	}
	cell := *anchor
	cell.PivotTable = stored
	sh.Set(row, col, &cell)
	paint(sh, row, col, layout)
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
//
// The refusals after those three are the reference's rules. Spike U
// put most of them to Google on 2026-10-09, and they are in Google's
// words: a value with both an offset and a formula, which the request's
// parser refuses before anything else; a formula summarized by anything
// but SUM or CUSTOM; CUSTOM on a column; a histogram's interval and its
// bounds; a grouping by hand named by a number; and a filter condition
// only data validation takes. A value with neither, a formula without =,
// and an item in two groups or two groups of one name are in this
// fake's own words: nothing asked Google, or a 429 cut the answer off.
// Two groups with a rule on one column are taken, as Google took them,
// though the reference allows one (§18).
func validatePivot(raw json.RawMessage) (*gsheets.PivotTable, error) {
	var pivot gsheets.PivotTable
	if err := json.Unmarshal(raw, &pivot); err != nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, errors.New("Invalid requests[0].updateCells: unreadable pivot table")
	}
	if err := valueOneof(pivot.Values); err != nil {
		return nil, err
	}
	if pivot.Source == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, errors.New("Invalid requests[0].updateCells: At least one source data must be specified")
	}
	groups := append(append([]*gsheets.PivotGroup{}, pivot.Rows...), pivot.Columns...)
	for _, g := range groups {
		if g.SortOrder == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return nil, errors.New("Invalid requests[0].updateCells: No sort order specified.")
		}
	}
	for _, v := range pivot.Values {
		if v.SummarizeFunction == "" {
			//nolint:staticcheck // Google's own wording, kept verbatim
			return nil, errors.New("Invalid requests[0].updateCells: No PivotValue.summarizeFunction specified.")
		}
	}
	if err := checkValues(pivot.Values); err != nil {
		return nil, err
	}
	for _, g := range groups {
		if r := g.GroupRule; r != nil {
			if err := checkGroupRule(r); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range pivot.Filters() {
		if c := f.FilterCriteria; c != nil && c.Condition != nil && !filterConditionTypes[c.Condition.Type] {
			return nil, errors.New("Invalid requests[0].updateCells: ConditionType '" + c.Condition.Type +
				"' is not supported in filters.")
		}
	}
	return &pivot, nil
}

// valueOneof is the request parser's refusal of a value that sets both
// an offset and a formula, which comes before any other. The field named
// is the one the request carries second, and this server's wire type
// writes sourceColumnOffset first.
func valueOneof(values []*gsheets.PivotValue) error {
	for i, v := range values {
		if v.SourceColumnOffset != nil && v.Formula != "" {
			return errors.New("Invalid value at 'requests[0].update_cells.rows[0].values[0].pivot_table.values[" +
				strconv.Itoa(i) + "]' (oneof), oneof field 'value' is already set. Cannot set 'formula'")
		}
	}
	return nil
}

// checkValues holds each value to a column or a formula, and a formula to
// the summaries it takes.
func checkValues(values []*gsheets.PivotValue) error {
	for _, v := range values {
		switch {
		case v.SourceColumnOffset == nil && v.Formula == "":
			return errors.New("invalid PivotValue: set exactly one of sourceColumnOffset and formula")
		case v.Formula != "" && !strings.HasPrefix(v.Formula, "="):
			return errors.New("invalid PivotValue: a formula starts with =")
		case v.Formula != "" && v.SummarizeFunction != "SUM" && v.SummarizeFunction != "CUSTOM":
			return errors.New("Invalid requests[0].updateCells: Invalid summarizeFunction: " + v.SummarizeFunction +
				`. Only "CUSTOM" or "SUM" are valid if PivotValue.calculatedField is set.`)
		case v.Formula == "" && v.SummarizeFunction == "CUSTOM":
			//nolint:staticcheck // Google's own wording, kept verbatim
			return errors.New(`Invalid requests[0].updateCells: "CUSTOM" may not be used in ` +
				`PivotValue.summarizeFunction unless PivotValue.calculatedField is set.`)
		}
	}
	return nil
}

// checkGroupRule holds one rule to what the reference says of it.
func checkGroupRule(r *gsheets.PivotGroupRule) error {
	set := 0
	for _, on := range []bool{r.HistogramRule != nil, r.DateTimeRule != nil, len(r.ManualRule) > 0} {
		if on {
			set++
		}
	}
	switch h, dt := r.HistogramRule, r.DateTimeRule; {
	case set != 1:
		return errors.New("invalid PivotGroupRule: set exactly one rule")
	case len(r.ManualRule) > 0:
		_, err := manualGroups(r.ManualRule)
		return err
	case h != nil && h.Interval <= 0:
		//nolint:staticcheck // Google's own wording, kept verbatim
		return errors.New("Invalid requests[0].updateCells: Histogram group rules require a positive value for interval.")
	case h != nil && h.Start != nil && h.End != nil && *h.Start >= *h.End:
		//nolint:staticcheck // Google's own wording, kept verbatim
		return errors.New("Invalid requests[0].updateCells: Start must be less than end.")
	case dt != nil && dateTimeLabel(dt.Type, 0) == nil:
		return errors.New("Invalid value at 'date_time_rule.type': " + strconv.Quote(dt.Type))
	}
	return nil
}

// filterConditionTypes are the conditions the discovery document says
// filters support, outside a data source.
var filterConditionTypes = map[string]bool{
	"NUMBER_GREATER": true, "NUMBER_GREATER_THAN_EQ": true, "NUMBER_LESS": true, "NUMBER_LESS_THAN_EQ": true,
	"NUMBER_EQ": true, "NUMBER_NOT_EQ": true, "NUMBER_BETWEEN": true, "NUMBER_NOT_BETWEEN": true,
	"TEXT_CONTAINS": true, "TEXT_NOT_CONTAINS": true, "TEXT_STARTS_WITH": true, "TEXT_ENDS_WITH": true,
	"TEXT_EQ": true, "DATE_EQ": true, "DATE_BEFORE": true, "DATE_AFTER": true, "BLANK": true, "NOT_BLANK": true,
	"CUSTOM_FORMULA": true,
}

// manualRule is a grouping made by hand, which the wire types keep raw:
// this fake is the one reader of what is inside it.
type manualRule struct {
	Groups []struct {
		GroupName *gsheets.ExtendedValue   `json:"groupName"`
		Items     []*gsheets.ExtendedValue `json:"items"`
	} `json:"groups"`
}

// manualGroups reads a grouping made by hand into each item's group
// name, holding it to the reference: a group name is a string and
// unique, and "Items may appear in at most one group within a given
// ManualRule".
func manualGroups(raw json.RawMessage) (map[string]string, error) {
	var rule manualRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return nil, errors.New("invalid ManualRule: " + err.Error())
	}
	names := map[string]bool{}
	out := map[string]string{}
	for _, g := range rule.Groups {
		switch {
		case g.GroupName != nil && g.GroupName.NumberValue != nil:
			//nolint:staticcheck // Google's own wording, kept verbatim
			return nil, errors.New("Invalid requests[0].updateCells: Found a manual group name of type number. " +
				"Manual group names must be strings.")
		case g.GroupName == nil || g.GroupName.StringValue == nil:
			return nil, errors.New("invalid ManualRuleGroup: the group name must be a string")
		}
		name := *g.GroupName.StringValue
		if names[name] {
			return nil, errors.New("invalid ManualRule: each group must have a unique group name")
		}
		names[name] = true
		for _, item := range g.Items {
			text := extendedText(item)
			if _, twice := out[text]; twice {
				return nil, errors.New("invalid ManualRule: an item may appear in at most one group")
			}
			out[text] = name
		}
	}
	return out, nil
}

// extendedText is a string, number or boolean item as a cell shows it.
func extendedText(v *gsheets.ExtendedValue) string {
	switch {
	case v == nil:
		return ""
	case v.StringValue != nil:
		return *v.StringValue
	case v.NumberValue != nil:
		return strconv.FormatFloat(*v.NumberValue, 'f', -1, 64)
	case v.BoolValue != nil:
		return strings.ToUpper(strconv.FormatBool(*v.BoolValue))
	}
	return ""
}

// bothFilterForms is the pivot as a read returns it: its filters in
// filterSpecs and in criteria both, which the reference says a response
// populates. A request with criteria alone is filtered by them, so a
// clear that left criteria behind reads back with its filters.
func bothFilterForms(raw json.RawMessage, p *gsheets.PivotTable) (json.RawMessage, error) {
	if len(p.FilterSpecs) == 0 && len(p.Criteria) == 0 {
		return append(json.RawMessage(nil), raw...), nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "filterSpecs")
	delete(m, "criteria")
	if specs := p.Filters(); len(specs) > 0 {
		criteria := map[string]*gsheets.PivotFilterCriteria{}
		for _, f := range specs {
			criteria[strconv.Itoa(f.ColumnOffsetIndex)] = f.FilterCriteria
		}
		m["filterSpecs"], m["criteria"] = specs, criteria
	}
	return json.Marshal(m)
}

// drawnCell says the cell holds a value nobody typed, which is what a
// pivot draws.
//
// One spelling, because three places in this fake now turn on it: the
// delete that takes a pivot's output, the clear that must leave such a
// cell alone, and the merge Google refuses over one. Two of them are
// modeling opposite behaviors from the same fact, so a drift between
// them would make the fake self-inconsistent rather than merely wrong.
func drawnCell(cell *gsheets.CellData) bool {
	return cell != nil && cell.EffectiveValue != nil && cell.UserEnteredValue == nil
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
		if drawnCell(cell) {
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

// computePivot works out the output, as rows of cells: a string, a
// number, or nil for a cell left empty.
//
// One row grouping. A second grouping is kept in the definition and not
// drawn, because how Google nests them is not something this repository
// has watched, and a fake that guessed would teach the tests a layout
// the API might not produce.
func computePivot(d *Doc, pivot *gsheets.PivotTable) ([][]any, error) {
	source := d.FindByID(pivot.Source.SheetID)
	if source == nil || len(pivot.Values) == 0 {
		return nil, nil
	}
	rect := a1.FromGridRange(pivot.Source)
	headings := sourceRow(source, rect, rect.FirstRow)
	kept, err := filteredRows(source, rect, pivot.Filters())
	if err != nil {
		return nil, err
	}

	// The heading row: the group's own heading, then one per value.
	heading := ""
	if len(pivot.Rows) > 0 {
		heading = headingAt(headings, pivot.Rows[0].SourceColumnOffset)
	}
	top := []any{heading}
	for _, v := range pivot.Values {
		name := v.Name
		switch {
		case name != "":
		case v.Formula != "":
			// Google draws a calculated value with no name under an empty
			// heading (spike U1).
		default:
			name = summaryLabel(v.SummarizeFunction) + " of " + headingAt(headings, *v.SourceColumnOffset)
		}
		top = append(top, name)
	}
	out := [][]any{top}
	if len(pivot.Rows) == 0 {
		return out, nil
	}
	line := func(label string, rows []int) ([]any, error) {
		cells := []any{label}
		for _, v := range pivot.Values {
			n, err := valueOf(v, source, rect, headings, rows)
			if err != nil {
				return nil, err
			}
			cells = append(cells, n)
		}
		return cells, nil
	}
	groups, order := groupRows(source, rect, kept, pivot.Rows[0].GroupRule, pivot.Rows[0].SourceColumnOffset)
	var every []int
	for _, key := range order {
		cells, err := line(key, groups[key])
		if err != nil {
			return nil, err
		}
		out = append(out, cells)
		every = append(every, groups[key]...)
	}
	if !pivot.Rows[0].ShowTotals {
		return out, nil
	}
	sort.Ints(every)
	cells, err := line("Grand Total", every)
	if err != nil {
		return nil, err
	}
	return append(out, cells), nil
}

// paint writes a computed output from its anchor.
func paint(sh *Sheet, row, col int, layout [][]any) {
	for i, cells := range layout {
		for j, v := range cells {
			switch v := v.(type) {
			case string:
				setComputed(sh, row+i, col+j, v)
			case float64:
				if math.IsInf(v, 0) || math.IsNaN(v) {
					setComputed(sh, row+i, col+j, "#DIV/0!")
					continue
				}
				setComputedNumber(sh, row+i, col+j, v)
			}
		}
	}
}

// valueOf is one value over a set of rows.
func valueOf(v *gsheets.PivotValue, source *Sheet, rect a1.Rect, headings []string, rows []int) (float64, error) {
	if v.Formula == "" {
		return summarize(v.SummarizeFunction, columnOf(source, rect, rows, *v.SourceColumnOffset)), nil
	}
	return calculated(v, source, rect, headings, rows)
}

// groupRows buckets rows by the value in one column, and returns the
// keys in the order a pivot sorts them into: ascending, and by what a
// bucket holds rather than by its label where a rule made it.
func groupRows(source *Sheet, rect a1.Rect, rows []int, rule *gsheets.PivotGroupRule, offset int) (map[string][]int, []string) {
	groups := map[string][]int{}
	rank := map[string]float64{}
	var order []string
	for _, r := range rows {
		key, at, ok := bucketOf(source, r, rect.FirstCol+offset, rule)
		if !ok {
			continue
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
			rank[key] = at
		}
		groups[key] = append(groups[key], r)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if rank[order[i]] != rank[order[j]] {
			return rank[order[i]] < rank[order[j]]
		}
		return order[i] < order[j]
	})
	return groups, order
}

// bucketOf is the group one cell falls in, and where that group sorts.
// A cell a rule cannot read — a word under a date rule, say — falls in
// none, which is a belief: Google may put it in a bucket of its own. A
// grouping made by hand sorts its group names among the values it leaves
// on their own, by label, which is a belief too.
func bucketOf(source *Sheet, row, col int, rule *gsheets.PivotGroupRule) (string, float64, bool) {
	if rule == nil || len(rule.ManualRule) > 0 {
		key := textAt(source, row, col)
		if rule != nil {
			// Read when the pivot was written, so it reads here.
			groups, _ := manualGroups(rule.ManualRule)
			if name, ok := groups[key]; ok {
				key = name
			}
		}
		return key, 0, key != ""
	}
	n, ok := numberAt(source, row, col)
	if !ok {
		return "", 0, false
	}
	if h := rule.HistogramRule; h != nil {
		key, at := histogramBucket(h, n)
		return key, at, true
	}
	if label := dateTimeLabel(rule.DateTimeRule.Type, n); label != nil {
		return label.text, label.rank, true
	}
	return "", 0, false
}

// histogramBucket labels a value's bucket as Google drew it in spike U,
// 2026-10-09: every 20 from 25 to 70 drew "< 25", "25 - 44", "45 - 64"
// and "65 - 70", the last holding 70, the end itself; every 10 with no
// start drew "20 - 29" and so on, counted from zero. A value past the
// end is "> end", which nothing has drawn. The last number of a bucket
// is one under the next one's first only for a whole interval, which is
// all the spike sent.
func histogramBucket(h *gsheets.HistogramRule, n float64) (string, float64) {
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	switch {
	case h.Start != nil && n < *h.Start:
		return "< " + num(*h.Start), math.Inf(-1)
	case h.End != nil && n > *h.End:
		return "> " + num(*h.End), math.Inf(1)
	}
	base := 0.0
	if h.Start != nil {
		base = *h.Start
	}
	lo := base + math.Floor((n-base)/h.Interval)*h.Interval
	hi := lo + h.Interval
	if h.Interval == math.Trunc(h.Interval) {
		hi--
	}
	if h.End != nil && lo+h.Interval >= *h.End {
		if lo >= *h.End && lo > base {
			lo -= h.Interval
		}
		hi = *h.End
	}
	return num(lo) + " - " + num(hi), lo
}

// dateLabel is a date bucket's label and where it sorts.
type dateLabel struct {
	text string
	rank float64
}

// dateTimeLabel buckets a date serial by one part of it, labeled as the
// enum's description shows: "2008-Nov" for YEAR_MONTH, "Q1", "Sunday",
// "7:45 PM". nil is a type the enum does not have.
func dateTimeLabel(kind string, serial float64) *dateLabel {
	t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Add(time.Duration(math.Round(serial*86400)) * time.Second)
	y, m, day := t.Date()
	q := (int(m)-1)/3 + 1
	l := func(text string, rank int) *dateLabel { return &dateLabel{text: text, rank: float64(rank)} }
	switch kind {
	case "SECOND":
		return l(strconv.Itoa(t.Second()), t.Second())
	case "MINUTE":
		return l(strconv.Itoa(t.Minute()), t.Minute())
	case "HOUR":
		return l(strconv.Itoa(t.Hour()), t.Hour())
	case "HOUR_MINUTE":
		return l(fmt.Sprintf("%d:%02d", t.Hour(), t.Minute()), t.Hour()*60+t.Minute())
	case "HOUR_MINUTE_AMPM":
		return l(t.Format("3:04 PM"), t.Hour()*60+t.Minute())
	case "DAY_OF_WEEK":
		return l(t.Weekday().String(), int(t.Weekday()))
	case "DAY_OF_YEAR":
		return l(strconv.Itoa(t.YearDay()), t.YearDay())
	case "DAY_OF_MONTH":
		return l(strconv.Itoa(day), day)
	case "DAY_MONTH":
		return l(fmt.Sprintf("%d-%s", day, t.Format("Jan")), int(m)*100+day)
	case "MONTH":
		return l(t.Format("Jan"), int(m))
	case "QUARTER":
		return l(fmt.Sprintf("Q%d", q), q)
	case "YEAR":
		return l(strconv.Itoa(y), y)
	case "YEAR_MONTH":
		return l(fmt.Sprintf("%d-%s", y, t.Format("Jan")), y*100+int(m))
	case "YEAR_QUARTER":
		return l(fmt.Sprintf("%d Q%d", y, q), y*10+q)
	case "YEAR_MONTH_DAY":
		return l(t.Format("2006-01-02"), y*10000+int(m)*100+day)
	}
	return nil
}

// filteredRows is the source's data rows a pivot's filters keep. The
// first row is headings, which is what headerCount defaults to for the
// shapes this server builds.
//
// The reference's rule: with visibleByDefault false a value is kept
// when it is listed and meets the condition; with it true, the list is
// ignored. A value is listed by the text it shows, which is a belief.
func filteredRows(source *Sheet, rect a1.Rect, filters []*gsheets.PivotFilterSpec) ([]int, error) {
	last := rect.LastRow
	if last == 0 {
		last = rect.FirstRow
	}
	var out []int
	for r := rect.FirstRow + 1; r <= last; r++ {
		keep := true
		for _, f := range filters {
			c := f.FilterCriteria
			if c == nil {
				continue
			}
			col := rect.FirstCol + f.ColumnOffsetIndex
			if !c.VisibleByDefault && !slices.Contains(c.VisibleValues, textAt(source, r, col)) {
				keep = false
			}
			if c.Condition != nil {
				ok, err := meets(c.Condition, source, r, col)
				if err != nil {
					return nil, err
				}
				keep = keep && ok
			}
		}
		if keep {
			out = append(out, r)
		}
	}
	return out, nil
}

// numberTests are the number conditions a filter takes, with how many
// operands each compares against.
var numberTests = map[string]struct {
	operands int
	test     func(n float64, o []float64) bool
}{
	"NUMBER_GREATER":         {1, func(n float64, o []float64) bool { return n > o[0] }},
	"NUMBER_GREATER_THAN_EQ": {1, func(n float64, o []float64) bool { return n >= o[0] }},
	"NUMBER_LESS":            {1, func(n float64, o []float64) bool { return n < o[0] }},
	"NUMBER_LESS_THAN_EQ":    {1, func(n float64, o []float64) bool { return n <= o[0] }},
	"NUMBER_EQ":              {1, func(n float64, o []float64) bool { return n == o[0] }},
	"NUMBER_NOT_EQ":          {1, func(n float64, o []float64) bool { return n != o[0] }},
	"NUMBER_BETWEEN":         {2, func(n float64, o []float64) bool { return n >= o[0] && n <= o[1] }},
	"NUMBER_NOT_BETWEEN":     {2, func(n float64, o []float64) bool { return n < o[0] || n > o[1] }},
}

// textTests are the text conditions a filter takes, each against one
// word.
var textTests = map[string]func(text, word string) bool{
	"TEXT_CONTAINS":     strings.Contains,
	"TEXT_NOT_CONTAINS": func(text, word string) bool { return !strings.Contains(text, word) },
	"TEXT_STARTS_WITH":  strings.HasPrefix,
	"TEXT_ENDS_WITH":    strings.HasSuffix,
	"TEXT_EQ":           func(text, word string) bool { return text == word },
}

// meets evaluates a filter condition on one cell. Text is compared
// without regard to case, which is a belief. A date or formula condition
// is refused by name: this fake does not evaluate one.
func meets(cond *gsheets.BooleanCondition, source *Sheet, row, col int) (bool, error) {
	var operands []float64
	var words []string
	for _, v := range cond.Values {
		if v == nil {
			continue
		}
		if strings.HasPrefix(v.UserEnteredValue, "=") {
			return false, errors.New("this fake does not evaluate a filter whose value is a formula")
		}
		words = append(words, strings.ToLower(v.UserEnteredValue))
		if n, err := strconv.ParseFloat(v.UserEnteredValue, 64); err == nil {
			operands = append(operands, n)
		}
	}
	text := strings.ToLower(textAt(source, row, col))
	if number, ok := numberTests[cond.Type]; ok {
		if len(operands) != number.operands {
			return false, errors.New("this fake compares a number condition with numbers only")
		}
		n, isNumber := numberAt(source, row, col)
		return isNumber && number.test(n, operands), nil
	}
	if test, ok := textTests[cond.Type]; ok {
		return len(words) == 1 && test(text, words[0]), nil
	}
	switch cond.Type {
	case "BLANK":
		return text == "", nil
	case "NOT_BLANK":
		return text != "", nil
	}
	return false, errors.New("this fake does not evaluate a " + cond.Type + " filter")
}

// numberAt is a cell's number, and whether it holds one.
func numberAt(sh *Sheet, row, col int) (float64, bool) {
	cell := sh.At(row, col)
	if cell == nil || cell.EffectiveValue == nil || cell.EffectiveValue.NumberValue == nil {
		return 0, false
	}
	return *cell.EffectiveValue.NumberValue, true
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

func summarize(fn string, values []float64) float64 {
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

// calculated works out a calculated value over a group's rows.
//
// Under SUM the formula is worked out per row, each heading meaning that
// row's value, and the results summed. Under CUSTOM it is worked out once,
// and a heading means the group's values, so it has to sit inside SUM,
// COUNT, AVERAGE, MIN or MAX. Those, arithmetic, parentheses and headings
// (quoted when they hold a space) are all this fake evaluates; anything
// else is refused by name. A cell that holds no number counts as 0, which
// is a belief.
func calculated(v *gsheets.PivotValue, source *Sheet, rect a1.Rect, headings []string, rows []int) (float64, error) {
	tokens, err := formulaTokens(strings.TrimPrefix(v.Formula, "="))
	if err != nil {
		return 0, err
	}
	column := func(name string) (int, error) {
		for i, h := range headings {
			if strings.EqualFold(h, name) {
				return rect.FirstCol + i, nil
			}
		}
		return 0, errors.New("this fake finds no heading " + strconv.Quote(name) + " for the formula " + v.Formula)
	}
	if v.SummarizeFunction == "SUM" {
		total := 0.0
		for _, r := range rows {
			e := &evaluator{tokens: tokens,
				name: func(name string) (float64, error) {
					col, err := column(name)
					n, _ := numberAt(source, r, col)
					return n, err
				},
				call: func(fn, _ string) (float64, error) {
					return 0, errors.New("this fake does not evaluate " + fn + " inside a formula summed per row")
				},
			}
			n, err := e.run()
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	}
	e := &evaluator{tokens: tokens,
		name: func(name string) (float64, error) {
			return 0, errors.New("this fake evaluates a heading in a CUSTOM formula only inside SUM, COUNT, " +
				"AVERAGE, MIN or MAX: " + name)
		},
		call: func(fn, name string) (float64, error) {
			api, ok := map[string]string{"SUM": "SUM", "COUNT": "COUNT", "AVERAGE": "AVERAGE", "MIN": "MIN", "MAX": "MAX"}[fn]
			if !ok {
				return 0, errors.New("this fake does not evaluate " + fn + " in a formula")
			}
			col, err := column(name)
			if err != nil {
				return 0, err
			}
			return summarize(api, columnOf(source, rect, rows, col-rect.FirstCol)), nil
		},
	}
	return e.run()
}

// formulaTokens splits a formula into numbers, names, quoted names and
// the six operators the evaluator reads.
func formulaTokens(text string) ([]string, error) {
	var out []string
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == ' ':
			i++
		case strings.IndexByte("+-*/(),", c) >= 0:
			out = append(out, string(c))
			i++
		case c == '\'':
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("this fake reads no closing quote in the formula")
			}
			out = append(out, text[i:i+end+2])
			i += end + 2
		case c == '.' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(text) && (text[j] == '.' || text[j] == '_' || text[j] >= '0' && text[j] <= '9' ||
				text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z') {
				j++
			}
			out = append(out, text[i:j])
			i = j
		default:
			return nil, errors.New("this fake does not evaluate " + strconv.Quote(string(c)) + " in a formula")
		}
	}
	return out, nil
}

// evaluator is arithmetic over headings and one-argument functions,
// read by recursive descent. What a heading and a call mean is the
// caller's.
type evaluator struct {
	tokens []string
	at     int
	name   func(name string) (float64, error)
	call   func(fn, name string) (float64, error)
}

func (e *evaluator) run() (float64, error) {
	n, err := e.sum()
	if err == nil && e.at != len(e.tokens) {
		err = errors.New("this fake does not evaluate the formula past " + strconv.Quote(e.tokens[e.at]))
	}
	return n, err
}

func (e *evaluator) peek() string {
	if e.at < len(e.tokens) {
		return e.tokens[e.at]
	}
	return ""
}

func (e *evaluator) next() string {
	t := e.peek()
	e.at++
	return t
}

func (e *evaluator) sum() (float64, error) {
	n, err := e.product()
	for err == nil && (e.peek() == "+" || e.peek() == "-") {
		op := e.next()
		var m float64
		if m, err = e.product(); op == "+" {
			n += m
		} else {
			n -= m
		}
	}
	return n, err
}

func (e *evaluator) product() (float64, error) {
	n, err := e.unit()
	for err == nil && (e.peek() == "*" || e.peek() == "/") {
		op := e.next()
		var m float64
		if m, err = e.unit(); op == "*" {
			n *= m
		} else {
			n /= m
		}
	}
	return n, err
}

func (e *evaluator) unit() (float64, error) {
	t := e.next()
	switch {
	case t == "":
		return 0, errors.New("this fake reads a formula that ends too soon")
	case t == "-":
		n, err := e.unit()
		return -n, err
	case t == "(":
		n, err := e.sum()
		if err == nil && e.next() != ")" {
			err = errors.New("this fake reads no closing parenthesis in the formula")
		}
		return n, err
	case strings.HasPrefix(t, "'"):
		return e.name(strings.Trim(t, "'"))
	}
	if n, err := strconv.ParseFloat(t, 64); err == nil {
		return n, nil
	}
	if e.peek() != "(" {
		return e.name(t)
	}
	e.next()
	arg := strings.Trim(e.next(), "'")
	if e.next() != ")" {
		return 0, errors.New("this fake evaluates a function of one heading only, as SUM(Revenue)")
	}
	return e.call(strings.ToUpper(t), arg)
}
