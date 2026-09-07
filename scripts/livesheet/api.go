//go:build live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// The driver builds its own scratch spreadsheet, and these are the calls
// it makes to do it.
//
// They are deliberately the driver's own rather than internal/gapi's:
// the server has no write path in this phase, and giving it one so the
// driver could set up would be designing the write path in the wrong
// place. Everything the driver *checks* goes through the server.
type driverAPI struct {
	http   *http.Client
	sheets string
	drive  string
}

func newDriverAPI(ctx context.Context, ts oauth2.TokenSource) *driverAPI {
	return &driverAPI{
		http:   oauth2.NewClient(ctx, ts),
		sheets: "https://sheets.googleapis.com/v4",
		drive:  "https://www.googleapis.com/drive/v3",
	}
}

// do sends one setup request, retrying what Google tells us to retry.
//
// The driver's own scaffolding is not what is under test, and Google
// returns transient 503s on this project — a run aborted before its
// first step because creating the scratch spreadsheet got one. A driver
// that fails for a reason that says nothing about the server is a driver
// people stop running. The retry says so out loud, so a run that needed
// three attempts does not read like one that needed none.
func (a *driverAPI) do(ctx context.Context, method, rawURL string, body, out any) error {
	const attempts = 4
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		var status int
		status, err = a.once(ctx, method, rawURL, body, out)
		if err == nil {
			if attempt > 1 {
				line("      (setup call succeeded on attempt %d)", attempt)
			}
			return nil
		}
		if status != 429 && status < 500 {
			return err
		}
		if attempt < attempts {
			line("      (setup call got HTTP %d, retrying)", status)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
	}
	return err
}

// once is one attempt, returning the status so do can decide.
func (a *driverAPI) once(ctx context.Context, method, rawURL string, body, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s returned %d: %s", method, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.Unmarshal(data, out)
}

// createSpreadsheet makes the scratch spreadsheet and returns its id and
// the title of its first sheet — which the driver reads rather than
// assumes, because Google names it in the account's language and this
// run is the one place that would notice.
func (a *driverAPI) createSpreadsheet(ctx context.Context, title string) (id, firstSheet string, err error) {
	var out struct {
		SpreadsheetID string `json:"spreadsheetId"`
		Sheets        []struct {
			Properties struct {
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	body := map[string]any{"properties": map[string]any{"title": title}}
	if err := a.do(ctx, http.MethodPost, a.sheets+"/spreadsheets", body, &out); err != nil {
		return "", "", err
	}
	if len(out.Sheets) == 0 {
		return "", "", fmt.Errorf("the new spreadsheet has no sheets")
	}
	return out.SpreadsheetID, out.Sheets[0].Properties.Title, nil
}

// addSheet adds a tab, so the driver can check that a second sheet is
// found by title and by id.
func (a *driverAPI) addSheet(ctx context.Context, id, title string) (int, error) {
	var out struct {
		Replies []struct {
			AddSheet struct {
				Properties struct {
					SheetID int `json:"sheetId"`
				} `json:"properties"`
			} `json:"addSheet"`
		} `json:"replies"`
	}
	body := map[string]any{"requests": []any{
		map[string]any{"addSheet": map[string]any{"properties": map[string]any{"title": title}}},
	}}
	u := a.sheets + "/spreadsheets/" + url.PathEscape(id) + ":batchUpdate"
	if err := a.do(ctx, http.MethodPost, u, body, &out); err != nil {
		return 0, err
	}
	if len(out.Replies) == 0 {
		return 0, fmt.Errorf("addSheet returned no reply")
	}
	return out.Replies[0].AddSheet.Properties.SheetID, nil
}

// putValues fills a range. USER_ENTERED so the formulas the driver
// writes become formulas, which is what makes show=both worth checking.
func (a *driverAPI) putValues(ctx context.Context, id, a1Range string, values [][]any) error {
	u := a.sheets + "/spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(a1Range) +
		"?valueInputOption=USER_ENTERED"
	return a.do(ctx, http.MethodPut, u, map[string]any{"values": values}, nil)
}

// addNote attaches a note to one cell, which no values read shows.
func (a *driverAPI) addNote(ctx context.Context, id string, sheetID, row, col int, note string) error {
	// The zero-based half-open conversion is a1's, even here. Writing it
	// out again in the one program that talks to the real API is how an
	// off-by-one gets tested against nothing.
	cell := a1.Rect{FirstRow: row, LastRow: row, FirstCol: col, LastCol: col}
	body := map[string]any{"requests": []any{map[string]any{
		"repeatCell": map[string]any{
			"range":  cell.GridRange(sheetID),
			"cell":   map[string]any{"note": note},
			"fields": "note",
		},
	}}}
	u := a.sheets + "/spreadsheets/" + url.PathEscape(id) + ":batchUpdate"
	return a.do(ctx, http.MethodPost, u, body, nil)
}

// about returns the signed-in address, so a step can search by owner
// without anybody typing one in.
func (a *driverAPI) about(ctx context.Context) (string, error) {
	var out struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	if err := a.do(ctx, http.MethodGet, a.drive+"/about?fields=user", nil, &out); err != nil {
		return "", err
	}
	return out.User.EmailAddress, nil
}

// decorate adds the three things a values read cannot show — a merge, a
// validation rule and a number format — so the options that surface them
// have something to surface.
func (a *driverAPI) decorate(ctx context.Context, id string, sheetID int) error {
	merge := a1.Rect{FirstCol: 1, FirstRow: 8, LastCol: 3, LastRow: 8}
	validated := a1.Rect{FirstCol: 1, FirstRow: 9, LastCol: 1, LastRow: 9}
	formatted := a1.Rect{FirstCol: 2, FirstRow: 10, LastCol: 2, LastRow: 10}
	body := map[string]any{"requests": []any{
		map[string]any{"mergeCells": map[string]any{
			"range": merge.GridRange(sheetID), "mergeType": "MERGE_ALL",
		}},
		map[string]any{"setDataValidation": map[string]any{
			"range": validated.GridRange(sheetID),
			"rule": map[string]any{
				"condition": map[string]any{"type": "ONE_OF_LIST", "values": []any{
					map[string]any{"userEnteredValue": "Skerry"},
					map[string]any{"userEnteredValue": "Plimth"},
				}},
				"strict": true,
			},
		}},
		map[string]any{"repeatCell": map[string]any{
			"range":  formatted.GridRange(sheetID),
			"cell":   map[string]any{"userEnteredFormat": map[string]any{"numberFormat": map[string]any{"type": "CURRENCY", "pattern": "\u00a3#,##0.00"}}},
			"fields": "userEnteredFormat.numberFormat",
		}},
	}}
	return a.do(ctx, http.MethodPost, a.sheets+"/spreadsheets/"+url.PathEscape(id)+":batchUpdate", body, nil)
}

// trash moves the scratch spreadsheet to the bin.
//
// It needs a write-capable Drive scope, and this server asks for
// drive.readonly on purpose (§17.6), so it will normally fail. That is
// reported rather than swallowed: the run ends by telling the person
// which file to remove and why the driver could not.
func (a *driverAPI) trash(ctx context.Context, id string) error {
	u := a.drive + "/files/" + url.PathEscape(id) + "?supportsAllDrives=true"
	return a.do(ctx, http.MethodPatch, u, map[string]any{"trashed": true}, nil)
}
