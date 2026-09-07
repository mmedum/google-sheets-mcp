package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/server"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func newServer(t *testing.T, cfg config.Config, log *slog.Logger) (*mcp.Server, *sheetstest.Server) {
	t.Helper()
	fake := sheetstest.Standard(t)
	api := fake.Client()
	if log != nil {
		api = fake.ClientLogging(log)
	}
	svc := service.New(service.Deps{API: api, Config: cfg, Logger: log})
	return server.New(server.Deps{Service: svc, Config: cfg, Logger: log, Version: "test"}), fake
}

func connect(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// The surface this phase ships. It is written out so a tool appearing or
// disappearing is a decision somebody made rather than something that
// happened.
var readTools = []string{"find_in_spreadsheet", "get_spreadsheet", "read_formatting", "read_range", "search_spreadsheets"}

// writeTools are registered unless the server is read-only;
// destructiveTools need the destructive flag as well.
var writeTools = []string{
	"append_rows", "create_spreadsheet", "edit_dimensions", "format_cells",
	"manage_anchor", "manage_range", "manage_sheet", "transform_range", "write_values",
}

var destructiveTools = []string{"clear_values", "delete_dimensions", "delete_sheet"}

// allTools is the surface this phase ships, which is what the schema
// dump carries.
func allTools() []string {
	all := append(append(append([]string{}, readTools...), writeTools...), destructiveTools...)
	slices.Sort(all)
	return all
}

func TestToolSurface(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	res, err := connect(t, s).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description; a tool the model cannot understand does not exist", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s declares no output schema, so a client showing structure would see nothing", tool.Name)
		}
		if strings.Contains(tool.Description, "Sheet1") {
			t.Errorf("%s offers Sheet1 as an example, which does not exist on a non-English account", tool.Name)
		}
		if strings.Contains(tool.Name, ".") {
			t.Errorf("%s has a dot in its name", tool.Name)
		}
	}
	slices.Sort(names)
	want := append(append([]string{}, readTools...), writeTools...)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

// TestToolsNameEachOther is the rule that a tool the model never finds
// does not exist: where two tools overlap, each description says when to
// choose the other.
func TestToolsNameEachOther(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	res, err := connect(t, s).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool.Description
	}
	for _, pair := range [][2]string{
		{"search_spreadsheets", "find_in_spreadsheet"},
		{"find_in_spreadsheet", "search_spreadsheets"},
		{"read_range", "get_spreadsheet"},
		{"get_spreadsheet", "read_range"},
		// Phase 2's four overlap the same way: a formatting read is the
		// other half of a values read, and a conditional format rule is
		// attached to a range rather than written into one, so the tool
		// that does not do it says which one does.
		{"read_formatting", "read_range"},
		{"format_cells", "manage_range"},
		{"manage_range", "read_formatting"},
		{"transform_range", "checkpoint"},
	} {
		if !strings.Contains(byName[pair[0]], pair[1]) {
			t.Errorf("%s never mentions %s, so a model choosing between them has nothing to go on", pair[0], pair[1])
		}
	}
}

func TestReadOnlyModeKeepsTheReads(t *testing.T) {
	s, _ := newServer(t, config.Config{ReadOnly: true, EnableDestructive: true}, nil)
	res, err := connect(t, s).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	// Destructive is on as well, so this asserts the two gates in the
	// order that matters: read-only wins over it, rather than the two
	// combining into a server that offers a destructive tool.
	if !slices.Equal(names, readTools) {
		t.Errorf("read-only mode registered %v, want only the reads", names)
	}
}

// TestBothHalvesCarryTheGrid is §4.9 made a test. Clients disagree about
// which half they show the model: one shows only structuredContent,
// others show the text. A design that puts the addresses in one half
// loses them on some client, silently, until a task goes strangely
// wrong.
func TestBothHalvesCarryTheGrid(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	cs := connect(t, s)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "read_range",
		Arguments: map[string]any{
			"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "range": "A1:C3",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the call failed: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("no text content; a client that shows only text would see nothing")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.StructuredContent == nil {
		t.Fatal("no structured content; a client that shows only structure would see nothing")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured struct {
		Grid string `json:"grid"`
	}
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatal(err)
	}
	for _, half := range []struct{ name, body string }{{"text", text}, {"structured", structured.Grid}} {
		for _, want := range []string{"A", "1 |", "Plimth"} {
			if !strings.Contains(half.body, want) {
				t.Errorf("the %s half has no %q:\n%s", half.name, want, half.body)
			}
		}
	}
	// Not the same bytes: the text half carries the footer, the
	// structured half carries the fields the footer describes.
	if text == structured.Grid {
		t.Error("the two halves are byte-identical, which is the one shape the specification asks callers not to send")
	}
}

func TestToolErrorsAreResultsNotProtocolErrors(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	res, err := connect(t, s).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_range",
		Arguments: map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": "Sheet1", "range": "A1:B2"},
	})
	if err != nil {
		t.Fatalf("a tool refusal arrived as a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("the refusal was not marked as an error")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "[not_found]") {
		t.Errorf("the refusal is not in [class] form: %q", text)
	}
	if !strings.Contains(text, sheetstest.SecondSheet) {
		t.Errorf("the refusal does not tell the model what does exist: %q", text)
	}
}

