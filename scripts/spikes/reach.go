//go:build live

package main

// Who a write that names an address reaches. A comment's assignee, an
// address mentioned in a comment's text and a protected range's editor
// each name a person, and no reference page says whether Google shares
// the spreadsheet with that person when it does not already reach them.
// The file's Drive permissions, read before and after each write, say.
//
// The outside address is reserved by RFC 2606 and has no mailbox, so
// whatever Google sends it reaches nobody; whether Google sends mail at
// all is not something this probe can see. The transcript prints the
// signed-in address as <self> and the reserved one as <outside>.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v5/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v5/internal/gsheets"
)

// outside is an address no account can hold.
const outside = "probe@example.com"

func askOutsideReach(ctx context.Context) {
	self := selfAddress(ctx)
	if self == "" {
		line("  the signed-in address could not be read; skipping")
		return
	}
	registry.Add(self, "<self>")
	registry.Add(outside, "<outside>")
	const sheet = "SpikeReach"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	perms := func(when string) { line("    permissions %s: %s", when, permissionSummary(ctx)) }

	line("  S1: a comment assigned to an outside address")
	perms("before anything")
	insert(ctx, "insert on A1 assigned to "+outside, sheetID, 0, 0, "Please check this figure.", outside)
	perms("after the assignment")

	line("  S2: a comment whose text mentions an outside address")
	insert(ctx, "insert on A2 mentioning +"+outside, sheetID, 1, 0, "Looping in +"+outside+" on this.", "")
	perms("after the mention")

	protect := func(what string, row int, users []string) {
		status, body := batchOne(ctx, map[string]any{"addProtectedRange": map[string]any{
			"protectedRange": map[string]any{
				"range":   a1.Rect{FirstCol: 2, FirstRow: row, LastCol: 2, LastRow: row}.GridRange(sheetID),
				"editors": map[string]any{"users": users},
			},
		}})
		line("    %-48s -> HTTP %d  %s", what, status, first120(body))
		if status == 200 {
			line("    editors read back: %s", protectedEditors(ctx, sheet, sheetID))
		}
	}
	line("  S3: a protected range whose editors name the signed-in account")
	protect("protect B1, editors [self]", 1, []string{self})
	perms("after the protection naming self")

	line("  S4: a protected range whose editors name an outside address")
	protect("protect B2, editors [self, outside]", 2, []string{self, outside})
	perms("after the protection naming the outside address")
}

// permissionSummary lists the file's Drive permissions as type, role
// and address, sorted so two reads can be compared.
func permissionSummary(ctx context.Context) string {
	status, body := call(ctx, http.MethodGet, driveBase+"/files/"+scratchID+
		"/permissions?fields=permissions(type,role,emailAddress)", nil)
	if status != 200 {
		return fmt.Sprintf("HTTP %d %s", status, first120(body))
	}
	var out struct {
		Permissions []struct {
			Type, Role, EmailAddress string
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "unreadable reply"
	}
	var rows []string
	for _, p := range out.Permissions {
		rows = append(rows, p.Type+"/"+p.Role+"/"+p.EmailAddress)
	}
	sort.Strings(rows)
	return strings.Join(rows, ", ")
}

// protectedEditors is the user editors of every protected range on one
// sheet.
func protectedEditors(ctx context.Context, sheet string, sheetID int) string {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+
		"?ranges="+url.QueryEscape(a1.QuoteSheet(sheet))+
		"&fields="+url.QueryEscape("sheets(properties(sheetId),protectedRanges(editors))"), nil)
	if status != 200 {
		return fmt.Sprintf("HTTP %d %s", status, first120(body))
	}
	var out gsheets.Spreadsheet
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "unreadable reply"
	}
	var parts []string
	for _, sh := range out.Sheets {
		if sh.Properties == nil || sh.Properties.SheetID != sheetID {
			continue
		}
		for _, p := range sh.ProtectedRanges {
			var users []string
			if p.Editors != nil {
				users = p.Editors.Users
			}
			parts = append(parts, "["+strings.Join(users, " ")+"]")
		}
	}
	return strings.Join(parts, " ")
}

// askChipWriteBack sends a smart chip's displayed text back through
// values.update, as a model does after a values read, and reads what the
// cell keeps. Each input mode starts from freshly seeded chips, since
// the mode before it may have dropped them. The chip is a rich link to
// the scratch spreadsheet itself, so the probe names nobody.
func askChipWriteBack(ctx context.Context) {
	const sheet = "SpikeChips"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	cells := a1.QuoteSheet(sheet) + "!A1:A2"
	chip := map[string]any{"richLinkProperties": map[string]any{
		"uri": "https://docs.google.com/spreadsheets/d/" + scratchID + "/edit",
	}}
	seed := func() bool {
		status, body := batchOne(ctx, map[string]any{"updateCells": map[string]any{
			"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 2}.GridRange(sheetID),
			"rows": []any{
				map[string]any{"values": []any{map[string]any{
					"userEnteredValue": map[string]any{"stringValue": "@"},
					"chipRuns":         []any{map[string]any{"chip": chip}},
				}}},
				// Every run needs a chip (Google: "Chip run must have a
				// chip."), so the text around one carries no run.
				map[string]any{"values": []any{map[string]any{
					"userEnteredValue": map[string]any{"stringValue": "See @ here"},
					"chipRuns":         []any{map[string]any{"startIndex": 4, "chip": chip}},
				}}},
			},
			"fields": "userEnteredValue,chipRuns",
		}})
		line("  %-50s -> HTTP %d  %s", "a rich link chip into A1, and one inside text in A2", status, first120(body))
		return status == 200
	}
	for _, mode := range []string{"RAW", "USER_ENTERED"} {
		if !seed() {
			return
		}
		chipsWhole(ctx, "A1:A2 as stored", cells)
		status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"/values/"+url.PathEscape(cells), nil)
		line("  %-50s -> HTTP %d  %s", "what a values read shows", status, whole(body))
		var read struct {
			Values [][]any `json:"values"`
		}
		if status != 200 || json.Unmarshal([]byte(body), &read) != nil || len(read.Values) < 2 {
			return
		}
		status, body = putMode(ctx, cells, read.Values, mode)
		line("  %-50s -> HTTP %d  %s", "send the read back, "+mode, status, first120(body))
		chipsWhole(ctx, "A1:A2 after it: chips kept?", cells)
	}
}
