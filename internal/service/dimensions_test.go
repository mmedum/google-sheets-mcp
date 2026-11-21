package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v5/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v5/internal/service"
)

func TestDimensionActions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		req         service.DimensionRequest
		wantShifted bool
		wantText    string
	}{
		{
			name:        "insert rows",
			req:         service.DimensionRequest{Action: service.DimInsert, Dimension: "rows", Band: "2:3"},
			wantShifted: true,
			wantText:    "Insert 2 row(s) before row 2",
		},
		{
			name:        "move rows",
			req:         service.DimensionRequest{Action: service.DimMove, Dimension: "rows", Band: "2:3", To: 6},
			wantShifted: true,
			wantText:    "Move rows 2:3",
		},
		{
			name:     "resize columns",
			req:      service.DimensionRequest{Action: service.DimResize, Dimension: "columns", Band: "B:C", Pixels: 120},
			wantText: "columns B:C",
		},
		{
			name:     "auto resize",
			req:      service.DimensionRequest{Action: service.DimAutoResize, Dimension: "columns", Band: "A:B"},
			wantText: "fit their contents",
		},
		{
			name:     "group",
			req:      service.DimensionRequest{Action: service.DimGroup, Dimension: "rows", Band: "2:5"},
			wantText: "Group rows 2:5",
		},
		{
			name:     "ungroup",
			req:      service.DimensionRequest{Action: service.DimUngroup, Dimension: "rows", Band: "2:5"},
			wantText: "Ungroup rows 2:5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.SecondSheet
			res, err := svc.EditDimensions(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("EditDimensions: %v", err)
			}
			if !strings.Contains(res.Render(), tc.wantText) {
				t.Errorf("summary = %q, missing %q", res.Render(), tc.wantText)
			}
			if res.Shifted != tc.wantShifted {
				t.Errorf("Shifted = %v, want %v", res.Shifted, tc.wantShifted)
			}
			// An action that moves addresses has to say so: a checkpoint
			// or an address the caller holds no longer points where it
			// did.
			if tc.wantShifted && !strings.Contains(res.Render(), "no longer points") {
				t.Errorf("a shifting change did not warn:\n%s", res.Render())
			}
		})
	}
}

func TestDimensionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.DimensionRequest
		want string
	}{
		{
			"dimension and band disagree",
			service.DimensionRequest{Action: service.DimInsert, Dimension: "rows", Band: "B:D"},
			"names columns and the dimension says rows",
		},
		{
			"a band past the sheet",
			service.DimensionRequest{Action: service.DimInsert, Dimension: "rows", Band: "60:70"},
			"past the end",
		},
		{
			"an unknown action",
			service.DimensionRequest{Action: "fold", Dimension: "rows", Band: "2:3"},
			"is not one of",
		},
		{
			"move with no destination",
			service.DimensionRequest{Action: service.DimMove, Dimension: "rows", Band: "2:3"},
			"needs to",
		},
		{
			"resize with no size",
			service.DimensionRequest{Action: service.DimResize, Dimension: "columns", Band: "B:C"},
			"needs pixels",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Sheet = sheetstest.SecondSheet
			_, err := svc.EditDimensions(context.Background(), tc.req)
			if err == nil {
				t.Fatal("it was allowed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, missing %q", err, tc.want)
			}
		})
	}
}

// An action a model cannot see is one it cannot reach — the unregistered
// tool rule one level down. The service refuses it too, because a
// description is not a gate.
func TestDeleteIsOffUnlessDestructiveIsOn(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.EditDimensions(context.Background(), service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "2:3",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") {
		t.Fatalf("delete with destructive off gave %v", err)
	}
	if !strings.Contains(err.Error(), "GSHEETS_ENABLE_DESTRUCTIVE") {
		t.Errorf("the refusal does not say how to turn it on: %q", err)
	}
}