func TestDumpSchemas(t *testing.T) {
	// The full surface, as --dump-schemas builds it: a dump that showed
	// only what this configuration registers would let a destructive
	// tool's schema change without the diff gate ever seeing it.
	s, _ := newServer(t, config.Config{EnableDestructive: true}, nil)
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), s, &buf, "test"); err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Server string `json:"server"`
		SDK    string `json:"sdk"`
		Tools  []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatalf("the dump is not JSON: %v", err)
	}
	if dump.Server != server.Name || dump.SDK != server.SDKVersion {
		t.Errorf("dump header = %+v", dump)
	}
	if len(dump.Tools) != len(allTools()) {
		t.Fatalf("the dump has %d tools", len(dump.Tools))
	}
	// Sorted, so a diff between two dumps is a change and not a map's
	// iteration order.
	for i := 1; i < len(dump.Tools); i++ {
		if dump.Tools[i-1].Name > dump.Tools[i].Name {
			t.Errorf("the dump is not sorted: %s before %s", dump.Tools[i-1].Name, dump.Tools[i].Name)
		}
	}
	// One invalid schema is not one broken tool: a client validating
	// against draft 2020-12 rejects the whole request and every tool in
	// the session dies with it.
	for _, tool := range dump.Tools {
		var schema struct {
			Type       string          `json:"type"`
			Properties json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("%s: input schema is not an object: %v", tool.Name, err)
			continue
		}
		if schema.Type != "object" || len(schema.Properties) == 0 {
			t.Errorf("%s: input schema is %s with properties %s", tool.Name, schema.Type, schema.Properties)
		}
	}
}

func TestInstructionsPointAtGetSpreadsheetFirst(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	cs := connect(t, s)
	init := cs.InitializeResult()
	if init == nil || init.Instructions == "" {
		t.Fatal("no instructions")
	}
	if !strings.Contains(init.Instructions, "get_spreadsheet") {
		t.Error("the instructions do not name the first call to make")
	}
	if strings.Contains(init.Instructions, "Sheet1") {
		t.Error("the instructions teach the model to guess a sheet name")
	}
}

// TestResourcesRouteByTemplate checks the SDK's own matching rather than
// this code's reading of RFC 6570. The two templates differ by one path
// segment, and if `{spreadsheet}` matched a slash the card template
// would swallow every sheet URI and answer a card for all of them —
// which looks like a working resource until somebody wants their data.
func TestResourcesRouteByTemplate(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	cs := connect(t, s)
	ctx := context.Background()

	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpls.ResourceTemplates) != 2 {
		t.Fatalf("%d resource templates; §8 offers two", len(tmpls.ResourceTemplates))
	}
	// No static list: enumerating spreadsheets is a Drive listing.
	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 0 {
		t.Errorf("%d static resources; there is deliberately no list", len(res.Resources))
	}

	card, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "gsheets://" + sheetstest.FixtureID})
	if err != nil {
		t.Fatalf("reading the card: %v", err)
	}
	if len(card.Contents) != 1 || !strings.Contains(card.Contents[0].Text, sheetstest.FirstSheet) {
		t.Errorf("the card resource does not name the sheets: %+v", card.Contents)
	}
	if card.Contents[0].MIMEType != "text/plain" {
		t.Errorf("card MIME type = %q", card.Contents[0].MIMEType)
	}

	sheet, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "gsheets://" + sheetstest.FixtureID + "/" + sheetstest.FirstSheet,
	})
	if err != nil {
		t.Fatalf("reading the sheet: %v", err)
	}
	if len(sheet.Contents) == 0 {
		t.Fatal("the sheet resource is empty")
	}
	if sheet.Contents[0].MIMEType != "text/csv" {
		t.Errorf("sheet MIME type = %q, want text/csv — the router sent this to the card handler", sheet.Contents[0].MIMEType)
	}
	if !strings.Contains(sheet.Contents[0].Text, "Plimth,Nardle") {
		t.Errorf("the sheet resource is not the CSV: %.80q", sheet.Contents[0].Text)
	}

	// A percent-encoded title, which is the case the scheme exists to
	// carry: the fixture's second sheet is not ASCII.
	enc, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "gsheets://" + sheetstest.FixtureID + "/" + url.PathEscape(sheetstest.SecondSheet),
	})
	if err != nil {
		t.Fatalf("reading a percent-encoded sheet title: %v", err)
	}
	if !strings.Contains(enc.Contents[0].Text, "Trennow") {
		t.Errorf("the encoded title read the wrong sheet: %.80q", enc.Contents[0].Text)
	}
}

func TestResourceNotFoundIsTheSpecifiedError(t *testing.T) {
	s, _ := newServer(t, config.Config{}, nil)
	cs := connect(t, s)
	_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "gsheets://" + sheetstest.FixtureID + "/Nardlewick",
	})
	if err == nil {
		t.Fatal("a sheet that does not exist was read")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Errorf("err = %v, want the specification's resource-not-found", err)
	}
}
