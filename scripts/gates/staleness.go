package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/auth"
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

// scopesAreDocumented checks the README lists every scope `login` asks
// for, in full.
//
// Setup step 4 said "add the two scopes below" and no scope appeared
// anywhere in the file — a dangling reference, so a reader following the
// instructions had nothing to add. It survived every gate because no
// gate compared the setup instructions with the code, and it was found
// by an outside setup report against a sibling server rather than by
// anything here.
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
	// for only under a setting is still a scope somebody has to add in
	// the Cloud console before login works, and the README is where they
	// find out.
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
	problems = append(problems, scopesAreDocumented()...)
	problems = append(problems, pathsExist()...)
	problems = append(problems, packageMapIsComplete()...)
	problems = append(problems, statusLineIsTrue()...)

	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d tools documented, %d settings documented, %d document(s) checked for dead paths\n",
		len(registered), len(keys), len(docPaths))
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

// docPaths are the documents whose prose is checked for paths that do
// not exist. CHANGELOG.md is deliberately absent: its older entries name
// files that were true when they were written, and history is not
// staleness.
var docPaths = []string{
	"README.md", "CONTRIBUTING.md", "SECURITY.md", "CODE_OF_CONDUCT.md",
	"docs/architecture.md", "docs/configuration.md", "docs/development.md",
	"docs/gcp-setup.md", "docs/runbook.md", "docs/security.md",
}

var (
	// A markdown link to something in this repository: [text](path).
	// Anything with a scheme, and anything that is only a fragment, is
	// somebody else's to keep working.
	docLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	// The package tree in §5 of the architecture, one line per package.
	packageLine = regexp.MustCompile(`(?m)^(internal/[a-z0-9/]+)/\s`)
)

// pathsExist fails when a document names a file this repository does not
// have.
//
// A link that stopped resolving is the cheapest kind of staleness to
// introduce and the most annoying to meet: a reader follows it, finds
// nothing, and stops trusting the rest of the page. Nothing else in
// `make check` reads prose for paths, so a file renamed in one commit
// leaves every reference to it wrong and green.
func pathsExist() []string {
	var problems []string
	for _, doc := range docPaths {
		body, err := os.ReadFile(doc)
		if err != nil {
			problems = append(problems, doc+" is named in this gate and does not exist")
			continue
		}
		dir := "."
		if i := strings.LastIndexByte(doc, '/'); i >= 0 {
			dir = doc[:i]
		}
		for _, m := range docLink.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			// A fragment on the end names a heading, which is not a path.
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			switch {
			case target == "", strings.Contains(target, "://"), strings.HasPrefix(target, "mailto:"):
				continue
			}
			full := target
			if !strings.HasPrefix(target, "/") && dir != "." {
				full = dir + "/" + target
			}
			full = strings.TrimPrefix(full, "./")
			if _, err := os.Stat(cleanPath(full)); err != nil {
				problems = append(problems, doc+" links to "+target+", which does not exist")
			}
		}
	}
	return problems
}

// cleanPath resolves the ".." a link from docs/ uses to reach the root.
func cleanPath(p string) string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == ".." && len(out) > 0 {
			out = out[:len(out)-1]
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}

// packageMapIsComplete fails when the architecture's package tree and
// the packages that exist disagree.
//
// Derived from the filesystem rather than read once and trusted. A map
// naming most of the packages is worse than no map: a reader takes the
// absence of a package as a statement that it does not exist, and the
// one left out is as likely to be the one carrying a guarantee as any
// other.
func packageMapIsComplete() []string {
	arch, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		return []string{err.Error()}
	}
	named := map[string]bool{}
	for _, m := range packageLine.FindAllStringSubmatch(string(arch), -1) {
		named[m[1]] = true
	}
	if len(named) < 5 {
		return []string{fmt.Sprintf("found %d packages in the architecture's tree; the format has probably changed", len(named))}
	}
	entries, err := os.ReadDir("internal")
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkg := "internal/" + e.Name()
		if !named[pkg] {
			problems = append(problems, "the architecture's package tree does not name "+pkg)
		}
		delete(named, pkg)
	}
	for pkg := range named {
		// A nested package is named with its parent and needs no entry
		// of its own in the tree.
		if strings.Count(pkg, "/") > 1 {
			continue
		}
		problems = append(problems, "the architecture's package tree names "+pkg+", which does not exist")
	}
	sort.Strings(problems)
	return problems
}

// statusDocs are the documents carrying a status line, and the order a
// reader meets them in.
var statusDocs = []string{"README.md", "docs/architecture.md"}

var (
	// The status line, as both documents write it: a bold "Status:" at
	// the start of a line, optionally inside a block quote.
	statusLine = regexp.MustCompile(`(?m)^>?\s*\*\*Status:.*$`)
	// A released version, as a status line would claim one.
	claimedVersion = regexp.MustCompile(`v(\d+\.\d+\.\d+)`)
)

// statusLineIsTrue fails when a document claims a version nothing can
// confirm.
//
// The status line is the first thing a reader sees and the furthest
// thing from any test, so it goes stale in a way nothing else notices: a
// README saying v0.5.0 five releases after v0.5.0 is read by everybody
// and checked by nobody.
//
// The claim is gathered first and the absence of a reference judged only
// against it. A project with no tag and everything under [Unreleased] is
// a project that has not shipped, not a fault — this repository is
// exactly that — so no claim and no tag passes silently. A claim with
// nothing to confirm it is the failure, and the way out of it is to name
// the phase rather than a version.
func statusLineIsTrue() []string {
	claims := map[string]string{}
	found := 0
	for _, doc := range statusDocs {
		body, err := os.ReadFile(doc)
		if err != nil {
			return []string{doc + " has no status line to check: " + err.Error()}
		}
		line := statusLine.Find(body)
		if line == nil {
			continue
		}
		found++
		if m := claimedVersion.FindSubmatch(line); m != nil {
			claims[doc] = string(m[1])
		}
	}
	if found == 0 {
		return []string{"no status line in " + strings.Join(statusDocs, " or ") +
			"; the pattern has probably changed, and this check is reading nothing"}
	}
	if len(claims) == 0 {
		return nil
	}

	var known []string
	if tag := newestTag(); tag != "" {
		known = append(known, tag)
	}
	if released := newestReleased(); released != "" {
		known = append(known, released)
	}
	var problems []string
	for doc, claimed := range claims {
		switch {
		case len(known) == 0:
			problems = append(problems, doc+" claims v"+claimed+
				" and nothing has been released; name the phase instead, or tag it")
		case !slices.Contains(known, claimed):
			problems = append(problems, fmt.Sprintf("%s claims v%s; the newest released version is %s",
				doc, claimed, strings.Join(known, " or ")))
		}
	}
	sort.Strings(problems)
	return problems
}

// newestTag is the highest semantic version tag, or "" when there is
// none.
func newestTag() string {
	out, err := exec.Command("git", "tag", "--sort=-v:refname").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if m := claimedVersion.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	return ""
}

// newestReleased is the newest version heading in the changelog, which
// is the version a release commit writes before its tag can exist.
func newestReleased() string {
	body, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return ""
	}
	if m := versionHeading.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}
