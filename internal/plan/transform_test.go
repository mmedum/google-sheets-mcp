package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
)

// A sort key is written as a column letter and reaches the API as the
// sheet's own zero-based column. Getting that conversion wrong sorts by
// the wrong column, which looks like a working sort.
func TestParseSort(t *testing.T) {
	specs, err := plan.ParseSort("B asc, D desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("%d specs", len(specs))
	}
	if specs[0].DimensionIndex != 1 || specs[0].SortOrder != gsheets.SortAscending {
		t.Errorf("first spec = %+v", specs[0])
	}
	if specs[1].DimensionIndex != 3 || specs[1].SortOrder != gsheets.SortDescending {
		t.Errorf("second spec = %+v", specs[1])
	}
	// A column with no order given sorts ascending, which is what a
	// person means by "sort by B".
	bare, _ := plan.ParseSort("C")
	if bare[0].SortOrder != gsheets.SortAscending {
		t.Errorf("a bare column sorts %q", bare[0].SortOrder)
	}
	for _, bad := range []string{"", "B upwards", "2 asc", "B asc extra"} {
		if _, err := plan.ParseSort(bad); err == nil {
			t.Errorf("ParseSort(%q) was accepted", bad)
		}
	}
}

func TestParseColumns(t *testing.T) {
	cols, err := plan.ParseColumns("B, D")
	if err != nil || len(cols) != 2 || cols[0] != 2 || cols[1] != 4 {
		t.Errorf("ParseColumns = %v, %v", cols, err)
	}
	if cols, err := plan.ParseColumns(""); err != nil || cols != nil {
		t.Errorf("an empty list = %v, %v", cols, err)
	}
	if _, err := plan.ParseColumns("B,2"); err == nil {
		t.Error("a number was accepted as a column letter")
	}
}

// One parse, two readers: the request needs the API's enum and the guard
// needs the character, and they used to be worked out separately from
// the same word — which is how " " reached one as a space and the other
// as "let Google decide".
func TestParseDelimiter(t *testing.T) {
	for in, want := range map[string]plan.Delimiter{
		"":          {Kind: gsheets.DelimiterAutodetect},
		"comma":     {Kind: gsheets.DelimiterComma, Sep: ","},
		",":         {Kind: gsheets.DelimiterComma, Sep: ","},
		"semicolon": {Kind: gsheets.DelimiterSemicolon, Sep: ";"},
		"period":    {Kind: gsheets.DelimiterPeriod, Sep: "."},
		"space":     {Kind: gsheets.DelimiterSpace, Sep: " "},
		// A space written as the character rather than the word. Trimmed
		// before it was read, this became the empty string and asked
		// Google to detect a delimiter instead of using the one given.
		" ": {Kind: gsheets.DelimiterSpace, Sep: " "},
		"|": {Kind: gsheets.DelimiterCustom, Custom: "|", Sep: "|"},
	} {
		got, err := plan.ParseDelimiter(in)
		if err != nil || got != want {
			t.Errorf("ParseDelimiter(%q) = %+v, %v, want %+v", in, got, err, want)
		}
	}
	if _, err := plan.ParseDelimiter("::"); err == nil {
		t.Error("a two-character delimiter was accepted")
	}
	// Only the custom kind carries the character into the request; the
	// named ones are the enum, and sending both would be two ways of
	// saying one thing.
	named, _ := plan.ParseDelimiter("comma")
	if named.Custom != "" {
		t.Errorf("a named delimiter carries %q into the request", named.Custom)
	}
}

func TestParsePaste(t *testing.T) {
	if got, _ := plan.ParsePaste(""); got != gsheets.PasteNormal {
		t.Errorf("the default paste is %q", got)
	}
	if got, _ := plan.ParsePaste("values"); got != gsheets.PasteValues {
		t.Errorf("values = %q", got)
	}
	if _, err := plan.ParsePaste("everything"); err == nil {
		t.Error("an unknown paste type was accepted")
	}
}

