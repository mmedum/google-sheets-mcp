//go:build live

// Command spikes answers the questions §15 says the reference does not.
//
// Each probe prints what the API actually did, so the verdict written
// into the evidence log is a transcript rather than a recollection. It
// creates its own scratch spreadsheet and fills it, so nothing it prints
// is anybody's data.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/auth"
	"github.com/mmedum/google-sheets-mcp/internal/credentials"
	"github.com/mmedum/google-sheets-mcp/internal/redact"
	"github.com/mmedum/google-sheets-mcp/internal/userconfig"
)

// line is the only way this program reaches the terminal, and it
// redacts. The first two runs of it were redacted by hand with sed
// afterwards, which is the same thing done worse: the spreadsheet id
// came back inside a values.update response and would have travelled
// into any paste of the raw output.
func line(format string, args ...any) {
	fmt.Println(redact.Line(fmt.Sprintf(format, args...)))
}

// sec prints a static section header.
func sec(title string) {
	// Through line, not around it. sec used to print directly, which
	// made the allowlist a list of functions permitted to reach the
	// terminal rather than a claim that everything reaching it is
	// redacted — and a later sec(someSheetTitle) would have been blessed
	// by name. One printer is the whole invariant.
	line("")
	line("== %s %s", title, strings.Repeat("=", max(0, 54-len(title))))
}

const sheetsBase = "https://sheets.googleapis.com/v4"

var client *http.Client
var scratchID string

// only names the spikes to run, so re-probing a single question does not
// mean re-running every other one. Each run creates a scratch
// spreadsheet it cannot trash, so a narrower run is a smaller mess — and
// a list rather than one letter, because a phase asking four questions
// otherwise leaves four spreadsheets behind.
var only = flag.String("only", "",
	"run these spikes, comma-separated (A, B, C, E, F, G, H, I, J, K, L, M, N, P); default runs all")

func main() {
	flag.Parse()
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "\nspikes: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ts, err := tokenSource(ctx)
	if err != nil {
		return err
	}
	client = oauth2.NewClient(ctx, ts)

	title := "spike scratch " + time.Now().UTC().Format("2006-01-02 15:04:05")
	first, second, err := setup(ctx, title)
	if err != nil {
		return err
	}
	line("scratch spreadsheet created; first sheet %q, second sheet %q", first, second)
	line("NOTE: this run leaves the spreadsheet behind — drive.readonly cannot trash it.")
	line("      Remove %q by hand.", title)

	// A slice rather than a map: a transcript that changes order between
	// runs cannot be diffed against the last one.
	for _, p := range []struct {
		letter string
		probe  func()
	}{
		{"C", func() { spikeC(ctx, first, second) }},
		{"E", func() { spikeE(ctx, first) }},
		{"A", func() { spikeA(ctx); spikeShape(ctx) }},
		{"B", func() { spikeB(ctx) }},
		{"F", func() { spikeF(ctx) }},
		{"H", func() { spikeH(ctx) }},
		{"I", func() { spikeI(ctx) }},
		{"G", func() { spikeG(ctx) }},
		{"J", func() { spikeJ(ctx) }},
		{"K", func() { spikeK(ctx) }},
		{"L", func() { spikeL(ctx) }},
		{"M", func() { spikeM(ctx) }},
		{"N", func() { spikeN(ctx) }},
		{"P", func() { spikeP(ctx) }},
		{"Q", func() { spikeQ(ctx) }},
	} {
		if wanted(p.letter) {
			p.probe()
		}
	}
	return nil
}

// wanted reads the -only list. An empty list is every spike.
func wanted(letter string) bool {
	if strings.TrimSpace(*only) == "" {
		return true
	}
	for _, want := range strings.Split(*only, ",") {
		if strings.EqualFold(strings.TrimSpace(want), letter) {
			return true
		}
	}
	return false
}

func tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	profile := userconfig.DefaultProfile
	secret, err := userconfig.ResolveClientSecretPath(profile)
	if err != nil {
		return nil, err
	}
	cfg, err := auth.LoadClientSecret(secret, auth.Scopes(false, false))
	if err != nil {
		return nil, err
	}
	tokenPath, err := userconfig.TokenFilePath(profile)
	if err != nil {
		return nil, err
	}
	store := &credentials.Store{Profile: profile, Keyring: credentials.OSKeyring(), FilePath: tokenPath}
	refresh, _, err := store.Resolve()
	if err != nil {
		return nil, err
	}
	return auth.TokenSource(ctx, cfg, refresh, 60*time.Second), nil
}

// call sends one request and returns the status and the body.
func call(ctx context.Context, method, u string, body any) (int, string) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, strings.TrimSpace(string(data))
}

func setup(ctx context.Context, title string) (first, second string, err error) {
	status, body := call(ctx, http.MethodPost, sheetsBase+"/spreadsheets",
		map[string]any{"properties": map[string]any{"title": title}})
	if status != 200 {
		return "", "", fmt.Errorf("create: HTTP %d: %s", status, body)
	}
	var created struct {
		SpreadsheetID string `json:"spreadsheetId"`
		Sheets        []struct {
			Properties struct {
				SheetID int    `json:"sheetId"`
				Title   string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		return "", "", err
	}
	scratchID = created.SpreadsheetID
	first = created.Sheets[0].Properties.Title
	firstID := created.Sheets[0].Properties.SheetID
	second = "Sidecar"

	// A second sheet, a named range that deliberately shares the FIRST
	// sheet's title, and a table. The named range points at the second
	// sheet, so if an unquoted reference resolves to it the values come
	// back from the wrong place and say so.
	reqs := []any{
		map[string]any{"addSheet": map[string]any{"properties": map[string]any{"title": second}}},
	}
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": reqs})
	if status != 200 {
		return "", "", fmt.Errorf("addSheet: HTTP %d: %s", status, body)
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
	_ = json.Unmarshal([]byte(body), &added)
	secondID := added.Replies[0].AddSheet.Properties.SheetID

	if err := put(ctx, a1.QuoteSheet(first)+"!A1:B3", [][]any{
		{"FIRSTSHEET-A1", "FIRSTSHEET-B1"},
		{"FIRSTSHEET-A2", "FIRSTSHEET-B2"},
		{"FIRSTSHEET-A3", "FIRSTSHEET-B3"},
	}); err != nil {
		return "", "", err
	}
	if err := put(ctx, a1.QuoteSheet(second)+"!A1:B3", [][]any{
		{"NAMEDRANGE-A1", "NAMEDRANGE-B1"},
		{"NAMEDRANGE-A2", "NAMEDRANGE-B2"},
		{"NAMEDRANGE-A3", "NAMEDRANGE-B3"},
	}); err != nil {
		return "", "", err
	}

	reqs = []any{
		// The named range carries the first sheet's title and points at
		// the second sheet.
		map[string]any{"addNamedRange": map[string]any{"namedRange": map[string]any{
			"name":  first,
			"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}.GridRange(secondID),
		}}},
		// A named range whose name clashes with nothing, to tell "the
		// sheet wins over a named range" apart from "the ! decides".
		map[string]any{"addNamedRange": map[string]any{"namedRange": map[string]any{
			"name":  "Solo",
			"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}.GridRange(secondID),
		}}},
		// A protected range on the first sheet.
		map[string]any{"addProtectedRange": map[string]any{"protectedRange": map[string]any{
			"range":       a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 1}.GridRange(firstID),
			"description": "spike protected row",
			"warningOnly": false,
		}}},
		// A native table on the first sheet.
		map[string]any{"addTable": map[string]any{"table": map[string]any{
			"name":  "SpikeTable",
			"range": a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}.GridRange(firstID),
		}}},
	}
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": reqs})
	if status != 200 {
		line("setup: some structure could not be added (HTTP %d): %s", status, first120(body))
	}
	return first, second, nil
}

func put(ctx context.Context, rangeA1 string, values [][]any) error {
	u := sheetsBase + "/spreadsheets/" + scratchID + "/values/" + url.PathEscape(rangeA1) + "?valueInputOption=RAW"
	status, body := call(ctx, http.MethodPut, u, map[string]any{"values": values})
	if status != 200 {
		return fmt.Errorf("put %s: HTTP %d: %s", rangeA1, status, body)
	}
	return nil
}

// batchOne sends one batchUpdate request and returns the status and the
// body. Every spike that writes structure goes through it, so a probe
// reads as the one request it is rather than as a batch whose ordering
// could explain the result.
func batchOne(ctx context.Context, req map[string]any) (int, string) {
	return call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{req}})
}

