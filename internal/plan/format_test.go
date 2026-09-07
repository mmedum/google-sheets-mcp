package plan_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

var rect = a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 3, LastCol: 2}

// The mask is the whole mechanism: a field named in it and absent from
// the body is set back to its default, which is how bold is turned off.
// So a patch has to name exactly what was set on it and nothing else.
func TestAPatchNamesExactlyWhatWasSet(t *testing.T) {
	var p plan.Patch
	if !p.Empty() {
		t.Error("a patch with nothing set is not empty")
	}
	p.Bold(false)
	p.Background(nil)
	req := p.Request(7, rect).RepeatCell
	if req == nil {
		t.Fatal("the patch did not compile to a repeatCell")
	}
	fields := strings.Split(req.Fields, ",")
	want := []string{"userEnteredFormat.textFormat.bold", "userEnteredFormat.backgroundColorStyle"}
	if len(fields) != len(want) {
		t.Fatalf("mask = %q", req.Fields)
	}
	for i, f := range want {
		if fields[i] != f {
			t.Errorf("mask = %q, want %v", req.Fields, want)
		}
	}
	// Turning bold off is the field named and the value absent, which is
	// the case a struct-shaped builder cannot express.
	if req.Cell.UserEnteredFormat.TextFormat.Bold {
		t.Error("bold(false) set bold")
	}
	if req.Cell.UserEnteredFormat.BackgroundColorStyle != nil {
		t.Error("a nil background was not left absent, so it would not clear")
	}
	if req.Range.SheetID != 7 {
		t.Errorf("the request names sheet %d", req.Range.SheetID)
	}
}

// Every setter, so one that forgets its mask entry is caught rather than
// discovered by a caller whose formatting silently did nothing.
func TestEverySetterAddsItsField(t *testing.T) {
	for name, set := range map[string]func(*plan.Patch){
		"userEnteredFormat.numberFormat":                    func(p *plan.Patch) { p.NumberFormat("DATE", "yyyy") },
		"userEnteredFormat.textFormat.italic":               func(p *plan.Patch) { p.Italic(true) },
		"userEnteredFormat.textFormat.underline":            func(p *plan.Patch) { p.Underline(true) },
		"userEnteredFormat.textFormat.strikethrough":        func(p *plan.Patch) { p.Strikethrough(true) },
		"userEnteredFormat.textFormat.fontSize":             func(p *plan.Patch) { p.FontSize(12) },
		"userEnteredFormat.textFormat.fontFamily":           func(p *plan.Patch) { p.FontFamily("Roboto") },
		"userEnteredFormat.textFormat.foregroundColorStyle": func(p *plan.Patch) { p.TextColour(nil) },
		"userEnteredFormat.horizontalAlignment":             func(p *plan.Patch) { p.HorizontalAlign("LEFT") },
		"userEnteredFormat.verticalAlignment":               func(p *plan.Patch) { p.VerticalAlign("TOP") },
		"userEnteredFormat.wrapStrategy":                    func(p *plan.Patch) { p.Wrap("WRAP") },
		"note":                                              func(p *plan.Patch) { p.Note("Quorbin") },
	} {
		var p plan.Patch
		set(&p)
		if got := p.Request(1, rect).RepeatCell.Fields; got != name {
			t.Errorf("mask = %q, want %q", got, name)
		}
	}
}

// Clearing is the bare mask, which cannot be combined with one naming
// fields under it. A caller who clears and sets sends both.
func TestClearFormatIsTheBareMask(t *testing.T) {
	req := plan.ClearFormat(1, rect).RepeatCell
	if req.Fields != "userEnteredFormat" {
		t.Errorf("clear mask = %q", req.Fields)
	}
	if req.Cell.UserEnteredFormat != nil {
		t.Error("a clear carries a format, so it would set one rather than remove one")
	}
}

func TestParseBorder(t *testing.T) {
	for _, tc := range []struct {
		in     string
		style  string
		colour bool
	}{
		{"1pt solid #cccccc", gsheets.BorderThin, true},
		{"2pt solid", gsheets.BorderMedium, false},
		{"3pt", gsheets.BorderThick, false},
		{"dotted", gsheets.BorderDotted, false},
		{"2pt dashed #000", gsheets.BorderDashed, true},
		{"double", gsheets.BorderDouble, false},
		{"none", gsheets.BorderNone, false},
	} {
		got, err := plan.ParseBorder(tc.in)
		if err != nil {
			t.Errorf("ParseBorder(%q): %v", tc.in, err)
			continue
		}
		if got.Style != tc.style {
			t.Errorf("ParseBorder(%q).Style = %q, want %q", tc.in, got.Style, tc.style)
		}
		if (got.ColorStyle != nil) != tc.colour {
			t.Errorf("ParseBorder(%q) colour = %v", tc.in, got.ColorStyle)
		}
	}
	for _, bad := range []string{"", "4pt", "wavy", "1pt solid #gg"} {
		if _, err := plan.ParseBorder(bad); err == nil {
			t.Errorf("ParseBorder(%q) was accepted", bad)
		}
	}
}

