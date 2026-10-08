package tools_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/v3/internal/config"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v3/internal/server"
	"github.com/mmedum/google-sheets-mcp/v3/internal/service"
	"github.com/mmedum/google-sheets-mcp/v3/internal/tools"
)

// The protocols a question goes out on: before 2026-07-28 the SDK asks
// with elicitation/create inside the call; from it, the call returns the
// question and comes back with the answer (§9a).
var protocols = []string{"2025-06-18", "2025-11-25", "2026-07-28"}

// everything registers every tool.
func everything(t *testing.T) config.Config {
	t.Helper()
	s := &config.Settings{
		Profile: "default", LogLevel: "info", LogFormat: "text",
		MaxCells: "5000", MaxChars: "20000", HTTPTimeout: "60s", WriteTimeout: "180s",
	}
	cfg, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	return tools.FullSurface(cfg)
}

// person answers the questions a test client is asked, and keeps them.
type person struct {
	mu        sync.Mutex
	questions []*mcp.ElicitParams
	action    string
	// fails makes the client answer with an error instead.
	fails bool
}

func (p *person) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, req.Params)
	if p.fails {
		return nil, errors.New("client-side secret text")
	}
	return &mcp.ElicitResult{Action: p.action}, nil
}

func (p *person) asked() []*mcp.ElicitParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.questions)
}

// now is the service's clock, which a test moves past the card cache.
var now = time.Now

// accepting confirms every question, to set a fixture up.
type accepting struct{}

func (accepting) Ask(_ context.Context, build service.Build) error {
	_, err := build()
	return err
}

// connect connects a client on protocol to a server over a fresh fake,
// with one data source already connected. A nil p declares no
// elicitation; opts adjust the client further. The fake's calls are
// reset, so a test counts only its own.
func connect(t *testing.T, cfg config.Config, protocol string, p *person, opts ...func(*mcp.ClientOptions)) (*mcp.ClientSession, *sheetstest.Server, string) {
	t.Helper()
	fake := sheetstest.Standard(t)
	svc := service.New(service.Deps{API: fake.Client(), Config: cfg, Now: func() time.Time { return now() }})
	src, err := svc.ManageDataSource(service.WithAsker(context.Background(), accepting{}), service.SourceRequest{
		Spreadsheet: sheetstest.FixtureID, Action: service.SourceAdd, Project: "example-project", Query: "SELECT 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fake.Reset()
	srv := server.New(server.Deps{Service: svc, Config: cfg, Version: "test"})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	o := &mcp.ClientOptions{}
	if p != nil {
		o.ElicitationHandler = p.handle
	}
	for _, fn := range opts {
		fn(o)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, o).
		Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake, src.ID
}

// askCase is a call that clears a tool's own guards and reaches its
// write, the operation the fake records for it, and words its question
// must carry.
type askCase struct {
	// tool is the tool the case calls, when it is not the case's name.
	tool  string
	args  map[string]any
	op    string
	shows []string
}

var askCases = map[string]askCase{
	"delete_sheet": {
		args: map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.SecondSheet, "confirm": true},
		op:   "spreadsheets.batchUpdate",
		shows: []string{"delete the sheet `" + sheetstest.SecondSheet + "` of `Quorbin Skerry`, and everything on it?",
			"non-empty cell", "Sheets cannot undo it"},
	},
	"delete_dimensions": {
		args: map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet,
			"dimension": "rows", "band": "2:3", "confirm": true},
		op:    "spreadsheets.batchUpdate",
		shows: []string{"delete rows 2:3 on the sheet `" + sheetstest.FirstSheet + "`", "moves up or left"},
	},
	"clear_values": {
		args: map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.SecondSheet,
			"range": "A1:B3", "confirm": true},
		op:    "values.clear",
		shows: []string{"clear the values in `'" + sheetstest.SecondSheet + "'!A1:B3` of `Quorbin Skerry`", "Formatting"},
	},
	"delete_data_source": {
		args:  map[string]any{"spreadsheet": sheetstest.FixtureID, "confirm": true},
		op:    "spreadsheets.batchUpdate",
		shows: []string{"delete_data_source: delete the data source `", "re-running its query"},
	},
	"manage_data_source refresh": {
		tool:  "manage_data_source",
		args:  map[string]any{"spreadsheet": sheetstest.FixtureID, "action": "refresh"},
		op:    "spreadsheets.batchUpdate",
		shows: []string{"refresh every data source in `Quorbin Skerry`?", "1 data source now"},
	},
	"manage_data_source": {
		args: map[string]any{"spreadsheet": sheetstest.FixtureID, "action": "add",
			"project": "example-project", "query": "SELECT 2"},
		op: "spreadsheets.batchUpdate",
		shows: []string{"connect BigQuery to `Quorbin Skerry`, billed to the Cloud project `example-project`?",
			"query: `SELECT 2`", "charged to that project"},
	},
}

