package plan_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

// evaluated and literal are the two input options, as the guard sees
// them: whether a string beginning with "=" becomes a formula.
const (
	evaluated = true
	literal   = false
)

// target builds a grid over A1:B2 with the cells given, in row order.
func target(cells ...grid.Cell) *grid.Grid {
	g := &grid.Grid{
		Sheet: "Vandel", Rect: a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 2, LastCol: 2},
		Cells: [][]grid.Cell{{{Kind: grid.KindEmpty}, {Kind: grid.KindEmpty}}, {{Kind: grid.KindEmpty}, {Kind: grid.KindEmpty}}},
	}
	for i, c := range cells {
		g.Cells[i/2][i%2] = c
	}
	return g
}

func blockerText(r plan.Report, ack plan.Ack) string {
	var parts []string
	for _, b := range r.Blockers(ack) {
		parts = append(parts, b.Why+"|"+b.Allow)
	}
	return strings.Join(parts, " ;; ")
}

// The guard's whole reason for existing: a formula and its result render
// identically, so overwriting one needs its own acknowledgement rather
// than being covered by the general one.
func TestFormulasNeedTheirOwnAcknowledgement(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindFormula, Formula: "=B1+1", Display: "3"})
	r := plan.Check(g, [][]any{{"x", "y"}, {"z", "w"}}, evaluated)

	if got := blockerText(r, plan.Ack{}); !strings.Contains(got, "A1") || !strings.Contains(got, "formulas") {
		t.Errorf("a formula in the target did not block the write: %q", got)
	}
	// overwrite alone is not enough.
	got := blockerText(r, plan.Ack{Overwrite: true})
	if !strings.Contains(got, "overwrite_formulas") {
		t.Errorf("overwrite alone allowed a write over a formula: %q", got)
	}
	if len(r.Blockers(plan.Ack{Overwrite: true, OverwriteFormulas: true})) != 0 {
		t.Error("both acknowledgements together still blocked the write")
	}
}

// The guard hole a live run found: a formula that evaluated to an error
// is KindError, so a check on the kind treated `=IMPORTRANGE(...)`
// showing #REF! as an ordinary value and let `overwrite` alone replace
// it.
func TestAnErroredFormulaStillNeedsOverwriteFormulas(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindError, Formula: `=IMPORTRANGE("x","y")`, Error: "#REF!", Display: "#REF!"})
	r := plan.Check(g, [][]any{{"x", "y"}, {"z", "w"}}, evaluated)
	if !r.Formulas.Any() {
		t.Fatal("an errored formula was not reported as a formula")
	}
	got := blockerText(r, plan.Ack{Overwrite: true})
	if !strings.Contains(got, "overwrite_formulas") {
		t.Errorf("overwrite alone replaced a formula that had errored: %q", got)
	}
}

func TestNonEmptyCellsNeedOverwrite(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindText, Display: "Plimth"})
	r := plan.Check(g, [][]any{{"x", "y"}, {"z", "w"}}, evaluated)
	if got := blockerText(r, plan.Ack{}); !strings.Contains(got, "not empty") || !strings.Contains(got, "overwrite") {
		t.Errorf("an occupied cell did not block the write: %q", got)
	}
	if n := len(r.Blockers(plan.Ack{Overwrite: true})); n != 0 {
		t.Errorf("overwrite left %d blocker(s)", n)
	}
	if r.NonEmpty.Total != 1 || r.NonEmpty.Named[0] != "A1" {
		t.Errorf("NonEmpty = %+v", r.NonEmpty)
	}
}

// A protected range and a partial merge are refusals nothing can
// acknowledge away, and they are listed first so a caller reading one
// line is not told to pass a flag that would not have helped.
func TestProtectionAndPartialMergeCannotBeAcknowledged(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindText, Display: "Plimth"})
	g.Protected = []grid.Protection{{
		Rect: a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 1, LastCol: 4}, Description: "heading row",
	}}
	g.Merges = []a1.Rect{{FirstRow: 2, FirstCol: 1, LastRow: 2, LastCol: 5}}
	r := plan.Check(g, [][]any{{"x", "y"}, {"z", "w"}}, evaluated)

	blockers := r.Blockers(plan.Ack{Overwrite: true, OverwriteFormulas: true})
	if len(blockers) != 2 {
		t.Fatalf("acknowledged everything and %d blocker(s) remain: %+v", len(blockers), blockers)
	}
	for _, b := range blockers {
		if b.Allow != "" {
			t.Errorf("%q offers %q as a way through, and there is none", b.Why, b.Allow)
		}
	}
	if !strings.Contains(blockers[0].Why, "heading row") {
		t.Errorf("the protection's description is not in the refusal: %q", blockers[0].Why)
	}
	if !strings.Contains(blockers[1].Why, "merged") || !strings.Contains(blockers[1].Why, "A2:E2") {
		t.Errorf("the merge refusal does not name the merge: %q", blockers[1].Why)
	}
}