// "none" erases the edges rather than leaving them alone, which is the
// API's own rule and the reason it is a style here.
func TestNoneErasesRatherThanLeavingAlone(t *testing.T) {
	border, err := plan.ParseBorder("none")
	if err != nil {
		t.Fatal(err)
	}
	req := plan.Borders(1, rect, border, plan.AllSides()).UpdateBorders
	for name, edge := range map[string]*gsheets.Border{
		"top": req.Top, "bottom": req.Bottom, "left": req.Left, "right": req.Right,
		"innerHorizontal": req.InnerHorizontal, "innerVertical": req.InnerVertical,
	} {
		if edge == nil || edge.Style != gsheets.BorderNone {
			t.Errorf("%s = %+v, want a NONE border", name, edge)
		}
	}
}

func TestParseSides(t *testing.T) {
	all, err := plan.ParseSides("")
	if err != nil || all != plan.AllSides() {
		t.Errorf("an empty sides list = %+v, %v", all, err)
	}
	outer, _ := plan.ParseSides("outer")
	if !outer.Top || outer.InnerHorizontal {
		t.Errorf("outer = %+v", outer)
	}
	inner, _ := plan.ParseSides("inner")
	if inner.Top || !inner.InnerVertical {
		t.Errorf("inner = %+v", inner)
	}
	list, _ := plan.ParseSides("top, bottom")
	if !list.Top || !list.Bottom || list.Left {
		t.Errorf("a list = %+v", list)
	}
	if _, err := plan.ParseSides("sideways"); err == nil {
		t.Error("an unknown side was accepted")
	}
}

// Only the named edges are drawn: a request that filled in the rest
// would draw borders nobody asked for and could not be undone selectively.
func TestBordersDrawOnlyTheSidesNamed(t *testing.T) {
	border, _ := plan.ParseBorder("1pt solid")
	sides, _ := plan.ParseSides("top")
	req := plan.Borders(1, rect, border, sides).UpdateBorders
	if req.Top == nil {
		t.Error("the named edge was not drawn")
	}
	if req.Bottom != nil || req.Left != nil || req.Right != nil ||
		req.InnerHorizontal != nil || req.InnerVertical != nil {
		t.Errorf("an unnamed edge was drawn: %+v", req)
	}
}

// The type is asked for rather than guessed from the pattern: a wrong
// guess formats a column as the wrong kind of thing and looks like it
// worked.
func TestParseNumberFormat(t *testing.T) {
	kind, pattern, err := plan.ParseNumberFormat("date:yyyy-mm-dd")
	if err != nil || kind != gsheets.NumberFormatDate || pattern != "yyyy-mm-dd" {
		t.Errorf("date with a pattern = %q, %q, %v", kind, pattern, err)
	}
	kind, pattern, err = plan.ParseNumberFormat("CURRENCY")
	if err != nil || kind != gsheets.NumberFormatCurrency || pattern != "" {
		t.Errorf("currency = %q, %q, %v", kind, pattern, err)
	}
	if _, _, err := plan.ParseNumberFormat("#,##0.00"); err == nil {
		t.Error("a bare pattern was accepted, so its type would have been guessed")
	}
}

func TestParseAlignAndWrap(t *testing.T) {
	if got, _ := plan.ParseAlign("centre", false); got != gsheets.AlignCentre {
		t.Errorf("horizontal centre = %q", got)
	}
	if got, _ := plan.ParseAlign("middle", true); got != gsheets.AlignMiddle {
		t.Errorf("vertical middle = %q", got)
	}
	// The two axes take different words, and a word from the wrong axis
	// is a caller who meant something else.
	if _, err := plan.ParseAlign("top", false); err == nil {
		t.Error("top was accepted as a horizontal alignment")
	}
	if _, err := plan.ParseAlign("left", true); err == nil {
		t.Error("left was accepted as a vertical alignment")
	}
	if got, _ := plan.ParseWrap("wrap"); got != gsheets.WrapWrap {
		t.Errorf("wrap = %q", got)
	}
	if _, err := plan.ParseWrap("fold"); err == nil {
		t.Error("an unknown wrap was accepted")
	}
}

func TestParseMerge(t *testing.T) {
	for in, want := range map[string]string{
		"all": gsheets.MergeAll, "rows": gsheets.MergeRows,
		"columns": gsheets.MergeColumns, "cols": gsheets.MergeColumns,
	} {
		if got, err := plan.ParseMerge(in); err != nil || got != want {
			t.Errorf("ParseMerge(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := plan.ParseMerge("some"); err == nil {
		t.Error("an unknown merge type was accepted")
	}
}

func TestMergeAndUnmergeCarryTheRange(t *testing.T) {
	if got := plan.Merge(3, rect, gsheets.MergeAll).MergeCells; got == nil || got.Range.SheetID != 3 {
		t.Errorf("merge = %+v", got)
	}
	if got := plan.Unmerge(3, rect).UnmergeCells; got == nil || got.Range.SheetID != 3 {
		t.Errorf("unmerge = %+v", got)
	}
}
