package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestFindReturnsAddresses(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Query: "Plimth",
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("no hits:\n%s", res.Matches)
	}
	first := res.Hits[0]
	if first.Sheet != sheetstest.FirstSheet || first.Address != "A1" || first.Kind != "value" {
		t.Errorf("first hit = %+v, want A1 on the first sheet", first)
	}
	if !strings.Contains(res.Matches, "A1") {
		t.Errorf("the rendering has no address:\n%s", res.Matches)
	}
	if len(res.SheetsSearched) != 3 {
		t.Errorf("searched %v; every grid sheet should be covered by default", res.SheetsSearched)
	}
}

func TestFindIsOneRequestForEverySheet(t *testing.T) {
	// A batch and all its subrequests count as one request against
	// quota, so several ranges cost what one does. Splitting them would
	// cost quota for nothing.
	srv, svc := standard(t)
	srv.Reset()
	if _, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Query: "Nardle",
	}); err != nil {
		t.Fatal(err)
	}
	grids := 0
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.get" && c.Query.Get("includeGridData") == "true" {
			grids++
			if len(c.Query["ranges"]) < 2 {
				t.Errorf("the search sent %d ranges; every sheet should travel together", len(c.Query["ranges"]))
			}
		}
	}
	if grids != 1 {
		t.Errorf("the search cost %d grid reads, want 1", grids)
	}
}

func TestFindKindsSayWhereTheMatchWas(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	// A number inside a formula is not a value, and a model that
	// searched for it needs to be told which it is looking at.
	res, err := svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Query: "B2+C2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("a formula matched without search_formulas: %+v", res.Hits)
	}

	res, err = svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Query: "B2+C2", SearchFormulas: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Kind != "formula" || res.Hits[0].Address != "D2" {
		t.Fatalf("formula search gave %+v", res.Hits)
	}

	res, err = svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Query: "reconciliation", SearchNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Kind != "note" || res.Hits[0].Address != "A2" {
		t.Fatalf("note search gave %+v", res.Hits)
	}
}

func TestFindCaseAndRegex(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	insensitive, err := svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Query: "plimth",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(insensitive.Hits) == 0 {
		t.Error("the default search is case-insensitive")
	}
	sensitive, err := svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Query: "plimth", MatchCase: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sensitive.Hits) != 0 {
		t.Errorf("match_case did not apply: %+v", sensitive.Hits)
	}

	re, err := svc.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Regex: `^Quorbin-\d\d$`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Hits) == 0 {
		t.Error("the regex matched nothing")
	}

	_, err = svc.Find(ctx, service.FindRequest{Spreadsheet: sheetstest.FixtureID, Regex: `(?P<`})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("a broken regex gave %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "RE2") {
		t.Errorf("the refusal does not say which dialect this is: %v", err)
	}

	_, err = svc.Find(ctx, service.FindRequest{Spreadsheet: sheetstest.FixtureID})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("a search with neither query nor regex gave %v", err)
	}
	_, err = svc.Find(ctx, service.FindRequest{Spreadsheet: sheetstest.FixtureID, Query: "a", Regex: "a"})
	if err == nil {
		t.Error("a search with both query and regex was accepted")
	}
}

// TestFindSaysWhenTheBudgetStoppedIt is the difference between "not
// there" and "not looked at". Reporting no matches for a spreadsheet
// that was only partly read is a wrong answer, not a small one.
func TestFindSaysWhenTheBudgetStoppedIt(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(2000, 10)
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: doc.ID, Query: "nothing matches this", Budget: service.Budget{Cells: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("the search ran out of budget and did not say so")
	}
	if !strings.Contains(res.Matches, "budget") {
		t.Errorf("the rendering does not mention the budget:\n%s", res.Matches)
	}
	if res.CellsRead > 100 {
		t.Errorf("read %d cells against a budget of 100", res.CellsRead)
	}
}

func TestFindStopsAtMaxMatches(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Regex: `.`, Budget: service.Budget{Matches: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 3 || !res.Truncated {
		t.Errorf("max_matches gave %d hits, truncated=%v", len(res.Hits), res.Truncated)
	}
}

func TestFindWithNoMatches(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Query: "Zzzzznothing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || !strings.Contains(res.Matches, "no matches") {
		t.Errorf("an empty search rendered as %q", res.Matches)
	}
	if res.Truncated {
		t.Error("a complete search must not claim to be truncated")
	}
}

