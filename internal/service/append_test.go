package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

// Where the rows land is Google's decision. The result reports the block
// it found and the range it wrote, because neither is predictable from
// the range the caller named.
func TestAppendReportsWhereItLanded(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if res.TableRange == "" || res.Range == "" {
		t.Fatalf("Append did not say where it went: %+v", res)
	}
	if !strings.Contains(res.Render(), res.Range) || !strings.Contains(res.Render(), res.TableRange) {
		t.Errorf("the summary names neither the block nor the destination:\n%s", res.Render())
	}
	// INSERT_ROWS, and it moves everything below.
	if !res.Shifted || !strings.Contains(res.Render(), "moved down") {
		t.Errorf("an inserting append did not say addresses moved:\n%s", res.Render())
	}
}

// An append always inserts. OVERWRITE wrote over rows chosen during the
// call, which no read could witness, so it is never sent.
func TestAppendOnlyInserts(t *testing.T) {
	srv, svc := standard(t)
	if _, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var sent []string
	for _, c := range srv.Calls() {
		if c.Op == "values.append" {
			sent = append(sent, c.Query.Get("insertDataOption"))
		}
	}
	if len(sent) != 1 || sent[0] != "INSERT_ROWS" {
		t.Errorf("insertDataOption sent = %v, want [INSERT_ROWS]", sent)
	}
}

// The range picks the block, so a default here would be a coin flip
// whose outcome is invisible until somebody reads the sheet.
func TestAppendNeedsARange(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
		Values: [][]any{{"Trennow"}},
	})
	if err == nil || !strings.Contains(err.Error(), "picks which block") {
		t.Fatalf("an append with no range gave %v", err)
	}
}

// The preview says less than a write's does, and says why.
func TestAppendDryRunSendsNothingAndSaysItCannotName(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "cannot name the destination") {
		t.Errorf("the preview overpromises:\n%s", res.Render())
	}
	if wrote(srv) {
		t.Error("a dry run reached the wire")
	}
}

// An append has no destination to read, so a formula that reaches
// outside is still gated — by position, not by an address it would be
// guessing.
func TestAppendGatesExternalFormulasByPosition(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{`=IMPORTRANGE("https://example.test/x","A1")`, ""}},
	})
	if err == nil || !strings.Contains(err.Error(), "row 1, column 1") {
		t.Fatalf("an appended IMPORTRANGE gave %v", err)
	}
}

func TestAppendCoercionIsReported(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"007", "TRUE"}},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(res.Coerced) != 2 {
		t.Errorf("%d coercion(s) from two coerced values: %+v", len(res.Coerced), res.Coerced)
	}
}

// An append inserts rows, so the grid grows. A read straight after it,
// inside the card's cache window, has to resolve against the grown grid
// or it clamps off the rows the append just added.
func TestAReadAfterAnAppendSeesTheAppendedRows(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(4, 2)
	srv.Add(doc, file)
	svc := newService(t, srv)
	ctx := context.Background()
	read := func() string {
		t.Helper()
		res, err := svc.Read(ctx, service.ReadRequest{Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:B"})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		return res.Range
	}
	if got := read(); got != "'Bractal'!A1:B4" {
		t.Fatalf("before the append the read covered %s, want 'Bractal'!A1:B4", got)
	}
	if _, err := svc.Append(ctx, service.AppendRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:B1",
		Values: [][]any{{"Zephrin", float64(12)}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := read(); got != "'Bractal'!A1:B5" {
		t.Errorf("after appending one row the read covered %s, want 'Bractal'!A1:B5", got)
	}
}

// fetchFirst runs during once the first card read has come back from
// Google and before the service stores it.
type fetchFirst struct {
	service.API
	during func()
}

func (f *fetchFirst) GetSpreadsheet(ctx context.Context, id string, o gapi.GetOptions) (*gsheets.Spreadsheet, error) {
	sp, err := f.API.GetSpreadsheet(ctx, id, o)
	if during := f.during; during != nil {
		f.during = nil
		during()
	}
	return sp, err
}

// A card read before an append landed, and stored after it, would bring
// back the row count the append made stale.
func TestACardReadBeforeAnAppendIsNotKept(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(4, 2)
	srv.Add(doc, file)
	fs, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	api := &fetchFirst{API: srv.Client()}
	svc := service.New(service.Deps{API: api, Config: fs})
	ctx := context.Background()
	api.during = func() {
		if _, err := svc.Append(ctx, service.AppendRequest{
			Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A1:B1",
			Values: [][]any{{"Zephrin", float64(12)}},
		}); err != nil {
			t.Errorf("Append: %v", err)
		}
	}
	if _, err := svc.Card(ctx, doc.ID); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Read(ctx, service.ReadRequest{Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:B"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Range != "'Bractal'!A1:B5" {
		t.Errorf("after the append the read covered %s, want 'Bractal'!A1:B5", res.Range)
	}
}
