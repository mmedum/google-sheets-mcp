package main

import (
	"strings"
	"testing"
)

// fixture is what the table is built against in a test — the same one
// the harness checks the table with before it creates anything, so the
// test and the run cannot disagree about what a filled fixture is.
func fixture() Fixture { return checkFixture }

// The guard §13 asks for, in `go test` where it costs nothing rather
// than in a live run that costs ten minutes and an account.
//
// A sibling project's first full eval run passed two tasks while sending
// the agent a literal `{folder}`: one found the file by name across the
// whole account, and one wanted a refusal and got one for the wrong
// reason. That is the end-state-versus-trace problem happening inside
// the harness built to catch it.
func TestEveryPromptIsSubstituted(t *testing.T) {
	if err := CheckPrompts(Tasks(fixture())); err != nil {
		t.Fatal(err)
	}
}

// And the guard has to fail, or it is a check nobody has watched work.
func TestCheckPromptsCatchesWhatItIsFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		task Task
		want string
	}{
		{
			name: "an unsubstituted placeholder",
			task: Task{Name: "x", Why: "y", Prompt: "read {spreadsheet} for me", MaxCalls: 1,
				Trace: func(*Run) error { return nil }, Unverifiable: "z"},
			want: "{spreadsheet}",
		},
		{
			name: "no scoring at all",
			task: Task{Name: "x", Why: "y", Prompt: "read it", MaxCalls: 1},
			want: "scores neither",
		},
		{
			name: "no end state and no reason",
			task: Task{Name: "x", Why: "y", Prompt: "read it", MaxCalls: 1,
				Trace: func(*Run) error { return nil }},
			want: "does not say why",
		},
		{
			name: "no call ceiling",
			task: Task{Name: "x", Why: "y", Prompt: "read it",
				Trace: func(*Run) error { return nil }, Unverifiable: "z"},
			want: "call ceiling",
		},
		{
			name: "no why",
			task: Task{Name: "x", Prompt: "read it", MaxCalls: 1,
				Trace: func(*Run) error { return nil }, Unverifiable: "z"},
			want: "has no why",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPrompts([]Task{tc.task})
			if err == nil {
				t.Fatal("the table was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// Two tasks under one name: results are keyed by name, so a
	// duplicate silently overwrites a result.
	dup := Task{Name: "same", Why: "y", Prompt: "read it", MaxCalls: 1,
		Trace: func(*Run) error { return nil }, Unverifiable: "z"}
	if err := CheckPrompts([]Task{dup, dup}); err == nil {
		t.Error("two tasks share a name and the table was accepted")
	}
}

// The trace checks are what catch a model that got the right answer the
// wrong way, so they are tested against traces built by hand.
func TestTraceChecks(t *testing.T) {
	f := fixture()

	t.Run("an invented sheet name fails even when a later call succeeds", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{
			{Tool: "read_range", Args: map[string]any{"sheet": "Sheet1"}, IsError: true, Result: "[not_found] no sheet"},
			{Tool: "read_range", Args: map[string]any{"sheet": f.Sheet}},
		}}
		if err := r.noInventedSheet(f); err == nil {
			t.Error("guessing a sheet name and recovering was scored as a pass")
		}
		if !r.mentionedSheet1() {
			t.Error("the Sheet1 guess was not noticed")
		}
	})

	t.Run("a sheet the model created is its own to name", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{
			{Tool: "manage_sheet", Args: map[string]any{"action": "add", "title": "Threnody"}},
			{Tool: "write_values", Args: map[string]any{"sheet": "Threnody"}},
		}}
		if err := r.noInventedSheet(f); err != nil {
			t.Errorf("writing to a sheet it had just created was scored as invented: %v", err)
		}
	})

	t.Run("acknowledging what nothing refused fails", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{
			{Tool: "write_values", Args: map[string]any{"overwrite": true}},
		}}
		if err := r.didNotAcknowledgeUnasked("overwrite"); err == nil {
			t.Error("a model that always passes overwrite has turned the guard off, and this passed it")
		}
	})

	t.Run("acknowledging after a refusal is the refusal being obeyed", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{
			{Tool: "write_values", Args: map[string]any{}, IsError: true, Result: "[blocked] pass overwrite"},
			{Tool: "write_values", Args: map[string]any{"overwrite": true}},
		}}
		if err := r.didNotAcknowledgeUnasked("overwrite"); err != nil {
			t.Errorf("obeying a refusal was scored as a failure: %v", err)
		}
	})

	t.Run("writing before reading fails", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{{Tool: "write_values"}}}
		if err := r.readBeforeWriting(); err == nil {
			t.Error("a write with no read before it was scored as a pass")
		}
	})

	t.Run("a refusal is found by class", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{
			{Tool: "write_values", IsError: true, Result: "[blocked] D2 holds a formula"},
		}}
		if !r.refused("blocked") {
			t.Error("the blocked refusal was not seen")
		}
		if r.refused("not_found") {
			t.Error("a different class matched")
		}
	})

	t.Run("an external formula is recognised however it is nested", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{{Tool: "write_values", Args: map[string]any{
			"values": []any{[]any{"=IMPORTRANGE(\"x\",\"y\")"}},
		}}}}
		if !r.wroteExternalFormula() {
			t.Error("an IMPORTRANGE inside a values array was not seen")
		}
	})

	t.Run("a values read cannot see a formula's fault", func(t *testing.T) {
		r := &Run{Calls: []ToolCall{{Tool: "read_range", Args: map[string]any{"range": "D21"}}}}
		if r.readFormulas() {
			t.Error("a plain read was counted as having looked at the formula")
		}
		r.Calls = append(r.Calls, ToolCall{Tool: "read_range", Args: map[string]any{"show": "both"}})
		if !r.readFormulas() {
			t.Error("show=both was not counted as reading formulas")
		}
	})
}

