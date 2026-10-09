package render

import (
	"fmt"
	"strconv"
	"strings"
)

// PivotAct is a change to a pivot table, in parts.
type PivotAct struct {
	Action string
	// Anchor is the cell it sits at, which is the only name a pivot has:
	// the API gives it no id.
	Anchor string
	Sheet  string
	Source string
	// Groups, Values and Filters are what an add was asked for, in the
	// caller's own words, so a result reads back what was written.
	Groups  []string
	Values  []string
	Filters []string
	// Changed names the arguments an update applied.
	Changed []string
	// Output is the rectangle the pivot draws right now, read after the
	// write rather than derived from it.
	Output string
}

// Phrase says what the act does.
func (a PivotAct) Phrase() string {
	switch a.Action {
	case "add":
		by := ""
		if len(a.Groups) > 0 {
			by = " grouped by " + JoinAnd(a.Groups)
		}
		summarizing := ""
		if len(a.Values) > 0 {
			summarizing = " summarizing " + JoinAnd(a.Values)
		}
		filtered := ""
		if len(a.Filters) > 0 {
			filtered = " filtered by " + JoinAnd(a.Filters)
		}
		return fmt.Sprintf("add a pivot table at %s on %q over %s%s%s%s", a.Anchor, a.Sheet, a.Source, by,
			summarizing, filtered)
	case "update":
		return fmt.Sprintf("change the %s of the pivot table at %s on %q", JoinAnd(a.Changed), a.Anchor, a.Sheet)
	default:
		return fmt.Sprintf("delete the pivot table at %s on %q", a.Anchor, a.Sheet)
	}
}

// PivotPreview renders what a pivot change would do.
func PivotPreview(a PivotAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would %s.\n", a.Phrase())
	if a.Action == "delete" && a.Output != "" {
		fmt.Fprintf(&b, "It would clear %s, which is everything the pivot draws.\n", a.Output)
	}
	return b.String()
}

// PivotDone renders what it did.
//
// The output rectangle, always, because it is the one thing about a
// pivot that is in no request: the size is computed from the data, so a
// caller who writes beside where they think it ends writes into it.
func PivotDone(a PivotAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %s.\n", upperFirst(a.Phrase()))
	switch {
	case a.Action == "delete":
		if a.Output != "" {
			fmt.Fprintf(&b, "It cleared %s, which was everything the pivot drew.\n", a.Output)
		}
	case a.Output != "":
		fmt.Fprintf(&b, "It covers %s. That rectangle is computed from the data rather than set, so it grows and "+
			"shrinks as the source does — a write into it stops the pivot drawing until the cell is cleared again.\n",
			a.Output)
	}
	return b.String()
}

// PivotRow is one pivot of a listing. Rows, Columns, Values and Filters
// are in the spelling the tool takes.
type PivotRow struct {
	Anchor  string
	Source  string
	Output  string
	Rows    []string
	Columns []string
	Values  []string
	Filters []string
}

// PivotList renders the pivot tables found in a rectangle.
func PivotList(pivots []PivotRow, sheet, window string) string {
	var b strings.Builder
	if len(pivots) == 0 {
		fmt.Fprintf(&b, "No pivot tables in %s on %q.\n", window, sheet)
		return b.String()
	}
	fmt.Fprintf(&b, "%d pivot table(s) in %s on %q:\n", len(pivots), window, sheet)
	for _, p := range pivots {
		line := "  " + p.Anchor
		if p.Source != "" {
			line += "  over " + p.Source
		}
		if p.Output != "" {
			line += "  covering " + p.Output
		}
		b.WriteString(line + "\n")
		for _, part := range []struct {
			arg     string
			entries []string
		}{
			{"group_rows", p.Rows}, {"group_columns", p.Columns}, {"values", p.Values}, {"filters", p.Filters},
		} {
			if len(part.entries) > 0 {
				fmt.Fprintf(&b, "    %s %s\n", part.arg, quotedList(part.entries))
			}
		}
	}
	return b.String()
}

// quotedList is a list as the argument takes it, ["A", "B sum"], since an
// entry may hold a comma.
func quotedList(entries []string) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = strconv.Quote(e)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
