//go:build live

// Command evals drives a model through this server's tools alone and
// scores what it did.
//
// The live driver proves the tools work. This proves they can be used —
// which is a different claim, and the one that fails quietly. §13: what
// no driver catches is a result that is internally consistent and wrong,
// and the way to catch it is to give the work to something that does not
// know how the server is built and watch which way it goes.
//
// So every task is scored twice. The **end state** is read back through
// this server, because a model's account of what it did is the least
// reliable thing in the run. The **trace** is scored because a task can
// be completed by a model that guessed a range and was lucky: guessing
// "Sheet1" and being refused, then reading the card and succeeding, is a
// pass by outcome and a failure by every rule this server is built on.
//
// The spreadsheet is created and filled here, so nothing a transcript
// carries is anybody's data (§9.1).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scratchTitle is the prefix every spreadsheet this makes carries, so
// the manual cleanup is one Drive search. Same reason as the live
// driver's: this server asks for drive.readonly on purpose, and trashing
// needs a write-capable Drive scope (§17a.1).
const scratchTitle = "evals scratch"

// toolPrefix is how the CLI names this server's tools in a trace.
const toolPrefix = "mcp__gsheets__"

func main() {
	bin := flag.String("bin", "./google-sheets-mcp", "the built server")
	model := flag.String("model", "sonnet", "which model to drive")
	only := flag.String("only", "", "run one task by name")
	budget := flag.Float64("budget", 5.0, "stop if the run would cost more than this many dollars")
	keep := flag.Bool("keep", true, "leave the scratch spreadsheet behind (drive.readonly cannot trash it)")
	flag.Parse()

	if err := run(context.Background(), *bin, *model, *only, *budget, *keep); err != nil {
		fmt.Fprintf(os.Stderr, "\nevals: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, bin, model, only string, budget float64, keep bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("the claude CLI is not on PATH, and it is what drives the model: %w", err)
	}
	// The prompts are checked before anything is created, against a
	// fixture whose fields are filled with placeholders of this
	// harness's own. A table with an unsubstituted {name} in it would
	// otherwise cost a scratch spreadsheet that drive.readonly cannot
	// trash, ten minutes, and — in the sibling run this rule comes from
	// — nothing at all, because it passed.
	//
	// The comment said this before the code did: the check ran after
	// build. Found by a review pass reading the two against each other.
	if err := CheckPrompts(Tasks(checkFixture)); err != nil {
		return err
	}
	fixture, cleanup, err := build(ctx, bin)
	if err != nil {
		return err
	}
	if keep {
		defer func() {
			sec("Cleanup")
			line("this run leaves %q behind; drive.readonly cannot trash it.", fixture.Title)
			line("find every one of them with:  name contains '%s'", scratchTitle)
		}()
	} else {
		defer cleanup()
	}

	// And again against the real one, because a field the fixture failed
	// to fill would substitute an empty string and look substituted.
	tasks := Tasks(fixture)
	if err := CheckPrompts(tasks); err != nil {
		return err
	}
	cfg, err := writeMCPConfig(bin)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(cfg) }()

	h, closeSession, err := scorer(ctx, bin)
	if err != nil {
		return err
	}
	defer closeSession()

	var results []Result
	for _, t := range tasks {
		if only != "" && !strings.EqualFold(only, t.Name) {
			continue
		}
		results = append(results, score(ctx, h, t, cfg, model, budget))
	}
	results = append(results, abTest(ctx, cfg, model, budget, fixture))
	report(results)
	for _, r := range results {
		if r.Failed {
			return errors.New("some tasks failed; the transcript above says which and why")
		}
	}
	return nil
}

// Result is one scored task.
type Result struct {
	Task string
	// Failed is set when a check the run could make came back wrong. A
	// check the run could not make is Unverified instead, and never a
	// failure: a task that fails for ever teaches nobody anything.
	Failed     bool
	Problems   []string
	Unverified string
	Calls      int
	Cost       float64
	Note       string
}

