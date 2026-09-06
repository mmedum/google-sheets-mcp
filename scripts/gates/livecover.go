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

	"github.com/mmedum/google-sheets-mcp/internal/livecover"
)

// §13 says the live driver covers every tool and every op. That is a
// guarantee stated over a surface, and a guarantee stated over a surface
// decays every time the surface grows — in a sibling repository the
// equivalent promise had quietly stopped covering eight newer tools,
// including the only ones that took an email address.
//
// So it is checked rather than asserted, and against the schema the
// binary actually publishes rather than a list anybody maintains. When
// it first ran, the driver was exercising 14 of 28 tool options.
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
	out := map[string]map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, 0, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// Two spellings. A step built on its own carries its type;
			// the ones inside a []step slice have it elided, which is
			// how nearly every step here is written — and reading only
			// the first spelling found one step out of nineteen.
			if isSliceOf(lit.Type, "step") {
				for _, elt := range lit.Elts {
					if inner, ok := elt.(*ast.CompositeLit); ok && inner.Type == nil {
						record(out, inner)
					}
				}
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "step" {
				return true
			}
			record(out, lit)
			return true
		})
	}
	return out, len(out), nil
}

// isSliceOf reports whether e is []name.
func isSliceOf(e ast.Expr, name string) bool {
	arr, ok := e.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	id, ok := arr.Elt.(*ast.Ident)
	return ok && id.Name == name
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