// argsFor is a case's arguments, with the fixture's data source filled in.
func argsFor(name, source string) map[string]any {
	args := map[string]any{}
	for k, v := range askCases[name].args {
		args[k] = v
	}
	if name == "delete_data_source" {
		args["id"] = source
	}
	return args
}

// toolOf is the tool a case calls.
func toolOf(name string) string {
	if t := askCases[name].tool; t != "" {
		return t
	}
	return name
}

// writes counts the calls that made a write of op.
func writes(fake *sheetstest.Server, op string) int {
	n := 0
	for _, c := range fake.Calls() {
		if c.Op == op && c.Method == http.MethodPost {
			n++
		}
	}
	return n
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func callTool(t *testing.T, cs *mcp.ClientSession, p *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), p)
	if err != nil {
		t.Fatalf("calling %s: %v", p.Name, err)
	}
	return res
}

// Declined, nothing is written; accepted, the write is made once. On
// every protocol, for every tool that asks, and the question says what
// the write would do.
func TestEveryAskingWriteWaitsForThePerson(t *testing.T) {
	for name, c := range askCases {
		for _, protocol := range protocols {
			for _, action := range []string{"decline", "cancel", "accept"} {
				p := &person{action: action}
				cs, fake, src := connect(t, everything(t), protocol, p)
				res := callTool(t, cs, &mcp.CallToolParams{Name: toolOf(name), Arguments: argsFor(name, src)})
				out := text(res)
				qs := p.asked()
				if len(qs) != 1 {
					t.Fatalf("%s %s %s: asked %d times: %s", name, protocol, action, len(qs), out)
				}
				for _, want := range c.shows {
					if !strings.Contains(qs[0].Message, want) {
						t.Errorf("%s: the question does not say %q:\n%s", name, want, qs[0].Message)
					}
				}
				n := writes(fake, c.op)
				if action != "accept" {
					if !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") || n != 0 {
						t.Errorf("%s %s %s: %d writes: %s", name, protocol, action, n, out)
					}
					continue
				}
				if res.IsError || n != 1 {
					t.Errorf("%s %s accepted: %d writes: %s", name, protocol, n, out)
				}
			}
		}
	}
}

// Every tool that takes confirm asks, as does manage_data_source; the
// confirm half of the list is read from the published schemas.
func TestEveryToolThatTakesConfirmAsks(t *testing.T) {
	cs, _, _ := connect(t, everything(t), "", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"manage_data_source": true}
	for _, tool := range res.Tools {
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["confirm"]; ok {
			want[tool.Name] = true
		}
		asks := strings.Contains(tool.Description, "also asks the person")
		if _, ok := askCases[tool.Name]; ok != asks {
			t.Errorf("%s: an asking case %v, and its description says it asks %v", tool.Name, ok, asks)
		}
	}
	if len(want) < 5 {
		t.Fatalf("found %d asking tools; the schemas were not read", len(want))
	}
	for name := range want {
		if _, ok := askCases[name]; !ok {
			t.Errorf("%s takes confirm or is an asking tool and has no asking case", name)
		}
	}
	for name := range askCases {
		if !want[toolOf(name)] {
			t.Errorf("%s has an asking case and is not an asking tool", name)
		}
	}
}

// A client that cannot ask gets no question, and the arguments are the
// guard; GSHEETS_REQUIRE_PROMPT refuses the write instead.
func TestAClientThatCannotAsk(t *testing.T) {
	for name, c := range askCases {
		for _, require := range []bool{false, true} {
			cfg := everything(t)
			cfg.RequirePrompt = require
			cs, fake, src := connect(t, cfg, "", nil)
			res := callTool(t, cs, &mcp.CallToolParams{Name: toolOf(name), Arguments: argsFor(name, src)})
			n := writes(fake, c.op)
			switch {
			case require && (!res.IsError || !strings.Contains(text(res), "GSHEETS_REQUIRE_PROMPT") || n != 0):
				t.Errorf("%s required: %d writes: %s", name, n, text(res))
			case !require && (res.IsError || n != 1):
				t.Errorf("%s not required: %d writes: %s", name, n, text(res))
			}
		}
	}
}

