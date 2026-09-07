package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestSheetCSVIsTheUsedRangeNotTheSheet(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.SheetCSV(context.Background(), sheetstest.FixtureID, sheetstest.FirstSheet)
	if err != nil {
		t.Fatalf("SheetCSV: %v", err)
	}
	// The fixture sheet is allocated 200 rows and 12 columns and holds
	// 26 rows across 4. A resource that answered with the allocated size
	// would be 2 400 cells of commas describing nothing.
	lines := strings.Split(strings.TrimRight(res.CSV, "\n"), "\n")
	if len(lines) != 26 {
		t.Errorf("the CSV has %d rows; the used range is 26 of the sheet's 200", len(lines))
	}
	if got := strings.Count(lines[0], ",") + 1; got != 4 {
		t.Errorf("the CSV has %d columns; the used range is 4 of the sheet's 12", got)
	}
	if !strings.HasPrefix(lines[0], "Plimth,Nardle,Grivet,Oblisk") {
		t.Errorf("the heading row is %q", lines[0])
	}
	if !strings.Contains(res.Range, "D26") {
		t.Errorf("Range = %q, want the used range ending at D26", res.Range)
	}
	if res.Note != "" {
		t.Errorf("nothing was cut, so there should be no note: %q", res.Note)
	}
}

func TestSheetCSVSaysWhenItCutSomething(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Fixture()
	// Wider than the character budget can carry: every row is one long
	// value, so the cut lands on rows rather than inside one.
	sh := doc.Sheets[0]
	long := strings.Repeat("Quorbin", 2000)
	for r := range 60 {
		sh.Set(r+1, 1, sheetstest.Str(long))
	}
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.SheetCSV(context.Background(), sheetstest.FixtureID, sheetstest.FirstSheet)
	if err != nil {
		t.Fatalf("SheetCSV: %v", err)
	}
	if len(res.CSV) > service.MaxResourceChars {
		t.Errorf("the CSV is %d characters, past the %d budget", len(res.CSV), service.MaxResourceChars)
	}
	if res.Note == "" {
		t.Fatal("the read was cut short and said nothing; a resource that answers half a sheet in silence is the bug")
	}
	if !strings.Contains(res.Note, "read_range") {
		t.Errorf("the note does not say how to read on: %q", res.Note)
	}
	// Whole rows, always. Half a quoted field parses as a different
	// value rather than as an error, which is the failure a client would
	// never see.
	for i, line := range strings.Split(strings.TrimRight(res.CSV, "\n"), "\n") {
		if strings.Count(line, `"`)%2 != 0 {
			t.Fatalf("row %d ends inside a quoted field: %.60q", i, line)
		}
	}
}

func TestSheetCSVOnAnEmptySheetSaysSo(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Fixture()
	// A sheet with an allocated size and nothing on it, which is what
	// every sheet somebody has just added looks like.
	doc.Sheets = append(doc.Sheets, &sheetstest.Sheet{Props: gsheets.SheetProperties{
		SheetID: 4001, Title: "Threnody", Index: gsheets.Ptr(3), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: 1000, ColumnCount: 26},
	}})
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.SheetCSV(context.Background(), sheetstest.FixtureID, "Threnody")
	if err != nil {
		t.Fatalf("SheetCSV: %v", err)
	}
	if res.CSV != "" {
		t.Errorf("an empty sheet produced %q", res.CSV)
	}
	if !strings.Contains(res.Note, "empty") {
		t.Errorf("Note = %q, want it to say the sheet is empty", res.Note)
	}
}

func TestSheetCSVRefusesASheetThatIsNotThere(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.SheetCSV(context.Background(), sheetstest.FixtureID, "Nardlewick")
	if err == nil {
		t.Fatal("a sheet that does not exist was read")
	}
	if !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("err = %v, want [not_found] so a client can tell it from a refusal", err)
	}
}

// One row can exceed the whole budget on its own — 26 cells at the
// 50 000-character cell limit is 1.3 MB — and then no row was dropped,
// so a note keyed on the row count says nothing at all.
func TestSheetCSVSaysWhenOneRowIsOverBudget(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Fixture()
	sh := doc.Sheets[0]
	for k := range sh.Cells {
		delete(sh.Cells, k)
	}
	for c := range 12 {
		sh.Set(1, c+1, sheetstest.Str(strings.Repeat("Quorbin", 8000)))
	}
	srv.Add(doc, file)
	svc := newService(t, srv)

	res, err := svc.SheetCSV(context.Background(), sheetstest.FixtureID, sheetstest.FirstSheet)
	if err != nil {
		t.Fatalf("SheetCSV: %v", err)
	}
	if len(res.CSV) <= service.MaxResourceChars {
		t.Skipf("the fixture row is %d characters, inside the budget; this test needs a wider one", len(res.CSV))
	}
	if res.Note == "" {
		t.Fatal("the resource returned more than its own stated limit and said nothing")
	}
	if !strings.Contains(res.Note, "single") {
		t.Errorf("Note = %q, want it to say one row is the reason", res.Note)
	}
}
