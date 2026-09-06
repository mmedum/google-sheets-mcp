//go:build live

package main

import (
	"fmt"
	"strings"
)

// The transform steps, and the band of the sheet they work in.
//
// They seed their own rows through write_values rather than reaching
// past the server, so what they assert about is what a caller would have
// produced by the same route.
func (d *driver) transformSteps() []step {
	return []step{
		{
			name: "seed the band the transforms work in",
			why:  "a transform's own data, written the way a caller would write it",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30",
				// B counts up and C counts down, so a sort by C tells an
				// absolute column index from one read against the range.
				// Row 34 repeats row 33, for the de-duplication.
				"tsv": "Plimth\tNardle\tGrivet\n" +
					"Quorbin\t1\t4\n" +
					"Vandel\t2\t3\n" +
					"  Skerry   spaced  \t3\t2\n" +
					"  Skerry   spaced  \t3\t2",
				"input": "literal",
			},
			check: func(_ string, s map[string]any) error {
				if rng, _ := s["range"].(string); !strings.HasSuffix(rng, "A30:C34") {
					return fmt.Errorf("the seed landed on %q", rng)
				}
				return nil
			},
		},
		{
			// The question this step exists for: the API's
			// dimensionIndex is the sheet's own column, not an offset
			// into the range. Sorting B31:C34 by C tells the two apart,
			// because a relative reading would sort by B and leave the
			// rows where they are.
			name: "a sort key is the sheet's column, not an offset into the range",
			why:  "a wrong reading here sorts by the neighbouring column and looks like a working sort",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "B31:C34",
				"action": "sort", "sort_by": "C asc",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "no longer points where it did") {
					return fmt.Errorf("a sort did not say the addresses moved: %s", text)
				}
				return nil
			},
		},
		{
			name: "and the rows came back in the order the sort asked for",
			why:  "the driver reads state back rather than believing the call that changed it",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "B31:C34", "format": "csv",
			},
			check: func(text string, _ map[string]any) error {
				// C ascending is 2,2,3,4, so B reads 3,3,2,1. A sort that
				// had read the index against the range would have sorted
				// by B and left it 1,2,3,3.
				if !strings.Contains(text, "3,2") || strings.Contains(text, "1,4\n2,3") {
					return fmt.Errorf("the rows are not in C order, so the sort key was read against the range: %s", text)
				}
				return nil
			},
		},
		{
			name: "a sort key outside the range is refused",
			why:  "Google's own refusal for this names an index the caller never typed",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "B31:C34",
				"action": "sort", "sort_by": "A asc",
			},
			expectError: "invalid",
		},
		{
			name: "a replacement, with every switch it takes",
			why:  "match_case and match_entire_cell decide what matches, and a regex decides it differently",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:C34",
				"action": "find_replace", "find": "Qu[a-z]+n", "replace": "Trennow",
				"regex": true, "match_case": true, "match_entire_cell": false,
			},
			check: func(text string, s map[string]any) error {
				if changed, _ := s["changed"].(float64); changed < 1 {
					return fmt.Errorf("a regex that matches Quorbin changed %v cells", changed)
				}
				if !strings.Contains(text, "occurrence(s) replaced") {
					return fmt.Errorf("the count the API reported is missing: %s", text)
				}
				return nil
			},
		},
		{
			name:        "replacing inside formulas is refused while formulas are in the way",
			why:         "a replacement inside formula text changes what a cell computes rather than what it shows",
			tool:        "transform_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:F1", "action": "find_replace", "find": "1", "replace": "2", "in_formulas": true},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "overwrite_formulas") {
					return fmt.Errorf("the refusal does not name the argument that would allow it: %s", text)
				}
				return nil
			},
		},
		{
			name: "acknowledged, it goes through",
			why:  "the guard refuses until it is told to allow, and then it allows",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A1:F1",
				"action": "find_replace", "find": "Zzzznomatch", "replace": "x",
				"in_formulas": true, "overwrite": true, "overwrite_formulas": true,
			},
		},
		{
			name: "whitespace is trimmed and the cells are counted",
			why:  "a transform that reported nothing would leave the caller unable to tell it from a no-op",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:C34",
				"action": "trim_whitespace",
			},
			check: func(_ string, s map[string]any) error {
				if changed, _ := s["changed"].(float64); changed < 2 {
					return fmt.Errorf("two cells were padded with spaces and %v were trimmed", changed)
				}
				return nil
			},
		},
		{
			name: "duplicate rows are removed, comparing one column",
			why:  "which columns decide that two rows are the same is the caller's, not Google's",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A31:C34",
				"action": "remove_duplicates", "columns": "A",
			},
			check: func(text string, s map[string]any) error {
				if changed, _ := s["changed"].(float64); changed != 1 {
					return fmt.Errorf("one row repeats another and %v were removed", changed)
				}
				if !strings.Contains(text, "no longer points where it did") {
					return fmt.Errorf("a de-duplication did not say the addresses moved: %s", text)
				}
				return nil
			},
		},
		{
			name: "a cell with a delimiter in it, to split",
			why:  "text_to_columns needs something to split, and its own cell keeps it out of the band",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": splitCell,
				"values": [][]any{{"Quorbin|Vandel|Skerry"}}, "input": "literal",
			},
		},
		{
			name: "a split spills into the columns to its right",
			why:  "the columns it lands on are ones the caller never named, which is what this guard is for",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": splitCell,
				"action": "text_to_columns", "delimiter": "|",
			},
		},
		{
			name: "the split landed where it said",
			why:  "a driver that only reports success can be wrong about every result it printed",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "E30:G30", "format": "csv",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Quorbin,Vandel,Skerry") {
					return fmt.Errorf("the split did not put three values across three columns: %s", text)
				}
				return nil
			},
		},
		{
			name: "the rows are shuffled",
			why:  "randomize is one of the two the fake cannot simulate, so this is the only place it runs",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A31:C33", "action": "randomize",
			},
		},
		{
			name: "a series is filled downwards",
			why:  "auto_fill is the other one the fake cannot simulate: the series is Google's judgement",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "B31:B32",
				"action": "auto_fill", "fill_rows": true, "fill_length": 2, "overwrite": true,
			},
		},
		{
			name: "a copy lands where it was told, transposed",
			why:  "a paste lands on cells the caller never named, so the destination is read and refused first",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:C30",
				"action": "copy_paste", "destination": "H30", "paste": "values", "transpose": true,
			},
			check: func(text string, s map[string]any) error {
				dest, _ := s["destination"].(string)
				if !strings.HasSuffix(dest, "H30:H32") {
					return fmt.Errorf("a transposed 1x3 copy landed on %q, want three rows of one column", dest)
				}
				if !strings.Contains(text, "transposed") {
					return fmt.Errorf("the result does not say it transposed: %s", text)
				}
				return nil
			},
		},
		{
			name:        "a copy onto occupied cells is refused",
			why:         "the destination is the half of a paste the caller cannot see",
			tool:        "transform_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:C30", "action": "copy_paste", "destination": "H30"},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "not empty") {
					return fmt.Errorf("the refusal does not say what is in the way: %s", text)
				}
				return nil
			},
		},
		{
			name: "a dry run says what would stop it and sends nothing",
			why:  "a preview is the one call that should always answer",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "A30:C30",
				"action": "copy_paste", "destination": "H30", "dry_run": true,
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "nothing was sent") || !strings.Contains(text, "would be refused") {
					return fmt.Errorf("the preview does not say what would stop it: %s", text)
				}
				return nil
			},
		},
		{
			name: "a cut empties the cells it came from",
			why:  "a move is the one transform whose source is part of what changed",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "E30:G30",
				"action": "cut_paste", "destination": "J30",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "is now blank") {
					return fmt.Errorf("a cut did not say it emptied its source: %s", text)
				}
				return nil
			},
		},
		{
			name: "and the source really is blank",
			why:  "all three of a sibling's wrong results were describing the state from before the write",
			tool: "read_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": d.workSheet, "range": "E30:G30", "format": "csv",
			},
			check: func(text string, _ map[string]any) error {
				if strings.Contains(text, "Quorbin") {
					return fmt.Errorf("the cut left its source behind: %s", text)
				}
				return nil
			},
		},
	}
}
