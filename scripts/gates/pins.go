package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The pins gate holds every third-party tool to one exact version, the
// same version everywhere it is named.
//
// An action is pinned by a full commit SHA, with the version it is in
// the comment after it. A tag can be moved or deleted by whoever gains
// access to the action's repository, and GitHub's secure-use reference
// says to "pin references to actions to a commit SHA".
// https://docs.github.com/en/actions/reference/security/secure-use
// A container image, a step's, a job's or a service's, is pinned by its
// sha256 digest for the same reason.
//
// Every tool an action installs is pinned beside it. A SHA pins the
// action, never the tool the action fetches: cosign-installer and
// download-syft install rather than do, and goreleaser-action's own
// default is `~> v2`, which floats across a whole major line. Narrowing
// that to `~> v2.18.0` changes the width of the range and not its kind,
// and still floats. Every action is classified as an installer or not,
// and an unknown one fails: being unclassified is how an unpinned tool
// slips through.
//
// Every version, an input, an env value, a `go run module@version` or a
// Makefile variable, is one exact version, and a tool named in two files
// is named at one version: a local lint or scan otherwise passes on a
// version CI never runs. A Makefile variable no recipe runs with the go
// command pins nothing, since the tool then runs from the PATH. A known
// tool's module@version on any line but a comment is held to one exact
// version too, whatever the readers made of the line: they do not follow
// bash -c, eval or a make conditional, and are not grown to.
//
// And every workflow pins its shell at the workflow level. The runner
// hands every `run` block to a shell, so this is not a property of
// shell-ish steps and not a property of matrix-ish jobs either: a
// job-level block fixes the forgetting exactly one level up, and the
// next job added is bare again. On Windows the default is PowerShell,
// which read `-coverprofile=cov.out` as a file called `cov` and let the
// suite carry on. Pinning it explicitly also turns on pipefail and drops
// profile and rc files, which is the behavior to want and worth knowing
// about. The shell is bash, and no job or step sets another.
// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#defaultsrunshell

// pinsTool is one tool this repository runs, and every way it can be
// named.
type pinsTool struct {
	name string
	// modules match a `module@version` in the Makefile, a run line or Go
	// source under scripts/.
	modules []string
	// envKeys are env names whose value is this tool's version.
	envKeys []string
	// actions install this tool, each with the with: input that pins it,
	// or the env: key where the action reads one instead.
	actions map[string]string
}

// pinsTools is every tool, with the installer actions a workflow uses. A
// row with installers fails when no workflow step uses one.
var pinsTools = []pinsTool{
	// go-version-file names go.mod, which is a pin by reference: go.mod
	// cannot disagree with itself the way two literals can.
	{name: "go", actions: map[string]string{"actions/setup-go": "go-version-file"}},
	{name: "golangci-lint", modules: []string{"github.com/golangci/golangci-lint"},
		actions: map[string]string{"golangci/golangci-lint-action": "version"}},
	{name: "goreleaser", modules: []string{"github.com/goreleaser/goreleaser"},
		actions: map[string]string{"goreleaser/goreleaser-action": "version"}},
	{name: "cosign", actions: map[string]string{"sigstore/cosign-installer": "cosign-release"}},
	{name: "syft", actions: map[string]string{"anchore/sbom-action/download-syft": "syft-version"}},
	{name: "mcp-publisher", envKeys: []string{"PUBLISHER_VERSION"}},
	// gitleaks-action has never had a version input; it reads the
	// scanner's version from the environment.
	{name: "gitleaks", modules: []string{"github.com/zricethezav/gitleaks", "github.com/gitleaks/gitleaks"},
		envKeys: []string{"GITLEAKS_VERSION"}, actions: map[string]string{"gitleaks/gitleaks-action": "GITLEAKS_VERSION"}},
	{name: "govulncheck", modules: []string{"golang.org/x/vuln/cmd/govulncheck"}},
	{name: "go-licenses", modules: []string{"github.com/google/go-licenses"}},
}

// pinsNotInstallers are the actions that install no tool, each with the
// reason, so that adding one is a decision rather than an omission.
var pinsNotInstallers = map[string]string{
	"actions/checkout":                "checks out the repository",
	"actions/upload-artifact":         "uploads, installs nothing",
	"actions/download-artifact":       "downloads, installs nothing",
	"actions/attest-build-provenance": "calls the attestation API",
	"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
	"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
}

