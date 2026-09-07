//go:build live

// Command livesheet drives the built server against a real Google
// account and prints a transcript.
//
// Green gates are not done. Anything touching the write path or an API
// response shape gets a live run before it counts, and the transcript is
// read — a sibling project's driver twice reported that all calls
// behaved as expected while three of its results were wrong, because it
// checked whether calls succeeded rather than whether they told the
// truth. Three rules are inherited from that fix rather than
// rediscovered:
//
//   - Every step carries an expected outcome and a reason. An expected
//     refusal proves as much as a success, and a success where a
//     refusal was expected is a failure. That alone caught two of the
//     three.
//   - A step that asserts state reads it back. All three wrong results
//     were describing the state from before the write.
//   - Anything eventually consistent is polled and says which it saw.
//     Here that is Drive's index, and one read cannot tell indexing lag
//     from a broken search.
//
// The driver never reads a spreadsheet it did not write: it creates a
// scratch one, fills it with its own invented data and works inside
// that, so the values in a transcript are its own. Redaction of ids and
// links is a second line of defence and lives in the print helper only —
// scrubbing on the read path means a step parses a placeholder out of
// one result and feeds it back into the next call, which is a mistake a
// sibling made on its driver's first run.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/auth"
	"github.com/mmedum/google-sheets-mcp/internal/credentials"
	"github.com/mmedum/google-sheets-mcp/internal/livecover"
	"github.com/mmedum/google-sheets-mcp/internal/userconfig"
)

