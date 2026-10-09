//go:build live

package main

import (
	"fmt"
	"strings"
)

// Phase 2's steps: formatting, the objects attached to a range, and the
// transforms.
//
// They work in a band of the scratch sheet the earlier steps do not
// touch — row 30 and below — so a transform that reorders rows cannot
// make an earlier step's assertion pass or fail for a reason nobody
// chose.
const (
	// bandRow is where the transform steps' own data starts.
	bandRow = 30
	// splitCell holds text with a delimiter in it, for text_to_columns.
	splitCell = "E30"
	// mergeBand is clear of the frozen first row and column, so the
	// merge steps are about what a merge discards rather than about the
	// freeze. The write steps filled A20:C22.
	mergeBand = "B20:C20"
)

func (d *driver) formatAll() {
	sec("format_cells")
	d.run(d.formatSteps()...)
	sec("read_formatting")
	d.run(d.readFormattingSteps()...)
	sec("manage_range")
	d.run(d.rangeSteps()...)
	d.run(d.tableSteps()...)
	d.run(d.typedColumnSteps()...)
	d.commaLocaleAll()
	sec("transform_range")
	d.run(d.transformSteps()...)
}

func (d *driver) formatSteps() []step {
	return []step{
		{
			name: "everything in one call is one batch",
			why:  "a header row that is bold, centered and shaded is one call rather than eight",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"number_format": "text", "bold": true, "italic": true, "underline": true,
				"strikethrough": false, "font_size": 11, "font_family": "Roboto",
				"text_color": "#b7472a", "background": "#d9e2f3",
				"borders": "1pt solid #cccccc", "border_sides": "outer",
				"horizontal": "center", "vertical": "middle", "wrap": "clip",
			},
			check: func(text string, s map[string]any) error {
				applied, _ := s["applied"].([]any)
				if len(applied) < 10 {
					return fmt.Errorf("%d ops reported for a call that set thirteen things: %v", len(applied), applied)
				}
				for _, want := range []string{"bold", "background", "borders", "font"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the result does not name %q: %s", want, text)
					}
				}
				return nil
			},
		},
		{
			// The work sheet froze its first row and column several
			// steps ago, and a merge cannot span that edge. Sheets says
			// so — "You can't merge frozen and non-frozen columns" —
			// without saying where the edge is, which is what this
			// server adds.
			name:        "a merge spanning the frozen edge is refused before it is sent",
			why:         "the freeze was set several calls ago and the caller is not thinking about it",
			tool:        "format_cells",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1", "merge": "all", "overwrite": true},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "frozen") || !strings.Contains(text, "manage_sheet freeze") {
					return fmt.Errorf("the refusal does not name the freeze or how to undo it: %s", text)
				}
				return nil
			},
		},
		{
			// Its own values rather than another step's. The first
			// version of these steps merged a band the write steps
			// happened to fill, and when it turned out to be empty the
			// merge simply succeeded — a step that asserts about values
			// has to put them there.
			name: "seed the band the merge steps work in",
			why:  "a merge discards values, so there have to be values to discard",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": mergeBand,
				"values": [][]any{{"Quorbin", "Vandel"}}, "input": "literal", "overwrite": true,
			},
		},
		{
			name: "a merge that would discard values is refused",
			why:  "Sheets keeps the top-left value of a merge and drops the rest with nothing saying so",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": mergeBand, "merge": "all",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "overwrite") {
					return fmt.Errorf("the refusal does not name the argument that would allow it: %s", text)
				}
				return nil
			},
		},
		{
			name: "a dry run previews the refusal rather than repeating it",
			why:  "a preview is the one call that should always answer, since it sends nothing",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": mergeBand,
				"merge": "all", "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") || !strings.Contains(text, "would be refused") {
					return fmt.Errorf("the preview does not say what would stop it: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, the merge goes through",
			why:  "the guard refuses until it is told to allow, and then it allows",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": mergeBand,
				"merge": "all", "overwrite": true,
			},
		},
		{
			name: "and unmerge takes it apart again",
			why:  "a merge that could not be undone would leave the sheet in a shape no later step could write to",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": mergeBand, "unmerge": true,
			},
		},
		{
			name: "a note is set, then replaced only when told to",
			why:  "a note is invisible in a values read, so replacing one is a loss the caller cannot see coming",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1", "note": "Quorbin check pending",
			},
		},
		{
			name:        "replacing that note is refused",
			why:         "the second note would take the first with it and no read would have shown it",
			tool:        "format_cells",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1", "note": "Vandel now"},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "already has a note") {
					return fmt.Errorf("the refusal does not say what is in the way: %s", text)
				}
				return nil
			},
		},
		{
			name:        "removing a note is guarded too",
			why:         "removing takes the same unseen thing that replacing does, which a review pass settled",
			tool:        "format_cells",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1", "clear_note": true},
			expectError: "blocked",
		},
		{
			name: "acknowledged, the note is removed",
			why:  "the guard refuses until it is told to allow, and then it allows",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1",
				"clear_note": true, "overwrite": true,
			},
		},
		{
			name:        "clearing formatting is refused over cells that carry some",
			why:         "Sheets cannot bring a format back, and the cells here were formatted a moment ago",
			tool:        "format_cells",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "C1", "clear_format": true},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "formatting of its own") {
					return fmt.Errorf("the refusal does not say what would go: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, the clear goes through",
			why:  "and the next step reads the range back to see that it did",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "C1",
				"clear_format": true, "overwrite": true,
			},
		},
	}
}

