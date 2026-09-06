package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// The words for a structural change, in one place.
//
// They used to be composed in the service, which built a fragment
// ("insert 2 row(s) before row 2 on %q") that this package then
// capitalised and wrapped. Phrasing decided above the renderer is
// phrasing the goldens cannot cover, and the same fragment appeared in a
// refusal, a preview and a result, where it had to agree with itself.
// So the service passes the parts and the template lives here.

// Band names a run of rows or columns the way a message does.
type Band struct {
	// Rows says the band runs down rather than across.
	Rows bool
	// First and Last are one-based and inclusive, as A1 counts.
	First int
	Last  int
}

// Count is how many rows or columns the band covers.
func (b Band) Count() int { return b.Last - b.First + 1 }

// Unit names what the band is made of, for a message that counts them.
func (b Band) Unit() string {
	if b.Rows {
		return "row(s)"
	}
	return "column(s)"
}

// Start names where the band begins, the way a person says it.
func (b Band) Start() string {
	if b.Rows {
		return "row " + strconv.Itoa(b.First)
	}
	name, _ := a1.ColumnName(b.First)
	return "column " + name
}

// String renders a band the way it was given.
func (b Band) String() string {
	if b.Rows {
		return fmt.Sprintf("rows %d:%d", b.First, b.Last)
	}
	first, _ := a1.ColumnName(b.First)
	last, _ := a1.ColumnName(b.Last)
	return fmt.Sprintf("columns %s:%s", first, last)
}

// DimensionAct is a change to rows or columns, in parts.
type DimensionAct struct {
	Action string
	Sheet  string
	Band   Band
	// To is where a move puts the band, Pixels the new size for a
	// resize. Each is read only by the action that uses it.
	To     int
	Pixels int
	// Cells and Formulas are what a delete would take with it.
	Cells    int
	Formulas int
	// Shifted says addresses after the band moved.
	Shifted bool
}

// Phrase says what the act does, in the lower case a sentence needs
// around it.
func (a DimensionAct) Phrase() string {
	b := a.Band
	switch a.Action {
	case "insert":
		return fmt.Sprintf("insert %d %s before %s on %q", b.Count(), b.Unit(), b.Start(), a.Sheet)
	case "delete":
		return fmt.Sprintf("delete %s on %q", b, a.Sheet)
	case "move":
		return fmt.Sprintf("move %s on %q to position %d", b, a.Sheet, a.To)
	case "resize":
		return fmt.Sprintf("set %s on %q to %d pixels", b, a.Sheet, a.Pixels)
	case "auto_resize":
		return fmt.Sprintf("size %s on %q to fit their contents", b, a.Sheet)
	case "group":
		return fmt.Sprintf("group %s on %q", b, a.Sheet)
	default:
		return fmt.Sprintf("ungroup %s on %q", b, a.Sheet)
	}
}

// DimensionPreview and DimensionDone render a change to rows or columns.
// Both say when addresses moved, because a checkpoint or an address the
// caller is holding no longer points where it did.
func DimensionPreview(a DimensionAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would %s.\n", a.Phrase())
	dimensionNotes(&b, a, "would take", "would move")
	return b.String()
}

// DimensionDone renders what the change did.
func DimensionDone(a DimensionAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %s.\n", upperFirst(a.Phrase()))
	dimensionNotes(&b, a, "took", "moved")
	return b.String()
}

func dimensionNotes(b *strings.Builder, a DimensionAct, took, moved string) {
	if a.Cells > 0 || a.Formulas > 0 {
		fmt.Fprintf(b, "It %s %d non-empty cell(s) and %d formula(s) with it.\n", took, a.Cells, a.Formulas)
	}
	if a.Shifted {
		fmt.Fprintf(b, "Addresses after the band %s: a checkpoint or an address from before this call no longer "+
			"points where it did.\n", moved)
	}
}

// SheetAct is a change to a sheet, in parts.
type SheetAct struct {
	Action string
	// Sheet is the tab acted on, and Title the name the act gives it:
	// the new sheet for add, the new name for rename, the copy's name
	// for duplicate.
	Sheet string
	Title string
	// Index is a position for reorder, Rows and Cols the new size for
	// resize or the frozen counts for freeze.
	Index      int
	Rows, Cols int
	// Destination is the other spreadsheet for copy_to, named the way
	// the caller named it.
	Destination string
	// Colour is a hex colour or empty, which clears.
	Colour string
}

// Phrase says what the act does.
func (a SheetAct) Phrase() string {
	switch a.Action {
	case "add":
		return fmt.Sprintf("add a sheet called %q", a.Title)
	case "rename":
		return fmt.Sprintf("rename %q to %q", a.Sheet, a.Title)
	case "duplicate":
		return fmt.Sprintf("duplicate %q", a.Sheet)
	case "copy_to":
		return fmt.Sprintf("copy %q into the spreadsheet %q", a.Sheet, a.Destination)
	case "hide":
		return fmt.Sprintf("hide %q", a.Sheet)
	case "unhide":
		return fmt.Sprintf("unhide %q", a.Sheet)
	case "reorder":
		return fmt.Sprintf("move %q to position %d", a.Sheet, a.Index)
	case "resize":
		return fmt.Sprintf("resize %q to %d rows and %d columns", a.Sheet, a.Rows, a.Cols)
	case "freeze":
		return fmt.Sprintf("freeze %d row(s) and %d column(s) on %q", a.Rows, a.Cols, a.Sheet)
	default:
		if a.Colour == "" {
			return fmt.Sprintf("clear the tab colour of %q", a.Sheet)
		}
		return fmt.Sprintf("set the tab colour of %q to %s", a.Sheet, a.Colour)
	}
}

// SheetPreview renders what a sheet change would do.
func SheetPreview(a SheetAct) string {
	return fmt.Sprintf("Dry run: nothing was sent. This would %s.\n", a.Phrase())
}

// SheetDone renders what it did, and lists the sheets afterwards so the
// next call can name one exactly.
func SheetDone(a SheetAct, sheets []string) string {
	return fmt.Sprintf("Done: %s.\n  the spreadsheet now has %d sheet(s): %s\n",
		upperFirst(a.Phrase()), len(sheets), strings.Join(sheets, ", "))
}

// CopyDone renders a copy into another spreadsheet, which is the one
// action whose result is in a file this one cannot list.
func CopyDone(a SheetAct, copied string) string {
	return fmt.Sprintf("Done: %s.\n  the copy is called %q, in the destination spreadsheet\n",
		upperFirst(a.Phrase()), copied)
}

// upperFirst capitalises a phrase that was written to sit mid-sentence.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