const pinsScripts = "scripts"

var (
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exactSemver = regexp.MustCompile(`^v?` + semverPattern + `$`)
	// pinsModuleAt is a module@version in text. The version ends where a
	// shell word or command does.
	pinsModuleAt = regexp.MustCompile(`([A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+)@([^\s"'` + "`" + `);&|\\]+)`)
	// pinsImageDigest is a container image pinned by its content digest.
	pinsImageDigest = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
	pinsEnvVersion  = regexp.MustCompile(`_VERSION$`)
	// pinsGoModToolchain and pinsGoModGo are setup-go's own patterns for
	// the toolchain directive and the go line.
	pinsGoModToolchain = regexp.MustCompile(`(?m)^toolchain go(1\.\d+(?:\.\d+|rc\d+)?)`)
	pinsGoModGo        = regexp.MustCompile(`(?m)^go (\d+(?:\.\d+)*)`)
)

// pinsFloors are the fewest workflows, action references and pinned
// versions the gate may read.
type pinsFloors struct{ workflows, actions, places int }

// pinsLimits are the repository's floors, each under what it holds.
var pinsLimits = pinsFloors{workflows: 3, actions: 10, places: 12}

// pinsFound is one place a tool's version is written.
type pinsFound struct{ version, at string }

// pinsReport is what the gate read and found.
type pinsReport struct {
	problems                               []string
	workflows, actions, installers, places int
	tools                                  map[string][]pinsFound
	// installed names each tool a workflow step installs.
	installed map[string]bool
	// judged is each tool@version a rule has recorded or refused, which the
	// text rule does not report again.
	judged map[string]bool
}

func (r *pinsReport) fail(format string, a ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, a...))
}

// add records one pin, refusing anything but one exact version.
func (r *pinsReport) add(tool, version, at string) {
	r.tools[tool] = append(r.tools[tool], pinsFound{version, at})
	r.judged[tool+"@"+version] = true
	r.places++
	if !exactSemver.MatchString(version) {
		r.fail("%s: %s is pinned to %q, which is not one exact version", at, tool, version)
	}
}

// judgeLatest marks each known tool's module@latest in text as refused.
func (r *pinsReport) judgeLatest(text string) {
	for _, m := range pinsModuleAt.FindAllStringSubmatch(text, -1) {
		if tool := pinsToolByModule(m[1]); tool != "" && m[2] == "latest" {
			r.judged[tool+"@latest"] = true
		}
	}
}

func pinGate() error {
	flows, err := readWorkflows()
	if err != nil {
		return err
	}
	mk, err := readMakefile()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	gomod := string(raw)

	r := &pinsReport{tools: map[string][]pinsFound{}, installed: map[string]bool{}, judged: map[string]bool{},
		workflows: len(flows)}
	for _, w := range flows {
		pinsWorkflow(gomod, w, r)
	}
	r.problems = append(r.problems, mk.refused...)
	pinsMakefileRecipes(mk, pinsMakefileVars(mk, r), r)
	r.add("go", pinsGoVersion(gomod), "go.mod")
	if err := pinsGoSource(r); err != nil {
		return err
	}
	// Last, so a version a reader recorded is not reported twice.
	for _, w := range flows {
		pinsTextVersions(w.path, w.text, func(s string) string { return s }, r)
	}
	pinsTextVersions(makefilePath, mk.text, mk.expand, r)

	for _, t := range pinsTools {
		found := r.tools[t.name]
		if len(found) == 0 {
			r.fail("%s is pinned nowhere: a tool this gate names that no file pins is either gone or unread", t.name)
			continue
		}
		for _, f := range found[1:] {
			if pinsNormal(f.version) != pinsNormal(found[0].version) {
				r.fail("%s is %s at %s and %s at %s: one tool, one version",
					t.name, found[0].version, found[0].at, f.version, f.at)
			}
		}
	}
	// A floor per installer, not a count of steps: one installer used in
	// every job would meet a count alone.
	for _, t := range pinsTools {
		if len(t.actions) > 0 && !r.installed[t.name] {
			r.fail("no workflow step installs %s through %s; the reader is not seeing the step, "+
				"or the table names an installer nothing uses", t.name, strings.Join(slices.Sorted(maps.Keys(t.actions)), " or "))
		}
	}
	// Zero findings and zero inputs look the same otherwise.
	r.problems = append(r.problems, floorProblems("workflows", r.workflows, pinsLimits.workflows)...)
	r.problems = append(r.problems, floorProblems("action references", r.actions, pinsLimits.actions)...)
	r.problems = append(r.problems, floorProblems("pinned versions", r.places, pinsLimits.places)...)
	if len(r.problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(r.problems), strings.Join(r.problems, "\n  "))
	}
	fmt.Printf("%d workflows with their shell pinned, %d actions by SHA, %d installers name their tool, "+
		"%d tools pinned in %d places, each at one version\n", r.workflows, r.actions, r.installers, len(r.tools), r.places)
	return nil
}

