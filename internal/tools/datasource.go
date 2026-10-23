package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

// ManageDataSourceInput is what manage_data_source takes.
type ManageDataSourceInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Action      string `json:"action" jsonschema:"refresh, cancel_refresh or list. Connecting one is connect_data_source and deleting one is delete_data_source, each its own tool"`
	ID          string `json:"id,omitempty" jsonschema:"which data source, as list reports it. refresh needs it; cancel_refresh takes every source when it is left out"`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// ConnectDataSourceInput is what connect_data_source takes.
type ConnectDataSourceInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`

	Project      string `json:"project" jsonschema:"a BigQuery-enabled Cloud project with a billing account attached. Every query this source runs is charged to it"`
	Query        string `json:"query,omitempty" jsonschema:"the SQL to run. Give this or table"`
	Dataset      string `json:"dataset,omitempty" jsonschema:"the BigQuery dataset, when connecting a whole table instead of a query"`
	Table        string `json:"table,omitempty" jsonschema:"the BigQuery table to connect whole"`
	TableProject string `json:"table_project,omitempty" jsonschema:"the project the table lives in, when that is not the project being charged"`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would be connected and send nothing"`
}

// DeleteDataSourceInput is what delete_data_source takes.
type DeleteDataSourceInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	ID          string `json:"id,omitempty" jsonschema:"required: which data source, as manage_data_source list reports it. A call without it is refused naming each source and its sheet"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"required: the exact title of the sheet the data source made, which goes with it, as manage_data_source list names it. Refused [conflict] if that is not the source's sheet now; a dry run, or a call without it, names it"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"required: this removes the data source and the sheet Google made for it, with everything on that sheet, and getting it back means re-running the query"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

func registerDataSource(s *mcp.Server, d Deps) {
	add(s, d, Def[ManageDataSourceInput, *service.SourceResult]{
		Name: "manage_data_source",
		Description: "Refresh a Connected Sheets data source, cancel a refresh, or list what is connected. " +
			"This reaches outside the spreadsheet: every refresh runs the source's BigQuery query again, charged " +
			"to the Cloud project behind it, so refresh names one source by id. " +
			"A refresh runs in the background, so a call that succeeds has started the work rather than finished it, " +
			"and the result says which state Google put it in. " +
			"get_spreadsheet already reports whether a spreadsheet has data sources at all, with no extra scope. " +
			"Connecting one is connect_data_source; deleting one is delete_data_source, which is off by default and " +
			"needs confirm.",
		Kind: Connected,
		Handle: func(ctx context.Context, in ManageDataSourceInput) (*service.SourceResult, error) {
			return d.Service.ManageDataSource(ctx, service.SourceRequest{
				Spreadsheet: in.Spreadsheet, Action: in.Action, ID: in.ID, DryRun: in.DryRun,
			})
		},
	})

	// connect_data_source is its own tool rather than an action of
	// manage_data_source: a client remembers its approval per tool, never
	// per argument, so an approval given for a refresh would otherwise
	// cover a new query, billed to a project the call names (§9a).
	add(s, d, Def[ConnectDataSourceInput, *service.SourceResult]{
		Name: "connect_data_source",
		Description: "Connect a BigQuery query or table to a spreadsheet through Connected Sheets. Google makes a " +
			"sheet for it and runs the query, charged to the Cloud project named, now and on every refresh. " +
			"It needs a Cloud project with BigQuery enabled and billing attached. " +
			"The query runs in the background, so a call that succeeds has started it rather than finished it, and " +
			"the result gives the new source's id and the state Google put it in. " +
			"manage_data_source refreshes and lists sources.",
		Kind: Connected,
		Handle: func(ctx context.Context, in ConnectDataSourceInput) (*service.SourceResult, error) {
			return d.Service.ConnectDataSource(ctx, service.ConnectRequest{
				Spreadsheet: in.Spreadsheet, Project: in.Project, Query: in.Query,
				Dataset: in.Dataset, Table: in.Table, TableProject: in.TableProject, DryRun: in.DryRun,
			})
		},
	})
}

// delete_data_source is its own tool for the reason edit_dimensions'
// delete became delete_dimensions (§17a.10, §7.4): an action that
// removes what nobody can read back does not belong inside a tool whose
// annotations say it is not destructive. A host in an auto-approve mode
// reads those annotations and asks nobody.
//
// It is Destructive rather than Connected, and needs no BigQuery scope:
// spike N sent deleteDataSource under this server's two scopes and got a
// 400 about the id, not a 403 about the scope. So it is reachable —
// and therefore gateable — wherever a data source exists, including one
// somebody else connected.
//
// It carries Claude Code's mark (§9a): it takes the source's sheet, and
// Google unlinks every table, pivot table, formula and chart built on
// the source, so what other objects depend on goes with it, and Sheets
// cannot undo it.
func registerDeleteDataSource(s *mcp.Server, d Deps) {
	add(s, d, Def[DeleteDataSourceInput, *service.SourceResult]{
		Name: "delete_data_source",
		Description: "Delete a Connected Sheets data source, the sheet Google made for it, and everything on " +
			"that sheet. Getting it back means connecting the source again and re-running its query, which is " +
			"charged to the Cloud project behind it. " +
			"It takes the source's id and the title of its sheet, both as manage_data_source list reports them; " +
			"a call without the id is refused naming each source and its sheet. " +
			"This needs no BigQuery scope, so it reaches a data source somebody else connected.",
		Kind:            Destructive,
		PromptEveryCall: true,
		Handle: func(ctx context.Context, in DeleteDataSourceInput) (*service.SourceResult, error) {
			return d.Service.DeleteDataSource(ctx, service.DeleteSourceRequest{
				Spreadsheet: in.Spreadsheet, ID: in.ID, Sheet: in.Sheet, Confirm: in.Confirm, DryRun: in.DryRun,
			})
		},
	})
}
