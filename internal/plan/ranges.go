package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The builders for the things attached to a range: a name, a
// protection, a validation rule, a table, a banding, a conditional
// format rule. Each has add, update and delete, and each update carries
// the mask that names what it changes.

// NamedRangeAdd names a rectangle.
func NamedRangeAdd(name string, sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{AddNamedRange: &gsheets.AddNamedRangeRequest{
		NamedRange: &gsheets.NamedRange{Name: name, Range: rect.GridRange(sheetID)},
	}}
}

// NamedRangeMove points an existing name at a different rectangle.
//
// Moving only. Renaming through the same request was written first and
// never reachable: manage_range identifies a named range by its name, so
// a rename would be a call that cannot say which one it means.
func NamedRangeMove(id string, sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{UpdateNamedRange: &gsheets.UpdateNamedRangeRequest{
		NamedRange: &gsheets.NamedRange{NamedRangeID: id, Range: rect.GridRange(sheetID)},
		Fields:     "range",
	}}
}

// NamedRangeDelete removes the name and leaves the cells.
func NamedRangeDelete(id string) *gsheets.Request {
	return &gsheets.Request{DeleteNamedRange: &gsheets.DeleteNamedRangeRequest{NamedRangeID: id}}
}

// ProtectedAdd protects a rectangle.
//
// No editors: with none named the protection is the owner's alone, which
// is the API's default and the strongest form. Naming them is §17a's,
// along with the question of how a live driver would exercise an
// argument that takes an email address. warningOnly is the weak form —
// it warns in the interface and refuses nothing over the API, and the
// guard treats it as such.
func ProtectedAdd(sheetID int, rect a1.Rect, description string, warningOnly bool) *gsheets.Request {
	return &gsheets.Request{AddProtectedRange: &gsheets.AddProtectedRangeRequest{
		ProtectedRange: &gsheets.ProtectedRange{
			Range: rect.GridRange(sheetID), Description: description, WarningOnly: warningOnly,
		},
	}}
}

// ProtectedUpdate changes a protection's description, its warning-only
// flag, or both.
func ProtectedUpdate(id int, description string, warningOnly, setWarning bool) *gsheets.Request {
	p := &gsheets.ProtectedRange{ProtectedRangeID: id}
	var fields []string
	if description != "" {
		p.Description = description
		fields = append(fields, "description")
	}
	if setWarning {
		p.WarningOnly = warningOnly
		fields = append(fields, "warningOnly")
	}
	return &gsheets.Request{UpdateProtectedRange: &gsheets.UpdateProtectedRangeRequest{
		ProtectedRange: p, Fields: strings.Join(fields, ","),
	}}
}

// ProtectedDelete removes a protection.
func ProtectedDelete(id int) *gsheets.Request {
	return &gsheets.Request{DeleteProtectedRange: &gsheets.DeleteProtectedRangeRequest{ProtectedRangeID: id}}
}

// Validation puts a rule on a rectangle. A nil rule clears whatever is
// there, which is the API's own meaning for the field being absent.
func Validation(sheetID int, rect a1.Rect, rule *gsheets.DataValidationRule) *gsheets.Request {
	return &gsheets.Request{SetDataValidation: &gsheets.SetDataValidationRequest{
		Range: rect.GridRange(sheetID), Rule: rule,
	}}
}

// TableAdd makes a native table over a rectangle.
func TableAdd(name string, sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{AddTable: &gsheets.AddTableRequest{
		Table: &gsheets.Table{Name: name, Range: rect.GridRange(sheetID)},
	}}
}

// TableRename gives a table a different name and leaves it where it is.
//
// Moving a table through the same request was written and never
// reachable: manage_range names a table by the range it covers, so a
// move would be a call whose own identifier is what it is changing.
func TableRename(id, name string) *gsheets.Request {
	return &gsheets.Request{UpdateTable: &gsheets.UpdateTableRequest{
		Table: &gsheets.Table{TableID: id, Name: name}, Fields: "name",
	}}
}

