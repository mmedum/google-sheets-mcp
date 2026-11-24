package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v5/internal/auth"
	"github.com/mmedum/google-sheets-mcp/v5/internal/config"
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

// scopesAreDocumented checks the README's prose, readme, lists every scope
// `login` asks for, in full.
//
// A setup step that says "add the two scopes below" with no scope in the
// file leaves a reader following the instructions nothing to add, and
// only a gate that compares the setup instructions with the code
// catches it.
//
// The full URL, because that is what somebody pastes into a consent
// screen. A bare "spreadsheets" in a table column is not the string the
// Cloud console takes.
func scopesAreDocumented(readme string) []string {
	want := loginScopes()
	if len(want) < 2 {
		return []string{"auth.Scopes returned fewer than two distinct scopes; this check is reading nothing"}
	}
	var problems []string
	for _, scope := range want {
		if !strings.Contains(readme, scope) {
			problems = append(problems, "README.md does not list the scope "+scope+", which `login` requests")
		}
	}
	return problems
}

// loginScopes is every scope `login` can ask for, sorted, each once.
// Every combination, not the default one: a scope this server asks for
// only under a setting is still a scope somebody with an External app has
// to add in the Cloud console before login works, and the README is where
// they find out.
func loginScopes() []string {
	scopes := map[string]bool{}
	for _, readOnly := range []bool{false, true} {
		for _, dataSources := range []bool{false, true} {
			for _, scope := range auth.Scopes(readOnly, dataSources) {
				scopes[scope] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(scopes))
}

// staleness fails when the documentation drifts from the code.
//
// Each check answers a question somebody asked once and then stopped
// asking: does the README list the tools that exist, the scopes login
// asks for and a permission rule for every delete, does the configuration
// document mention every setting, and does the CHANGELOG say what
// changed. The README and the configuration page are read with their
// fences blanked, since an example documents nothing.
func staleness(bin string) error {
	var problems []string

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
	sort.Strings(deletes)
	problems = append(problems, readmeProblems(string(readme), registered, deletes)...)

	conf, err := os.ReadFile(configDocPath)
	if err != nil {
		return err
	}
	vars := config.Vars()
	problems = append(problems, configDocProblems(vars, fieldRules, string(conf))...)

	if err := stalenessVerdict(problems); err != nil {
		return err
	}
	fmt.Printf("%d tools documented, %d settings documented\n", len(registered), len(vars))
	return nil
}

// readmeProblems holds the README, text, to the tools the binary registers,
// the deletes among them and the scopes login asks for. The tool table and
// the scopes are read from its prose, its fences blanked: an example
// documents nothing. The permission rules sit in a fence of their own.
func readmeProblems(text string, registered, deletes []string) []string {
	readme := strings.ReplaceAll(text, "\r\n", "\n")
	prose := strings.Join(docsProse(readme), "\n")
	rows := readmeTool.FindAllStringSubmatch(prose, -1)
	documented := make([]string, 0, len(rows))
	for _, m := range rows {
		documented = append(documented, m[1])
	}
	sort.Strings(documented)
	var problems []string
	if missing := difference(registered, documented); len(missing) > 0 {
		problems = append(problems, "README's tool table is missing: "+strings.Join(missing, ", "))
	}
	if extra := difference(documented, registered); len(extra) > 0 {
		problems = append(problems, "README's tool table names tools that are not registered: "+strings.Join(extra, ", "))
	}
	problems = append(problems, permissionRulesAreTools(readme, registered, deletes)...)
	return append(problems, scopesAreDocumented(prose)...)
}

// stalenessVerdict fails with every problem found, or passes with none.
func stalenessVerdict(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
}

// configDocPath is the page every setting is documented on.
const configDocPath = "docs/configuration.md"

// configDocProblems holds the configuration page, text, to the variables
// the server reads, vars, both ways. Its prose names every one as a whole
// word, the removed one it refuses and the two read outside Define
// included, and names no other; a fenced example documents nothing. And it
// states each of rules, the bundle's fieldRules, in the words the server
// refuses a value with, so a rule added for the bundle's field is held on
// the page too.
func configDocProblems(vars []config.Var, rules map[string]fieldRule, text string) []string {
	text = strings.Join(docsProse(text), "\n")
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
