package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/service"
)

func rangeReq(kind, action, rangeA1 string) service.RangeRequest {
	return service.RangeRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
		Range: rangeA1, Kind: kind, Action: action,
	}
}

func TestNamedRangeRoundTrip(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	req := rangeReq(service.RangeNamed, service.RangeAdd, "A1:B2")
	req.Name = "Skerry_totals"
	if _, err := svc.ManageRange(ctx, req); err != nil {
		t.Fatalf("adding a named range: %v", err)
	}
	doc := srv.Doc(sheetstest.FixtureID)
	if len(doc.NamedRanges) != 2 {
		t.Fatalf("%d named ranges after adding one", len(doc.NamedRanges))
	}

	// Adding the same name twice is a caller who meant update, and the
	// refusal says so rather than leaving two ranges with one name.
	if _, err := svc.ManageRange(ctx, req); err == nil || !strings.Contains(err.Error(), "already has a named range") {
		t.Errorf("adding a duplicate name gave %v", err)
	}

	req.Action = service.RangeUpdate
	req.Range = "A1:B3"
	if _, err := svc.ManageRange(ctx, req); err != nil {
		t.Fatalf("moving a named range: %v", err)
	}
	req.Action = service.RangeDelete
	if _, err := svc.ManageRange(ctx, req); err != nil {
		t.Fatalf("deleting a named range: %v", err)
	}
	if len(srv.Doc(sheetstest.FixtureID).NamedRanges) != 1 {
		t.Error("the named range was not deleted")
	}
	// A name that is not there is refused with the ones that are, so the
	// caller can see the typo.
	req.Name = "Nardle_totals"
	_, err := svc.ManageRange(ctx, req)
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("deleting a name that is not there gave %v", err)
	}
	if !strings.Contains(err.Error(), sheetstest.FirstSheet) {
		t.Errorf("the refusal does not list the names that exist: %q", err)
	}
}

// A protection is the real guarantee this server can offer against a
// concurrent edit, so adding and lifting one has to work — including
// lifting one over the very range it protects.
func TestProtectedRangeCanBeAddedAndLifted(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	add := rangeReq(service.RangeProtected, service.RangeAdd, "A1:B2")
	add.Description = "Skerry figures"
	res, err := svc.ManageRange(ctx, add)
	if err != nil {
		t.Fatalf("adding a protection: %v", err)
	}
	if !strings.Contains(res.Render(), "protected") {
		t.Errorf("the result does not say what happened:\n%s", res.Render())
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if len(sh.Protected) != 1 || sh.Protected[0].Description != "Skerry figures" {
		t.Fatalf("protections = %+v", sh.Protected)
	}

	update := rangeReq(service.RangeProtected, service.RangeUpdate, "A1:B2")
	update.WarningOnly = boolPtr(true)
	if _, err := svc.ManageRange(ctx, update); err != nil {
		t.Fatalf("updating a protection: %v", err)
	}
	if !srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Protected[0].WarningOnly {
		t.Error("the protection was not made warning-only")
	}
	// The protection over a range must not block the request that lifts
	// it. Anything else is a trap rather than a guard.
	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeDelete, "A1:B2")); err != nil {
		t.Fatalf("deleting a protection: %v", err)
	}
	if len(srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Protected) != 0 {
		t.Error("the protection was not removed")
	}
}

// A protection over the first sheet's heading row refuses everything
// written into it — including a named range or a validation rule.
func TestAttachingToAProtectedRangeIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := rangeReq(service.RangeValidation, service.RangeAdd, "A1:D1")
	req.Sheet = sheetstest.FirstSheet
	req.Condition = "not_blank"
	_, err := newService(t, srv).ManageRange(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("attaching to a protected range gave %v", err)
	}
	if batched(srv) {
		t.Fatal("a refused change reached the wire")
	}
}

func TestValidationRoundTrip(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	add := rangeReq(service.RangeValidation, service.RangeAdd, "A1:A2")
	add.Condition = "one_of_list"
	add.Values = []string{"Quorbin", "Vandel"}
	add.Message = "pick one"
	res, err := svc.ManageRange(ctx, add)
	if err != nil {
		t.Fatalf("adding validation: %v", err)
	}
	if !strings.Contains(res.Render(), "one of list") {
		t.Errorf("the result does not describe the rule:\n%s", res.Render())
	}
	cell := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(1, 1)
	if cell.DataValidation == nil || !cell.DataValidation.ShowCustomUI {
		t.Fatalf("validation = %+v", cell.DataValidation)
	}
	if !cell.DataValidation.Strict {
		t.Error("the rule is not strict, and a rule that only warns is one a paste walks through")
	}

	// The softer form is the caller's to ask for.
	soft := add
	soft.Strict = boolPtr(false)
	if _, err := svc.ManageRange(ctx, soft); err != nil {
		t.Fatalf("adding a warning-only rule: %v", err)
	}
	if srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(1, 1).DataValidation.Strict {
		t.Error("strict=false was ignored")
	}

	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeValidation, service.RangeDelete, "A1:A2")); err != nil {
		t.Fatalf("deleting validation: %v", err)
	}
	if srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).At(1, 1).DataValidation != nil {
		t.Error("the rule was not removed")
	}
}

func TestTableAndBandingRoundTrip(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	add := rangeReq(service.RangeTable, service.RangeAdd, "A1:B3")
	add.Name = "Trennow"
	if _, err := svc.ManageRange(ctx, add); err != nil {
		t.Fatalf("adding a table: %v", err)
	}
	rename := rangeReq(service.RangeTable, service.RangeUpdate, "A1:B3")
	rename.Name = "Bractal"
	if _, err := svc.ManageRange(ctx, rename); err != nil {
		t.Fatalf("renaming a table: %v", err)
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if len(sh.Tables) != 1 || sh.Tables[0].Name != "Bractal" {
		t.Fatalf("tables = %+v", sh.Tables)
	}

	band := rangeReq(service.RangeBanding, service.RangeAdd, "A1:B3")
	band.Color = "#d9e2f3"
	band.Header = true
	if _, err := svc.ManageRange(ctx, band); err != nil {
		t.Fatalf("adding a banding: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Bandings; len(got) != 1 {
		t.Fatalf("%d bandings", len(got))
	} else if got[0].RowProperties.HeaderColorStyle == nil {
		t.Error("a banding asked for a header and got none")
	}

	recolor := rangeReq(service.RangeBanding, service.RangeUpdate, "A1:B3")
	recolor.Color = "#f3d9e2"
	if _, err := svc.ManageRange(ctx, recolor); err != nil {
		t.Fatalf("recoloring a banding: %v", err)
	}
	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeBanding, service.RangeDelete, "A1:B3")); err != nil {
		t.Fatalf("deleting a banding: %v", err)
	}
	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeTable, service.RangeDelete, "A1:B3")); err != nil {
		t.Fatalf("deleting a table: %v", err)
	}
	sh = srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if len(sh.Tables) != 0 || len(sh.Bandings) != 0 {
		t.Errorf("tables %d, bandings %d after deleting both", len(sh.Tables), len(sh.Bandings))
	}
}

