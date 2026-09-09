package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestClearNeedsConfirmAndSaysWhatIsThere(t *testing.T) {
	srv, svc := destructive(t)
	ctx := context.Background()
	req := service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
	}

	_, err := svc.Clear(ctx, req)
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("a clear without confirm gave %v", err)
	}
	if !strings.Contains(err.Error(), "cell(s)") || !strings.Contains(err.Error(), "cannot undo") {
		t.Errorf("the refusal does not say what it costs: %q", err)
	}
	if wrote(srv) {
		t.Fatal("an unconfirmed clear reached the wire")
	}

	req.Confirm = true
	res, err := svc.Clear(ctx, req)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if res.Cells == 0 {
		t.Error("the result does not say how much it removed")
	}
	// The API keeps everything but the values, so a clear that warned
	// about notes would be warning about the wrong tool.
	if !strings.Contains(res.Render(), "notes and validation rules were left alone") {
		t.Errorf("the summary does not say what survives:\n%s", res.Render())
	}
}

func TestClearDryRunSendsNothing(t *testing.T) {
	srv, svc := destructive(t)
	res, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3", DryRun: true,
	})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
		t.Errorf("a dry run did not say so:\n%s", res.Render())
	}
	if wrote(srv) {
		t.Error("a dry run reached the wire")
	}
}

// A protected range refuses a clear as surely as it refuses a write, and
// saying so before the call beats a 403 from Google that names an id.
func TestClearRefusesAProtectedRange(t *testing.T) {
	srv, svc := destructive(t)
	_, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:D1", Confirm: true,
	})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("a clear over a protected range gave %v", err)
	}
	if wrote(srv) {
		t.Error("a refused clear reached the wire")
	}
}

func TestClearPastTheSheetIsRefused(t *testing.T) {
	_, svc := destructive(t)
	_, err := svc.Clear(context.Background(), service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A60:B70", Confirm: true,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a clear past the sheet gave %v", err)
	}
}
