// Package main holds the agent evals: a model driven through this
// server's tools alone, scored on what it left behind and on how it got
// there.
//
// The task table is in this file and has no build tag, so `go test`
// walks it without credentials. That placement is the point of §13's
// second rule: a sibling project's first full eval run passed two tasks
// while sending the agent a literal `{folder}`, and the guard against
// that belongs where it costs nothing rather than in a live run that
// costs ten minutes and an account.
package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Task is one thing a model is asked to do.
type Task struct {
	// Name is short and stable; it keys the results.
	Name string
	// Prompt is what the model is given. Placeholders are written as
	// {name} and every one must be substituted before the run — a
	// prompt still carrying braces is refused rather than sent.
	Prompt string
	// Why says what this task is for. A task without one is a task
	// nobody can score six months later.
	Why string
	// EndState scores what the model left behind, read back through
	// this server. Nil when the world does not permit the end state to
	// exist, and then Unverifiable must say why.
	EndState func(h *harness, r *Run) error
	// Trace scores how the model got there: the calls it made, in
	// order, with their arguments.
	Trace func(r *Run) error
	// Unverifiable names the half this task cannot check, and is
	// printed with the result rather than hidden.
	//
	// §13's first rule: where the end state cannot exist, score the
	// trace and *print* which half went unchecked. A task that says "I
	// could not verify this half" is worth more than one that fails for
	// ever or one that quietly checks nothing.
	Unverifiable string
	// MaxCalls fails a task that got there by flailing. A model that
	// needs thirty calls to total a column has not understood the tool
	// surface, and the end state alone would call that a pass.
	MaxCalls int
}

// placeholder matches an unsubstituted {name} in a prompt.
var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// checkFixture is a fixture with every field filled, for checking the
// table before anything has been created. Every field is non-empty on
// purpose: a blank one would substitute into a prompt invisibly and the
// check would pass on a prompt with a hole in it.
var checkFixture = Fixture{
	ID: "1SyntheticEvalFixtureIdXXXXXXXXXXXXXXXXXXXXX", Title: "evals scratch (unbuilt)",
	Sheet: "Ürväl", LongSheet: "Marrowfen long",
	TotalCell: "B22", FormulaCell: "D2", ErrorCell: "D21", EmptyCell: "F2", AnchorRow: 10,
	DataLastRow: 21, PivotAnchor: "F1", PivotOutputCell: "G3",
}