// An existing object is named by the range it covers, exactly. A
// protection over a column and one over a cell inside it are different
// objects, and a call meaning the second must not reach the first.
func TestARangeThatMatchesNothingIsRefusedWithWhatIsNearby(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	add := rangeReq(service.RangeProtected, service.RangeAdd, "A1:B2")
	if _, err := svc.ManageRange(ctx, add); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeDelete, "A1"))
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a range inside a protection gave %v", err)
	}
	if !strings.Contains(err.Error(), "A1:B2") {
		t.Errorf("the refusal does not say what is nearby: %q", err)
	}
	// And one that matches nothing at all says so without inventing a
	// neighbor.
	_, err = svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeDelete, "E5:F6"))
	if err == nil || strings.Contains(err.Error(), "nearby") {
		t.Errorf("a range far from any protection gave %v", err)
	}
}

// Two protections over the same rectangle cannot be told apart by the
// range, so the caller is asked rather than one being picked.
func TestARangeThatMatchesSeveralIsAmbiguous(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	for range 2 {
		if _, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeAdd, "A1:B2")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeDelete, "A1:B2"))
	if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous]") {
		t.Fatalf("two protections over one range gave %v", err)
	}
}

// A conditional rule is the one object the API identifies by position,
// so it takes an index, and an index past the end says how many there
// are rather than leaving the caller to guess.
func TestConditionalFormatRules(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	add := rangeReq(service.RangeRule, service.RangeAdd, "A1:B3")
	add.Condition = "text_contains"
	add.Values = []string{"Quorbin"}
	add.Color = "#d9ead3"
	add.Bold = boolPtr(true)
	res, err := svc.ManageRange(ctx, add)
	if err != nil {
		t.Fatalf("adding a rule: %v", err)
	}
	if !strings.Contains(res.Render(), "text contains Quorbin") || !strings.Contains(res.Render(), "#d9ead3") {
		t.Errorf("the result does not describe the rule:\n%s", res.Render())
	}

	update := rangeReq(service.RangeRule, service.RangeUpdate, "A1:B3")
	update.Condition = "not_blank"
	update.TextColor = "#b7472a"
	if _, err := svc.ManageRange(ctx, update); err != nil {
		t.Fatalf("updating rule 0: %v", err)
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	if len(sh.Conditional) != 1 || sh.Conditional[0].BooleanRule.Condition.Type != "NOT_BLANK" {
		t.Fatalf("rules = %+v", sh.Conditional)
	}

	past := rangeReq(service.RangeRule, service.RangeDelete, "A1:B3")
	past.Index = 4
	_, err = svc.ManageRange(ctx, past)
	if err == nil || !strings.Contains(err.Error(), "conditional format rule(s)") {
		t.Fatalf("an index past the end gave %v", err)
	}

	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeRule, service.RangeDelete, "A1:B3")); err != nil {
		t.Fatalf("deleting rule 0: %v", err)
	}
	if len(srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Conditional) != 0 {
		t.Error("the rule was not deleted")
	}
}

// A color scale is a conditional format rule of its own. It is written
// whole, read back in the spelling it was written in, and replaced whole
// by an update, the same as a rule with a condition.
func TestColorScaleRoundTrip(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	const written = "color scale: min #ffffff -> percentile 50 #ffd666 -> max #57bb8a"

	add := rangeReq(service.RangeRule, service.RangeAdd, "A1:B3")
	add.Gradient = []string{"min #ffffff", "percentile 50 #ffd666", "max #57bb8a"}
	res, err := svc.ManageRange(ctx, add)
	if err != nil {
		t.Fatalf("adding a color scale: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "conditional rule added "+written {
		t.Errorf("applied = %q", res.Applied)
	}
	rules := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Conditional
	if len(rules) != 1 || rules[0].GradientRule == nil || rules[0].BooleanRule != nil {
		t.Fatalf("rules = %+v", rules)
	}
	mid := rules[0].GradientRule.Midpoint
	if mid == nil || mid.Type != "PERCENTILE" || mid.Value != "50" || mid.ColorStyle == nil || mid.Color != nil {
		t.Errorf("the midpoint was sent as %+v; want PERCENTILE 50 in colorStyle alone", mid)
	}

	read, err := svc.Formatting(ctx, service.FormattingRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
	})
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if len(read.Rules) != 1 || read.Rules[0] != "index 0 on A1:B3: "+written {
		t.Errorf("read back as %q", read.Rules)
	}

	update := rangeReq(service.RangeRule, service.RangeUpdate, "A1:B3")
	update.Gradient = []string{"number 0 #ffffff", "max #57bb8a"}
	if _, err := svc.ManageRange(ctx, update); err != nil {
		t.Fatalf("updating the color scale: %v", err)
	}
	rules = srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Conditional
	if len(rules) != 1 || rules[0].GradientRule.Midpoint != nil || rules[0].GradientRule.Minpoint.Type != "NUMBER" {
		t.Errorf("the update did not replace the whole scale: %+v", rules[0].GradientRule)
	}

	// And a condition replaces a color scale outright.
	swap := rangeReq(service.RangeRule, service.RangeUpdate, "A1:B3")
	swap.Condition = "not_blank"
	swap.Bold = boolPtr(true)
	if _, err := svc.ManageRange(ctx, swap); err != nil {
		t.Fatalf("replacing the color scale with a condition: %v", err)
	}
	rules = srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Conditional
	if rules[0].GradientRule != nil || rules[0].BooleanRule == nil {
		t.Errorf("the rule carries %+v after a condition replaced the scale", rules[0])
	}
}

// A color scale has its colors in its points and no condition, so any
// of the arguments a condition rule takes beside it is refused rather
// than left unsent.
func TestColorScaleRefusals(t *testing.T) {
	scale := []string{"min #ffffff", "max #57bb8a"}
	for _, tc := range []struct {
		name   string
		mutate func(*service.RangeRequest)
		want   string
	}{
		{"beside a condition", func(r *service.RangeRequest) { r.Condition = "not_blank" },
			"[invalid] gradient is a color scale, a rule of its own with its colors in its points, so it does not " +
				"take condition. Pass gradient alone, or a condition and a format"},
		{"beside every format argument", func(r *service.RangeRequest) {
			r.Values = []string{"1"}
			r.Color = "#d9ead3"
			r.TextColor = "#b7472a"
			r.Bold = boolPtr(false)
		}, "does not take values, color, text_color and bold"},
		{"on another kind", func(r *service.RangeRequest) { r.Kind = service.RangeBanding; r.Color = "#d9ead3" },
			"[invalid] gradient is a color scale, which only kind conditional_format takes"},
		{"a point the parser refuses", func(r *service.RangeRequest) { r.Gradient = []string{"max #ffffff", "min #57bb8a"} },
			`[invalid] gradient point "max #ffffff" puts max first`},
		{"neither a condition nor a scale", func(r *service.RangeRequest) { r.Gradient = nil },
			"[invalid] a conditional format rule needs condition and a format to apply, or gradient for a color scale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sheetstest.Standard(t)
			req := rangeReq(service.RangeRule, service.RangeAdd, "A1:B3")
			req.Gradient = scale
			tc.mutate(&req)
			_, err := newService(t, srv).ManageRange(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to say %q", err, tc.want)
			}
			if batched(srv) {
				t.Error("a refused color scale reached the wire")
			}
		})
	}
}