func (d *driver) readFormattingSteps() []step {
	return []step{
		{
			name: "the formatting that was just set reads back",
			why:  "a driver that only reports success can be wrong about every result it printed",
			tool: "read_formatting",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1"},
			check: func(text string, s map[string]any) error {
				blocks, _ := s["blocks"].([]any)
				if len(blocks) == 0 {
					return fmt.Errorf("no formatting blocks on a range that was formatted two calls ago")
				}
				for _, want := range []string{"bold", "italic", "underline", "#b7472a", "#d9e2f3"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the formatting read does not report %q: %s", want, text)
					}
				}
				// C1 was cleared, so it must not be in a block with the
				// other two. A read that still showed it would mean the
				// clear was reported and not made.
				if strings.Contains(text, "A1:C1 ") {
					return fmt.Errorf("A1:C1 is one block, and C1 was cleared: %s", text)
				}
				return nil
			},
		},
		{
			name: "the cell budget bounds the read and says so",
			why:  "a partial answer that looked whole is the bug the budgets exist to avoid",
			tool: "read_formatting",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C40", "max_cells": 6,
			},
			check: func(text string, s map[string]any) error {
				if truncated, _ := s["truncated"].(bool); !truncated {
					return fmt.Errorf("a 120-cell range under a 6-cell budget did not report itself truncated")
				}
				if !strings.Contains(text, "cell budget stopped this") {
					return fmt.Errorf("the footer does not say the budget cut it: %s", text)
				}
				return nil
			},
		},
	}
}

