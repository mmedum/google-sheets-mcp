//go:build live

package main

import (
	"fmt"
	"strings"
	"time"
)

func (d *driver) cardSteps() []step {
	return []step{
		{
			name: "the card names every sheet",
			why:  "every later call names a sheet, and no sheet name may be guessed",
			tool: "get_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet},
			check: func(text string, s map[string]any) error {
				for _, want := range []string{d.firstSheet, d.secondSheet} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the card does not name the sheet %q", want)
					}
				}
				sheets, _ := s["sheets"].([]any)
				if len(sheets) != 2 {
					return fmt.Errorf("the structured half lists %d sheets, and the spreadsheet has 2", len(sheets))
				}
				if card, _ := s["card"].(string); card == "" {
					return fmt.Errorf("the structured half carries no rendering, so a structure-only client sees nothing")
				}
				return nil
			},
		},
		{
			name: "a URL resolves like an id",
			why:  "people paste links, and the gid in one names a sheet",
			tool: "get_spreadsheet",
			args: map[string]any{"spreadsheet": "https://docs.google.com/spreadsheets/d/" + d.spreadsheet + "/edit"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, d.firstSheet) {
					return fmt.Errorf("the card from a URL does not match the card from an id")
				}
				return nil
			},
		},
		{
			name:        "a spreadsheet that does not exist",
			why:         "a refusal proves as much as a success, and this one has to say what to do next",
			tool:        "get_spreadsheet",
			args:        map[string]any{"spreadsheet": "1NoSuchSpreadsheetXXXXXXXXXXXXXXXXXXXXXXXXXXX"},
			expectError: "not_found",
		},
	}
}