// Autofill is sent in its explicit form: the bare range form asks Google
// to work out which part of the rectangle is the source, and a write has
// one guess too many in it already.
func TestAutoFillNamesItsSource(t *testing.T) {
	req := plan.AutoFill(4, rect, true, 10).AutoFill
	if req.SourceAndDestination == nil {
		t.Fatal("autofill was sent without naming its source")
	}
	if req.SourceAndDestination.Dimension != gsheets.DimensionRows {
		t.Errorf("filling down asked for %q", req.SourceAndDestination.Dimension)
	}
	if req.SourceAndDestination.FillLength != 10 {
		t.Errorf("autofill = %+v", req)
	}
	if plan.AutoFill(4, rect, false, 1).AutoFill.SourceAndDestination.Dimension != gsheets.DimensionColumns {
		t.Error("filling across asked for rows")
	}
}

// A cut lands on one cell and keeps its shape, which is what the API's
// own types say. The conversion to zero-based is a1's.
func TestCutPasteLandsOnACoordinate(t *testing.T) {
	req := plan.CutPaste(1, rect, 2, 4, 3, gsheets.PasteNormal).CutPaste
	if req.Destination.SheetID != 2 {
		t.Errorf("the cut names sheet %d", req.Destination.SheetID)
	}
	if req.Destination.RowIndex != 3 || req.Destination.ColumnIndex != 2 {
		t.Errorf("row 4 column 3 became %+v", req.Destination)
	}
}

func TestCopyPasteTransposes(t *testing.T) {
	plain := plan.CopyPaste(1, rect, rect, 1, gsheets.PasteNormal, false).CopyPaste
	if plain.PasteOrientation != gsheets.PasteNormalOrientation {
		t.Errorf("orientation = %q", plain.PasteOrientation)
	}
	turned := plan.CopyPaste(1, rect, rect, 1, gsheets.PasteNormal, true).CopyPaste
	if turned.PasteOrientation != gsheets.PasteTranspose {
		t.Errorf("a transposed copy asked for %q", turned.PasteOrientation)
	}
}

// Comparison columns are a half-open band of one column each, and the
// arithmetic is a1's like every other conversion.
func TestDedupeNamesItsColumns(t *testing.T) {
	req := plan.Dedupe(1, a1.Rect{FirstRow: 1, FirstCol: 1, LastRow: 9, LastCol: 4}, []int{2, 4}).DeleteDuplicates
	if len(req.ComparisonColumns) != 2 {
		t.Fatalf("%d comparison columns", len(req.ComparisonColumns))
	}
	if req.ComparisonColumns[0].StartIndex != 1 || req.ComparisonColumns[0].EndIndex != 2 {
		t.Errorf("column B became %+v", req.ComparisonColumns[0])
	}
	if plan.Dedupe(1, rect, nil).DeleteDuplicates.ComparisonColumns != nil {
		t.Error("an empty column list still named columns, which is not the API's default")
	}
}

func TestFindReplaceCarriesItsSwitches(t *testing.T) {
	req := plan.FindReplace(1, rect, "Quorbin", "Vandel", plan.FindReplaceOptions{
		MatchCase: true, MatchEntireCell: true, Regex: true, InFormulas: true,
	}).FindReplace
	if !req.MatchCase || !req.MatchEntireCell || !req.SearchByRegex || !req.IncludeFormulas {
		t.Errorf("the switches did not travel: %+v", req)
	}
	if req.Find != "Quorbin" || req.Replacement != "Vandel" {
		t.Errorf("find and replace = %q, %q", req.Find, req.Replacement)
	}
	// An empty replacement is a removal, so it has to reach the wire
	// rather than being dropped as a zero value. Checked on the JSON,
	// because that is where an omitempty would swallow it.
	raw, err := json.Marshal(plan.FindReplace(1, rect, "Quorbin", "", plan.FindReplaceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"replacement":""`) {
		t.Errorf("an empty replacement did not reach the wire: %s", raw)
	}
}
