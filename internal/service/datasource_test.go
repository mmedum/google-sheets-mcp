package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v4/internal/plan"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

// addSource connects the source every test here starts from.
func addSource(t *testing.T, svc *service.Service) *service.SourceResult {
	t.Helper()
	res, err := svc.ConnectDataSource(context.Background(), service.ConnectRequest{
		Spreadsheet: sheetstest.FixtureID, Project: "example-project", Query: "SELECT 1",
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
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
	res, err := svc.ConnectDataSource(context.Background(), service.ConnectRequest{
		Spreadsheet: sheetstest.FixtureID, Project: "example-project", Dataset: "warehouse", Table: "orders",
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !strings.Contains(res.Render(), "warehouse.orders") {
		t.Errorf("the result does not name the table:\n%s", res.Render())
	}
}

func TestSourceAddRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  service.ConnectRequest
		want string
	}{
		{"no project", service.ConnectRequest{Query: "SELECT 1"}, "project"},
		{"neither query nor table", service.ConnectRequest{Project: "p"}, "query"},
		{"both query and table", service.ConnectRequest{Project: "p", Query: "SELECT 1", Table: "orders"}, "pass one"},
		{"a table with no dataset", service.ConnectRequest{Project: "p", Table: "orders"}, "dataset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, svc := standard(t)
			tc.req.Spreadsheet = sheetstest.FixtureID
			_, err := svc.ConnectDataSource(context.Background(), tc.req)
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
	// With no id, a cancel acts on every source, and the result says so
	// rather than naming one.
	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceCancel,
	})
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if res.Render() != "Done: Cancel the refresh of every data source in this spreadsheet.\n" {
		t.Errorf("cancel all reads:\n%s", res.Render())
	}
}

// A refresh names its source: every refresh runs a billed query, and
// refreshing them all is not reached by leaving id out. The refusal
// lists the ids, and nothing is sent, dry run or not.
func TestSourceRefreshNeedsAnID(t *testing.T) {
	srv, svc := standard(t)
	added := addSource(t, svc)
	for _, dry := range []bool{false, true} {
		srv.Reset()
		_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.SourceRefresh, DryRun: dry,
		})
		want := "[invalid] refresh needs id, the data source to run again; it has \"" + added.ID +
			"\" on the sheet \"Data source 1\". Each refresh runs its query again, charged to the Cloud project behind it"
		if err == nil || err.Error() != want {
			t.Errorf("dry run %v: error = %v\nwant %s", dry, err, want)
		}
		for _, c := range srv.Calls() {
			if c.Op == "spreadsheets.batchUpdate" {
				t.Errorf("dry run %v: a refresh with no id sent a write", dry)
			}
		}
	}
}

// Connecting is its own tool, so manage_data_source refuses add and
// names the tool that does it.
func TestManageDataSourceDoesNotConnect(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceAdd,
	})
	if err == nil || err.Error() != "[invalid] connecting a data source is connect_data_source, its own tool, "+
		"since it starts a query billed to a Cloud project" {
		t.Errorf("error = %v", err)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Error("add through manage_data_source sent a write")
		}
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
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: added.ID, Sheet: "Data source 1",
	})
	if err == nil {
		t.Fatal("an unconfirmed delete went ahead")
	}
	if !strings.Contains(err.Error(), "[blocked]") || !strings.Contains(err.Error(), "Data source 1") {
		t.Errorf("the refusal does not name what goes:\n%v", err)
	}

	res, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: added.ID, Sheet: "Data source 1", Confirm: true,
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

// TestSourceDeleteRefusals: a delete without an id, or with one the
// spreadsheet does not have, is refused naming each source and its
// sheet. With only the destructive flag on, manage_data_source is not
// registered, and these refusals are where the ids come from.
func TestSourceDeleteRefusals(t *testing.T) {
	_, svc := destructive(t)
	id := addSource(t, svc).ID
	for _, tc := range []struct {
		id, want string
	}{
		{"", `[invalid] delete needs id, the data source to delete; it has "` + id + `" on the sheet "Data source 1"`},
		{"9999", `[not_found] this spreadsheet has no data source "9999"; it has "` + id + `" on the sheet "Data source 1"`},
	} {
		_, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: tc.id, Sheet: "Data source 1", Confirm: true,
		})
		if err == nil || err.Error() != tc.want {
			t.Errorf("id %q: error = %v\nwant %s", tc.id, err, tc.want)
		}
	}
}

