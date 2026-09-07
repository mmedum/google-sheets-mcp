package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// update regenerates the goldens from the fixtures. They are generated,
// never recorded: nothing in testdata/ ever came out of a real
// spreadsheet, and there is no path by which it could.
var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/render -update` to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs.\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// fixtureGrid builds a grid straight from the in-memory spreadsheet,
// without the network or the service.
func fixtureGrid(t *testing.T, sheetTitle string, rect a1.Rect) *grid.Grid {
	t.Helper()
	doc, _ := sheetstest.Fixture()
	sh := doc.Find(sheetTitle)
	if sh == nil {
		t.Fatalf("no sheet %q in the fixture", sheetTitle)
	}
	// The fake's own response builder, so the goldens are generated from
	// the ragged shape a real read produces — trailing empty rows and
	// cells omitted — rather than from a neat rectangle that would
	// under-exercise the padding.
	g := grid.Build(sheetTitle, sh.Props.SheetID, rect, sheetstest.GridData(sh, rect), grid.AsRaw)
	for _, m := range sh.Merges {
		if r := a1.FromGridRange(m); r.Overlaps(rect) {
			g.Merges = append(g.Merges, r)
		}
	}
	for _, p := range sh.Protected {
		if r := a1.FromGridRange(p.Range); r.Overlaps(rect) {
			g.Protected = append(g.Protected, grid.Protection{
				Rect: r, Description: p.Description, CanEdit: p.RequestingUserCanEdit,
			})
		}
	}
	return g
}

func TestGridGolden(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 6})
	opts := render.GridOptions{Show: render.ShowValues, TotalRows: 200}
	res := render.Grid(g, opts)
	golden(t, "grid_values.txt", res.Text+render.Footer(g, res, opts)+"\n")
}

func TestGridBothGolden(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 4})
	opts := render.GridOptions{
		Show: render.ShowBoth, TotalRows: 200,
		IncludeNotes: true, IncludeValidation: true, IncludeMerges: true,
	}
	res := render.Grid(g, opts)
	golden(t, "grid_both.txt", res.Text+render.Footer(g, res, opts)+"\n")
}

func TestGridAlignsColumnsUnderTheirLetters(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 3})
	res := render.Grid(g, render.GridOptions{})
	lines := strings.Split(strings.TrimRight(res.Text, "\n"), "\n")
	header := lines[0]
	// Every column letter sits at the same offset as the values under
	// it. If it did not, the addresses would be a decoration rather than
	// a contract.
	for _, letter := range []string{"A", "B", "C"} {
		col := strings.Index(header, letter)
		if col < 0 {
			t.Fatalf("no %s in %q", letter, header)
		}
		for _, line := range lines[1:] {
			if len(line) <= col {
				t.Fatalf("row %q is shorter than column %s at offset %d", line, letter, col)
			}
		}
	}
}

func TestGridOffARangeThatStartsAway(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 2, FirstRow: 10, LastCol: 4, LastRow: 12})
	res := render.Grid(g, render.GridOptions{})
	if !strings.Contains(res.Text, " B ") && !strings.Contains(res.Text, "B  ") {
		t.Errorf("the header does not start at column B:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "10 |") {
		t.Errorf("the gutter does not start at row 10:\n%s", res.Text)
	}
}

func TestGridClipsLongValuesAndSaysSo(t *testing.T) {
	long := strings.Repeat("Quorbin", 40)
	data := &gsheets.GridData{RowData: []*gsheets.RowData{{Values: []*gsheets.CellData{sheetstest.Str(long)}}}}
	g := grid.Build("Vandel", 0, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}, data, grid.AsRaw)
	opts := render.GridOptions{}
	res := render.Grid(g, opts)
	if !strings.Contains(res.Text, "…") {
		t.Errorf("a long value was not clipped:\n%s", res.Text)
	}
	if res.Shortened != 1 {
		t.Errorf("Shortened = %d", res.Shortened)
	}
	if !strings.Contains(render.Footer(g, res, opts), "shortened") {
		t.Error("the footer does not say a value was shortened")
	}
}

func TestGridKeepsAlignmentThroughNewlines(t *testing.T) {
	// A newline inside a cell would break every address below it.
	data := &gsheets.GridData{RowData: []*gsheets.RowData{{Values: []*gsheets.CellData{
		sheetstest.Str("one\ntwo"), sheetstest.Str("three"),
	}}}}
	g := grid.Build("Vandel", 0, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 1}, data, grid.AsRaw)
	res := render.Grid(g, render.GridOptions{})
	if strings.Count(res.Text, "\n") != 2 {
		t.Errorf("a cell newline reached the grid:\n%q", res.Text)
	}
}

func TestEmptyGrid(t *testing.T) {
	g := grid.Build("Vandel", 0, a1.WholeSheet, nil, grid.AsRaw)
	res := render.Grid(g, render.GridOptions{})
	if !strings.Contains(res.Text, "empty") {
		t.Errorf("an empty rectangle rendered as %q", res.Text)
	}
}

func TestCardGolden(t *testing.T) {
	golden(t, "card.txt", render.Spreadsheet(render.Card{
		Title: "Quorbin Skerry", ID: sheetstest.FixtureID,
		Link:   "https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit",
		Locale: "en_GB", TimeZone: "Etc/GMT", Recalc: "ON_CHANGE",
		Owner: "fixture@example.test", Modified: "2026-03-11T14:25:00.000Z",
		Sheets: []render.CardSheet{
			{Title: sheetstest.FirstSheet, ID: 0, Rows: 200, Cols: 12, FrozenRows: 1, Holds: []string{"1 table", "1 merge"}},
			{Title: sheetstest.SecondSheet, ID: 1837, Rows: 50, Cols: 8},
			{Title: sheetstest.ApostropheName, ID: 2914, Rows: 20, Cols: 4, Hidden: true, TabColor: "#3366cc"},
		},
		NamedRanges: []render.NamedItem{{
			Name: sheetstest.FirstSheet, Range: "'Ürväl'!A1:B2",
			Detail: "shares its name with a sheet; this server always quotes a sheet title, so the two stay apart",
		}},
		Tables:      []render.NamedItem{{Name: "Oblisk", Range: "'Vandel'!A1:D21", Detail: "Plimth TEXT and Nardle DOUBLE"}},
		Protected:   []render.NamedItem{{Name: "heading row", Range: "'Vandel'!A1:D1", Detail: "you may not edit it"}},
		FilterViews: []render.NamedItem{{Name: "Grivet over 500", Range: "'Vandel'!A1:D21"}},
	}))
}

func TestSeparated(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 2})
	rows := render.Rows(g, render.ShowValues)
	csv := render.Separated(rows, ',')
	if !strings.HasPrefix(csv, "Plimth,Nardle\n") {
		t.Errorf("csv = %q", csv)
	}
	tsv := render.Separated(rows, '\t')
	if !strings.HasPrefix(tsv, "Plimth\tNardle\n") {
		t.Errorf("tsv = %q", tsv)
	}
	// A value containing the separator has to survive it.
	withComma := [][]string{{"a,b", "c"}}
	if got := render.Separated(withComma, ','); !strings.Contains(got, `"a,b"`) {
		t.Errorf("a comma inside a value was not quoted: %q", got)
	}
}

func TestRowsFollowShow(t *testing.T) {
	g := fixtureGrid(t, sheetstest.FirstSheet, a1.Rect{FirstCol: 4, FirstRow: 2, LastCol: 4, LastRow: 2})
	if got := render.Rows(g, render.ShowFormulas)[0][0]; got != "=B2+C2" {
		t.Errorf("formulas gave %q", got)
	}
	if got := render.Rows(g, render.ShowValues)[0][0]; strings.HasPrefix(got, "=") {
		t.Errorf("values gave a formula: %q", got)
	}
	// One field cannot hold two answers, and the value is what a machine
	// format is usually after.
	if got := render.Rows(g, render.ShowBoth)[0][0]; strings.HasPrefix(got, "=") {
		t.Errorf("both gave a formula in the machine form: %q", got)
	}
}

func TestHitsAndCandidates(t *testing.T) {
	hs := []render.Hit{
		{Title: "Quorbin Skerry", ID: sheetstest.FixtureID, Owner: "fixture@example.test",
			Modified: "2026-03-11T14:25:00.000Z"},
	}
	out := render.Hits(hs, "next-page-fixture")
	for _, want := range []string{"Quorbin Skerry", sheetstest.FixtureID, "page_token"} {
		if !strings.Contains(out, want) {
			t.Errorf("Hits is missing %q:\n%s", want, out)
		}
	}
	if got := render.Hits(nil, ""); !strings.Contains(got, "no spreadsheets matched") {
		t.Errorf("empty Hits = %q", got)
	}
	if got := render.Candidates(hs); !strings.Contains(got, sheetstest.FixtureID) {
		t.Errorf("Candidates = %q", got)
	}
}

func TestMatchesRendering(t *testing.T) {
	ms := []render.Match{
		{Sheet: "Vandel", Address: "D2", Kind: "value", Text: "30", Formula: "=B2+C2"},
		{Sheet: "Vandel", Address: "A2", Kind: "note", Text: "Quorbin reconciliation pending"},
	}
	out := render.Matches(ms, 240, render.Complete, false)
	for _, want := range []string{"Vandel!D2", "value: 30", "[=B2+C2]", "note:"} {
		if !strings.Contains(out, want) {
			t.Errorf("Matches is missing %q:\n%s", want, out)
		}
	}
	if got := render.Matches(nil, 10, render.Complete, false); strings.Contains(got, "raise") {
		t.Errorf("a complete search must not offer a dial to turn: %q", got)
	}
	// The three endings have three different answers, so a search
	// stopped by max_matches must not be told to raise max_cells.
	cells := render.Matches(nil, 10, render.CellBudget, false)
	if !strings.Contains(cells, "max_cells") || strings.Contains(cells, "max_matches was reached") {
		t.Errorf("the cell-budget note is wrong: %q", cells)
	}
	matches := render.Matches(ms, 10, render.MatchLimit, false)
	if !strings.Contains(matches, "max_matches") || strings.Contains(matches, "raise max_cells") {
		t.Errorf("the match-limit note sends the caller to the wrong dial: %q", matches)
	}
	// A new sheet is allocated 1000 rows long before it holds any, so a
	// search of a nearly empty spreadsheet hits the budget having seen
	// everything. Saying "this did not cover the whole spreadsheet"
	// there sends a model back for a second look that finds nothing.
	ended := render.Matches(ms, 10, render.CellBudget, true)
	if strings.Contains(ended, "did not cover the whole spreadsheet") {
		t.Errorf("the note ignores that the data ran out first: %q", ended)
	}
	if !strings.Contains(ended, "probably nothing further") {
		t.Errorf("the note does not say what was actually observed: %q", ended)
	}
}
