package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/plan"
)

// The operand count is the check worth having: a "between" with one
// value is a caller who meant something else, and the API's own refusal
// for it names no argument.
func TestParseConditionChecksTheOperandCount(t *testing.T) {
	cond, err := plan.ParseCondition("number_between", []string{"1", "10"})
	if err != nil || cond.Type != "NUMBER_BETWEEN" || len(cond.Values) != 2 {
		t.Fatalf("number_between = %+v, %v", cond, err)
	}
	if _, err := plan.ParseCondition("number_between", []string{"1"}); err == nil {
		t.Error("a between with one value was accepted")
	}
	if _, err := plan.ParseCondition("blank", []string{"x"}); err == nil {
		t.Error("a condition taking no values was given one and accepted it")
	}
	if _, err := plan.ParseCondition("one_of_list", nil); err == nil {
		t.Error("a list condition with no list was accepted")
	}
	if _, err := plan.ParseCondition("number_bigger", []string{"1"}); err == nil {
		t.Error("a condition outside the closed set was accepted")
	}
}

// Two names differ from the API's spelling. A caller writes the short
// one and the request has to carry the API's.
func TestParseConditionTranslatesTheTwoOddSpellings(t *testing.T) {
	for name, want := range map[string]string{
		"number_greater_eq": "NUMBER_GREATER_THAN_EQ",
		"number_less_eq":    "NUMBER_LESS_THAN_EQ",
		"text_contains":     "TEXT_CONTAINS",
		"custom_formula":    "CUSTOM_FORMULA",
	} {
		cond, err := plan.ParseCondition(name, []string{"1"})
		if err != nil {
			t.Fatalf("ParseCondition(%q): %v", name, err)
		}
		if cond.Type != want {
			t.Errorf("ParseCondition(%q).Type = %q, want %q", name, cond.Type, want)
		}
	}
}

// The refusal has to say what the choices are, so the names come from
// the same table the parser reads.
func TestConditionNamesAreListedInTheRefusal(t *testing.T) {
	names := plan.ConditionNames()
	if len(names) < 20 {
		t.Fatalf("only %d condition names", len(names))
	}
	_, err := plan.ParseCondition("nonsense", nil)
	if err == nil {
		t.Fatal("nonsense was accepted")
	}
	for _, name := range names {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %q, so a caller cannot see the choice", name)
		}
	}
}

// A list condition without its dropdown is a dropdown nobody can use.
func TestAListRuleShowsItsDropdown(t *testing.T) {
	list, _ := plan.ParseCondition("one_of_list", []string{"Quorbin", "Vandel"})
	if rule := plan.ValidationRule(list, true, ""); !rule.ShowCustomUI {
		t.Error("a one_of_list rule does not show its list")
	}
	other, _ := plan.ParseCondition("number_greater", []string{"1"})
	if rule := plan.ValidationRule(other, true, "over one"); rule.ShowCustomUI {
		t.Error("a non-list rule asks for a dropdown")
	} else if !rule.Strict || rule.InputMessage != "over one" {
		t.Errorf("rule = %+v", rule)
	}
}

// One color in, four out: the header a shade of it, the bands it and
// white. Nobody has four colors in their hand.
func TestBandingBuildsTheShadesFromOneColor(t *testing.T) {
	base, err := plan.ParseColor("#3366cc")
	if err != nil {
		t.Fatal(err)
	}
	props := plan.Banding(base, true)
	if props.SecondBandColorStyle != base {
		t.Error("the band is not the color that was given")
	}
	if props.FirstBandColorStyle == nil || props.FirstBandColorStyle.RGBColor.Red != 1 {
		t.Error("the other band is not white")
	}
	if props.HeaderColorStyle == nil {
		t.Fatal("a banding asked for a header and got none")
	}
	if props.HeaderColorStyle.RGBColor.Blue >= base.RGBColor.Blue {
		t.Error("the header is not darker than the band")
	}
	if plan.Banding(base, false).HeaderColorStyle != nil {
		t.Error("a banding with no header got one")
	}
}

