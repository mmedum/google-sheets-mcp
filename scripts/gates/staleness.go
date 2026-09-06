package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

var (
	// def(&s.Field, "flag-name", "ENV_KEY", default, usage) is how every
	// setting is registered, so the list of settings is derived rather
	// than typed. A hand-written list of eleven names is the same shape
	// as a hand-written package list: silent when it falls behind.
	configKey = regexp.MustCompile(`def\(&s\.\w+,\s*"[a-z-]+",\s*"([A-Z_]+)"`)
	// The README's tool table.
	readmeTool = regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	// A version heading, as Keep a Changelog writes it.
	versionHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
)

// schemaDump is the shape of `google-sheets-mcp --dump-schemas`.
type schemaDump struct {
	Tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"inputSchema"`
	} `json:"tools"`
}

// staleness fails when the documentation drifts from the code.
//
// Each check answers a question somebody asked once and then stopped
// asking: does the README list the tools that exist, does the
// configuration document mention every setting, does the CHANGELOG say
// what changed, does the architecture still claim there is no code.
func staleness(bin string) error {
	var problems []string

	_, dump, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	registered := make([]string, 0, len(dump.Tools))
	for _, t := range dump.Tools {
		registered = append(registered, t.Name)
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

	cfg, err := os.ReadFile("internal/config/config.go")
	if err != nil {
		return err
	}
	conf, err := os.ReadFile("docs/configuration.md")
	if err != nil {
		return err
	}
	keys := configKey.FindAllStringSubmatch(string(cfg), -1)
	if len(keys) < 5 {
		return fmt.Errorf("found %d settings in config.go; the def() pattern has probably changed", len(keys))
	}
	for _, m := range keys {
		if !strings.Contains(string(conf), "GSHEETS_"+m[1]) {
			problems = append(problems, "docs/configuration.md does not document GSHEETS_"+m[1])
		}
	}

	arch, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(string(arch)), "there is no code yet") {
		problems = append(problems, "docs/architecture.md still says there is no code yet")
	}

	if err := changelogDocumentsTheChange(); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d tools documented, %d settings documented\n", len(registered), len(keys))
	return nil
}

// dumpSchemas returns the raw dump and the parsed one, so a caller that
// needs both does not run the binary twice.
func dumpSchemas(bin string) ([]byte, *schemaDump, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	var d schemaDump
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, nil, fmt.Errorf("parse --dump-schemas: %w", err)
	}
	return out, &d, nil
}

// changelogDocumentsTheChange requires entries when Go source changed
// since the last tag.
//
// The exception in the middle is a scar: a release commit moves the
// entries out of [Unreleased] and under the version it is about to tag,
// and CI runs before that tag can exist — so a gate without it fails on
// the one pull request it was written to guard.
func changelogDocumentsTheChange() error {
	last := lastTag()
	diffArgs := []string{"diff", "--quiet", "HEAD~1", "--", "*.go"}
	if last != "" {
		diffArgs = []string{"diff", "--quiet", last + "..HEAD", "--", "*.go"}
	}
	if exec.Command("git", diffArgs...).Run() == nil {
		return nil // no Go changed
	}

	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return err
	}
	if entriesUnder(string(changelog), "## [Unreleased]") > 0 {
		return nil
	}
	if m := versionHeading.FindStringSubmatch(string(changelog)); m != nil {
		tagged := exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/v"+m[1]).Run() == nil
		if !tagged && entriesUnder(string(changelog), "## ["+m[1]+"]") > 0 {
			return nil
		}
	}
	since := last
	if since == "" {
		since = "the previous commit"
	}
	return fmt.Errorf("source changed since %s but CHANGELOG.md documents nothing new; "+
		"put the entries under [Unreleased], or under the version heading this release is about to tag", since)
}

// entriesUnder counts "- " bullets between a heading and the next one.
func entriesUnder(changelog, heading string) int {
	n, inside := 0, false
	for line := range strings.Lines(changelog) {
		switch {
		case strings.HasPrefix(line, heading):
			inside = true
		case strings.HasPrefix(line, "## ["):
			inside = false
		case inside && strings.HasPrefix(line, "- "):
			n++
		}
	}
	return n
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
