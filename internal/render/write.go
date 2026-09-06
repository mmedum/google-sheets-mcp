package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/grid"
)

// Coercion is one cell Google stored differently from how it was sent.
// A view model rather than a service type, so this package stays below
// the service and renders without one.
type Coercion struct {
	Address   string
	Sent      string
	SentKind  string
	Stored    string
	Kind      string
	Displayed string
}

// Write is everything a write result renders.
type Write struct {
	Range string
	Cells int
	// Grid is the region after the write, addressed.
	Grid       string
	Checkpoint string
	Coerced    []Coercion
	Formulas   []string
	// Kept names notes and validation rules the write left in place.
	// They are invisible in a values read, so a caller cannot know they
	// are there — and a rule that survives still applies to the value
	// that just replaced the old one.
	Kept []string
	// TailLeft is the part of the range the values did not cover.
	TailLeft string
	// Counts describes the target before a dry run's write, and Blockers
	// what would stop it.
	Counts   grid.Counts
	Blockers []string
}

// WriteDone renders what a write did.
//
// The order is what a reader needs in the order they need it: where it
// landed, what it looks like now, then the three things they could not
// have predicted — what Google changed, what now holds a formula, and
// what the write took with it.
func WriteDone(w Write) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Wrote %d cell(s) to %s.\n", w.Cells, w.Range)
	if w.Grid != "" {
		b.WriteString("\n")
		b.WriteString(w.Grid)
	}
	writeNotes(&b, w)
	if w.Checkpoint != "" {
		fmt.Fprintf(&b, "checkpoint %s\n", w.Checkpoint)
	}
	return b.String()
}

// WritePreview renders what a write would do, having sent nothing.
func WritePreview(w Write) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. %d cell(s) would be written to %s.\n", w.Cells, w.Range)
	fmt.Fprintf(&b, "The target holds %d non-empty cell(s), %d of them formulas.\n", w.Counts.NonEmpty, w.Counts.Formulas)
	// What would stop it, if anything. A preview that showed the region
	// and not the refusal would send the caller to make a write that
	// cannot go through.
	if len(w.Blockers) > 0 {
		b.WriteString("\nAs asked, this write would be refused:\n")
		for _, line := range w.Blockers {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	if w.Grid != "" {
		b.WriteString("\n")
		b.WriteString(w.Grid)
	}
	writeNotes(&b, w)
	return b.String()
}

func writeNotes(b *strings.Builder, w Write) {
	if len(w.Coerced) > 0 {
		fmt.Fprintf(b, "\nGoogle stored %d value(s) differently from how they were sent:\n", len(w.Coerced))
		for _, c := range w.Coerced {
			// Both kinds, always. Without them "12" -> "12" reads as a
			// line reporting no change at all, when what changed is that
			// the cell holds a number where it held text.
			fmt.Fprintf(b, "  %s  %s %s -> %s %s", c.Address,
				c.SentKind, quote(c.SentKind, c.Sent), c.Kind, quote(c.Kind, c.Stored))
			// A date is stored as a serial number and displayed as a
			// date. Without the pair, the number reads as data loss.
			if c.Displayed != "" && c.Displayed != c.Stored {
				fmt.Fprintf(b, ", displayed %s", quote("text", c.Displayed))
			}
			b.WriteString("\n")
		}
	}
	if len(w.Formulas) > 0 {
		fmt.Fprintf(b, "\nFormulas now in: %s\n", strings.Join(w.Formulas, ", "))
	}
	if len(w.Kept) > 0 {
		fmt.Fprintf(b, "\nA value write keeps them, so %s are still there. A validation rule that survives applies "+
			"to the value that replaced the old one.\n", JoinAnd(w.Kept))
	}
	if w.TailLeft != "" {
		fmt.Fprintf(b, "\n%s was inside the range and holds whatever it held: a write skips cells it has no value for "+
			"rather than clearing them.\n", w.TailLeft)
	}
}

// Change is one structural change a batch made, as its result reports it.
type Change struct {
	What   string
	Detail string
}

// Changes renders what a structural write did, one line each.
func Changes(heading string, changes []Change) string {
	var b strings.Builder
	b.WriteString(heading)
	if !strings.HasSuffix(heading, "\n") {
		b.WriteString("\n")
	}
	for _, c := range changes {
		if c.Detail == "" {
			fmt.Fprintf(&b, "  %s\n", c.What)
			continue
		}
		fmt.Fprintf(&b, "  %s — %s\n", c.What, c.Detail)
	}
	return b.String()
}

// quote renders one side of a coercion. Text is quoted because its edges
// are the point — a leading zero, a trailing space — and a number is
// not, because `number "7"` reads as a string of one digit.
func quote(kind, s string) string {
	switch {
	case s == "":
		return "(empty)"
	case kind == "text":
		return `"` + s + `"`
	}
	return s
}

// JoinAnd is strings.Join with an Oxford-free "and" for message text. It
// is exported because the service builds refusal messages too, and two
// copies of this would phrase a list differently the day one changed.
func JoinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Append is everything an append result renders.
type Append struct {
	Rows  int
	Cells int
	// Searched is the range the caller named, which picks the block.
	Searched string
	// Landed is where the rows went, and TableRange the block Google
	// appended after. Neither is predictable from Searched.
	Landed     string
	TableRange string
	Inserting  bool
	Grid       string
	Coerced    []Coercion
	Formulas   []string
	// Blockers is what would stop the append, for a preview.
	Blockers []string
}

// AppendDone renders what an append did.
//
// It leads with where the rows landed rather than with how many there
// are, because that is the one thing the caller could not have worked
// out: verified live, the same sheet given A1 and given the whole sheet
// appends six rows apart.
func AppendDone(a Append) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Appended %d row(s), %d cell(s), to %s.\n", a.Rows, a.Cells, a.Landed)
	if a.TableRange != "" {
		fmt.Fprintf(&b, "Google found the block %s from the range you named (%s) and wrote after it.\n",
			a.TableRange, a.Searched)
	}
	if a.Inserting {
		b.WriteString("Rows were inserted, so everything below the insert point moved down: an address or a " +
			"checkpoint from before this call no longer points where it did.\n")
	}
	if a.Grid != "" {
		b.WriteString("\n")
		b.WriteString(a.Grid)
	}
	writeNotes(&b, Write{Coerced: a.Coerced, Formulas: a.Formulas})
	return b.String()
}

