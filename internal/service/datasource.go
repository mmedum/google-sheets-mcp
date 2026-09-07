package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Data source actions.
const (
	SourceAdd     = "add"
	SourceRefresh = "refresh"
	SourceCancel  = "cancel_refresh"
	SourceDelete  = "delete"
	SourceList    = "list"
)

// SourceRequest is what manage_data_source asks for.
type SourceRequest struct {
	Spreadsheet string
	Action      string
	// ID is the spreadsheet-scoped data source id, which list reports.
	// Empty means every source, for refresh and cancel_refresh only.
	ID string

	// Project is the Cloud project the queries are charged to.
	Project string
	Query   string
	// Dataset and Table connect a whole table instead of a query.
	Dataset      string
	Table        string
	TableProject string

	// Confirm is required by delete, and by nothing else here. Deleting
	// a data source removes the DATA_SOURCE sheet Google made for it and
	// everything on it — strictly more than delete_sheet does, and what
	// it removes cannot be got back without re-running a billed query.
	// Registration gates the tool and the service gates the act (§8), so
	// the confirm lives here, where the caller can be told what they are
	// agreeing to.
	Confirm bool
	DryRun  bool
}

// SourceRecord is one data source, as a listing reports it.
type SourceRecord struct {
	ID    string `json:"id"`
	Sheet string `json:"sheet,omitempty" jsonschema:"the DATA_SOURCE sheet Google made for it"`
	Kind  string `json:"kind,omitempty" jsonschema:"where the data comes from"`
}

