package plan_test

import (
	"encoding/json"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/plan"
)

// asJSON is a rule or a condition as it goes on the wire, so a test
// states the request rather than walking pointers.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestParsePivotGroup is the grammar of a group: a column alone, a date
// rule, a histogram rule. Each entry is also written back by
// PivotGroupText to the spelling given, which is what list reports.
func TestParsePivotGroup(t *testing.T) {
	for _, tc := range []struct {
		in, column, rule, text string
	}{
		{"Region", "Region", "null", "Region"},
		{"B by year_month", "B", `{"dateTimeRule":{"type":"YEAR_MONTH"}}`, "B by year_month"},
		{"Date by YEAR", "Date", `{"dateTimeRule":{"type":"YEAR"}}`, "Date by year"},
		{"Logged by hour_minute_ampm", "Logged", `{"dateTimeRule":{"type":"HOUR_MINUTE_AMPM"}}`, "Logged by hour_minute_ampm"},
		{"Age every 10 from 20 to 70", "Age", `{"histogramRule":{"interval":10,"start":20,"end":70}}`, "Age every 10 from 20 to 70"},
		{"Age every 2.5", "Age", `{"histogramRule":{"interval":2.5}}`, "Age every 2.5"},
		{"Age every 5 to 100", "Age", `{"histogramRule":{"interval":5,"end":100}}`, "Age every 5 to 100"},
		// A start of zero is sent, not dropped as unset.
		{"Age every 10 from 0", "Age", `{"histogramRule":{"interval":10,"start":0}}`, "Age every 10 from 0"},
		{"Score every 5 from -10 to 10", "Score", `{"histogramRule":{"interval":5,"start":-10,"end":10}}`,
			"Score every 5 from -10 to 10"},
		// The last keyword with something after it starts the rule, so a
		// heading may hold either word.
		{"Sold by by month", "Sold by", `{"dateTimeRule":{"type":"MONTH"}}`, "Sold by by month"},
		{"Pay every week every 5", "Pay every week", `{"histogramRule":{"interval":5}}`, "Pay every week every 5"},
		{"Sold by", "Sold by", "null", "Sold by"},
	} {
		got, err := plan.ParsePivotGroup(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.Column != tc.column || asJSON(t, got.Rule) != tc.rule {
			t.Errorf("%q = column %q rule %s, want %q %s", tc.in, got.Column, asJSON(t, got.Rule), tc.column, tc.rule)
		}
		if text := plan.PivotGroupText(got.Column, got.Rule, false); text != tc.text {
			t.Errorf("%q is written back as %q, want %q", tc.in, text, tc.text)
		}
	}
}

func TestParsePivotGroupRefusals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Date by fortnight", `"Date by fortnight" groups by "fortnight", which is not one of second, minute, hour, ` +
			`hour_minute, hour_minute_ampm, day_of_week, day_of_year, day_of_month, day_month, month, quarter, year, ` +
			`year_month, year_quarter, year_month_day`},
		{"Region by hand", `"Region by hand" is a grouping made by hand in Sheets, which this tool cannot set; ` +
			`an update that leaves the groups out keeps it`},
		{"Age every ten", `"Age every ten" has "ten" as its bucket size, which is not a number`},
		{"Age every 0", `"Age every 0" has a bucket size of 0, and it has to be more than 0`},
		{"Age every -5", `"Age every -5" has a bucket size of -5, and it has to be more than 0`},
		{"Age every 10 from 70 to 20", `"Age every 10 from 70 to 20" starts at 70 and ends at 20; the start has ` +
			`to be below the end`},
		{"Age every 10 from 20 to 20", `"Age every 10 from 20 to 20" starts at 20 and ends at 20; the start has ` +
			`to be below the end`},
		{"Age every 10 from x", `"Age every 10 from x" has "x" as its start, which is not a number`},
		{"Age every 10 between 1 and 2", `"Age every 10 between 1 and 2" needs a bucket size after every, then an ` +
			`optional start and end, such as "Age every 10 from 20 to 70"`},
	} {
		_, err := plan.ParsePivotGroup(tc.in)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: error =\n%v\nwant\n%s", tc.in, err, tc.want)
		}
	}
}

// TestAGroupMadeByHandReadsAsOne is the one rule this tool reads and
// does not write: list says so in words the parser then refuses.
func TestAGroupMadeByHandReadsAsOne(t *testing.T) {
	if got := plan.PivotGroupText("C", nil, true); got != "C by hand" {
		t.Errorf("a group made by hand reads %q", got)
	}
	if _, err := plan.ParsePivotGroup("C by hand"); err == nil {
		t.Error("a group made by hand was taken back")
	}
}