// Exactly one member of the union, for every builder phase 2 adds. A
// request with two would be a request nobody wrote and Google would
// apply both.
func TestPhase2BuildersSetOneUnionMember(t *testing.T) {
	cond, _ := plan.ParseCondition("number_greater", []string{"1"})
	format := &gsheets.CellFormat{TextFormat: &gsheets.TextFormat{Bold: true}}
	for name, req := range map[string]*gsheets.Request{
		"NamedRangeAdd":    plan.NamedRangeAdd("Quorbin", 1, rect),
		"NamedRangeMove":   plan.NamedRangeMove("nr1", 1, rect),
		"NamedRangeDelete": plan.NamedRangeDelete("nr1"),
		"ProtectedAdd":     plan.ProtectedAdd(1, rect, "why", false),
		"ProtectedUpdate":  plan.ProtectedUpdate(2, "why", true, true),
		"ProtectedDelete":  plan.ProtectedDelete(2),
		"Validation":       plan.Validation(1, rect, plan.ValidationRule(cond, true, "")),
		"TableAdd":         plan.TableAdd("Skerry", 1, rect),
		"TableRename":      plan.TableRename("t1", "Skerry"),
		"TableDelete":      plan.TableDelete("t1"),
		"BandingAdd":       plan.BandingAdd(1, rect, &gsheets.BandingProperties{}),
		"BandingUpdate":    plan.BandingUpdate(1, true, &gsheets.BandingProperties{}),
		"BandingDelete":    plan.BandingDelete(1),
		"RuleAdd":          plan.RuleAdd(0, plan.Rule(1, rect, cond, format)),
		"RuleUpdate":       plan.RuleUpdate(0, 1, plan.Rule(1, rect, cond, format)),
		"RuleDelete":       plan.RuleDelete(0, 1),
		"ClearFormat":      plan.ClearFormat(1, rect),
		"Merge":            plan.Merge(1, rect, gsheets.MergeAll),
		"Unmerge":          plan.Unmerge(1, rect),
		"Sort":             plan.Sort(1, rect, nil),
		"FindReplace":      plan.FindReplace(1, rect, "a", "b", plan.FindReplaceOptions{}),
		"Trim":             plan.Trim(1, rect),
		"Dedupe":           plan.Dedupe(1, rect, []int{1}),
		"TextToColumns":    plan.TextToColumns(1, rect, plan.Delimiter{Kind: gsheets.DelimiterComma}),
		"Randomize":        plan.Randomize(1, rect),
		"AutoFill":         plan.AutoFill(1, rect, true, 5),
		"CopyPaste":        plan.CopyPaste(1, rect, rect, 2, gsheets.PasteNormal, false),
		"CutPaste":         plan.CutPaste(1, rect, 2, 4, 1, gsheets.PasteNormal),
	} {
		if n := setMembers(req); n != 1 {
			t.Errorf("%s set %d union members", name, n)
		}
	}
}

// An update with an empty mask changes nothing, which the API treats as
// an error rather than a no-op.
func TestEveryUpdateCarriesAMask(t *testing.T) {
	if got := plan.NamedRangeMove("nr1", 1, rect).UpdateNamedRange.Fields; got != "range" {
		t.Errorf("a move masks %q", got)
	}
	if got := plan.TableRename("t1", "Skerry").UpdateTable.Fields; got != "name" {
		t.Errorf("a rename masks %q", got)
	}
	if got := plan.ProtectedUpdate(1, "why", false, false).UpdateProtectedRange.Fields; got != "description" {
		t.Errorf("a description-only update masks %q", got)
	}
	if got := plan.ProtectedUpdate(1, "", true, true).UpdateProtectedRange.Fields; got != "warningOnly" {
		t.Errorf("a warning-only update masks %q", got)
	}
	// The axis a banding runs along decides its mask, and writing the
	// wrong one leaves the range carrying both sets.
	if got := plan.BandingUpdate(1, true, nil).UpdateBanding.Fields; got != "columnProperties" {
		t.Errorf("a column banding masks %q", got)
	}
	if got := plan.BandingUpdate(1, false, nil).UpdateBanding.Fields; got != "rowProperties" {
		t.Errorf("a row banding masks %q", got)
	}
}

// setMembers counts the union members a request sets. One is the only
// right answer: the union is a struct where exactly one field carries a
// request, and two would be a request nobody wrote.
func setMembers(req *gsheets.Request) int {
	raw, err := json.Marshal(req)
	if err != nil {
		return -1
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return -1
	}
	return len(members)
}