// score runs one task and applies both halves.
func score(ctx context.Context, h *harness, t Task, cfg, model string, budget float64) Result {
	sec(t.Name)
	line("why: %s", t.Why)
	r := drive(ctx, t, cfg, model, budget)
	res := Result{Task: t.Name, Calls: len(r.Calls), Cost: r.Cost, Unverified: t.Unverifiable}
	if r.Err != nil {
		res.Failed, res.Problems = true, []string{fmt.Sprintf("the run did not complete: %v", r.Err)}
		return res
	}
	line("%d tool call(s), %d turn(s)", len(r.Calls), r.Turns)
	for _, c := range r.Calls {
		outcome := "ok"
		if c.IsError {
			outcome = "refused: " + firstLine(c.Result)
		}
		line("  %-24s %s", c.Tool, outcome)
	}

	if t.MaxCalls > 0 && len(r.Calls) > t.MaxCalls {
		res.Problems = append(res.Problems,
			fmt.Sprintf("%d tool calls, over this task's ceiling of %d", len(r.Calls), t.MaxCalls))
	}
	if t.Trace != nil {
		if err := t.Trace(r); err != nil {
			res.Problems = append(res.Problems, "trace: "+err.Error())
		}
	}
	if t.EndState != nil {
		if err := t.EndState(h, r); err != nil {
			res.Problems = append(res.Problems, "end state: "+err.Error())
		}
	}
	res.Failed = len(res.Problems) > 0
	for _, p := range res.Problems {
		line("  FAIL %s", p)
	}
	// Printed, always, and not only when something failed. A task that
	// scored one half and silently skipped the other is the shape this
	// rule exists to stop.
	if res.Unverified != "" {
		line("  UNVERIFIED %s", res.Unverified)
	}
	if !res.Failed {
		line("  pass")
	}
	return res
}

// drive runs the CLI once and parses the stream into a Run.
func drive(ctx context.Context, t Task, cfg, model string, budget float64) *Run {
	r := &Run{Task: t.Name}
	args := []string{
		"-p", t.Prompt,
		"--output-format", "stream-json", "--verbose",
		"--mcp-config", cfg,
		// Only this server's tools. An eval that let the model reach
		// for a shell would be scoring the shell.
		"--strict-mcp-config",
		"--allowed-tools", toolPrefix + "*",
		"--disallowed-tools", "Bash,Read,Write,Edit,WebFetch,WebSearch",
		"--model", model,
		"--max-budget-usd", fmt.Sprintf("%.2f", budget),
		"--permission-mode", "acceptEdits",
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		r.Err = err
		return r
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		r.Err = err
		return r
	}
	parse(bufio.NewScanner(out), r)
	if err := cmd.Wait(); err != nil && r.Err == nil {
		// The CLI's own stderr, or the failure is a bare exit status
		// with the explanation discarded.
		r.Err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return r
}

// parse reads the stream-json events this harness cares about.
func parse(sc *bufio.Scanner, r *Run) {
	// A single event can carry a whole tool result, which is larger than
	// the scanner's default line budget.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	// Indexes into r.Calls rather than copies of them. Keeping a
	// *ToolCall beside a value copy meant a result had to be matched
	// back by scanning for "same tool, no result yet" — which attributes
	// the wrong result to the wrong call the moment a model issues two
	// calls to one tool in parallel, and a trace check reading IsError
	// would then score a refusal as a success.
	pending := map[string]int{}
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					Text      string          `json:"text"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
					IsError   bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
			Result    string  `json:"result"`
			NumTurns  int     `json:"num_turns"`
			TotalCost float64 `json:"total_cost_usd"`
			IsError   bool    `json:"is_error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant", "user":
			for _, c := range ev.Message.Content {
				switch c.Type {
				case "tool_use":
					if !strings.HasPrefix(c.Name, toolPrefix) {
						continue
					}
					call := ToolCall{Tool: strings.TrimPrefix(c.Name, toolPrefix)}
					_ = json.Unmarshal(c.Input, &call.Args)
					r.Calls = append(r.Calls, call)
					pending[c.ID] = len(r.Calls) - 1
				case "tool_result":
					i, ok := pending[c.ToolUseID]
					if !ok {
						continue
					}
					r.Calls[i].Result = flatten(c.Content)
					// The CLI's own flag, or this server's class prefix.
					// A tool result carrying "[blocked] …" is a refusal
					// whether or not the client marked it as one, and
					// several tasks are scored on exactly that.
					r.Calls[i].IsError = c.IsError || strings.HasPrefix(r.Calls[i].Result, "[")
				}
			}
		case "result":
			r.Text, r.Turns, r.Cost = ev.Result, ev.NumTurns, ev.TotalCost
			if ev.IsError && ev.Subtype != "" && ev.Subtype != "success" {
				r.Err = fmt.Errorf("the CLI ended with %s", ev.Subtype)
			}
		}
	}
	if err := sc.Err(); err != nil && r.Err == nil {
		r.Err = fmt.Errorf("reading the model's output: %w", err)
	}
}