// TableDelete removes the table and leaves the cells on the sheet.
func TableDelete(id string) *gsheets.Request {
	return &gsheets.Request{DeleteTable: &gsheets.DeleteTableRequest{TableID: id}}
}

// BandingAdd colours alternate rows of a rectangle.
//
// Rows, because that is what manage_range offers; §17a carries the
// column form, which wants an argument on a tool that already takes
// eighteen. BandingUpdate does take the axis, because a column banding
// made elsewhere can be recoloured here and writing rowProperties onto
// one leaves it carrying both sets, which the API rejects.
func BandingAdd(sheetID int, rect a1.Rect, props *gsheets.BandingProperties) *gsheets.Request {
	return &gsheets.Request{AddBanding: &gsheets.AddBandingRequest{
		BandedRange: &gsheets.BandedRange{Range: rect.GridRange(sheetID), RowProperties: props},
	}}
}

// BandingUpdate re-colours an existing banding.
func BandingUpdate(id int, columns bool, props *gsheets.BandingProperties) *gsheets.Request {
	b := &gsheets.BandedRange{BandedRangeID: id}
	field := "rowProperties"
	if columns {
		b.ColumnProperties, field = props, "columnProperties"
	} else {
		b.RowProperties = props
	}
	return &gsheets.Request{UpdateBanding: &gsheets.UpdateBandingRequest{BandedRange: b, Fields: field}}
}

// BandingDelete removes a banding.
func BandingDelete(id int) *gsheets.Request {
	return &gsheets.Request{DeleteBanding: &gsheets.DeleteBandingRequest{BandedRangeID: id}}
}

// Banding builds the colours from one base colour: the header a shade
// of it and the two bands the colour and white.
//
// One colour rather than four, because four is what a person does not
// have in their hand and the interface asks for one too.
func Banding(base *gsheets.ColorStyle, header bool) *gsheets.BandingProperties {
	props := &gsheets.BandingProperties{
		FirstBandColorStyle:  white(),
		SecondBandColorStyle: base,
	}
	if header {
		props.HeaderColorStyle = darker(base)
	}
	return props
}

func white() *gsheets.ColorStyle {
	return &gsheets.ColorStyle{RGBColor: &gsheets.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1}}
}

// darker is the header's shade of the band colour. Two thirds, which is
// enough to read as a heading and not so much that a pale band gives a
// black header.
func darker(c *gsheets.ColorStyle) *gsheets.ColorStyle {
	if c == nil || c.RGBColor == nil {
		return nil
	}
	return &gsheets.ColorStyle{RGBColor: &gsheets.Color{
		Red: c.RGBColor.Red * 2 / 3, Green: c.RGBColor.Green * 2 / 3, Blue: c.RGBColor.Blue * 2 / 3, Alpha: 1,
	}}
}

// Rule builds a conditional format rule over one rectangle.
//
// Built once and handed to whatever needs it: the request that sends it
// and the renderer that describes it. Assembled separately in each, the
// description of the rule a caller just wrote and the description
// read_formatting gives of the same rule could differ, and only one of
// the two has a golden over it.
func Rule(sheetID int, rect a1.Rect, cond *gsheets.BooleanCondition, format *gsheets.CellFormat) *gsheets.ConditionalFormatRule {
	return &gsheets.ConditionalFormatRule{
		Ranges:      []*gsheets.GridRange{rect.GridRange(sheetID)},
		BooleanRule: &gsheets.BooleanRule{Condition: cond, Format: format},
	}
}

// RuleAdd inserts a conditional format rule at an index. Rules are
// evaluated in order, so the index is how a caller says which wins.
func RuleAdd(index int, rule *gsheets.ConditionalFormatRule) *gsheets.Request {
	return &gsheets.Request{AddConditionalFormatRule: &gsheets.AddConditionalFormatRuleRequest{
		Index: index, Rule: rule,
	}}
}

