package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The API coverage record, and the gate that holds it.
//
// §16 asks that every member of the Sheets `batchUpdate` union is either
// used or written down as deliberately out. Phase 4 answered that in
// prose, which is the shape §17a.26 already warns about: a claim a gate
// does not hold is a claim that goes stale the first time somebody adds
// a builder. This holds it.
//
// Two files, split by which of them a person writes:
//
//   - testdata/api-surface.json is what Google publishes, written by
//     `gates api-diff` from the discovery documents. Nobody edits it.
//   - testdata/api-coverage.tsv is one verdict per item, by hand.
//
// `gates api-coverage` runs offline in `make check` and holds the two
// files and the code to each other, four ways: a published item with no
// verdict fails, a verdict for an item that no longer exists fails, a
// thing the code builds and the record calls `out` fails, and a thing
// the record calls `used` that no code builds fails.
//
// `gates api-diff` refetches and rewrites the snapshot. It stays manual:
// a gate that fails when Google is slow is one people learn to re-run
// until it passes. CI reads the snapshot rather than the network, which
// is the difference between a completeness claim that is checked and one
// that is only checkable.
//
// One row may stand for a whole API's remainder, and only one way: a
// `*` name with the verdict `out`. Drive publishes sixty-four methods
// and this server calls three, and its position on the other sixty-one
// is categorical rather than per-method — file management belongs to a
// server built on the Drive API (§1, §17.6). Sixty-one copies of that
// sentence would be a record nobody reads. A wildcard may never say
// `used`: claiming a whole API is called is a claim no code backs, and
// the gate still fails if the client calls something the wildcard
// covers. The count it absorbs is printed, so a surface that grows
// behind it is visible rather than silent.
//
// Sheets has no wildcard, and that is where §16's requirement lives:
// every method and every one of the sixty-nine request kinds is judged
// by name.
//
// **A row is a request kind or a method, and the record says which.**
// That distinction is the whole shape of this API: Sheets publishes
// thirteen methods this server could call and sixty-nine request kinds
// inside one of them, and the union is where Google adds capability. A
// record keyed only on methods would call this server complete while
// missing every chart, filter and pivot request in the API.
const (
	apiSurfacePath  = "testdata/api-surface.json"
	apiCoveragePath = "testdata/api-coverage.tsv"
)

// Item kinds.
const (
	kindMethod  = "method"
	kindRequest = "request"
)

// Verdicts.
const (
	verdictUsed = "used"
	verdictOut  = "out"
)

// apiSurface is the machine-written half: what the discovery documents
// publish, as of a stated date.
type apiSurface struct {
	Fetched string    `json:"fetched"`
	Note    string    `json:"note"`
	APIs    []apiDoc  `json:"apis"`
	Items   []apiItem `json:"items"`
}

// apiDoc is one discovery document and the revision it was read at.
type apiDoc struct {
	API      string `json:"api"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
	URL      string `json:"url"`
}

// apiItem is one method or one request kind.
//
// Verb and path live here, in the generated file, rather than as columns
// somebody types: a name that stays put while its path moves is a break,
// and the only way to see it is to have recorded the path from the
// document rather than from memory.
type apiItem struct {
	API  string `json:"api"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Verb string `json:"verb,omitempty"`
	Path string `json:"path,omitempty"`
}

func (i apiItem) key() string { return i.API + "\t" + i.Kind + "\t" + i.Name }

// coverageRow is the hand-written half: one verdict, with its reason.
type coverageRow struct {
	API     string
	Kind    string
	Name    string
	Verdict string
	// Reason names the code that implements it for `used`, and says why
	// it is not called for `out`. Either way it is a sentence somebody
	// wrote, which is the point of the file.
	Reason string
	Line   int
}

func (r coverageRow) key() string { return r.API + "\t" + r.Kind + "\t" + r.Name }

// apiCoverageGate is the offline check.
func apiCoverageGate() error {
	surface, err := readSurface()
	if err != nil {
		return err
	}
	rows, err := readCoverage()
	if err != nil {
		return err
	}
	if len(surface.Items) == 0 {
		return fmt.Errorf("%s lists nothing, so this gate is reading nothing; run `gates api-diff`", apiSurfacePath)
	}

	// The code's own answer: what internal/plan builds and what
	// internal/gapi calls, which is the half that goes stale on its own.
	built, err := builtRequests()
	if err != nil {
		return err
	}
	called, err := calledMethods()
	if err != nil {
		return err
	}

	problems := judge(surface.Items, rows, built, called)
	sort.Strings(problems)
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	reportCoverage(surface, rows)
	return nil
}