func TestManageRangeRefusesWhatItCannotBuild(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	for name, mutate := range map[string]func(*service.RangeRequest){
		"an unknown kind":            func(r *service.RangeRequest) { r.Kind = "sparkline" },
		"an unknown action":          func(r *service.RangeRequest) { r.Action = "rename" },
		"a named range with no name": func(r *service.RangeRequest) { r.Kind = service.RangeNamed; r.Name = "" },
		"a table with no name":       func(r *service.RangeRequest) { r.Kind = service.RangeTable; r.Name = "" },
		"a banding with no color":    func(r *service.RangeRequest) { r.Kind = service.RangeBanding },
		"a banding with a bad color": func(r *service.RangeRequest) {
			r.Kind = service.RangeBanding
			r.Color = "puce"
		},
		"validation with no condition": func(r *service.RangeRequest) { r.Kind = service.RangeValidation },
		"a rule with no format": func(r *service.RangeRequest) {
			r.Kind = service.RangeRule
			r.Condition = "not_blank"
		},
	} {
		req := rangeReq(service.RangeValidation, service.RangeAdd, "A1:B2")
		mutate(&req)
		_, err := svc.ManageRange(context.Background(), req)
		if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if batched(srv) {
		t.Error("a request that could not be built still reached the wire")
	}
}

// An update needs something to update, and a protection with neither a
// description nor a warning flag is a call that would change nothing.
func TestUpdatingNeedsSomethingToChange(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeAdd, "A1:B2")); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ManageRange(ctx, rangeReq(service.RangeProtected, service.RangeUpdate, "A1:B2"))
	if err == nil || !strings.Contains(err.Error(), "description, warning_only") {
		t.Errorf("an empty protection update gave %v", err)
	}
	table := rangeReq(service.RangeTable, service.RangeAdd, "A1:B3")
	table.Name = "Trennow"
	if _, err := svc.ManageRange(ctx, table); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ManageRange(ctx, rangeReq(service.RangeTable, service.RangeUpdate, "A1:B3"))
	if err == nil || !strings.Contains(err.Error(), "needs name, column_types, or both") {
		t.Errorf("a table update with no name gave %v", err)
	}
}

func TestManageRangeDryRunSendsNothing(t *testing.T) {
	srv := sheetstest.Standard(t)
	req := rangeReq(service.RangeNamed, service.RangeAdd, "A1:B2")
	req.Name = "Skerry_totals"
	req.DryRun = true
	res, err := newService(t, srv).ManageRange(context.Background(), req)
	if err != nil {
		t.Fatalf("ManageRange: %v", err)
	}
	if !res.DryRun || batched(srv) {
		t.Fatal("a dry run reached the wire")
	}
	if !strings.Contains(res.Render(), "nothing was sent") {
		t.Errorf("a dry run did not say so:\n%s", res.Render())
	}
}