// getValues asks for one range and reports what came back.
func getValues(ctx context.Context, rangeA1 string) (int, string) {
	u := sheetsBase + "/spreadsheets/" + scratchID + "/values/" + url.PathEscape(rangeA1) +
		"?valueRenderOption=UNFORMATTED_VALUE"
	return call(ctx, http.MethodGet, u, nil)
}

func probe(ctx context.Context, what, rangeA1 string) {
	status, body := getValues(ctx, rangeA1)
	line("  %-46s -> HTTP %d  %s", what, status, first120(body))
}

func first120(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func spikeC(ctx context.Context, first, second string) {
	sec("Spike C: range syntax")
	line("  the first sheet holds FIRSTSHEET-*; the named range %q points at %q, which holds NAMEDRANGE-*", first, second)

	probe(ctx, "quoted sheet title", a1.QuoteSheet(first)+"!A1:B1")
	probe(ctx, "UNQUOTED title, shadowed by a named range", first+"!A1:B1")
	probe(ctx, "bare named range name (no ! and no range)", first)
	probe(ctx, "bare name, quoted", a1.QuoteSheet(first))
	probe(ctx, "second sheet, quoted", a1.QuoteSheet(second)+"!A1:B1")
	// Which of the two rules is doing the work: does a sheet beat a
	// named range, or does the "!" decide before either is consulted?
	probe(ctx, "named range with no clash, bare", "Solo")
	probe(ctx, "named range with no clash, with !A1:B1", "Solo!A1:B1")
	probe(ctx, "named range with no clash, quoted bare", "'Solo'")
	probe(ctx, "clashing name, sub-range of the named range", first+"!A2:B2")
	probe(ctx, "table reference SpikeTable[Column]", "SpikeTable[FIRSTSHEET-A1]")
	probe(ctx, "table name alone", "SpikeTable")
	probe(ctx, "malformed range", a1.QuoteSheet(first)+"!A1:B2:C3")
	probe(ctx, "range past the sheet", a1.QuoteSheet(first)+"!A5000:B5001")
	probe(ctx, "R1C1", "R1C1:R1C2")
}

func spikeE(ctx context.Context, first string) {
	sec("Spike E: error shapes")

	status, body := call(ctx, http.MethodGet,
		sheetsBase+"/spreadsheets/1NoSuchSpreadsheetIdXXXXXXXXXXXXXXXXXXXXXXX?fields=spreadsheetId", nil)
	line("  %-46s -> HTTP %d  %s", "missing spreadsheet", status, first120(body))

	probe(ctx, "missing sheet", "'NoSuchSheetHere'!A1:B2")
	probe(ctx, "unparseable range", "!!!")

	// A write into the protected range, as the owner. The owner is
	// always an editor, so this is expected to succeed — which is itself
	// the answer: this shape cannot be observed with one account.
	u := sheetsBase + "/spreadsheets/" + scratchID + "/values/" +
		url.PathEscape(a1.QuoteSheet(first)+"!A1:B1") + "?valueInputOption=RAW"
	status, body = call(ctx, http.MethodPut, u, map[string]any{"values": [][]any{{"FIRSTSHEET-A1", "FIRSTSHEET-B1"}}})
	line("  %-46s -> HTTP %d  %s", "write into a protected range (as owner)", status, first120(body))

	// An unsupported field, to see what a 400 from the request body
	// looks like rather than from the range.
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate",
		map[string]any{"requests": []any{map[string]any{"noSuchRequest": map[string]any{}}}})
	line("  %-46s -> HTTP %d  %s", "batchUpdate with an unknown request kind", status, first120(body))

	line("")
	line("  Not observable with one account and one project:")
	line("    a protected-range 403 (the owner is always an editor)")
	line("    a read-only-scope 403 (this token holds the write scope)")
	line("    a 429 (would need sustained hammering of a shared quota)")
}
