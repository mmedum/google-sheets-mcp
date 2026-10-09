package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// PivotFieldMask is the CellData field a pivot write names, and the only
// one it names: an updateCells carrying `pivotTable` alone leaves every
// other property of the cell where it was.
const PivotFieldMask = "pivotTable"

// WritePivot anchors a pivot table at a one-based cell.
//
// `start` rather than `range`, because a pivot's output is computed and
// its rectangle is not known here — a range would clear the named field
// across a rectangle this code would have to guess at.
func WritePivot(sheetID, col, row int, pivot json.RawMessage) *gsheets.Request {
	return &gsheets.Request{UpdateCells: &gsheets.UpdateCellsRequest{
		Start: &gsheets.GridCoordinate{
			SheetID:     sheetID,
			RowIndex:    a1.ZeroBased(row),
			ColumnIndex: a1.ZeroBased(col),
		},
		Rows:   []*gsheets.RowData{{Values: []*gsheets.CellData{{PivotTable: pivot}}}},
		Fields: PivotFieldMask,
	}}
}

// ClearPivot removes the pivot anchored at a cell, and its whole output
// with it. There is no deletePivotTable request: naming the field with
// no pivot in the cell is the delete.
func ClearPivot(sheetID, col, row int) *gsheets.Request {
	return WritePivot(sheetID, col, row, nil)
}

// The grammar of manage_pivot_table's group_rows, group_columns, values
// and filters. Each entry names its column as the caller wrote it, a
// letter or a heading; which column of the source that is takes the
// source and maybe a read of its first row, so the service resolves it.
// Each parse has a spelling that writes it back, which is what list
// reports, so a pivot read can be sent again as it reads.

// summaryFunctions are the ways a pivot value is reduced, in the
// spelling a caller writes, to the API's.
var summaryFunctions = map[string]string{
	"sum": "SUM", "count": "COUNTA", "count_numbers": "COUNT", "count_unique": "COUNTUNIQUE",
	"average": "AVERAGE", "max": "MAX", "min": "MIN", "median": "MEDIAN",
	"product": "PRODUCT", "stdev": "STDEV", "stdevp": "STDEVP", "var": "VAR", "varp": "VARP",
}

// summaryNames lists the summaries a caller may write, for a message.
func summaryNames() []string { return sortedKeys(summaryFunctions) }

// dateTimeTypes are the date groupings, in the spelling a caller writes:
// the API's enum, lower-cased, without its unspecified member.
var dateTimeTypes = []string{
	"second", "minute", "hour", "hour_minute", "hour_minute_ampm", "day_of_week", "day_of_year",
	"day_of_month", "day_month", "month", "quarter", "year", "year_month", "year_quarter", "year_month_day",
}

// filterConditions are the conditions a pivot table filter takes: the
// ones the discovery document says filters support, of those
// manage_range already names. The others it marks "Supported by data
// validation" alone, and Google refuses one in a filter: "ConditionType
// 'ONE_OF_LIST' is not supported in filters" (spike U6).
var filterConditions = map[string]bool{
	"number_greater": true, "number_greater_eq": true, "number_less": true, "number_less_eq": true,
	"number_eq": true, "number_not_eq": true, "number_between": true, "number_not_between": true,
	"text_contains": true, "text_not_contains": true, "text_starts_with": true, "text_ends_with": true,
	"text_eq": true, "date_before": true, "date_after": true, "blank": true, "not_blank": true,
	"custom_formula": true,
}

// relativeDates are the dates a filter's date condition may count from
// today, in the spelling a read writes them: the API's enum, lower-cased,
// without its unspecified member. The discovery document allows one on
// DATE_BEFORE and DATE_AFTER, the two date conditions a filter takes.
var relativeDates = []string{"past_year", "past_month", "past_week", "yesterday", "today", "tomorrow"}

// PivotGroupEntry is one entry of group_rows or group_columns, parsed.
type PivotGroupEntry struct {
	Column string
	// Rule buckets the column's values, and is nil for a plain group.
	Rule *gsheets.PivotGroupRule
}

// groupKeyword is where a group's rule may start. The last one with
// something after it is the rule, so a heading with either word in it
// is still the column: "Sold by by month".
var groupKeyword = regexp.MustCompile(`(?i)\s(by|every)\b`)