// Google's own refusal for a bad name is "The name given to this range
// is invalid", which names neither the rule nor the character that broke
// it — verified live, on a name whose only fault was a space.
func TestCheckNamedRange(t *testing.T) {
	for _, ok := range []string{"Totals", "_totals", "Skerry_totals", "Q1_total", "a1b"} {
		if err := plan.CheckNamedRange(ok); err != nil {
			t.Errorf("CheckNamedRange(%q) = %v", ok, err)
		}
	}
	for name, why := range map[string]string{
		"":               "empty",
		"Livesheet band": "a space",
		"totals!":        "punctuation",
		"1totals":        "a leading digit",
		"Q1":             "a cell address",
		"AB12":           "a cell address",
	} {
		err := plan.CheckNamedRange(name)
		if err == nil {
			t.Errorf("CheckNamedRange(%q) accepted %s", name, why)
			continue
		}
		// The rule, not just the refusal: a caller who cannot see what
		// was wrong has to guess, which is what Google's own message
		// leaves them doing.
		if name != "" && !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal for %q does not quote it: %v", name, err)
		}
	}
	// A cell address is refused with a way out rather than a rule.
	if err := plan.CheckNamedRange("Q1"); err == nil || !strings.Contains(err.Error(), "Q1_total") {
		t.Errorf("the cell-address refusal offers no alternative: %v", err)
	}
}

// A color scale is two or three points, lowest first, each with its
// type, its value where the type needs one, and a hex color sent as
// colorStyle, never the deprecated color.
func TestParseGradientBuildsThePoints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		points []string
		want   string
	}{
		{"three points", []string{"min #ffffff", "percentile 50 #ffd666", "max #57bb8a"},
			`{"minpoint":{"colorStyle":{"rgbColor":{"red":1,"green":1,"blue":1,"alpha":1}},"type":"MIN"},` +
				`"midpoint":{"colorStyle":{"rgbColor":{"red":1,"green":0.8392156862745098,"blue":0.4,"alpha":1}},"type":"PERCENTILE","value":"50"},` +
				`"maxpoint":{"colorStyle":{"rgbColor":{"red":0.3411764705882353,"green":0.7333333333333333,"blue":0.5411764705882353,"alpha":1}},"type":"MAX"}}`},
		// The type is read in any case, the value kept as written, and a
		// formula with spaces in it survives whole.
		{"two points with values", []string{"NUMBER  0  #fff", "percent =MAX(B2:B9, 1) #000"},
			`{"minpoint":{"colorStyle":{"rgbColor":{"red":1,"green":1,"blue":1,"alpha":1}},"type":"NUMBER","value":"0"},` +
				`"maxpoint":{"colorStyle":{"rgbColor":{"alpha":1}},"type":"PERCENT","value":"=MAX(B2:B9, 1)"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scale, err := plan.ParseGradient(tc.points)
			if err != nil {
				t.Fatalf("ParseGradient: %v", err)
			}
			got, _ := json.Marshal(scale)
			if string(got) != tc.want {
				t.Errorf("scale =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestParseGradientRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		points []string
		want   string
	}{
		{"one point", []string{"min #ffffff"}, "two or three points"},
		{"four points", []string{"min #ffffff", "number 1 #ffffff", "number 2 #ffffff", "max #57bb8a"},
			"two or three points"},
		{"no color", []string{"min", "max #57bb8a"}, `"min" needs a color at the end`},
		{"a value where the color goes", []string{"percentile 50", "max #57bb8a"},
			`"percentile 50" ends in "50", which is not a hex color`},
		{"a color with no #", []string{"min ffffff", "max #57bb8a"}, `ends in "ffffff"`},
		{"none for a color", []string{"min none", "max #57bb8a"}, `ends in "none"`},
		{"an unknown type", []string{"lowest #ffffff", "max #57bb8a"}, `starts with "lowest"`},
		{"a value on min", []string{"min 5 #ffffff", "max #57bb8a"},
			`"min 5 #ffffff" gives min a value, and min takes none: it is the lowest value in the range. Write "min #ffffff"`},
		{"a value on max", []string{"min #ffffff", "max 9 #57bb8a"},
			`gives max a value, and max takes none: it is the highest value in the range. Write "max #57bb8a"`},
		{"no value on number", []string{"number #ffffff", "max #57bb8a"},
			`"number #ffffff" needs a value between number and the color, such as "number 50 #ffffff"`},
		{"no value on percent", []string{"min #ffffff", "percent #57bb8a"}, `"percent #57bb8a" needs a value`},
		{"max first", []string{"max #ffffff", "number 9 #57bb8a"}, "only for the last point"},
		{"min last", []string{"number 1 #ffffff", "min #57bb8a"}, "only for the first point"},
		{"min in the middle", []string{"number 1 #ffffff", "min #ffd666", "max #57bb8a"}, "only for the first point"},
		{"max in the middle", []string{"min #ffffff", "max #ffd666", "number 9 #57bb8a"}, "only for the last point"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plan.ParseGradient(tc.points)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}
