package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// Anchor is one durable label, in the terms a caller reads.
type Anchor struct {
	Name  string
	Note  string
	Range string
	Sheet string
	Scope string
}

// Where says what the anchor points at, in one phrase.
func (a Anchor) Where() string {
	switch {
	case a.Range != "":
		return a.Range
	case a.Sheet != "":
		return fmt.Sprintf("the sheet %q", a.Sheet)
	default:
		return "the whole spreadsheet"
	}
}

// AnchorNames is how a list of anchors is written, wherever it is
// written.
//
// One function because a live run found the two callers disagreeing: the
// refusal quoted the names and the result printed them bare, for the
// same delete, a moment apart. That is the shape §17a.9 moved the
// structural tools' English into this package to stop, reintroduced by
// adding the clause to one caller and the sentence to the other.
func AnchorNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return JoinAnd(quoted)
}

// AnchorsTaken is the clause a delete's refusal adds when anchors would
// go with the band.
//
// "and the anchor(s)" rather than a second "takes … with them": the
// sentence it joins already ends in "with them", and the live transcript
// read "takes 2 cells and 0 formulas with them, and takes the anchor(s)
// X with them".
func AnchorsTaken(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return ", and the anchor(s) " + AnchorNames(names)
}

// AnchorActed is an add, a move or a removal, in parts.
type AnchorActed struct {
	Action  string
	Anchor  Anchor
	Preview bool
}

// Phrase says what the act does.
func (a AnchorActed) Phrase() string {
	switch a.Action {
	case "add":
		return fmt.Sprintf("anchor %q to %s", a.Anchor.Name, a.Anchor.Where())
	case "move":
		return fmt.Sprintf("move the anchor %q to %s", a.Anchor.Name, a.Anchor.Where())
	default:
		return fmt.Sprintf("remove the anchor %q, which points at %s", a.Anchor.Name, a.Anchor.Where())
	}
}

// AnchorAct renders one act on an anchor.
func AnchorAct(a AnchorActed) string {
	var b strings.Builder
	if a.Preview {
		fmt.Fprintf(&b, "Dry run: nothing was sent. This would %s.\n", a.Phrase())
	} else {
		fmt.Fprintf(&b, "Done: %s.\n", upperFirst(a.Phrase()))
	}
	if a.Action == "remove" {
		b.WriteString("The row it named is untouched; only the label goes.\n")
		return b.String()
	}
	// Said on every add and move, because it is the whole difference
	// between an anchor and an address, and a caller who does not know
	// it will keep re-anchoring after every insert.
	switch a.Anchor.Scope {
	case "row", "column":
		fmt.Fprintf(&b, "The anchor follows this %s as the sheet is edited: inserting, deleting, moving and sorting "+
			"%ss around it all keep it pointing at the same %s. Deleting the %s itself takes the anchor with it.\n",
			a.Anchor.Scope, a.Anchor.Scope, a.Anchor.Scope, a.Anchor.Scope)
	}
	if a.Anchor.Note != "" {
		fmt.Fprintf(&b, "Note: %s\n", a.Anchor.Note)
	}
	return b.String()
}

// AnchorList renders every anchor in a spreadsheet.
func AnchorList(anchors []Anchor) string {
	if len(anchors) == 0 {
		return "No anchors in this spreadsheet. manage_anchor with action=add makes one: a label that keeps " +
			"pointing at the same row as the sheet is edited around it.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d anchor(s):\n", len(anchors))
	for _, a := range anchors {
		fmt.Fprintf(&b, "  %s → %s", a.Name, a.Where())
		if a.Note != "" {
			fmt.Fprintf(&b, " — %s", a.Note)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Pass a name as %s<name> anywhere a range or band is taken.\n", a1.AnchorPrefix)
	return b.String()
}