// pinsWorkflow applies the workflow rules to one file, with gomod the
// repository's go.mod.
func pinsWorkflow(gomod string, w workflowFile, r *pinsReport) {
	r.problems = append(r.problems, w.refused...)
	if w.shell == "" {
		r.fail("%s: no workflow-level `defaults: run: shell:`. Every run step goes to a shell, "+
			"so a job-level block only fixes the jobs that exist today", w.path)
	}
	for _, sh := range w.shells {
		// An empty workflow shell is the missing one above.
		if sh.value != "bash" && (sh.value != "" || sh.job != "") {
			r.fail("%s:%d: %s sets the shell to %q, and every run step runs under bash: "+
				"another shell reads the command its own way", w.path, sh.line, sh.what, sh.value)
		}
	}
	for i, line := range strings.Split(w.text, "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") && strings.Contains(t, "@latest") {
			r.fail("%s:%d: something is pinned to @latest", w.path, i+1)
			r.judgeLatest(t)
		}
	}
	for _, s := range w.steps {
		at := fmt.Sprintf("%s:%d", w.path, s.line)
		switch {
		case strings.HasPrefix(s.uses, "./"):
			// A local action or workflow carries no version of its own.
		case strings.HasPrefix(s.uses, "docker://"):
			r.actions++
			if !pinsImageDigest.MatchString(strings.TrimPrefix(s.uses, "docker://")) {
				r.fail("%s: job %s: %s is a container image with no sha256 digest, so the image it runs can change",
					at, s.job, s.uses)
			}
		default:
			pinsActionRef(at, s, r)
			if !s.reusable {
				pinsStep(gomod, w, s, r)
			}
		}
		// This repository's own reusable workflow is one of the workflows
		// read here, which holds a version it is given where it uses it.
		if !s.reusable || !strings.HasPrefix(s.uses, "./") {
			pinsInputs(w.path, s, r)
		}
	}
	for _, img := range w.images {
		if !pinsImageDigest.MatchString(img.value) {
			r.fail("%s:%d: job %s runs %s from %s, which has no sha256 digest, so the image it runs can change",
				w.path, img.line, img.job, img.what, img.value)
		}
	}
	for _, k := range w.env {
		if !workflowVersionKey.MatchString(k.key) {
			continue
		}
		if tool := pinsToolByEnv(k.key); tool != "" {
			r.add(tool, k.value, fmt.Sprintf("%s:%d: %s", w.path, k.line, k.key))
		} else {
			r.fail("%s:%d: the env value %s names a version of a tool pinsTools does not know", w.path, k.line, k.key)
		}
	}
	for _, run := range w.runs {
		for _, g := range pinsGoRuns(run.text) {
			module, version, _ := strings.Cut(g.pkg, "@")
			if tool := pinsToolByModule(module); tool != "" {
				r.add(tool, version, fmt.Sprintf("%s:%d: go %s %s", w.path, run.line, g.verb, module))
			} else {
				r.fail("%s:%d: go %s %s names a tool pinsTools does not know", w.path, run.line, g.verb, g.pkg)
			}
		}
	}
}

// pinsActionRef holds one action reference to a commit SHA, with the
// version it is in the comment after it.
func pinsActionRef(at string, s workflowStep, r *pinsReport) {
	r.actions++
	action, sha, ok := strings.Cut(s.uses, "@")
	switch {
	case !ok:
		r.fail("%s: %s has no ref at all", at, action)
	case !fullSHA.MatchString(sha):
		r.fail("%s: %s is pinned to %q, a tag or branch that can be moved; pin the 40-character commit SHA",
			at, action, sha)
	case s.comment == "":
		r.fail("%s: %s is pinned by SHA with no comment saying which version it is", at, action)
	}
}