// histogramTail is what follows "every": the interval, then an optional
// start and end.
var histogramTail = regexp.MustCompile(`(?i)^(\S+)(?:\s+from\s+(\S+))?(?:\s+to\s+(\S+))?$`)

// ParsePivotGroup reads one group: "Region", "Date by year_month", or
// "Age every 10 from 20 to 70". An entry with neither word is a column
// alone.
//
// A grouping made by hand in Sheets reads back as "<column> by hand",
// and is refused here: this tool does not write one, and an update that
// leaves the groups out keeps it.
func ParsePivotGroup(text string) (PivotGroupEntry, error) {
	text = strings.TrimSpace(text)
	var column, keyword, rest string
	found := groupKeyword.FindAllStringSubmatchIndex(text, -1)
	for i := len(found) - 1; i >= 0 && rest == ""; i-- {
		m := found[i]
		column, keyword, rest = strings.TrimSpace(text[:m[0]]), strings.ToLower(text[m[2]:m[3]]), strings.TrimSpace(text[m[1]:])
	}
	if rest == "" {
		return PivotGroupEntry{Column: text}, nil
	}
	if keyword == "by" {
		kind := strings.ToLower(rest)
		if kind == "hand" {
			return PivotGroupEntry{}, fmt.Errorf("%q is a grouping made by hand in Sheets, which this tool cannot set; "+
				"an update that leaves the groups out keeps it", text)
		}
		for _, t := range dateTimeTypes {
			if kind == t {
				return PivotGroupEntry{Column: column, Rule: &gsheets.PivotGroupRule{
					DateTimeRule: &gsheets.DateTimeRule{Type: strings.ToUpper(kind)},
				}}, nil
			}
		}
		return PivotGroupEntry{}, fmt.Errorf("%q groups by %q, which is not one of %s", text, rest,
			strings.Join(dateTimeTypes, ", "))
	}
	tail := histogramTail.FindStringSubmatch(rest)
	if tail == nil {
		return PivotGroupEntry{}, fmt.Errorf("%q needs a bucket size after every, then an optional start and end, "+
			"such as \"Age every 10 from 20 to 70\"", text)
	}
	interval, err := number(text, "bucket size", tail[1])
	if err != nil {
		return PivotGroupEntry{}, err
	}
	if interval <= 0 {
		return PivotGroupEntry{}, fmt.Errorf("%q has a bucket size of %s, and it has to be more than 0", text, tail[1])
	}
	rule := &gsheets.HistogramRule{Interval: interval}
	if tail[2] != "" {
		start, err := number(text, "start", tail[2])
		if err != nil {
			return PivotGroupEntry{}, err
		}
		rule.Start = &start
	}
	if tail[3] != "" {
		end, err := number(text, "end", tail[3])
		if err != nil {
			return PivotGroupEntry{}, err
		}
		rule.End = &end
	}
	if rule.Start != nil && rule.End != nil && *rule.Start >= *rule.End {
		return PivotGroupEntry{}, fmt.Errorf("%q starts at %s and ends at %s; the start has to be below the end",
			text, tail[2], tail[3])
	}
	return PivotGroupEntry{Column: column, Rule: &gsheets.PivotGroupRule{HistogramRule: rule}}, nil
}

// number reads one number of a histogram, naming which it was.
func number(entry, what, text string) (float64, error) {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("%q has %q as its %s, which is not a number", entry, text, what)
	}
	return n, nil
}

// PivotGroupText writes a group back in the spelling ParsePivotGroup
// takes. A rule made by hand in Sheets reads as "<column> by hand".
func PivotGroupText(column string, rule *gsheets.PivotGroupRule) string {
	switch {
	case rule == nil:
		return column
	case len(rule.ManualRule) > 0:
		return column + " by hand"
	case rule.DateTimeRule != nil:
		return column + " by " + strings.ToLower(rule.DateTimeRule.Type)
	case rule.HistogramRule != nil:
		h := rule.HistogramRule
		text := column + " every " + formatNumber(h.Interval)
		if h.Start != nil {
			text += " from " + formatNumber(*h.Start)
		}
		if h.End != nil {
			text += " to " + formatNumber(*h.End)
		}
		return text
	}
	return column
}

func formatNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// PivotValueEntry is one entry of values, parsed: a column summarized,
// or a calculated value's formula.
type PivotValueEntry struct {
	// Column is empty for a calculated value.
	Column  string
	Formula string
	// Function is the API's name for the summary.
	Function string
	Name     string
}

// ParsePivotValue reads one value: "B sum", "Units sum as Total units",
// or a calculated value, which starts with "=" and needs a name:
// "=SUM(Revenue)/SUM(Cost) as Margin" uses the formula as written
// (CUSTOM), and "=Revenue-Cost sum as Margin" works it out per row and
// sums it (SUM). Google takes no other summary for a formula.
func ParsePivotValue(text string) (PivotValueEntry, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return PivotValueEntry{}, fmt.Errorf("values has an empty entry")
	}
	if strings.HasPrefix(text, "=") {
		return parseCalculated(text)
	}
	body, name, _ := strings.Cut(text, " as ")
	body = strings.TrimSpace(body)
	last := strings.LastIndexFunc(body, unicode.IsSpace)
	if last < 0 {
		return PivotValueEntry{}, fmt.Errorf("%q does not say how to summarize the column; write it as \"B sum\", "+
			"and add \"as Total\" to name it", text)
	}
	column, fn := strings.TrimSpace(body[:last]), strings.ToLower(body[last+1:])
	if fn == "custom" {
		return PivotValueEntry{}, fmt.Errorf("%q asks for custom, which is for a calculated value, one that "+
			"starts with =; a column takes %s", text, strings.Join(summaryNames(), ", "))
	}
	function, ok := summaryFunctions[fn]
	if !ok {
		return PivotValueEntry{}, fmt.Errorf("%q is not a summary this server offers: %s", fn,
			strings.Join(summaryNames(), ", "))
	}
	return PivotValueEntry{Column: column, Function: function, Name: strings.TrimSpace(name)}, nil
}

// parseCalculated reads a calculated value. The name follows the last
// " as ", since a formula is likelier to hold those words than a name.
func parseCalculated(text string) (PivotValueEntry, error) {
	at := strings.LastIndex(text, " as ")
	if at < 0 || strings.TrimSpace(text[at+len(" as "):]) == "" {
		return PivotValueEntry{}, fmt.Errorf("%q is a calculated value, which needs a name: add \"as <name>\", "+
			"such as \"=Revenue-Cost as Margin\"", text)
	}
	value := PivotValueEntry{Name: strings.TrimSpace(text[at+len(" as "):]), Function: "CUSTOM"}
	formula := strings.TrimSpace(text[:at])
	if last := strings.LastIndexFunc(formula, unicode.IsSpace); last >= 0 {
		switch word := strings.ToLower(formula[last+1:]); {
		case word == "sum":
			value.Function, formula = "SUM", strings.TrimSpace(formula[:last])
		case word == "custom":
			formula = strings.TrimSpace(formula[:last])
		case summaryFunctions[word] != "":
			return PivotValueEntry{}, fmt.Errorf("%q summarizes a calculated value by %s, and Google takes only "+
				"sum or custom for one", text, word)
		}
	}
	if strings.TrimSpace(strings.TrimPrefix(formula, "=")) == "" {
		return PivotValueEntry{}, fmt.Errorf("%q has no formula after the =", text)
	}
	value.Formula = formula
	return value, nil
}

// PivotValueText writes a value back in the spelling ParsePivotValue
// takes. column is the value's column letter, and unused for a
// calculated value. A summary this server does not name is written as
// Google's word for it, lower-cased.
func PivotValueText(column string, v *gsheets.PivotValue) string {
	if v == nil {
		return ""
	}
	var text string
	switch {
	case v.Formula != "" && v.SummarizeFunction == "SUM":
		text = v.Formula + " sum"
	case v.Formula != "":
		text = v.Formula
	default:
		text = column + " " + summaryName(v.SummarizeFunction)
	}
	if v.Name != "" {
		text += " as " + v.Name
	}
	return text
}

// summaryName is the caller's word for an API summary.
func summaryName(function string) string {
	for name, api := range summaryFunctions {
		if api == function {
			return name
		}
	}
	return strings.ToLower(function)
}