func (d *driver) rangeSteps() []step {
	const named = "Livesheet_band"
	return append([]step{
		{
			name: "a dry run attaches nothing",
			why:  "a preview that named a range would be a preview that wrote",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"kind": "named_range", "action": "add", "name": named, "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing: %s", text)
				}
				return nil
			},
		},
		{
			name: "a named range is added and deleted",
			why:  "a name is the one attached object a caller can already refer to by name",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"kind": "named_range", "action": "add", "name": named,
			},
		},
		{
			name:        "adding it twice is refused",
			why:         "two ranges with one name is a spreadsheet nothing can refer to unambiguously",
			tool:        "manage_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1", "kind": "named_range", "action": "add", "name": named},
			expectError: "invalid",
		},
		{
			name: "and deleting it leaves the cells",
			why:  "deleting a name must not delete what it named",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"kind": "named_range", "action": "delete", "name": named,
			},
		},
		{
			name: "a protection is added, softened and lifted",
			why:  "a protected range is the only real guarantee this server can offer against a concurrent edit",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A2:C2",
				"kind": "protected_range", "action": "add",
				"description": "livesheet protected band", "warning_only": false,
			},
		},
		{
			name: "the protection is updated",
			why:  "warning_only is the weak form, and a caller has to be able to move between them",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A2:C2",
				"kind": "protected_range", "action": "update", "warning_only": true,
			},
		},
		{
			name: "and lifted, over the very range it protects",
			why:  "a protection that blocked the request lifting it would be a trap rather than a guard",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A2:C2",
				"kind": "protected_range", "action": "delete",
			},
		},
		{
			name: "a validation rule with a dropdown",
			why:  "a list rule that showed no list would be a dropdown nobody can use",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A3:A4",
				"kind": "data_validation", "action": "add", "condition": "one_of_list",
				"values": []any{"Quorbin", "Vandel", "Skerry"},
				"strict": true, "message": "pick one of the three",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "one of list") {
					return fmt.Errorf("the result does not describe the rule: %s", text)
				}
				return nil
			},
		},
		{
			name:        "a condition given the wrong number of values is refused",
			why:         "a between with one operand is a caller who meant something else, and Google's own refusal names no argument",
			tool:        "manage_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A3:A4", "kind": "data_validation", "action": "add", "condition": "number_between", "values": []any{"1"}},
			expectError: "invalid",
		},
		{
			name: "and the rule is removed",
			why:  "a validation rule left behind would refuse a later step's write for a reason nobody chose",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A3:A4",
				"kind": "data_validation", "action": "delete",
			},
		},
		{
			name: "banding, colored from one color",
			why:  "nobody has four colors in their hand, and the interface asks for one too",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "banding", "action": "add", "color": "#d9e2f3", "header": true,
			},
		},
		{
			name: "the banding is recolored, then removed",
			why:  "an update that could not change the color would leave delete-and-add as the only way",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "banding", "action": "update", "color": "#f3e2d9",
			},
		},
		{
			name: "banding removed",
			why:  "and the next steps write in that band",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "banding", "action": "delete",
			},
		},
		{
			name: "a conditional format rule, added at an index",
			why:  "rules are evaluated in order, so the index is how a caller says which wins",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "add", "index": 0,
				"condition": "text_contains", "values": []any{"Quorbin"},
				"color": "#d9ead3", "text_color": "#b7472a", "bold": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "text contains Quorbin") {
					return fmt.Errorf("the result does not describe the rule: %s", text)
				}
				return nil
			},
		},
		{
			name: "the rule is replaced in place",
			why:  "an update that appended instead would leave two rules where the caller asked for one",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "update", "index": 0,
				"condition": "not_blank", "color": "#d9ead3",
			},
		},
		{
			name:        "an index past the end says how many there are",
			why:         "the API's own refusal for this names neither the sheet nor the count",
			tool:        "manage_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8", "kind": "conditional_format", "action": "delete", "index": 9},
			expectError: "invalid",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "conditional format rule(s)") {
					return fmt.Errorf("the refusal does not say how many there are: %s", text)
				}
				return nil
			},
		},
		{
			name: "the conditional rule is deleted by its index",
			why:  "the index is the API's own identifier for a rule, and deleting by it has to work",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "delete", "index": 0,
			},
		},
	}, d.colorScaleSteps()...)
}