// A merge wholly inside the target is replaced along with everything
// else, so it is not a refusal. Only one the write cuts across is.
func TestAMergeInsideTheTargetIsNotABlocker(t *testing.T) {
	g := target()
	g.Merges = []a1.Rect{{FirstRow: 1, FirstCol: 1, LastRow: 1, LastCol: 2}}
	r := plan.Check(g, [][]any{{"x", "y"}, {"z", "w"}}, evaluated)
	if len(r.Merges) != 0 {
		t.Errorf("a contained merge was reported as partial: %v", r.Merges)
	}
}

// Warning-only protection is a nudge in the interface and does not
// refuse an API write, so treating it as a refusal would block a write
// Google would have accepted.
func TestWarningOnlyProtectionDoesNotBlock(t *testing.T) {
	g := target()
	g.Protected = []grid.Protection{{
		Rect: a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 1, LastCol: 2}, WarningOnly: true,
	}}
	r := plan.Check(g, nil, evaluated)
	if len(r.Blockers(plan.Ack{})) != 0 {
		t.Error("warning-only protection blocked a write the API would have allowed")
	}
}

// A note and a validation rule are invisible in a values read and
// survive a value write, verified live. So they neither refuse the write
// nor are reported as lost — they are reported as still being there,
// because a rule that survives applies to the value that just replaced
// the old one.
func TestAnnotationsSurviveAndAreReportedAsSuch(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindEmpty, Note: "Quorbin reconciliation pending"},
		grid.Cell{Kind: grid.KindEmpty, Validation: "one of list: Skerry"})
	r := plan.Check(g, nil, evaluated)

	// Nothing here is a value, so nothing needs acknowledging.
	if n := len(r.Blockers(plan.Ack{})); n != 0 {
		t.Errorf("an annotation blocked a write into a cell with no value: %+v", r.Blockers(plan.Ack{}))
	}
	if r.NonEmpty.Any() {
		t.Errorf("a cell holding only an annotation counted as occupied: %v", r.NonEmpty)
	}
	keeps := strings.Join(r.Keeps(), " ")
	for _, want := range []string{"notes on A1", "validation on B1"} {
		if !strings.Contains(keeps, want) {
			t.Errorf("Keeps() = %q, missing %q", keeps, want)
		}
	}
}

// A formula that fetches a URL is an outbound request made by a machine
// the person cannot see, and IMPORTRANGE is the other direction. Both
// are gated, and each is named for what it is.
func TestExternalFormulasAreGated(t *testing.T) {
	g := target()
	r := plan.Check(g, [][]any{
		{`=IMPORTXML("https://example.test/"&ENCODEURL(A1),"//p")`, `=IMPORTRANGE("https://example.test/x","A1")`},
	}, evaluated)

	got := blockerText(r, plan.Ack{Overwrite: true, OverwriteFormulas: true})
	if !strings.Contains(got, "fetches a URL") || !strings.Contains(got, "A1") {
		t.Errorf("an IMPORTXML was not gated: %q", got)
	}
	if !strings.Contains(got, "IMPORTRANGE") || !strings.Contains(got, "B1") {
		t.Errorf("an IMPORTRANGE was not gated: %q", got)
	}
	if strings.Count(got, "allow_external_formulas") != 2 {
		t.Errorf("the two risks were not named separately: %q", got)
	}
	if n := len(r.Blockers(plan.Ack{AllowExternalFormulas: true})); n != 0 {
		t.Errorf("the acknowledgement left %d blocker(s)", n)
	}
}

// Under RAW nothing is evaluated: verified live, "=1+2" is stored as the
// four-character string. Gating it would refuse text Sheets never runs.
func TestExternalFormulasAreNotGatedUnderRaw(t *testing.T) {
	r := plan.Check(target(), [][]any{{`=IMPORTXML("https://example.test/","//p")`}}, literal)
	if r.Fetching.Any() {
		t.Error("a literal write was treated as writing a formula")
	}
}