// PivotFilterEntry is one entry of filters, parsed: the values of a
// column to show, or a condition its values must meet. A column may have
// one of each, and a value is then shown when it is listed and meets
// the condition.
type PivotFilterEntry struct {
	Column    string
	Show      []string
	Condition *gsheets.BooleanCondition
}

// filterEntry splits "<column> show <values>" and "<column> <condition>
// [<values>]". The column is the shortest run of text a keyword can
// follow, as in a table's column_types.
var filterEntry = regexp.MustCompile(`(?i)^(.+?)\s+(show|` + strings.Join(ConditionNames(), "|") + `)(?:\s+(.*))?$`)

// ParsePivotFilter reads one filter: "Region show East, West" or
// "Amount number_greater 100". A condition takes manage_range's names
// and the values after it, two separated by a comma for a between; a
// list of values to show is separated by commas, so a value with a comma
// in it cannot be listed here.
//
// A date condition's value may be a relative date, "Day date_after
// past_week", which is how a read writes one back. Typed into a cell the
// word would be text, never a date.
func ParsePivotFilter(text string) (PivotFilterEntry, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return PivotFilterEntry{}, fmt.Errorf("filters has an empty entry")
	}
	m := filterEntry.FindStringSubmatch(text)
	if m == nil {
		return PivotFilterEntry{}, fmt.Errorf("filter %q needs a column, then show and the values to show, or a "+
			"condition, such as \"Region show East, West\" or \"Amount number_greater 100\"", text)
	}
	column, keyword, rest := strings.TrimSpace(m[1]), strings.ToLower(m[2]), strings.TrimSpace(m[3])
	if keyword == "show" {
		var show []string
		for _, v := range strings.Split(rest, ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				return PivotFilterEntry{}, fmt.Errorf("filter %q has an empty value to show; the values are "+
					"separated by commas", text)
			}
			show = append(show, v)
		}
		return PivotFilterEntry{Column: column, Show: show}, nil
	}
	if !filterConditions[keyword] {
		return PivotFilterEntry{}, fmt.Errorf("filter %q uses %s, which only data validation takes; a pivot table "+
			"filter takes %s", text, keyword, strings.Join(sortedKeys(filterConditions), ", "))
	}
	var values []string
	switch {
	case rest == "":
	case conditions[keyword] == 2:
		for _, v := range strings.Split(rest, ",") {
			values = append(values, strings.TrimSpace(v))
		}
	default:
		values = []string{rest}
	}
	cond, err := ParseCondition(keyword, values)
	if err != nil {
		return PivotFilterEntry{}, fmt.Errorf("filter %q: %w", text, err)
	}
	if cond.Type == "DATE_BEFORE" || cond.Type == "DATE_AFTER" {
		word := strings.ToLower(strings.Join(strings.Fields(values[0]), "_"))
		if slices.Contains(relativeDates, word) {
			cond.Values[0] = &gsheets.ConditionValue{RelativeDate: strings.ToUpper(word)}
		}
	}
	return PivotFilterEntry{Column: column, Condition: cond}, nil
}

// PivotFilterTexts writes a column's filter back in the spelling
// ParsePivotFilter takes: one entry for the values shown, one for the
// condition. Values a condition-only filter ignores are left out, and so
// is a filter that shows every value.
func PivotFilterTexts(column string, c *gsheets.PivotFilterCriteria) []string {
	if c == nil {
		return nil
	}
	var out []string
	if !c.VisibleByDefault && (len(c.VisibleValues) > 0 || c.Condition == nil) {
		out = append(out, strings.TrimSpace(column+" show "+strings.Join(c.VisibleValues, ", ")))
	}
	if cond := c.Condition; cond != nil {
		text := column + " " + conditionName(cond.Type)
		var values []string
		for _, v := range cond.Values {
			switch {
			case v == nil:
			case v.UserEnteredValue != "":
				values = append(values, v.UserEnteredValue)
			case v.RelativeDate != "":
				values = append(values, strings.ToLower(v.RelativeDate))
			}
		}
		if len(values) > 0 {
			text += " " + strings.Join(values, ", ")
		}
		out = append(out, text)
	}
	return out
}

// conditionName is a condition type in the spelling a caller writes.
func conditionName(apiType string) string {
	for name, api := range conditionTypes {
		if api == apiType {
			return name
		}
	}
	return strings.ToLower(apiType)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
