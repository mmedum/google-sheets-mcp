//go:build live

package main

import (
	"fmt"
	"strings"
	"time"
)

// The write steps. Every one of them changes the scratch spreadsheet the
// driver made and filled, so nothing here touches anybody's data.
//
// They run in sequence rather than as one slice, because each batch
// needs what the last one produced: the spreadsheet create_spreadsheet
// made, the sheet manage_sheet added, the checkpoint a read returned.
func (d *driver) writeAll() {
	sec("create_spreadsheet")
	d.run(d.createSteps()...)
	sec("manage_sheet")
	d.run(d.sheetSteps()...)
	sec("write_values")
	d.run(d.writeSteps()...)
	d.checkpointSteps()
	sec("append_rows")
	d.run(d.appendSteps()...)
	// Phase 2 before the dimension steps: those insert and delete rows,
	// which would move the band the transforms assert about.
	d.formatAll()
	sec("edit_dimensions")
	d.run(d.dimensionSteps()...)
	sec("delete_dimensions")
	d.run(d.deleteDimensionSteps()...)
	// Phase 3's anchors, on a sheet of their own: they insert, sort and
	// delete rows to prove an anchor survives all three, which would
	// move every band the steps above assert about.
	d.anchorAll()
	sec("clear_values")
	d.run(d.clearSteps()...)
	sec("delete_sheet")
	d.run(d.deleteSteps()...)
}

// scratchTitle is the prefix every file this driver creates carries, so
// the manual cleanup is one Drive search rather than a hunt. The driver
// cannot trash what it makes: this server asks for drive.readonly on
// purpose (§17.6), and trashing needs a write-capable Drive scope.
const scratchTitle = "livesheet scratch"

func (d *driver) createSteps() []step {
	return []step{
		{
			name: "a dry run creates nothing",
			why:  "a preview that created a file would be a preview that wrote",
			tool: "create_spreadsheet",
			args: map[string]any{
				"title":   scratchTitle + " preview only",
				"values":  [][]any{{"Plimth", "Nardle"}},
				"dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing")
				}
				if id, _ := s["spreadsheet"].(string); id != "" {
					return fmt.Errorf("a dry run returned a spreadsheet id, so it made one")
				}
				return nil
			},
		},
		{
			name: "a new spreadsheet, seeded",
			why:  "the seed goes through the ordinary write path, so input means what it means everywhere else",
			tool: "create_spreadsheet",
			args: map[string]any{
				"title":     scratchTitle + " second " + time.Now().UTC().Format("15:04:05"),
				"sheets":    []any{"Grivet", "Oblisk"},
				"tsv":       "Plimth\tNardle\n007\tOblisk",
				"input":     "literal",
				"locale":    "en_GB",
				"time_zone": "Etc/UTC",
			},
			check: func(text string, s map[string]any) error {
				id, _ := s["spreadsheet"].(string)
				if id == "" {
					return fmt.Errorf("no spreadsheet id came back")
				}
				d.created = id
				reg(id, "<second-spreadsheet>")
				// A sheets list replaces Google's default sheet rather
				// than adding to it, verified live. Two asked for, two
				// expected, and no third.
				sheets, _ := s["sheets"].([]any)
				if len(sheets) != 2 {
					return fmt.Errorf("the new spreadsheet has %d sheet(s), and exactly two were asked for", len(sheets))
				}
				if first, _ := sheets[0].(string); first != "Grivet" {
					return fmt.Errorf("the first sheet is %q, and the list asked for Grivet first", first)
				}
				// literal input stores what it was given, so the leading
				// zero survives. Under typed it would have become 7.
				if coerced, _ := s["coerced"].([]any); len(coerced) != 0 {
					return fmt.Errorf("literal input was coerced: %v", coerced)
				}
				if !strings.Contains(text, "Created") {
					return fmt.Errorf("the summary does not carry the card")
				}
				return nil
			},
		},
	}
}