// A protection made over a whole sheet carries a GridRange holding only
// a sheetId, which reads back as unbounded on every side. The caller's
// range is clamped to the sheet before anything else, so compared as
// they stand no range anybody could type would ever match it — and the
// refusal formatted that unbounded rectangle as the empty string.
func TestAnUnboundedStoredRangeCanStillBeNamed(t *testing.T) {
	srv := sheetstest.Standard(t)
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	sh.Protected = append(sh.Protected, &gsheets.ProtectedRange{
		ProtectedRangeID: 91,
		// Only the sheet id, as the Sheets interface writes it.
		Range:       &gsheets.GridRange{SheetID: sh.Props.SheetID},
		Description: "the whole sheet",
	})
	svc := newService(t, srv)
	// The whole sheet, named the way a caller would name it.
	if _, err := svc.ManageRange(context.Background(), rangeReq(service.RangeProtected, service.RangeDelete, "")); err != nil {
		t.Fatalf("deleting a whole-sheet protection: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Protected; len(got) != 0 {
		t.Errorf("%d protections left", len(got))
	}
}

// And the refusal that names what is nearby has to name it, rather than
// formatting an unbounded rectangle as nothing at all.
func TestTheNearbyRefusalNamesARange(t *testing.T) {
	srv := sheetstest.Standard(t)
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	sh.Protected = append(sh.Protected, &gsheets.ProtectedRange{
		ProtectedRangeID: 92,
		Range:            &gsheets.GridRange{SheetID: sh.Props.SheetID},
	})
	_, err := newService(t, srv).ManageRange(context.Background(),
		rangeReq(service.RangeProtected, service.RangeDelete, "A1:B2"))
	if err == nil {
		t.Fatal("a cell range matched a whole-sheet protection exactly")
	}
	if strings.Contains(err.Error(), "cover , ") || strings.HasSuffix(err.Error(), "cover ") {
		t.Errorf("the refusal names an empty range: %q", err)
	}
	if !strings.Contains(err.Error(), "A1:H50") {
		t.Errorf("the refusal does not name the protection that is there: %q", err)
	}
}

// Deleting a table takes the conditional format rules over its range,
// verified live: one rule before, none after, and nothing in the reply
// says so. Adding a table leaves them, so it is the delete that takes
// them.
func TestDeletingATableThatTakesRulesIsRefused(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()

	table := rangeReq(service.RangeTable, service.RangeAdd, "A1:B3")
	table.Name = "Trennow"
	if _, err := svc.ManageRange(ctx, table); err != nil {
		t.Fatal(err)
	}
	rule := rangeReq(service.RangeRule, service.RangeAdd, "A1:B3")
	rule.Condition = "not_blank"
	rule.Color = "#d9ead3"
	if _, err := svc.ManageRange(ctx, rule); err != nil {
		t.Fatal(err)
	}

	remove := rangeReq(service.RangeTable, service.RangeDelete, "A1:B3")
	_, err := svc.ManageRange(ctx, remove)
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("deleting a table over a rule gave %v", err)
	}
	if !strings.Contains(err.Error(), "not blank") {
		t.Errorf("the refusal does not say which rule would go: %q", err)
	}

	remove.Overwrite = true
	if _, err := svc.ManageRange(ctx, remove); err != nil {
		t.Fatalf("overwrite did not allow it: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Tables; len(got) != 0 {
		t.Errorf("%d tables left", len(got))
	}
}

// A table with no rules over it is deleted without ceremony: the guard
// is about what would be lost, not about the act.
func TestDeletingAPlainTableIsNotHeldBack(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	ctx := context.Background()
	table := rangeReq(service.RangeTable, service.RangeAdd, "A1:B3")
	table.Name = "Trennow"
	if _, err := svc.ManageRange(ctx, table); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ManageRange(ctx, rangeReq(service.RangeTable, service.RangeDelete, "A1:B3")); err != nil {
		t.Errorf("deleting a table with no rules over it was refused: %v", err)
	}
}

// TestATableAddGoogleFailedIsSettledByARead is an add answered HTTP 500,
// which says nothing of whether it ran. Spike T Q11 saw a run of them make
// no table. A table has a name and a range, so a read afterwards says
// which: nothing added, or added after all. Anything else, and a read
// that fails too, stay [ambiguous_outcome].
func TestATableAddGoogleFailedIsSettledByARead(t *testing.T) {
	const internal = `{"error":{"code":500,"message":"Internal error encountered.","status":"INTERNAL"}}`
	const ambiguous = "[ambiguous_outcome] ambiguous outcome: spreadsheets.batchUpdate may have been applied; read " +
		"the spreadsheet before repeating it, since a repeat could apply it twice (google api " +
		"spreadsheets.batchUpdate: HTTP 500 INTERNAL: Internal error encountered.)"
	for _, tc := range []struct {
		name    string
		action  string
		applied bool
		getFail bool
		want    string
	}{
		{"nothing added", service.RangeAdd, false, false,
			`[unavailable] Google answered HTTP 500 INTERNAL (Internal error encountered). A read afterwards finds ` +
				`no table called "Trennow" on A1:B3, so nothing was added and the call can be repeated. Once Google ` +
				`has failed a table add this way, it has been seen to fail every later one in the same spreadsheet, ` +
				`for a reason not known`},
		{"added after all", service.RangeAdd, true, false,
			"Done: 1 change(s) to 'Ürväl'!A1:B3.\n  table added — Trennow\n\n" +
				"Google answered HTTP 500 INTERNAL (Internal error encountered), and a read afterwards found the change made.\n"},
		{"the read fails too", service.RangeAdd, false, true, ambiguous},
		{"a rename", service.RangeUpdate, false, false, ambiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sheetstest.Standard(t)
			if tc.action == service.RangeUpdate {
				seedTypedTable(srv)
			}
			svc := newService(t, srv)
			ctx := context.Background()
			if _, err := svc.Card(ctx, sheetstest.FixtureID); err != nil {
				t.Fatal(err)
			}
			srv.FailOnce("spreadsheets.batchUpdate", sheetstest.Failure{Status: 500, Body: internal, Applied: tc.applied})
			if tc.getFail {
				// The add's read of the header row goes through; the read
				// after the 500 does not.
				srv.FailOnce("spreadsheets.get", sheetstest.Failure{Pass: true})
				srv.Fail("spreadsheets.get", sheetstest.Failure{Status: 500})
			}
			req := rangeReq(service.RangeTable, tc.action, "A1:B3")
			if tc.action == service.RangeUpdate {
				req.Range = "A1:D3"
			}
			req.Name = "Trennow"
			res, err := svc.ManageRange(ctx, req)
			got := ""
			if err != nil {
				got = err.Error()
			} else {
				got = res.Summary
			}
			if got != tc.want {
				t.Errorf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// typedSheet gives the second sheet a four-column header row, A1:D1, for
// a table over A1:D3. "ID" is a heading that also reads as a column
// letter, far outside the table.
func typedSheet(srv *sheetstest.Server) *sheetstest.Sheet {
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	sh.Set(1, 3, sheetstest.Str("Status"))
	sh.Set(1, 4, sheetstest.Str("ID"))
	return sh
}

// sentColumns is the column array the last batchUpdate carried, as JSON,
// and the mask it went under.
func sentColumns(t *testing.T, srv *sheetstest.Server) (string, string) {
	t.Helper()
	var body string
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			body = c.Body
		}
	}
	var req gsheets.BatchUpdateSpreadsheetRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil || len(req.Requests) != 1 {
		t.Fatalf("no single request was sent: %s", body)
	}
	var table *gsheets.Table
	var fields string
	switch r := req.Requests[0]; {
	case r.AddTable != nil:
		table = r.AddTable.Table
	case r.UpdateTable != nil:
		table, fields = r.UpdateTable.Table, r.UpdateTable.Fields
	default:
		t.Fatalf("the request is not a table request: %s", body)
	}
	columns, _ := json.Marshal(table.ColumnProperties)
	return string(columns), fields
}

// TestTableAddTypesTheNamedColumns is column_types on add: a column by
// letter or by heading, counted from the table's first column, and only
// the columns named, each with its header's text as its name.
func TestTableAddTypesTheNamedColumns(t *testing.T) {
	srv := sheetstest.Standard(t)
	typedSheet(srv)
	svc := newService(t, srv)
	ctx := context.Background()

	add := rangeReq(service.RangeTable, service.RangeAdd, "A1:D3")
	add.Name = "Trennow"
	add.ColumnTypes = []string{"Bractal currency", "C dropdown: Open, In progress, Done", "ID number"}
	res, err := svc.ManageRange(ctx, add)
	if err != nil {
		t.Fatalf("adding a typed table: %v", err)
	}
	columns, _ := sentColumns(t, srv)
	const want = `[{"columnIndex":1,"columnName":"Bractal","columnType":"CURRENCY"},` +
		`{"columnIndex":2,"columnName":"Status","columnType":"DROPDOWN","dataValidationRule":{"condition":{"type":"ONE_OF_LIST",` +
		`"values":[{"userEnteredValue":"Open"},{"userEnteredValue":"In progress"},{"userEnteredValue":"Done"}]}}},` +
		`{"columnIndex":3,"columnName":"ID","columnType":"DOUBLE"}]`
	if columns != want {
		t.Errorf("columnProperties =\n%s\nwant\n%s", columns, want)
	}
	wantApplied := []string{
		"table added Trennow",
		"column typed Bractal currency",
		"column typed C dropdown (Open, In progress, Done)",
		"column typed ID number",
	}
	if strings.Join(res.Applied, "\n") != strings.Join(wantApplied, "\n") {
		t.Errorf("applied = %q", res.Applied)
	}
	card, err := svc.Card(ctx, sheetstest.FixtureID)
	if err != nil {
		t.Fatal(err)
	}
	const line = "Trennow -> 'Ürväl'!A1:D3 (Trennow, Bractal currency, Status dropdown (Open, In progress, Done) and ID number)"
	if !strings.Contains(card.Card, line) {
		t.Errorf("the card does not say %q:\n%s", line, card.Card)
	}
}

// TestTableColumnsCountFromTheTablesFirstColumn is the index the API
// takes: "relative to its position in the table and is not necessarily
// the same as the column index in the sheet".
func TestTableColumnsCountFromTheTablesFirstColumn(t *testing.T) {
	srv := sheetstest.Standard(t)
	typedSheet(srv)
	add := rangeReq(service.RangeTable, service.RangeAdd, "B1:D3")
	add.Name = "Trennow"
	add.ColumnTypes = []string{"C date", "D boolean"}
	if _, err := newService(t, srv).ManageRange(context.Background(), add); err != nil {
		t.Fatalf("adding a typed table: %v", err)
	}
	columns, _ := sentColumns(t, srv)
	const want = `[{"columnIndex":1,"columnName":"Status","columnType":"DATE"},` +
		`{"columnIndex":2,"columnName":"ID","columnType":"BOOLEAN"}]`
	if columns != want {
		t.Errorf("columnProperties = %s, want %s", columns, want)
	}
}

// TestATableAddOverAFormulaHeaderIsRefused is spike T Q10: Google refuses
// a table over a formula in its header row, typed or not, with
// "Formulas are not supported in a table header row." A plain add is
// refused here before it is sent, a dry run included, naming every cell.
func TestATableAddOverAFormulaHeaderIsRefused(t *testing.T) {
	const want = `[invalid] A1, C1 in the header row of A1:D3 hold a formula, and Google takes no table over ` +
		`one: "Formulas are not supported in a table header row." Replace them with the text they show first`
	for _, dryRun := range []bool{false, true} {
		srv := sheetstest.Standard(t)
		typedSheet(srv).Set(1, 1, sheetstest.Formula(`="Tren"&"now"`, 0, "Trennow")).
			Set(1, 3, sheetstest.Formula(`="Sta"&"tus"`, 0, "Status"))
		add := rangeReq(service.RangeTable, service.RangeAdd, "A1:D3")
		add.Name = "Trennow"
		add.DryRun = dryRun
		_, err := newService(t, srv).ManageRange(context.Background(), add)
		if err == nil || err.Error() != want {
			t.Errorf("dry_run=%v: error =\n%v\nwant\n%s", dryRun, err, want)
		}
		if batched(srv) {
			t.Errorf("dry_run=%v: a refused add reached the wire", dryRun)
		}
	}
}

// TestTableAddKeepsTheHeaderRow is the header row after a typed add.
// Google writes "Column 1", "Column 2" into the header cell of a typed
// column sent with no name (spike T Q1), so the add names each one after
// its header, and the header reads as it did.
func TestTableAddKeepsTheHeaderRow(t *testing.T) {
	srv := sheetstest.Standard(t)
	typedSheet(srv)
	add := rangeReq(service.RangeTable, service.RangeAdd, "A1:D3")
	add.Name = "Trennow"
	add.ColumnTypes = []string{"Bractal currency", "D dropdown: Open, Done"}
	if _, err := newService(t, srv).ManageRange(context.Background(), add); err != nil {
		t.Fatalf("adding a typed table: %v", err)
	}
	sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
	var got []string
	for col := 1; col <= 4; col++ {
		got = append(got, sh.At(1, col).FormattedValue)
	}
	if strings.Join(got, ", ") != "Trennow, Bractal, Status, ID" {
		t.Errorf("the header row after the add reads %q", got)
	}
}

// TestTableAddOverAnEmptyHeaderSaysGoogleNamesIt is a typed column whose
// header cell is empty. There is no name to send, so Google writes one of
// its own into the cell; nothing is lost, and the result says so.
func TestTableAddOverAnEmptyHeaderSaysGoogleNamesIt(t *testing.T) {
	srv := sheetstest.Standard(t)
	typedSheet(srv)
	add := rangeReq(service.RangeTable, service.RangeAdd, "A1:E3")
	add.Name = "Trennow"
	add.ColumnTypes = []string{"E date"}
	res, err := newService(t, srv).ManageRange(context.Background(), add)
	if err != nil {
		t.Fatalf("adding a typed table: %v", err)
	}
	if columns, _ := sentColumns(t, srv); columns != `[{"columnIndex":4,"columnType":"DATE"}]` {
		t.Errorf("columnProperties = %s", columns)
	}
	want := []string{"table added Trennow", "column typed E date",
		`empty header E1: Google writes a name such as "Column 1" into it`}
	if strings.Join(res.Applied, "\n") != strings.Join(want, "\n") {
		t.Errorf("applied = %q", res.Applied)
	}
}

// boldStart is "Status" with its first two letters bold, as Google reads
// rich text back: a run with the bold, then one with an empty format.
func boldStart() *gsheets.CellData {
	c := sheetstest.Str("Status")
	c.TextFormatRuns = []json.RawMessage{
		json.RawMessage(`{"format":{"bold":true}}`), json.RawMessage(`{"startIndex":2,"format":{}}`),
	}
	return c
}

// TestTableAddIsRefusedOverAHeaderANameWouldReplace is a typed column
// whose header cell holds a smart chip or rich text. The add sends the
// text the cell shows as the column's name, and Google writes it into the
// cell as plain text, which loses the chip or the formatting. A column
// the add does not type is sent no name, so what is in its header is not
// held against it. A formula is refused in any column: Google takes no
// table over one (spike T Q10).
func TestTableAddIsRefusedOverAHeaderANameWouldReplace(t *testing.T) {
	chip := func() *gsheets.CellData {
		c := sheetstest.Str("Jane Doe")
		c.ChipRuns = []gsheets.ChipRun{{Chip: &gsheets.Chip{
			PersonProperties: &gsheets.PersonProperties{Email: "janedoe@example.test"},
		}}}
		return c
	}
	for _, tc := range []struct {
		name  string
		cell  *gsheets.CellData
		types []string
		want  string
	}{
		{"a formula", sheetstest.Formula(`="Sta"&"tus"`, 0, "Status"), []string{"Status date"},
			`[invalid] C1 in the header row of A1:D3 holds a formula, and Google takes no table over one: ` +
				`"Formulas are not supported in a table header row." Replace it with the text it shows first`},
		{"a formula in a column not typed", sheetstest.Formula(`="Sta"&"tus"`, 0, "Status"), []string{"B date"},
			`[invalid] C1 in the header row of A1:D3 holds a formula, and Google takes no table over one: ` +
				`"Formulas are not supported in a table header row." Replace it with the text it shows first`},
		{"a chip", chip(), []string{"C date", "D number"},
			"[blocked] in the header row of the table on A1:D3, C1 holds a smart chip (a person or a file link). " +
				"Typing a column sends its header's text as the column's name, and Google writes each name into " +
				"its header cell as plain text, so the chip would be lost. Make it plain text first, or set the " +
				"type in Sheets"},
		{"a chip in a column not typed", chip(), []string{"B date"}, ""},
		{"rich text", boldStart(), []string{"C date"},
			"[blocked] in the header row of the table on A1:D3, C1 holds text with part of it formatted on its own, " +
				"such as a bold word. Typing a column sends its header's text as the column's name, and Google writes " +
				"each name into its header cell as plain text, so that formatting would be lost. Make it plain text " +
				"first, or set the type in Sheets"},
		{"rich text in a column not typed", boldStart(), []string{"B date"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sheetstest.Standard(t)
			typedSheet(srv).Set(1, 3, tc.cell)
			add := rangeReq(service.RangeTable, service.RangeAdd, "A1:D3")
			add.Name = "Trennow"
			add.ColumnTypes = tc.types
			_, err := newService(t, srv).ManageRange(context.Background(), add)
			if tc.want == "" {
				if err != nil {
					t.Errorf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Errorf("error =\n%v\nwant\n%s", err, tc.want)
			}
			if batched(srv) {
				t.Error("a refused add reached the wire")
			}
		})
	}
}

// seedTypedTable puts a table over A1:D3 on the second sheet with a type
// on every column, a dropdown's list and a chip among them.
func seedTypedTable(srv *sheetstest.Server) {
	sh := typedSheet(srv)
	sh.Tables = []*gsheets.Table{{
		TableID: "AAAAtable1", Name: "Trennow",
		Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 3}.GridRange(1837),
		ColumnProperties: []*gsheets.TableColumn{
			{ColumnIndex: 0, ColumnName: "Trennow", ColumnType: gsheets.ColumnText},
			{ColumnIndex: 1, ColumnName: "Bractal", ColumnType: gsheets.ColumnDouble},
			{ColumnIndex: 2, ColumnName: "Status", ColumnType: gsheets.ColumnDropdown,
				DataValidationRule: &gsheets.TableColumnDataValidationRule{Condition: &gsheets.BooleanCondition{
					Type: "ONE_OF_LIST", Values: []*gsheets.ConditionValue{{UserEnteredValue: "Open"}, {UserEnteredValue: "Done"}},
				}}},
			{ColumnIndex: 3, ColumnName: "ID", ColumnType: gsheets.ColumnPeople},
		},
	}}
}

// TestTableUpdateRetypesOneColumnAndKeepsTheRest is the round trip.
// Google replaces the whole list (spike T Q3b), and so does the fake, so
// every column goes back as it was — the dropdown with its list, the
// chip as a chip — and each with the name it was read with, so no header
// is left blank. The card after it reads what the fake kept.
func TestTableUpdateRetypesOneColumnAndKeepsTheRest(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	svc := newService(t, srv)
	ctx := context.Background()

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"B currency"}
	res, err := svc.ManageRange(ctx, update)
	if err != nil {
		t.Fatalf("retyping a column: %v", err)
	}
	columns, fields := sentColumns(t, srv)
	const want = `[{"columnName":"Trennow","columnType":"TEXT"},` +
		`{"columnIndex":1,"columnName":"Bractal","columnType":"CURRENCY"},` +
		`{"columnIndex":2,"columnName":"Status","columnType":"DROPDOWN","dataValidationRule":{"condition":{"type":"ONE_OF_LIST",` +
		`"values":[{"userEnteredValue":"Open"},{"userEnteredValue":"Done"}]}}},` +
		`{"columnIndex":3,"columnName":"ID","columnType":"PEOPLE_CHIP"}]`
	if columns != want || fields != "columnProperties" {
		t.Errorf("sent %s under %q\nwant %s under \"columnProperties\"", columns, fields, want)
	}
	if strings.Join(res.Applied, "\n") != "column typed B currency" {
		t.Errorf("applied = %q", res.Applied)
	}
	card, err := svc.Card(ctx, sheetstest.FixtureID)
	if err != nil {
		t.Fatal(err)
	}
	const line = "(Trennow text, Bractal currency, Status dropdown (Open, Done) and ID people_chip)"
	if !strings.Contains(card.Card, line) {
		t.Errorf("the card does not say %q:\n%s", line, card.Card)
	}
}

// TestTableUpdateReadsTheColumnsFresh is why the update does not take
// the array from the cached card: a dropdown somebody added since the
// card was read would be sent back without it, and lost. The name a
// retyped column goes back with is the one this read gave it, which is
// the name Google holds, even where its header cell reads otherwise.
func TestTableUpdateReadsTheColumnsFresh(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	svc := newService(t, srv)
	ctx := context.Background()
	if _, err := svc.Card(ctx, sheetstest.FixtureID); err != nil {
		t.Fatal(err)
	}
	// Somebody else, inside the card's cache window, retypes column A
	// and renames column B.
	table := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Tables[0]
	table.ColumnProperties[0].ColumnType = gsheets.ColumnDate
	table.ColumnProperties[1].ColumnName = "Quorbin"

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"B percent"}
	if _, err := svc.ManageRange(ctx, update); err != nil {
		t.Fatalf("retyping a column: %v", err)
	}
	columns, _ := sentColumns(t, srv)
	if !strings.HasPrefix(columns, `[{"columnName":"Trennow","columnType":"DATE"},`+
		`{"columnIndex":1,"columnName":"Quorbin","columnType":"PERCENT"}`) {
		t.Errorf("the update sent the cached columns: %s", columns)
	}
}

// TestTableUpdateRenamesAndRetypesInOneRequest is both at once: one
// request, one mask naming both.
func TestTableUpdateRenamesAndRetypesInOneRequest(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	svc := newService(t, srv)
	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.Name = "Bractal"
	update.ColumnTypes = []string{"Trennow date_time"}
	if _, err := svc.ManageRange(context.Background(), update); err != nil {
		t.Fatalf("renaming and retyping: %v", err)
	}
	columns, fields := sentColumns(t, srv)
	if fields != "name,columnProperties" || !strings.HasPrefix(columns, `[{"columnName":"Trennow","columnType":"DATE_TIME"},`) {
		t.Errorf("sent %s under %q", columns, fields)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Tables[0].Name; got != "Bractal" {
		t.Errorf("the table is called %q", got)
	}
}

// TestTableUpdateNamesAColumnTheReadLeftOutByItsHeader is a read with
// no entry for some columns. Whether Google ever returns such an array
// is unverified (§18); if it does, the update still sends an entry for
// every column, each a column the read left out named by its header
// cell's text from the same read and given no type, so no header can be
// left blank.
func TestTableUpdateNamesAColumnTheReadLeftOutByItsHeader(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	table := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Tables[0]
	table.ColumnProperties = table.ColumnProperties[2:3]

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"D date"}
	if _, err := newService(t, srv).ManageRange(context.Background(), update); err != nil {
		t.Fatalf("retyping a column: %v", err)
	}
	columns, _ := sentColumns(t, srv)
	const want = `[{"columnName":"Trennow"},{"columnIndex":1,"columnName":"Bractal"},` +
		`{"columnIndex":2,"columnName":"Status","columnType":"DROPDOWN","dataValidationRule":{"condition":` +
		`{"type":"ONE_OF_LIST","values":[{"userEnteredValue":"Open"},{"userEnteredValue":"Done"}]}}},` +
		`{"columnIndex":3,"columnName":"ID","columnType":"DATE"}]`
	if columns != want {
		t.Errorf("columnProperties =\n%s\nwant\n%s", columns, want)
	}
}

// TestTableUpdateIsRefusedOverAFormulaInTheHeader is a header a name
// sent back would change: written into the cell, the name replaces the
// formula with the text it shows. Google does not keep a formula written
// into a table's header (spike T Q7), and takes no table over one (Q10),
// so this is a header made some other way; the fake is seeded with it
// directly. A rename alone sends no column, so it is not held back.
func TestTableUpdateIsRefusedOverAFormulaInTheHeader(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 3, sheetstest.Formula(`="Sta"&"tus"`, 0, "Status"))
	svc := newService(t, srv)
	ctx := context.Background()

	for _, dryRun := range []bool{false, true} {
		update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
		update.ColumnTypes = []string{"B currency"}
		update.DryRun = dryRun
		_, err := svc.ManageRange(ctx, update)
		const want = "[blocked] in the header row of the table on A1:D3, C1 holds a formula. Changing a column type " +
			"sends every column's name back, and Google writes each name into its header cell as plain text, so " +
			"the formula would be lost. Make it plain text first, or set the type in Sheets"
		if err == nil || err.Error() != want {
			t.Errorf("dry_run=%v: error =\n%v\nwant\n%s", dryRun, err, want)
		}
	}
	if batched(srv) {
		t.Fatal("a refused update reached the wire")
	}
	rename := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	rename.Name = "Bractal"
	if _, err := svc.ManageRange(ctx, rename); err != nil {
		t.Errorf("a rename over a formula header was refused: %v", err)
	}
}

