package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// addSource connects the source every test here starts from.
func addSource(t *testing.T, svc *service.Service) *service.SourceResult {
	t.Helper()
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceAdd,
		Project: "example-project", Query: "SELECT 1",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return res
}

// TestSourceAddReportsTheRefreshRatherThanClaimingItArrived is the
// asynchrony this tool has to be honest about: the request starts a
// refresh, and the call returning says nothing about whether the data is
// there.
func TestSourceAddReportsTheRefreshRatherThanClaimingItArrived(t *testing.T) {
	_, svc := standard(t)
	res := addSource(t, svc)
	if res.ID == "" {
		t.Fatal("add returned no id, so nothing else can name the source")
	}
	if res.State != "RUNNING" {
		t.Errorf("state = %q, want the refresh's own state", res.State)
	}
	text := res.Render()
	for _, want := range []string{"example-project", "background"} {
		if !strings.Contains(text, want) {
			t.Errorf("the result does not mention %q:\n%s", want, text)
		}
	}
}

func TestSourceAddTable(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceAdd,
		Project: "example-project", Dataset: "warehouse", Table: "orders",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(res.Render(), "warehouse.orders") {
		t.Errorf("the result does not name the table:\n%s", res.Render())
	}
}

func TestSourceAddRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.SourceRequest
		want string
	}{
		{"no project", service.SourceRequest{Query: "SELECT 1"}, "project"},
		{"neither query nor table", service.SourceRequest{Project: "p"}, "query"},
		{"both query and table", service.SourceRequest{Project: "p", Query: "SELECT 1", Table: "orders"}, "pass one"},
		{"a table with no dataset", service.SourceRequest{Project: "p", Table: "orders"}, "dataset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			tc.req.Action = service.SourceAdd
			_, err := svc.ManageDataSource(context.Background(), tc.req)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSourceList(t *testing.T) {
	_, svc := standard(t)
	added := addSource(t, svc)
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Sources) != 1 || res.Sources[0].ID != added.ID {
		t.Fatalf("sources = %+v", res.Sources)
	}
	if res.Sources[0].Kind != "BigQuery" || res.Sources[0].Sheet == "" {
		t.Errorf("the listing does not say what it is or where it lives: %+v", res.Sources[0])
	}
}

func TestSourceListEmpty(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Render(), "No data sources") {
		t.Errorf("empty listing reads:\n%s", res.Render())
	}
}

// TestSourceRefreshFailureIsNotACallFailure is the distinction the
// result has to keep: the request succeeds and the execution inside it
// fails, which live comes back as HTTP 200 with a FAILED status.
func TestSourceRefreshFailureIsNotACallFailure(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceRefresh, ID: "9999",
	})
	if err != nil {
		t.Fatalf("a failed refresh arrived as a failed call: %v", err)
	}
	if res.State != "FAILED" || res.Error == "" {
		t.Errorf("state = %q, error = %q; the result must carry the refresh's own failure", res.State, res.Error)
	}
	if !strings.Contains(res.Render(), "The request was accepted; the query was not") {
		t.Errorf("the result does not separate the two:\n%s", res.Render())
	}
}

func TestSourceRefreshAndCancel(t *testing.T) {
	_, svc := standard(t)
	added := addSource(t, svc)
	for _, action := range []string{service.SourceRefresh, service.SourceCancel} {
		res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
			Spreadsheet: sheetstest.FixtureID, Action: action, ID: added.ID,
		})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !strings.Contains(res.Render(), added.ID) {
			t.Errorf("%s does not name the source:\n%s", action, res.Render())
		}
	}
	// With no id, both act on every source, and the result says so
	// rather than naming one.
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceRefresh,
	})
	if err != nil {
		t.Fatalf("refresh all: %v", err)
	}
	if !strings.Contains(res.Render(), "every data source") {
		t.Errorf("refresh all reads:\n%s", res.Render())
	}
}

// TestSourceDeleteNamesTheSheetItTakes is the one thing the reply does
// not say: deleting a source deletes the DATA_SOURCE sheet Google made
// for it, and everything on that sheet with it.
func TestSourceDeleteNamesTheSheetItTakes(t *testing.T) {
	_, svc := destructive(t)
	added := addSource(t, svc)
	// Unconfirmed first: the delete takes a sheet with it, so it is
	// refused until the caller says so, and the refusal names the sheet.
	_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: added.ID,
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went ahead")
	}
	if !strings.Contains(err.Error(), "[blocked]") || !strings.Contains(err.Error(), "Data source 1") {
		t.Errorf("the refusal does not name what goes:\n%v", err)
	}

	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: added.ID, Confirm: true,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(res.Render(), "the sheet") {
		t.Errorf("the result does not name the sheet it took:\n%s", res.Render())
	}
	after, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceList,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after.Sources) != 0 {
		t.Errorf("sources after the delete = %+v", after.Sources)
	}
}

func TestSourceDeleteRefusals(t *testing.T) {
	_, svc := destructive(t)
	if _, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete,
	}); err == nil || !strings.Contains(err.Error(), "id") {
		t.Errorf("error = %v, want id named", err)
	}
	if _, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: "9999", Confirm: true,
	}); err == nil {
		t.Error("a delete of a source that does not exist was accepted")
	}
}

func TestSourceDryRun(t *testing.T) {
	srv, svc := standard(t)
	for _, req := range []service.SourceRequest{
		{Action: service.SourceAdd, Project: "example-project", Query: "SELECT 1"},
		{Action: service.SourceRefresh, ID: "1"},
		{Action: service.SourceDelete, ID: "1", Confirm: true},
	} {
		srv.Reset()
		req.Spreadsheet = sheetstest.FixtureID
		req.DryRun = true
		res, err := svc.ManageDataSource(context.Background(), req)
		if err != nil {
			t.Fatalf("%s dry run: %v", req.Action, err)
		}
		if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
			t.Errorf("%s dry run:\n%s", req.Action, res.Render())
		}
		for _, c := range srv.Calls() {
			if c.Op == "spreadsheets.batchUpdate" {
				t.Errorf("%s dry run sent a write", req.Action)
			}
		}
	}
}

func TestSourceUnknownAction(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: "reconnect",
	})
	if err == nil || !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("error = %v, want the action named", err)
	}
}

// TestSourceDeleteIsOffWithoutTheDestructiveFlag is the altitude finding
// of the phase's cleanup pass. Deleting a data source takes a whole
// sheet with it — by this project's own criterion (§17c) that is a
// destructive act, and it was reachable with GSHEETS_ENABLE_DESTRUCTIVE
// off, under a tool whose annotations told clients it was not
// destructive.
func TestSourceDeleteIsOffWithoutTheDestructiveFlag(t *testing.T) {
	_, svc := standard(t)
	added := addSource(t, svc)
	_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: added.ID, Confirm: true,
	})
	if err == nil {
		t.Fatal("a delete went ahead with the destructive flag off")
	}
	if !strings.Contains(err.Error(), "[unsupported]") {
		t.Errorf("error = %v, want unsupported naming the setting", err)
	}
}

// TestDeleteIsNotAManageAction is the other half of the split: the
// action is gone from the tool that used to carry it, so a caller
// reaching for it is told the tool that does.
func TestDeleteIsNotAManageAction(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: "remove",
	})
	if err == nil || !strings.Contains(err.Error(), "add, refresh, cancel_refresh or list") {
		t.Errorf("error = %v, want the actions manage_data_source has", err)
	}
}
