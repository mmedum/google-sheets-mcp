// Package plan turns a write into something that can be checked before
// it is sent: the guard that reads what a write would destroy, and the
// typed builders that compile an op into the batchUpdate union.
//
// Nothing here touches the network. The guard is a pure function over
// the rectangle that was read and the values about to replace it, which
// is what lets `dry_run` be the same code path as the real write with
// the request never built.
//
// Sheets has no undo through the API, so a refusal here is the only one
// there is.
package plan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// MaxCellChars is the longest string one cell holds.
//
// Verified live: 60 004 characters is refused with `400
// INVALID_ARGUMENT`, "Your input contains more than the maximum of 50000
// characters in a single cell", under both input options. Checked here
// so the caller is told which cell is too long, rather than having the
// whole write refused with a message that names none of them.
const MaxCellChars = 50000

// Ack are the acknowledgements the caller passed. Each one exists
// because the thing it permits is invisible in a values read: a formula
// renders as its result, and a formula that fetches a URL renders as
// whatever came back.
type Ack struct {
	Overwrite             bool
	OverwriteFormulas     bool
	AllowExternalFormulas bool
}

// maxNamed is how many addresses a finding lists before it starts
// counting instead. Enough to go and look at, short enough to read.
const maxNamed = 10

// Cells is a set of addresses that names a few and counts the rest.
type Cells struct {
	Named []string
	Total int
}

// Add records one address.
func (c *Cells) Add(addr string) {
	c.Total++
	if len(c.Named) < maxNamed {
		c.Named = append(c.Named, addr)
	}
}

// Any reports whether anything was recorded.
func (c Cells) Any() bool { return c.Total > 0 }

// AddCell records one cell, naming it only if this set still has room.
//
// The address is what costs: every set caps at ten names and counts the
// rest, so on a 5 000-cell write all but ten were formatted and thrown
// away — 28 000 allocations, on the path of every guarded write. Taking
// the coordinates instead of the string is what makes the formatting
// conditional without the caller knowing the cap.
//
// The caller used to ask, through a Full method and a condition naming
// every set the cell could land in. That worked and it put the cap in
// two places: a set added later without its clause would be counted
// correctly and never named, and the failure is a message going quiet.
// It also had to be got right twice — the first attempt asked whether
// *any* set was full, which never fires on a rectangle of plain values,
// and the benchmark is what said so.
func (c *Cells) AddCell(g *grid.Grid, i, j int) {
	c.Total++
	if len(c.Named) < maxNamed {
		c.Named = append(c.Named, g.Address(i, j))
	}
}

// Merge folds another set into this one, keeping the cap.
//
// Here rather than at the caller, because "name a few and count the
// rest" is this type's rule: the version outside it had to add the
// names back one at a time and then patch Total by hand to undo the cap
// Add applies, which is a caller knowing how Add works.
func (c *Cells) Merge(o Cells) {
	for _, name := range o.Named {
		c.Add(name)
	}
	// The names are capped and the totals are not, so what is left is
	// the count of everything Add would have refused to name.
	c.Total += o.Total - len(o.Named)
}

// verb agrees with how many cells were recorded. "A1 are not empty" is
// the kind of sentence that makes a careful message read as a template.
func (c Cells) verb(singular, plural string) string {
	if c.Total == 1 {
		return singular
	}
	return plural
}

// String lists the addresses, saying how many were not listed.
func (c Cells) String() string {
	if c.Total == 0 {
		return ""
	}
	s := strings.Join(c.Named, ", ")
	if c.Total > len(c.Named) {
		s += fmt.Sprintf(" and %d more", c.Total-len(c.Named))
	}
	return s
}

// Report is everything the guard found in a write's target.
//
// It is the same value behind a refusal and behind a dry run: one
// describes what stopped the write, the other what would have.
type Report struct {
	// Pivots are the cells that anchor a pivot table. A write over one
	// replaces the pivot outright and takes its whole output, and the
	// API's reply says nothing (spike M) — so it is worth naming rather
	// than counting among the non-empty cells it is also in.
	//
	// Only the anchor. A pivot's output cells are ordinary computed
	// values on the wire, and finding the anchor from one of them would
	// mean reading up and left of every guarded write. §17a records that.
	Pivots Cells
	// NonEmpty and Formulas are what a value write destroys. Formulas
	// are counted in NonEmpty too: a formula is a non-empty cell.
	NonEmpty Cells
	Formulas Cells
	// Notes and Validation are invisible in a values read and survive a
	// value write, so they are reported as surviving: a rule that still
	// applies to a value the caller has just replaced is worth knowing
	// about.
	Notes      Cells
	Validation Cells
	// Merges are merged ranges the write covers only part of. Writing
	// into one is refused outright: the API applies the write to the
	// merge's anchor cell and silently ignores the rest.
	Merges []a1.Rect
	// Protected are protections over the target that this account may
	// not edit.
	Protected []grid.Protection
	// Fetching are formulas being written that take an arbitrary URL, so
	// Google would fetch it from its own servers with whatever the
	// formula puts in the query string. CrossSpreadsheet are
	// IMPORTRANGE, which exfiltrates nothing and pulls another
	// spreadsheet's data into this one.
	Fetching         Cells
	CrossSpreadsheet Cells
	// TooLong are cells whose text is past MaxCellChars.
	TooLong Cells
	// Discarded are cells a merge would throw away. Sheets keeps the
	// top-left value of a merge and drops the rest without saying so,
	// which is the one formatting operation that loses data.
	Discarded Cells
	// NoteReplaced are cells whose note a note op would overwrite. A
	// note is invisible in a values read, so a caller replacing one
	// cannot have seen what was there.
	NoteReplaced Cells
	// Formatted are cells carrying a format of their own, which
	// clear_format removes. Cells that only inherit the sheet's defaults
	// are not counted: clearing takes nothing from them.
	Formatted Cells
}