func (d *driver) readSteps() []step {
	return []step{
		{
			name: "an addressed grid",
			why:  "the addresses are the substance: they are what lets a model write back without counting",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:D6"},
			check: func(text string, s map[string]any) error {
				for _, want := range []string{"A", "B", "C", "D", "1 |", "6 |", "Plimth"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the grid is missing %q", want)
					}
				}
				grid, _ := s["grid"].(string)
				if !strings.Contains(grid, "1 |") {
					return fmt.Errorf("the structured half has no addressed grid")
				}
				if cp, _ := s["checkpoint"].(string); !strings.HasPrefix(cp, "ck_") {
					return fmt.Errorf("no checkpoint on the read")
				}
				return nil
			},
		},
		{
			name: "show=both puts the formula under the value",
			why:  "a formula and its result render identically, and this is the only way to tell them apart",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "D1:D6", "show": "both"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "=B2+C2") {
					return fmt.Errorf("no formula in a show=both read of a formula column")
				}
				return nil
			},
		},
		{
			name: "an error cell says which error",
			why:  "Google returns a typed error value, and the display text is what a person sees",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "D6:D6"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "#DIV/0!") {
					return fmt.Errorf("a division by zero did not render as #DIV/0!; the seeded row 6 divides by zero")
				}
				return nil
			},
		},
		{
			name: "a note is shown beside the grid",
			why:  "a values read cannot show a note, and a write over that cell would destroy it",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:A6", "include_notes": true},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "A2") || !strings.Contains(text, "reconciliation") {
					return fmt.Errorf("the note on A2 was not listed")
				}
				return nil
			},
		},
		{
			name: "a sheet whose title needs quoting",
			why:  "the title is never concatenated, so a name with an accent or a space still resolves",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.secondSheet, "range": "A1:B2"},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "Trennow") {
					return fmt.Errorf("the second sheet's contents are missing")
				}
				rng, _ := s["range"].(string)
				if !strings.HasPrefix(rng, "'") {
					return fmt.Errorf("the range sent was not quoted: %q", rng)
				}
				return nil
			},
		},
		{
			name: "the same sheet by its numeric id",
			why:  "a sheet id survives a rename, and it is what a URL's gid carries",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": fmt.Sprint(d.secondSheetID), "range": "A1:B2"},
			check: func(_ string, s map[string]any) error {
				if got, _ := s["sheet"].(string); got != d.secondSheet {
					return fmt.Errorf("sheet id %d resolved to %q", d.secondSheetID, got)
				}
				return nil
			},
		},
		{
			name: "an open-ended range is bounded before the call",
			why:  "clamping the rendering while fetching a whole column is the bug this avoids, not repeats",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A:D", "max_cells": 8},
			check: func(_ string, s map[string]any) error {
				rng, _ := s["range"].(string)
				if strings.HasSuffix(rng, ":D") || !strings.Contains(rng, ":") {
					return fmt.Errorf("the range read is still open-ended: %q", rng)
				}
				if truncated, _ := s["truncated"].(bool); !truncated {
					return fmt.Errorf("a read of a whole sheet under an 8-cell budget did not report being cut")
				}
				if _, ok := s["continue_from"]; !ok {
					return fmt.Errorf("a truncated read did not say where to continue")
				}
				return nil
			},
		},
		{
			name: "the annotations a values read cannot show",
			why:  "a merge, a validation rule and a note are invisible in the values, and a write over them destroys them",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:D10",
				"include_notes": true, "include_validation": true, "include_merges": true,
			},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"notes: A2", "validation: A9", "merges: A8:C8"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the grid does not carry %q", want)
					}
				}
				if !strings.Contains(text, "one of list") {
					return fmt.Errorf("the validation rule was listed without saying what it allows")
				}
				return nil
			},
		},
		{
			name: "formatted shows what the person sees",
			why:  "a currency symbol inside a number is a trap for arithmetic, which is why raw is the default and this is a choice",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "B10:B10", "formatted": true},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "1,234.50") {
					return fmt.Errorf("the currency format was not applied; the cell holds 1234.5 formatted as currency")
				}
				return nil
			},
		},
		{
			name: "the same cell unformatted is computable",
			why:  "the default has to be the number, not the display string",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "B10:B10"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "1234.5") || strings.Contains(text, "1,234.50") {
					return fmt.Errorf("an unformatted read returned the display string")
				}
				return nil
			},
		},
		{
			name: "csv for feeding elsewhere",
			why:  "the machine formats have to carry the same window the grid does, not the whole rectangle",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:B3", "format": "csv"},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "Plimth,Nardle") {
					return fmt.Errorf("the csv is not csv")
				}
				rows, _ := s["rows"].([]any)
				if len(rows) != 3 {
					return fmt.Errorf("the structured half carries %d rows for a three-row range", len(rows))
				}
				if grid, _ := s["grid"].(string); !strings.Contains(grid, "1 |") {
					return fmt.Errorf("the structured half lost the addressed grid")
				}
				return nil
			},
		},
		{
			name: "a budgeted read continues where it stopped",
			why:  "a continuation that starts in the wrong place is worse than no continuation",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:D6", "max_cells": 8},
			check: func(text string, s map[string]any) error {
				from, _ := s["continue_from"].(float64)
				if int(from) != 3 {
					return fmt.Errorf("an 8-cell budget over 4 columns should stop after row 2, and continue_from is %v", s["continue_from"])
				}
				d.continueFrom = int(from)
				return nil
			},
		},
		{
			// The row is hard-coded so the gate can see continue_from in
			// a static argument map, and the step before it asserts the
			// server chose that same row — so a change in either fails
			// rather than quietly agreeing.
			name: "the continuation starts where the last read stopped",
			why:  "a continuation that starts in the wrong place silently skips or repeats rows",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:D6",
				"max_cells": 8, "continue_from": 3,
			},
			check: func(text string, _ map[string]any) error {
				if d.continueFrom != 3 {
					return fmt.Errorf("the previous read stopped at %d, so this step is reading the wrong row", d.continueFrom)
				}
				if !strings.Contains(text, "3 |") {
					return fmt.Errorf("the continuation does not start at row 3")
				}
				if strings.Contains(text, "1 |") {
					return fmt.Errorf("the continuation repeated a row the previous read already showed")
				}
				return nil
			},
		},
		{
			name:        "a sheet that does not exist",
			why:         "the API's own message for this says only \"Unable to parse range\"; ours has to list what exists",
			tool:        "read_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": "Sheet1 that is not here", "range": "A1:B2"},
			expectError: "not_found",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, d.firstSheet) {
					return fmt.Errorf("the refusal does not name the sheets that do exist")
				}
				return nil
			},
		},
		{
			name:        "a range that is not a range",
			why:         "a total parser refuses nonsense instead of returning a plausible rectangle",
			tool:        "read_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "range": "A1:B2:C3"},
			expectError: "invalid",
		},
		{
			name:        "no sheet at all",
			why:         "nothing defaults a sheet, because Google names the first one in the account's language",
			tool:        "read_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "range": "A1:B2"},
			expectError: "invalid",
		},
	}
}

