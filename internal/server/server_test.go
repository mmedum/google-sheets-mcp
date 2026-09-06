package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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

// phase0Tools is the surface this phase ships. It is written out so a
// tool appearing or disappearing is a decision somebody made rather than
// something that happened.
var phase0Tools = []string{"find_in_spreadsheet", "get_spreadsheet", "read_range", "search_spreadsheets"}

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
	if !slices.Equal(names, phase0Tools) {
		t.Errorf("tools = %v, want %v", names, phase0Tools)
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
	} {
		if !strings.Contains(byName[pair[0]], pair[1]) {
			t.Errorf("%s never mentions %s, so a model choosing between them has nothing to go on", pair[0], pair[1])
		}
	}
}

func TestReadOnlyModeKeepsTheReads(t *testing.T) {
	s, _ := newServer(t, config.Config{ReadOnly: true}, nil)
	res, err := connect(t, s).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(phase0Tools) {
		t.Errorf("read-only mode registered %d tools; every phase 0 tool is a read", len(res.Tools))
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
	s, _ := newServer(t, config.Config{}, nil)
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
	if len(dump.Tools) != len(phase0Tools) {
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
