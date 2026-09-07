package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// ManageDataSourceInput is what manage_data_source takes.
type ManageDataSourceInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Action      string `json:"action" jsonschema:"add, refresh, cancel_refresh or list. Deleting is delete_data_source, its own tool, because it takes a whole sheet with it"`
	ID          string `json:"id,omitempty" jsonschema:"which data source, as list reports it. refresh and cancel_refresh take every source when it is left out; delete does not"`

	Project      string `json:"project,omitempty" jsonschema:"for add: a BigQuery-enabled Cloud project with a billing account attached. Every query this source runs is charged to it"`
	Query        string `json:"query,omitempty" jsonschema:"for add: the SQL to run"`
	Dataset      string `json:"dataset,omitempty" jsonschema:"for add: the BigQuery dataset, when connecting a whole table instead of a query"`
	Table        string `json:"table,omitempty" jsonschema:"for add: the BigQuery table to connect whole"`
	TableProject string `json:"table_project,omitempty" jsonschema:"for add: the project the table lives in, when that is not the project being charged"`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// DeleteDataSourceInput is what delete_data_source takes.
type DeleteDataSourceInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	ID          string `json:"id" jsonschema:"which data source, as manage_data_source list and get_spreadsheet both report"`
	Confirm     bool   `json:"confirm" jsonschema:"required: this removes the data source and the sheet Google made for it, with everything on that sheet, and getting it back means re-running the query"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

func registerDataSource(s *mcp.Server, d Deps) {
	add(s, d, Def[ManageDataSourceInput, *service.SourceResult]{
		Name: "manage_data_source",
		Description: "Connect a BigQuery data source to a spreadsheet, refresh it, cancel a refresh, delete it or " +
			"list what is connected. This is Connected Sheets, and it reaches outside the spreadsheet: it needs a " +
			"Cloud project with BigQuery enabled and billing attached, and every query is charged to that project. " +
			"A refresh runs in the background, so a call that succeeds has started the work rather than finished it, " +
			"and the result says which state Google put it in. " +
			"get_spreadsheet already reports whether a spreadsheet has data sources at all, with no extra scope, so " +
			"this tool is only needed to change one. " +
			"Deleting one is delete_data_source, which is off by default and needs confirm.",
		Kind: Connected,
		Handle: func(ctx context.Context, in ManageDataSourceInput) (*service.SourceResult, error) {
			return d.Service.ManageDataSource(ctx, service.SourceRequest{
				Spreadsheet: in.Spreadsheet, Action: in.Action, ID: in.ID,
				Project: in.Project, Query: in.Query,
				Dataset: in.Dataset, Table: in.Table, TableProject: in.TableProject,
				DryRun: in.DryRun,
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
func registerDeleteDataSource(s *mcp.Server, d Deps) {
	add(s, d, Def[DeleteDataSourceInput, *service.SourceResult]{
		Name: "delete_data_source",
		Description: "Delete a Connected Sheets data source, the sheet Google made for it, and everything on " +
			"that sheet. Getting it back means connecting the source again and re-running its query, which is " +
			"charged to the Cloud project behind it. " +
			"manage_data_source list and get_spreadsheet both report the ids. " +
			"This needs no BigQuery scope, so it reaches a data source somebody else connected.",
		Kind: Destructive,
		Handle: func(ctx context.Context, in DeleteDataSourceInput) (*service.SourceResult, error) {
			return d.Service.ManageDataSource(ctx, service.SourceRequest{
				Spreadsheet: in.Spreadsheet, Action: service.SourceDelete, ID: in.ID,
				Confirm: in.Confirm, DryRun: in.DryRun,
			})
		},
	})
}
