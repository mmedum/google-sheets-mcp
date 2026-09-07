package render_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

func hex(s string) *gsheets.ColorStyle {
	var c gsheets.Color
	for i, part := range []*float64{&c.Red, &c.Green, &c.Blue} {
		var n int
		for j := range 2 {
			d := s[1+i*2+j]
			switch {
			case d >= '0' && d <= '9':
				n = n*16 + int(d-'0')
			default:
				n = n*16 + int(d-'a') + 10
			}
		}
		*part = float64(n) / 255
	}
	c.Alpha = 1
	return &gsheets.ColorStyle{RGBColor: &c}
}

// A colour goes out as three floats and has to come back as the six
// digits somebody typed, or a formatting read describes a colour nobody
// can pass to format_cells.
func TestHexColourRoundTrips(t *testing.T) {
	for _, want := range []string{"#d9e2f3", "#000000", "#3366cc", "#b7472a"} {
		if got := render.HexColour(hex(want)); got != want {
			t.Errorf("HexColour(%s) = %q", want, got)
		}
	}
	if got := render.HexColour(nil); got != "" {
		t.Errorf("a missing colour rendered as %q", got)
	}
	// White is a colour somebody set, not a default to hide: a background
	// only reaches a cell's own format when it was set, and setting white
	// is how highlighting is cleared. Hidden here, such a cell read as
	// unformatted while the guard still refused a clear_format over it
	// and named it.
	if got := render.HexColour(hex("#ffffff")); got != "#ffffff" {
		t.Errorf("white rendered as %q", got)
	}
	theme := &gsheets.ColorStyle{ThemeColor: "ACCENT1"}
	if got := render.HexColour(theme); got != "accent1" {
		t.Errorf("a theme colour rendered as %q, and it has no hex to give", got)
	}
}

func TestStyleDescribesWhatWasSet(t *testing.T) {
	style := render.StyleOf(&gsheets.CellFormat{
		NumberFormat:         &gsheets.NumberFormat{Type: "CURRENCY", Pattern: `"$"#,##0.00`},
		BackgroundColorStyle: hex("#d9e2f3"),
		TextFormat: &gsheets.TextFormat{
			Bold: true, Italic: true, FontFamily: "Roboto", FontSize: 12, ForegroundColorStyle: hex("#b7472a"),
		},
		HorizontalAlign: "CENTER", VerticalAlign: "MIDDLE", WrapStrategy: "WRAP",
	})
	got := style.Describe()
	for _, want := range []string{
		"currency", "bold", "italic", "Roboto", "12pt", "text #b7472a", "background #d9e2f3",
		"center", "middle", "wrap",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the description %q does not mention %q", got, want)
		}
	}
	if got == (render.Style{}).Describe() {
		t.Error("a style with ten fields set describes itself as unformatted")
	}
}

// A cell with no format of its own says so, and says it the same way
// every time: the blocks are grouped by this string, so two spellings of
// "unformatted" would be two blocks.
func TestAnEmptyStyleDescribesItself(t *testing.T) {
	var empty render.Style
	if got := empty.Describe(); got != render.StyleOf(nil).Describe() {
		t.Errorf("a nil format and a zero style describe differently: %q", got)
	}
	if !strings.Contains(empty.Describe(), "no formatting") {
		t.Errorf("an unformatted cell reads as %q", empty.Describe())
	}
}

// Four edges is a box, which is the commonest border there is and the
// least worth spelling out edge by edge.
func TestBordersAreDescribedByWhatIsDrawn(t *testing.T) {
	thin := &gsheets.Border{Style: gsheets.BorderThin, ColorStyle: hex("#cccccc")}
	boxed := render.StyleOf(&gsheets.CellFormat{Borders: &gsheets.Borders{
		Top: thin, Bottom: thin, Left: thin, Right: thin,
	}})
	if !strings.Contains(boxed.Describe(), "boxed") {
		t.Errorf("four edges read as %q", boxed.Describe())
	}
	if !strings.Contains(boxed.Describe(), "#cccccc") {
		t.Errorf("the border colour is missing from %q", boxed.Describe())
	}
	partial := render.StyleOf(&gsheets.CellFormat{Borders: &gsheets.Borders{Top: thin, Bottom: thin}})
	if !strings.Contains(partial.Describe(), "top+bottom") {
		t.Errorf("two edges read as %q", partial.Describe())
	}
	// A NONE border is an edge that is not drawn, so it is not described
	// as one.
	none := render.StyleOf(&gsheets.CellFormat{Borders: &gsheets.Borders{
		Top: &gsheets.Border{Style: gsheets.BorderNone},
	}})
	if none != (render.Style{}) {
		t.Errorf("an undrawn border described itself as %q", none.Describe())
	}
}