func TestDeleteCountsThenNeedsConfirm(t *testing.T) {
	srv, svc := destructive(t)
	ctx := context.Background()
	req := service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "2:3",
	}

	_, err := svc.EditDimensions(ctx, req)
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Fatalf("delete without confirm gave %v", err)
	}
	if !strings.Contains(err.Error(), "non-empty cell") {
		t.Errorf("the refusal does not say what it costs: %q", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("an unconfirmed delete reached the wire")
		}
	}

	req.Confirm = true
	res, err := svc.EditDimensions(ctx, req)
	if err != nil {
		t.Fatalf("EditDimensions: %v", err)
	}
	if res.Cells == 0 || !res.Shifted {
		t.Errorf("a delete reported %+v", res)
	}
}

func TestDimensionDryRunSendsNothing(t *testing.T) {
	srv, svc := destructive(t)
	res, err := svc.EditDimensions(context.Background(), service.DimensionRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
		Action: service.DimDelete, Dimension: "rows", Band: "2:3", DryRun: true,
	})
	if err != nil {
		t.Fatalf("EditDimensions: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "would take") {
		t.Errorf("the preview does not say what it would cost:\n%s", res.Render())
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Error("a dry run reached the wire")
		}
	}
}

// A delete that cuts a pivot table's source shrinks it, and Google's
// reply says nothing, so the delete names the pivot beforehand. A band
// clear of the source names none, and a sheet the read budget did not
// finish is named as not searched.
func TestADeleteNamesThePivotWhoseSourceItCuts(t *testing.T) {
	ctx := context.Background()
	del := func(band string) service.DimensionRequest {
		return service.DimensionRequest{Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet,
			Action: service.DimDelete, Dimension: "columns", Band: band, DryRun: true}
	}
	_, svc := destructive(t)
	addPivot(t, svc, "H1")

	res, err := svc.EditDimensions(ctx, del("B:B"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "the pivot table at 'Vandel'!H1, which reads 'Vandel'!A1:C6"
	if len(res.Pivots) != 1 || res.Pivots[0] != want {
		t.Errorf("pivots = %q, want [%q]", res.Pivots, want)
	}
	if !strings.Contains(res.Render(), "The pivot table at 'Vandel'!H1, which reads 'Vandel'!A1:C6 stays where it is") ||
		strings.Contains(res.Render(), "did not reach") {
		t.Errorf("the preview does not name the pivot, or says it did not search:\n%s", res.Render())
	}
	confirmless := del("B:B")
	confirmless.DryRun = false
	if _, err := svc.EditDimensions(ctx, confirmless); err == nil ||
		!strings.Contains(err.Error(), "Sheets cannot undo it. The pivot table at 'Vandel'!H1, which reads 'Vandel'!A1:C6 stays") {
		t.Errorf("the refusal does not name the pivot: %v", err)
	}

	clear, err := svc.EditDimensions(ctx, del("K:K"))
	if err != nil {
		t.Fatal(err)
	}
	if len(clear.Pivots) != 0 {
		t.Errorf("a band clear of the source named %q", clear.Pivots)
	}
	// A band that takes the anchor takes the pivot, which is not one
	// that stays and reads less.
	if takes, err := svc.EditDimensions(ctx, del("A:H")); err != nil || len(takes.Pivots) != 0 {
		t.Errorf("a band over the anchor named %q as staying (%v)", pivotsOf(takes), err)
	}

	srv := sheetstest.Standard(t)
	fs, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	fs.EnableDestructive, fs.MaxCells = true, 4
	small := service.New(service.Deps{API: srv.Client(), Config: fs})
	addPivot(t, small, "H1")
	res, err = small.EditDimensions(ctx, del("B:B"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Render(), "The search for pivot tables did not reach") {
		t.Errorf("a search the budget cut short is not said to be:\n%s", res.Render())
	}
}

func pivotsOf(r *service.DimensionResult) []string {
	if r == nil {
		return nil
	}
	return r.Pivots
}