func (d *driver) sheetSteps() []step {
	const added = "Livesheet writes"
	return []step{
		{
			name: "add a sheet to work in",
			why:  "every later write needs a sheet whose contents this run owns",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "title": added,
				"index": 1, "rows": 200, "cols": 12,
			},
			check: func(_ string, s map[string]any) error {
				title, _ := s["sheet"].(string)
				if title != added {
					return fmt.Errorf("add produced %q", title)
				}
				d.workSheet = title
				return nil
			},
		},
		{
			name: "a dry run changes nothing",
			why:  "the preview and the result describe the same act, so they come from the same code",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "rename", "sheet": added,
				"title": "Livesheet renamed", "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing")
				}
				return nil
			},
		},
		{
			name: "duplicate, hide, unhide and reorder",
			why:  "duplicate is what delete_sheet's description offers instead of deleting",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "duplicate", "sheet": added, "title": "Livesheet copy",
			},
			check: func(_ string, s map[string]any) error {
				if title, _ := s["sheet"].(string); title != "Livesheet copy" {
					return fmt.Errorf("duplicate produced %q", title)
				}
				return nil
			},
		},
		{
			name: "hide the copy",
			why:  "hiding keeps the sheet and takes it out of the way, which deleting does not",
			tool: "manage_sheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "hide", "sheet": "Livesheet copy"},
		},
		{
			name: "unhide it again",
			why:  "the pair is one setting, and a tool that can only set it one way is half a tool",
			tool: "manage_sheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "unhide", "sheet": "Livesheet copy"},
		},
		{
			name: "move it to the end",
			why:  "index is zero-based, and a reorder that lands where the sheet already was proves nothing",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "reorder", "sheet": "Livesheet copy", "index": 3,
			},
			check: func(_ string, s map[string]any) error {
				sheets, _ := s["sheets"].([]any)
				if len(sheets) != 4 {
					return fmt.Errorf("the result lists %d sheet(s), and the spreadsheet has 4", len(sheets))
				}
				// Read back rather than assumed: a duplicate lands at
				// the front, so a move to position 0 would have been a
				// no-op that passed.
				if last, _ := sheets[3].(string); last != "Livesheet copy" {
					return fmt.Errorf("after a move to position 3 the last sheet is %q", last)
				}
				return nil
			},
		},
		{
			name: "grow the working sheet",
			why:  "a write past the end is refused with the size, so growing is how a caller answers that",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "resize", "sheet": added, "rows": 300, "cols": 14,
			},
		},
		{
			name:        "shrinking is refused, and says where removing lives",
			why:         "a resize that shrinks takes the data on the rows it removes, and this tool is not the destructive one",
			tool:        "manage_sheet",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "action": "resize", "sheet": added, "rows": 2},
			expectError: "blocked",
		},
		{
			name: "freeze the heading row and the first column",
			why:  "zero unfreezes, so both counts are always in the mask",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "freeze", "sheet": added, "rows": 1, "cols": 1,
			},
		},
		{
			name: "colour the tab",
			why:  "the API wants three floats and a person has a hex colour",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "tab_color", "sheet": added, "colour": "#4a90d9",
			},
		},
		{
			name: "copy the sheet into the other spreadsheet",
			why:  "copy_to is the one action that writes to a second file, and it has its own endpoint",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "copy_to", "sheet": added, "destination": d.created,
			},
			check: func(_ string, s map[string]any) error {
				if title, _ := s["sheet"].(string); title == "" {
					return fmt.Errorf("copy_to did not name the copy")
				}
				return nil
			},
		},
	}
}

