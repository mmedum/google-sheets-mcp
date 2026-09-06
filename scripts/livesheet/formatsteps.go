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
)

func (d *driver) formatAll() {
	sec("format_cells")
	d.run(d.formatSteps()...)
	sec("read_formatting")
	d.run(d.readFormattingSteps()...)
	sec("manage_range")
	d.run(d.rangeSteps()...)
	sec("transform_range")
	d.run(d.transformSteps()...)
}

func (d *driver) formatSteps() []step {
	return []step{
		{
			name: "everything in one call is one batch",
			why:  "a header row that is bold, centred and shaded is one call rather than eight",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"number_format": "text", "bold": true, "italic": true, "underline": true,
				"strikethrough": false, "font_size": 11, "font_family": "Roboto",
				"text_colour": "#b7472a", "background": "#d9e2f3",
				"borders": "1pt solid #cccccc", "border_sides": "outer",
				"horizontal": "centre", "vertical": "middle", "wrap": "clip",
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
			name: "a merge that would discard values is refused",
			why:  "Sheets keeps the top-left value of a merge and drops the rest with nothing saying so",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1", "merge": "all",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "B1") || !strings.Contains(text, "overwrite") {
					return fmt.Errorf("the refusal names neither the cells nor the argument: %s", text)
				}
				return nil
			},
		},
		{
			name: "a dry run previews the refusal rather than repeating it",
			why:  "a preview is the one call that should always answer, since it sends nothing",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
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
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1",
				"merge": "all", "overwrite": true,
			},
		},
		{
			name: "and unmerge takes it apart again",
			why:  "a merge that could not be undone would leave the sheet in a shape no later step could write to",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:C1", "unmerge": true,
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
			name: "removing a note is not held back",
			why:  "removing is what the caller asked for; replacing is the loss they cannot see",
			tool: "format_cells",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1", "clear_note": true,
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
	const named = "Livesheet band"
	return []step{
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
			name: "banding, coloured from one colour",
			why:  "nobody has four colours in their hand, and the interface asks for one too",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "banding", "action": "add", "colour": "#d9e2f3", "header": true,
			},
		},
		{
			name: "the banding is recoloured, then removed",
			why:  "an update that could not change the colour would leave delete-and-add as the only way",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "banding", "action": "update", "colour": "#f3e2d9",
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
				"colour": "#d9ead3", "text_colour": "#b7472a", "bold": true,
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
				"condition": "not_blank", "colour": "#d9ead3",
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
			name: "a table over the band, and away again",
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
			name: "the table is removed and the cells stay",
			why:  "deleting a table must not delete what is in it",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "table", "action": "delete",
			},
		},
		{
			name: "the conditional rule is deleted",
			why:  "the scratch sheet is left as the transforms expect to find it",
			tool: "manage_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A5:C8",
				"kind": "conditional_format", "action": "delete", "index": 0,
			},
		},
	}
}