// flatten pulls the text out of a tool result, whichever shape it came
// in: the CLI sends a bare string for some and a content array for
// others, and a harness that read only one would score every refusal as
// a success.
func flatten(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return string(raw)
	}
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String()
}

// writeMCPConfig writes the config the CLI loads this server from.
func writeMCPConfig(bin string) (string, error) {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return "", err
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"gsheets": map[string]any{
			"command": abs,
			"args":    []string{"serve"},
			// Destructive tools stay off. An eval that could delete a
			// sheet would be scoring the model's restraint rather than
			// the server's, and §10 makes that a deployment choice.
			"env": map[string]string{"GSHEETS_LOG_LEVEL": "error"},
		},
	}}
	f, err := os.CreateTemp("", "evals-mcp-*.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if err := json.NewEncoder(f).Encode(cfg); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// scorer opens a session of this harness's own, for reading end states
// back. Separate from the model's session on purpose: the model's is
// gone by the time a task is scored.
func scorer(ctx context.Context, bin string) (*harness, func(), error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "evals", Version: "0"}, nil)
	cmd := exec.CommandContext(ctx, bin, "serve")
	cmd.Stderr = os.Stderr
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the server for scoring: %w", err)
	}
	h := &harness{call: func(tool string, args map[string]any) (string, map[string]any, error) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return "", nil, err
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		if res.IsError {
			return text.String(), nil, fmt.Errorf("%s", strings.TrimSpace(text.String()))
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
		}
		return text.String(), structured, nil
	}}
	return h, func() { _ = session.Close() }, nil
}

func report(results []Result) {
	sec("Results")
	var failed, unverified int
	var cost float64
	for _, r := range results {
		cost += r.Cost
		switch {
		case r.Failed:
			failed++
			line("  FAIL  %-40s %d call(s)", r.Task, r.Calls)
		default:
			line("  pass  %-40s %d call(s)", r.Task, r.Calls)
		}
		if r.Note != "" {
			line("        %s", r.Note)
		}
		if r.Unverified != "" {
			unverified++
			line("        unverified: %s", r.Unverified)
		}
	}
	line("")
	line("%d task(s), %d failed, %d carrying a half nothing could check. $%.2f.",
		len(results), failed, unverified, cost)
	line("")
	line("Read this transcript rather than the counts. A task can pass by outcome and fail by every")
	line("rule this server is built on, and the trace lines above are where that shows.")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// sec prints a section header through line, not around it. Routing it
// directly would make the transcript gate's allowlist a list of
// functions permitted to reach the terminal rather than a claim that
// everything reaching it is redacted — and a later sec(sheetTitle) would
// then be blessed by name.
func sec(title string) {
	line("")
	line("== %s %s", title, strings.Repeat("=", max(0, 54-len(title))))
}

func line(format string, args ...any) {
	fmt.Println(redactLine(fmt.Sprintf(format, args...)))
}