func (d *driver) writeSteps() []step {
	return []step{
		{
			name: "a write into empty cells, with the coercion reported",
			why:  "typed input rewrites what it is given, and the caller is told rather than protected",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1",
				"values": [][]any{{"Plimth", "007", "2026-09-05"}},
			},
			check: func(text string, s map[string]any) error {
				if rng, _ := s["range"].(string); !strings.HasSuffix(rng, "A1:C1") {
					return fmt.Errorf("a single anchor cell and three values wrote %q, want A1:C1", rng)
				}
				coerced, _ := s["coerced"].([]any)
				if len(coerced) != 2 {
					return fmt.Errorf("%d coercion(s) from 007 and a date: %v", len(coerced), coerced)
				}
				// The date is the case that needs the pair: 46270 alone
				// reads as data loss.
				if !strings.Contains(text, "displayed") {
					return fmt.Errorf("the summary does not pair the stored serial with what the cell shows")
				}
				if cp, _ := s["checkpoint"].(string); !strings.HasPrefix(cp, "ck_") {
					return fmt.Errorf("no checkpoint on a write")
				}
				return nil
			},
		},
		{
			name:        "the same write again is refused",
			why:         "the guard is the reason this server exists, and it names the cells and the way through",
			tool:        "write_values",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1", "values": [][]any{{"Vandel"}}},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "A1") || !strings.Contains(text, "overwrite") {
					return fmt.Errorf("the refusal names neither the cell nor the argument: %s", text)
				}
				return nil
			},
		},
		{
			name: "a dry run says what is there and sends nothing",
			why:  "dry_run is what stands in for a suggestion mode Sheets does not have",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1",
				"values": [][]any{{"Vandel"}}, "overwrite": true, "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") || !strings.Contains(text, "non-empty") {
					return fmt.Errorf("the preview does not describe the target: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, the write goes through",
			why:  "the guard refuses until it is told to allow, and then it allows",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1",
				"values": [][]any{{"Vandel"}}, "overwrite": true,
			},
		},
		{
			name: "bulk text, stored exactly as sent",
			why:  "literal is the only correct answer for a product code, and it is never substituted",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A10",
				"tsv": "Quorbin-01\t007\nSkerry-02\t008", "input": "literal",
			},
			check: func(_ string, s map[string]any) error {
				if coerced, _ := s["coerced"].([]any); len(coerced) != 0 {
					return fmt.Errorf("literal input was coerced: %v", coerced)
				}
				return nil
			},
		},
		{
			name: "a formula is created and named",
			why:  "a formula and its result render identically, so the result says which cells hold one",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "D1", "values": [][]any{{"=1+2"}},
			},
			check: func(_ string, s map[string]any) error {
				f, _ := s["formulas_created"].([]any)
				if len(f) != 1 {
					return fmt.Errorf("a write of =1+2 reported %v as formulas", f)
				}
				return nil
			},
		},
		{
			name: "overwrite alone will not replace a formula",
			why:  "the formula case loses work invisibly, so it is acknowledged separately",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "D1",
				"values": [][]any{{float64(3)}}, "overwrite": true,
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "overwrite_formulas") {
					return fmt.Errorf("the refusal does not name the second acknowledgement: %s", text)
				}
				return nil
			},
		},
		{
			name: "both acknowledgements replace it",
			why:  "two refusals for one write would be a tool nobody could use",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "D1",
				"values": [][]any{{float64(3)}}, "overwrite": true, "overwrite_formulas": true,
			},
		},
		{
			name: "a formula that reaches outside is refused",
			why:  "IMPORTRANGE pulls another spreadsheet's data in and embeds its id",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "F1",
				"values": [][]any{{d.importFormula()}},
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "allow_external_formulas") {
					return fmt.Errorf("the refusal does not name the way through: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, it is written and named",
			why:  "the gate is an acknowledgement, not a ban: the caller decides and is told what they wrote",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "F1",
				"values": [][]any{{d.importFormula()}}, "allow_external_formulas": true,
			},
			check: func(_ string, s map[string]any) error {
				if f, _ := s["formulas_created"].([]any); len(f) != 1 {
					return fmt.Errorf("the IMPORTRANGE was not reported as a formula: %v", f)
				}
				return nil
			},
		},
		{
			// An IMPORTRANGE at a spreadsheet nobody has authorised
			// shows #REF!, so this cell holds a formula that evaluated
			// to an error. A guard that tested the cell's kind treated
			// it as an ordinary value and let overwrite alone replace
			// it; a live transcript is where that showed up.
			name: "a formula that errored still needs overwrite_formulas",
			why:  "a broken formula is still a formula, and replacing one with a value loses it just the same",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "F1",
				"values": [][]any{{"Nardle"}}, "overwrite": true,
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "overwrite_formulas") {
					return fmt.Errorf("a formula showing an error was replaceable with overwrite alone: %s", text)
				}
				return nil
			},
		},
		{
			name: "a range wider than the values leaves the rest alone",
			why:  "values.update skips cells it has no value for rather than clearing them",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A20:C22",
				"values": [][]any{{"Trennow"}}, "overwrite": true,
			},
			check: func(text string, s map[string]any) error {
				tail, _ := s["tail_left"].(string)
				if tail == "" {
					return fmt.Errorf("the result does not say what the write left behind")
				}
				if !strings.Contains(text, "skips cells") {
					return fmt.Errorf("the summary does not explain the tail: %s", text)
				}
				return nil
			},
		},
	}
}

