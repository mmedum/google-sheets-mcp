package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v4/internal/auth"
	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
)

// The README's tool table.
var readmeTool = regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")

// schemaDump is the shape of `google-sheets-mcp --dump-schemas`.
type schemaDump struct {
	Tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"inputSchema"`
		OutputSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"outputSchema"`
	} `json:"tools"`
}

// scopesAreDocumented checks the README lists every scope `login` asks
// for, in full.
//
// A setup step that says "add the two scopes below" with no scope in the
// file leaves a reader following the instructions nothing to add, and
// only a gate that compares the setup instructions with the code
// catches it.
//
// The full URL, because that is what somebody pastes into a consent
// screen. A bare "spreadsheets" in a table column is not the string the
// Cloud console takes.
func scopesAreDocumented() []string {
	body, err := os.ReadFile("README.md")
	if err != nil {
		return []string{"README.md cannot be read to check its scopes: " + err.Error()}
	}
	// Every combination, not the default one: a scope this server asks
	// for only under a setting is still a scope somebody with an
	// External app has to add in the Cloud console before login works,
	// and the README is where they find out.
	want := map[string]bool{}
	for _, readOnly := range []bool{false, true} {
		for _, dataSources := range []bool{false, true} {
			for _, scope := range auth.Scopes(readOnly, dataSources) {
				want[scope] = true
			}
		}
	}
	if len(want) < 2 {
		return []string{"auth.Scopes returned fewer than two distinct scopes; this check is reading nothing"}
	}
	var problems []string
	for scope := range want {
		if !strings.Contains(string(body), scope) {
			problems = append(problems, "README.md does not list the scope "+scope+", which `login` requests")
		}
	}
	sort.Strings(problems)
	return problems
}