// TestTableUpdateIsRefusedOverAChipInTheHeader is the other header a
// name sent back could change: written into the cell, the name would
// replace a person or a file chip with the text it shows.
func TestTableUpdateIsRefusedOverAChipInTheHeader(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	chip := sheetstest.Str("Jane Doe")
	chip.ChipRuns = []gsheets.ChipRun{{Chip: &gsheets.Chip{
		PersonProperties: &gsheets.PersonProperties{Email: "janedoe@example.test"},
	}}}
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 4, chip)

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"B currency"}
	_, err := newService(t, srv).ManageRange(context.Background(), update)
	const want = "[blocked] in the header row of the table on A1:D3, D1 holds a smart chip (a person or a file " +
		"link). Changing a column type sends every column's name back, and Google writes each name into its " +
		"header cell as plain text, so the chip would be lost. Make it plain text first, or set the type in Sheets"
	if err == nil || err.Error() != want {
		t.Errorf("error =\n%v\nwant\n%s", err, want)
	}
	if batched(srv) {
		t.Fatal("a refused update reached the wire")
	}
}

// TestTableUpdateIsRefusedOverRichTextInTheHeader is spike T Q7: an
// update sending "Flag" back as read dropped the runs that made two of
// its letters bold. Runs whose formats are all empty format nothing, and
// are not held back.
func TestTableUpdateIsRefusedOverRichTextInTheHeader(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 3, boldStart())
	svc := newService(t, srv)
	ctx := context.Background()

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"B currency"}
	_, err := svc.ManageRange(ctx, update)
	const want = "[blocked] in the header row of the table on A1:D3, C1 holds text with part of it formatted on its " +
		"own, such as a bold word. Changing a column type sends every column's name back, and Google writes each " +
		"name into its header cell as plain text, so that formatting would be lost. Make it plain text first, or " +
		"set the type in Sheets"
	if err == nil || err.Error() != want {
		t.Errorf("error =\n%v\nwant\n%s", err, want)
	}
	if batched(srv) {
		t.Fatal("a refused update reached the wire")
	}

	plain := sheetstest.Str("Status")
	plain.TextFormatRuns = []json.RawMessage{json.RawMessage(`{"format":{}}`)}
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 3, plain)
	if _, err := svc.ManageRange(ctx, update); err != nil {
		t.Errorf("a header whose runs format nothing was refused: %v", err)
	}
}

