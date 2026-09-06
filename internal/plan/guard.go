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
	for i, row := range g.Cells {
		for j, cell := range row {
			addr := g.Address(i, j)
			// HasFormula rather than the kind: a formula that evaluated
			// to an error is KindError, and a write over one would
			// otherwise need only `overwrite`.
			switch {
			case cell.HasFormula():
				r.Formulas.Add(addr)
				r.NonEmpty.Add(addr)
			case !cell.Empty():
				r.NonEmpty.Add(addr)
			}
			if cell.Note != "" {
				r.Notes.Add(addr)
			}
			if cell.Validation != "" {
				r.Validation.Add(addr)
			}
		}
	}
	CheckValues(&r, values, formulasEvaluated, func(i, j int) string {
		return g.Address(i, j)
	})
	return r
}

// CheckDestination is what refuses any write to a rectangle, whatever
// the write is and whatever the caller acknowledged: a protected range
// this account may not edit, and a merge the write would cut across.
//
// Separate from Check because clear_values needs exactly these and none
// of the rest. It used to call Check and switch the other findings off
// with acknowledgements it does not offer — which worked, and meant any
// blocker added later applied to a clear silently.
func CheckDestination(g *grid.Grid) Report {
	var r Report
	for _, m := range g.Merges {
		// A merge wholly inside the target is replaced along with
		// everything else. One the write cuts across is the problem.
		if !g.Rect.Contains(m) {
			r.Merges = append(r.Merges, m)
		}
	}
	for _, p := range g.Protected {
		// WarningOnly protection is a nudge in the interface and does
		// not refuse an API write, so it is not treated as one here.
		if !p.CanEdit && !p.WarningOnly {
			r.Protected = append(r.Protected, p)
		}
	}
	return r
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
			at := label(i, j)
			if at == "" {
				continue
			}
			if len(s) > MaxCellChars {
				r.TooLong.Add(at)
			}
			if !formulasEvaluated || !strings.HasPrefix(strings.TrimSpace(s), "=") {
				continue
			}
			if externalFetch.MatchString(s) {
				r.Fetching.Add(at)
			}
			if externalRange.MatchString(s) {
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