// TestTheStopReasonPicksTheRightDial is why the three endings are not
// one boolean. Telling somebody to raise max_cells when max_matches
// stopped them sends them to a dial that changes nothing.
func TestTheStopReasonPicksTheRightDial(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(2000, 10)
	srv.Add(doc, file)
	big := newService(t, srv)
	_, small := standard(t)
	ctx := context.Background()

	byCells, err := big.Find(ctx, service.FindRequest{
		Spreadsheet: doc.ID, Query: "nothing matches this", Budget: service.Budget{Cells: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if byCells.StoppedBy != "cells" || !strings.Contains(byCells.Matches, "max_cells") {
		t.Errorf("a cell-budget stop reported %q: %s", byCells.StoppedBy, byCells.Matches)
	}

	byMatches, err := small.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Regex: ".", Budget: service.Budget{Matches: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if byMatches.StoppedBy != "matches" {
		t.Errorf("a match-limit stop reported %q", byMatches.StoppedBy)
	}
	if strings.Contains(byMatches.Matches, "raise max_cells") {
		t.Errorf("a match-limit stop told the caller to raise max_cells: %s", byMatches.Matches)
	}

	complete, err := small.Find(ctx, service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Query: "Trennow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if complete.Truncated || complete.StoppedBy != "" {
		t.Errorf("a complete search reported %q", complete.StoppedBy)
	}
}

// TestBothLimitsBitingKeepsTheHonestOne is the case where the two
// endings overlap. If max_matches overwrote a cell budget that had
// already bitten, the caller would be told to raise max_matches — which
// would never reach the sheets the budget excluded.
func TestBothLimitsBitingKeepsTheHonestOne(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(500, 8)
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: doc.ID, Regex: ".", Budget: service.Budget{Cells: 40, Matches: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StoppedBy != "cells" {
		t.Errorf("both limits bit and the result reported %q; raising max_matches would not reach the rest", res.StoppedBy)
	}
	if !strings.Contains(res.Matches, "did not cover the whole spreadsheet") {
		t.Errorf("the warning that the search was partial was dropped:\n%s", res.Matches)
	}
}

// TestCellsReadCountsWhatWasLookedAt keeps the number honest: a search
// that stops at the first match has not read a whole window.
func TestCellsReadCountsWhatWasLookedAt(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(500, 8)
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: doc.ID, Regex: ".", Budget: service.Budget{Matches: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Two, not one: the limit is noticed by looking one cell further,
	// which is what makes "there may be more" true rather than assumed.
	// The number that matters is that it is not the 4000-cell window.
	if res.CellsRead != 2 {
		t.Errorf("a search that stopped at the first match reported %d cells searched", res.CellsRead)
	}
	if !res.Truncated {
		t.Error("stopping at max_matches must say there may be more")
	}
}

func TestShareDividesTheBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		total int
		sizes []int
		want  []int
	}{
		{"two equal sheets", 100, []int{1000, 1000}, []int{50, 50}},
		{"a small sheet releases its surplus", 100, []int{10, 1000}, []int{10, 90}},
		{"everything fits", 100, []int{10, 20}, []int{10, 20}},
		{"one sheet", 100, []int{1000}, []int{100}},
		{"more sheets than budget", 2, []int{100, 100, 100}, []int{1, 1, 0}},
		{"no budget", 0, []int{100}, []int{0}},
		{"no sheets", 100, nil, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := service.Share(tc.total, tc.sizes)
			if len(got) != len(tc.want) {
				t.Fatalf("Share = %v, want %v", got, tc.want)
			}
			sum := 0
			for i := range got {
				sum += got[i]
				if got[i] > tc.sizes[i] {
					t.Errorf("sheet %d was given %d of a %d-cell sheet", i, got[i], tc.sizes[i])
				}
			}
			if sum > tc.total {
				t.Errorf("Share handed out %d against a budget of %d", sum, tc.total)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Share = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}

// TestASearchCoversEverySheet is the defect a live run found: the budget
// was spent in order, so the first sheet swallowed all of it and a term
// present on two sheets came back with one match.
func TestASearchCoversEverySheet(t *testing.T) {
	_, svc := standard(t)
	// "Nardle" is a heading on the first sheet and nothing on the
	// others; "Trennow" is only on the second. A search of the whole
	// spreadsheet has to see both.
	res, err := svc.Find(context.Background(), service.FindRequest{
		Spreadsheet: sheetstest.FixtureID, Regex: "Nardle|Trennow",
	})
	if err != nil {
		t.Fatal(err)
	}
	sheets := map[string]bool{}
	for _, h := range res.Hits {
		sheets[h.Sheet] = true
	}
	if !sheets[sheetstest.FirstSheet] || !sheets[sheetstest.SecondSheet] {
		t.Errorf("the search covered %v; both sheets hold a match:\n%s", sheets, res.Matches)
	}
}