// colorScaleSteps write a color scale with each of the midpoint types
// the Sheets interface offers, number, percent and percentile, and read
// it back. Spike S found Google takes min and max there too (§18).
func (d *driver) colorScaleSteps() []step {
	const percentile = "color scale: min #ffffff -> percentile 50 #ffd666 -> max #57bb8a"
	const percent = "color scale: number 0 #ffffff -> percent 50 #ffd666 -> max #57bb8a"
	const number = "color scale: min #ffffff -> number 10 #ffd666 -> percentile 90 #57bb8a"
	return []step{
		{
			name: "a color scale with a percentile midpoint",
			why:  "a gradient rule is a conditional format with no condition: its colors are in its points",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "add", "index": 0,
				"gradient": []any{"min #ffffff", "percentile 50 #ffd666", "max #57bb8a"},
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, percentile) {
					return fmt.Errorf("the result does not describe the scale: %s", text)
				}
				return nil
			},
		},
		d.readsBack("A5:C8", percentile),
		{
			name: "a percent midpoint replaces the scale whole",
			why:  "an update sends the whole rule, so the old points must not survive it",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "update", "index": 0,
				"gradient": []any{"number 0 #ffffff", "percent 50 #ffd666", "max #57bb8a"},
			},
		},
		d.readsBack("A5:C8", percent),
		{
			name: "a number midpoint",
			why:  "the third midpoint type manage_range sends",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "update", "index": 0,
				"gradient": []any{"min #ffffff", "number 10 #ffd666", "percentile 90 #57bb8a"},
			},
		},
		d.readsBack("A5:C8", number),
		{
			name: "the color scale is deleted by its index",
			why:  "the table steps below count the rules over this band",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "delete", "index": 0,
			},
		},
	}
}

// readsBack is the read after a color-scale write. The result describes
// the request; only a read says what Google stored, colorStyle or color.
func (d *driver) readsBack(rangeA1, want string) step {
	return d.readsBackFrom(d.spreadsheet, d.workSheet, rangeA1, want)
}

func (d *driver) readsBackFrom(spreadsheet, sheet, rangeA1, want string) step {
	return step{
		name: "read back as written: " + want,
		why:  "the result describes the request; only a read says what Google stored",
		tool: "read_formatting",
		args: map[string]any{"spreadsheet": spreadsheet, "sheet": sheet, "range": rangeA1},
		check: func(text string, _ map[string]any) error {
			if !strings.Contains(text, want) {
				return fmt.Errorf("the read does not say %q: %s", want, text)
			}
			return nil
		},
	}
}

// tableSteps are the table's add, rename and delete, and the rule a
// delete takes with it.
func (d *driver) tableSteps() []step {
	return []step{
		{
			name: "a table over the band",
			why:  "a table is a first-class object in the API and nothing else here creates one",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "table", "action": "add", "name": "LivesheetTable",
			},
		},
		{
			name: "the table is renamed",
			why:  "a rename that moved it instead would take the cells with it",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "table", "action": "update", "name": "LivesheetTableTwo",
			},
		},
		{
			// The step that found this: a rule over the table's range
			// vanished across a delete, and spike J narrowed it to the
			// delete rather than the add.
			name: "a rule over the table's range, to be taken by the delete",
			why:  "deleting a table removes every conditional rule over its range, and the reply says nothing",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "add", "index": 0,
				"condition": "not_blank", "color": "#d9ead3",
			},
		},
		{
			name:        "deleting the table is refused while a rule is over it",
			why:         "nothing in Sheets brings a conditional rule back, and nothing in the reply says one went",
			tool:        "manage_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8", "kind": "table", "action": "delete"},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "conditional format rule") || !strings.Contains(text, "overwrite") {
					return fmt.Errorf("the refusal does not say what would go or how to allow it: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, the table and its rule go",
			why:  "the guard refuses until it is told to allow, and then it allows",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "table", "action": "delete", "overwrite": true,
			},
		},
		{
			name: "and the rule really did go with it",
			why:  "a driver that only reports success can be wrong about every result it printed",
			tool: "read_formatting",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8"},
			check: func(text string, s map[string]any) error {
				if rules, _ := s["conditional_rules"].([]any); len(rules) != 0 {
					return fmt.Errorf("%d conditional rule(s) survived the table delete: %s", len(rules), text)
				}
				return nil
			},
		},
	}
}