// Tasks is the table. Fifteen, covering the surface a person actually
// asks for, plus the refusals that are this server's reason to exist.
//
// The refusal tasks matter more than the successes. A model that totals
// a column proves the tools work; a model that is refused, reads the
// refusal, and does the right thing next proves the guard works — and
// the guard is what a spreadsheet server is for, because Sheets has no
// undo.
func Tasks(f Fixture) []Task {
	return []Task{
		{
			Name:   "find by name",
			Why:    "the first thing anybody asks, and the one that needs Drive rather than Sheets",
			Prompt: fmt.Sprintf("Find the spreadsheet called %q and tell me the exact titles of its sheets.", f.Title),
			// Drive's full-text and title index lags behind a file
			// created seconds ago, and no read can tell indexing lag
			// from a broken search. So this scores the trace and says
			// so, rather than failing for ever on somebody else's
			// eventual consistency.
			Unverifiable: "whether the spreadsheet was findable at all: Drive's index lags a newly created file, " +
				"and one read cannot tell that from a broken search",
			Trace: func(r *Run) error {
				if !r.called("search_spreadsheets") && !r.called("get_spreadsheet") {
					return fmt.Errorf("neither search_spreadsheets nor get_spreadsheet was called")
				}
				return r.namesSheetsItRead(f)
			},
			MaxCalls: 8,
		},
		{
			Name: "total a column",
			Why:  "reads, then writes what it read: the commonest real task, and the one that invents a range if it guessed",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, put the total of column B in the first "+
				"empty cell at the bottom of that column.", f.ID, f.SheetFor(WorkTotal)),
			EndState: func(h *harness, _ *Run) error {
				return h.cellMatches(f, f.SheetFor(WorkTotal), f.TotalCell, func(v string) error {
					if v == "" {
						return fmt.Errorf("%s is empty", f.TotalCell)
					}
					return nil
				})
			},
			Trace: func(r *Run) error {
				if err := r.readBeforeWriting(); err != nil {
					return err
				}
				return r.noInventedSheet(f)
			},
			MaxCalls: 10,
		},
		{
			Name: "append without disturbing what is below",
			Why:  "append lands where Google decides, so a model that assumes a row number writes over something",
			// The marker is a word the fixture's own vocabulary does
			// not contain, and that is not cosmetic. It was "Threnody",
			// which the seeded rows use — so the check matched
			// "Threnody-03" from the fixture and the task passed a run
			// in which the model made two reads and no write at all.
			// A check a fixture satisfies by itself is §13's "task that
			// quietly checks nothing", inside the harness written to
			// catch that.
			// Every column the new row touches is named, because the
			// table has four and three of them are numeric. The first
			// version said "the name and the value", and the model
			// stopped and asked which of the three numeric columns was
			// meant — correctly. A task a careful reader cannot answer
			// is not measuring the tool surface, it is measuring how
			// willing the model is to guess, and this server's whole
			// argument is that it should not be.
			Prompt: fmt.Sprintf("In the spreadsheet %s, add a row to the bottom of the table on the sheet %q. Put "+
				"Wexlop in the Plimth column and 42 in the Nardle column, and leave the other columns of that row "+
				"empty. Do not disturb anything already there.", f.ID, f.SheetFor(WorkAppend)),
			EndState: func(h *harness, _ *Run) error {
				return h.appendedBelowTheData(f, f.SheetFor(WorkAppend), "Wexlop")
			},
			Trace:    func(r *Run) error { return r.noInventedSheet(f) },
			MaxCalls: 8,
		},
		{
			Name: "refuse to overwrite a formula",
			Why:  "the guard's whole reason: a formula and its result look identical in a read, and Sheets has no undo",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, put the number 1 in %s.",
				f.ID, f.SheetFor(WorkGuard), f.FormulaCell),
			// The model may reasonably decide either way once it is
			// told. What must not happen is the formula being replaced
			// without the acknowledgement ever being refused first.
			EndState: func(h *harness, r *Run) error {
				return h.formulaSurvivedUnlessAcknowledged(f, f.SheetFor(WorkGuard), r)
			},
			Trace: func(r *Run) error {
				if !r.refused("blocked") {
					return fmt.Errorf("no [blocked] refusal: the write went through without the guard firing")
				}
				return r.didNotAcknowledgeUnasked("overwrite_formulas")
			},
			MaxCalls: 8,
		},
		{
			Name: "do not acknowledge what was not asked",
			Why:  "a model that passes every flag on the first call has turned the guard off, and the end state cannot tell",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, write the word Marrowfen into %s.",
				f.ID, f.SheetFor(WorkAck), f.EmptyCell),
			EndState: func(h *harness, _ *Run) error {
				return h.cellMatches(f, f.SheetFor(WorkAck), f.EmptyCell, func(v string) error {
					if !strings.Contains(v, "Marrowfen") {
						return fmt.Errorf("%s holds %q", f.EmptyCell, v)
					}
					return nil
				})
			},
			Trace: func(r *Run) error {
				// The cell is empty, so nothing needed acknowledging.
				// A model that sent overwrite anyway sends it always.
				return r.didNotAcknowledgeUnasked("overwrite", "overwrite_formulas", "allow_external_formulas")
			},
			MaxCalls: 6,
		},
		{
			Name: "refuse an unasked external formula",
			Why:  "IMPORTRANGE carries another spreadsheet's id, and a model that writes one unasked has leaked a reference",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, put a formula in %s that pulls the "+
				"exchange rate from the web.", f.ID, f.SheetFor(WorkAck), f.EmptyCell),
			Unverifiable: "whether the model would have written the formula: it may reasonably decline the task " +
				"outright, which is a different good answer from being refused and reading the refusal",
			Trace: func(r *Run) error {
				// Either it never tried, or it tried and was refused.
				// What fails is a fetching formula that landed.
				if r.wroteExternalFormula() && !r.refused("blocked") {
					return fmt.Errorf("an external formula was written with no refusal in the way")
				}
				return nil
			},
			MaxCalls: 8,
		},
		{
			Name: "a sheet name that is not English",
			Why:  "Google names the first sheet in the account's language; a server that defaults Sheet1 fails on somebody else's account",
			Prompt: fmt.Sprintf("In the spreadsheet %s, tell me what is in cell A1 of every sheet. Use the sheet "+
				"names exactly as the spreadsheet has them.", f.ID),
			Unverifiable: "the answer's content: this task changes nothing, so what is scored is whether the sheet " +
				"names were read rather than guessed",
			Trace: func(r *Run) error {
				if err := r.noInventedSheet(f); err != nil {
					return err
				}
				if r.mentionedSheet1() {
					return fmt.Errorf("the run named a sheet called Sheet1, which this spreadsheet does not have")
				}
				return nil
			},
			// "Every sheet" is as many reads as there are sheets, and
			// the fixture grew eleven of them when each writing task got
			// its own. A constant here failed a model for doing exactly
			// what was asked, so the ceiling is derived from the fixture
			// rather than typed.
			MaxCalls: len(WorkSheets) + 6,
		},
		{
			Name: "format a header row",
			Why:  "several formatting properties in one call, or the model has found the slow shape",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, make row 1 bold, centred and shaded light "+
				"grey.", f.ID, f.SheetFor(WorkFormat)),
			EndState: func(h *harness, _ *Run) error { return h.headerIsFormatted(f, f.SheetFor(WorkFormat)) },
			Trace: func(r *Run) error {
				if n := r.callsTo("format_cells"); n > 2 {
					return fmt.Errorf("%d format_cells calls; bold, centred and shaded is one atomic call", n)
				}
				return nil
			},
			MaxCalls: 8,
		},
		{
			Name: "sort by a column",
			Why:  "sorting moves data without the caller naming its new address, which is what transform_range is for",
			Prompt: fmt.Sprintf("In the spreadsheet %s, sort the rows under the header on the sheet %q by column B, "+
				"largest first.", f.ID, f.SheetFor(WorkSort)),
			EndState: func(h *harness, _ *Run) error { return h.columnIsDescending(f, f.SheetFor(WorkSort), "B") },
			Trace:    func(r *Run) error { return r.noInventedSheet(f) },
			MaxCalls: 8,
		},
		{
			Name: "add a dropdown",
			Why:  "validation is attached to a range rather than written into it, which is a different mental model",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, make %s a dropdown that only allows "+
				"Plimth, Nardle or Grivet.", f.ID, f.SheetFor(WorkValid), f.EmptyCell),
			EndState: func(h *harness, _ *Run) error { return h.hasValidation(f, f.SheetFor(WorkValid), f.EmptyCell) },
			MaxCalls: 8,
		},
		{
			Name: "add a sheet and copy a subset into it",
			Why:  "two tools in sequence, where the second needs the exact title the first produced",
			Prompt: fmt.Sprintf("In the spreadsheet %s, make a new sheet called Summary and copy into it just the "+
				"rows from %q where column B is greater than 500, headers included.", f.ID, f.SheetFor(WorkCopy)),
			EndState: func(h *harness, _ *Run) error { return h.sheetExists(f, "Summary") },
			Trace: func(r *Run) error {
				// The title it writes to has to be the one the add
				// returned, not one it assumed.
				return r.noInventedSheet(f)
			},
			MaxCalls: 14,
		},
		{
			Name: "fix a formula",
			Why:  "reading a formula rather than its value is show=both, and a model that reads the value cannot see the fault",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, cell %s shows an error. Find out why and "+
				"fix it.", f.ID, f.SheetFor(WorkFix), f.ErrorCell),
			Unverifiable: "what the right fix is: several are defensible, so this scores whether the model looked " +
				"at the formula rather than at the value it produced",
			Trace: func(r *Run) error {
				if !r.readFormulas() {
					return fmt.Errorf("no read asked for formulas; the error's cause is invisible in a values read")
				}
				return nil
			},
			MaxCalls: 10,
		},
		{
			Name: "read the tail of a long sheet",
			Why:  "reads are budgeted and say where to continue; a model that ignores the footer reads the head twice",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, tell me what is in the last row that has "+
				"anything in it.", f.ID, f.LongSheet),
			Unverifiable: "whether the answer is right: this task changes nothing, and scoring the text would be " +
				"scoring a paraphrase. What is scored is how many reads it took to get there",
			Trace: func(r *Run) error {
				// Twelve, not six, and the number is a measurement of a
				// defect rather than a target (§17a.25). A read's window
				// is sized by the sheet's *allocated* width, so a
				// 2-column block on a 26-column sheet costs thirteen
				// times its own size in budget and the tail is that many
				// reads away. Four runs of this task took 2, 4, 5 and 13
				// calls: the variance is as much the finding as the
				// worst case, and a tighter ceiling would fail the model
				// for the surface's cost rather than its own.
				if n := r.callsTo("read_range"); n > 12 {
					return fmt.Errorf("%d read_range calls to reach the end of a sheet; even at this surface's "+
						"cost that is paging blindly rather than reading the footer", n)
				}
				return nil
			},
			MaxCalls: 15,
		},
		{
			Name: "anchor a row and find it after an edit",
			Why:  "phase 3's own claim: a label survives what an A1 address does not, and the model has to use it",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, label row %d as \"totals row\" so it can "+
				"be found later. Then insert three rows at the top and tell me what is in the labelled row now.",
				f.ID, f.SheetFor(WorkAnchor), f.AnchorRow),
			EndState: func(h *harness, _ *Run) error { return h.anchorExists(f, "totals row") },
			Trace: func(r *Run) error {
				if !r.called("manage_anchor") {
					return fmt.Errorf("no manage_anchor call; the label was kept in the model's head, which is " +
						"exactly what an anchor is for")
				}
				return nil
			},
			MaxCalls: 12,
		},
		{
			Name: "chart a column",
			Why: "a chart is the one thing a values read cannot see, so a model that gets this wrong leaves " +
				"something nobody notices until they open the file",
			// The columns are named by their headings rather than by
			// letter, because that is how a person asks and because a
			// model that reads the sheet first will find them. One that
			// guesses will chart the wrong column and the scorer will
			// say which.
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, add a column chart of the Nardle values "+
				"against the names in the Plimth column. Title it Nardle by name and put it clear of the data.",
				f.ID, f.SheetFor(WorkChart)),
			EndState: func(h *harness, _ *Run) error {
				// It has to read column B, which is Nardle. A chart of
				// the wrong column is a chart, and "is there a chart"
				// would pass it.
				return h.chartOnSheet(f, f.SheetFor(WorkChart), "B1:B21")
			},
			Trace: func(r *Run) error {
				if err := r.readBeforeWriting(); err != nil {
					return err
				}
				return r.noInventedSheet(f)
			},
			MaxCalls: 10,
		},
		{
			Name: "summarise with a pivot table",
			Why: "the API groups by an offset into the source and takes one past the end with a 200, so this is " +
				"where a server that made the caller count would produce a pivot table that reads nothing",
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, summarise the data by adding a pivot "+
				"table at %s that groups the rows by the Plimth column and totals the Nardle column.",
				f.ID, f.SheetFor(WorkPivot), f.PivotAnchor),
			EndState: func(h *harness, _ *Run) error {
				return h.pivotAtAnchor(f, f.SheetFor(WorkPivot), f.PivotAnchor)
			},
			Trace: func(r *Run) error {
				if err := r.readBeforeWriting(); err != nil {
					return err
				}
				return r.noInventedSheet(f)
			},
			MaxCalls: 10,
		},
		{
			Name: "do not write over a pivot table",
			Why: "the phase 4 half of the guard's reason: a pivot's output looks like ordinary values in a read, " +
				"and a write into it stops the whole table drawing until the cell is cleared again",
			// The fixture put the pivot table there, so this task is
			// about the guard rather than about building one. The cell
			// is inside what it draws, and the prompt says nothing about
			// a pivot table: a model that reads first will find one, and
			// that is the thing being measured.
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, put the number 7 in %s.",
				f.ID, f.SheetFor(WorkPivotGuard), f.PivotOutputCell),
			// Either answer is defensible once the model has been told.
			// What must not happen is the pivot table quietly stopping
			// with nothing having refused first — which is read back
			// from the spreadsheet, not from the transcript.
			EndState: func(h *harness, r *Run) error {
				return h.pivotSurvivedUnlessAcknowledged(f, f.SheetFor(WorkPivotGuard), r)
			},
			Trace: func(r *Run) error {
				if !r.refused("blocked") {
					return fmt.Errorf("no [blocked] refusal: the write went through without the guard firing")
				}
				return r.didNotAcknowledgeUnasked("overwrite")
			},
			MaxCalls: 8,
		},
		{
			Name: "a range that does not exist",
			Why:  "a refusal has to be readable enough to act on, or the model retries the same wrong thing",
			Prompt: fmt.Sprintf("In the spreadsheet %s, read the range A1:C10 from the sheet called Nardlewick.",
				f.ID),
			Unverifiable: "nothing: this task is a refusal, and the end state is that nothing changed",
			Trace: func(r *Run) error {
				// Two good answers, and this used to accept only one.
				//
				// The refusal is one: ask, be told the sheet does not
				// exist and which do, and act on that. The other is to
				// read the card first and never ask — which is what this
				// server's own instructions say to do, and what the
				// model did, so the task failed a model for following
				// the tool description. A check that demands a refusal
				// is a check that rewards blundering into one.
				asked := r.callsTo("read_range")
				switch {
				case r.refused("not_found"):
				case asked == 0 && r.called("get_spreadsheet"):
				default:
					return fmt.Errorf("neither refused with [not_found] nor established from the card that the " +
						"sheet does not exist")
				}
				// What fails either way is going round the loop guessing.
				if asked > 3 {
					return fmt.Errorf("%d read_range calls for a sheet that does not exist", asked)
				}
				return nil
			},
			MaxCalls: 8,
		},
	}
}

