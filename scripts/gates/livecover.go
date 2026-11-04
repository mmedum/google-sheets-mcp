package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v4/internal/livecover"
)

// The live driver covers every tool and every option (D-61). A guarantee
// stated over a surface decays every time the surface grows, so it is
// checked rather than asserted, and against the schema the binary
// publishes rather than a list anybody maintains.
//
// It is a static check because CI has no credentials: the point is to
// fail at the moment somebody adds an option and forgets the driver, not
// at the moment somebody next runs it.

// liveCoverGate compares the driver's calls with the published schema.
func liveCoverGate(bin string) error {
	_, dump, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	if len(dump.Tools) == 0 {
		return fmt.Errorf("the binary published no tools; this check is not looking at a server")
	}
	sent, called, err := driverCalls(filepath.Join("scripts", "livesheet"))
	if err != nil {
		return err
	}
	if called < 3 {
		return fmt.Errorf("found calls to %d tools in the driver; the check is not reading it", called)
	}

	tools := make([]livecover.Tool, 0, len(dump.Tools))
	for _, t := range dump.Tools {
		options := make([]string, 0, len(t.InputSchema.Properties))
		for name := range t.InputSchema.Properties {
			options = append(options, name)
		}
		tools = append(tools, livecover.Tool{Name: t.Name, Options: options})
	}
	report := livecover.Check(sent, tools)
	for _, e := range report.Excused {
		fmt.Printf("  undrivable: %s\n", e)
	}
	if err := livecover.Err(report); err != nil {
		return err
	}
	fmt.Printf("%s (read from the driver's source; a live run checks the same thing on the wire)\n",
		livecover.Summary(report))
	return nil
}

// driverCalls reads which tools the driver calls and which arguments it
// sets, from the syntax tree rather than from a list.
func driverCalls(dir string) (map[string]map[string]bool, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, 0, err
		}
		files = append(files, file)
	}
	out := map[string]map[string]bool{}
	for _, entry := range stepLiterals(files, "step") {
		if lit, ok := entry.(*ast.CompositeLit); ok {
			record(out, lit)
		}
	}
	return out, len(out), nil
}

// stepLiterals is every entry of typ the files build: each literal of the
// type, and each element of a []typ literal. An element has its type
// elided, which is how nearly every step is written, and reading only
// the typed spelling found one step out of nineteen. An element may also
// be a call that returns one; one written with its type is a literal of
// its own.
func stepLiterals(files []*ast.File, typ string) []ast.Expr {
	var out []ast.Expr
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			switch {
			case !ok:
			case docsIsIdent(lit.Type, typ):
				out = append(out, lit)
			case isSliceOf(lit.Type, typ):
				for _, elt := range lit.Elts {
					if inner, ok := elt.(*ast.CompositeLit); !ok || inner.Type == nil {
						out = append(out, elt)
					}
				}
			}
			return true
		})
	}
	return out
}

// isSliceOf reports whether e is []name.
func isSliceOf(e ast.Expr, name string) bool {
	arr, ok := e.(*ast.ArrayType)
	return ok && arr.Len == nil && docsIsIdent(arr.Elt, name)
}

// record pulls the tool and its argument keys out of one step literal.
func record(out map[string]map[string]bool, lit *ast.CompositeLit) {
	tool, args := "", []string{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "tool":
			tool = stringLit(kv.Value)
		case "args":
			args = mapKeysOf(kv.Value)
		}
	}
	if tool == "" {
		return
	}
	if out[tool] == nil {
		out[tool] = map[string]bool{}
	}
	for _, a := range args {
		out[tool][a] = true
	}
}

// mapKeysOf reads the string keys of a map literal.
func mapKeysOf(e ast.Expr) []string {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var keys []string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if s, err := strconv.Unquote(exprText(kv.Key)); err == nil {
			keys = append(keys, s)
		}
	}
	return keys
}

func exprText(e ast.Expr) string {
	if lit, ok := e.(*ast.BasicLit); ok {
		return lit.Value
	}
	return ""
}
