//go:build live

package main

import (
	"context"
	"fmt"
)

// abTest measures what §4.9 currently only argues.
//
// §4.9 says every read shows addresses, so a model can write back to
// what it just read without counting rows. That is reasoning, not
// measurement: the number behind the original rule was measured on
// *prose* reads in a sibling project on one version of one client, and a
// grid is a different claim from a paragraph.
//
// So the same task is run twice, over the same spreadsheet, differing
// only in what the read hands back. Both arms end in a write that names
// an address, because that is where counting rows goes wrong — a read
// that is pleasant to look at and produces a write to the wrong row has
// answered the wrong question.
//
// What this is not: a modified server. The plan describes toggling the
// rendering inside the structured half, which would mean a build flag
// existing only for this. `format=json` gives the same thing through the
// tool surface — rows and no addresses in the text half — and costs
// nothing to keep. It is a weaker instrument than the plan's and it is
// the one that does not put test-only machinery in the server; where it
// differs is recorded rather than smoothed over.
func abTest(ctx context.Context, cfg, model string, budget float64, f Fixture) Result {
	sec("A/B: does the addressed grid save tool calls?")
	line("why: §4.9 is reasoning rather than measurement, and this is the measurement")

	ask := func(how string) *Run {
		return drive(ctx, Task{
			Name: "a/b " + how,
			Prompt: fmt.Sprintf("In the spreadsheet %s, on the sheet %q, find the row whose column A value is "+
				"Threnody-03 and write the word Checked into column F of that same row. %s", f.ID, f.Sheet, how),
			MaxCalls: 20,
		}, cfg, model, budget)
	}

	// Arm A: the server's own default, the addressed grid.
	withGrid := ask("Read whatever you need first.")
	// Arm B: the same work with rows and no addresses in front of it.
	withRows := ask("When you read, pass format=json so you get the raw rows.")

	res := Result{Task: "A/B: addressed grid against bare rows"}
	if withGrid.Err != nil || withRows.Err != nil {
		res.Failed = true
		res.Problems = []string{fmt.Sprintf("an arm did not complete: grid=%v rows=%v", withGrid.Err, withRows.Err)}
		line("  FAIL %s", res.Problems[0])
		return res
	}
	res.Calls = len(withGrid.Calls)
	res.Cost = withGrid.Cost + withRows.Cost

	// Both arms have to have landed on the same cell, or the comparison
	// is between a right answer and a wrong one rather than between two
	// renderings.
	gridCell := wroteTo(withGrid)
	rowsCell := wroteTo(withRows)
	line("  addressed grid: %d call(s), wrote to %s", len(withGrid.Calls), orNone(gridCell))
	line("  bare rows:      %d call(s), wrote to %s", len(withRows.Calls), orNone(rowsCell))

	switch {
	case gridCell == "" || rowsCell == "":
		res.Note = "one arm never wrote anything, so this run compares a completed task with an abandoned one"
		res.Unverified = "the call-count comparison: an arm that did not finish cannot be counted against one that did"
	case gridCell != rowsCell:
		res.Note = fmt.Sprintf("the arms disagree about which row that is: %s against %s. That is the finding, "+
			"and it is a stronger one than a call count", gridCell, rowsCell)
	default:
		res.Note = fmt.Sprintf("both arms wrote to %s; the difference is %d call(s) in favour of %s",
			gridCell, abs(len(withGrid.Calls)-len(withRows.Calls)), cheaper(withGrid, withRows))
	}
	line("  %s", res.Note)
	line("")
	line("  One run of two arms settles nothing on its own. What it is for is the direction and the")
	line("  disagreement: if the arms write to different rows, the rendering is doing more than saving calls.")
	return res
}

// wroteTo is the range the run's last successful write named.
func wroteTo(r *Run) string {
	out := ""
	for _, c := range r.Calls {
		if c.IsError || c.Tool != "write_values" {
			continue
		}
		if rng, _ := c.Args["range"].(string); rng != "" {
			out = rng
		}
	}
	return out
}

func cheaper(a, b *Run) string {
	if len(a.Calls) <= len(b.Calls) {
		return "the addressed grid"
	}
	return "bare rows"
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