// The end-state checks read back through the server, so they are tested
// against a stub that answers the way the server would.
func TestEndStateChecksReadBack(t *testing.T) {
	f := fixture()

	t.Run("a formula replaced with no refusal fails", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "D2 | 42\n", nil, nil // no "=" anywhere: the formula is gone
		}}
		err := (&harness{call: h.call}).formulaSurvivedUnlessAcknowledged(f, f.Sheet, &Run{})
		if err == nil {
			t.Error("the formula was replaced and nothing had refused, and this passed")
		}
	})

	t.Run("a formula replaced after a refusal is a decision", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "D2 | 42\n", nil, nil
		}}
		r := &Run{Calls: []ToolCall{{IsError: true, Result: "[blocked] D2 holds a formula"}}}
		if err := h.formulaSurvivedUnlessAcknowledged(f, f.Sheet, r); err != nil {
			t.Errorf("a model that was refused, read it and went ahead was failed: %v", err)
		}
	})

	t.Run("a column out of order fails", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "", map[string]any{"rows": []any{
				[]any{"100"}, []any{"900"}, []any{"500"},
			}}, nil
		}}
		if err := h.columnIsDescending(f, f.Sheet, "B"); err == nil {
			t.Error("100, 900, 500 was accepted as descending")
		}
	})

	t.Run("a column with no numbers is not silently in order", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "", map[string]any{"rows": []any{[]any{""}, []any{""}}}, nil
		}}
		if err := h.columnIsDescending(f, f.Sheet, "B"); err == nil {
			t.Error("an empty column passed a sort check; nothing was sorted and nothing said so")
		}
	})

	t.Run("a sheet that is not there fails", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "", map[string]any{"sheets": []any{f.Sheet, f.LongSheet}}, nil
		}}
		if err := h.sheetExists(f, "Summary"); err == nil {
			t.Error("a missing sheet was scored as created")
		}
		if err := h.sheetExists(f, f.Sheet); err != nil {
			t.Errorf("a sheet that exists was scored as missing: %v", err)
		}
	})

	t.Run("a header missing one of the three properties fails", func(t *testing.T) {
		h := &harness{call: func(string, map[string]any) (string, map[string]any, error) {
			return "A1:D1 bold, background #eeeeee\n", nil, nil // not centred
		}}
		err := h.headerIsFormatted(f, f.Sheet)
		if err == nil {
			t.Fatal("a header that was bold and shaded but not centred passed")
		}
		if !strings.Contains(err.Error(), "centred") {
			t.Errorf("err = %v, want it to name the property that is missing", err)
		}
	})
}

// Every task's prompt has to name the spreadsheet it works in, or the
// model is being asked to find one and the task is measuring Drive.
func TestPromptsNameTheirSpreadsheet(t *testing.T) {
	f := fixture()
	for _, task := range Tasks(f) {
		if strings.Contains(task.Prompt, f.ID) || strings.Contains(task.Prompt, f.Title) {
			continue
		}
		t.Errorf("%q names no spreadsheet: %s", task.Name, task.Prompt)
	}
}

// A prompt must not tell the model which tool to call. The eval is
// whether the tool surface leads somewhere sensible; a prompt naming the
// tool has answered the question it was asking.
func TestPromptsDoNotNameTools(t *testing.T) {
	tools := []string{
		"get_spreadsheet", "read_range", "write_values", "append_rows", "format_cells",
		"manage_range", "transform_range", "manage_sheet", "edit_dimensions", "find_in_spreadsheet",
		"search_spreadsheets", "read_formatting", "create_spreadsheet", "manage_anchor",
	}
	for _, task := range Tasks(fixture()) {
		for _, tool := range tools {
			if strings.Contains(task.Prompt, tool) {
				t.Errorf("%q names the tool %s in its prompt: %s", task.Name, tool, task.Prompt)
			}
		}
	}
}

func TestFixtureIsSyntheticThroughout(t *testing.T) {
	f := fixture()
	// The vocabulary the fixture builds from is invented, and a value
	// that looks like a real address or domain would reach a transcript.
	for _, s := range []string{f.Sheet, f.LongSheet, f.Title} {
		if strings.Contains(s, "@") || strings.Contains(s, ".com") {
			t.Errorf("%q looks like it identifies something", s)
		}
	}
	if f.Sheet == "Sheet1" {
		t.Error("the working sheet is called Sheet1, so the guessing task cannot fail")
	}
}