// Blocker is one reason a write is refused, and the argument that would
// allow it. An empty Allow means nothing does.
type Blocker struct {
	Why   string
	Allow string
}

// Blockers is what stands between this report and the write, given the
// acknowledgements the caller passed.
//
// Order matters: the refusals nothing can acknowledge come first, so a
// caller who reads only the first line is not told to pass a flag that
// would not have helped.
func (r Report) Blockers(ack Ack) []Blocker {
	var out []Blocker

	for _, p := range r.Protected {
		why := fmt.Sprintf("%s is protected", a1.FormatRect(p.Rect))
		if p.Description != "" {
			why += fmt.Sprintf(" (%q)", p.Description)
		}
		out = append(out, Blocker{Why: why + " and this account may not edit it"})
	}
	for _, m := range r.Merges {
		out = append(out, Blocker{Why: fmt.Sprintf(
			"%s is a merged range and the write covers only part of it; Sheets would apply the write to its top-left cell and drop the rest",
			a1.FormatRect(m))})
	}
	if r.TooLong.Any() {
		out = append(out, Blocker{Why: fmt.Sprintf(
			"%s %s more than %d characters, which is the most one cell takes", r.TooLong,
			r.TooLong.verb("holds", "hold"), MaxCellChars)})
	}

	if r.Fetching.Any() && !ack.AllowExternalFormulas {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s would hold a formula that fetches a URL from Google's servers, "+
				"which can carry this spreadsheet's own data in its query string", r.Fetching),
			Allow: "allow_external_formulas",
		})
	}
	if r.CrossSpreadsheet.Any() && !ack.AllowExternalFormulas {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s would hold an IMPORTRANGE, which pulls another spreadsheet's data into this one "+
				"and embeds that spreadsheet's id", r.CrossSpreadsheet),
			Allow: "allow_external_formulas",
		})
	}

	if r.Discarded.Any() && !ack.Overwrite {
		out = append(out, Blocker{
			Why: fmt.Sprintf("merging would keep the top-left value and discard %s, which %s not empty",
				r.Discarded, r.Discarded.verb("is", "are")),
			Allow: "overwrite",
		})
	}
	if r.NoteReplaced.Any() && !ack.Overwrite {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s already %s a note, which no values read shows, so this would replace or remove "+
				"something you have not seen", r.NoteReplaced, r.NoteReplaced.verb("has", "have")),
			Allow: "overwrite",
		})
	}
	if r.Formatted.Any() && !ack.Overwrite {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s %s formatting of its own, which clearing removes", r.Formatted,
				r.Formatted.verb("has", "have")),
			Allow: "overwrite",
		})
	}

	// The formula case first, and separately, because it is the one that
	// loses work invisibly: a formula and its result render identically,
	// so a caller who has only read the values does not know one is
	// there.
	if r.Formulas.Any() && !ack.OverwriteFormulas {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s %s a formula, which a value write replaces with a plain value", r.Formulas,
				r.Formulas.verb("holds", "hold")),
			Allow: "overwrite_formulas (and overwrite)",
		})
	}
	if r.Pivots.Any() && !ack.Overwrite {
		out = append(out, Blocker{
			Why: fmt.Sprintf("%s %s a pivot table, and a write there replaces it and clears everything it draws",
				r.Pivots, r.Pivots.verb("anchors", "anchor")),
			Allow: "overwrite",
		})
	}
	if r.NonEmpty.Any() && !ack.Overwrite {
		out = append(out, Blocker{
			Why:   fmt.Sprintf("%s %s not empty", r.NonEmpty, r.NonEmpty.verb("is", "are")),
			Allow: "overwrite",
		})
	}
	return out
}

