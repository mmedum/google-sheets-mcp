package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/service"
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
	band.Colour = "#d9e2f3"
	band.Header = true
	if _, err := svc.ManageRange(ctx, band); err != nil {
		t.Fatalf("adding a banding: %v", err)
	}
	if got := srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Bandings; len(got) != 1 {
		t.Fatalf("%d bandings", len(got))
	} else if got[0].RowProperties.HeaderColorStyle == nil {
		t.Error("a banding asked for a header and got none")
	}

	recolour := rangeReq(service.RangeBanding, service.RangeUpdate, "A1:B3")
	recolour.Colour = "#f3d9e2"
	if _, err := svc.ManageRange(ctx, recolour); err != nil {
		t.Fatalf("recolouring a banding: %v", err)
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
	// neighbour.
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
	add.Colour = "#d9ead3"
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
	update.TextColour = "#b7472a"
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

func TestManageRangeRefusesWhatItCannotBuild(t *testing.T) {
	srv := sheetstest.Standard(t)
	svc := newService(t, srv)
	for name, mutate := range map[string]func(*service.RangeRequest){
		"an unknown kind":            func(r *service.RangeRequest) { r.Kind = "sparkline" },
		"an unknown action":          func(r *service.RangeRequest) { r.Action = "rename" },
		"a named range with no name": func(r *service.RangeRequest) { r.Kind = service.RangeNamed; r.Name = "" },
		"a table with no name":       func(r *service.RangeRequest) { r.Kind = service.RangeTable; r.Name = "" },
		"a banding with no colour":   func(r *service.RangeRequest) { r.Kind = service.RangeBanding },
		"a banding with a bad colour": func(r *service.RangeRequest) {
			r.Kind = service.RangeBanding
			r.Colour = "puce"
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
	if err == nil || !strings.Contains(err.Error(), "the new name") {
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
	rule.Colour = "#d9ead3"
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
