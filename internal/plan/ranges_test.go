package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
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

// One colour in, four out: the header a shade of it, the bands it and
// white. Nobody has four colours in their hand.
func TestBandingBuildsTheShadesFromOneColour(t *testing.T) {
	base, err := plan.ParseColour("#3366cc")
	if err != nil {
		t.Fatal(err)
	}
	props := plan.Banding(base, true)
	if props.SecondBandColorStyle != base {
		t.Error("the band is not the colour that was given")
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
