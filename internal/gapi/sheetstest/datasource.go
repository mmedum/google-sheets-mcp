package sheetstest

import (
	"errors"
	"strconv"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The fake's Connected Sheets.
//
// One thing it cannot reproduce, and the tests say so rather than
// pretending otherwise: the live 403 for a missing bigquery.readonly
// scope. Nothing here knows which scopes a token carries, so what is
// covered is the shape of each call and what the results say — and the
// scope refusal is spike N's transcript and §18's row, not a test's.

// applySource is the fake's half of the Connected Sheets union.
func applySource(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.AddDataSource != nil:
		return addDataSource(d, req.AddDataSource)
	case req.RefreshDataSource != nil:
		return refreshDataSource(d, req.RefreshDataSource)
	case req.CancelDataSourceRefresh != nil:
		return &gsheets.Reply{}, true, nil
	case req.DeleteDataSource != nil:
		return deleteDataSource(d, req.DeleteDataSource.DataSourceID)
	}
	return nil, false, nil
}

func addDataSource(d *Doc, req *gsheets.AddDataSourceRequest) (*gsheets.Reply, bool, error) {
	if req.DataSource == nil || req.DataSource.Spec == nil || req.DataSource.Spec.BigQuery == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("No data source specification is set.")
	}
	// Google makes a DATA_SOURCE sheet for every source, and the id it
	// gives the source is its own.
	sh := &Sheet{Props: gsheets.SheetProperties{
		SheetID: nextSheetID(d), Title: nextTitle(d, "Data source "), SheetType: "DATA_SOURCE",
	}}
	d.Sheets = append(d.Sheets, sh)
	source := &gsheets.DataSource{
		DataSourceID: strconv.Itoa(nextSourceID(d)),
		SheetID:      sh.Props.SheetID,
		Spec:         req.DataSource.Spec,
	}
	d.DataSources = append(d.DataSources, source)
	// A refresh starts in the background, so the reply says RUNNING
	// rather than that the data is there.
	return &gsheets.Reply{AddDataSource: &gsheets.AddDataSourceReply{
		DataSource:          source,
		DataExecutionStatus: &gsheets.DataExecutionStatus{State: "RUNNING"},
	}}, true, nil
}

func refreshDataSource(d *Doc, req *gsheets.RefreshDataSourceRequest) (*gsheets.Reply, bool, error) {
	// An id that names nothing comes back as a failed execution rather
	// than a failed request, which is what the live run showed: HTTP 200
	// with a status inside saying the object does not exist.
	if req.DataSourceID != "" && findSource(d, req.DataSourceID) == nil {
		return &gsheets.Reply{RefreshDataSource: &gsheets.RefreshDataSourceReply{
			Statuses: []*gsheets.RefreshDataSourceObjectExecutionStatus{{
				DataExecutionStatus: &gsheets.DataExecutionStatus{
					State: "FAILED", ErrorCode: "OBJECT_NOT_FOUND",
					ErrorMessage: "The data source object doesn't exist.",
				},
			}},
		}}, true, nil
	}
	return &gsheets.Reply{RefreshDataSource: &gsheets.RefreshDataSourceReply{
		Statuses: []*gsheets.RefreshDataSourceObjectExecutionStatus{{
			DataExecutionStatus: &gsheets.DataExecutionStatus{State: "RUNNING"},
		}},
	}}, true, nil
}

// deleteDataSource takes the source's sheet with it, which is what the
// reference says and what makes the delete worth naming beforehand.
func deleteDataSource(d *Doc, id string) (*gsheets.Reply, bool, error) {
	for i, ds := range d.DataSources {
		if ds.DataSourceID != id {
			continue
		}
		for j, sh := range d.Sheets {
			if sh.Props.SheetID == ds.SheetID {
				d.Sheets = append(d.Sheets[:j], d.Sheets[j+1:]...)
				break
			}
		}
		d.DataSources = append(d.DataSources[:i], d.DataSources[i+1:]...)
		return &gsheets.Reply{}, true, nil
	}
	return nil, true, errors.New("Invalid requests[0].deleteDataSource: The data source ID doesn't exist: " + id)
}

func findSource(d *Doc, id string) *gsheets.DataSource {
	for _, ds := range d.DataSources {
		if ds.DataSourceID == id {
			return ds
		}
	}
	return nil
}

func nextSourceID(d *Doc) int {
	next := 1080547365
	for _, ds := range d.DataSources {
		if n, err := strconv.Atoi(ds.DataSourceID); err == nil && n >= next {
			next = n + 1
		}
	}
	return next
}