// Refreshing every source asks; refreshing one, or canceling, does not.
func TestWhichRefreshesAsk(t *testing.T) {
	for _, tc := range []struct {
		args func(src string) map[string]any
		want string
	}{
		{func(string) map[string]any { return map[string]any{"action": "refresh"} },
			"refresh every data source in `Quorbin Skerry`?"},
		{func(src string) map[string]any { return map[string]any{"action": "refresh", "id": src} }, ""},
		{func(string) map[string]any { return map[string]any{"action": "cancel_refresh"} }, ""},
	} {
		p := &person{action: "decline"}
		cs, fake, src := connect(t, everything(t), "2026-07-28", p)
		args := tc.args(src)
		args["spreadsheet"] = sheetstest.FixtureID
		res := callTool(t, cs, &mcp.CallToolParams{Name: "manage_data_source", Arguments: args})
		qs := p.asked()
		n := writes(fake, "spreadsheets.batchUpdate")
		switch {
		case tc.want == "" && (len(qs) != 0 || res.IsError || n != 1):
			t.Errorf("%v: asked %d, %d writes: %s", args, len(qs), n, text(res))
		case tc.want != "" && (len(qs) != 1 || !res.IsError || n != 0):
			t.Errorf("%v: asked %d, %d writes: %s", args, len(qs), n, text(res))
		case tc.want != "" && (!strings.Contains(qs[0].Message, tc.want) || !strings.Contains(qs[0].Message, "1 data source")):
			t.Errorf("%v: %s", args, qs[0].Message)
		}
	}
}

// A dry run asks nothing and writes nothing.
func TestADryRunAsksNothing(t *testing.T) {
	p := &person{action: "decline"}
	cs, fake, src := connect(t, everything(t), "2026-07-28", p)
	for name := range askCases {
		args := argsFor(name, src)
		delete(args, "confirm")
		args["dry_run"] = true
		if res := callTool(t, cs, &mcp.CallToolParams{Name: toolOf(name), Arguments: args}); res.IsError {
			t.Errorf("%s: %s", name, text(res))
		}
	}
	if qs := p.asked(); len(qs) != 0 {
		t.Errorf("asked %d questions: %s", len(qs), qs[0].Message)
	}
	if n := writes(fake, "spreadsheets.batchUpdate") + writes(fake, "values.clear"); n != 0 {
		t.Errorf("a dry run made %d writes", n)
	}
}

// A client that answers the question with an error writes nothing, and
// its error text is not repeated.
func TestAClientErrorIsNotAnAnswer(t *testing.T) {
	for name, c := range askCases {
		for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
			cs, fake, src := connect(t, everything(t), protocol, &person{fails: true})
			res := callTool(t, cs, &mcp.CallToolParams{Name: toolOf(name), Arguments: argsFor(name, src)})
			out := text(res)
			if !res.IsError || !strings.HasPrefix(out, "[blocked]") || strings.Contains(out, "secret") || writes(fake, c.op) != 0 {
				t.Errorf("%s %s: %d writes: %s", name, protocol, writes(fake, c.op), out)
			}
		}
	}
}

// mrtr connects a 2026-07-28 client that hands each question back
// instead of answering it, so a test can answer by hand.
func mrtr(t *testing.T) (*mcp.ClientSession, *sheetstest.Server, string) {
	t.Helper()
	return connect(t, everything(t), "2026-07-28", &person{action: "accept"}, func(o *mcp.ClientOptions) {
		o.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	})
}

var accepted = mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: "accept"}}

// The first round only asks. The answer counts once, only with the state
// it was asked with, only for that call, and only while fresh.
func TestTheAnswerIsBoundToItsQuestion(t *testing.T) {
	cs, fake, _ := mrtr(t)
	c := askCases["delete_sheet"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args})
	q, ok := first.InputRequests["confirm"].(*mcp.ElicitParams)
	if !first.NeedsInput() || !ok || q.Mode != "form" || first.RequestState == "" || writes(fake, c.op) != 0 {
		t.Fatalf("first round %+v; %d writes", first, writes(fake, c.op))
	}
	state := first.RequestState

	blocked := func(p *mcp.CallToolParams, want string) {
		t.Helper()
		res := callTool(t, cs, p)
		if out := text(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, want) {
			t.Errorf("%s", out)
		}
	}
	other := map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": sheetstest.FirstSheet, "confirm": true}
	blocked(&mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted},
		"answers to a question this server has not asked")
	blocked(&mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted, RequestState: state + "x"},
		"did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted,
		RequestState: "e30." + strings.Split(state, ".")[1]}, "did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_sheet", Arguments: other, InputResponses: accepted, RequestState: state},
		"another call")
	blocked(&mcp.CallToolParams{Name: "clear_values", Arguments: askCases["clear_values"].args, InputResponses: accepted,
		RequestState: state}, "another call")
	blocked(&mcp.CallToolParams{Name: "get_spreadsheet", Arguments: map[string]any{"spreadsheet": sheetstest.FixtureID},
		InputResponses: accepted, RequestState: state}, "not a tool here that asks the person")
	if n := writes(fake, c.op) + writes(fake, "values.clear"); n != 0 {
		t.Fatalf("%d writes before the answer", n)
	}

	done := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted, RequestState: state})
	if done.IsError || writes(fake, c.op) != 1 {
		t.Fatalf("the verified retry: %s; %d writes", text(done), writes(fake, c.op))
	}
	blocked(&mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted, RequestState: state}, "already used")
}