// importFormula is an IMPORTRANGE at the other spreadsheet this run
// made. It reaches outside this spreadsheet, which is what the gate is
// about, and it reaches nowhere this run does not already own.
func (d *driver) importFormula() string {
	return fmt.Sprintf("=IMPORTRANGE(%q,%q)", d.created, "Grivet!A1")
}

// checkpointSteps run apart from the rest because the second one needs
// the checkpoint the first returned.
func (d *driver) checkpointSteps() {
	sec("checkpoints")
	d.run(step{
		name: "a read hands back a checkpoint",
		why:  "it is what a write compares against, and it is over a rectangle rather than the spreadsheet",
		tool: "read_range",
		args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1"},
		check: func(_ string, s map[string]any) error {
			cp, _ := s["checkpoint"].(string)
			if !strings.HasPrefix(cp, "ck_") {
				return fmt.Errorf("no checkpoint on the read")
			}
			d.checkpoint = cp
			return nil
		},
	})
	if d.checkpoint == "" {
		return
	}
	d.run(
		step{
			name: "a matching checkpoint lets the write through",
			why:  "the guard narrows the window between reading and writing; it has to open as well as close",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"values":    [][]any{{"Nardle", "Grivet", "Oblisk"}},
				"overwrite": true, "overwrite_formulas": true, "expect_checkpoint": d.checkpoint,
			},
		},
		step{
			name: "the same checkpoint is now stale",
			why:  "the cells changed under it, which is exactly what it exists to catch",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"values":    [][]any{{"Yalmic", "Bractal", "Umberly"}},
				"overwrite": true, "overwrite_formulas": true, "expect_checkpoint": d.checkpoint,
			},
			expectError: "conflict",
		},
	)
}

func (d *driver) appendSteps() []step {
	return []step{
		{
			name: "rows land after the block Google finds",
			why:  "the range picks the block and Google picks the destination; both are reported because neither is predictable",
			tool: "append_rows",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"values": [][]any{{"Zephrin", "12", "13"}},
			},
			check: func(text string, s map[string]any) error {
				table, _ := s["table_range"].(string)
				landed, _ := s["range"].(string)
				if table == "" || landed == "" {
					return fmt.Errorf("the append did not say where it looked or where it landed")
				}
				if !strings.Contains(text, "moved down") {
					return fmt.Errorf("an inserting append did not say addresses moved")
				}
				return nil
			},
		},
		{
			name:        "overwrite needs acknowledging",
			why:         "the destination is decided during the call, so the server cannot read it first",
			tool:        "append_rows",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1", "values": [][]any{{"x"}}, "insert": "overwrite"},
			expectError: "blocked",
		},
		{
			name: "acknowledged, it overwrites what follows",
			why:  "the two insert modes differ in what they destroy, and the caller chooses",
			tool: "append_rows",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"tsv": "Vandel\t21\t22", "input": "typed", "insert": "overwrite", "overwrite": true,
			},
			check: func(text string, s map[string]any) error {
				if shifted, _ := s["rows_below_shifted"].(bool); shifted {
					return fmt.Errorf("an overwriting append claimed rows moved")
				}
				if !strings.Contains(text, "Appended") {
					return fmt.Errorf("the summary does not say what happened: %s", text)
				}
				return nil
			},
		},
		{
			name: "a dry run says it cannot name the destination",
			why:  "a preview that named a destination it cannot know would be a preview that guessed",
			tool: "append_rows",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"values": [][]any{{"Skerry"}}, "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "cannot name the destination") {
					return fmt.Errorf("the preview overpromises: %s", text)
				}
				return nil
			},
		},
		{
			name: "an appended formula that reaches outside",
			why:  "the gate applies wherever values are written, not only where a rectangle is named",
			tool: "append_rows",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"values": [][]any{{d.importFormula()}}, "allow_external_formulas": true,
			},
		},
	}
}