func main() {
	bin := flag.String("bin", "./google-sheets-mcp", "the built server")
	profile := flag.String("profile", envOr("GSHEETS_PROFILE", "default"), "which profile's credentials to use")
	keep := flag.Bool("keep", false, "do not attempt to trash the scratch spreadsheet")
	flag.Parse()

	if err := run(context.Background(), *bin, *profile, *keep); err != nil {
		fmt.Fprintf(os.Stderr, "\nlivesheet: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(ctx context.Context, bin, profile string, keep bool) error {
	ts, err := tokenSource(ctx, profile)
	if err != nil {
		return err
	}
	api := newDriverAPI(ctx, ts)

	title := fmt.Sprintf("%s %s", scratchTitle, time.Now().UTC().Format("2006-01-02 15:04:05"))
	sec("Scratch spreadsheet")
	id, firstSheet, err := api.createSpreadsheet(ctx, title)
	if err != nil {
		return fmt.Errorf("create the scratch spreadsheet: %w", err)
	}
	reg(id, "<spreadsheet>")
	line("created %s, first sheet %q", redact(id), firstSheet)
	// The first sheet's name is read, never assumed: on a non-English
	// account it is not "Sheet1", and this run is the one place that
	// would notice.
	if firstSheet == "Sheet1" {
		line("note: this account names the first sheet Sheet1, so a run here cannot catch an assumption about it")
	} else {
		line("this account names the first sheet %q, which is what makes the assumption visible", firstSheet)
	}

	secondSheet := "Ürväl livesheet"
	secondID, err := api.addSheet(ctx, id, secondSheet)
	if err != nil {
		return fmt.Errorf("add the second sheet: %w", err)
	}
	line("added a second sheet %q (id %d), whose title needs quoting", secondSheet, secondID)

	if err := api.putValues(ctx, id, a1.QuoteSheet(firstSheet)+"!A1:D6", seedRows()); err != nil {
		return fmt.Errorf("fill the scratch spreadsheet: %w", err)
	}
	if err := api.putValues(ctx, id, a1.QuoteSheet(secondSheet)+"!A1:B2", [][]any{{"Trennow", "Bractal"}, {"Skerry", 42}}); err != nil {
		return fmt.Errorf("fill the second sheet: %w", err)
	}
	if err := api.addNote(ctx, id, 0, 2, 1, "Quorbin reconciliation pending"); err != nil {
		return fmt.Errorf("add a note: %w", err)
	}
	if err := api.putValues(ctx, id, a1.QuoteSheet(firstSheet)+"!A8:B10", [][]any{
		{"Umberly merged", ""},
		{"Skerry", ""},
		{"", 1234.5},
	}); err != nil {
		return fmt.Errorf("fill the annotated rows: %w", err)
	}
	if err := api.decorate(ctx, id, 0); err != nil {
		return fmt.Errorf("add a merge, a validation rule and a number format: %w", err)
	}
	owner, err := api.about(ctx)
	if err != nil {
		return fmt.Errorf("read the signed-in account: %w", err)
	}
	line("filled A1:D6 with invented data, a formula column and a note on A2")
	line("added a merge on A8:C8, a validation rule on A9 and a currency format on B10")

	defer func() {
		sec("Cleanup")
		if keep {
			line("keeping %s at your request; trash it when you are done", redact(id))
			return
		}
		if err := api.trash(ctx, id); err != nil {
			// Expected, and said plainly rather than swallowed: this
			// server asks for drive.readonly on purpose, and a
			// read-only Drive scope cannot trash a file.
			line("could not trash the scratch spreadsheets: %v", err)
			line("THIS RUN LEFT FILES BEHIND, and it is expected: the server asks for drive.readonly,")
			line("and trashing needs a write-capable Drive scope.")
			// Every file this driver makes carries the same prefix, so
			// the cleanup is one search rather than a hunt. Deciding
			// this rather than reusing one spreadsheet across runs is
			// deliberate: a driver that carries state between runs is a
			// driver whose results can be explained by the last run.
			line("Find them all in Drive with this search, and trash them:")
			line("    %s", scratchTitle)
			return
		}
		line("trashed the scratch spreadsheet")
	}()

	session, stop, err := startServer(ctx, bin, profile)
	if err != nil {
		return err
	}
	defer stop()

	d := &driver{ctx: ctx, session: session, spreadsheet: id, firstSheet: firstSheet,
		secondSheet: secondSheet, secondSheetID: secondID, title: title, owner: owner,
		since: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}
	d.runAll()

	sec("Coverage")
	coverErr := d.coverage()

	sec("Result")
	line("%d step(s), %d failed, %d undetermined", d.steps, d.failed, d.undetermined)
	if coverErr != nil {
		line("")
		for _, l := range strings.Split(coverErr.Error(), "\n") {
			line("%s", l)
		}
	}
	if d.undetermined > 0 {
		line("")
		line("An undetermined step is not a pass. This run could not tell a slow index")
		line("from a broken one; re-run against the same account before concluding either.")
	}
	if coverErr != nil {
		return coverErr
	}
	if d.failed > 0 {
		return fmt.Errorf("%d step(s) did not behave as expected", d.failed)
	}
	line("Read the transcript above before calling this phase done. A driver that")
	line("only reports success can be wrong about every result it printed.")
	return nil
}

func tokenSource(ctx context.Context, profile string) (oauth2.TokenSource, error) {
	uc, err := userconfig.Load(profile)
	if err != nil {
		return nil, fmt.Errorf("%w (run `google-sheets-mcp login` first)", err)
	}
	secret := uc.ClientSecretPath
	if secret == "" {
		if secret, err = userconfig.DefaultClientSecretPath(profile); err != nil {
			return nil, err
		}
	}
	cfg, err := auth.LoadClientSecret(secret, auth.Scopes(false))
	if err != nil {
		return nil, err
	}
	tokenPath, err := userconfig.TokenFilePath(profile)
	if err != nil {
		return nil, err
	}
	store := &credentials.Store{
		Profile: profile, Keyring: credentials.OSKeyring(), FilePath: tokenPath,
		Warn: func(m string) { line("warning: %s", m) },
	}
	refresh, _, err := store.Resolve()
	if err != nil {
		return nil, err
	}
	return auth.TokenSource(ctx, cfg, refresh, 60*time.Second), nil
}

// startServer runs the built binary and connects to it as a client, the
// way a real host does.
func startServer(ctx context.Context, bin, profile string) (*mcp.ClientSession, func(), error) {
	cmd := exec.CommandContext(ctx, bin)
	// The destructive tools are registered for the run: they are part of
	// the surface and a driver that could not call them would leave the
	// two most dangerous tools untested.
	cmd.Env = append(cmd.Environ(),
		"GSHEETS_PROFILE="+profile, "GSHEETS_LOG_LEVEL=warn", "GSHEETS_ENABLE_DESTRUCTIVE=true")
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "livesheet", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", bin, err)
	}
	return session, func() { _ = session.Close() }, nil
}

// seedRows is the driver's own data. Invented words and figures nobody
// works with, so a transcript is safe to paste into a commit message.
func seedRows() [][]any {
	return [][]any{
		{"Plimth", "Nardle", "Grivet", "Oblisk"},
		{"Quorbin-01", 243.4, 419.79, "=B2+C2"},
		{"Vandel-02", 268.12, 149.62, "=B3+C3"},
		{"Skerry-03", 701.7, 903.63, "=B4+C4"},
		{"Umberly-04", 149.2, 59.6, "=B5+C5"},
		{"Zephrin-05", 0, 0, "=B6/C6"},
	}
}

// step is one call through the server.
type step struct {
	name string
	// why says what this proves. A step without one is a step nobody
	// will know how to read six months later.
	why  string
	tool string
	args map[string]any
	// expectError is the class the call must refuse with, or "" for a
	// call that must succeed. A success where a refusal was expected is
	// a failure, which is how two of three wrong results were caught in
	// the project this rule comes from.
	expectError string
	// check reads the result and says what is wrong with it, if
	// anything. This is the half that catches a call which succeeded and
	// told the truth about nothing.
	check func(text string, structured map[string]any) error
}

type driver struct {
	ctx           context.Context
	session       *mcp.ClientSession
	spreadsheet   string
	firstSheet    string
	secondSheet   string
	secondSheetID int
	title         string
	owner         string
	since         string
	continueFrom  int
	// What the write steps produce and later ones need: the second
	// spreadsheet create_spreadsheet made, the sheet this run writes in,
	// and a checkpoint from a read.
	created    string
	workSheet  string
	checkpoint string
	steps      int
	failed     int
	// undetermined counts steps the world would not let this run
	// settle — Drive's full-text index has not caught up, say. They are
	// neither passes nor failures: reporting one as a pass hides a
	// broken search, and reporting it as a failure makes the driver red
	// for something the server did not do. So it is a third number, and
	// a human reads it.
	undetermined int
	// sent is every (tool, option) pair that reached the server.
	sent map[string]map[string]bool
}

// schemaOptions reads the property names out of a tool's input schema.
// The SDK carries it as `any`, which over the wire is decoded JSON.
func schemaOptions(schema any) []string {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(props))
	for name := range props {
		out = append(out, name)
	}
	return out
}

