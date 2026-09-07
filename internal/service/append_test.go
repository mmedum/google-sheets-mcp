package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
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
	// INSERT_ROWS is the default, and it moves everything below.
	if !res.Shifted || !strings.Contains(res.Render(), "moved down") {
		t.Errorf("an inserting append did not say addresses moved:\n%s", res.Render())
	}
}

// insert=overwrite writes over whatever follows the block, and which
// rows those are is decided during the call. So it is acknowledged
// rather than checked, and the refusal says which of the two it is.
func TestOverwriteAppendNeedsAcknowledgement(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}}, Insert: service.InsertOverwrite,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("insert=overwrite without overwrite gave %v", err)
	}
	if !strings.Contains(err.Error(), "cannot read them first") {
		t.Errorf("the refusal does not say why there is no preview: %q", err)
	}
	if wrote(srv) {
		t.Error("a refused append reached the wire")
	}

	res, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}}, Insert: service.InsertOverwrite, Overwrite: true,
	})
	if err != nil {
		t.Fatalf("acknowledged append: %v", err)
	}
	if res.Shifted {
		t.Error("an overwriting append claimed rows moved")
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

func TestInsertEnumIsClosed(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"x"}}, Insert: "shove",
	})
	if err == nil || !strings.Contains(err.Error(), "rows or overwrite") {
		t.Fatalf("an unknown insert option gave %v", err)
	}
	if len(srv.Calls()) != 0 {
		t.Error("a typo cost a request")
	}
}

// insert=overwrite without overwrite is refused, and the preview of that
// same call still answers — it is the call that explains the refusal.
func TestAppendDryRunPreviewsAnUnacknowledgedOverwrite(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Append(context.Background(), service.AppendRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B3",
		Values: [][]any{{"Trennow", float64(9)}}, Insert: service.InsertOverwrite, DryRun: true,
	})
	if err != nil {
		t.Fatalf("a dry run of an unacknowledged overwrite: %v", err)
	}
	if !strings.Contains(res.Render(), "would be refused") {
		t.Errorf("the preview does not say the call would be refused:\n%s", res.Render())
	}
	if wrote(srv) {
		t.Error("a dry run reached the wire")
	}
}
