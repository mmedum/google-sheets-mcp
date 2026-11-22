package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v5/internal/config"
	"github.com/mmedum/google-sheets-mcp/v5/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v5/internal/service"
)

// A budget past the maximum is refused rather than quietly reduced, and
// the refusal names the dial. Clamping would answer a question nobody
// asked: the footer would describe a window the caller did not request,
// and a continuation would resume from a row they cannot predict.
func TestBudgetsPastTheMaximumAreRefused(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	read := func(b service.Budget) error {
		_, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B2", Budget: b,
		})
		return err
	}
	find := func(b service.Budget) error {
		_, err := svc.Find(ctx, service.FindRequest{
			Spreadsheet: sheetstest.FixtureID, Query: "Plimth", Budget: b,
		})
		return err
	}

	for _, tc := range []struct {
		name string
		call func(service.Budget) error
		bud  service.Budget
		want string
	}{
		{"cells past the maximum", read, service.Budget{Cells: config.MaxMaxCells + 1}, "max_cells"},
		{"chars past the maximum", read, service.Budget{Chars: config.MaxMaxChars + 1}, "max_chars"},
		{"matches past the maximum", find, service.Budget{Matches: service.MaxMaxMatches + 1}, "max_matches"},
		{"a negative cell budget", read, service.Budget{Cells: -1}, "max_cells"},
		{"a negative character budget", read, service.Budget{Chars: -1}, "max_chars"},
		{"a negative match limit", find, service.Budget{Matches: -1}, "max_matches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(tc.bud)
			if err == nil {
				t.Fatalf("%v was accepted", tc.bud)
			}
			if !strings.HasPrefix(err.Error(), "[invalid]") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want an [invalid] naming %s", err, tc.want)
			}
		})
	}
}

// A zero means "whatever this deployment configured", which is the only
// reason a tool can pass the caller's numbers through untouched.
func TestZeroBudgetTakesTheConfiguredDefault(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(5000, 20)
	srv.Add(doc, file)
	svc := newService(t, srv)

	// The character budget is raised out of the way, because at the two
	// defaults it bites a few rows before the cell budget does, and this
	// test is about the cell half.
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:T",
		Budget: service.Budget{Chars: config.MaxMaxChars},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// 5000 cells over 20 columns is 250 rows, so the read stops there
	// and the next one continues at 251. A default that had not been
	// applied would have read the whole 5000-row sheet.
	if res.ContinueFrom != config.DefaultMaxCells/20+1 {
		t.Errorf("continue_from = %d, want the configured cell default to have bounded the read", res.ContinueFrom)
	}
}

// A raised cell budget fetches no rows the character budget could never
// render: each row draws at least " |" a cell and a newline, so the fetch
// stops where even rows of empty cells would run out of characters.
func TestTheFetchStopsWhereTheRenderingMust(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(5000, 2)
	srv.Add(doc, file)
	svc := newService(t, srv)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:B",
		Budget: service.Budget{Cells: config.MaxMaxCells, Chars: 1000},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// 1000 characters at 2 a cell and 1 a row is 200 rows, plus the one
	// row always drawn.
	if res.Range != "'Bractal'!A1:B201" {
		t.Errorf("the read fetched %s, want 'Bractal'!A1:B201", res.Range)
	}
	if res.ContinueFrom < 2 || res.ContinueFrom > 202 {
		t.Errorf("continue_from = %d, want a row the fetch reached", res.ContinueFrom)
	}
}

// A sparse range renders runs of empty rows as one line, so a page cut
// by the character bound can be shorter than the budget allows. It still
// says where to read on, so nothing is skipped.
func TestASparsePageCutByTheBoundSaysWhereToReadOn(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(400, 2)
	for r := 2; r <= 400; r++ {
		if r%100 != 0 {
			doc.Sheets[0].Set(r, 1, nil)
			doc.Sheets[0].Set(r, 2, nil)
		}
	}
	srv.Add(doc, file)
	svc := newService(t, srv)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:B",
		Budget: service.Budget{Cells: config.MaxMaxCells, Chars: 500},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Range != "'Bractal'!A1:B101" || res.ContinueFrom != 102 {
		t.Errorf("read %s and continues at %d, want 'Bractal'!A1:B101 and 102", res.Range, res.ContinueFrom)
	}
}

// A read of a sheet's allocated width, cut short over data that ends far
// to the left, says which narrower range reads more rows a call. It is a
// hint, since a later row might reach further right.
func TestACutReadNamesTheNarrowerRange(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(3000, 2)
	doc.Sheets[0].Props.GridProperties.ColumnCount = 26
	srv.Add(doc, file)
	svc := newService(t, srv)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal",
		Budget: service.Budget{Chars: config.MaxMaxChars},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	const want = "every row shown ends by column B, so 'Bractal'!A193:B3000 reads more rows a call"
	if !strings.Contains(res.Footer, want) {
		t.Errorf("footer %q does not say %q", res.Footer, want)
	}

	narrow, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Range: "A:B", Budget: service.Budget{Chars: config.MaxMaxChars},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(narrow.Footer, "every row shown ends") {
		t.Errorf("a read already as narrow as its data still offers a narrower range: %q", narrow.Footer)
	}
}

// Data that ends before the read was cut gains nothing from more rows a
// call, so the footer offers no narrower range for it.
func TestNoNarrowerRangeOnceTheDataHasEnded(t *testing.T) {
	srv := sheetstest.New(t)
	doc, file := sheetstest.Large(20, 2)
	doc.Sheets[0].Props.GridProperties.RowCount = 3000
	doc.Sheets[0].Props.GridProperties.ColumnCount = 26
	srv.Add(doc, file)
	svc := newService(t, srv)
	res, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: doc.ID, Sheet: "Bractal", Budget: service.Budget{Chars: config.MaxMaxChars},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.ContinueFrom == 0 {
		t.Fatal("the read was not cut, so this test shows nothing")
	}
	if !strings.Contains(res.Footer, "data ends at row 20") || strings.Contains(res.Footer, "every row shown ends") {
		t.Errorf("a read whose data ended offers a narrower range, or does not say the data ended: %q", res.Footer)
	}
}
