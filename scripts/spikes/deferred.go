//go:build live

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v4/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi"
)

// askFoldedReads asks which reads a wider field mask can fold into a
// call this server already makes. Each fold saves a request, and only
// Google can say whether the mask is accepted and what it adds to a
// payload: the fake returns whatever a test asks it for.
//
// It asks three things. Can a batchUpdate carry the card back, so the
// write that invalidated it need not re-read it? Can the card carry a
// count of the conditional format rules? Can a grid read carry the
// anchors on the rows it reads, so they need no search of their own?
func askFoldedReads(ctx context.Context) {
	const sheet = "SpikeDeferred"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:B4", [][]any{
		{"Region", "Units"},
		{"Plimth", 120},
		{"Quorbin", 85},
		{"Vandel", 143},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}

	line("")
	line("  can a batchUpdate carry the card back, and can it be masked?")
	// The fold stands or falls on whether the returned spreadsheet
	// arrives unmasked, and so carries the spreadsheetUrl the card's own
	// mask deliberately drops. The discovery document says
	// responseIncludeGridData is "ignored if a field mask was set in the
	// request", which implies the standard fields parameter applies to
	// this reply too. Implies is not evidence.
	renameTo := "SpikeDeferredRenamed"
	body := map[string]any{
		"requests": []any{map[string]any{"updateSheetProperties": map[string]any{
			"properties": map[string]any{"sheetId": sheetID, "title": renameTo},
			"fields":     "title",
		}}},
		"includeSpreadsheetInResponse": true,
		"responseRanges":               []string{a1.QuoteSheet(renameTo) + "!A1:B4"},
	}
	status, resp := call(ctx, http.MethodPost,
		sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate", body)
	line("    unmasked, includeSpreadsheetInResponse -> HTTP %d  %d bytes", status, len(resp))
	reportSpreadsheetReply("      unmasked", resp)

	// The same call again, under the card's own field mask. If the mask
	// lands, the reply is a card and the cleanup is a rename away.
	renameBack := map[string]any{
		"requests": []any{map[string]any{"updateSheetProperties": map[string]any{
			"properties": map[string]any{"sheetId": sheetID, "title": sheet},
			"fields":     "title",
		}}},
		"includeSpreadsheetInResponse": true,
	}
	masked := "spreadsheetId,replies,updatedSpreadsheet(" + gapi.CardFields + ")"
	status, resp = call(ctx, http.MethodPost,
		sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate?fields="+url.QueryEscape(masked), renameBack)
	line("    under the card's own field mask        -> HTTP %d  %d bytes", status, len(resp))
	reportSpreadsheetReply("      masked  ", resp)

	line("")
	line("  does the card mask take conditionalFormats, and what does it cost?")
	// If the card can carry the rules, a rule update needs no request of
	// its own to count them — and the cost is paid by every
	// get_spreadsheet, so the bytes matter as much as the acceptance.
	status, _ = batchOne(ctx, map[string]any{"addConditionalFormatRule": map[string]any{
		"index": 0,
		"rule": map[string]any{
			"ranges": []any{a1.Rect{FirstCol: 2, FirstRow: 2, LastCol: 2, LastRow: 4}.GridRange(sheetID)},
			"booleanRule": map[string]any{
				"condition": map[string]any{
					"type":   "NUMBER_GREATER",
					"values": []any{map[string]any{"userEnteredValue": "100"}},
				},
				"format": map[string]any{"textFormat": map[string]any{"bold": true}},
			},
		},
	}})
	line("    a rule was added                       -> HTTP %d", status)
	for _, mask := range []string{
		gapi.CardFields,
		gapi.CardFields + ",sheets.conditionalFormats",
		gapi.CardFields + ",sheets.conditionalFormats(ranges)",
	} {
		status, body := call(ctx, http.MethodGet,
			sheetsBase+"/spreadsheets/"+scratchID+"?fields="+url.QueryEscape(mask), nil)
		label := "the card as it is"
		switch {
		case mask != gapi.CardFields && len(mask) > len(gapi.CardFields)+30:
			label = "the card plus the rules' ranges"
		case mask != gapi.CardFields:
			label = "the card plus whole rules"
		}
		line("    %-38s -> HTTP %d  %d bytes  %s", label, status, len(body), ruleNote(body))
	}

	line("")
	line("  does a grid read carry the anchors on the rows it reads?")
	// delete_dimensions pays a developerMetadata.search to name the
	// anchors it would destroy. The rows are already being counted by a
	// grid read in the same call, and rowMetadata would carry the
	// anchors for no extra request — if the mask is accepted, and if the
	// per-row objects do not cost more than the request they save.
	const key = "spike-deferred-anchor"
	status, resp = batchOne(ctx, map[string]any{"createDeveloperMetadata": map[string]any{
		"developerMetadata": map[string]any{
			"metadataKey": key, "metadataValue": "row3", "visibility": "DOCUMENT",
			"location": rowLocation(sheetID, 3),
		},
	}})
	line("    an anchor was created on row 3         -> HTTP %d", status)

	// The mask is built off the server's own, so what is measured is the
	// real read plus one field rather than a mask written to succeed.
	// GridFields ends by closing values, rowData, data and sheets;
	// rowMetadata is a field of data, so it goes inside the third.
	// GridFields ends by closing values, rowData, data and sheets, in
	// that order. rowMetadata is a field of data, so it goes after
	// rowData's closer and before data's: two closers, the new field,
	// then the two that were removed.
	//
	// A count wrong by one is a 400, and under a heading asking whether
	// the mask is supported, a malformed mask reads as "no" — so the mask
	// is printed, and its parentheses are balanced here before it is
	// sent.
	withAnchors := strings.TrimSuffix(gapi.GridFields, "))))") + ")),rowMetadata(developerMetadata)))"
	line("    the wider mask asked for is %s", withAnchors)
	if open, closed := strings.Count(withAnchors, "("), strings.Count(withAnchors, ")"); open != closed {
		line("    that mask is malformed: %d '(' against %d ')'. Fix the spike, not the verdict.", open, closed)
		return
	}
	for _, mask := range []string{gapi.GridFields, withAnchors} {
		v := url.Values{}
		v.Set("includeGridData", "true")
		v.Add("ranges", q+"!A1:B4")
		v.Set("fields", mask)
		status, body := call(ctx, http.MethodGet,
			sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
		label := "the server's grid mask"
		if mask != gapi.GridFields {
			label = "the same plus rowMetadata"
		}
		line("    %-38s -> HTTP %d  %d bytes", label, status, len(body))
		if status == 200 && mask != gapi.GridFields {
			reportRowMetadata(body)
		}
	}

	// The request this would replace, for the comparison that decides it.
	status, found, _ := searchRaw(ctx, map[string]any{
		"metadataLocation":         map[string]any{"sheetId": sheetID},
		"locationMatchingStrategy": "INTERSECTING_LOCATION",
	})
	line("    the search it would replace            -> HTTP %d  %d anchor(s) on the sheet", status, len(found))
}

// reportSpreadsheetReply says whether a batchUpdate came back carrying a
// spreadsheet, and whether that spreadsheet carried the URL the card's
// mask exists to drop.
func reportSpreadsheetReply(label, body string) {
	var out struct {
		UpdatedSpreadsheet *struct {
			SpreadsheetID  string            `json:"spreadsheetId"`
			SpreadsheetURL string            `json:"spreadsheetUrl"`
			Sheets         []json.RawMessage `json:"sheets"`
			Properties     json.RawMessage   `json:"properties"`
		} `json:"updatedSpreadsheet"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("%s: unreadable: %v", label, err)
		return
	}
	if out.UpdatedSpreadsheet == nil {
		line("%s: no updatedSpreadsheet in the reply", label)
		return
	}
	link := "absent"
	if out.UpdatedSpreadsheet.SpreadsheetURL != "" {
		link = "PRESENT, and the card's mask exists to drop it"
	}
	line("%s: %d sheet(s), properties %s, spreadsheetUrl %s",
		label, len(out.UpdatedSpreadsheet.Sheets),
		present(out.UpdatedSpreadsheet.Properties), link)
}

func present(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "absent"
	}
	return "present"
}

// ruleNote counts the conditional format rules a card reply carries, so
// an accepted mask is told apart from one that was accepted and returned
// nothing.
func ruleNote(body string) string {
	var out struct {
		Sheets []struct {
			ConditionalFormats []json.RawMessage `json:"conditionalFormats"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "(unreadable)"
	}
	n := 0
	for _, sh := range out.Sheets {
		n += len(sh.ConditionalFormats)
	}
	if n == 0 {
		return "no rules in the reply"
	}
	return "carrying " + strconv.Itoa(n) + " rule(s)"
}

// reportRowMetadata says how many row objects came back for a four-row
// window and how many of them carried anything, which is what folding
// the anchors into a grid read costs: the API emits one object per row
// whether or not the row has an anchor.
func reportRowMetadata(body string) {
	var out struct {
		Sheets []struct {
			Data []struct {
				RowMetadata []*struct {
					DeveloperMetadata []struct {
						MetadataKey string `json:"metadataKey"`
					} `json:"developerMetadata"`
				} `json:"rowMetadata"`
			} `json:"data"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("      rowMetadata unreadable: %v", err)
		return
	}
	rows, carrying := 0, 0
	var keys []string
	for _, sh := range out.Sheets {
		for _, d := range sh.Data {
			rows += len(d.RowMetadata)
			for _, r := range d.RowMetadata {
				if r == nil || len(r.DeveloperMetadata) == 0 {
					continue
				}
				carrying++
				for _, m := range r.DeveloperMetadata {
					keys = append(keys, m.MetadataKey)
				}
			}
		}
	}
	line("      %d rowMetadata object(s) for a 4-row window, %d carrying an anchor: %v", rows, carrying, keys)
}