// TestABooleanColumnIsRefusedOverOtherValues is a checkbox column over
// cells that are not TRUE or FALSE values, on add and on update alike.
// Google turned a word, the text "TRUE" and an empty cell into FALSE
// (spike T Q5). A number is refused too, since nothing says it is kept.
// A TRUE or FALSE value and an empty cell are not held back.
func TestABooleanColumnIsRefusedOverOtherValues(t *testing.T) {
	const refusal = " something other than a TRUE or FALSE value, and Google turns such a cell into FALSE when " +
		"its column is typed boolean, text reading TRUE included, so what is there would be lost. Write TRUE or " +
		"FALSE with input typed, which stores a true or false value rather than text, or clear them, first; an " +
		"empty cell becomes FALSE"
	for _, tc := range []struct {
		name, column, want string
	}{
		{"numbers", "Bractal boolean", "[blocked] B2, B3 hold" + refusal},
		{"a word and the text TRUE", "Status boolean", "[blocked] C2, C3 hold" + refusal},
	} {
		for _, action := range []string{service.RangeAdd, service.RangeUpdate} {
			srv := sheetstest.Standard(t)
			if action == service.RangeAdd {
				typedSheet(srv)
			} else {
				seedTypedTable(srv)
			}
			srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).
				Set(2, 3, sheetstest.Str("maybe")).Set(3, 3, sheetstest.Str("TRUE"))
			req := rangeReq(service.RangeTable, action, "A1:D3")
			req.Name = "Trennow"
			req.ColumnTypes = []string{tc.column}
			_, err := newService(t, srv).ManageRange(context.Background(), req)
			if err == nil || err.Error() != tc.want {
				t.Errorf("%s, %s: error =\n%v\nwant\n%s", tc.name, action, err, tc.want)
			}
			if batched(srv) {
				t.Fatalf("%s, %s: a refused boolean column reached the wire", tc.name, action)
			}
		}
	}

	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(2, 4, sheetstest.Bool(true)).Set(3, 4, sheetstest.Bool(false))
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(2, 3, sheetstest.Bool(true))
	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"C boolean", "D boolean"}
	if _, err := newService(t, srv).ManageRange(context.Background(), update); err != nil {
		t.Errorf("a boolean column over TRUE, FALSE and an empty cell was refused: %v", err)
	}
}

