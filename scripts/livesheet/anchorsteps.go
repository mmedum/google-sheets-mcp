//go:build live

package main

import (
	"fmt"
	"strings"
)

// The anchor steps, on a sheet of their own.
//
// Its own sheet because the claim under test is that an anchor survives
// rows moving, and testing that means inserting and deleting rows — which
// would move every band the other steps assert about. A step that has to
// share a sheet with a step that moves rows is a step whose failures are
// somebody else's.
//
// What is being checked is not that the calls succeed. It is that the
// anchor still points at the same *values* after the sheet has been
// edited around it, so every check reads the row back rather than
// reading the anchor's own report of where it is.
const anchorSheet = "Livesheet anchors"

func (d *driver) anchorAll() {
	sec("manage_anchor")
	d.run(d.anchorSetupSteps()...)
	d.run(d.anchorSteps()...)
	d.run(d.anchorDurabilitySteps()...)
	d.run(d.anchorRemovalSteps()...)
}

func (d *driver) anchorSetupSteps() []step {
	return []step{
		{
			name: "add a sheet for the anchor steps",
			why:  "these steps insert and delete rows, which would move what every other step asserts about",
			tool: "manage_sheet",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "title": anchorSheet, "rows": 50, "cols": 6,
			},
		},
		{
			name: "fill it with rows that name themselves",
			why:  "a row that says which row it started on is how a moved anchor is told from a lucky one",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet, "range": "A1:B6",
				"values": [][]any{
					{"Header", "start"},
					{"Plimth", "row2"},
					{"Quorbin", "row3"},
					{"Vandel", "row4"},
					{"Threnody", "row5"},
					{"Marrowfen", "row6"},
				},
			},
		},
	}
}

func (d *driver) anchorSteps() []step {
	return []step{
		{
			name: "a dry run anchors nothing",
			why:  "a preview that created an anchor would be a preview that wrote",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "preview only", "range": "2:2", "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "nothing was sent") {
					return fmt.Errorf("the preview does not say it sent nothing")
				}
				if dry, _ := s["dry_run"].(bool); !dry {
					return fmt.Errorf("the structured half does not say it was a dry run")
				}
				return nil
			},
		},
		{
			name: "the dry run left nothing behind",
			why:  "the preview's own claim, checked against the spreadsheet rather than believed",
			tool: "manage_anchor",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				if strings.Contains(text, "preview only") {
					return fmt.Errorf("the dry run created an anchor")
				}
				return nil
			},
		},
		{
			name: "anchor a row, with a note",
			why:  "the label and the note are what a later call gets back, and a move must not erase either",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "third row", "note": "the one that moves", "range": "3:3",
			},
			check: func(text string, s map[string]any) error {
				anchors, _ := s["anchors"].([]any)
				if len(anchors) != 1 {
					return fmt.Errorf("the result carries %d anchor(s)", len(anchors))
				}
				first, _ := anchors[0].(map[string]any)
				if scope, _ := first["scope"].(string); scope != "row" {
					return fmt.Errorf("scope = %q, want row", scope)
				}
				if !strings.Contains(text, "follows this row") {
					return fmt.Errorf("the summary does not say what an anchor is for:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "a second anchor cannot take the same name",
			why:  "Google allows two entries under one key, so uniqueness is this server's to keep or anchor:x has two answers",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "third row", "range": "4:4",
			},
			expectError: "invalid",
		},
		{
			name: "a rectangle cannot be anchored",
			why:  "the API refuses it naming a type nobody sent; this refusal names the range that caused it",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "a block", "range": "A1:C4",
			},
			expectError: "invalid",
		},
		{
			name: "anchor a column",
			why:  "an anchor attaches to a column as well as a row, and the two must not be confused by a delete",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "the names column", "range": "A:A",
			},
			check: func(_ string, s map[string]any) error {
				anchors, _ := s["anchors"].([]any)
				if len(anchors) != 1 {
					return fmt.Errorf("the result carries %d anchor(s)", len(anchors))
				}
				first, _ := anchors[0].(map[string]any)
				if scope, _ := first["scope"].(string); scope != "column" {
					return fmt.Errorf("scope = %q, want column", scope)
				}
				return nil
			},
		},
		{
			name: "anchor the sheet itself",
			why:  "an empty range is the sheet, which is a third location type and a different lookup",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "add", "name": "the anchor sheet",
			},
		},
		{
			name: "list every anchor, whatever it is attached to",
			why:  "one request has to reach rows, columns and sheets, or listing costs a request per sheet",
			tool: "manage_anchor",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "list"},
			check: func(text string, s map[string]any) error {
				anchors, _ := s["anchors"].([]any)
				if len(anchors) != 3 {
					return fmt.Errorf("listed %d anchors, want the row, the column and the sheet", len(anchors))
				}
				for _, want := range []string{"third row", "the names column", "the anchor sheet"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the listing does not name %q:\n%s", want, text)
					}
				}
				return nil
			},
		},
	}
}

