package main

import (
	"fmt"
	"strconv"
	"strings"
)

// harness is what an end-state check reads through.
//
// A function rather than a session, so the scoring is ordinary code a
// test can drive with a stub. Everything an end-state check knows comes
// back through this server's own tools: reading the spreadsheet another
// way would be scoring Google rather than scoring the thing under test.
type harness struct {
	call func(tool string, args map[string]any) (text string, structured map[string]any, err error)
}

// read returns one range as the addressed grid a model would see.
func (h *harness) read(f Fixture, sheet, rng string, extra map[string]any) (string, map[string]any, error) {
	args := map[string]any{"spreadsheet": f.ID, "sheet": sheet, "range": rng}
	for k, v := range extra {
		args[k] = v
	}
	return h.call("read_range", args)
}

// cellMatches reads one cell and applies a predicate to what is in it.
func (h *harness) cellMatches(f Fixture, sheet, cell string, want func(string) error) error {
	_, s, err := h.read(f, sheet, cell, map[string]any{"format": "json"})
	if err != nil {
		return fmt.Errorf("reading %s: %w", cell, err)
	}
	return want(firstValue(s))
}

// There is deliberately no "is this value anywhere on the sheet" helper.
//
// There was, and it is what let the append task pass a run in which the
// model wrote nothing: the marker it looked for was a word the fixture's
// own vocabulary seeds, so the sheet contained it before the task began.
// A check of that shape passes on the fixture rather than on the work,
// and the shape is the problem rather than the word — so the helper is
// gone and each end state says where it expects to find something.

// appendedBelowTheData reports whether a marker landed under the seeded
// block rather than on top of it.
//
// Below, not merely present: "add a row to the bottom" is the task, and
// a write that replaced row 2 would satisfy "is it on the sheet" while
// doing the opposite of what was asked.
func (h *harness) appendedBelowTheData(f Fixture, sheet, marker string) error {
	first := f.DataLastRow + 1
	rng := fmt.Sprintf("A%d:B%d", first, first+6)
	text, _, err := h.read(f, sheet, rng, nil)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rng, err)
	}
	if !strings.Contains(text, marker) {
		return fmt.Errorf("%q is not in %s, so nothing was appended under the data", marker, rng)
	}
	return nil
}

// sheetExists reports whether the spreadsheet has a sheet by this title.
func (h *harness) sheetExists(f Fixture, title string) error {
	_, s, err := h.call("get_spreadsheet", map[string]any{"spreadsheet": f.ID})
	if err != nil {
		return err
	}
	sheets, _ := s["sheets"].([]any)
	for _, v := range sheets {
		if name, _ := v.(string); name == title {
			return nil
		}
	}
	return fmt.Errorf("no sheet called %q; the spreadsheet has %v", title, sheets)
}

// formulaSurvivedUnlessAcknowledged is the guard's end state, and it has
// two acceptable answers.
//
// A model told "put 1 there" may reasonably decide, after being refused,
// that the person meant it — or may reasonably stop and ask. What must
// not happen is the formula going without the refusal ever appearing. So
// the check is conditional on the trace rather than absolute, which is
// the honest shape: an absolute check here would fail a correct model
// half the time and teach nobody anything.
func (h *harness) formulaSurvivedUnlessAcknowledged(f Fixture, sheet string, r *Run) error {
	text, _, err := h.read(f, sheet, f.FormulaCell, map[string]any{"show": "formulas"})
	if err != nil {
		return err
	}
	gone := !strings.Contains(text, "=")
	if !gone {
		return nil
	}
	if !r.refused("blocked") {
		return fmt.Errorf("the formula in %s was replaced and nothing refused first", f.FormulaCell)
	}
	return nil
}

// headerIsFormatted reads the formatting back rather than the values.
func (h *harness) headerIsFormatted(f Fixture, sheet string) error {
	text, _, err := h.call("read_formatting", map[string]any{
		"spreadsheet": f.ID, "sheet": sheet, "range": "1:1",
	})
	if err != nil {
		return err
	}
	var missing []string
	for what, marker := range map[string]string{
		"bold": "bold", "centred": "cent", "shaded": "background",
	} {
		if !strings.Contains(strings.ToLower(text), marker) {
			missing = append(missing, what)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("row 1 is not %s", strings.Join(missing, ", "))
	}
	return nil
}

// columnIsDescending reads the column back and checks the order.
//
// Read back, not inferred from the sort's own report of itself: §13's
// rule from a sibling's driver, where all three wrong results were a
// result describing the state from before the write.
func (h *harness) columnIsDescending(f Fixture, sheet, column string) error {
	// The seeded block only. Reading to row 100 read past it into what
	// earlier tasks had written — a column total three tasks back — and
	// failed a model that had sorted exactly what it was asked to.
	block := fmt.Sprintf("%s2:%s%d", column, column, f.DataLastRow)
	_, s, err := h.read(f, sheet, block, map[string]any{"format": "json"})
	if err != nil {
		return err
	}
	rows, _ := s["rows"].([]any)
	last := 0.0
	first := true
	for _, row := range rows {
		cells, _ := row.([]any)
		if len(cells) == 0 {
			continue
		}
		text, _ := cells[0].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			continue
		}
		if !first && v > last {
			return fmt.Errorf("column %s is not in descending order: %g comes after %g", column, v, last)
		}
		last, first = v, false
	}
	if first {
		return fmt.Errorf("column %s has no numbers in it to be ordered", column)
	}
	return nil
}

// hasValidation reads the rule attached to a cell.
func (h *harness) hasValidation(f Fixture, sheet, cell string) error {
	text, _, err := h.call("read_formatting", map[string]any{
		"spreadsheet": f.ID, "sheet": sheet, "range": cell,
	})
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(text), "validation") {
		return fmt.Errorf("%s carries no validation rule", cell)
	}
	return nil
}

// anchorExists reads the anchors back by name.
func (h *harness) anchorExists(f Fixture, name string) error {
	text, _, err := h.call("manage_anchor", map[string]any{
		"spreadsheet": f.ID, "action": "list",
	})
	if err != nil {
		return err
	}
	if !strings.Contains(text, name) {
		return fmt.Errorf("no anchor called %q", name)
	}
	return nil
}

// firstValue pulls the first cell out of a json-format read.
func firstValue(s map[string]any) string {
	rows, _ := s["rows"].([]any)
	if len(rows) == 0 {
		return ""
	}
	cells, _ := rows[0].([]any)
	if len(cells) == 0 {
		return ""
	}
	v, _ := cells[0].(string)
	return strings.TrimSpace(v)
}
