package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// `make check` says on its own first line that it is what CI runs, and
// three times now it has not been.
//
// The Makefile ran `go vet -tags=live` and CI did not, so nothing in CI
// compiled the live driver or the spikes — `go list ./scripts/livesheet`
// returns a single stub file, and a change that broke the driver stayed
// green until somebody tried to run it, which is the step that closes a
// phase. CI scanned for secrets with gitleaks and the Makefile had no
// such target, so the drift ran the other way too. And `go mod tidy` was
// in neither, running only inside a release, in the one form that
// writes.
//
// Two hand-maintained lists that have to agree is the shape this
// repository keeps writing gates against, so it gets one of its own.
// Adding a check means adding a row to the table below; leaving it out
// of either list fails here.

// parityCheck is one thing `make check` does, and how to recognise it in
// the CI workflow.
type parityCheck struct {
	// target is the Makefile target named in `check:`.
	target string
	// runs is a substring of the CI step that performs it. A substring
	// rather than an exact line, because CI writes some of these as an
	// action and some as a `run:`.
	runs string
	// why is printed when the check is missing on one side.
	why string
}

// parityChecks is the mapping. A gates subcommand does not need a row —
// those are derived from the dispatcher below — but every other check
// does.
var parityChecks = []parityCheck{
	{target: "fmt", runs: "gofmt -l .", why: "formatting"},
	{target: "vet", runs: "go vet ./...", why: "vet"},
	{target: "vet", runs: "go vet -tags=live ./...", why: "vet of the build-tagged driver and spikes"},
	{target: "tidy", runs: "go mod tidy -diff", why: "go.mod is what tidy would write"},
	{target: "lint", runs: "golangci-lint", why: "the linter"},
	{target: "vuln", runs: "govulncheck", why: "known vulnerabilities"},
	{target: "licenses", runs: "go-licenses", why: "the licence allow-list"},
	{target: "secrets", runs: "gitleaks", why: "the secret scanner"},
}

// gateTargets maps a gates subcommand to the Makefile target that runs
// it, where the two names differ.
var gateTargets = map[string]string{
	"coverage": "cover",
}

// precommitOnly are gates a hook runs rather than CI. Each needs a
// reason, the same way the coverage exemptions do.
var precommitOnly = map[string]string{
	"precommit": "the pre-commit hook's own entry point; it runs the other gates rather than being one",
	"leaks":     "run by CI and by `make check` under its own target; the history sweep is manual (§17a)",
	"mcpb-pack": "runs at release time, as the universal binary's post hook, where the binaries it packs " +
		"exist; `mcpb` is the half that runs on every commit, against the names it will pack",
}

func parityGate() error {
	makefile, workflow, err := readBothLists()
	if err != nil {
		return err
	}
	if err := checkParity(makefile, workflow); err != nil {
		return err
	}
	subcommands, _ := gateSubcommands()
	fmt.Printf("%d check(s) and %d gate(s) run in both `make check` and CI\n", len(parityChecks), len(subcommands))
	return nil
}

// readBothLists loads the two files that have to agree.
func readBothLists() (makefile, workflow string, err error) {
	m, err := os.ReadFile("Makefile")
	if err != nil {
		return "", "", err
	}
	ci, err := os.ReadFile(filepath.Join(".github", "workflows", "ci.yml"))
	if err != nil {
		return "", "", err
	}
	return string(m), string(ci), nil
}

// uncommented drops lines whose first non-blank character starts a
// comment. Only whole lines: a trailing comment sits after real content
// on the same line, and cutting there would need to know about quoting.
func uncommented(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// stepKey matches the start of a workflow step's `run:` or `uses:`, with
// or without the leading list dash.
var stepKey = regexp.MustCompile(`^\s*-?\s*(run|uses):`)

// executableLines keeps only what a workflow actually executes.
//
// A `run:` block is a YAML scalar that continues over indented lines, so
// once one starts, its continuation lines are kept until something less
// indented appears. That is not a YAML parser and does not need to be:
// it has to be right about which lines carry commands, and a command
// that runs is always inside one of these two keys.
func executableLines(workflow string) string {
	var kept []string
	inRun, runIndent := false, 0
	for _, line := range strings.Split(uncommented(workflow), "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case stepKey.MatchString(line):
			kept = append(kept, line)
			inRun, runIndent = strings.Contains(line, "run:"), indent
		case inRun && strings.TrimSpace(line) == "":
			kept = append(kept, line)
		case inRun && indent > runIndent:
			kept = append(kept, line)
		default:
			inRun = false
		}
	}
	return strings.Join(kept, "\n")
}

