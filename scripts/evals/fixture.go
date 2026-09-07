//go:build live

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// build makes the spreadsheet the tasks work in, through this server's
// own tools.
//
// Through the server rather than around it, for two reasons. It is one
// fewer credential path to maintain, and a fixture that cannot be built
// through the tool surface is a fixture describing a spreadsheet the
// tools cannot make — which would be worth knowing before scoring a
// model on it.
//
// Everything in it is invented here. §9.1: an eval transcript carries
// prompts, arguments and results, so the only way it can be safe to read
// is for none of it to be anybody's.
func build(ctx context.Context, bin string) (Fixture, func(), error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "evals-setup", Version: "0"}, nil)
	cmd := exec.CommandContext(ctx, bin, "serve")
	cmd.Stderr = os.Stderr
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return Fixture{}, nil, fmt.Errorf("connect to the server to build the fixture: %w", err)
	}
	closeSession := func() { _ = session.Close() }

	call := func(tool string, args map[string]any) (map[string]any, error) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return nil, err
		}
		if res.IsError {
			var text strings.Builder
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text.WriteString(tc.Text)
				}
			}
			return nil, fmt.Errorf("%s: %s", tool, strings.TrimSpace(text.String()))
		}
		out, _ := res.StructuredContent.(map[string]any)
		return out, nil
	}

	f := Fixture{
		Title: fmt.Sprintf("%s %s", scratchTitle, time.Now().UTC().Format("2006-01-02 15:04:05")),
		// Not English and not Sheet1. A model that guesses is meant to
		// be caught here, and it cannot be caught on a sheet whose name
		// it could have guessed.
		Sheet:       "Ürväl",
		LongSheet:   "Marrowfen long",
		TotalCell:   "B22",
		FormulaCell: "D2",
		ErrorCell:   "D21",
		EmptyCell:   "F2",
		AnchorRow:   10,
		DataLastRow: 21,
		// Clear of the seeded block in A:D, so the pivot's own output is
		// the only thing in those columns and a write into it means one
		// thing.
		PivotAnchor:     "F1",
		PivotOutputCell: "G3",
	}

	sec("Fixture")
	created, err := call("create_spreadsheet", map[string]any{
		"title": f.Title, "sheets": []any{f.Sheet, f.LongSheet},
	})
	if err != nil {
		closeSession()
		return Fixture{}, nil, err
	}
	f.ID, _ = created["spreadsheet"].(string)
	if f.ID == "" {
		closeSession()
		return Fixture{}, nil, fmt.Errorf("create_spreadsheet returned no id")
	}
	// Registered before anything else is printed, so the id never
	// reaches the terminal even in an error from the next call.
	reg(f.ID, "<spreadsheet>")
	line("created %q", f.Title)

	// Twenty rows of invented names and numbers, a formula column, and
	// one formula that fails — the error cell a task has to diagnose by
	// reading the formula rather than the value.
	rows := [][]any{{"Plimth", "Nardle", "Grivet", "Oblisk"}}
	for i := range 20 {
		name := fmt.Sprintf("%s-%02d", vocabulary[i%len(vocabulary)], i+1)
		b := 100 + (i*137)%900
		c := 50 + (i*89)%400
		rows = append(rows, []any{name, b, c, fmt.Sprintf("=B%d+C%d", i+2, i+2)})
	}
	if _, err := call("write_values", map[string]any{
		"spreadsheet": f.ID, "sheet": f.Sheet, "range": "A1:D21", "values": rows,
	}); err != nil {
		closeSession()
		return Fixture{}, nil, err
	}
	// The error cell: a division by a cell holding zero, so the fault is
	// in the formula and invisible in the value it produced.
	//
	// Written left to right. "E21:D21" was the first spelling, which A1
	// normalises to D21:E21 — so the zero and the formula landed the
	// wrong way round, the formula referred to itself, and the task told
	// the model to diagnose an error in a cell holding a plain 0.
	if _, err := call("write_values", map[string]any{
		"spreadsheet": f.ID, "sheet": f.Sheet, "range": f.ErrorCell + ":E21",
		"values":             [][]any{{"=C21/E21", 0}},
		"overwrite":          true,
		"overwrite_formulas": true,
	}); err != nil {
		closeSession()
		return Fixture{}, nil, err
	}
	line("filled %s with 20 rows, a formula column and one failing formula", f.Sheet)

	// One copy per task that changes anything, so no task can see
	// another's writes. Duplicated rather than re-seeded: the copy is
	// the same block by construction, where twenty more write_values
	// calls would be twenty more chances for the sheets to differ.
	for _, work := range WorkSheets {
		if _, err := call("manage_sheet", map[string]any{
			"spreadsheet": f.ID, "action": "duplicate",
			"sheet": f.Sheet, "title": f.SheetFor(work),
		}); err != nil {
			closeSession()
			return Fixture{}, nil, err
		}
	}
	line("duplicated it into %d work sheets, one per task that writes", len(WorkSheets))

	// A sheet longer than one read's default budget, so the tail task
	// has a tail to find.
	long := make([][]any, 0, 900)
	for i := range 900 {
		long = append(long, []any{fmt.Sprintf("row %d", i+1), (i * 7) % 1000})
	}
	if _, err := call("write_values", map[string]any{
		"spreadsheet": f.ID, "sheet": f.LongSheet, "range": "A1:B900", "values": long,
	}); err != nil {
		closeSession()
		return Fixture{}, nil, err
	}
	line("filled %s with 900 rows, past a default read's budget", f.LongSheet)

	// A pivot table for the guard task to collide with, on its own work
	// sheet. Anchored at F1, clear of the seeded block in A:D, so the
	// output it draws is the only thing in those columns and a write
	// into it is unambiguous.
	//
	// The fixture builds it rather than a task, because the task under
	// test is what the guard does when a write lands on a pivot's
	// output — not whether the model can make one, which is the task
	// before it.
	if _, err := call("manage_pivot_table", map[string]any{
		"spreadsheet": f.ID, "sheet": f.SheetFor(WorkPivotGuard), "action": "add",
		"anchor": f.PivotAnchor, "source": "A1:D21",
		"group_rows": []any{"A"}, "values": []any{"B sum"},
	}); err != nil {
		closeSession()
		return Fixture{}, nil, err
	}
	line("anchored a pivot table at %s on %q, for the write guard to refuse", f.PivotAnchor, f.SheetFor(WorkPivotGuard))

	return f, closeSession, nil
}

// vocabulary is invented and deliberately unlike anything real, so a
// value in a transcript cannot be mistaken for somebody's data.
var vocabulary = []string{
	"Quorbin", "Skerry", "Threnody", "Marrowfen", "Vandel",
	"Bractal", "Trennow", "Yalmic", "Umberly", "Zephrin",
}