// TestADataSourceDeleteCarriesItsSheet holds delete_data_source's
// witness (§9a.7): the title of the sheet that goes with the source,
// which a client's prompt shows where an id means nothing to a person.
// Leaving it out is refused naming it, and a title that is not the
// source's sheet is refused, dry run or not. Nothing is sent either way.
func TestADataSourceDeleteCarriesItsSheet(t *testing.T) {
	srv, svc := destructive(t)
	id := addSource(t, svc).ID
	del := func(sheet string, dry bool) (*service.SourceResult, error) {
		return svc.ManageDataSource(context.Background(), service.SourceRequest{
			Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: id, Sheet: sheet,
			Confirm: true, DryRun: dry,
		})
	}
	srv.Reset()
	missing := `[invalid] delete needs sheet, the title of the sheet the data source made, so the call names the ` +
		`tab that goes with it: the data source ` + id + ` made the sheet "Data source 1". Pass that`
	if _, err := del("", false); err == nil || err.Error() != missing {
		t.Errorf("no sheet: error = %v\nwant %s", err, missing)
	}
	wrong := `[conflict] the data source ` + id + ` made the sheet "Data source 1", not "Data source 2"; read it ` +
		`again before deleting it`
	for _, dry := range []bool{false, true} {
		if _, err := del("Data source 2", dry); err == nil || err.Error() != wrong {
			t.Errorf("dry run %v, another sheet: error = %v\nwant %s", dry, err, wrong)
		}
	}
	preview, err := del("", true)
	if err != nil {
		t.Fatalf("a dry run without the sheet: %v", err)
	}
	if want := "Dry run: nothing was sent. This would delete the data source " + id +
		", and the sheet \"Data source 1\" Google made for it.\n"; preview.Render() != want {
		t.Errorf("dry run = %q\nwant %q", preview.Render(), want)
	}
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			t.Fatal("a refused or previewed delete sent a write")
		}
	}
	if _, err := del("Data source 1", false); err != nil {
		t.Fatalf("a delete naming its sheet: %v", err)
	}
	if len(srv.Doc(sheetstest.FixtureID).DataSources) != 0 {
		t.Error("the source is still there after a delete naming its sheet")
	}
}

// TestADataSourceWitnessIsReadPastTheCache: a sheet renamed since the
// card was cached is compared as it stands, so the title the person sees
// now is not refused as a conflict.
func TestADataSourceWitnessIsReadPastTheCache(t *testing.T) {
	srv, svc := destructive(t)
	id := addSource(t, svc).ID
	if _, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceList,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	sheetID := srv.Doc(sheetstest.FixtureID).DataSources[0].SheetID
	if _, err := srv.Client().BatchUpdate(context.Background(), sheetstest.FixtureID,
		&gsheets.BatchUpdateSpreadsheetRequest{Requests: []*gsheets.Request{plan.RenameSheet(sheetID, "Orders")}}); err != nil {
		t.Fatalf("rename behind the server's back: %v", err)
	}
	if _, err := svc.ManageDataSource(context.Background(), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: id, Sheet: "Orders", Confirm: true,
	}); err != nil {
		t.Errorf("a delete naming the sheet's title now: %v", err)
	}
}

func TestSourceDryRun(t *testing.T) {
	srv, svc := standard(t)
	id := addSource(t, svc).ID
	for _, call := range []struct {
		name string
		run  func() (*service.SourceResult, error)
	}{
		{"connect", func() (*service.SourceResult, error) {
			return svc.ConnectDataSource(context.Background(), service.ConnectRequest{
				Spreadsheet: sheetstest.FixtureID, Project: "example-project", Query: "SELECT 1", DryRun: true})
		}},
		{"refresh", func() (*service.SourceResult, error) {
			return svc.ManageDataSource(context.Background(), service.SourceRequest{
				Spreadsheet: sheetstest.FixtureID, Action: service.SourceRefresh, ID: id, DryRun: true})
		}},
		{"delete", func() (*service.SourceResult, error) {
			return svc.ManageDataSource(context.Background(), service.SourceRequest{
				Spreadsheet: sheetstest.FixtureID, Action: service.SourceDelete, ID: id, DryRun: true})
		}},
	} {
		srv.Reset()
		res, err := call.run()
		if err != nil {
			t.Fatalf("%s dry run: %v", call.name, err)
		}
		if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
			t.Errorf("%s dry run:\n%s", call.name, res.Render())
		}
		for _, c := range srv.Calls() {
			if c.Op == "spreadsheets.batchUpdate" {
				t.Errorf("%s dry run sent a write", call.name)
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
	if err == nil || !strings.Contains(err.Error(), "not one of refresh, cancel_refresh or list") {
		t.Errorf("error = %v, want the actions manage_data_source has", err)
	}
}
