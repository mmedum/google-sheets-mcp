package gsheets

// Connected Sheets: an external data source, its refresh and its
// removal. Added in phase 4, behind a setting (§17.6a) because
// addDataSource needs a scope this server does not ask for by default.
//
// Only the BigQuery arm is modelled. Looker is the other, and it needs a
// Looker instance rather than a Cloud project, which is a second thing
// nobody has by accident.

// DataSource is an external source connected to a spreadsheet.
type DataSource struct {
	DataSourceID string          `json:"dataSourceId,omitempty"`
	SheetID      int             `json:"sheetId,omitempty"`
	Spec         *DataSourceSpec `json:"spec,omitempty"`
}

// DataSourceSpec says where the data comes from.
type DataSourceSpec struct {
	BigQuery *BigQueryDataSourceSpec `json:"bigQuery,omitempty"`
}

// BigQueryDataSourceSpec is a BigQuery source: either a whole table or a
// query. The project is charged for every query run against it.
type BigQueryDataSourceSpec struct {
	ProjectID string             `json:"projectId,omitempty"`
	QuerySpec *BigQueryQuerySpec `json:"querySpec,omitempty"`
	TableSpec *BigQueryTableSpec `json:"tableSpec,omitempty"`
}

// BigQueryQuerySpec is a raw SQL query.
type BigQueryQuerySpec struct {
	RawQuery string `json:"rawQuery,omitempty"`
}

// BigQueryTableSpec names a table.
type BigQueryTableSpec struct {
	TableProjectID string `json:"tableProjectId,omitempty"`
	DatasetID      string `json:"datasetId,omitempty"`
	TableID        string `json:"tableId,omitempty"`
}

// AddDataSourceRequest connects a source, and creates a DATA_SOURCE
// sheet for it.
type AddDataSourceRequest struct {
	DataSource *DataSource `json:"dataSource,omitempty"`
}

// AddDataSourceReply carries the source that was made and how its first
// refresh went.
type AddDataSourceReply struct {
	DataSource          *DataSource          `json:"dataSource,omitempty"`
	DataExecutionStatus *DataExecutionStatus `json:"dataExecutionStatus,omitempty"`
}

// RefreshDataSourceRequest re-runs a source's query.
type RefreshDataSourceRequest struct {
	DataSourceID string `json:"dataSourceId,omitempty"`
	IsAll        bool   `json:"isAll,omitempty"`
	Force        bool   `json:"force,omitempty"`
}

// RefreshDataSourceReply carries one status per object refreshed.
type RefreshDataSourceReply struct {
	Statuses []*RefreshDataSourceObjectExecutionStatus `json:"statuses,omitempty"`
}

// RefreshDataSourceObjectExecutionStatus is how one object's refresh went.
type RefreshDataSourceObjectExecutionStatus struct {
	DataExecutionStatus *DataExecutionStatus `json:"dataExecutionStatus,omitempty"`
}

// CancelDataSourceRefreshRequest stops a refresh in flight.
type CancelDataSourceRefreshRequest struct {
	DataSourceID string `json:"dataSourceId,omitempty"`
	IsAll        bool   `json:"isAll,omitempty"`
}

// DeleteDataSourceRequest removes a source, its sheet and the links to it.
type DeleteDataSourceRequest struct {
	DataSourceID string `json:"dataSourceId"`
}

// DataExecutionStatus is how a refresh went. A refresh is asynchronous,
// so a successful request means the work was started rather than done —
// which is why every result here reports the state rather than "done".
type DataExecutionStatus struct {
	State        string `json:"state,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	LastRefresh  string `json:"lastRefreshTime,omitempty"`
}
