package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// SearchDeveloperMetadata finds developer metadata entries by filter.
//
// A POST that only reads, and one of exactly three in this API (§11). It
// is marked so rather than inferred: derived from the method it would
// sit on the write limiter, refuse to retry, and be blocked under a dry
// run — three wrong answers from one inference. A test over this
// package's syntax tree allows the flag only on the three by name.
//
// It needs a write-capable scope. `developerMetadata.search` lists
// `spreadsheets`, `drive` and `drive.file`, and not
// `spreadsheets.readonly`, so this call is unavailable in read-only mode
// however harmless it is.
func (c *Client) SearchDeveloperMetadata(ctx context.Context, id string, filters []*gsheets.DataFilter) (*gsheets.SearchDeveloperMetadataResponse, error) {
	if len(filters) == 0 {
		// An empty filter list matches nothing rather than everything,
		// so a caller who meant "all of them" would get silence.
		return nil, fmt.Errorf("%w: a developer metadata search needs at least one filter", ErrInvalid)
	}
	body, err := json.Marshal(&gsheets.SearchDeveloperMetadataRequest{DataFilters: filters})
	if err != nil {
		return nil, fmt.Errorf("%w: the search could not be encoded", ErrInvalid)
	}
	out, err := c.do(ctx, request{
		op:          "developerMetadata.search",
		spreadsheet: id,
		method:      http.MethodPost,
		url:         c.sheets + "/spreadsheets/" + url.PathEscape(id) + "/developerMetadata:search",
		body:        body,
		readOnly:    true,
	})
	if err != nil {
		return nil, err
	}
	var res gsheets.SearchDeveloperMetadataResponse
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("%w: developerMetadata.search returned something this server cannot read", ErrUnavailable)
	}
	return &res, nil
}