// staleness fails when the documentation drifts from the code.
//
// Each check answers a question somebody asked once and then stopped
// asking: does the README list the tools that exist, the scopes login
// asks for and a permission rule for every delete, does the configuration
// document mention every setting, and does the CHANGELOG say what
// changed.
func staleness(bin string) error {
	var problems []string
	// First, so a git that cannot answer stops the gate before it reads
	// anything else.
	if err := changelogDocumentsTheChange(); errors.As(err, new(couldNotRun)) {
		return err
	} else if err != nil {
		problems = append(problems, err.Error())
	}

	_, dump, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	registered := make([]string, 0, len(dump.Tools))
	var deletes []string
	for _, t := range dump.Tools {
		registered = append(registered, t.Name)
		// A delete is a tool that takes confirm: the five gated ones, read
		// from the binary rather than listed here.
		if _, ok := t.InputSchema.Properties["confirm"]; ok {
			deletes = append(deletes, t.Name)
		}
		if t.Description == "" {
			problems = append(problems, t.Name+" has no description")
		}
		// A description that teaches the model to guess a sheet name is
		// a documentation bug that becomes a runtime failure on somebody
		// else's account.
		if strings.Contains(t.Description, "Sheet1") {
			problems = append(problems, t.Name+"'s description offers Sheet1, which does not exist on a non-English account")
		}
	}
	sort.Strings(registered)
	if len(registered) == 0 {
		return fmt.Errorf("the binary registered no tools; this check is not looking at a server")
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	var documented []string
	for _, m := range readmeTool.FindAllStringSubmatch(string(readme), -1) {
		documented = append(documented, m[1])
	}
	sort.Strings(documented)
	if missing := difference(registered, documented); len(missing) > 0 {
		problems = append(problems, "README's tool table is missing: "+strings.Join(missing, ", "))
	}
	if extra := difference(documented, registered); len(extra) > 0 {
		problems = append(problems, "README's tool table names tools that are not registered: "+strings.Join(extra, ", "))
	}
	sort.Strings(deletes)
	problems = append(problems, permissionRulesAreTools(string(readme), registered, deletes)...)

	conf, err := os.ReadFile(configDocPath)
	if err != nil {
		return err
	}
	vars := config.Vars()
	problems = append(problems, configDocProblems(vars, fieldRules, string(conf))...)

	problems = append(problems, scopesAreDocumented()...)

	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d tools documented, %d settings documented\n", len(registered), len(vars))
	return nil
}

// configDocPath is the page every setting is documented on.
const configDocPath = "docs/configuration.md"

// configDocProblems holds the configuration page, text, to the variables
// the server reads, vars, both ways. It names every one as a whole word,
// the removed one it refuses and the two read outside Define included,
// and names no other. And it states each of rules, the bundle's
// fieldRules, in the words the server refuses a value with, so a rule
// added for the bundle's field is held on the page too.
func configDocProblems(vars []config.Var, rules map[string]fieldRule, text string) []string {
	var problems []string
	documented := map[string]bool{}
	for _, m := range settingWord.FindAllStringSubmatch(text, -1) {
		documented[m[2]] = true
	}
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
		if !documented[v.Name] {
			problems = append(problems, fmt.Sprintf("%s does not document %s", configDocPath, v.Name))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		if !known[name] {
			problems = append(problems, fmt.Sprintf("%s documents %s, which the code does not read", configDocPath, name))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(rules)) {
		if rule := rules[name]; !strings.Contains(text, rule.words) {
			problems = append(problems, fmt.Sprintf("%s does not %s %q", configDocPath, rule.fails, rule.words))
		}
	}
	return problems
}

// settingWord is a setting's name standing as a word of its own, all of
// it: a longer name that contains one, such as GSHEETS_READ_ONLY_X, is
// that longer name and not GSHEETS_READ_ONLY.
var settingWord = regexp.MustCompile(`(^|[^A-Z0-9_])(` + config.EnvPrefix + `[A-Z0-9_]+)`)

// permissionRule is a Claude Code permission rule for one of this
// server's tools, under the server name the README uses.
var permissionRule = regexp.MustCompile(`"mcp__google-sheets__([a-z0-9_]+)"`)

// permissionRulesAreTools holds the README's Claude Code permission rules
// to the registered tools. A rule for a tool that does not exist matches
// nothing, so the person it was meant to ask is never asked. It also
// holds that every delete has a rule, since per-call approval is the
// client's and these rules are how a person asks to approve each delete:
// a README that lost its block, or one delete from it, would pass a check
// over its names alone.
func permissionRulesAreTools(readme string, registered, deletes []string) []string {
	if len(deletes) == 0 {
		return []string{"the binary lists no tool that takes confirm; this check is not looking at the deletes"}
	}
	var problems, ruled []string
	for _, m := range permissionRule.FindAllStringSubmatch(readme, -1) {
		ruled = append(ruled, m[1])
		if !slices.Contains(registered, m[1]) {
			problems = append(problems, "README.md gives a permission rule for "+m[1]+", which the binary does not register")
		}
	}
	for _, name := range deletes {
		if !slices.Contains(ruled, name) {
			problems = append(problems, "README.md gives no permission rule for "+name+", which deletes")
		}
	}
	return problems
}

// dumpSchemas returns the raw dump and the parsed one, so a caller that
// needs both does not run the binary twice. It runs with nowhere to find
// a setting, as the smoke test does, so a GSHEETS_ variable in the
// maintainer's shell cannot change the surface a gate reads or records.
func dumpSchemas(bin string) ([]byte, *schemaDump, error) {
	cmd := exec.Command(bin, "--dump-schemas")
	smokeEnv(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	var d schemaDump
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, nil, fmt.Errorf("parse --dump-schemas: %w", err)
	}
	return out, &d, nil
}

// changelogDocumentsTheChange requires entries when the binary's source,
// as shippedChangedSince reads it, changed since the newest release. A
// test, a fixture, a script under scripts/ and a dependency bump change
// nothing an entry would describe, so a change confined to them needs none.
//
// The exception in the middle is a scar: a release commit moves the
// entries out of [Unreleased] and under the version it is about to tag,
// and CI runs before that tag can exist — so a gate without it fails on
// the one pull request it was written to guard.
func changelogDocumentsTheChange() error {
	list, err := git("tag")
	if err != nil {
		return couldNotRun{err}
	}
	tags := strings.Fields(list)
	since, last := "HEAD~1", stalenessNewestRelease(tags)
	if last != "" {
		since = "v" + last
	}
	changed, err := shippedChangedSince(since)
	if err != nil {
		return couldNotRun{err}
	}
	if !changed {
		return nil
	}

	changelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	doc := readChangelog(string(changelog))
	if doc.entries("Unreleased") > 0 {
		return nil
	}
	if v := doc.versions(); len(v) > 0 && !slices.Contains(tags, "v"+v[0].label) && doc.entries(v[0].label) > 0 {
		return nil
	}
	if last == "" {
		since = "the previous commit"
	}
	return fmt.Errorf("the binary's source changed since %s but %s documents nothing new; "+
		"put the entries under [Unreleased], or under the version heading this release is about to tag", since, changelogPath)
}

// stalenessNewestRelease is the newest release tag's version by semver
// order, a prerelease included, or "" when there is none. git's version
// sort puts v2.0.0-rc.1 after v2.0.0, so the order is semver's.
func stalenessNewestRelease(tags []string) string {
	newest := ""
	for _, tag := range tags {
		v, ok := strings.CutPrefix(tag, "v")
		if ok && changelogVersion.MatchString(v) && (newest == "" || changelogCompare(v, newest) > 0) {
			newest = v
		}
	}
	return newest
}

// shippedChangedSince reports whether the binary's source changed between
// since and HEAD: a Go file other than a test in a package of this module
// the binary imports, a file such a package embeds, or go.mod outside its
// require and tool lines. A package counts by its directory, so a file
// another platform builds counts, and so does a file removed from it.
func shippedChangedSince(since string) (bool, error) {
	out, err := git("diff", since+"..HEAD", "--name-only", "--no-renames", "-z")
	if err != nil {
		return false, err
	}
	var shipped *shippedSource
	for _, file := range strings.Split(out, "\x00") {
		switch file {
		case "":
		case "go.mod":
			if changed, err := goModChangedSince(since); err != nil || changed {
				return changed, err
			}
		default:
			if shipped == nil {
				if shipped, err = listShipped(); err != nil {
					return false, err
				}
			}
			if shipped.has(file) {
				return true, nil
			}
		}
	}
	return false, nil
}

// shippedSource is what the binary is built from in this module, on this
// platform: its package directories and the files they embed, each
// slash-separated from the module root.
type shippedSource struct{ dirs, embeds map[string]bool }

func (s *shippedSource) has(file string) bool {
	return s.embeds[file] || strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") && s.dirs[path.Dir(file)]
}

// listShipped asks the go command what ./cmd/... is built from. A package
// of another module is a dependency, which go.mod's require lines name.
func listShipped() (*shippedSource, error) {
	const what = "go list -deps ./cmd/..."
	out, err := exec.Command("go", "list", "-deps", "-json=Dir,EmbedFiles,Module", "./cmd/...").Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return nil, fmt.Errorf("%s: %s", what, strings.TrimSpace(string(exit.Stderr)))
	case err != nil:
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	s := &shippedSource{dirs: map[string]bool{}, embeds: map[string]bool{}}
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p struct {
			Dir        string
			EmbedFiles []string
			Module     *struct {
				Main bool
				Dir  string
			}
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		dir := filepath.ToSlash(rel)
		s.dirs[dir] = true
		for _, f := range p.EmbedFiles {
			s.embeds[path.Join(dir, f)] = true
		}
	}
	// An empty listing is not reading the binary, and would excuse every
	// change.
	if len(s.dirs) == 0 {
		return nil, fmt.Errorf("%s named no package of this module, so it is not reading the binary", what)
	}
	return s, nil
}

// goModChangedSince reports whether go.mod changed between since and HEAD
// outside its require and tool lines. A dependency bump needs no entry
// (CONTRIBUTING.md#changelog); a go, toolchain or replace line changes what
// the binary is built with.
func goModChangedSince(since string) (bool, error) {
	before, err := git("show", since+":go.mod")
	if err != nil {
		return false, err
	}
	after, err := git("show", "HEAD:go.mod")
	if err != nil {
		return false, err
	}
	return goModBuildLines(before) != goModBuildLines(after), nil
}

// goModBuildLines is a go.mod's lines, each trimmed and the blank ones
// dropped, without its require and tool directives, a block's included
// (https://go.dev/ref/mod#go-mod-file-require). A tool directive adds a
// package `go tool` runs (https://go.dev/ref/mod#go-mod-file-tool). ( is a
// token of its own, so `require(` opens a block as `require (` does
// (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren || len(fields) > 1 && strings.HasPrefix(fields[1], "(")
		default:
			kept = append(kept, strings.Join(fields, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// difference returns the members of a that are not in b.
func difference(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