// typedBand is the block the typed-column steps make a table over, with
// a header row, clear of every other step's cells.
const typedBand = "A50:D53"

// typedColumnSteps type a table's columns on add, retype one on update,
// and check that the rest, a dropdown's list included, survive the
// whole-array round trip, the header cells with them. A formula into the
// header is refused, since Google would replace it (spike T). Two things
// here are unverified (§18), and each step says which it settles:
// whether an update replaces the whole array, and what a type does to
// the cells.
func (d *driver) typedColumnSteps() []step {
	headers := func(when string) step {
		return step{
			name: "the header cells read as written, " + when,
			why: "Google writes a name sent into its header cell, and \"Column 1\" into a typed column's sent " +
				"with none (§18); an add sends each typed column's header text and an update every column's",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A50:D50", "show": "values"},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"Item", "Amount", "Due", "Status"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the header %q is gone %s: %s", want, when, text)
					}
				}
				return nil
			},
		}
	}
	card := func(when string, want ...string) step {
		return step{
			name: "the card shows the column types " + when,
			why:  "the card reads the types back in the spelling column_types takes",
			tool: "get_spreadsheet",
			args: map[string]any{"spreadsheet": d.spreadsheet},
			check: func(text string, _ map[string]any) error {
				for _, l := range strings.Split(text, "\n") {
					if !strings.Contains(l, "LivesheetTyped") {
						continue
					}
					line("     LOOK: %s", strings.TrimSpace(l))
					for _, w := range want {
						if !strings.Contains(l, w) {
							return fmt.Errorf("the card's table line does not say %q: %s", w, l)
						}
					}
					return nil
				}
				return fmt.Errorf("the card lists no table called LivesheetTyped:\n%s", text)
			},
		}
	}
	return []step{
		{
			name: "a block with a header row, for a typed table",
			why:  "a header is how a column is named by text, and what a table write must leave alone",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": typedBand, "input": "typed",
				"values": [][]any{
					{"Item", "Amount", "Due", "Status"},
					{"Quorbin", "12.5", "2026-10-01", "Open"},
					{"Skerry", "3", "2026-10-02", "Done"},
					{"Nardle", "40", "2026-10-03", ""},
				},
			},
		},
		{
			name: "a table typing three of its four columns, by heading and by letter",
			why: "column_types sends only the columns named, each with its header's text as its name, which " +
				"keeps Google from writing \"Column 1\" over the header (§18)",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": typedBand,
				"kind": "table", "action": "add", "name": "LivesheetTyped",
				"column_types": []any{"Amount number", "C date", "Status dropdown: Open, In progress, Done"},
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Status dropdown (Open, In progress, Done)") {
					return fmt.Errorf("the result does not describe the dropdown: %s", text)
				}
				return nil
			},
		},
		card("after the add", "number", "date", "dropdown (Open, In progress, Done)"),
		headers("after the add"),
		{
			name: "what a number type did to the cells under it",
			why: "whether a type change rewrites a column's number format is unverified (§18); the " +
				"transcript shows what the cells carry now",
			tool: "read_formatting",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "B51:D53"},
			check: func(text string, _ map[string]any) error {
				line("     LOOK: %s", strings.Join(strings.Fields(text), " "))
				return nil
			},
		},
		{
			name: "one column retyped, and the others sent back as they were",
			why: "whether one column alone would replace the array is open (§18), so the update reads the " +
				"array and sends every column back, each with its name, since Google refuses an entry with " +
				"none; the dropdown goes in the card's own spelling, and the card after it must show the change " +
				"and the dropdown's list both",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": typedBand,
				"kind": "table", "action": "update",
				"column_types": []any{"Amount currency", "Status dropdown (Open, In progress, Done)"},
			},
		},
		card("after the update", "currency", "date", "dropdown (Open, In progress, Done)"),
		headers("after the update"),
		{
			name: "a formula into a table's header cell is refused",
			why: "spike T: Google replaces a formula written there with a column name of its own and answers " +
				"200, so the formula is lost with nothing said",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "D50", "input": "typed",
				"values": [][]any{{`="Sta"&"tus"`}}, "overwrite": true,
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "D50") || !strings.Contains(text, "table's header row") {
					return fmt.Errorf("the refusal does not name the header cell: %s", text)
				}
				return nil
			},
		},
		{
			name: "a boolean type over text is refused",
			why: "a boolean column shows checkboxes, and what Google does to a word already in one is " +
				"unverified (§18), so the update stops and names the cells",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": typedBand,
				"kind": "table", "action": "update", "column_types": []any{"Item boolean"},
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "A51") || !strings.Contains(text, "TRUE or FALSE") {
					return fmt.Errorf("the refusal does not name the cells: %s", text)
				}
				return nil
			},
		},
		{
			name: "the typed table goes",
			why:  "the band is left as the steps found it, values aside",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": typedBand,
				"kind": "table", "action": "delete",
			},
		},
	}
}