func TestParsePivotValue(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want plan.PivotValueEntry
		text string
	}{
		{"B sum", plan.PivotValueEntry{Column: "B", Function: "SUM"}, "B sum"},
		{"Units sum as Total units", plan.PivotValueEntry{Column: "Units", Function: "SUM", Name: "Total units"},
			"Units sum as Total units"},
		// The summary is the last word, so a heading may have a space.
		{"Unit price average as Mean", plan.PivotValueEntry{Column: "Unit price", Function: "AVERAGE", Name: "Mean"},
			"Unit price average as Mean"},
		{"C stdevp", plan.PivotValueEntry{Column: "C", Function: "STDEVP"}, "C stdevp"},
		{"C count", plan.PivotValueEntry{Column: "C", Function: "COUNTA"}, "C count"},
		// A calculated value: as written by default, or summed per row.
		{"=SUM(Revenue)/SUM(Cost) as Margin",
			plan.PivotValueEntry{Formula: "=SUM(Revenue)/SUM(Cost)", Function: "CUSTOM", Name: "Margin"},
			"=SUM(Revenue)/SUM(Cost) as Margin"},
		{"=Revenue-Cost sum as Margin", plan.PivotValueEntry{Formula: "=Revenue-Cost", Function: "SUM", Name: "Margin"},
			"=Revenue-Cost sum as Margin"},
		{"=Revenue - Cost CUSTOM as Net", plan.PivotValueEntry{Formula: "=Revenue - Cost", Function: "CUSTOM", Name: "Net"},
			"=Revenue - Cost as Net"},
		// The name follows the last " as ", and a summary word inside the
		// formula is the formula's.
		{"=IF(Cost>0, Revenue, 0) as share as Ratio",
			plan.PivotValueEntry{Formula: "=IF(Cost>0, Revenue, 0) as share", Function: "CUSTOM", Name: "Ratio"},
			"=IF(Cost>0, Revenue, 0) as share as Ratio"},
	} {
		got, err := plan.ParsePivotValue(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.in, got, tc.want)
		}
		value := &gsheets.PivotValue{Formula: got.Formula, SummarizeFunction: got.Function, Name: got.Name}
		if text := plan.PivotValueText(got.Column, value); text != tc.text {
			t.Errorf("%q is written back as %q, want %q", tc.in, text, tc.text)
		}
	}
}

func TestParsePivotValueRefusals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" ", "values has an empty entry"},
		{"B", `"B" does not say how to summarize the column; write it as "B sum", and add "as Total" to name it`},
		{"B total", `"total" is not a summary this server offers: average, count, count_numbers, count_unique, max, ` +
			`median, min, product, stdev, stdevp, sum, var, varp`},
		{"B custom", `"B custom" asks for custom, which is for a calculated value, one that starts with =; a column ` +
			`takes average, count, count_numbers, count_unique, max, median, min, product, stdev, stdevp, sum, var, varp`},
		{"=Revenue-Cost", `"=Revenue-Cost" is a calculated value, which needs a name: add "as <name>", such as ` +
			`"=Revenue-Cost as Margin"`},
		{"=Revenue-Cost as  ", `"=Revenue-Cost as" is a calculated value, which needs a name: add "as <name>", such ` +
			`as "=Revenue-Cost as Margin"`},
		{"=Revenue average as Mean", `"=Revenue average as Mean" summarizes a calculated value by average, and Google ` +
			`takes only sum or custom for one`},
		{"= sum as Margin", `"= sum as Margin" has no formula after the =`},
	} {
		_, err := plan.ParsePivotValue(tc.in)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: error =\n%v\nwant\n%s", tc.in, err, tc.want)
		}
	}
}

// TestPivotValueTextNamesWhatItCannotParse is a summary Google has and
// this server does not name: written in Google's word, lower-cased, so
// sending it back is refused with the list rather than read as another.
func TestPivotValueTextNamesWhatItCannotParse(t *testing.T) {
	offset := 0
	got := plan.PivotValueText("A", &gsheets.PivotValue{SourceColumnOffset: &offset, SummarizeFunction: "NONE"})
	if got != "A none" {
		t.Errorf("got %q", got)
	}
}