// The args maps below are written out rather than built by a helper.
// The coverage gate reads them from the syntax tree, so an option folded
// into a shared map is an option the gate cannot see — and a gate that
// cannot see an option is a gate that will not notice the day somebody
// adds one and forgets the driver.
func (d *driver) dimensionSteps() []step {
	return []step{
		{
			name: "insert rows, taking the formatting from above",
			why:  "inheritFromBefore cannot be true at the very start, so the request is built rather than passed through",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "insert", "dimension": "rows", "band": "2:3", "inherit": true,
			},
			check: func(text string, s map[string]any) error {
				if shifted, _ := s["addresses_shifted"].(bool); !shifted {
					return fmt.Errorf("an insert did not report that addresses moved")
				}
				if !strings.Contains(text, "no longer points") {
					return fmt.Errorf("the summary does not warn that a held address is now wrong")
				}
				return nil
			},
		},
		{
			// Marked first. The previous version of this step moved two
			// rows the inserts above had left empty, so reading the
			// destination back found blank cells and the check passed
			// on the row gutters alone — a step that tested nothing.
			name: "mark the rows a move will carry",
			why:  "a move of empty rows lands anywhere and looks correct, whatever the conversion did",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A2:A3",
				"values": [][]any{{"MovedFirst"}, {"MovedSecond"}}, "overwrite": true, "overwrite_formulas": true,
			},
		},
		{
			name: "move a band to where it was asked for",
			why:  "the API reads destinationIndex against the sheet before the move, so to must mean where it ends up",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "move", "dimension": "rows", "band": "2:3", "to": 8,
			},
		},
		{
			name: "and the marked rows are at row 8",
			why:  "a move's result cannot show what it did, and the whole question is which row the band landed on",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A7:A10", "show": "values",
			},
			check: func(text string, _ map[string]any) error {
				for _, line := range strings.Split(text, "\n") {
					if !strings.Contains(line, "MovedFirst") {
						continue
					}
					if strings.HasPrefix(strings.TrimSpace(line), "8 |") {
						return nil
					}
					return fmt.Errorf("the band asked for row 8 landed here instead: %q", strings.TrimSpace(line))
				}
				return fmt.Errorf("the moved rows are not in A7:A10 at all:\n%s", text)
			},
		},
		{
			name: "set a column width",
			why:  "pixels is the API's unit and there is no other",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "resize", "dimension": "columns", "band": "B:C", "pixels": 140,
			},
		},
		{
			name: "size columns to their contents",
			why:  "this is the one people want after a write, and it is why the tool is not called insert_rows",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "auto_resize", "dimension": "columns", "band": "A:D",
			},
		},
		{
			name: "group rows",
			why:  "a group is a pair of operations and a tool that can only make one is half a tool",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "group", "dimension": "rows", "band": "12:14",
			},
		},
		{
			name: "ungroup them again",
			why:  "the other half",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "ungroup", "dimension": "rows", "band": "12:14",
			},
		},
		{
			name: "a dry run says what it would do and does nothing",
			why:  "the preview and the result describe the same act, so they come from the same code",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "auto_resize", "dimension": "columns", "band": "A:B", "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing: %s", text)
				}
				return nil
			},
		},
		{
			name: "rows with a column band is refused",
			why:  "either reading would move somebody's data somewhere they did not ask for",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"action": "insert", "dimension": "rows", "band": "B:D",
			},
			expectError: "invalid",
		},
	}
}

