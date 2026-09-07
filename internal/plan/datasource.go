package plan

import "github.com/mmedum/google-sheets-mcp/internal/gsheets"

// The Connected Sheets builders. Each needs the bigquery.readonly scope
// against a real BigQuery source, which is why manage_data_source is
// registered only with GSHEETS_ENABLE_DATA_SOURCES (§17.6a).

// AddDataSource connects a BigQuery query to the spreadsheet. Google
// makes a DATA_SOURCE sheet for it and starts a refresh.
func AddDataSource(projectID, query string) *gsheets.Request {
	return &gsheets.Request{AddDataSource: &gsheets.AddDataSourceRequest{
		DataSource: &gsheets.DataSource{Spec: &gsheets.DataSourceSpec{
			BigQuery: &gsheets.BigQueryDataSourceSpec{
				ProjectID: projectID,
				QuerySpec: &gsheets.BigQueryQuerySpec{RawQuery: query},
			},
		}},
	}}
}

// AddDataSourceTable connects a whole BigQuery table.
func AddDataSourceTable(projectID, tableProject, dataset, table string) *gsheets.Request {
	return &gsheets.Request{AddDataSource: &gsheets.AddDataSourceRequest{
		DataSource: &gsheets.DataSource{Spec: &gsheets.DataSourceSpec{
			BigQuery: &gsheets.BigQueryDataSourceSpec{
				ProjectID: projectID,
				TableSpec: &gsheets.BigQueryTableSpec{
					TableProjectID: tableProject, DatasetID: dataset, TableID: table,
				},
			},
		}},
	}}
}

// RefreshDataSource re-runs one source's query, or every one.
//
// force is deliberately not offered above this: it re-runs a source that
// is already in an error state, which is a decision about somebody
// else's billed query rather than a detail of this call.
func RefreshDataSource(id string) *gsheets.Request {
	if id == "" {
		return &gsheets.Request{RefreshDataSource: &gsheets.RefreshDataSourceRequest{IsAll: true}}
	}
	return &gsheets.Request{RefreshDataSource: &gsheets.RefreshDataSourceRequest{DataSourceID: id}}
}

// CancelDataSourceRefresh stops a refresh that is still running.
func CancelDataSourceRefresh(id string) *gsheets.Request {
	if id == "" {
		return &gsheets.Request{CancelDataSourceRefresh: &gsheets.CancelDataSourceRefreshRequest{IsAll: true}}
	}
	return &gsheets.Request{CancelDataSourceRefresh: &gsheets.CancelDataSourceRefreshRequest{DataSourceID: id}}
}

// DeleteDataSource removes a source, its sheet and every link to it.
func DeleteDataSource(id string) *gsheets.Request {
	return &gsheets.Request{DeleteDataSource: &gsheets.DeleteDataSourceRequest{DataSourceID: id}}
}
