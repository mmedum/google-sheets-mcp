//go:build live

package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// spikeS answers what the reference leaves open about a color scale, a
// conditional format rule with a gradientRule.
//
// The discovery document gives the shape — a minpoint, an optional
// midpoint and a maxpoint, each a type, a value and a color — and none
// of the refusals. manage_range is built on four beliefs, and the fake
// refuses what they say Google refuses. Each is asked here.
//
// Q1. What a scale sent with colorStyle alone reads back as: colorStyle,
// the deprecated color, or both.
// Q2. Which midpoint types are taken. The Sheets interface offers number,
// percent and percentile; manage_range sends no min or max there.
// Q3. What the fake refuses: a number point with no value, a scale with
// no maxpoint, a point with no type, and a rule carrying both kinds. And
// whether a value on min is kept, since the reference calls it unused.
// Q4. A number value under a comma-decimal locale: is "1.5" taken, is
// "1,5", and what reads back. What a locale does to the colors is not
// in any reply; the transcript names the sheet for a person to look at.
// Q5. A percent or percentile value outside 0 to 100. The reference
// states no bound and manage_range refuses none; is one refused, and
// what reads back.
func spikeS(ctx context.Context) {
	sec("Spike S: color scales, and what Google refuses in one")
	const sheet = "SpikeScales"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	if err := put(ctx, a1.QuoteSheet(sheet)+"!A1:B5", [][]any{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	// Column A takes every rule but one; column B takes the comma value
	// alone, so the two spellings in Q4 can be compared side by side.
	columnA := a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 5}.GridRange(sheetID)
	columnB := a1.Rect{FirstCol: 2, FirstRow: 1, LastCol: 2, LastRow: 5}.GridRange(sheetID)
	over := columnA
	white := map[string]any{"rgbColor": map[string]any{"red": 1, "green": 1, "blue": 1}}
	green := map[string]any{"rgbColor": map[string]any{"red": 0.34, "green": 0.73, "blue": 0.54}}
	point := func(kind, value string) map[string]any {
		p := map[string]any{"colorStyle": white}
		if kind != "" {
			p["type"] = kind
		}
		if value != "" {
			p["value"] = value
		}
		return p
	}
	maxpoint := map[string]any{"type": "MAX", "colorStyle": green}
	// add sends one rule at index 0, so the rule just added is always the
	// first one a read finds.
	add := func(what string, rule map[string]any) int {
		rule["ranges"] = []any{over}
		status, body := batchOne(ctx, map[string]any{"addConditionalFormatRule": map[string]any{
			"index": 0, "rule": rule,
		}})
		line("    %-48s -> HTTP %d  %s", what, status, whole(body))
		return status
	}
	scale := func(min, mid, max map[string]any) map[string]any {
		g := map[string]any{}
		if min != nil {
			g["minpoint"] = min
		}
		if mid != nil {
			g["midpoint"] = mid
		}
		if max != nil {
			g["maxpoint"] = max
		}
		return map[string]any{"gradientRule": g}
	}

	line("")
	line("  Q1: a scale sent with colorStyle alone; which color fields read back?")
	if add("min and max, colorStyle only", scale(point("MIN", ""), nil, maxpoint)) == 200 {
		firstScale(ctx, sheetID, "minpoint")
	}

	line("")
	line("  Q2: which midpoint types are taken?")
	for _, mid := range []struct{ kind, value string }{
		{"NUMBER", "3"}, {"PERCENT", "50"}, {"PERCENTILE", "50"}, {"MIN", ""}, {"MAX", ""},
	} {
		if add("midpoint "+mid.kind, scale(point("MIN", ""), point(mid.kind, mid.value), maxpoint)) == 200 {
			firstScale(ctx, sheetID, "midpoint")
		}
	}

	line("")
	line("  Q3: what the fake refuses, asked of Google")
	add("NUMBER minpoint with no value", scale(point("NUMBER", ""), nil, maxpoint))
	add("PERCENTILE midpoint with no value", scale(point("MIN", ""), point("PERCENTILE", ""), maxpoint))
	add("no maxpoint", scale(point("MIN", ""), nil, nil))
	add("minpoint with no type", scale(point("", "1"), nil, maxpoint))
	both := scale(point("MIN", ""), nil, maxpoint)
	both["booleanRule"] = map[string]any{
		"condition": map[string]any{"type": "NOT_BLANK"},
		"format":    map[string]any{"textFormat": map[string]any{"bold": true}},
	}
	add("booleanRule and gradientRule together", both)
	if add("MIN minpoint with a value, which is unused", scale(point("MIN", "2"), nil, maxpoint)) == 200 {
		firstScale(ctx, sheetID, "minpoint")
	}

	line("")
	line("  Q5: a percent or percentile value outside 0 to 100")
	for _, mid := range []struct{ kind, value string }{
		{"PERCENT", "150"}, {"PERCENTILE", "150"}, {"PERCENTILE", "-10"},
	} {
		if add("midpoint "+mid.kind+" "+mid.value, scale(point("MIN", ""), point(mid.kind, mid.value), maxpoint)) == 200 {
			firstScale(ctx, sheetID, "midpoint")
		}
	}

	line("")
	line("  Q4: a number value under a comma-decimal locale")
	locale := spreadsheetLocale(ctx)
	line("    the scratch spreadsheet's locale is %q", locale)
	status, body := setLocale(ctx, "de_DE")
	line("    %-48s -> HTTP %d  %s", "set the locale to de_DE", status, first120(body))
	if status != 200 {
		return
	}
	for _, c := range []struct {
		value  string
		column *gsheets.GridRange
	}{{"1.5", columnA}, {"1,5", columnB}} {
		over = c.column
		if add("NUMBER minpoint "+c.value+" under de_DE", scale(point("NUMBER", c.value), nil, maxpoint)) == 200 {
			firstScale(ctx, sheetID, "minpoint")
		}
	}
	line("    no reply says how de_DE read a value it took. The run of 2026-10-09 refused 1.5 and took")
	line("    1,5. Open %q: column B has the scale from 1,5 over 1 to 5; if its color first changes", sheet)
	line("    between 1 and 2, de_DE read it as one and a half.")
	if locale != "" {
		status, body = setLocale(ctx, locale)
		line("    %-48s -> HTTP %d  %s", "set the locale back", status, first120(body))
	}
}

// firstScale prints one point of the first conditional rule on a sheet,
// as Google stored it.
func firstScale(ctx context.Context, sheetID int, which string) {
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?fields=sheets(properties(sheetId),conditionalFormats(gradientRule))", nil)
	if status != 200 {
		line("      read back -> HTTP %d  %s", status, first120(body))
		return
	}
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int `json:"sheetId"`
			} `json:"properties"`
			ConditionalFormats []struct {
				GradientRule map[string]json.RawMessage `json:"gradientRule"`
			} `json:"conditionalFormats"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		line("      read back: unreadable reply")
		return
	}
	for _, sh := range out.Sheets {
		if sh.Properties.SheetID != sheetID || len(sh.ConditionalFormats) == 0 {
			continue
		}
		line("      %s read back as %s", which, whole(string(sh.ConditionalFormats[0].GradientRule[which])))
		return
	}
	line("      read back: no rule on the sheet")
}

// spreadsheetLocale reads the scratch spreadsheet's locale.
func spreadsheetLocale(ctx context.Context) string {
	status, body := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"?fields=properties(locale)", nil)
	if status != 200 {
		return ""
	}
	var out struct {
		Properties struct {
			Locale string `json:"locale"`
		} `json:"properties"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.Properties.Locale
}

// setLocale changes the scratch spreadsheet's locale.
func setLocale(ctx context.Context, locale string) (int, string) {
	return batchOne(ctx, map[string]any{"updateSpreadsheetProperties": map[string]any{
		"properties": map[string]any{"locale": locale}, "fields": "locale",
	}})
}