func (d *driver) anchorDurabilitySteps() []step {
	return []step{
		{
			name: "read through the anchor before anything moves",
			why:  "the baseline: without it, a later read landing on the right row proves nothing",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "range": "anchor:third row"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Quorbin") {
					return fmt.Errorf("the anchor did not read row 3:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "insert ten rows above the anchored row",
			why:  "this is what makes an A1 address wrong and an anchor right, so it is the step the feature exists for",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "insert", "dimension": "rows", "band": "1:10",
			},
		},
		{
			name: "the anchor still reads the same values",
			why:  "an anchor that kept a row number would now read a blank row and look like it worked",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "range": "anchor:third row"},
			check: func(text string, s map[string]any) error {
				if !strings.Contains(text, "Quorbin") {
					return fmt.Errorf("the anchor lost its row after an insert above it:\n%s", text)
				}
				// The row number must have changed, or the insert never
				// happened and this step proves nothing.
				rng, _ := s["range"].(string)
				if !strings.Contains(rng, "13") {
					return fmt.Errorf("the anchor reads %q; ten rows were inserted above row 3, so it should be row 13", rng)
				}
				return nil
			},
		},
		{
			name: "sort the block by the first column",
			why:  "a sort rewrites which row holds what, and spike K found the anchor travels with the values",
			tool: "transform_range",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet, "range": "A11:B16",
				"action": "sort", "sort_by": "A asc",
			},
		},
		{
			name: "the anchor followed the values through the sort",
			why:  "the one behaviour here that a reading of the reference would have got backwards",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "range": "anchor:third row"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Quorbin") {
					return fmt.Errorf("after a sort the anchor points at a different row's values:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "move the anchor to another row",
			why:  "a move keeps the label and the note; a field mask of * would blank both",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "move", "name": "third row", "range": "12:12",
			},
			check: func(_ string, s map[string]any) error {
				anchors, _ := s["anchors"].([]any)
				if len(anchors) != 1 {
					return fmt.Errorf("the result carries %d anchor(s)", len(anchors))
				}
				return nil
			},
		},
		{
			name: "the moved anchor kept its note",
			why:  "the note is the half a careless field mask silently erases, and no error would say so",
			tool: "manage_anchor",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "list"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "the one that moves") {
					return fmt.Errorf("the move erased the note:\n%s", text)
				}
				return nil
			},
		},
		{
			name:        "an anchor that does not exist is refused",
			why:         "a name nobody set must not resolve to a plausible rectangle",
			tool:        "read_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "range": "anchor:no such label"},
			expectError: "not_found",
		},
	}
}

func (d *driver) anchorRemovalSteps() []step {
	return []step{
		{
			name: "deleting rows names the anchors it would take",
			why:  "spike K: the row's anchor goes with it and Google's reply says nothing at all",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"dimension": "rows", "band": "12:12",
			},
			expectError: "blocked",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "third row") {
					return fmt.Errorf("the refusal does not name the anchor that would go:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "a band can name an anchor too",
			why:  "the promise is any range or band argument, and a band takes a different path from a range",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "resize", "dimension": "rows", "band": "anchor:third row", "pixels": 30,
			},
		},
		{
			name: "and the dimension has to agree with it",
			why:  "a row anchor read as a column band would resize the wrong thing and look like it worked",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"action": "resize", "dimension": "columns", "band": "anchor:third row", "pixels": 90,
			},
			expectError: "invalid",
		},
		{
			name: "confirmed, the delete takes it",
			why:  "the count in the refusal has to be what actually happens, not a warning nobody checked",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": anchorSheet,
				"dimension": "rows", "band": "12:12", "confirm": true,
			},
			check: func(_ string, s map[string]any) error {
				gone, _ := s["anchors_removed"].([]any)
				if len(gone) != 1 {
					return fmt.Errorf("the result reports %d anchor(s) removed, want 1", len(gone))
				}
				return nil
			},
		},
		{
			name:        "the anchor is gone with its row",
			why:         "read back rather than inferred from the delete's own report of itself",
			tool:        "read_range",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "range": "anchor:third row"},
			expectError: "not_found",
		},
		{
			name: "remove an anchor and keep the data",
			why:  "removing a label must not touch the column it named",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "remove", "name": "the names column",
			},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "untouched") {
					return fmt.Errorf("the summary does not say the data survives:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "the column it named still holds its values",
			why:  "the removal's own claim, checked against the sheet",
			tool: "read_range",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": anchorSheet, "range": "A11:A16"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Quorbin") {
					return fmt.Errorf("removing the anchor took the column's values:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "removing one that is not there is refused",
			why:  "a silent success would let a caller believe a label was cleared when it was never found",
			tool: "manage_anchor",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "remove", "name": "never existed",
			},
			expectError: "not_found",
		},
	}
}