// Keeps lists what stays behind that no values read shows.
//
// Verified live: values.update leaves a cell's note and its validation
// rule alone, so an earlier version of this — which called them lost and
// said so in every result — was telling the caller the opposite of what
// happened. They are still worth naming: a validation rule that survives
// still applies to the value that just replaced the old one.
func (r Report) Keeps() []string {
	var out []string
	if r.Notes.Any() {
		out = append(out, fmt.Sprintf("notes on %s", r.Notes))
	}
	if r.Validation.Any() {
		out = append(out, fmt.Sprintf("data validation on %s", r.Validation))
	}
	return out
}

// externalFetch takes an arbitrary URL, which Google requests from its
// own servers. The reference for IMPORTXML is explicit that its argument
// is "The URL of the page to examine, including protocol", and ENCODEURL
// makes putting the sheet's own values in a query string convenient.
//
// externalRange is IMPORTRANGE, which takes a spreadsheet URL rather
// than an arbitrary one. It exfiltrates nothing; its risk is the other
// direction, and it embeds another spreadsheet's id in a string.
var (
	externalFetch = regexp.MustCompile(`(?i)\b(IMPORTXML|IMPORTDATA|IMPORTHTML|IMPORTFEED|IMAGE|HYPERLINK)\s*\(`)
	externalRange = regexp.MustCompile(`(?i)\bIMPORTRANGE\s*\(`)
)

// Check reads a write's target and reports everything in the way.
//
// g is the rectangle as it stands, read with the field mask that carries
// what a values read cannot show. values is what would replace it, in
// the same shape.
//
// formulasEvaluated says whether a string beginning with "=" becomes a
// formula: verified live, RAW stores "=1+2" as the four-character
// string, so an external-formula check under RAW would refuse text that
// Sheets never evaluates. It is a boolean rather than the input option's
// name because a second copy of "USER_ENTERED" in this package would
// have to stay equal to gapi's with nothing keeping it so — and if it
// drifted, the external-formula gate would open silently.
func Check(g *grid.Grid, values [][]any, formulasEvaluated bool) Report {
	r := CheckDestination(g)
	CheckPartialMerges(&r, g)
	for i, row := range g.Cells {
		for j, cell := range row {
			// What this cell is. AddCell decides whether the address is
			// worth formatting, so nothing here knows the cap.
			//
			// HasFormula rather than the kind: a formula that evaluated
			// to an error is KindError, and a write over one would
			// otherwise need only `overwrite`.
			if cell.HasFormula() {
				r.Formulas.AddCell(g, i, j)
				r.NonEmpty.AddCell(g, i, j)
			} else if !cell.Empty() {
				r.NonEmpty.AddCell(g, i, j)
			}
			if cell.Pivot {
				r.Pivots.AddCell(g, i, j)
			}
			if cell.Note != "" {
				r.Notes.AddCell(g, i, j)
			}
			if cell.Validation != "" {
				r.Validation.AddCell(g, i, j)
			}
		}
	}
	CheckValues(&r, values, formulasEvaluated, func(i, j int) string {
		return g.Address(i, j)
	})
	return r
}

// CheckDestination is what refuses any request touching a rectangle,
// whatever it is and whatever the caller acknowledged: a protected range
// this account may not edit.
//
// Separate from Check because clear_values needs exactly this and none
// of the rest. It used to call Check and switch the other findings off
// with acknowledgements it does not offer — which worked, and meant any
// blocker added later applied to a clear silently.
//
// The partial-merge scan used to be here too, and phase 2 brought that
// mistake back in a new shape: two of the three callers ran this and
// then deleted r.Merges, because naming a range or setting a validation
// rule does not care about a merge. A finding two thirds of the callers
// throw away is a rule decided after the fact at each call site — so it
// is CheckPartialMerges now, and the callers that write values into
// cells ask for it.
func CheckDestination(g *grid.Grid) Report {
	var r Report
	for _, p := range g.Protected {
		// WarningOnly protection is a nudge in the interface and does
		// not refuse an API write, so it is not treated as one here.
		if !p.CanEdit && !p.WarningOnly {
			r.Protected = append(r.Protected, p)
		}
	}
	return r
}

// CheckPartialMerges records the merged ranges a write covers only part
// of, which is a refusal nothing can acknowledge.
//
// It applies to whatever writes into the cells: Sheets applies such a
// write to the merge's anchor and silently ignores the rest. It does not
// apply to what is merely attached to the range, which is why it is
// asked for rather than always produced.
func CheckPartialMerges(r *Report, g *grid.Grid) {
	for _, m := range g.Merges {
		// A merge wholly inside the target is replaced along with
		// everything else. One the write cuts across is the problem.
		if !g.Rect.Contains(m) {
			r.Merges = append(r.Merges, m)
		}
	}
}

