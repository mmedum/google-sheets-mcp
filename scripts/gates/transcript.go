package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The live driver may reach standard output through exactly one
// redacting helper, and this reads its syntax tree to say so.
//
// §9.1 promises a transcript is safe to paste into a commit message.
// That promise held because every line happened to go through one
// function — not because anything stopped a step printing directly, and
// a step that did would be invisible in review. The same class of gap
// has now been found twice by hand: once here, where the redactor
// substituted only the values the driver created and let everything the
// API returned through, and once in a sibling, where a renderer started
// printing a person's name in a place the redactor had never been told
// about.
//
// So the rule is structural rather than remembered. It is the same
// argument as the fixtures being generated: a control that depends on
// noticing is not a control.
var (
	// printers reach the terminal on their own.
	printers = []string{"Print", "Printf", "Println"}
	// The Fprint* family is not listed, and does not need to be. Any of
	// them that reaches the terminal has to name os.Stdout to do it, and
	// naming os.Stdout is the rule below.
	//
	// This was the hole: the list above used to be justified by "Fprintf
	// to a caller-supplied writer does not reach the terminal", which is
	// true of a caller's writer and not of os.Stdout. One
	// fmt.Fprintln(os.Stdout, sheetTitle) would have put a title on the
	// terminal unredacted with the gate reporting nothing. A comment
	// that argues for a gap is worse than one that leaves it unexplained
	// — it buys the reader's agreement in advance, so the gate ends up
	// protected by its own prose from the person most likely to notice.
	// allowedIn are the functions permitted to call one: the redacting
	// helper itself, and the build-tag stub that runs when the driver is
	// not compiled in and has nothing to redact.
	//
	// It was three. `sec` printed the section header directly, which was
	// safe because the titles are literals — and that is the wrong kind
	// of safe: it made this list "functions allowed to reach the
	// terminal" rather than "everything reaching the terminal is
	// redacted", so a later sec(someSheetTitle) would have been blessed
	// by name. A sibling hit the same shape one level down, where an
	// allowlist of safe *expressions* would have blessed a call whose
	// value happened to be harmless today. The narrower the list, the
	// less there is to launder.
	allowedIn = []string{"line", "main"}
)

// transcriptGate checks the live driver and the spike probes.
func transcriptGate() error {
	var problems []string
	files, prints := 0, 0

	// The evals are here for the same reason and with more at stake: an
	// eval transcript carries a model's prompts, the arguments it chose
	// and the text of every result it read, and it is meant to be quoted.
	for _, dir := range []string{
		filepath.Join("scripts", "livesheet"),
		filepath.Join("scripts", "spikes"),
		filepath.Join("scripts", "evals"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			files++
			for _, found := range printsOutside(fset, file, allowedIn) {
				prints++
				problems = append(problems, path+": "+found)
			}
		}
	}

	// Zero findings and zero inputs must not read the same.
	if files < 4 {
		return fmt.Errorf("parsed %d files under scripts/; the check is not reading the drivers", files)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d print(s) bypassing the redacting helper:\n  %s\n"+
			"every transcript line goes through line(), or §9.1's promise that a transcript is safe to paste is a hope",
			len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d driver files: every print goes through the redacting helper\n", files)
	return nil
}

// printsOutside reports fmt.Print* calls made from a function that is
// not allowed to make them.
func printsOutside(fset *token.FileSet, file *ast.File, allowed []string) []string {
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || slices.Contains(allowed, fn.Name.Name) {
			continue
		}
		// Mentions, not calls.
		//
		// A rule about calls is bypassed by a value: `p := fmt.Println;
		// p(x)` is a call to p, and `w := os.Stdout; fmt.Fprintln(w, x)`
		// is a call whose first argument is an identifier. Both reach
		// the terminal and neither is a call to anything this could
		// match. Matching the *mention* catches them at the assignment
		// instead, and needs no type information — which is what a
		// sibling repository pointed out after this gate had recorded
		// the gap as needing a type checker.
		//
		// What it still cannot see is a writer arriving as a parameter
		// from outside the package. Written down rather than implied.
		ast.Inspect(fn, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			what := pkg.Name + "." + sel.Sel.Name
			switch {
			// os.Stdout named at all: assigned, passed, or handed to a
			// child process, every one of which puts unredacted bytes
			// on the terminal.
			case pkg.Name == "os" && sel.Sel.Name == "Stdout":
			case pkg.Name == "fmt" && slices.Contains(printers, sel.Sel.Name):
			default:
				return true
			}
			out = append(out, fmt.Sprintf("%s names %s at %s", fn.Name.Name, what, fset.Position(sel.Pos())))
			return true
		})
	}
	return out
}
