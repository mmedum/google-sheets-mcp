package plan

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// The builders for the things attached to a range: a name, a
// protection, a validation rule, a table, a banding, a conditional
// format rule. Each has add, update and delete, and each update carries
// the mask that names what it changes.

// namedRangePattern is what Google accepts as a named range's name:
// letters, digits and underscores, not starting with a digit.
var namedRangePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CheckNamedRange refuses a name Google will refuse, and says why.
//
// Verified live: a name with a space comes back as "The name given to
// this range is invalid", which names neither the rule nor the character
// that broke it. A caller who wrote "Livesheet band" has to guess.
//
// The A1 case is the one worth spelling out separately: "Q1" is a
// perfectly good word for a quarter and a perfectly good cell reference,
// and a named range cannot be either-or.
func CheckNamedRange(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a named range needs a name")
	case !namedRangePattern.MatchString(name):
		return fmt.Errorf("%q is not a valid name for a named range: use letters, digits and underscores, "+
			"starting with a letter or an underscore. Spaces and punctuation are not allowed", name)
	}
	if _, err := a1.ParseRect(name); err == nil {
		return fmt.Errorf("%q is a cell address, so it cannot also be a named range; "+
			"add a word to it, such as %q", name, name+"_total")
	}
	return nil
}

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

// TableAdd makes a native table over a rectangle, with the column
// types it was given. A column left out is Google's to type.
func TableAdd(name string, sheetID int, rect a1.Rect, columns []*gsheets.TableColumn) *gsheets.Request {
	return &gsheets.Request{AddTable: &gsheets.AddTableRequest{
		Table: &gsheets.Table{Name: name, Range: rect.GridRange(sheetID), ColumnProperties: columns},
	}}
}

// TableUpdate renames a table, retypes its columns, or both, and leaves
// it where it is. An empty name keeps the name, and nil columns keep the
// columns.
//
// columns is the whole array. The mask names columnProperties, a list,
// which may be replaced whole (§18), and then a column left out of it
// would lose its type and its dropdown.
//
// Moving a table through the same request was written and never
// reachable: manage_range names a table by the range it covers, so a
// move would be a call whose own identifier is what it is changing.
func TableUpdate(id, name string, columns []*gsheets.TableColumn) *gsheets.Request {
	var fields []string
	if name != "" {
		fields = append(fields, "name")
	}
	if columns != nil {
		fields = append(fields, "columnProperties")
	}
	return &gsheets.Request{UpdateTable: &gsheets.UpdateTableRequest{
		Table:  &gsheets.Table{TableID: id, Name: name, ColumnProperties: columns},
		Fields: strings.Join(fields, ","),
	}}
}

// ColumnSpec is one entry of column_types, parsed: the column as the
// caller named it, and the type it gets.
type ColumnSpec struct {
	// Column is a letter or a header, as written. Which column of the
	// table it is takes a read, so the service resolves it.
	Column string
	Type   string
	// Rule is a dropdown's list, and nil for every other type.
	Rule *gsheets.TableColumnDataValidationRule
}

// columnTypes are the column types a caller writes. Each is the API's
// name lower-cased, except where columnTypeAPI says otherwise.
var columnTypes = []string{"boolean", "currency", "date", "date_time", "dropdown", "number", "percent", "text", "time"}

// columnTypeAPI are the API's spellings, where they differ from the
// name a caller writes.
var columnTypeAPI = map[string]string{"number": gsheets.ColumnDouble}

// chipTypes are the smart chip column types. A read shows them; nothing
// here writes one, so each is refused by name rather than as unknown.
var chipTypes = []string{"files_chip", "people_chip", "finance_chip", "place_chip", "ratings_chip"}

// ColumnTypeName is a column type in the spelling ParseColumnType takes,
// so a type read back can be written again as it reads. A chip type
// reads the same way, and is refused.
func ColumnTypeName(apiType string) string {
	for name, api := range columnTypeAPI {
		if api == apiType {
			return name
		}
	}
	return strings.ToLower(apiType)
}