// SourceResult is manage_data_source's answer.
type SourceResult struct {
	Summary     string         `json:"summary"`
	Spreadsheet string         `json:"spreadsheet"`
	Action      string         `json:"action"`
	Sources     []SourceRecord `json:"data_sources,omitempty"`
	ID          string         `json:"id,omitempty"`
	// State is how a refresh is going. A refresh is asynchronous, so a
	// call that succeeds has started the work rather than finished it.
	State  string `json:"state,omitempty" jsonschema:"the refresh's state: RUNNING, SUCCEEDED, FAILED or CANCELLED"`
	Error  string `json:"error,omitempty" jsonschema:"what Google said went wrong with the refresh, which is not the same as this call failing"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r SourceResult) Render() string { return r.Summary }

// ManageDataSource answers manage_data_source.
//
// Everything but list needs the bigquery.readonly scope, and the tool is
// registered only with GSHEETS_ENABLE_DATA_SOURCES so that scope is
// asked for at login (§17.6a). list is the exception on purpose: the
// card's mask carries dataSources under the ordinary scopes, so
// get_spreadsheet reports a connected source for every user.
//
// A refresh is asynchronous. The call returning does not mean the data
// arrived, so the result reports the state Google gave rather than
// claiming the sheet is up to date.
func (s *Service) ManageDataSource(ctx context.Context, req SourceRequest) (*SourceResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	res := &SourceResult{Spreadsheet: ref.ID, Action: req.Action}
	switch req.Action {
	case SourceList:
		return s.listSources(ctx, ref, res)
	case SourceAdd:
		return s.addSource(ctx, ref, req, res)
	case SourceRefresh, SourceCancel:
		return s.refreshSource(ctx, ref, req, res)
	case SourceDelete:
		return s.deleteSource(ctx, ref, req, res)
	}
	return nil, Errorf("invalid",
		"action %q is not one of add, refresh, cancel_refresh or list", req.Action)
}

// listSources reads the card, which carries the sources whatever the
// scopes are.
func (s *Service) listSources(ctx context.Context, ref Reference, res *SourceResult) (*SourceResult, error) {
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	res.Sources = sourceRecords(sp)
	res.Summary = render.SourceList(sourceRows(res.Sources))
	return res, nil
}

// addSource connects a BigQuery query or table.
func (s *Service) addSource(ctx context.Context, ref Reference, req SourceRequest, res *SourceResult) (*SourceResult, error) {
	project := strings.TrimSpace(req.Project)
	if project == "" {
		return nil, Errorf("invalid",
			"add needs project, a BigQuery-enabled Cloud project with a billing account attached. "+
				"Every query this source runs is charged to it")
	}
	query := strings.TrimSpace(req.Query)
	table := strings.TrimSpace(req.Table)
	switch {
	case query != "" && table != "":
		return nil, Errorf("invalid", "query and table are two ways to say the same thing; pass one")
	case query == "" && table == "":
		return nil, Errorf("invalid", "add needs query, some SQL to run, or table, a BigQuery table to connect whole")
	case table != "" && strings.TrimSpace(req.Dataset) == "":
		return nil, Errorf("invalid", "a table needs dataset, the BigQuery dataset it is in")
	}

	act := render.SourceAct{Action: SourceAdd, Project: project, Query: query, Table: table, Dataset: req.Dataset}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.SourcePreview(act)
		return res, nil
	}
	op := plan.AddDataSource(project, query)
	if table != "" {
		tableProject := strings.TrimSpace(req.TableProject)
		if tableProject == "" {
			tableProject = project
		}
		op = plan.AddDataSourceTable(project, tableProject, strings.TrimSpace(req.Dataset), table)
	}
	reply, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{op},
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	if r := firstReply(reply); r != nil && r.AddDataSource != nil {
		if ds := r.AddDataSource.DataSource; ds != nil {
			res.ID, act.ID = ds.DataSourceID, ds.DataSourceID
		}
		if st := r.AddDataSource.DataExecutionStatus; st != nil {
			res.State, res.Error = st.State, st.ErrorMessage
			act.State, act.Error = st.State, st.ErrorMessage
		}
	}
	res.Summary = render.SourceDone(act)
	return res, nil
}

// refreshSource starts or stops a refresh.
func (s *Service) refreshSource(ctx context.Context, ref Reference, req SourceRequest, res *SourceResult) (*SourceResult, error) {
	id := strings.TrimSpace(req.ID)
	act := render.SourceAct{Action: req.Action, ID: id}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.SourcePreview(act)
		return res, nil
	}
	op := plan.RefreshDataSource(id)
	if req.Action == SourceCancel {
		op = plan.CancelDataSourceRefresh(id)
	}
	reply, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{op},
	})
	if err != nil {
		return nil, wrap(err)
	}
	res.ID = id
	// The reply's status is the refresh's, not the request's: a 200 with
	// a FAILED state inside it is a call that worked and a refresh that
	// did not, and saying "done" to that would be the silent half-answer
	// this server exists to avoid.
	if r := firstReply(reply); r != nil && r.RefreshDataSource != nil && len(r.RefreshDataSource.Statuses) > 0 {
		if st := r.RefreshDataSource.Statuses[0].DataExecutionStatus; st != nil {
			res.State, res.Error = st.State, st.ErrorMessage
			act.State, act.Error = st.State, st.ErrorMessage
		}
	}
	res.Summary = render.SourceDone(act)
	return res, nil
}

// deleteSource removes a source, its sheet and the links to it.
func (s *Service) deleteSource(ctx context.Context, ref Reference, req SourceRequest, res *SourceResult) (*SourceResult, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, Errorf("invalid", "delete needs id, which manage_data_source list reports")
	}
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	// Named before it goes, from the card: the delete takes a whole
	// sheet with it, and the reply says nothing about what that was.
	_, byID := sheetTitles(sp)
	var sheet string
	for _, ds := range sp.DataSources {
		if ds.DataSourceID == id {
			sheet = byID[ds.SheetID]
		}
	}
	act := render.SourceAct{Action: SourceDelete, ID: id, Sheet: sheet}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.SourcePreview(act)
		return res, nil
	}
	// The same belt-and-braces check edit_dimensions makes for its own
	// destructive action: the tool that carries this one is registered
	// under the destructive flag, and a service that trusted that would
	// be trusting a registration decision made one layer up.
	if !s.cfg.EnableDestructive {
		return nil, Errorf("unsupported",
			"deleting a data source is off in this server; start it with GSHEETS_ENABLE_DESTRUCTIVE=true to turn it on")
	}
	if !req.Confirm {
		where := ""
		if sheet != "" {
			where = fmt.Sprintf(", and the sheet %q it made, with everything on it", sheet)
		}
		return nil, Errorf("blocked",
			"deleting the data source %s removes it%s, and getting it back means re-running the query it was "+
				"built from. Pass confirm to go ahead", id, where)
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.DeleteDataSource(id)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.ID = id
	res.Summary = render.SourceDone(act)
	return res, nil
}

// sourceRecords flattens the card's data sources.
//
// The id-to-title map is built once rather than scanned per source,
// which is what sheetTitles is for and what every other reader of a
// GridRange in this package already uses.
func sourceRecords(sp *gsheets.Spreadsheet) []SourceRecord {
	_, byID := sheetTitles(sp)
	out := make([]SourceRecord, 0, len(sp.DataSources))
	for _, ds := range sp.DataSources {
		rec := SourceRecord{ID: ds.DataSourceID, Sheet: byID[ds.SheetID]}
		if ds.Spec != nil && ds.Spec.BigQuery != nil {
			rec.Kind = "BigQuery"
		}
		out = append(out, rec)
	}
	return out
}

func sourceRows(found []SourceRecord) []render.SourceRow {
	out := make([]render.SourceRow, 0, len(found))
	for _, s := range found {
		out = append(out, render.SourceRow{ID: s.ID, Sheet: s.Sheet, Kind: s.Kind})
	}
	return out
}