func (d *driver) deleteDimensionSteps() []step {
	return []step{
		{
			name: "put something on the rows a delete will take",
			why:  "a delete over empty rows counts zero, and a zero that is always right tests nothing",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:B31",
				"values": [][]any{{"Yalmic", "=2+2"}, {"Bractal", float64(9)}}, "overwrite": true,
			},
		},
		{
			name: "a delete says what it would take, and takes nothing",
			why:  "deleting a row takes its data and nothing in Sheets brings it back",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"dimension": "rows", "band": "30:31", "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing: %s", text)
				}
				if cells, _ := s["cells"].(float64); cells != 4 {
					return fmt.Errorf("the preview counted %v cell(s), and four are on those rows", cells)
				}
				return nil
			},
		},
		{
			name: "and is refused without confirm",
			why:  "the count comes before the question, so the confirmation is informed",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "dimension": "rows", "band": "30:31",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "4 non-empty") {
					return fmt.Errorf("the refusal does not count what is on the rows: %s", text)
				}
				return nil
			},
		},
		{
			name: "confirmed, it deletes",
			why:  "a gate that never opens is a tool nobody can use",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet,
				"dimension": "rows", "band": "30:31", "confirm": true,
			},
		},
	}
}

func (d *driver) clearSteps() []step {
	return []step{
		{
			// The first run of these steps cleared an empty rectangle
			// and reported "0 non-empty cells" three times, which is
			// true and proves nothing: the count is the only thing a
			// clear's result says, so it is what has to be exercised.
			name: "put something there to clear",
			why:  "a clear over empty cells reports zero, and a zero that is always right tests nothing",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A40:B41",
				"values": [][]any{{"Quorbin", "=1+1"}, {"Skerry", float64(2)}}, "overwrite": true,
			},
		},
		{
			name: "a dry run counts what is there",
			why:  "a clear's result is emptiness, so the count is the only thing that says what happened",
			tool: "clear_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A40:B41", "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing: %s", text)
				}
				cells, _ := s["cells"].(float64)
				formulas, _ := s["formulas"].(float64)
				if cells != 4 || formulas != 1 {
					return fmt.Errorf("the preview counted %v cell(s) and %v formula(s), and four cells with one formula are there",
						cells, formulas)
				}
				return nil
			},
		},
		{
			name: "without confirm it refuses",
			why:  "Sheets cannot undo a clear",
			tool: "clear_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A40:B41",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "4 non-empty") {
					return fmt.Errorf("the refusal does not count what is there: %s", text)
				}
				return nil
			},
		},
		{
			name: "confirmed, it clears the values and keeps the rest",
			why:  "the API keeps formatting, notes and validation rules, and the result says so",
			tool: "clear_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A40:B41", "confirm": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "left alone") {
					return fmt.Errorf("the summary does not say what survives a clear: %s", text)
				}
				if cells, _ := s["cells"].(float64); cells != 4 {
					return fmt.Errorf("the clear reported %v cell(s), and four were there", cells)
				}
				return nil
			},
		},
	}
}

func (d *driver) deleteSteps() []step {
	return []step{
		{
			name: "a dry run counts what would go with it",
			why:  "the count is what makes the confirmation informed rather than a formality",
			tool: "delete_sheet",
			// The working sheet rather than the copy: the copy was
			// duplicated before anything was written to it, so deleting
			// it counted zero of everything and the count — the whole
			// point of the confirmation — went untested.
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "dry_run": true},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "cannot undo") {
					return fmt.Errorf("the preview does not say it is irreversible: %s", text)
				}
				if cells, _ := s["cells"].(float64); cells < 1 {
					return fmt.Errorf("the preview counted %v cell(s) on a sheet this run filled", cells)
				}
				return nil
			},
		},
		{
			name:        "without confirm it refuses and offers duplicate",
			why:         "the safer route is named in the refusal, where somebody will read it",
			tool:        "delete_sheet",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "duplicate") {
					return fmt.Errorf("the refusal does not offer the safer route: %s", text)
				}
				return nil
			},
		},
		{
			name: "confirmed, the sheet goes",
			why:  "and the result lists what is left, so the next call can name one",
			tool: "delete_sheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "confirm": true},
			check: func(_ string, s map[string]any) error {
				if cells, _ := s["cells"].(float64); cells < 1 {
					return fmt.Errorf("the deletion reported %v cell(s) on a sheet this run filled", cells)
				}
				sheets, _ := s["sheets"].([]any)
				for _, sh := range sheets {
					if title, _ := sh.(string); title == d.workSheet {
						return fmt.Errorf("the deleted sheet is still listed")
					}
				}
				return nil
			},
		},
	}
}