// reportCoverage prints what was judged, and what a wildcard stood for.
func reportCoverage(surface *apiSurface, rows []coverageRow) {
	used, out := 0, 0
	absorbed := map[string]int{}
	named := map[string]bool{}
	for _, r := range rows {
		switch {
		case r.Name == "*":
		case r.Verdict == verdictUsed:
			used++
			named[r.key()] = true
		default:
			out++
			named[r.key()] = true
		}
	}
	for _, it := range surface.Items {
		if !named[it.key()] {
			absorbed[it.API+" "+it.Kind]++
		}
	}
	fmt.Printf("%d of %d published API item(s) named: %d used, %d deliberately out, against %s fetched %s\n",
		used+out, len(surface.Items), used, out, apiSurfacePath, surface.Fetched)
	for _, k := range sortedKeysOf(absorbed) {
		fmt.Printf("  %d further %s item(s) covered by one `*` row\n", absorbed[k], k)
	}
}

// judge is the whole comparison, as a function of what was read rather
// than of what is on disk — so every way it can fail can be watched
// failing, which is what makes it a gate rather than a hope.
func judge(items []apiItem, rows []coverageRow, built, called map[string]bool) []string {
	calledOrBuilt := func(it apiItem) bool {
		if it.Kind == kindMethod {
			return called[it.Name]
		}
		return built[it.Name]
	}

	var problems []string
	byKey := make(map[string]coverageRow, len(rows))
	for _, r := range rows {
		if prev, ok := byKey[r.key()]; ok {
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s has a second verdict; the first is on line %d",
				apiCoveragePath, r.Line, r.Kind, r.Name, prev.Line))
			continue
		}
		byKey[r.key()] = r
	}

	// The wildcards, and the rule that keeps them honest.
	wild := map[string]coverageRow{}
	for _, r := range rows {
		if r.Name != "*" {
			continue
		}
		if r.Verdict != verdictOut {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: a `*` row may only say %s; claiming a whole API is used is a claim no code backs",
				apiCoveragePath, r.Line, verdictOut))
			continue
		}
		wild[r.API+"\t"+r.Kind] = r
	}
	// Published and unjudged: the half that makes this a completeness
	// claim rather than a list of what somebody happened to think of.
	published := make(map[string]apiItem, len(items))
	for _, it := range items {
		published[it.key()] = it
		if _, ok := byKey[it.key()]; ok {
			continue
		}
		if w, ok := wild[it.API+"\t"+it.Kind]; ok {
			// A wildcard that covers something the client calls is the
			// one way this could hide real coverage, so it is refused
			// where it happens rather than trusted.
			if calledOrBuilt(it) {
				problems = append(problems, fmt.Sprintf(
					"%s %s.%s is covered by the `*` row on line %d and this repository uses it; give it a row of "+
						"its own saying what implements it", it.Kind, it.API, it.Name, w.Line))
			}
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s %s.%s is published and has no verdict; add a row to %s saying `used` with what implements it, "+
				"or `out` with why this server does not call it", it.Kind, it.API, it.Name, apiCoveragePath))
	}
	// A verdict for something that is gone. Google removes things, and a
	// record that keeps judging them reads as coverage of an API that no
	// longer exists.
	for _, r := range rows {
		if r.Name == "*" {
			continue
		}
		if _, ok := published[r.key()]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s %s.%s has a verdict and is not in %s; `gates api-diff` may have removed it",
				apiCoveragePath, r.Line, r.Kind, r.API, r.Name, apiSurfacePath))
		}
	}

	// And the code, which is the half that goes stale on its own.
	problems = append(problems, compareToCode(rows, built, called)...)
	return problems
}

// compareToCode holds the record against what the code actually does.
func compareToCode(rows []coverageRow, built, called map[string]bool) []string {
	var problems []string
	for _, r := range rows {
		if r.Name == "*" {
			continue
		}
		var does bool
		switch r.Kind {
		case kindRequest:
			does = built[r.Name]
		case kindMethod:
			does = called[r.Name]
		default:
			problems = append(problems, fmt.Sprintf("%s:%d: kind %q is not %s or %s",
				apiCoveragePath, r.Line, r.Kind, kindMethod, kindRequest))
			continue
		}
		switch {
		case r.Verdict == verdictUsed && !does:
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s %s is recorded as used and nothing in this repository %s it",
				apiCoveragePath, r.Line, r.Kind, r.Name, buildsOrCalls(r.Kind)))
		case r.Verdict == verdictOut && does:
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s %s is recorded as deliberately out and this repository %s it; "+
					"change the verdict to used and name what does",
				apiCoveragePath, r.Line, r.Kind, r.Name, buildsOrCalls(r.Kind)))
		case r.Verdict != verdictUsed && r.Verdict != verdictOut:
			problems = append(problems, fmt.Sprintf("%s:%d: verdict %q is not %s or %s",
				apiCoveragePath, r.Line, r.Verdict, verdictUsed, verdictOut))
		case r.Reason == "":
			problems = append(problems, fmt.Sprintf("%s:%d: %s %s has a verdict and no reason",
				apiCoveragePath, r.Line, r.Kind, r.Name))
		}
	}
	// The other direction: something the code does that the record has
	// no row for at all. Without this the gate is satisfied by a record
	// that judges half the API and ignores the rest of the client.
	for name := range built {
		if !anyRow(rows, kindRequest, name) {
			problems = append(problems, fmt.Sprintf(
				"internal/plan builds the request %s and %s has no row for it", name, apiCoveragePath))
		}
	}
	for name := range called {
		if !anyRow(rows, kindMethod, name) {
			problems = append(problems, fmt.Sprintf(
				"internal/gapi calls the method %s and %s has no row for it", name, apiCoveragePath))
		}
	}
	return problems
}