func TestConditionText(t *testing.T) {
	cond := &gsheets.BooleanCondition{
		Type:   "NUMBER_BETWEEN",
		Values: []*gsheets.ConditionValue{{UserEnteredValue: "1"}, {UserEnteredValue: "10"}},
	}
	if got := render.ConditionText(cond); got != "number between 1, 10" {
		t.Errorf("ConditionText = %q", got)
	}
	if got := render.ConditionText(&gsheets.BooleanCondition{Type: "BLANK"}); got != "blank" {
		t.Errorf("a condition with no values = %q", got)
	}
	relative := &gsheets.BooleanCondition{
		Type: "DATE_AFTER", Values: []*gsheets.ConditionValue{{RelativeDate: "PAST_MONTH"}},
	}
	if got := render.ConditionText(relative); !strings.Contains(got, "past month") {
		t.Errorf("a relative date = %q", got)
	}
	if got := render.ConditionText(nil); got != "no condition" {
		t.Errorf("a missing condition = %q", got)
	}
}

func TestRuleText(t *testing.T) {
	rule := &gsheets.ConditionalFormatRule{BooleanRule: &gsheets.BooleanRule{
		Condition: &gsheets.BooleanCondition{Type: "TEXT_CONTAINS",
			Values: []*gsheets.ConditionValue{{UserEnteredValue: "Quorbin"}}},
		Format: &gsheets.CellFormat{BackgroundColorStyle: hex("#d9ead3")},
	}}
	got := render.RuleText(rule)
	if !strings.Contains(got, "text contains Quorbin") || !strings.Contains(got, "#d9ead3") {
		t.Errorf("RuleText = %q", got)
	}
	// A gradient is named rather than described: what matters is that
	// the colour on a cell comes from a rule and not from the cell.
	gradient := &gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{}}
	if got := render.RuleText(gradient); !strings.Contains(got, "gradient") {
		t.Errorf("a gradient rule = %q", got)
	}
	if render.RuleText(nil) != "" {
		t.Error("a missing rule described itself")
	}
	if got := render.RuleText(&gsheets.ConditionalFormatRule{}); !strings.Contains(got, "no condition") {
		t.Errorf("a rule with neither kind = %q", got)
	}
}

// The formatting answer, whole. A golden because the shape of it is the
// substance: blocks first, then the things attached to the range that
// explain a block that looks unformatted and is not.
func TestFormattingReadsAsAnAnswer(t *testing.T) {
	golden(t, "formatting.txt", render.Formats(render.Formatting{
		Range: "'Vandel'!A1:D10",
		Blocks: []render.Block{
			{Range: "A1:D1", Style: "bold, background #d9e2f3, center"},
			{Range: "B2:B10", Style: `currency "$"#,##0.00, right`},
		},
		Merges:     []string{"A8:C8"},
		Rules:      []render.NamedItem{{Range: "B2:B10", Name: "index 0", Detail: "number greater 100 -> background #d9ead3"}},
		Bandings:   []render.NamedItem{{Range: "A1:D10", Detail: "rows #d9e2f3"}},
		Validation: []render.NamedItem{{Range: "A9", Detail: "one of list: Quorbin, Vandel"}},
		Notes:      []render.NamedItem{{Range: "A2", Detail: "reconciliation pending"}},
		Protected:  []render.NamedItem{{Range: "A1:D1", Name: "headings", Detail: "you may edit it"}},
		Footer:     "14 cell(s) carry no format of their own",
	}))
}