// The cell limit is 50 000 characters, refused by Google with a message
// that names none of the cells. Checked here so the caller learns which.
func TestCellsPastTheCharacterLimitAreRefused(t *testing.T) {
	long := strings.Repeat("Quorbin", plan.MaxCellChars/7+1)
	r := plan.Check(target(), [][]any{{"short", long}}, evaluated)
	got := blockerText(r, plan.Ack{Overwrite: true, OverwriteFormulas: true, AllowExternalFormulas: true})
	if !strings.Contains(got, "B1") || !strings.Contains(got, "50000") {
		t.Errorf("an over-long cell was not refused by address: %q", got)
	}
}

// An append has no destination to read, so its findings are labelled by
// position rather than by an address the server would be guessing.
func TestCheckValuesLabelsByPosition(t *testing.T) {
	var r plan.Report
	plan.CheckValues(&r, [][]any{{"ok"}, {`=IMAGE("https://example.test/x")`}}, evaluated, plan.Position)
	if !strings.Contains(r.Fetching.String(), "row 2, column 1") {
		t.Errorf("Fetching = %q, want a position", r.Fetching.String())
	}
}

// A refusal names a few addresses and counts the rest: enough to go and
// look at, short enough to read.
func TestCellsNamesAFewAndCountsTheRest(t *testing.T) {
	var c plan.Cells
	for i := range 25 {
		c.Add(string(rune('A'+i%26)) + "1")
	}
	if c.Total != 25 {
		t.Errorf("Total = %d", c.Total)
	}
	if !strings.HasSuffix(c.String(), "and 15 more") {
		t.Errorf("String() = %q", c.String())
	}
}

// CheckDestination is what applies to any request touching a rectangle,
// and none of the refusals a caller acknowledges. Clear used to ask the
// full guard and switch the rest off with acknowledgements it does not
// offer, which would have applied a later blocker to a clear silently.
//
// A partial merge is not part of it. It refuses whatever writes into the
// cells, and the tools that only attach something to a range asked for
// it and then deleted it — a finding two thirds of the callers throw
// away is a rule decided at the call site.
func TestCheckDestinationIsOnlyTheUnacknowledgeableRefusals(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindFormula, Formula: "=B1+1", Display: "3"})
	g.Protected = []grid.Protection{{Rect: a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 1, LastCol: 4}}}
	g.Merges = []a1.Rect{{FirstRow: 2, FirstCol: 1, LastRow: 2, LastCol: 5}}

	r := plan.CheckDestination(g)
	// The cell-level findings are not its business, so nothing has to be
	// acknowledged away to see the one that is.
	if r.NonEmpty.Any() || r.Formulas.Any() {
		t.Errorf("CheckDestination looked at the cells: %+v", r)
	}
	if len(r.Merges) != 0 {
		t.Errorf("CheckDestination reported a partial merge nobody asked it about: %+v", r.Merges)
	}
	blockers := r.Blockers(plan.Ack{})
	if len(blockers) != 1 {
		t.Fatalf("%d blocker(s) over one protection: %+v", len(blockers), blockers)
	}
	// Asked for, it is there — and it is a refusal nothing acknowledges.
	withMerges := plan.CheckDestination(g)
	plan.CheckPartialMerges(&withMerges, g)
	if len(withMerges.Blockers(plan.Ack{})) != 2 {
		t.Errorf("the partial merge did not block: %+v", withMerges.Merges)
	}
	for _, b := range blockers {
		if b.Allow != "" {
			t.Errorf("%q offers a way through, and there is none", b.Why)
		}
	}
	// And the full guard still reports everything, so the split did not
	// lose a refusal.
	full := plan.Check(g, nil, evaluated)
	if len(full.Blockers(plan.Ack{})) != 4 {
		t.Errorf("Check lost a refusal after the split: %+v", full.Blockers(plan.Ack{}))
	}
}

func TestEmptyReportBlocksNothing(t *testing.T) {
	r := plan.Check(target(), nil, evaluated)
	if len(r.Blockers(plan.Ack{})) != 0 || len(r.Keeps()) != 0 {
		t.Error("an empty target refused a write")
	}
}

