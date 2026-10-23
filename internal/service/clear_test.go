package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
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

// A clear names the cells it takes, so the client's prompt shows them:
// an omitted range, which meant the whole sheet, and an anchor, which
// resolves when the call runs, are both refused with the range each
// stands for now. Nothing is cleared, and the range named goes through.
func TestAClearNamesItsRange(t *testing.T) {
	srv, svc := destructive(t)
	ctx := context.Background()
	anchorOn(t, svc, "the totals", "24:24")
	for _, tc := range []struct {
		sheet, rng, want string
	}{
		{sheetstest.SecondSheet, "", `[invalid] clear_values needs range, so the call names what goes: the whole of ` +
			`"Ürväl" is 'Ürväl'!A1:H50. Pass that to clear all of it`},
		{sheetstest.SecondSheet, "  ", `[invalid] clear_values needs range, so the call names what goes: the whole of ` +
			`"Ürväl" is 'Ürväl'!A1:H50. Pass that to clear all of it`},
		{"", "anchor:the totals", `[invalid] clear_values takes the range itself, not an anchor, so the call names ` +
			`what goes: anchor:the totals points at 'Vandel'!A24:L24 now. Pass that range`},
	} {
		_, err := svc.Clear(ctx, service.ClearRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: tc.sheet, Range: tc.rng, Confirm: true,
		})
		if err == nil || err.Error() != tc.want {
			t.Errorf("range %q:\n got %v\nwant %s", tc.rng, err, tc.want)
		}
	}
	if wrote(srv) {
		t.Fatal("a clear that did not name its range reached the wire")
	}
	if _, err := svc.Clear(ctx, service.ClearRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "'Ürväl'!A1:H50", Confirm: true,
	}); err != nil || !wrote(srv) {
		t.Errorf("the whole sheet, named: %v, wrote %v", err, wrote(srv))
	}
}