// Any answer but an accept is refused before the call reads anything.
func TestARefusalIsRefusedBeforeAnyRead(t *testing.T) {
	for _, action := range []string{"decline", "cancel", "maybe"} {
		cs, fake, _ := mrtr(t)
		c := askCases["clear_values"]
		first := callTool(t, cs, &mcp.CallToolParams{Name: "clear_values", Arguments: c.args})
		before := len(fake.Calls())
		res := callTool(t, cs, &mcp.CallToolParams{Name: "clear_values", Arguments: c.args, RequestState: first.RequestState,
			InputResponses: mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: action}}})
		if out := text(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") {
			t.Errorf("%s: %s", action, out)
		}
		if n := len(fake.Calls()); before == 0 || n != before {
			t.Errorf("%s: %d Sheets calls after the answer (%d before)", action, n-before, before)
		}
	}
}

// What the person saw is what is written: a sheet renamed between the
// question and the answer is another question, and the next call asks
// again.
func TestAChangeAfterTheQuestionIsRefused(t *testing.T) {
	cs, fake, _ := mrtr(t)
	c := askCases["delete_sheet"]
	args := map[string]any{"spreadsheet": sheetstest.FixtureID, "sheet": "1837", "confirm": true}
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: args})
	for _, sh := range fake.Doc(sheetstest.FixtureID).Sheets {
		if sh.Props.SheetID == 1837 {
			sh.Props.Title = "Something else"
		}
	}
	later := time.Now().Add(service.CacheWindow + time.Second)
	now = func() time.Time { return later }
	t.Cleanup(func() { now = time.Now })
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: args,
		InputResponses: accepted, RequestState: first.RequestState})
	if out := text(res); !res.IsError || !strings.Contains(out, "changed after the person was asked") || writes(fake, c.op) != 0 {
		t.Errorf("%s; %d writes", out, writes(fake, c.op))
	}
}

// A state that travels through the client expires; one that stays in
// the process, before 2026-07-28, waits as long as the request does.
func TestALateAnswerIsRefusedOnlyWhenTheStateTravels(t *testing.T) {
	t.Cleanup(tools.SetAskTTL(-time.Minute))
	cs, fake, _ := mrtr(t)
	c := askCases["delete_sheet"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args})
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := text(res); !res.IsError || !strings.Contains(out, "expired") || writes(fake, c.op) != 0 {
		t.Fatalf("%s; %d writes", out, writes(fake, c.op))
	}
	for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
		cs, fake, _ := connect(t, everything(t), protocol, &person{action: "accept"})
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_sheet", Arguments: c.args})
		if res.IsError || writes(fake, c.op) != 1 {
			t.Errorf("%s: a slow accept in the process was refused: %s", protocol, text(res))
		}
	}
}

// A tool that asks the person before every write carries Claude Code's
// requiresUserInteraction mark only for a client that cannot ask; with
// both, the person would answer twice for one call. The list is the
// four §9a asks before every write: a new name here needs a look at
// whether it really asks every time.
func TestTheMarkIsForAClientThatCannotAsk(t *testing.T) {
	marked := func(cs *mcp.ClientSession) []string {
		t.Helper()
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tool := range res.Tools {
			if tool.Meta["anthropic/requiresUserInteraction"] == true {
				out = append(out, tool.Name)
			}
		}
		slices.Sort(out)
		return out
	}
	all := "clear_values delete_data_source delete_dimensions delete_sheet"
	for _, protocol := range protocols {
		cs, _, _ := connect(t, everything(t), protocol, &person{action: "accept"})
		if got := marked(cs); len(got) != 0 {
			t.Errorf("%s, a client that can ask: marked %v, want none", protocol, got)
		}
		cs, _, _ = connect(t, everything(t), protocol, nil)
		if got := strings.Join(marked(cs), " "); got != all {
			t.Errorf("%s, a client that cannot ask: marked %q, want %q", protocol, got, all)
		}
	}
}