// checkParity is the comparison, over the two files' text rather than
// their paths, so a test can hand it a version with one check removed
// and watch this fail.
func checkParity(makefile, workflow string) error {
	// Whole-line comments go first, and here rather than in the reader,
	// so nothing can reach the comparison without passing through it.
	//
	// This gate matches by substring, and a substring is found inside a
	// comment as readily as inside a step: "gates staleness" was found
	// in "# - run: go run ./scripts/gates staleness" and counted as
	// running. That made the cheapest way to unblock a red build — one
	// "#" — also the way to switch off the check that would have
	// noticed. Reported by a sibling repository on 2026-09-06, and
	// present here when it was looked for.
	//
	// A step disabled by an `if:` that is never true still passes. That
	// needs a YAML parse, which is not worth a dependency here.
	makefile = uncommented(makefile)
	// The workflow is reduced to the lines that actually execute
	// something, rather than merely scrubbed of comments.
	//
	// This gate matches by substring, and a substring is found anywhere:
	// "gates staleness" was found inside "# - run: go run ./scripts/gates
	// staleness" and counted as running, which made the cheapest way to
	// unblock a red build — one "#" — also the way to switch off the
	// check that would have noticed. Reported by a sibling repository on
	// 2026-09-06, and present here when it was looked for.
	//
	// Dropping comments alone fixes that one spelling. Keeping only the
	// `run:` and `uses:` scalars closes the class: a step's `name:`, a
	// `description:`, or a YAML string anywhere else can no longer stand
	// in for the step. A step disabled by an `if:` that is never true
	// still passes, which needs a YAML parse and is not worth a
	// dependency here.
	workflow = executableLines(workflow)
	targets, err := checkTargets(makefile)
	if err != nil {
		return err
	}

	var problems []string
	// Every mapped check is on both sides.
	for _, c := range parityChecks {
		if !targets[c.target] {
			problems = append(problems,
				fmt.Sprintf("`make check` does not run %q, which CI does (%s)", c.target, c.why))
		}
		if !strings.Contains(workflow, c.runs) {
			problems = append(problems,
				fmt.Sprintf("ci.yml does not run %q, which `make check` does (%s)", c.runs, c.why))
		}
	}

	// Every gates subcommand is run by both, or excused by name.
	subcommands, err := gateSubcommands()
	if err != nil {
		return err
	}
	if len(subcommands) < 8 {
		return fmt.Errorf("found %d gate subcommands; the dispatcher is not being read", len(subcommands))
	}
	for _, name := range subcommands {
		if reason, excused := precommitOnly[name]; excused {
			if reason == "" {
				problems = append(problems, fmt.Sprintf("%q is excused from parity with no reason", name))
			}
			continue
		}
		target := name
		if t, ok := gateTargets[name]; ok {
			target = t
		}
		if !targets[target] {
			problems = append(problems, fmt.Sprintf("`make check` does not run the %q gate", name))
		}
		if !strings.Contains(workflow, "gates "+name) {
			problems = append(problems, fmt.Sprintf("ci.yml does not run the %q gate", name))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("`make check` and ci.yml disagree, and the Makefile says they do not:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}

var checkTargetLine = regexp.MustCompile(`(?m)^check:\s*(.*?)(?:\s*##.*)?$`)

// checkTargets reads the prerequisites of the Makefile's `check` target.
func checkTargets(makefile string) (map[string]bool, error) {
	m := checkTargetLine.FindStringSubmatch(makefile)
	if m == nil {
		return nil, fmt.Errorf("no `check:` target in the Makefile")
	}
	out := map[string]bool{}
	for _, f := range strings.Fields(m[1]) {
		out[f] = true
	}
	if len(out) < 10 {
		return nil, fmt.Errorf("`check:` has %d prerequisites; the line is not being read", len(out))
	}
	return out, nil
}

// gateSubcommands reads the dispatcher's own case labels, so a gate
// added there is under this check from its first commit rather than when
// somebody remembers to list it.
func gateSubcommands() ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("scripts", "gates", "main.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Tag == nil {
			return true
		}
		// The one switch over os.Args[1].
		if !isArgsIndex(sw.Tag) {
			return true
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				name, err := strconv.Unquote(lit.Value)
				if err == nil {
					out = append(out, name)
				}
			}
		}
		return true
	})
	sort.Strings(out)
	return out, nil
}

// isArgsIndex reports whether the expression is os.Args[n], which is
// what the dispatcher switches on.
func isArgsIndex(e ast.Expr) bool {
	idx, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os" && sel.Sel.Name == "Args"
}
