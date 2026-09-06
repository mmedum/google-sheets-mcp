package gapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// readOnlyMethods are the three POSTs in the Sheets API that only read.
// Two POSTs beside them do write — values.batchClearByDataFilter and
// values.batchUpdateByDataFilter — so neither the method nor the name
// can decide this: "get" reads like a read on a method that is a POST
// only because a filter is too long for a query string.
var readOnlyMethods = []string{
	"spreadsheets.getByDataFilter",
	"values.batchGetByDataFilter",
	"developerMetadata.search",
}

// TestReadOnlyFlagOnlyOnTheThreeReadingPOSTs reads this package's syntax
// tree rather than its call sites.
//
// The flag is the one field here that can be set *wrongly* rather than
// merely forgotten: absent from a search it costs speed, present on a
// real write it lets that write run during a preview. A comment cannot
// hold that shut, so this does.
func TestReadOnlyFlagOnlyOnTheThreeReadingPOSTs(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no source files parsed; the check is not reading anything")
	}

	literals, flagged := 0, 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "request" {
				return true
			}
			literals++
			fields := map[string]ast.Expr{}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					fields[key.Name] = kv.Value
				}
			}
			if !isTrue(fields["readOnly"]) {
				return true
			}
			flagged++
			op := stringLit(fields["op"])
			if op == "" {
				t.Errorf("%s: a request sets readOnly without a literal op; the allowed set is by name",
					fset.Position(lit.Pos()))
				return true
			}
			if !slices.Contains(readOnlyMethods, op) {
				t.Errorf("%s: %q is marked readOnly, but only %s may be. A POST that writes and is marked "+
					"readOnly would run during a dry run", fset.Position(lit.Pos()), op, strings.Join(readOnlyMethods, ", "))
			}
			return true
		})
	}

	// Zero findings and zero inputs must not look the same. Phase 0 has
	// no reading POST yet, so the count that matters is how many
	// requests were read at all.
	if literals < 4 {
		t.Fatalf("found %d request literals; the check is not reading the package", literals)
	}
	t.Logf("%d request literals, %d marked readOnly", literals, flagged)
}

func isTrue(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "true"
}

func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
