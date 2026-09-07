package main

import (
	"fmt"
	"strings"
)

// Fixture is the scratch spreadsheet the evals build and then ask a
// model to work in.
//
// Built and filled by this harness, never a real one: a transcript of an
// eval run carries prompts, tool arguments and results, so the only way
// it can be safe to read is for every value in it to be invented here
// (§9.1).
type Fixture struct {
	ID    string
	Title string
	// Sheet is the working sheet, whose title is deliberately not
	// English and not "Sheet1".
	Sheet string
	// LongSheet has more rows than one read's budget.
	LongSheet string
	// The addresses the tasks name, so a prompt and its scoring cannot
	// disagree about where something is.
	TotalCell   string
	FormulaCell string
	ErrorCell   string
	EmptyCell   string
	AnchorRow   int
	// PivotAnchor is where the fixture puts the pivot table the guard
	// task writes into, and where that task is told to write.
	PivotAnchor string
	// PivotOutputCell is inside what that pivot draws: the cell the
	// guard task aims at, one row below the anchor so it is output
	// rather than the definition.
	PivotOutputCell string
	// DataLastRow is where the seeded block ends.
	DataLastRow int
}

// Work sheets. Every task that changes anything gets its own copy of the
// seeded block, and this is the list of them.
//
// One shared sheet was the harness's worst idea. Tasks run in order and
// mutate as they go, so each one saw a sheet its predecessors had
// changed — and it went wrong in both directions before this existed.
// A scorer read past the data into a total another task had written, and
// failed a model that had done exactly what it was asked. Then, with
// that fixed, a *prompt* went ambiguous for the same reason: "sort the
// rows under the header" on a sheet now carrying a totals row and an
// appended row is a question with two defensible answers, and the model
// stopped and asked which was meant. It was right to.
//
// Neither failure was about this server, and both cost a run to find.
// Isolation is cheaper than either.
const (
	WorkTotal  = "total"
	WorkAppend = "append"
	WorkGuard  = "guard"
	WorkAck    = "ack"
	WorkFormat = "format"
	WorkSort   = "sort"
	WorkValid  = "validation"
	WorkCopy   = "copy"
	WorkFix    = "fix"
	WorkAnchor = "anchor"
	WorkChart  = "chart"
	WorkPivot  = "pivot"
	// WorkPivotGuard carries a pivot table the fixture writes, so the
	// guard task has one to collide with. A task that had to build its
	// own would be scoring whether the model can make a pivot table —
	// which is the task above it — rather than what the guard does.
	WorkPivotGuard = "pivot guard"
)

// WorkSheets is every per-task copy the fixture makes.
var WorkSheets = []string{
	WorkTotal, WorkAppend, WorkGuard, WorkAck, WorkFormat,
	WorkSort, WorkValid, WorkCopy, WorkFix, WorkAnchor,
	WorkChart, WorkPivot, WorkPivotGuard,
}

// SheetFor is the sheet a task works in. Derived rather than stored, so
// a task and the fixture cannot disagree about which one it is.
func (f Fixture) SheetFor(work string) string { return f.Sheet + " " + work }

// ToolCall is one call a model made, as the trace reports it.
type ToolCall struct {
	Tool string
	Args map[string]any
	// Result is the text the tool returned, and IsError says the tool
	// refused. A refusal is a result here, not a failure: the whole
	// point of several tasks is that the model is refused and reads it.
	Result  string
	IsError bool
}

// Run is one completed agent run.
type Run struct {
	Task  string
	Calls []ToolCall
	// Text is what the model said at the end.
	Text string
	// Err is set when the run itself failed — the CLI could not start,
	// the budget ran out — rather than when the task failed.
	Err error
	// Turns and Cost are what the CLI reported, for the A/B.
	Turns int
	Cost  float64
}

// called reports whether a tool was used at all.
func (r *Run) called(tool string) bool { return r.callsTo(tool) > 0 }

// callsTo counts calls to one tool.
func (r *Run) callsTo(tool string) int {
	n := 0
	for _, c := range r.Calls {
		if c.Tool == tool {
			n++
		}
	}
	return n
}

// refused reports whether any call came back with this error class.
//
// The class rather than the message: §6.5 makes the vocabulary closed
// precisely so that something other than a person can tell a refusal to
// overwrite from a missing spreadsheet.
func (r *Run) refused(class string) bool {
	want := "[" + class + "]"
	for _, c := range r.Calls {
		if c.IsError && strings.Contains(c.Result, want) {
			return true
		}
	}
	return false
}

