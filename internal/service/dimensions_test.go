package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
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