// AppendPreview renders what an append would do, having sent nothing.
//
// It says less than a write's preview does, and says why: the
// destination is chosen during the call.
func AppendPreview(a Append) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. %d row(s), %d cell(s) would be appended after the block Google finds "+
		"from %s.\n", a.Rows, a.Cells, a.Searched)
	b.WriteString("Which block that is, and therefore where the rows land, is decided during the call, so this " +
		"preview cannot name the destination.\n")
	if a.Inserting {
		b.WriteString("Rows would be inserted, so everything below the insert point would move down.\n")
	} else {
		b.WriteString("Rows would overwrite whatever follows the block.\n")
	}
	if len(a.Blockers) > 0 {
		b.WriteString("\nAs asked, this append would be refused:\n")
		for _, line := range a.Blockers {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return b.String()
}

// CreatePreview renders what create_spreadsheet would do.
func CreatePreview(title string, sheets []string, seedRows int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would create a spreadsheet called %q in My Drive.\n", title)
	if len(sheets) > 0 {
		fmt.Fprintf(&b, "With exactly these sheets, in order: %s. Naming them replaces the single sheet Google would "+
			"otherwise make.\n", strings.Join(sheets, ", "))
	} else {
		b.WriteString("With one sheet, which Google creates and names in the account's language.\n")
	}
	if seedRows > 0 {
		fmt.Fprintf(&b, "And %d row(s) of seed values on the first sheet.\n", seedRows)
	}
	return b.String()
}

// CreateDone renders the new spreadsheet's card and what was seeded.
func CreateDone(card, seeded, note string, coerced []Coercion, formulas []string) string {
	var b strings.Builder
	b.WriteString("Created.\n\n")
	b.WriteString(card)
	if !strings.HasSuffix(card, "\n") {
		b.WriteString("\n")
	}
	if seeded != "" {
		fmt.Fprintf(&b, "\nSeed values written to %s.\n", seeded)
	}
	if note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	writeNotes(&b, Write{Coerced: coerced, Formulas: formulas})
	return b.String()
}

// DeletePreview says what deleting a sheet would take with it.
func DeletePreview(sheet string, cells, formulas, charts int) string {
	return fmt.Sprintf("Dry run: nothing was sent. Deleting %q would take %d non-empty cell(s), "+
		"%d formula(s) and %d chart(s) with it, and Sheets cannot undo it.\n", sheet, cells, formulas, charts)
}

// DeleteDone says what a deletion took.
func DeleteDone(sheet string, cells, formulas, charts int, left []string) string {
	return fmt.Sprintf("Deleted %q, with %d non-empty cell(s), %d formula(s) and %d chart(s).\nThe sheets left are: %s\n",
		sheet, cells, formulas, charts, strings.Join(left, ", "))
}

// DimensionPreview and DimensionDone render a change to rows or columns.
// Both say when addresses moved, because a checkpoint or an address the
// caller is holding no longer points where it did.
func DimensionPreview(what string, cells, formulas int, shifted bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would %s.\n", what)
	dimensionNotes(&b, cells, formulas, shifted, "would take", "would move")
	return b.String()
}

// DimensionDone renders what the change did.
func DimensionDone(what string, cells, formulas int, shifted bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %s.\n", strings.ToUpper(what[:1])+what[1:])
	dimensionNotes(&b, cells, formulas, shifted, "took", "moved")
	return b.String()
}

func dimensionNotes(b *strings.Builder, cells, formulas int, shifted bool, took, moved string) {
	if cells > 0 || formulas > 0 {
		fmt.Fprintf(b, "It %s %d non-empty cell(s) and %d formula(s) with it.\n", took, cells, formulas)
	}
	if shifted {
		fmt.Fprintf(b, "Addresses after the band %s: a checkpoint or an address from before this call no longer "+
			"points where it did.\n", moved)
	}
}

// ClearPreview and ClearDone render a clear. Both name what the range
// holds, because a clear is the one write whose result is emptiness and
// therefore says nothing on its own.
//
// Neither mentions notes or validation rules: the API keeps them, in its
// own words "all other properties of the cell (such as formatting, data
// validation, etc..) are kept". A write is what removes those, and a
// clear that claimed to would be warning about the wrong tool.
func ClearPreview(rangeA1 string, c grid.Counts) string {
	return fmt.Sprintf("Dry run: nothing was sent. Clearing %s would remove %d non-empty cell(s), %d of them formulas.\n",
		rangeA1, c.NonEmpty, c.Formulas)
}

// ClearDone renders what a clear removed.
func ClearDone(rangeA1 string, c grid.Counts) string {
	return fmt.Sprintf("Cleared %s: %d non-empty cell(s), %d of them formulas. "+
		"Formatting, notes and validation rules were left alone.\n", rangeA1, c.NonEmpty, c.Formulas)
}