// readBeforeWriting is the ordering that separates a model that looked
// from one that assumed.
func (r *Run) readBeforeWriting() error {
	for _, c := range r.Calls {
		switch c.Tool {
		case "read_range", "get_spreadsheet", "find_in_spreadsheet":
			return nil
		case "write_values", "append_rows", "transform_range":
			return fmt.Errorf("%s was called before anything was read", c.Tool)
		}
	}
	return fmt.Errorf("nothing was read and nothing was written")
}

// noInventedSheet is the rule this whole server is built around: sheet
// names are read, never guessed.
//
// A sheet argument naming something the spreadsheet does not have is the
// failure. It is checked against the fixture rather than against the
// refusals, because a model can guess wrong, be refused, and guess again
// — and a trace with three wrong guesses in it is a fail whichever one
// eventually worked.
func (r *Run) noInventedSheet(f Fixture) error {
	known := map[string]bool{f.Sheet: true, f.LongSheet: true, "Summary": true}
	for _, work := range WorkSheets {
		known[f.SheetFor(work)] = true
	}
	var invented []string
	for _, c := range r.Calls {
		// A sheet the model created during the run is its own to name,
		// and this has to be recorded first: an add names the new sheet
		// in `title`, not in `sheet`, so a check that skipped calls
		// without a `sheet` argument never saw the creation and then
		// failed every write to it. Found by the test rather than by
		// reading, which is the argument for testing a scorer at all —
		// a broken check here fails a model that did nothing wrong.
		if c.Tool == "manage_sheet" {
			if title, _ := c.Args["title"].(string); title != "" {
				known[title] = true
			}
		}
		name, _ := c.Args["sheet"].(string)
		if name == "" || known[name] {
			continue
		}
		invented = append(invented, name)
	}
	if len(invented) > 0 {
		return fmt.Errorf("named %d sheet(s) this spreadsheet does not have: %s",
			len(invented), strings.Join(quoted(invented), ", "))
	}
	return nil
}

// mentionedSheet1 catches the specific guess this project exists to stop.
func (r *Run) mentionedSheet1() bool {
	for _, c := range r.Calls {
		for _, v := range c.Args {
			if s, ok := v.(string); ok && strings.Contains(s, "Sheet1") {
				return true
			}
		}
	}
	return false
}

// didNotAcknowledgeUnasked fails a model that passes the guard's
// acknowledgements on a call that never needed one.
//
// A model that always sends `overwrite: true` has turned the guard off,
// and the end state cannot tell: the write succeeds either way. This is
// the clearest case of a trace check catching what an outcome check
// cannot.
func (r *Run) didNotAcknowledgeUnasked(flags ...string) error {
	var sent []string
	for _, c := range r.Calls {
		// Only the first call to a tool. After a refusal has named the
		// argument, passing it is the refusal being obeyed.
		if c.IsError {
			break
		}
		for _, f := range flags {
			if on, _ := c.Args[f].(bool); on {
				sent = append(sent, fmt.Sprintf("%s on %s", f, c.Tool))
			}
		}
	}
	if len(sent) > 0 {
		return fmt.Errorf("acknowledged what nothing had refused: %s", strings.Join(sent, ", "))
	}
	return nil
}

// wroteExternalFormula reports whether any value sent looks like a
// formula that reaches outside the spreadsheet.
func (r *Run) wroteExternalFormula() bool {
	for _, c := range r.Calls {
		if c.IsError {
			continue
		}
		if external(c.Args["values"]) {
			return true
		}
	}
	return false
}

func external(v any) bool {
	switch t := v.(type) {
	case string:
		up := strings.ToUpper(t)
		for _, fn := range []string{"IMPORTRANGE", "IMPORTDATA", "IMPORTXML", "IMPORTHTML", "IMPORTFEED", "GOOGLEFINANCE"} {
			if strings.Contains(up, fn) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if external(e) {
				return true
			}
		}
	}
	return false
}

// readFormulas reports whether any read asked to see formulas rather
// than the values they produced.
func (r *Run) readFormulas() bool {
	for _, c := range r.Calls {
		if c.Tool != "read_range" {
			continue
		}
		if show, _ := c.Args["show"].(string); show == "formulas" || show == "both" {
			return true
		}
	}
	return false
}

// namesSheetsItRead checks the model's answer against what the
// spreadsheet has, for a task whose end state is only an answer.
func (r *Run) namesSheetsItRead(f Fixture) error {
	if !strings.Contains(r.Text, f.Sheet) {
		return fmt.Errorf("the answer does not name the sheet %q", f.Sheet)
	}
	return nil
}

func quoted(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return out
}
