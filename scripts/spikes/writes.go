//go:build live

package main

// The three probes phase 1 depends on: what USER_ENTERED does to input
// (A), where an append lands and what it reports (B), and whether
// Drive's file version is a cheaper staleness check than re-reading (F).
//
// Every value written here is invented, and every line goes through the
// redacting printer.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

const driveBase = "https://www.googleapis.com/drive/v3"

// Value input options, as the API spells them.
const (
	userEntered = "USER_ENTERED"
	raw         = "RAW"
)

// addSheet adds a sheet and returns its id.
func addSheet(ctx context.Context, title string) (int, error) {
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{
			map[string]any{"addSheet": map[string]any{"properties": map[string]any{"title": title}}},
		}})
	if status != 200 {
		return 0, fmt.Errorf("addSheet %q: HTTP %d: %s", title, status, first120(body))
	}
	var added struct {
		Replies []struct {
			AddSheet struct {
				Properties struct {
					SheetID int `json:"sheetId"`
				} `json:"properties"`
			} `json:"addSheet"`
		} `json:"replies"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil || len(added.Replies) == 0 {
		return 0, fmt.Errorf("addSheet %q: unreadable reply", title)
	}
	return added.Replies[0].AddSheet.Properties.SheetID, nil
}

// putMode writes one range under a stated valueInputOption and asks for
// the stored values back, which is the shape §4.4 says every write uses.
func putMode(ctx context.Context, rangeA1 string, values [][]any, mode string) (int, string) {
	u := sheetsBase + "/spreadsheets/" + scratchID + "/values/" + url.PathEscape(rangeA1) +
		"?valueInputOption=" + mode + "&includeValuesInResponse=true&responseValueRenderOption=FORMULA"
	return call(ctx, http.MethodPut, u, map[string]any{"values": values})
}

// cellsIn reads a rectangle with every field the write guard looks at.
func cellsIn(ctx context.Context, rangeA1 string) ([][]*gsheets.CellData, error) {
	v := url.Values{}
	v.Set("includeGridData", "true")
	v.Add("ranges", rangeA1)
	v.Set("fields", "sheets(data(rowData(values(userEnteredValue,effectiveValue,formattedValue,"+
		"userEnteredFormat(numberFormat)))))")
	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", status, first120(body))
	}
	var got gsheets.Spreadsheet
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return nil, err
	}
	var out [][]*gsheets.CellData
	for _, sh := range got.Sheets {
		for _, d := range sh.Data {
			for _, row := range d.RowData {
				if row == nil {
					out = append(out, nil)
					continue
				}
				out = append(out, row.Values)
			}
		}
	}
	return out, nil
}

// describe says what one cell holds, in the terms the guard and the
// coercion report use: the value that was stored, the value it computed
// to, and what a person sees.
func describe(c *gsheets.CellData) string {
	if c == nil {
		return "empty"
	}
	parts := []string{"stored " + extended(c.UserEnteredValue)}
	if eff := extended(c.EffectiveValue); eff != extended(c.UserEnteredValue) {
		parts = append(parts, "effective "+eff)
	}
	parts = append(parts, "displayed "+strconv.Quote(c.FormattedValue))
	if f := c.UserEnteredFormat; f != nil && f.NumberFormat != nil {
		nf := f.NumberFormat.Type
		if f.NumberFormat.Pattern != "" {
			nf += "(" + f.NumberFormat.Pattern + ")"
		}
		parts = append(parts, "format "+nf)
	}
	return strings.Join(parts, ", ")
}

// extended names which arm of the value union is set, which is the whole
// question spike A asks.
func extended(v *gsheets.ExtendedValue) string {
	switch {
	case v == nil:
		return "none"
	case v.FormulaValue != nil:
		return "formula " + strconv.Quote(*v.FormulaValue)
	case v.StringValue != nil:
		return "string " + strconv.Quote(clip(*v.StringValue))
	case v.NumberValue != nil:
		return "number " + strconv.FormatFloat(*v.NumberValue, 'f', -1, 64)
	case v.BoolValue != nil:
		return "bool " + strconv.FormatBool(*v.BoolValue)
	case v.ErrorValue != nil:
		return "error " + v.ErrorValue.Display()
	}
	return "none"
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "… (" + strconv.Itoa(len(s)) + " chars)"
	}
	return s
}

// coercionInputs are the strings §15.A names. Each is a shape a person
// types and Google rewrites, or does not.
var coercionInputs = []string{
	"1-2", "007", "=1+2", "$100.15", "TRUE", "'0123", "2026-09-05",
}

func spikeA(ctx context.Context) {
	sec("Spike A: the coercion matrix")
	const sheet = "SpikeCoercion"
	if _, err := addSheet(ctx, sheet); err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)

	// One column per input option, so the two are read back together and
	// a difference is a difference in the option and nothing else.
	rows := make([][]any, len(coercionInputs))
	for i, in := range coercionInputs {
		rows[i] = []any{in}
	}
	for col, mode := range map[string]string{"A": userEntered, "B": raw} {
		rangeA1 := fmt.Sprintf("%s!%s1:%s%d", q, col, col, len(rows))
		if status, body := putMode(ctx, rangeA1, rows, mode); status != 200 {
			line("  %s write failed: HTTP %d %s", mode, status, first120(body))
			return
		}
	}
	got, err := cellsIn(ctx, fmt.Sprintf("%s!A1:B%d", q, len(rows)))
	if err != nil {
		line("  read-back failed: %v", err)
		return
	}
	line("  %-14s %s", "typed", "-> what Sheets stored")
	for i, in := range coercionInputs {
		var ue, rw *gsheets.CellData
		if i < len(got) {
			if len(got[i]) > 0 {
				ue = got[i][0]
			}
			if len(got[i]) > 1 {
				rw = got[i][1]
			}
		}
		line("  %-14s USER_ENTERED: %s", strconv.Quote(in), describe(ue))
		line("  %-14s RAW:          %s", "", describe(rw))
	}

	// The size question §15.A ends on, asked separately: a 60 000
	// character cell is past the 50 000 the reference documents, and a
	// failure here must not take the rest of the run with it.
	long := strings.Repeat("Quorbin", 8572) // 60 004 characters
	line("")
	for _, mode := range []string{userEntered, raw} {
		status, body := putMode(ctx, q+"!D1", [][]any{{long}}, mode)
		line("  %-14s a %d-character string -> HTTP %d %s", mode, len(long), status, first120(body))
	}
}

// spikeShape asks what values.update does when the range and the array
// disagree about size. It decides write_values' contract: whether a
// caller may name an anchor cell, and what the guard's target rectangle
// actually is.
func spikeShape(ctx context.Context) {
	sec("Spike A, continued: the range against the array")
	const sheet = "SpikeShape"
	if _, err := addSheet(ctx, sheet); err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)

	// A block of markers, so a write that lands somewhere unexpected is
	// visible rather than inferred from a response.
	seed := make([][]any, 8)
	for i := range seed {
		seed[i] = []any{"before", "before", "before", "before"}
	}
	if err := put(ctx, q+"!A1:D8", seed); err != nil {
		line("  setup failed: %v", err)
		return
	}

	two := [][]any{{"Quorbin", 1}, {"Vandel", 2}, {"Skerry", 3}}
	for _, tc := range []struct {
		what, rangeA1 string
		values        [][]any
	}{
		{"a single anchor cell, a 3x2 array", "A1", two},
		{"a range smaller than the array", "A1:B2", two},
		{"a range larger than the array", "A1:D8", two},
		{"a ragged array", "A1:C3", [][]any{{"Grivet", 1, 2}, {"Oblisk"}, {"Trennow", 3}}},
		{"a whole column", "A:A", [][]any{{"Yalmic"}, {"Bractal"}}},
	} {
		status, body := putMode(ctx, q+"!"+tc.rangeA1, tc.values, raw)
		var res struct {
			UpdatedRange string `json:"updatedRange"`
			UpdatedCells int    `json:"updatedCells"`
		}
		_ = json.Unmarshal([]byte(body), &res)
		line("  %-34s range %-6s -> HTTP %d  updatedRange %s (%d cells)",
			tc.what, tc.rangeA1, status, strconv.Quote(res.UpdatedRange), res.UpdatedCells)
		if status != 200 {
			line("    %s", first120(body))
		}
	}
	line("")
	line("  what the sheet holds afterwards, so a write that landed elsewhere is visible:")
	layout(ctx, q+"!A1:D8")
}

// appendResult is the half of an append response that says where it
// landed.
type appendResult struct {
	TableRange string `json:"tableRange"`
	Updates    struct {
		UpdatedRange   string `json:"updatedRange"`
		UpdatedRows    int    `json:"updatedRows"`
		UpdatedColumns int    `json:"updatedColumns"`
		UpdatedCells   int    `json:"updatedCells"`
	} `json:"updates"`
}

func appendValues(ctx context.Context, rangeA1 string, values [][]any, insert string) (int, string) {
	u := sheetsBase + "/spreadsheets/" + scratchID + "/values/" + url.PathEscape(rangeA1) +
		":append?valueInputOption=RAW&insertDataOption=" + insert
	return call(ctx, http.MethodPost, u, map[string]any{"values": values})
}

// layout prints what a rectangle actually holds, one row per line, so a
// step that claims where an append landed is checked against the sheet
// rather than against the response that described it.
func layout(ctx context.Context, rangeA1 string) {
	rows, err := cellsIn(ctx, rangeA1)
	if err != nil {
		line("    read-back failed: %v", err)
		return
	}
	for i, row := range rows {
		var cells []string
		for _, c := range row {
			if c == nil || c.UserEnteredValue == nil {
				cells = append(cells, "-")
				continue
			}
			cells = append(cells, extended(c.UserEnteredValue))
		}
		if len(cells) == 0 {
			cells = []string{"(empty row)"}
		}
		line("    row %2d | %s", i+1, strings.Join(cells, " | "))
	}
}

func spikeB(ctx context.Context) {
	sec("Spike B: where an append lands")
	line("  each sheet starts as a block in rows 1-3, a gap in rows 4-5, a second block in rows 6-7")

	block1 := [][]any{{"Plimth", 1}, {"Nardle", 2}, {"Grivet", 3}}
	block2 := [][]any{{"Yalmic", 60}, {"Zephrin", 70}}
	added := [][]any{{"Bractal", 900}, {"Umberly", 901}}

	for _, tc := range []struct {
		sheet, insert, target, why string
	}{
		{"SpikeAppendInsert", "INSERT_ROWS", "A1", "the range names a cell inside the first block"},
		{"SpikeAppendOverwrite", "OVERWRITE", "A1", "the same, with the option that reuses empty rows"},
		{"SpikeAppendSecond", "OVERWRITE", "A6", "the range names the second block instead"},
		{"SpikeAppendWhole", "OVERWRITE", "", "the range is the whole sheet"},
	} {
		if _, err := addSheet(ctx, tc.sheet); err != nil {
			line("  setup failed: %v", err)
			continue
		}
		q := a1.QuoteSheet(tc.sheet)
		if err := put(ctx, q+"!A1:B3", block1); err != nil {
			line("  setup failed: %v", err)
			continue
		}
		if err := put(ctx, q+"!A6:B7", block2); err != nil {
			line("  setup failed: %v", err)
			continue
		}
		target := q
		if tc.target != "" {
			target = q + "!" + tc.target
		}
		status, body := appendValues(ctx, target, added, tc.insert)
		line("")
		line("  %s, range %s — %s", tc.insert, tc.target+" (as sent: "+redactedTarget(tc.target)+")", tc.why)
		if status != 200 {
			line("    HTTP %d %s", status, first120(body))
			continue
		}
		var res appendResult
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			line("    unreadable reply: %v", err)
			continue
		}
		line("    tableRange   %s", strconv.Quote(res.TableRange))
		line("    updatedRange %s (%d rows, %d cells)",
			strconv.Quote(res.Updates.UpdatedRange), res.Updates.UpdatedRows, res.Updates.UpdatedCells)
		layout(ctx, q+"!A1:B12")
	}
}

// redactedTarget says what was sent without repeating the sheet title,
// which the line already carries once.
func redactedTarget(t string) string {
	if t == "" {
		return "the whole sheet"
	}
	return "sheet!" + t
}

// driveStamp reads the file's version and modification time.
func driveStamp(ctx context.Context) stamp {
	v, m := driveVersion(ctx)
	return stamp{version: v, modified: m}
}

// driveVersion reads the file's version and modification time.
func driveVersion(ctx context.Context) (version, modified string) {
	status, body := call(ctx, http.MethodGet,
		driveBase+"/files/"+url.PathEscape(scratchID)+"?fields=version,modifiedTime", nil)
	if status != 200 {
		return "", "HTTP " + strconv.Itoa(status) + " " + first120(body)
	}
	var f struct {
		Version      string `json:"version"`
		ModifiedTime string `json:"modifiedTime"`
	}
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		return "", err.Error()
	}
	return f.Version, f.ModifiedTime
}

// stamp is the pair a cheap staleness check could be built on.
type stamp struct {
	version  string
	modified string
}

func (s stamp) String() string {
	return "version " + strconv.Quote(s.version) + ", modified " + s.modified
}

// waitForChange polls until either field moves off from, and says how
// long it took and which one moved. Polled rather than read once: Drive
// is eventually consistent, and one read cannot tell a lagging field
// from one that never changes.
//
// Both fields, because they fail differently. A version that never moves
// is useless; a modifiedTime that moves but lags is worse than useless,
// since it would report "unchanged" during exactly the window a
// checkpoint exists to cover.
func waitForChange(ctx context.Context, from stamp) (stamp, time.Duration, string) {
	start := time.Now()
	for range 12 {
		now := driveStamp(ctx)
		switch {
		case now.version != from.version && now.version != "":
			return now, time.Since(start), "version"
		case now.modified != from.modified && now.modified != "":
			return now, time.Since(start), "modifiedTime"
		}
		select {
		case <-ctx.Done():
			return now, time.Since(start), ""
		case <-time.After(time.Second):
		}
	}
	return driveStamp(ctx), time.Since(start), ""
}

func spikeF(ctx context.Context) {
	sec("Spike F: is a Drive field a cheaper staleness check")
	const sheet = "SpikeVersion"
	if _, err := addSheet(ctx, sheet); err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:A1", [][]any{{"Skerry"}}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	// Let the create-and-fill settle, so the first reading is a baseline
	// rather than the tail of the setup.
	time.Sleep(5 * time.Second)

	at := driveStamp(ctx)
	line("  before any edit: %s", at)

	report := func(what string, edit func() error) bool {
		if err := edit(); err != nil {
			line("  %s failed: %v", what, err)
			return false
		}
		next, took, moved := waitForChange(ctx, at)
		if moved == "" {
			line("  after %-28s neither field moved in %s; still %s", what+":", took.Round(time.Second), next)
		} else {
			line("  after %-28s %s moved after %s; now %s", what+":", moved, took.Round(time.Millisecond), next)
		}
		at = next
		return true
	}

	if !report("a value change", func() error { return put(ctx, q+"!A1:A1", [][]any{{"Vandel"}}) }) {
		return
	}
	if !report("writing the same value again", func() error { return put(ctx, q+"!A1:A1", [][]any{{"Vandel"}}) }) {
		return
	}
	report("a structural batchUpdate", func() error {
		status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
			map[string]any{"requests": []any{map[string]any{"updateSheetProperties": map[string]any{
				"properties": map[string]any{"title": sheet, "hidden": false},
				"fields":     "hidden",
			}}}})
		if status != 200 {
			return fmt.Errorf("HTTP %d %s", status, first120(body))
		}
		return nil
	})

	line("")
	line("  A field that moves on every edit is a spreadsheet-wide staleness check for one")
	line("  Drive call. One that lags, or that misses an edit, is worse than the re-read it")
	line("  would replace, because it would say nothing changed when something did.")
}

// spikeH asks what spreadsheets.create does with a sheets list: whether
// the sheets given are added beside the one Google always makes, or are
// the whole set.
//
// It decides create_spreadsheet's contract. A description that promised
// Google's own first sheet beside the caller's would send a model
// looking for a tab that is not there, and the seed values would land on
// a different sheet from the one it expected.
func spikeH(ctx context.Context) {
	sec("Spike H: spreadsheets.create with a sheets list")

	report := func(what string, body map[string]any) {
		status, out := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets", body)
		if status != 200 {
			line("  %-40s -> HTTP %d %s", what, status, first120(out))
			return
		}
		var created struct {
			SpreadsheetID string `json:"spreadsheetId"`
			Sheets        []struct {
				Properties struct {
					SheetID int    `json:"sheetId"`
					Title   string `json:"title"`
					Index   int    `json:"index"`
				} `json:"properties"`
			} `json:"sheets"`
		}
		if err := json.Unmarshal([]byte(out), &created); err != nil {
			line("  %-40s -> unreadable reply", what)
			return
		}
		reg := make([]string, 0, len(created.Sheets))
		for _, sh := range created.Sheets {
			reg = append(reg, fmt.Sprintf("%q (id %d, index %d)", sh.Properties.Title, sh.Properties.SheetID, sh.Properties.Index))
		}
		line("  %-40s -> %d sheet(s): %s", what, len(created.Sheets), strings.Join(reg, ", "))
		line("      NOTE: left behind; remove %q by hand.", body["properties"].(map[string]any)["title"])
	}

	stamp := time.Now().UTC().Format("15:04:05")
	report("no sheets given", map[string]any{
		"properties": map[string]any{"title": "spike create default " + stamp},
	})
	report("one sheet given, no sheetId", map[string]any{
		"properties": map[string]any{"title": "spike create listed " + stamp},
		"sheets": []any{
			map[string]any{"properties": map[string]any{"title": "Quorbin"}},
			map[string]any{"properties": map[string]any{"title": "Vandel"}},
		},
	})
}

// spikeI asks two questions the write path already assumes an answer to.
//
// Does values.update keep a cell's note and validation rule, the way
// values.clear documents that it does? The guard tells a caller "the
// write removed notes on A1", and if the note survives that sentence is
// false. And where does moveDimension's destinationIndex land a band —
// against the sheet before the move, as it does for a sheet's index, or
// after?
func spikeI(ctx context.Context) {
	sec("Spike I: what a values write keeps, and where a move lands")
	const sheet = "SpikeKeeps"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:B4", [][]any{
		{"Quorbin", "one"}, {"Vandel", "two"}, {"Skerry", "three"}, {"Yalmic", "four"},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}
	// A note and a validation rule on A1, both invisible in a values read.
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{
			map[string]any{"repeatCell": map[string]any{
				"range":  a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}.GridRange(sheetID),
				"cell":   map[string]any{"note": "Bractal reconciliation pending"},
				"fields": "note",
			}},
			map[string]any{"setDataValidation": map[string]any{
				"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 1, LastRow: 1}.GridRange(sheetID),
				"rule": map[string]any{
					"condition": map[string]any{"type": "ONE_OF_LIST", "values": []any{
						map[string]any{"userEnteredValue": "Quorbin"},
						map[string]any{"userEnteredValue": "Umberly"},
					}},
					"strict": true,
				},
			}},
		}})
	if status != 200 {
		line("  setup failed: HTTP %d %s", status, first120(body))
		return
	}
	annotated := func(what string) {
		v := url.Values{}
		v.Set("includeGridData", "true")
		v.Add("ranges", q+"!A1")
		v.Set("fields", "sheets(data(rowData(values(userEnteredValue,note,dataValidation))))")
		st, out := call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
		if st != 200 {
			line("  %-28s read failed: HTTP %d", what, st)
			return
		}
		var got gsheets.Spreadsheet
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			line("  %-28s unreadable", what)
			return
		}
		var c *gsheets.CellData
		for _, sh := range got.Sheets {
			for _, d := range sh.Data {
				for _, row := range d.RowData {
					if row != nil && len(row.Values) > 0 {
						c = row.Values[0]
					}
				}
			}
		}
		if c == nil {
			line("  %-28s A1 came back empty", what)
			return
		}
		line("  %-28s value %s, note %t, validation %t", what, extended(c.UserEnteredValue), c.Note != "", c.DataValidation != nil)
	}
	annotated("before any write:")
	if err := put(ctx, q+"!A1", [][]any{{"Zephrin"}}); err != nil {
		line("  write failed: %v", err)
		return
	}
	annotated("after values.update on A1:")

	// Where a moved band lands. Rows 1-2 hold Quorbin and Vandel; moving
	// them "to index 3" is the question.
	line("")
	line("  rows before the move: %s", rowLabels(ctx, q))
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{map[string]any{"moveDimension": map[string]any{
			"source": map[string]any{
				"sheetId": sheetID, "dimension": "ROWS", "startIndex": 0, "endIndex": 2,
			},
			"destinationIndex": 3,
		}}}})
	if status != 200 {
		line("  move failed: HTTP %d %s", status, first120(body))
		return
	}
	line("  rows after moving 1-2 with destinationIndex 3: %s", rowLabels(ctx, q))
	line("  (if the two moved rows now start at row 2, the index is read against the sheet before the move)")
}

// rowLabels reads column A and prints it as one line.
func rowLabels(ctx context.Context, q string) string {
	status, body := getValues(ctx, q+"!A1:A4")
	if status != 200 {
		return "read failed"
	}
	var vr gsheets.ValueRange
	if err := json.Unmarshal([]byte(body), &vr); err != nil {
		return "unreadable"
	}
	var out []string
	for _, row := range vr.Values {
		if len(row) == 0 {
			out = append(out, "(empty)")
			continue
		}
		out = append(out, fmt.Sprint(row[0]))
	}
	return strings.Join(out, ", ")
}