// columnEntry splits "<column> <type>" with optional options, after a
// colon or in parentheses: "Status dropdown: Open, Done" as a caller
// writes it, "Status dropdown (Open, Done)" as get_spreadsheet shows it.
// The column is the shortest run of text the type can follow, so a
// header with spaces in it, or with a type's name in it, still works:
// "Due date date" is the column "Due date" typed as a date.
var columnEntry = regexp.MustCompile(`(?i)^(.+?)\s+(` +
	strings.Join(slices.Sorted(slices.Values(append(slices.Clone(columnTypes), chipTypes...))), "|") +
	`)\s*(?::(.*)|\((.*)\))?$`)

// ParseColumnType reads one entry of column_types: "B date", "Amount
// currency", "Status dropdown: Open, In progress, Done".
//
// A dropdown's options are separated by commas, so an option with a
// comma in it cannot be written here; data_validation with one_of_list
// takes each option as its own value.
func ParseColumnType(text string) (ColumnSpec, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ColumnSpec{}, fmt.Errorf("column_types has an empty entry")
	}
	m := columnEntry.FindStringSubmatchIndex(text)
	if m == nil {
		last := strings.LastIndexFunc(text, unicode.IsSpace)
		if last < 0 {
			return ColumnSpec{}, fmt.Errorf("column type %q needs a column and a type, such as \"B date\" or "+
				"\"Amount currency\"", text)
		}
		return ColumnSpec{}, fmt.Errorf("column type %q ends in %q, which is not one of %s", text,
			text[last+1:], strings.Join(columnTypes, ", "))
	}
	column := strings.TrimSpace(text[m[2]:m[3]])
	name := strings.ToLower(text[m[4]:m[5]])
	listed := ""
	hasOptions := m[6] >= 0 || m[8] >= 0
	switch {
	case m[6] >= 0:
		listed = text[m[6]:m[7]]
	case m[8] >= 0:
		listed = text[m[8]:m[9]]
	}
	if slices.Contains(chipTypes, name) {
		return ColumnSpec{}, fmt.Errorf("column type %q asks for %s, a smart chip column, which this server "+
			"shows and does not set", text, name)
	}
	spec := ColumnSpec{Column: column, Type: strings.ToUpper(name)}
	if api, ok := columnTypeAPI[name]; ok {
		spec.Type = api
	}
	if spec.Type != gsheets.ColumnDropdown {
		if hasOptions {
			return ColumnSpec{}, fmt.Errorf("column type %q gives %s options, and only a dropdown takes them", text, name)
		}
		return spec, nil
	}
	if !hasOptions {
		return ColumnSpec{}, fmt.Errorf("column type %q needs its options after a colon, such as "+
			"\"%s dropdown: Open, In progress, Done\"", text, column)
	}
	var options []string
	for _, option := range strings.Split(listed, ",") {
		option = strings.TrimSpace(option)
		if option == "" {
			return ColumnSpec{}, fmt.Errorf("column type %q has an empty option; options are separated by commas", text)
		}
		options = append(options, option)
	}
	cond, err := ParseCondition("one_of_list", options)
	if err != nil {
		return ColumnSpec{}, err
	}
	spec.Rule = &gsheets.TableColumnDataValidationRule{Condition: cond}
	return spec, nil
}

// TableDelete removes the table and leaves the cells on the sheet.
func TableDelete(id string) *gsheets.Request {
	return &gsheets.Request{DeleteTable: &gsheets.DeleteTableRequest{TableID: id}}
}

// BandingAdd colors alternate rows of a rectangle.
//
// Rows, because that is what manage_range offers; §17a carries the
// column form, which wants an argument on a tool that already takes
// twenty-one. BandingUpdate does take the axis, because a column banding
// made elsewhere can be recolored here and writing rowProperties onto
// one leaves it carrying both sets, which the API rejects.
func BandingAdd(sheetID int, rect a1.Rect, props *gsheets.BandingProperties) *gsheets.Request {
	return &gsheets.Request{AddBanding: &gsheets.AddBandingRequest{
		BandedRange: &gsheets.BandedRange{Range: rect.GridRange(sheetID), RowProperties: props},
	}}
}

// BandingUpdate re-colors an existing banding.
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

// Banding builds the colors from one base color: the header a shade
// of it and the two bands the color and white.
//
// One color rather than four, because four is what a person does not
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

// darker is the header's shade of the band color. Two thirds, which is
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

