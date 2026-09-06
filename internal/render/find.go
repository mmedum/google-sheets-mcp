package render

import (
	"fmt"
	"strings"
)

// Match is one hit from find_in_spreadsheet.
type Match struct {
	Sheet   string
	Address string
	// Kind says where the match was: a value, the formula under it, or
	// the note beside it. A model that searched for a number and found
	// it inside a formula needs to know which.
	Kind    string
	Text    string
	Formula string
}

// StoppedBy says which limit ended a search. The three have different
// answers, so they are not one boolean: telling somebody to raise
// max_cells when it was max_matches that stopped them sends them to the
// wrong dial.
type StoppedBy string

// Reasons a search ended early.
const (
	// Complete means the search covered everything it was asked to.
	Complete StoppedBy = ""
	// CellBudget means max_cells ran out before the spreadsheet did.
	CellBudget StoppedBy = "cells"
	// MatchLimit means max_matches was reached; the cells were read.
	MatchLimit StoppedBy = "matches"
)

// Note is the sentence that says what to do about it.
//
// dataEndedEarly softens the cell-budget case. A sheet is allocated a
// grid long before it is filled — a new one is 1000 by 26 — so a search
// of a nearly empty spreadsheet reaches the budget while having seen
// every value in it, and "this did not cover the whole spreadsheet"
// then sends a model back for a second look that finds nothing. Live
// run, three populated rows, 4992 cells searched.
func (s StoppedBy) Note(dataEndedEarly bool) string {
	switch s {
	case CellBudget:
		if dataEndedEarly {
			return "the cell budget was reached before the end of the allocated grid, but every sheet " +
				"searched ran out of data before that point, so there is probably nothing further; " +
				"raise max_cells if you need certainty\n"
		}
		return "the cell budget was reached, so this did not cover the whole spreadsheet; " +
			"narrow it with sheet or raise max_cells to search further\n"
	case MatchLimit:
		return "max_matches was reached, so there may be more; the cells were read, " +
			"so raise max_matches rather than max_cells\n"
	}
	return ""
}

// Matches renders search hits as addresses with their context.
//
// The truncation note is printed whether or not anything matched, and
// especially when nothing did: "no matches" for a spreadsheet that was
// only partly read is a wrong answer, not a small one.
func Matches(ms []Match, searched int, stopped StoppedBy, dataEndedEarly bool) string {
	var b strings.Builder
	if len(ms) == 0 {
		fmt.Fprintf(&b, "no matches in %d cell(s) searched\n", searched)
		b.WriteString(stopped.Note(dataEndedEarly))
		return b.String()
	}
	fmt.Fprintf(&b, "%d match(es) in %d cell(s) searched:\n", len(ms), searched)
	for _, m := range ms {
		text, _ := clip(m.Text)
		fmt.Fprintf(&b, "  %s!%s  %s: %s", m.Sheet, m.Address, m.Kind, text)
		if m.Formula != "" && m.Kind != "formula" {
			formula, _ := clip(m.Formula)
			fmt.Fprintf(&b, "  [%s]", formula)
		}
		b.WriteByte('\n')
	}
	b.WriteString(stopped.Note(dataEndedEarly))
	return b.String()
}

// Hit is one spreadsheet from search_spreadsheets.
type Hit struct {
	Title    string
	ID       string
	Modified string
	Owner    string
	Link     string
}

// Hits renders a Drive search.
func Hits(hs []Hit, nextPage string) string {
	if len(hs) == 0 {
		return "no spreadsheets matched\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d spreadsheet(s):\n", len(hs))
	for _, h := range hs {
		fmt.Fprintf(&b, "  %s\n    id: %s\n", h.Title, h.ID)
		var meta []string
		if h.Owner != "" {
			meta = append(meta, "owner "+h.Owner)
		}
		if h.Modified != "" {
			meta = append(meta, "modified "+h.Modified)
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "    %s\n", strings.Join(meta, "; "))
		}
	}
	if nextPage != "" {
		fmt.Fprintf(&b, "more results: pass page_token %s\n", nextPage)
	}
	return b.String()
}

// Candidates renders the choices behind an [ambiguous] refusal. Listing
// them is the whole answer: taking the first match is how a server ends
// up writing to the wrong spreadsheet.
func Candidates(hs []Hit) string {
	var b strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&b, "\n  %s (id %s", h.Title, h.ID)
		if h.Modified != "" {
			fmt.Fprintf(&b, ", modified %s", h.Modified)
		}
		b.WriteString(")")
	}
	return b.String()
}