// pinsStep checks one step that uses an action: an installer sets the
// key that pins its tool, and any other action is classified.
func pinsStep(gomod string, w workflowFile, s workflowStep, r *pinsReport) {
	file, action := w.path, s.action()
	at := fmt.Sprintf("%s:%d: %s", file, s.line, action)
	installer := false
	for _, t := range pinsTools {
		key, ok := t.actions[action]
		if !ok {
			continue
		}
		installer = true
		r.installers++
		r.installed[t.name] = true
		// An env key is read from the step's environment, where the env
		// pass records its version.
		v, set := s.input(key)
		if pinsEnvVersion.MatchString(key) {
			set = w.stepEnv(s, key)
		}
		switch {
		case !set:
			r.fail("%s is pinned by SHA and does not set %s, so the tool it installs is whatever "+
				"is current that morning", at, key)
		case t.name == "go" && path.Clean(v.value) != "go.mod":
			r.fail("%s sets %s to %q, which is not the repository's go.mod", at, key, v.value)
		case t.name == "go":
			r.add(t.name, pinsGoVersion(gomod), at)
		case !pinsEnvVersion.MatchString(key):
			r.add(t.name, v.value, fmt.Sprintf("%s:%d: %s", file, v.line, action))
		}
	}
	if _, known := pinsNotInstallers[action]; !installer && !known {
		r.fail("%s is not classified: add it to pinsTools with the input that pins its tool, "+
			"or to pinsNotInstallers with the reason", at)
	}
}

// pinsInputs holds every version input of a step or a reusable call to
// one exact version, but the key an installer's tool is pinned by, which
// pinsStep holds. An action's code, a local action's included, and a
// reusable workflow from another repository are not read here, so the
// version each is given is held where it is given.
func pinsInputs(file string, s workflowStep, r *pinsReport) {
	action := s.action()
	for _, key := range slices.Sorted(maps.Keys(s.with)) {
		v := s.with[key]
		installs := slices.ContainsFunc(pinsTools, func(t pinsTool) bool {
			pin, ok := t.actions[action]
			return ok && workflowInputName(pin) == workflowInputName(key)
		})
		if installs || !workflowVersionInput(key) {
			continue
		}
		if !exactSemver.MatchString(v.value) {
			r.fail("%s:%d: %s: %s is %q, which is not one exact version", file, v.line, action, key, v.value)
		}
	}
}

// pinsGoRun is one module@version the go command runs or installs.
type pinsGoRun struct{ verb, pkg string }

// pinsGoRuns is each module@version a command line runs or installs with
// the go command, read as the shell splits it, its flags skipped. A
// package of this module has no version and is not one.
func pinsGoRuns(line string) []pinsGoRun {
	var out []pinsGoRun
	for _, run := range goRunsIn(line) {
		if strings.Contains(run.pkg, "@") {
			out = append(out, pinsGoRun{"run", run.pkg})
		}
	}
	for _, c := range goCommands(line) {
		if c.sub != "install" {
			continue
		}
		for _, pkg := range goAfterFlags(c.args) {
			if strings.Contains(pkg, "@") {
				out = append(out, pinsGoRun{"install", pkg})
			}
		}
	}
	return out
}

// pinsGoVersion is the Go a go.mod runs: its toolchain directive, or else
// its go line. setup-go installs that, and the go command switches to it
// (https://github.com/actions/setup-go/blob/b7ad1dad31e06c5925ef5d2fc7ad053ef454303e/src/installer.ts#L645,
// setup-go v7.0.0).
func pinsGoVersion(gomod string) string {
	if m := pinsGoModToolchain.FindStringSubmatch(gomod); m != nil {
		return m[1]
	}
	if m := pinsGoModGo.FindStringSubmatch(gomod); m != nil {
		return m[1]
	}
	return ""
}