// TestADropdownColumnIsRefusedOverCellsWithTheirOwnRule is spike T Q1: an
// add typing a column dropdown over cells with a list of their own read
// them back with no rule. On add and on update, a dry run included, the
// call is refused before anything is sent, naming the cells. A cell with
// a rule and no value counts.
func TestADropdownColumnIsRefusedOverCellsWithTheirOwnRule(t *testing.T) {
	for _, tc := range []struct {
		action string
		ruled  []int
		want   string
	}{
		{service.RangeAdd, []int{2, 3}, "[blocked] C2, C3 have data validation rules of their own, and the " +
			"table's dropdown would replace them: Google drops a cell's own rule when its column is typed " +
			"dropdown, and says nothing. To use the table's list, remove the rule from those cells first, with " +
			"kind data_validation and action delete"},
		{service.RangeUpdate, []int{3}, "[blocked] C3 has a data validation rule of its own, and the table's " +
			"dropdown would replace it: Google drops a cell's own rule when its column is typed dropdown, and " +
			"says nothing. To use the table's list, remove the rule from that cell first, with kind " +
			"data_validation and action delete"},
	} {
		for _, dryRun := range []bool{false, true} {
			srv := sheetstest.Standard(t)
			if tc.action == service.RangeAdd {
				typedSheet(srv)
			} else {
				seedTypedTable(srv)
			}
			sh := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet)
			sh.Set(2, 3, sheetstest.Str("Open"))
			for _, row := range tc.ruled {
				if sh.At(row, 3) == nil {
					sh.Set(row, 3, &gsheets.CellData{})
				}
				sheetstest.WithValidation(sh.At(row, 3), "Open", "Closed")
			}
			req := rangeReq(service.RangeTable, tc.action, "A1:D3")
			req.Name = "Trennow"
			req.DryRun = dryRun
			req.ColumnTypes = []string{"Status dropdown: Open, Done"}
			_, err := newService(t, srv).ManageRange(context.Background(), req)
			if err == nil || err.Error() != tc.want {
				t.Errorf("%s, dry_run=%v: error =\n%v\nwant\n%s", tc.action, dryRun, err, tc.want)
			}
			if batched(srv) {
				t.Fatalf("%s, dry_run=%v: a refused dropdown column reached the wire", tc.action, dryRun)
			}
		}
	}
}

