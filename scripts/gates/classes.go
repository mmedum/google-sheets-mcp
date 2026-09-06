package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// classGate holds the error vocabulary closed, from both sides.
//
// This is built in phase 0 rather than left as a later cleanup because
// it costs an afternoon and it is the difference between a vocabulary
// and a habit. In a sibling repository the equivalent check found four
// classes that appeared in no document at all, and the list had been
// living in comments in two packages where nothing could disagree with
// it.
//
// Both directions matter. A class emitted and not declared is a word the
// model has to learn from a message; a class declared and never emitted
// is documentation of something that does not happen.
// plannedClasses are declared in the vocabulary and not emitted yet,
// each with the phase that will emit it.
//
// The vocabulary is written down whole because a model should not learn
// a class twice, and these three belong to code this phase has not
// written. An entry here is a decision somebody made and a reviewer can
// see; without the list the gate would either pass on a vocabulary
// nothing holds, or fail on a plan that is going as intended.
var plannedClasses = map[string]string{
	"blocked":     "the write guard refusing what the API would allow, in phase 1 with write_values",
	"conflict":    "a checkpoint mismatch on a write, in phase 1",
	"unsupported": "a capability the API or this configuration lacks, first met in phase 2",
}

func classGate() error {
	declared := service.Classes()
	emitted, files, err := emittedClasses()
	if err != nil {
		return err
	}
	// Everything gapi.Class can return reaches a tool through wrap(), so
	// it counts as emitted whether or not a literal spells it out.
	for _, c := range gapi.Classes() {
		emitted[c] = append(emitted[c], "internal/gapi (returned by Class)")
	}

	if files < 15 {
		return fmt.Errorf("read %d files under internal/; the scan is not looking at the tree", files)
	}

	var problems []string
	for _, c := range slices.Sorted(maps.Keys(emitted)) {
		if !slices.Contains(declared, c) {
			problems = append(problems, fmt.Sprintf("%q is emitted at %s but is not in service.Classes()",
				c, strings.Join(dedupe(emitted[c]), ", ")))
		}
	}
	for _, c := range declared {
		if _, ok := emitted[c]; ok {
			if reason, planned := plannedClasses[c]; planned {
				problems = append(problems, fmt.Sprintf("%q is emitted now, so remove it from plannedClasses (%s)", c, reason))
			}
			continue
		}
		reason, planned := plannedClasses[c]
		if !planned {
			problems = append(problems, fmt.Sprintf("%q is declared in service.Classes() and no code emits it", c))
			continue
		}
		fmt.Printf("%-20s not emitted yet: %s\n", c, reason)
	}
	if len(problems) > 0 {
		return fmt.Errorf("the error vocabulary and the code disagree:\n  %s", strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d classes declared, %d of them planned for a later phase, checked against %d files\n",
		len(declared), len(plannedClasses), files)
	return nil
}

// emittedClasses finds every class name the code passes as an error
// class, by reading the syntax tree rather than by grepping for a
// pattern somebody hoped was complete.
func emittedClasses() (map[string][]string, int, error) {
	out := map[string][]string{}
	files := 0
	fset := token.NewFileSet()

	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("%s: %w", path, perr)
		}
		files++
		// Inside the service package the constructor is spelled bare;
		// everywhere else it is qualified. fmt.Errorf is neither, and
		// counting it would fill the vocabulary with format strings.
		inService := strings.HasPrefix(filepath.ToSlash(path), "internal/service/")
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if isServiceRef(node.Fun, "Errorf", inService) && len(node.Args) > 0 {
					if c := stringLit(node.Args[0]); c != "" {
						out[c] = append(out[c], path)
					}
				}
			case *ast.CompositeLit:
				if !isServiceRef(node.Type, "Error", inService) {
					return true
				}
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Class" {
						if c := stringLit(kv.Value); c != "" {
							out[c] = append(out[c], path)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	return out, files, err
}

// isServiceRef reports whether e names service.<want>, or a bare <want>
// inside the service package itself.
func isServiceRef(e ast.Expr, want string, inService bool) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return inService && v.Name == want
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == "service" && v.Sel.Name == want
	case *ast.UnaryExpr:
		return isServiceRef(v.X, want, inService)
	}
	return false
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

func dedupe(xs []string) []string {
	out := slices.Clone(xs)
	slices.Sort(out)
	return slices.Compact(out)
}