func (d *driver) findSteps() []step {
	return []step{
		{
			name: "text to an A1 address",
			why:  "there is no server-side search in the Sheets API, so this reads and matches here",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "query": "Skerry"},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "A4") {
					return fmt.Errorf("Skerry-03 is in A4 and the search did not say so")
				}
				hits, _ := s["hits"].([]any)
				if len(hits) == 0 {
					return fmt.Errorf("the structured half has no hits")
				}
				return nil
			},
		},
		{
			name: "a match inside a formula says so",
			why:  "a model that searched for a number needs to know it found it in a formula",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "query": "B2+C2", "search_formulas": true},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "formula") {
					return fmt.Errorf("the match was not reported as a formula")
				}
				return nil
			},
		},
		{
			name: "a note is searchable and labelled",
			why:  "a note is a Sheets field and is in scope, unlike a comment thread",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "query": "reconciliation", "search_notes": true},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "note") || !strings.Contains(text, "A2") {
					return fmt.Errorf("the note on A2 was not found")
				}
				return nil
			},
		},
		{
			name: "a search narrowed to one sheet",
			why:  "narrowing is the answer to a budget that cannot cover a whole spreadsheet",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.secondSheet, "query": "Skerry"},
			check: func(text string, s map[string]any) error {
				sheets, _ := s["sheets_searched"].([]any)
				if len(sheets) != 1 {
					return fmt.Errorf("a search narrowed to one sheet covered %d", len(sheets))
				}
				if strings.Contains(text, d.firstSheet+"!") {
					return fmt.Errorf("a search narrowed to the second sheet returned a hit on the first")
				}
				return nil
			},
		},
		{
			name: "match_case distinguishes what a default search does not",
			why:  "the default is case-insensitive, so the flag has to actually change the answer",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.firstSheet, "query": "skerry", "match_case": true},
			check: func(text string, s map[string]any) error {
				hits, _ := s["hits"].([]any)
				if len(hits) != 0 {
					return fmt.Errorf("a case-sensitive search for a lower-case term matched %d cell(s)", len(hits))
				}
				if !strings.Contains(text, "no matches") {
					return fmt.Errorf("an empty search did not say so")
				}
				return nil
			},
		},
		{
			name: "max_matches stops and names the right dial",
			why:  "telling somebody to raise max_cells when max_matches stopped them sends them to a dial that changes nothing",
			tool: "find_in_spreadsheet",
			// max_cells is raised past the sheet's allocated 1000x26 on
			// purpose. Without it the cell budget bites first and the
			// server correctly reports *that* — the first limit to bite
			// is the honest one — so the step would be testing the
			// wrong thing while looking like it tested the right one.
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.firstSheet,
				"regex": ".", "max_matches": 2, "max_cells": 30000,
			},
			check: func(text string, s map[string]any) error {
				hits, _ := s["hits"].([]any)
				if len(hits) != 2 {
					return fmt.Errorf("max_matches 2 returned %d hits", len(hits))
				}
				if by, _ := s["stopped_by"].(string); by != "matches" {
					return fmt.Errorf("stopped_by is %q, want matches", by)
				}
				if strings.Contains(text, "raise max_cells") {
					return fmt.Errorf("the note sends the caller to the wrong dial")
				}
				return nil
			},
		},
		{
			name:        "a regex that does not compile",
			why:         "RE2 has no lookaround, and the refusal has to say which dialect this is",
			tool:        "find_in_spreadsheet",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "regex": "(?P<"},
			expectError: "invalid",
		},
		{
			name: "a budget that stops the search says so",
			why:  "\"no matches\" for a spreadsheet that was only partly read is a wrong answer, not a small one",
			tool: "find_in_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "query": "nothing matches this at all", "max_cells": 4},
			check: func(text string, s map[string]any) error {
				if truncated, _ := s["truncated"].(bool); !truncated {
					return fmt.Errorf("a 4-cell budget over two sheets did not report being cut")
				}
				if !strings.Contains(text, "budget") {
					return fmt.Errorf("the rendering does not mention the budget")
				}
				return nil
			},
		},
	}
}