// CheckValues is the half of the guard that reads only what is being
// sent: a string too long for a cell, and a formula that reaches outside
// the spreadsheet.
//
// Separate from Check because an append has no destination to read. Its
// rows go wherever Google's table detection puts them, which is not
// knowable before the call (verified live), so it labels its findings by
// position in the array rather than by an address it would be guessing.
func CheckValues(r *Report, values [][]any, formulasEvaluated bool, label func(i, j int) string) {
	for i, row := range values {
		for j, v := range row {
			s, ok := v.(string)
			if !ok {
				continue
			}
			// The tests first, the address after. Naming a cell costs a
			// formatted string and almost every value is unremarkable,
			// so building one before knowing whether anything is wrong
			// with the value was half the guard's allocations.
			//
			// An empty label still means a cell outside the grid and
			// still records nothing; it is now asked for only when there
			// would be something to record.
			tooLong := len(s) > MaxCellChars
			var fetching, cross bool
			if formulasEvaluated && strings.HasPrefix(strings.TrimSpace(s), "=") {
				fetching = externalFetch.MatchString(s)
				cross = externalRange.MatchString(s)
			}
			if !tooLong && !fetching && !cross {
				continue
			}
			at := label(i, j)
			if at == "" {
				continue
			}
			if tooLong {
				r.TooLong.Add(at)
			}
			if fetching {
				r.Fetching.Add(at)
			}
			if cross {
				r.CrossSpreadsheet.Add(at)
			}
		}
	}
}

// Position labels a cell by where it sits in the values array, for a
// write whose destination the server cannot know in advance.
func Position(i, j int) string {
	return fmt.Sprintf("row %d, column %d of the values", i+1, j+1)
}

// CheckMerge records what a merge would throw away.
//
// Sheets keeps one value per merged block — the top-left of it — and
// drops the others with nothing in the response saying so. Which cell
// survives depends on the merge type, so the anchor is worked out here
// rather than assumed to be the rectangle's corner: merging by rows
// keeps the leftmost cell of every row, and by columns the top cell of
// every column.
func CheckMerge(r *Report, g *grid.Grid, kind string) {
	for i, row := range g.Cells {
		for j, cell := range row {
			if cell.Empty() || anchors(kind, i, j) {
				continue
			}
			r.Discarded.Add(g.Address(i, j))
		}
	}
}

// anchors reports whether the cell at this position in the rectangle is
// the one its merge keeps.
func anchors(kind string, i, j int) bool {
	switch kind {
	case gsheets.MergeRows:
		return j == 0
	case gsheets.MergeColumns:
		return i == 0
	default:
		return i == 0 && j == 0
	}
}

// CheckNoteReplace records the notes a note op would overwrite or
// remove.
//
// Both, and an earlier version returned early for a removal on the
// grounds that removing is what the caller asked for. That was wrong for
// the reason the guard exists at all: a note is invisible in a values
// read, so a caller clearing notes across a column has not seen what is
// in them either. Setting the note that is already there changes
// nothing and is not held back.
//
// Called only when the request carries a note op, so "no note op" is the
// caller's business rather than an empty string standing in for it.
func CheckNoteReplace(r *Report, g *grid.Grid, note string) {
	for i, row := range g.Cells {
		for j, cell := range row {
			if cell.Note != "" && cell.Note != note {
				r.NoteReplaced.Add(g.Address(i, j))
			}
		}
	}
}

// CheckClearFormat records the cells whose own formatting a clear would
// remove.
//
// The cell's own format, not the one that applies to it. A cell showing
// the sheet's default font has nothing to lose, and counting it would
// make the refusal fire on every range in every spreadsheet, which is a
// guard nobody reads.
func CheckClearFormat(r *Report, g *grid.Grid) {
	for i, row := range g.Cells {
		for j, cell := range row {
			if cell.Format != nil {
				r.Formatted.Add(g.Address(i, j))
			}
		}
	}
}

// Merge folds another report's findings into this one.
//
// Every field, and next to the struct rather than in whichever package
// needed it first. The service had a four-field version of this for a
// paste's destination, so a paste onto cells carrying notes or
// validation rules reported neither — and a field added to Report later
// would have been dropped there in silence.
func (r *Report) Merge(o Report) {
	r.NonEmpty.Merge(o.NonEmpty)
	r.Pivots.Merge(o.Pivots)
	r.Formulas.Merge(o.Formulas)
	r.Notes.Merge(o.Notes)
	r.Validation.Merge(o.Validation)
	r.Fetching.Merge(o.Fetching)
	r.CrossSpreadsheet.Merge(o.CrossSpreadsheet)
	r.TooLong.Merge(o.TooLong)
	r.Discarded.Merge(o.Discarded)
	r.NoteReplaced.Merge(o.NoteReplaced)
	r.Formatted.Merge(o.Formatted)
	r.Merges = append(r.Merges, o.Merges...)
	r.Protected = append(r.Protected, o.Protected...)
}