func buildsOrCalls(kind string) string {
	if kind == kindMethod {
		return "calls"
	}
	return "builds"
}

func anyRow(rows []coverageRow, kind, name string) bool {
	for _, r := range rows {
		if r.Kind == kind && r.Name == name {
			return true
		}
	}
	return false
}

// builtRequests are the union members some plan builder constructs.
//
// The builders rather than the wire struct's fields: a field declared
// and never built is a member this server cannot send, and calling that
// coverage would be the same overstatement the prose version made.
//
// Read from the syntax tree, which is how five other gates here read the
// code. Reflection would need a type this package could import and a
// rule about which methods count; the AST needs neither and sees a
// composite literal wherever it is written.
func builtRequests() (map[string]bool, error) {
	fields, err := requestFields()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	err = walkGo("internal/plan", func(_ string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Request" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "gsheets" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if name, ok := fields[key.Name]; ok {
					out[name] = true
				}
			}
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no batchUpdate request is built anywhere in internal/plan, which cannot be true")
	}
	return out, nil
}

// requestFields maps the union struct's Go field names to the JSON names
// the API publishes, so the two halves of this gate speak one language.
func requestFields() (map[string]string, error) {
	out := map[string]string{}
	err := walkGo("internal/gsheets", func(_ string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Request" {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				if f.Tag == nil || len(f.Names) == 0 {
					continue
				}
				tag, err := strconv.Unquote(f.Tag.Value)
				if err != nil {
					continue
				}
				name, _, _ := strings.Cut(jsonTag(tag), ",")
				if name != "" {
					out[f.Names[0].Name] = name
				}
			}
			return false
		})
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the gsheets.Request union has no fields, which cannot be true")
	}
	return out, nil
}

// jsonTag pulls the json value out of a struct tag.
func jsonTag(tag string) string {
	for _, part := range strings.Fields(tag) {
		if rest, ok := strings.CutPrefix(part, `json:`); ok {
			if v, err := strconv.Unquote(rest); err == nil {
				return v
			}
		}
	}
	return ""
}

// calledMethods are the API methods the client names.
//
// Every call in internal/gapi carries its method in an `op` field, which
// exists so a log line and an error can say which call failed. That
// makes it the one place the client states what it is calling, and this
// reads it rather than guessing from a URL built by concatenation.
func calledMethods() (map[string]bool, error) {
	out := map[string]bool{}
	err := walkGo("internal/gapi", func(path string, file *ast.File) {
		if strings.HasSuffix(path, "_test.go") || strings.Contains(path, "sheetstest") {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "op" {
				return true
			}
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil {
					out[v] = true
				}
			}
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no API method is named anywhere in internal/gapi, which cannot be true")
	}
	return out, nil
}

// walkGo parses every Go file under a directory.
func walkGo(dir string, fn func(path string, file *ast.File)) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fn(path, file)
		return nil
	})
}

func readSurface() (*apiSurface, error) {
	body, err := os.ReadFile(apiSurfacePath)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read; run `gates api-diff` to write it: %w", apiSurfacePath, err)
	}
	var s apiSurface
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("%s is not JSON: %w", apiSurfacePath, err)
	}
	return &s, nil
}

// readCoverage parses the hand-written verdicts.
func readCoverage() ([]coverageRow, error) {
	body, err := os.ReadFile(apiCoveragePath)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", apiCoveragePath, err)
	}
	var out []coverageRow
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("%s:%d: %d column(s), want 5 tab-separated: api, kind, name, verdict, reason",
				apiCoveragePath, i+1, len(parts))
		}
		out = append(out, coverageRow{
			API: strings.TrimSpace(parts[0]), Kind: strings.TrimSpace(parts[1]),
			Name: strings.TrimSpace(parts[2]), Verdict: strings.TrimSpace(parts[3]),
			Reason: strings.TrimSpace(parts[4]), Line: i + 1,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s judges nothing, so this gate is reading nothing", apiCoveragePath)
	}
	return out, nil
}

func sortedKeysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