// A merge keeps the top-left value of each merged block and drops the
// rest, with nothing in the response saying so. Which cell survives
// depends on the merge type, and getting that wrong would name the wrong
// cells as lost — or say nothing was.
func TestAMergeReportsWhatItWouldDiscard(t *testing.T) {
	full := target(
		grid.Cell{Kind: grid.KindText, Display: "Quorbin"}, grid.Cell{Kind: grid.KindText, Display: "Vandel"},
		grid.Cell{Kind: grid.KindText, Display: "Skerry"}, grid.Cell{Kind: grid.KindText, Display: "Plimth"},
	)
	var all plan.Report
	plan.CheckMerge(&all, full, gsheets.MergeAll)
	if all.Discarded.Total != 3 {
		t.Errorf("merging all of A1:B2 would discard %d cells, want 3", all.Discarded.Total)
	}
	// Merging by rows keeps the leftmost cell of every row, so only the
	// right-hand column goes.
	var rows plan.Report
	plan.CheckMerge(&rows, full, gsheets.MergeRows)
	if rows.Discarded.Total != 2 || !strings.Contains(rows.Discarded.String(), "B1") {
		t.Errorf("merging by rows would discard %v", rows.Discarded)
	}
	// And by columns, the top cell of every column survives.
	var cols plan.Report
	plan.CheckMerge(&cols, full, gsheets.MergeColumns)
	if cols.Discarded.Total != 2 || !strings.Contains(cols.Discarded.String(), "A2") {
		t.Errorf("merging by columns would discard %v", cols.Discarded)
	}
	if got := blockerText(all, plan.Ack{}); !strings.Contains(got, "overwrite") {
		t.Errorf("a discarding merge did not say what would allow it: %q", got)
	}
	if len(all.Blockers(plan.Ack{Overwrite: true})) != 0 {
		t.Error("overwrite did not allow a discarding merge")
	}
}

// An empty rectangle has nothing to lose, so the guard stays out of the
// way: a merge over blank cells is the commonest one there is.
func TestMergingEmptyCellsIsNotBlocked(t *testing.T) {
	var r plan.Report
	plan.CheckMerge(&r, target(), gsheets.MergeAll)
	if r.Discarded.Any() {
		t.Errorf("an empty merge reported %v", r.Discarded)
	}
}

// A note is invisible in a values read, so a caller replacing one cannot
// have seen what was there.
func TestReplacingANoteNeedsAcknowledging(t *testing.T) {
	g := target(grid.Cell{Kind: grid.KindText, Display: "Quorbin", Note: "check with Vandel"})
	var r plan.Report
	plan.CheckNoteReplace(&r, g, "new note")
	if r.NoteReplaced.Total != 1 {
		t.Fatalf("replacing a note reported %v", r.NoteReplaced)
	}
	if got := blockerText(r, plan.Ack{}); !strings.Contains(got, "A1") {
		t.Errorf("the refusal does not name the cell: %q", got)
	}
	if len(r.Blockers(plan.Ack{Overwrite: true})) != 0 {
		t.Error("overwrite did not allow replacing a note")
	}

	// Removing is the same loss: a note is invisible in a values read, so
	// a caller clearing notes across a column has not seen them either.
	var removing plan.Report
	plan.CheckNoteReplace(&removing, g, "")
	if removing.NoteReplaced.Total != 1 {
		t.Error("removing a note was not counted, and it takes something no read would have shown")
	}
	// Setting the note that is already there changes nothing, so it is
	// not held back.
	var same plan.Report
	plan.CheckNoteReplace(&same, g, "check with Vandel")
	if same.NoteReplaced.Any() {
		t.Error("setting the note already there was treated as a replacement")
	}
	// And a cell with no note has nothing to lose either way.
	var blank plan.Report
	plan.CheckNoteReplace(&blank, target(grid.Cell{Kind: grid.KindText, Display: "Quorbin"}), "")
	if blank.NoteReplaced.Any() {
		t.Error("clearing notes from cells that have none was refused")
	}
}

// Clearing removes the cell's own format and nothing else, so a cell
// showing only the sheet's defaults has nothing to lose. Counting those
// would make the refusal fire on every range in every spreadsheet.
func TestClearingFormatCountsOnlyTheCellsOwnFormat(t *testing.T) {
	// A1 was given a format of its own; B1 carries none and only shows
	// the sheet's defaults, which a clear takes nothing from.
	g := target(
		grid.Cell{Kind: grid.KindText, Display: "Quorbin", Format: &gsheets.CellFormat{}},
		grid.Cell{Kind: grid.KindText, Display: "Vandel"},
	)
	var r plan.Report
	plan.CheckClearFormat(&r, g)
	if r.Formatted.Total != 1 || !strings.Contains(r.Formatted.String(), "A1") {
		t.Errorf("clearing reported %v, and only A1 carries a format of its own", r.Formatted)
	}
	if len(r.Blockers(plan.Ack{Overwrite: true})) != 0 {
		t.Error("overwrite did not allow a clear")
	}
}