// coverage checks what the run actually exercised against the surface
// the server actually publishes, and is the authoritative half of the
// pair: the gate in scripts/gates reads the source and runs without
// credentials, this reads the wire.
func (d *driver) coverage() error {
	res, err := d.session.ListTools(d.ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	tools := make([]livecover.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		tools = append(tools, livecover.Tool{Name: t.Name, Options: schemaOptions(t.InputSchema)})
	}
	if len(tools) == 0 {
		return fmt.Errorf("the server published no tools; this check is not looking at one")
	}
	report := livecover.Check(d.sent, tools)
	line("%s", livecover.Summary(report))
	for _, e := range report.Excused {
		line("  undrivable: %s", e)
	}
	return livecover.Err(report)
}

func (d *driver) runAll() {
	sec("get_spreadsheet")
	d.run(d.cardSteps()...)
	sec("read_range")
	d.run(d.readSteps()...)
	sec("find_in_spreadsheet")
	d.run(d.findSteps()...)
	sec("search_spreadsheets")
	d.run(d.searchSteps()...)
	d.searchWithIndexingLag()
	d.writeAll()
}

func (d *driver) run(steps ...step) {
	for _, s := range steps {
		d.steps++
		text, structured, err := d.call(s.tool, s.args)
		switch {
		case err != nil:
			d.fail(s, "the call itself failed: %v", err)
			continue
		case s.expectError != "" && !strings.HasPrefix(text, "["+s.expectError+"]"):
			d.fail(s, "expected a [%s] refusal, got: %s", s.expectError, firstLine(text))
			continue
		case s.expectError == "" && strings.HasPrefix(text, "["):
			d.fail(s, "expected success, got a refusal: %s", firstLine(text))
			continue
		}
		if err := runCheck(s, text, structured); err != nil {
			d.fail(s, "%v", err)
			continue
		}
		d.pass(s, text)
	}
}

// runCheck runs one step's check and turns a panic in it into a failed
// step.
//
// A panicking check is a defect in the driver rather than in the server,
// and it should be loud — but it should not cost the forty live steps
// after it. A run against a real account is expensive to repeat, and an
// unchecked type assertion in one check took a whole one down.
func runCheck(s step, text string, structured map[string]any) (err error) {
	if s.check == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the check itself panicked, which is a bug in this driver: %v", r)
		}
	}()
	return s.check(text, structured)
}

func (d *driver) pass(s step, text string) {
	line("ok   %s", s.name)
	line("     why: %s", s.why)
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		line("     | %s", l)
	}
}

func (d *driver) fail(s step, format string, args ...any) {
	d.failed++
	line("FAIL %s", s.name)
	line("     why: %s", s.why)
	line("     "+format, args...)
}

// call runs one tool and returns the text half and the structured half.
// Both, because the whole point of §4.9 is that a client may show either
// one, and a driver that read only one of them would not notice the
// other going empty.
func (d *driver) call(tool string, args map[string]any) (string, map[string]any, error) {
	// Recorded here rather than read out of the source, because a step
	// can exist and never run — sitting in a slice nobody passes to run,
	// or behind a condition that was false. This is what was actually
	// sent.
	if d.sent == nil {
		d.sent = map[string]map[string]bool{}
	}
	if d.sent[tool] == nil {
		d.sent[tool] = map[string]bool{}
	}
	for k := range args {
		d.sent[tool][k] = true
	}
	res, err := d.session.CallTool(d.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if text.Len() == 0 {
		return "", nil, errors.New("the result had no text content, so a text-only client would see nothing")
	}
	var structured map[string]any
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return "", nil, err
		}
		if err := json.Unmarshal(raw, &structured); err != nil {
			return "", nil, err
		}
	} else if !res.IsError {
		return "", nil, errors.New("the result had no structured content, so a structure-only client would see nothing")
	}
	return text.String(), structured, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