// RuleUpdate replaces the rule at an index.
func RuleUpdate(index, sheetID int, rule *gsheets.ConditionalFormatRule) *gsheets.Request {
	return &gsheets.Request{UpdateConditionalFormatRule: &gsheets.UpdateConditionalFormatRuleRequest{
		Index: index, SheetID: sheetID, Rule: rule,
	}}
}

// RuleDelete removes the rule at an index.
func RuleDelete(index, sheetID int) *gsheets.Request {
	return &gsheets.Request{DeleteConditionalFormatRule: &gsheets.DeleteConditionalFormatRuleRequest{
		Index: index, SheetID: sheetID,
	}}
}

// conditions is the closed set of condition types this server builds,
// with how many operands each takes. -1 means one or more.
//
// Closed rather than passed through. The API's enum has members that
// need a range, a data source or a relative date, and a server that
// forwarded the caller's string would send a request no code here has
// read — which is the same rule that keeps the batchUpdate union typed.
var conditions = map[string]int{
	"number_greater":     1,
	"number_greater_eq":  1,
	"number_less":        1,
	"number_less_eq":     1,
	"number_eq":          1,
	"number_not_eq":      1,
	"number_between":     2,
	"number_not_between": 2,
	"text_contains":      1,
	"text_not_contains":  1,
	"text_starts_with":   1,
	"text_ends_with":     1,
	"text_eq":            1,
	"text_is_email":      0,
	"text_is_url":        0,
	"date_before":        1,
	"date_after":         1,
	"date_on_or_before":  1,
	"date_on_or_after":   1,
	"date_between":       2,
	"date_is_valid":      0,
	"one_of_list":        -1,
	"blank":              0,
	"not_blank":          0,
	"custom_formula":     1,
	"boolean":            0,
}

// conditionTypes are the API's spellings, where they differ from the
// name a caller writes.
var conditionTypes = map[string]string{
	"number_greater_eq": "NUMBER_GREATER_THAN_EQ",
	"number_less_eq":    "NUMBER_LESS_THAN_EQ",
}

// ConditionNames lists what a caller may write, for a message that has
// to say what the choices are.
func ConditionNames() []string {
	out := make([]string, 0, len(conditions))
	for name := range conditions {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ParseCondition builds a condition from a name and its operands, and
// checks the count. A "between" with one operand is a caller who meant
// something else, and the API's own refusal for it names no argument.
func ParseCondition(name string, values []string) (*gsheets.BooleanCondition, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	want, ok := conditions[name]
	if !ok {
		return nil, fmt.Errorf("condition %q is not one of %s", name, strings.Join(ConditionNames(), ", "))
	}
	switch {
	case want == -1 && len(values) == 0:
		return nil, fmt.Errorf("condition %q needs at least one value", name)
	case want >= 0 && len(values) != want:
		return nil, fmt.Errorf("condition %q takes %d value(s) and was given %d", name, want, len(values))
	}
	kind, ok := conditionTypes[name]
	if !ok {
		kind = strings.ToUpper(name)
	}
	cond := &gsheets.BooleanCondition{Type: kind}
	for _, v := range values {
		cond.Values = append(cond.Values, &gsheets.ConditionValue{UserEnteredValue: v})
	}
	return cond, nil
}

// ValidationRule wraps a condition into the rule a cell carries.
//
// strict decides whether a value the rule refuses is rejected or merely
// flagged, and it is the caller's: a dropdown that rejects is the point
// of a dropdown, and a warning is what a soft check wants.
func ValidationRule(cond *gsheets.BooleanCondition, strict bool, message string) *gsheets.DataValidationRule {
	return &gsheets.DataValidationRule{
		Condition: cond, Strict: strict, InputMessage: message,
		// A list condition shows its dropdown. Without this a
		// one_of_list rule validates and shows nothing, which is a
		// dropdown nobody can use.
		ShowCustomUI: cond != nil && cond.Type == "ONE_OF_LIST",
	}
}
