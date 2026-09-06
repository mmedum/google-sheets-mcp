package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
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
	// defaults it bites a few rows before the cell budget does (§17a) and
	// this test is about the cell half.
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