// TestBooleanAndDropdownColumnsAreReadInOneRequest is what the two
// refusals above cost: one read after the header, of the cells under
// every column typed boolean or dropdown and of nothing else. A rule on a
// cell of a column typed otherwise holds nothing back.
func TestBooleanAndDropdownColumnsAreReadInOneRequest(t *testing.T) {
	srv := sheetstest.Standard(t)
	typedSheet(srv).Set(2, 4, sheetstest.Bool(true)).
		Set(2, 2, sheetstest.WithValidation(sheetstest.Num(42, "42"), "42"))
	add := rangeReq(service.RangeTable, service.RangeAdd, "A1:D3")
	add.Name = "Trennow"
	add.ColumnTypes = []string{"Bractal currency", "Status dropdown: Open, Done", "ID boolean"}
	if _, err := newService(t, srv).ManageRange(context.Background(), add); err != nil {
		t.Fatalf("adding a typed table: %v", err)
	}
	var reads []string
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.get" && c.Query.Get("includeGridData") == "true" {
			reads = append(reads, strings.Join(c.Query["ranges"], " and "))
		}
	}
	const want = "'Ürväl'!A1:D1 | 'Ürväl'!C2:C3 and 'Ürväl'!D2:D3"
	if got := strings.Join(reads, " | "); got != want {
		t.Errorf("the cell reads were\n%s\nwant\n%s", got, want)
	}
}

// TestTableUpdateIsRefusedWhenTheTableMoved is the header row the
// formula check reads: the caller's first row. A table moved since the
// card was read has another, so the update stops and says where it is.
func TestTableUpdateIsRefusedWhenTheTableMoved(t *testing.T) {
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	svc := newService(t, srv)
	ctx := context.Background()
	if _, err := svc.Card(ctx, sheetstest.FixtureID); err != nil {
		t.Fatal(err)
	}
	// Somebody moves it down a row inside the card's cache window.
	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Tables[0].Range =
		a1.Rect{FirstCol: 1, FirstRow: 2, LastCol: 4, LastRow: 4}.GridRange(1837)

	update := rangeReq(service.RangeTable, service.RangeUpdate, "A1:D3")
	update.ColumnTypes = []string{"B currency"}
	_, err := svc.ManageRange(ctx, update)
	const want = "[not_found] the table on A1:D3 covers A2:D4 since this call began; name it by that range"
	if err == nil || err.Error() != want {
		t.Errorf("error =\n%v\nwant\n%s", err, want)
	}
	if batched(srv) {
		t.Error("an update of a moved table reached the wire")
	}
}

func TestTableColumnTypeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   string
		action string
		types  []string
		want   string
	}{
		{"another kind", service.RangeBanding, service.RangeAdd, []string{"B date"},
			"[invalid] column_types types a table's columns, which only kind table takes, on add or update"},
		{"a delete", service.RangeTable, service.RangeDelete, []string{"B date"},
			"[invalid] column_types types a table's columns, which only kind table takes, on add or update"},
		{"a letter outside the table", service.RangeTable, service.RangeUpdate, []string{"F date"},
			"[invalid] column F is outside the table A1:D3"},
		{"a heading the table does not have", service.RangeTable, service.RangeUpdate, []string{"Owner date"},
			`[not_found] no column "Owner" in the table; its first row holds "bractal", "id", "status" and "trennow"`},
		{"one column twice", service.RangeTable, service.RangeUpdate, []string{"B date", "Bractal number"},
			`[invalid] column_types names one column twice, as "B" and "Bractal"`},
		{"a chip", service.RangeTable, service.RangeUpdate, []string{"ID people_chip"},
			`[invalid] column type "ID people_chip" asks for people_chip, a smart chip column, which this server ` +
				`shows and does not set`},
		{"a dropdown with no options", service.RangeTable, service.RangeUpdate, []string{"Status dropdown"},
			`[invalid] column type "Status dropdown" needs its options after a colon, such as ` +
				`"Status dropdown: Open, In progress, Done"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := sheetstest.Standard(t)
			seedTypedTable(srv)
			req := rangeReq(tc.kind, tc.action, "A1:D3")
			req.ColumnTypes = tc.types
			req.Color = "#d9e2f3"
			_, err := newService(t, srv).ManageRange(context.Background(), req)
			if err == nil || err.Error() != tc.want {
				t.Errorf("error =\n%v\nwant\n%s", err, tc.want)
			}
			if batched(srv) {
				t.Error("a refused request reached the wire")
			}
		})
	}
}
