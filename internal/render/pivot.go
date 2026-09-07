package render

import (
	"fmt"
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
	// Groups and Values are what an add was asked for, in the caller's
	// own words, so a result reads back what was written.
	Groups []string
	Values []string
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
		summarising := ""
		if len(a.Values) > 0 {
			summarising = " summarising " + JoinAnd(a.Values)
		}
		return fmt.Sprintf("add a pivot table at %s on %q over %s%s%s", a.Anchor, a.Sheet, a.Source, by, summarising)
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

// PivotRow is one line of a listing.
type PivotRow struct {
	Anchor string
	Source string
	Output string
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
	}
	return b.String()
}
