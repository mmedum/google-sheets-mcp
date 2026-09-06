package render_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// The words for a band live here now, so this is where they are checked.
// They used to be composed in the service, where no golden could reach
// them (§17a.9).
func TestBandReadsTheWayItWasGiven(t *testing.T) {
	rows := render.Band{Rows: true, First: 2, Last: 5}
	if rows.String() != "rows 2:5" || rows.Count() != 4 || rows.Unit() != "row(s)" || rows.Start() != "row 2" {
		t.Errorf("rows band = %q, %d, %q, %q", rows, rows.Count(), rows.Unit(), rows.Start())
	}
	cols := render.Band{First: 2, Last: 4}
	if cols.String() != "columns B:D" || cols.Count() != 3 || cols.Unit() != "column(s)" || cols.Start() != "column B" {
		t.Errorf("columns band = %q, %d, %q, %q", cols, cols.Count(), cols.Unit(), cols.Start())
	}
}

func TestDimensionActPhrasesEveryAction(t *testing.T) {
	band := render.Band{Rows: true, First: 2, Last: 3}
	for action, want := range map[string]string{
		"insert":      `insert 2 row(s) before row 2 on "Vandel"`,
		"delete":      `delete rows 2:3 on "Vandel"`,
		"move":        `move rows 2:3 on "Vandel" to position 7`,
		"resize":      `set rows 2:3 on "Vandel" to 120 pixels`,
		"auto_resize": `size rows 2:3 on "Vandel" to fit their contents`,
		"group":       `group rows 2:3 on "Vandel"`,
		"ungroup":     `ungroup rows 2:3 on "Vandel"`,
	} {
		act := render.DimensionAct{Action: action, Sheet: "Vandel", Band: band, To: 7, Pixels: 120}
		if got := act.Phrase(); got != want {
			t.Errorf("%s = %q, want %q", action, got, want)
		}
	}
}

// A preview and a result describe the same act, in the same terms, one
// having sent nothing. That is the property the parts model exists for.
func TestDimensionPreviewAndDoneAgree(t *testing.T) {
	act := render.DimensionAct{
		Action: "delete", Sheet: "Vandel", Band: render.Band{Rows: true, First: 2, Last: 3},
		Cells: 6, Formulas: 2, Shifted: true,
	}
	preview, done := render.DimensionPreview(act), render.DimensionDone(act)
	if !strings.Contains(preview, "nothing was sent") {
		t.Errorf("the preview does not say it sent nothing: %q", preview)
	}
	if !strings.Contains(preview, "would take") || !strings.Contains(done, "It took") {
		t.Errorf("the two tenses did not travel:\n%s\n%s", preview, done)
	}
	for _, text := range []string{preview, done} {
		if !strings.Contains(text, "delete rows 2:3") && !strings.Contains(text, "Delete rows 2:3") {
			t.Errorf("the act is not described: %q", text)
		}
		if !strings.Contains(text, "6 non-empty cell(s) and 2 formula(s)") {
			t.Errorf("what it takes is missing: %q", text)
		}
		if !strings.Contains(text, "no longer points where it did") {
			t.Errorf("a shifted address is not reported: %q", text)
		}
	}
	// A result reads as a sentence, so the phrase is capitalised where
	// it starts one and lower-case where it does not.
	if !strings.Contains(done, "Done: Delete") {
		t.Errorf("the result does not start its sentence: %q", done)
	}
}

func TestSheetActPhrasesEveryAction(t *testing.T) {
	for action, want := range map[string]string{
		"add":       `add a sheet called "Oblisk"`,
		"rename":    `rename "Vandel" to "Oblisk"`,
		"duplicate": `duplicate "Vandel"`,
		"copy_to":   `copy "Vandel" into the spreadsheet "Skerry plan"`,
		"hide":      `hide "Vandel"`,
		"unhide":    `unhide "Vandel"`,
		"reorder":   `move "Vandel" to position 2`,
		"resize":    `resize "Vandel" to 500 rows and 26 columns`,
		"freeze":    `freeze 500 row(s) and 26 column(s) on "Vandel"`,
		"tab_color": `set the tab colour of "Vandel" to #4a90d9`,
	} {
		act := render.SheetAct{
			Action: action, Sheet: "Vandel", Title: "Oblisk", Index: 2,
			Rows: 500, Cols: 26, Destination: "Skerry plan", Colour: "#4a90d9",
		}
		if got := act.Phrase(); got != want {
			t.Errorf("%s = %q, want %q", action, got, want)
		}
	}
	// An empty colour clears rather than setting one, and the sentence
	// has to say which.
	clearing := render.SheetAct{Action: "tab_color", Sheet: "Vandel"}
	if got := clearing.Phrase(); !strings.Contains(got, "clear") {
		t.Errorf("clearing a tab colour reads as %q", got)
	}
}

func TestSheetDoneListsWhatIsLeft(t *testing.T) {
	act := render.SheetAct{Action: "add", Title: "Oblisk"}
	got := render.SheetDone(act, []string{"Vandel", "Oblisk"})
	if !strings.Contains(got, `Done: Add a sheet called "Oblisk"`) {
		t.Errorf("SheetDone = %q", got)
	}
	if !strings.Contains(got, "2 sheet(s): Vandel, Oblisk") {
		t.Errorf("the sheets afterwards are missing, and the next call has to name one: %q", got)
	}
	if got := render.SheetPreview(act); !strings.Contains(got, "nothing was sent") {
		t.Errorf("SheetPreview = %q", got)
	}
	copied := render.CopyDone(render.SheetAct{Action: "copy_to", Sheet: "Vandel", Destination: "Skerry plan"}, "Copy of Vandel")
	if !strings.Contains(copied, `"Copy of Vandel", in the destination spreadsheet`) {
		t.Errorf("CopyDone = %q", copied)
	}
}
