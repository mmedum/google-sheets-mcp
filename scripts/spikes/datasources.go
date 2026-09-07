//go:build live

package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mmedum/google-sheets-mcp/internal/auth"
)

// spikeN answers §15.N: whether manage_data_source is a tool this
// server can honestly ship.
//
// §8 lists it for phase 4 and the discovery document attaches two
// conditions the tool surface never mentioned. addDataSource "requires
// an additional bigquery.readonly OAuth scope", and a BigQuery source
// needs "a BigQuery enabled Google Cloud project with a billing account
// attached", which any query against it is charged to. §17.6 decided
// this server asks for spreadsheets and drive.readonly and nothing else.
//
// So the question is not how the tool should be shaped. It is whether
// the calls behind it can be made at all under the scopes this server
// holds, and what a caller sees when they cannot. A tool that returns a
// 403 for every caller who has not separately built a billing-attached
// BigQuery project is not a tool; it is a support ticket.
//
// Nothing here creates a data source. Every step is a call this server
// would make, sent to see what comes back.
func spikeN(ctx context.Context) {
	sec("Spike N: data sources, and whether this server can reach one")

	line("")
	line("  Q1: what scopes does this token actually carry?")
	// The whole spike turns on this, and it is worth printing rather
	// than asserting: the answer to every step below is either the
	// scope or the account, and the two are told apart here.
	for _, s := range auth.Scopes(false, false) {
		line("    %s", s)
	}
	line("    the discovery document asks for bigquery.readonly on top of these")

	line("")
	line("  Q2: does the card mask accept the data source fields at all?")
	// If this fails, get_spreadsheet cannot even report that a
	// spreadsheet has data sources, which is a smaller question than
	// managing them and a prerequisite for it.
	for _, mask := range []string{
		"dataSources",
		"dataSourceSchedules",
		"dataSources(dataSourceId,sheetId,spec)",
		"sheets(properties(sheetId,sheetType,dataSourceSheetProperties))",
	} {
		status, body := call(ctx, http.MethodGet,
			sheetsBase+"/spreadsheets/"+scratchID+"?fields="+url.QueryEscape(mask), nil)
		line("    %-52s -> HTTP %d  %s", mask, status, first120(body))
	}

	line("")
	line("  Q3: what does addDataSource say under these scopes?")
	// A syntactically complete BigQuery spec against a project id that
	// does not exist. Two things can refuse it — the missing scope and
	// the missing project — and the message says which, which is the
	// whole point of sending it.
	status, body := batchOne(ctx, map[string]any{"addDataSource": map[string]any{
		"dataSource": map[string]any{"spec": map[string]any{
			"bigQuery": map[string]any{
				"projectId": "no-such-project-" + shortStamp(),
				"querySpec": map[string]any{"rawQuery": "SELECT 1"},
			},
		}},
	}})
	line("    addDataSource, BigQuery, raw query -> HTTP %d  %s", status, first120(body))

	status, body = batchOne(ctx, map[string]any{"addDataSource": map[string]any{
		"dataSource": map[string]any{"spec": map[string]any{}},
	}})
	line("    addDataSource with an empty spec   -> HTTP %d  %s", status, first120(body))

	line("")
	line("  Q4: and the other three calls, with nothing to aim them at?")
	// The error a caller would see for "refresh my data source" on a
	// spreadsheet that has none. If it is indistinguishable from the
	// scope refusal above, a tool here could not tell a caller which of
	// the two happened.
	status, body = batchOne(ctx, map[string]any{"refreshDataSource": map[string]any{"isAll": true}})
	line("    refreshDataSource isAll            -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"refreshDataSource": map[string]any{"dataSourceId": "1080547365"}})
	line("    refreshDataSource by id            -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"deleteDataSource": map[string]any{"dataSourceId": "1080547365"}})
	line("    deleteDataSource by id             -> HTTP %d  %s", status, first120(body))
	status, body = batchOne(ctx, map[string]any{"cancelDataSourceRefresh": map[string]any{"isAll": true}})
	line("    cancelDataSourceRefresh isAll      -> HTTP %d  %s", status, first120(body))

}

// shortStamp is a suffix that differs between runs, so the project id
// this spike invents cannot collide with one somebody really owns.
func shortStamp() string {
	return strconv.FormatInt(time.Now().UnixNano()%100000, 10)
}