// pinsMakefileVars reads every `module@version` variable, and returns each
// module@version every such variable's value holds, by variable.
func pinsMakefileVars(mk *makefile, r *pinsReport) map[string][]string {
	pinnedBy := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(mk.vars)) {
		// Recipes are read expanded, so a version kept in another
		// variable is compared as the recipe runs it.
		value := mk.expand(mk.vars[name])
		found := pinsModuleAt.FindAllStringSubmatch(value, -1)
		for _, m := range found {
			pinnedBy[name] = append(pinnedBy[name], m[0])
		}
		if strings.Contains(value, "@latest") {
			r.fail("%s: %s is pinned to @latest", makefilePath, name)
			r.judgeLatest(value)
			continue
		}
		for _, m := range found {
			if tool := pinsToolByModule(m[1]); tool != "" {
				r.add(tool, m[2], makefilePath+": "+name)
			} else {
				r.fail("%s: %s names %s, which pinsTools does not know", makefilePath, name, m[1])
			}
		}
	}
	return pinnedBy
}

// pinsMakefileRecipes holds what the recipes run with the go command. A
// module written into a recipe is a pin of its own, and a variable in
// pinnedBy whose module@version no recipe naming it runs pins nothing: the
// tool then runs from the PATH.
func pinsMakefileRecipes(mk *makefile, pinnedBy map[string][]string, r *pinsReport) {
	// ran is each variable and module@version a recipe naming it runs.
	ran := map[[2]string]bool{}
	for _, target := range slices.Sorted(maps.Keys(mk.targets)) {
		for _, raw := range mk.targets[target].recipe {
			refs := mk.refs(raw)
			for _, g := range pinsGoRuns(mk.expand(raw)) {
				named := false
				for name := range refs {
					if slices.Contains(pinnedBy[name], g.pkg) {
						named = true
						ran[[2]string{name, g.pkg}] = true
					}
				}
				if named {
					continue
				}
				module, version, _ := strings.Cut(g.pkg, "@")
				if tool := pinsToolByModule(module); tool != "" {
					r.add(tool, version, makefilePath+": "+target)
				} else {
					r.fail("%s: %s runs go %s %s, a tool pinsTools does not know", makefilePath, target, g.verb, g.pkg)
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(pinnedBy)) {
		for _, pkg := range pinnedBy[name] {
			if !ran[[2]string{name, pkg}] {
				r.fail("%s: %s pins %s and no recipe runs it with the go command, so the tool runs from the PATH",
					makefilePath, name, pkg)
			}
		}
	}
}

// pinsTextVersions holds a known tool's module@version on any line of text
// but a comment line to one exact version, whatever the readers made of
// the line, expanded by expand first. A version a rule already recorded or
// refused is not reported again.
func pinsTextVersions(file, text string, expand func(string) string, r *pinsReport) {
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range pinsModuleAt.FindAllStringSubmatch(expand(line), -1) {
			tool, version := pinsToolByModule(m[1]), m[2]
			if tool == "" || exactSemver.MatchString(version) || r.judged[tool+"@"+version] {
				continue
			}
			r.fail("%s:%d: %s is pinned to %q, which is not one exact version", file, i+1, tool, version)
		}
	}
}

// pinsGoSource reads every `module@version` of a known tool out of the
// string literals in the maintainer tooling's Go source, so a copy of a
// pin in a gate is held to the Makefile's like any other. A comment pins
// nothing, so an example in one is not read.
func pinsGoSource(r *pinsReport) error {
	if _, err := os.Stat(pinsScripts); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(pinsScripts, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range pinsModuleAt.FindAllStringSubmatch(text, -1) {
				if tool := pinsToolByModule(m[1]); tool != "" {
					r.add(tool, m[2], filepath.ToSlash(p)+": "+m[1])
				}
			}
			return true
		})
		return nil
	})
}

// pinsToolByModule is the tool a module path is, or one of its packages
// is: a path that only starts with the same text is another module.
func pinsToolByModule(module string) string {
	for _, t := range pinsTools {
		for _, m := range t.modules {
			if module == m || strings.HasPrefix(module, m+"/") {
				return t.name
			}
		}
	}
	return ""
}

func pinsToolByEnv(key string) string {
	for _, t := range pinsTools {
		if slices.Contains(t.envKeys, key) {
			return t.name
		}
	}
	return ""
}

func pinsNormal(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func indent(line string) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return -1 // a blank line has no indentation of its own
}
