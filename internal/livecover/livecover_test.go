package livecover

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	tools := []Tool{
		{Name: "read_range", Options: []string{"spreadsheet", "range", "max_chars"}},
		{Name: "get_spreadsheet", Options: []string{"spreadsheet"}},
	}
	sent := map[string]map[string]bool{
		"read_range": {"spreadsheet": true, "range": true},
	}
	r := Check(sent, tools)

	if r.Covered != 2 || r.Total != 4 {
		t.Errorf("Check = %d/%d, want 2/4", r.Covered, r.Total)
	}
	// max_chars is excused by name, so it is not a gap.
	if len(r.Gaps) != 0 {
		t.Errorf("gaps = %v, want none; max_chars is excused", r.Gaps)
	}
	if len(r.Excused) != 1 || !strings.Contains(r.Excused[0], "max_chars") {
		t.Errorf("excused = %v", r.Excused)
	}
	// A tool nothing called is worse than a missing option and is said
	// separately, because "the driver never calls this tool" and "the
	// driver calls it without one flag" want different fixes.
	if len(r.NoCaller) != 1 || r.NoCaller[0] != "get_spreadsheet" {
		t.Errorf("NoCaller = %v", r.NoCaller)
	}
	if err := Err(r); err == nil || !strings.Contains(err.Error(), "no step calls it") {
		t.Errorf("Err = %v", err)
	}
	if !strings.Contains(Summary(r), "2 of 4") {
		t.Errorf("Summary = %q", Summary(r))
	}
}

func TestCheckPassesWhenCovered(t *testing.T) {
	tools := []Tool{{Name: "a", Options: []string{"x", "y"}}}
	sent := map[string]map[string]bool{"a": {"x": true, "y": true}}
	r := Check(sent, tools)
	if err := Err(r); err != nil {
		t.Errorf("a fully covered surface failed: %v", err)
	}
	if r.Covered != 2 || r.Total != 2 {
		t.Errorf("Check = %d/%d", r.Covered, r.Total)
	}
}

func TestAnOptionMissingIsAGapNotAnExcuse(t *testing.T) {
	tools := []Tool{{Name: "read_range", Options: []string{"formatted"}}}
	r := Check(map[string]map[string]bool{"read_range": {}}, tools)
	if len(r.Gaps) != 1 || r.Gaps[0] != "read_range.formatted" {
		t.Fatalf("gaps = %v", r.Gaps)
	}
	if err := Err(r); err == nil || !strings.Contains(err.Error(), "Undrivable") {
		t.Errorf("the failure does not say how to record a genuine exception: %v", err)
	}
}

func TestEveryExemptionHasARealReason(t *testing.T) {
	for key, reason := range Undrivable {
		if len(reason) < 30 {
			t.Errorf("%s is excused with a reason too short to be one: %q", key, reason)
		}
		if !strings.Contains(key, ".") {
			t.Errorf("%q is not a tool.option key", key)
		}
	}
}

// TestAToolCanBeExcusedWholesale is the other half of Undrivable, and
// the distinction is the point: an option nobody sends is a gap in the
// driver, and a tool nobody can call is a limit of the account.
func TestAToolCanBeExcusedWholesale(t *testing.T) {
	tools := []Tool{{Name: "manage_data_source", Options: []string{"action", "project"}}}
	r := Check(map[string]map[string]bool{}, tools)
	if len(r.NoCaller) != 0 {
		t.Errorf("NoCaller = %v, want the excused tool left out of it", r.NoCaller)
	}
	if len(r.Excused) != 1 || !strings.Contains(r.Excused[0], "BigQuery") {
		t.Errorf("Excused = %v, want the reason", r.Excused)
	}
	if err := Err(r); err != nil {
		t.Errorf("an excused tool still failed the gate: %v", err)
	}
	// A tool with no reason recorded is still a failure, or the excuse
	// would be a way to make the gate quiet rather than a record.
	r = Check(map[string]map[string]bool{}, []Tool{{Name: "manage_chart", Options: []string{"action"}}})
	if err := Err(r); err == nil {
		t.Error("a tool nobody calls and nobody excused passed the gate")
	}
}