// commaLocaleAll writes a number point under a locale that writes a
// decimal with a comma. Spike S found Google refuses 1.5 there and takes
// 1,5 (§18); which value 1,5 colors from, only a person looking can say.
// It needs a spreadsheet in that locale, so it makes one.
func (d *driver) commaLocaleAll() {
	d.run(step{
		name: "a spreadsheet in a comma-decimal locale",
		why:  "a color scale's number value is text, and only a spreadsheet in such a locale can say how it is read",
		tool: "create_spreadsheet",
		args: map[string]any{
			"title":  scratchTitle + " comma locale",
			"sheets": []any{"Grivet"},
			"values": [][]any{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}},
			"locale": "de_DE",
		},
		check: func(_ string, s map[string]any) error {
			id, _ := s["spreadsheet"].(string)
			if id == "" {
				return fmt.Errorf("no spreadsheet id came back")
			}
			d.commaLocale = id
			reg(id, "<comma-locale-spreadsheet>")
			return nil
		},
	})
	if d.commaLocale == "" {
		return
	}
	d.run(d.commaLocaleSteps()...)
	line("     LOOK: no reply says what de_DE read 1,5 as. In the comma-locale spreadsheet, column B scales")
	line("     from 1,5 over 1 to 5. If its color first changes between 1 and 2, de_DE read it as one and")
	line("     a half; record it in §18.")
}

func (d *driver) commaLocaleSteps() []step {
	return []step{
		{
			name: "a number point written with a decimal point, under de_DE, is refused",
			why: "manage_range sends the value as written, and spike S found Google refuses 1.5 where the " +
				"locale writes one and a half as 1,5",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.commaLocale, "sheet": "Grivet", "range": "A1:A5",
				"kind": "conditional_format", "action": "add", "index": 0,
				"gradient": []any{"number 1.5 #ffffff", "max #57bb8a"},
			},
			expectError: "invalid",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Invalid InterpolationPoint.value: 1.5") {
					return fmt.Errorf("the refusal is not Google's for the value: %s", text)
				}
				return nil
			},
		},
		{
			name: "the same point written with a decimal comma",
			why:  "the spelling a person in that locale types, which spike S found Google takes",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.commaLocale, "sheet": "Grivet", "range": "B1:B5",
				"kind": "conditional_format", "action": "add", "index": 0,
				"gradient": []any{"number 1,5 #ffffff", "max #57bb8a"},
			},
		},
		d.readsBackFrom(d.commaLocale, "Grivet", "B1:B5", "color scale: number 1,5 #ffffff -> max #57bb8a"),
	}
}