// Gradient builds a color scale over one rectangle, for the same reason
// Rule exists: the request and the description share one value.
func Gradient(sheetID int, rect a1.Rect, scale *gsheets.GradientRule) *gsheets.ConditionalFormatRule {
	return &gsheets.ConditionalFormatRule{
		Ranges:       []*gsheets.GridRange{rect.GridRange(sheetID)},
		GradientRule: scale,
	}
}

// pointTypes are the color-scale point types a caller writes.
var pointTypes = map[string]string{
	"min":        gsheets.PointMin,
	"max":        gsheets.PointMax,
	"number":     gsheets.PointNumber,
	"percent":    gsheets.PointPercent,
	"percentile": gsheets.PointPercentile,
}

// ParseGradient builds a color scale from two or three points, lowest
// first, each "<type> [value] #hex": "min #ffffff", "percentile 50
// #ffd666", "max #57bb8a".
//
// min and max take no value: they are the range's own lowest and
// highest. number, percent and percentile need one, which is kept as
// written, since the API takes it as text and it may be a formula. min
// is only for the first point and max only for the last, the way the
// Sheets interface offers them; whether the API takes either as a
// midpoint is unverified (§18), so neither is sent there.
func ParseGradient(points []string) (*gsheets.GradientRule, error) {
	if len(points) < 2 || len(points) > 3 {
		return nil, fmt.Errorf("gradient takes two or three points, lowest first, such as "+
			"[\"min #ffffff\", \"max #57bb8a\"]; it was given %d", len(points))
	}
	built := make([]*gsheets.InterpolationPoint, len(points))
	for i, text := range points {
		point, err := parsePoint(text)
		if err != nil {
			return nil, err
		}
		switch {
		case point.Type == gsheets.PointMax && i < len(points)-1:
			return nil, fmt.Errorf("gradient point %q uses max, which is only for the last point", strings.TrimSpace(text))
		case point.Type == gsheets.PointMin && i > 0:
			return nil, fmt.Errorf("gradient point %q uses min, which is only for the first point", strings.TrimSpace(text))
		}
		built[i] = point
	}
	scale := &gsheets.GradientRule{Minpoint: built[0], Maxpoint: built[len(built)-1]}
	if len(built) == 3 {
		scale.Midpoint = built[1]
	}
	return scale, nil
}

// parsePoint reads one "<type> [value] #hex". The value is everything
// between the first word and the last, so a formula with spaces in it
// survives.
func parsePoint(text string) (*gsheets.InterpolationPoint, error) {
	text = strings.TrimSpace(text)
	first := strings.IndexFunc(text, unicode.IsSpace)
	if first < 0 {
		return nil, fmt.Errorf("gradient point %q needs a color at the end, such as \"max #57bb8a\"", text)
	}
	last := strings.LastIndexFunc(text, unicode.IsSpace)
	name := strings.ToLower(text[:first])
	value := strings.TrimSpace(text[first : last+1])
	colorText := text[last+1:]

	kind, ok := pointTypes[name]
	if !ok {
		return nil, fmt.Errorf("gradient point %q starts with %q, which is not min, max, number, percent or percentile",
			text, text[:first])
	}
	color, err := ParseColor(colorText)
	if err != nil || color == nil || !strings.HasPrefix(colorText, "#") {
		return nil, fmt.Errorf("gradient point %q ends in %q, which is not a hex color such as #57bb8a", text, colorText)
	}
	switch {
	case kind == gsheets.PointMin && value != "":
		return nil, fmt.Errorf("gradient point %q gives min a value, and min takes none: it is the lowest value "+
			"in the range. Write \"min %s\"", text, colorText)
	case kind == gsheets.PointMax && value != "":
		return nil, fmt.Errorf("gradient point %q gives max a value, and max takes none: it is the highest value "+
			"in the range. Write \"max %s\"", text, colorText)
	case kind != gsheets.PointMin && kind != gsheets.PointMax && value == "":
		return nil, fmt.Errorf("gradient point %q needs a value between %s and the color, such as \"%s 50 %s\"",
			text, name, name, colorText)
	}
	return &gsheets.InterpolationPoint{ColorStyle: color, Type: kind, Value: value}, nil
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
