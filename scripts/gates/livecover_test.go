package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestDriverCallsReadsBothSpellings is the bug this check had on its
// first run: a step written on its own carries its type, and a step
// inside a []step slice has it elided. Reading only the first spelling
// found one step out of nineteen and reported "the check is not reading
// it" — which was true, and would have been a silent 1/28 coverage
// claim if the floor had not caught it.
func TestDriverCallsReadsBothSpellings(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func steps() []step {
	return []step{
		{name: "a", tool: "read_range", args: map[string]any{"spreadsheet": x, "range": y}},
		{name: "b", tool: "read_range", args: map[string]any{"show": z}},
	}
}

func one() {
	s := step{name: "c", tool: "search_spreadsheets", args: map[string]any{"name": n}}
	_ = s
}

func notAStep() {
	_ = other{tool: "get_spreadsheet", args: map[string]any{"spreadsheet": x}}
}
`
	if err := os.WriteFile(filepath.Join(dir, "steps.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, tools, err := driverCalls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tools != 2 {
		t.Fatalf("found %d tools, want 2 (read_range and search_spreadsheets)", tools)
	}
	// Both spellings, and arguments unioned across steps of one tool.
	for _, want := range []string{"spreadsheet", "range", "show"} {
		if !got["read_range"][want] {
			t.Errorf("read_range.%s was not seen; the elided-type spelling is being missed", want)
		}
	}
	if !got["search_spreadsheets"]["name"] {
		t.Error("the explicitly typed spelling is being missed")
	}
	// A literal of some other type is not a step.
	if _, ok := got["get_spreadsheet"]; ok {
		t.Error("a composite literal of another type was read as a step")
	}
}

// The exemption list and its reasons live in internal/livecover, which
// both this gate and the driver read, and are tested there. Testing a
// copy here would be testing that a copy exists.

func TestMapKeysOf(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", `package p
var m = map[string]any{"a": 1, "b": 2}
var n = []int{1, 2}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			got = append(got, mapKeysOf(vs.Values[0])...)
		}
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("mapKeysOf = %v, want [a b]", got)
	}
}