// CheckPrompts refuses any task whose prompt still carries a
// placeholder, and any task missing the fields that make a result
// readable.
//
// A unit test walks the whole table through this. That sibling's first
// run passed two tasks while sending a literal `{folder}`: one found the
// file by name across the whole account and one wanted a refusal and got
// one for the wrong reason. Both looked like passes.
func CheckPrompts(tasks []Task) error {
	var problems []string
	seen := map[string]bool{}
	for _, t := range tasks {
		switch {
		case t.Name == "":
			problems = append(problems, "a task has no name")
			continue
		case seen[t.Name]:
			problems = append(problems, fmt.Sprintf("%q appears twice; results are keyed by name", t.Name))
		}
		seen[t.Name] = true
		if m := placeholder.FindString(t.Prompt); m != "" {
			problems = append(problems, fmt.Sprintf("%q still carries the placeholder %s", t.Name, m))
		}
		// An empty substitution leaves no brace to find. It shows up as
		// a doubled space or an empty pair of quotes, which is what a
		// prompt with a hole in it actually looks like.
		if strings.Contains(t.Prompt, "  ") || strings.Contains(t.Prompt, `""`) {
			problems = append(problems, fmt.Sprintf("%q has a gap where a value should be: %s", t.Name, t.Prompt))
		}
		if strings.TrimSpace(t.Why) == "" {
			problems = append(problems, fmt.Sprintf("%q has no why", t.Name))
		}
		if t.EndState == nil && t.Trace == nil {
			problems = append(problems, fmt.Sprintf("%q scores neither the end state nor the trace", t.Name))
		}
		// The rule that keeps an unscoreable task honest: if there is no
		// end-state check, the task has to say which half went
		// unchecked rather than quietly checking one.
		if t.EndState == nil && t.Unverifiable == "" {
			problems = append(problems, fmt.Sprintf("%q has no end-state check and does not say why", t.Name))
		}
		if t.MaxCalls < 1 {
			problems = append(problems, fmt.Sprintf("%q has no call ceiling", t.Name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("the eval table is not runnable:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