// A range with nothing on it says so, rather than answering with a blank
// space that reads like a failure.
func TestFormattingSaysWhenThereIsNothing(t *testing.T) {
	got := render.Formats(render.Formatting{Range: "'Vandel'!A1:B2"})
	if !strings.Contains(got, "nothing in this range carries formatting of its own") {
		t.Errorf("an unformatted range rendered as %q", got)
	}
}

func TestOpsDoneAndPreviewDescribeTheSameChange(t *testing.T) {
	ops := render.Ops{
		Range: "'Vandel'!A1:D1",
		Applied: []render.Applied{
			{Kind: "bold", Value: "on"},
			{Kind: "background", Value: "#d9e2f3"},
			{Kind: "unmerge"},
		},
		Shifted: true,
		Emptied: "'Vandel'!A1:C1",
	}
	done := render.OpsDone(ops)
	if !strings.Contains(done, "3 change(s) to 'Vandel'!A1:D1") {
		t.Errorf("OpsDone = %q", done)
	}
	for _, want := range []string{
		"bold — on", "background — #d9e2f3",
		"The rows moved: an address or a checkpoint",
		"A cut empties the cells it came from, so 'Vandel'!A1:C1 is now blank.",
	} {
		if !strings.Contains(done, want) {
			t.Errorf("OpsDone does not mention %q:\n%s", want, done)
		}
	}
	// An op with no value is a line of its own rather than one with a
	// dangling dash.
	if strings.Contains(done, "unmerge —") {
		t.Errorf("an op with no value rendered a dash:\n%s", done)
	}

	ops.Blockers = []string{"A1 is not empty; pass overwrite to allow it"}
	preview := render.OpsPreview(ops)
	// The same fact in the tense a preview needs, from the same template.
	if !strings.Contains(preview, "The rows would move") {
		t.Errorf("a preview reports a move in the past tense:\n%s", preview)
	}
	if !strings.Contains(preview, "nothing was sent") {
		t.Errorf("OpsPreview does not say it sent nothing:\n%s", preview)
	}
	if !strings.Contains(preview, "would be refused") || !strings.Contains(preview, "pass overwrite") {
		t.Errorf("a preview did not report what would stop the write:\n%s", preview)
	}
}

// A preview that showed the region and not the refusal would send the
// caller to make a write that cannot go through.
func TestOpsPreviewWithNothingInTheWaySaysNothingAboutRefusals(t *testing.T) {
	got := render.OpsPreview(render.Ops{Range: "'Vandel'!A1", Applied: []render.Applied{{Kind: "bold", Value: "on"}}})
	if strings.Contains(got, "refused") {
		t.Errorf("a clean preview mentioned a refusal:\n%s", got)
	}
}

// Three replies count three different things, and a replacement that
// matched nothing and a trim that changed nothing are both all zeros —
// which is why the kind is carried rather than inferred from the fields
// that happen to be set.
func TestOpsCarryTheCountsTheAPIReported(t *testing.T) {
	for _, tc := range []struct {
		counts render.Counts
		total  int
		want   string
	}{
		{render.Counts{Kind: render.CountReplaced, Occurrences: 4, Cells: 3, Formulas: 1, Rows: 2}, 4,
			"4 occurrence(s) replaced in 3 cell(s) and 1 formula(s), across 2 row(s)."},
		{render.Counts{Kind: render.CountTrimmed, Cells: 7}, 7, "7 cell(s) had whitespace trimmed."},
		{render.Counts{Kind: render.CountDeduped, Cells: 2}, 2, "2 duplicate row(s) removed."},
		{render.Counts{Kind: render.CountReplaced}, 0, "0 occurrence(s) replaced"},
	} {
		got := render.OpsDone(render.Ops{
			Range: "'Vandel'!A1:D10", Applied: []render.Applied{{Kind: "replaced", Value: `"a" with "b"`}},
			Counted: &tc.counts,
		})
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s counts read as:\n%s", tc.counts.Kind, got)
		}
		if tc.counts.Total() != tc.total {
			t.Errorf("%s total = %d, want %d", tc.counts.Kind, tc.counts.Total(), tc.total)
		}
	}
	// Most actions report no count at all, and a line saying nothing
	// would be worse than no line.
	var none *render.Counts
	if none.Sentence() != "" || none.Total() != 0 {
		t.Errorf("a reply with no count read as %q", none.Sentence())
	}
}
