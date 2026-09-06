package plan_test

import (
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

var whole = a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 3, LastCol: 3}

// The coercion table as the live probe recorded it. Sent on the left,
// stored on the right, and the pair is what a caller has to be shown:
// the string "7" and the number 7 print the same and are not the same
// cell.
func TestDiffReportsWhatGoogleStored(t *testing.T) {
	sent := [][]any{{"007", "$100.15", "2026-09-05"}}
	stored := [][]any{{float64(7), float64(100.15), float64(46270)}}

	got := plan.Diff(whole, sent, stored)
	if len(got) != 3 {
		t.Fatalf("%d change(s) from three coerced values: %+v", len(got), got)
	}
	for i, want := range []plan.Change{
		{Address: "A1", Col: 0, Sent: "007", SentKind: "text", Stored: "7", Kind: "number"},
		{Address: "B1", Col: 1, Sent: "$100.15", SentKind: "text", Stored: "100.15", Kind: "number"},
		{Address: "C1", Col: 2, Sent: "2026-09-05", SentKind: "text", Stored: "46270", Kind: "number"},
	} {
		if got[i] != want {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want)
		}
	}
}

// A value Google kept as it was sent is not a change, whatever its type.
func TestDiffIsSilentWhenNothingChanged(t *testing.T) {
	sent := [][]any{{"Plimth", float64(42), true}}
	if got := plan.Diff(whole, sent, sent); len(got) != 0 {
		t.Errorf("unchanged values reported as changes: %+v", got)
	}
}

// The string "7" and the number 7 render identically, so a diff over
// text alone would report nothing for the one coercion a product code
// cares about most.
func TestDiffComparesKindAsWellAsText(t *testing.T) {
	got := plan.Diff(whole, [][]any{{"7"}}, [][]any{{float64(7)}})
	if len(got) != 1 || got[0].Kind != "number" {
		t.Errorf(`"7" stored as the number 7 gave %+v`, got)
	}
}

// A date is stored as a serial and displayed as a date. Without the
// pair, the number reads as data loss, which is why the write pays for
// one extra read when and only when something was coerced.
func TestWithDisplayPairsTheStoredValueWithWhatIsShown(t *testing.T) {
	changes := plan.Diff(whole, [][]any{{"2026-09-05"}}, [][]any{{float64(46270)}})
	changes = plan.WithDisplay(changes, [][]any{{"2026-09-05"}})
	if changes[0].Displayed != "2026-09-05" {
		t.Errorf("Displayed = %q, want the date the cell shows", changes[0].Displayed)
	}
}

// A read that did not reach a cell leaves the display blank rather than
// filling it with a guess.
func TestWithDisplayLeavesWhatItCouldNotReadBlank(t *testing.T) {
	changes := plan.Diff(whole, [][]any{{"007", "007"}}, [][]any{{float64(7), float64(7)}})
	changes = plan.WithDisplay(changes, [][]any{{"7"}})
	if changes[0].Displayed != "7" || changes[1].Displayed != "" {
		t.Errorf("displays = %q and %q", changes[0].Displayed, changes[1].Displayed)
	}
}

// The API omits trailing empties, so a response is routinely shorter
// than what was sent. A cell it did not answer for is empty, not absent.
func TestDiffHandlesARaggedResponse(t *testing.T) {
	got := plan.Diff(whole, [][]any{{"Plimth", "Nardle"}}, [][]any{{"Plimth"}})
	if len(got) != 1 || got[0].Address != "B1" || got[0].Kind != "empty" {
		t.Errorf("a value that came back missing gave %+v", got)
	}
}

// Formulas are read off what came back, with one thing the response
// cannot say: under RAW a string beginning with "=" is stored as that
// string and never evaluated.
func TestFormulasDependOnTheInputOption(t *testing.T) {
	stored := [][]any{{"=B1+1", "Plimth"}}
	if got := plan.Formulas(whole, stored, true); len(got) != 1 || got[0] != "A1" {
		t.Errorf("Formulas when formulas are evaluated = %v, want [A1]", got)
	}
	if got := plan.Formulas(whole, stored, false); len(got) != 0 {
		t.Errorf("Formulas under literal input = %v; nothing is evaluated there", got)
	}
}