func TestParsePivotFilter(t *testing.T) {
	for _, tc := range []struct {
		in, column, show, condition, text string
	}{
		{"Region show East, West", "Region", `["East","West"]`, "null", "Region show East, West"},
		{"Status show Open", "Status", `["Open"]`, "null", "Status show Open"},
		{"Unit price number_greater 100", "Unit price", "null",
			`{"type":"NUMBER_GREATER","values":[{"userEnteredValue":"100"}]}`, "Unit price number_greater 100"},
		{"Amount number_greater_eq 5", "Amount", "null",
			`{"type":"NUMBER_GREATER_THAN_EQ","values":[{"userEnteredValue":"5"}]}`, "Amount number_greater_eq 5"},
		{"Amount number_between 10, 20", "Amount", "null",
			`{"type":"NUMBER_BETWEEN","values":[{"userEnteredValue":"10"},{"userEnteredValue":"20"}]}`,
			"Amount number_between 10, 20"},
		// One operand is the rest of the entry, commas and all.
		{"Note text_contains a, b", "Note", "null",
			`{"type":"TEXT_CONTAINS","values":[{"userEnteredValue":"a, b"}]}`, "Note text_contains a, b"},
		{"Due blank", "Due", "null", `{"type":"BLANK"}`, "Due blank"},
		{"Due DATE_AFTER 2026-01-01", "Due", "null",
			`{"type":"DATE_AFTER","values":[{"userEnteredValue":"2026-01-01"}]}`, "Due date_after 2026-01-01"},
	} {
		got, err := plan.ParsePivotFilter(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.Column != tc.column || asJSON(t, got.Show) != tc.show || asJSON(t, got.Condition) != tc.condition {
			t.Errorf("%q = column %q show %s condition %s", tc.in, got.Column, asJSON(t, got.Show), asJSON(t, got.Condition))
		}
		criteria := &gsheets.PivotFilterCriteria{VisibleValues: got.Show, Condition: got.Condition, VisibleByDefault: got.Show == nil}
		if texts := plan.PivotFilterTexts(got.Column, criteria); len(texts) != 1 || texts[0] != tc.text {
			t.Errorf("%q is written back as %q, want %q", tc.in, texts, tc.text)
		}
	}
}

func TestParsePivotFilterRefusals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "filters has an empty entry"},
		{"Region", `filter "Region" needs a column, then show and the values to show, or a condition, such as ` +
			`"Region show East, West" or "Amount number_greater 100"`},
		{"Region show", `filter "Region show" has an empty value to show; the values are separated by commas`},
		{"Region show East,,West", `filter "Region show East,,West" has an empty value to show; the values are ` +
			`separated by commas`},
		{"Email text_is_email", `filter "Email text_is_email" uses text_is_email, which only data validation takes; ` +
			`a pivot table filter takes blank, custom_formula, date_after, date_before, not_blank, number_between, ` +
			`number_eq, number_greater, number_greater_eq, number_less, number_less_eq, number_not_between, ` +
			`number_not_eq, text_contains, text_ends_with, text_eq, text_not_contains, text_starts_with`},
		{"Status one_of_list Open", `filter "Status one_of_list Open" uses one_of_list, which only data validation ` +
			`takes; a pivot table filter takes blank, custom_formula, date_after, date_before, not_blank, ` +
			`number_between, number_eq, number_greater, number_greater_eq, number_less, number_less_eq, ` +
			`number_not_between, number_not_eq, text_contains, text_ends_with, text_eq, text_not_contains, text_starts_with`},
		{"Amount number_between 10", `filter "Amount number_between 10": condition "number_between" takes 2 ` +
			`value(s) and was given 1`},
		{"Amount number_greater", `filter "Amount number_greater": condition "number_greater" takes 1 value(s) ` +
			`and was given 0`},
		{"Due blank now", `filter "Due blank now": condition "blank" takes 0 value(s) and was given 1`},
	} {
		_, err := plan.ParsePivotFilter(tc.in)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: error =\n%v\nwant\n%s", tc.in, err, tc.want)
		}
	}
}

// TestPivotFilterTexts is what a filter made anywhere reads back as. The
// reference: with visibleByDefault false a value must be listed and meet
// the condition; with it true the list is ignored.
func TestPivotFilterTexts(t *testing.T) {
	greater := &gsheets.BooleanCondition{Type: "NUMBER_GREATER", Values: []*gsheets.ConditionValue{{UserEnteredValue: "5"}}}
	for _, tc := range []struct {
		name     string
		criteria *gsheets.PivotFilterCriteria
		want     string
	}{
		{"a list and a condition, both holding",
			&gsheets.PivotFilterCriteria{VisibleValues: []string{"East"}, Condition: greater},
			`["B show East","B number_greater 5"]`},
		{"a condition with the list ignored",
			&gsheets.PivotFilterCriteria{VisibleValues: []string{"East"}, Condition: greater, VisibleByDefault: true},
			`["B number_greater 5"]`},
		{"a filter that shows every value", &gsheets.PivotFilterCriteria{VisibleByDefault: true}, `null`},
		{"a filter that shows no value", &gsheets.PivotFilterCriteria{}, `["B show"]`},
		{"a relative date",
			&gsheets.PivotFilterCriteria{VisibleByDefault: true, Condition: &gsheets.BooleanCondition{
				Type: "DATE_AFTER", Values: []*gsheets.ConditionValue{{RelativeDate: "PAST_WEEK"}}}},
			`["B date_after past_week"]`},
	} {
		if got := asJSON(t, plan.PivotFilterTexts("B", tc.criteria)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