// searchSteps are the criteria Drive answers from file metadata, which
// it does not have to index first.
func (d *driver) searchSteps() []step {
	return []step{
		{
			name: "by owner, with a limit",
			why:  "owner is what tells two spreadsheets of the same name apart, and limit is what keeps a wide search cheap",
			tool: "search_spreadsheets",
			args: map[string]any{"owner": d.owner, "limit": 1},
			check: func(text string, s map[string]any) error {
				hits, _ := s["spreadsheets"].([]any)
				if len(hits) != 1 {
					return fmt.Errorf("limit 1 returned %d results", len(hits))
				}
				if !strings.Contains(text, "owner ") {
					return fmt.Errorf("the rendering does not name the owner it matched on")
				}
				return nil
			},
		},
		{
			name: "by modification time",
			why:  "\"what changed this week\" is how people look for a spreadsheet they cannot name",
			tool: "search_spreadsheets",
			args: map[string]any{"name": "livesheet scratch", "modified_after": d.since},
			check: func(_ string, s map[string]any) error {
				hits, _ := s["spreadsheets"].([]any)
				if len(hits) == 0 {
					return fmt.Errorf("a spreadsheet created after %s was not returned by modified_after", d.since)
				}
				return nil
			},
		},
		{
			name:        "a search with no criterion at all",
			why:         "listing every spreadsheet in an account is a Drive job; refusing keeps this server inside one spreadsheet",
			tool:        "search_spreadsheets",
			args:        map[string]any{"limit": 5},
			expectError: "invalid",
		},
	}
}

// searchWithIndexingLag polls, because Drive's index is eventually
// consistent: a spreadsheet created a second ago may not be findable
// yet, and one read cannot tell indexing lag from a broken search. If it
// never appears, this says so loudly rather than printing an empty
// result and moving on.
func (d *driver) searchWithIndexingLag() {
	d.poll(step{
		name: "the scratch spreadsheet is findable by title",
		why:  "a title is how a person names a spreadsheet, and Drive's index is what turns it into an id",
		tool: "search_spreadsheets",
		args: map[string]any{"name": d.title},
	})
	// Full text is a second index and a slower one, so it gets the same
	// polling. The search term is a bare word on purpose: Drive
	// tokenises, and the first version of this step asked for
	// "Quorbin-01" — a term that can never match however long the index
	// is given. It reported "cannot tell lag from broken" for two runs,
	// which is the failure mode of an undetermined result: it looked
	// like patience when it was a step asserting something the world
	// does not permit.
	d.poll(step{
		name: "the scratch spreadsheet is findable by its contents",
		why:  "text searches Drive's full-text index, which reaches cell values and lags behind the title index",
		tool: "search_spreadsheets",
		args: map[string]any{"text": "Quorbin"},
	})
}

// poll runs a step until its result names the scratch spreadsheet, and
// says loudly if it never does.
func (d *driver) poll(s step) {
	const attempts = 10
	d.steps++
	for attempt := 1; attempt <= attempts; attempt++ {
		text, structured, err := d.call(s.tool, s.args)
		if err != nil {
			d.fail(s, "the call itself failed: %v", err)
			return
		}
		if strings.Contains(text, d.spreadsheet) {
			line("ok   %s (found on attempt %d)", s.name, attempt)
			line("     why: %s", s.why)
			if hits, _ := structured["spreadsheets"].([]any); len(hits) == 0 {
				d.fail(s, "the text half found it and the structured half is empty")
				return
			}
			for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				line("     | %s", l)
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
	// The step failed to find *this* spreadsheet. That has two causes
	// with opposite meanings, and they are distinguishable: run the same
	// query without expecting the new file. If it matches something
	// else, the query works and the index is behind. If it matches
	// nothing anywhere, the query itself is suspect — which is what was
	// happening when this step searched for a hyphenated compound Drive
	// tokenises apart, and reported patience for two runs instead.
	d.undetermined++
	line("UNKNOWN %s", s.name)
	text, _, err := d.call(s.tool, s.args)
	switch {
	case err != nil:
		line("     the query itself failed: %v", err)
	case strings.Contains(text, "no spreadsheets matched"):
		line("     THE QUERY MATCHES NOTHING AT ALL, not even spreadsheets indexed long ago.")
		line("     That is not indexing lag. Suspect the query or the search itself:")
		line("     Drive tokenises, so a hyphenated or punctuated term can never match.")
	default:
		line("     The query matches other spreadsheets, so the search works and Drive")
		line("     has not indexed this one yet — content indexing lags well behind")
		line("     the title index. Lag, not breakage.")
	}
	line("     Either way this run did not observe it; re-run before concluding.")
}
