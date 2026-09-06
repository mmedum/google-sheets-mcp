package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
)

// searchTerm is a word that exists nowhere else, so finding it in a log
// proves a search term reached one. It reaches the server as a Drive
// query and a regex, and from there into a request URL, which is the
// route that is easy to forget.
const searchTerm = "Zorblatt"

// toolCalls is what the logging test drives. Each tool gets at least one
// call that works and one that is refused, because a refusal is where a
// message is most tempted to quote what it refused.
//
// The table is kept complete by TestEveryToolIsDriven rather than by
// anybody remembering to extend it: in a sibling repository this same
// test had quietly stopped covering eight newer tools, including the
// only ones that took an email address.
var toolCalls = map[string][]map[string]any{
	"get_spreadsheet": {
		{"spreadsheet": sheetstest.FixtureID},
		{"spreadsheet": searchTerm},
	},
	"read_range": {
		{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "range": "A1:D6",
			"include_notes": true, "include_validation": true, "include_merges": true},
		{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "range": "A1:D4", "show": "both"},
		{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "range": "A1:B2", "format": "csv"},
		// Refusals: a sheet that does not exist, and a range that is not one.
		{"spreadsheet": sheetstest.FixtureID, "sheet": searchTerm, "range": "A1:B2"},
		{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "range": searchTerm},
	},
	"search_spreadsheets": {
		{"name": "Quorbin"},
		{"name": searchTerm},
		{"modified_after": searchTerm},
	},
	"find_in_spreadsheet": {
		{"spreadsheet": sheetstest.FixtureID, "query": "Plimth", "search_formulas": true, "search_notes": true},
		{"spreadsheet": sheetstest.FixtureID, "regex": "(?i)" + searchTerm},
		{"spreadsheet": sheetstest.FixtureID, "regex": "(?P<"},
	},
}

func TestEveryToolIsDriven(t *testing.T) {
	s, _ := newServer(t, config.Config{EnableDestructive: true}, nil)
	res, err := connect(t, s).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
		if len(toolCalls[tool.Name]) == 0 {
			t.Errorf("%s is registered and the logging test never calls it", tool.Name)
		}
	}
	for name := range toolCalls {
		if !registered[name] {
			t.Errorf("the logging test calls %s, which is not registered", name)
		}
	}
	if len(registered) == 0 {
		t.Fatal("no tools registered; this check is not looking at a server")
	}
}

// TestLogsCarryNothingFromTheSpreadsheet drives every tool at debug
// level against a fixture whose every value is a word that exists
// nowhere else, and fails if any of it reaches the log.
//
// The forbidden list is derived from the fixture rather than typed out,
// so a value added to the fixture is covered from the moment it is
// added. Two routes are the ones that catch people: a search term
// travels inside a request URL, and this server's own error messages
// quote the spreadsheet back — a [not_found] that lists the sheet titles
// which do exist is the right answer to give a model and the wrong thing
// to write into a log.
func TestLogsCarryNothingFromTheSpreadsheet(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, fake := newServer(t, config.Config{}, log)
	cs := connect(t, s)

	ctx := context.Background()
	calls := 0
	for _, name := range slices.Sorted(keys(toolCalls)) {
		for _, args := range toolCalls[name] {
			if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			calls++
		}
	}

	logged := buf.String()
	// Zero findings and zero inputs must not read the same. If nothing
	// was logged, this test proves nothing.
	if lines := strings.Count(logged, "\n"); lines < calls {
		t.Fatalf("%d log lines for %d calls; the logger is not recording anything", lines, calls)
	}

	for _, forbidden := range forbiddenStrings(t) {
		if strings.Contains(logged, forbidden) {
			t.Errorf("the log carries %q, which came out of the spreadsheet", forbidden)
		}
	}

	// The truncated id is the one identifier that may appear: it is the
	// correlation key an operator needs to trace a retry to the call that
	// caused it, and six characters of a base64url id cannot be looked
	// up or pasted into a URL.
	if !strings.Contains(logged, "1Synth…") {
		t.Error("no truncated spreadsheet id in the log; a retry could not be traced to its call")
	}

	// Asserted over what actually reached the fake, not over a list of
	// calls somebody remembered to write. Phase 0 reads; the day a write
	// arrives, this line is what notices.
	methods := map[string]bool{}
	for _, c := range fake.Calls() {
		methods[c.Method] = true
	}
	if len(methods) != 1 || !methods[http.MethodGet] {
		t.Errorf("the tools sent %v; every phase 0 tool is a read", slices.Sorted(keys(methods)))
	}
}

// forbiddenStrings is everything the fixture holds that a log must never
// carry, read out of the fixture itself.
func forbiddenStrings(t *testing.T) []string {
	t.Helper()
	doc, file := sheetstest.Fixture()
	seen := map[string]bool{}
	add := func(vs ...string) {
		for _, v := range vs {
			if len(v) >= 4 {
				seen[v] = true
			}
		}
	}
	add(doc.ID, doc.Title, file.Name, searchTerm)
	// "Synthetic" rather than the whole id, so a truncated id that keeps
	// six characters is allowed and a longer prefix is not.
	add("SyntheticFixture")
	for _, sh := range doc.Sheets {
		add(sh.Props.Title)
		for _, c := range sh.Cells {
			add(c.FormattedValue, c.Note)
			if v := c.UserEnteredValue; v != nil {
				if v.StringValue != nil {
					add(*v.StringValue)
				}
				if v.FormulaValue != nil {
					add(*v.FormulaValue)
				}
			}
		}
	}
	for _, n := range doc.NamedRanges {
		add(n.Name)
	}
	// A1 ranges are addresses, and an address says which part of
	// somebody's spreadsheet was touched.
	add("A1:D6", "A1:D4", "A1:B2")
	if len(seen) < 20 {
		t.Fatalf("only %d forbidden strings; the list is not being read out of the fixture", len(seen))
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

func keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
