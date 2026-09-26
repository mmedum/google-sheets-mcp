package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The workflow gate holds three rules that read as complete when they
// are not.
//
// An action is pinned by a full commit SHA, because GitHub's own words
// are that "pinning an action to a full-length commit SHA is currently
// the only way to use an action as an immutable release".
//
// And every tool an action installs is pinned beside it. A SHA pins the
// action, never the tool the action fetches: cosign-installer and
// download-syft install rather than do, and goreleaser-action's own
// default is `~> v2`, which floats across a whole major line. Narrowing
// that to `~> v2.18.0` changes the width of the range and not its kind,
// and a sibling recorded exactly that narrowing as the fix while still
// floating afterwards. So this is a gate and not a comment.
//
// And every workflow pins its shell at the workflow level. The runner
// hands every `run` block to a shell, so this is not a property of
// shell-ish steps and not a property of matrix-ish jobs either: a
// job-level block fixes the forgetting exactly one level up, and the
// next job added is bare again. On Windows the default is PowerShell,
// which read `-coverprofile=cov.out` as a file called `cov` and let the
// suite carry on. Pinning it explicitly also turns on pipefail and drops
// profile and rc files, which is the behavior to want and worth knowing
// about.
var (
	usesLine    = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exactSemver = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.\-]+)?$`)

	// versionKeys name a tool's version wherever a workflow sets one —
	// an action input, or an environment variable where the action reads
	// one instead. go-version is absent on purpose: every workflow uses
	// go-version-file, which points at go.mod and is a pin by reference.
	//
	// Every key here must match something, and the check below fails if
	// one does not. A list like this is a claim about what is covered,
	// and an entry that can never match is a false claim that reads
	// exactly like a true one: `gitleaks-version` sat here naming an
	// input `gitleaks-action` has never had, so the one wrapper-installed
	// tool the check appeared to cover was the one it did not.
	versionKeys = []string{"version", "cosign-release", "syft-version", "GITLEAKS_VERSION"}
	versionLine = regexp.MustCompile(`(?m)^\s*(` + strings.Join(versionKeys, "|") + `):\s*["']?([^"'\s#]+)["']?`)
)

func pinGate() error {
	dir := filepath.Join(".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}

	var problems []string
	files, actions, versions, installers := 0, 0, 0, 0
	matched := map[string]int{}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		files++
		content := string(data)

		for _, m := range usesLine.FindAllStringSubmatch(content, -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue // a local action carries no version of its own
			}
			actions++
			_, rev, ok := strings.Cut(ref, "@")
			if !ok || !fullSHA.MatchString(rev) {
				problems = append(problems, fmt.Sprintf("%s: %s is not pinned to a full commit SHA (a tag is mutable)", e.Name(), ref))
			}
		}
		for _, m := range versionLine.FindAllStringSubmatch(content, -1) {
			key, value := m[1], m[2]
			versions++
			matched[key]++
			if !exactSemver.MatchString(value) {
				problems = append(problems, fmt.Sprintf("%s: %s: %q is not exactly one version — a range, a bare major "+
					"or `latest` lets the tool change under a pinned action", e.Name(), key, value))
			}
		}
		i, toolProblems := unpinnedTools(e.Name(), content)
		installers += i
		problems = append(problems, toolProblems...)
		if !pinsShell(content) {
			problems = append(problems, fmt.Sprintf("%s: no workflow-level `defaults: run: shell: bash`. "+
				"Every run step goes to a shell, so a job-level block only fixes the jobs that exist today", e.Name()))
		}
	}

	// A checker that finds nothing passes for the wrong reason. All
	// three counts are asserted because each can go to zero on its own:
	// a renamed directory takes the files, a renamed input takes the
	// versions.
	switch {
	case files < 3:
		return fmt.Errorf("found %d workflow files; the check is not reading %s", files, dir)
	case actions < 5:
		return fmt.Errorf("found %d action references; the check is not reading the workflows", actions)
	case versions < 3:
		return fmt.Errorf("found %d tool versions; the input names have probably changed", versions)
	case installers < 3:
		return fmt.Errorf("found %d tool installers; the classification table has probably drifted "+
			"from the actions the workflows use", installers)
	}
	// A key that matches nothing is a hole in the check wearing the
	// shape of coverage, so it is a finding rather than a silent zero.
	for _, key := range versionKeys {
		if matched[key] == 0 {
			problems = append(problems, fmt.Sprintf("the pin check names %q and no workflow sets it: "+
				"either it is misspelled, or the tool it named is gone and the entry should be too", key))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%d workflows: %d actions pinned by SHA, %d tool versions exact, %d installers name their tool's version, every shell pinned at the workflow level\n",
		files, actions, versions, installers)
	return nil
}

// pinsShell reports whether the workflow sets `defaults: run: shell:` at
// the *workflow* level.
//
// The load-bearing case is a job-level block, which looks like the fix
// and leaves the next job bare. So the indentation is what decides, and
// this is a small parser rather than a substring search.
func pinsShell(content string) bool {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if indent(line) != 0 || strings.TrimRight(line, " \t") != "defaults:" {
			continue
		}
		runIndent := -1
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" || strings.HasPrefix(strings.TrimSpace(next), "#") {
				continue
			}
			n := indent(next)
			if n == 0 {
				break // the defaults block ended
			}
			trimmed := strings.TrimSpace(next)
			// The order of the last two cases is the check. Leaving the
			// run block is tested *before* a shell key, so a `shell:`
			// that is a sibling of `run:` rather than a child of it —
			// which is `defaults.shell`, and pins nothing — answers
			// false. Swap them and that case answers true; the table
			// test has it, and it fails if this is reordered.
			switch {
			case runIndent < 0 && trimmed == "run:":
				runIndent = n
			case runIndent >= 0 && n <= runIndent:
				return false // out of the run block without a shell
			case runIndent >= 0 && strings.HasPrefix(trimmed, "shell:"):
				return strings.TrimSpace(strings.TrimPrefix(trimmed, "shell:")) != ""
			}
		}
	}
	return false
}

func indent(line string) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return -1 // a blank line has no indentation of its own
}

// The rule the version keys above could not reach. Each of them judges a
// version that is *written*; an action that installs a tool and names no
// version at all is an absence, and the "every key must match something"
// check cannot see it either — that catches a key naming nothing, not a
// tool named by no key.
//
// The Pipedrive server's release published nothing on exactly this
// shape: `sigstore/cosign-installer` pinned by SHA with no
// `cosign-release`, so the job installed whatever cosign was newest, and
// that cosign had changed its default signing format.
// `anchore/sbom-action/download-syft` had the same hole one step below
// it. A SHA pins the wrapper, not the tool.
var (
	// installerPins maps an action to the input keys that pin the tool
	// it installs. Any one of them satisfies the rule.
	installerPins = map[string][]string{
		"sigstore/cosign-installer":         {"cosign-release"},
		"anchore/sbom-action/download-syft": {"syft-version"},
		"goreleaser/goreleaser-action":      {"version"},
		"golangci/golangci-lint-action":     {"version"},
		// A file is a pin by reference, and a better one: go.mod cannot
		// disagree with itself the way two literals can.
		"actions/setup-go": {"go-version-file", "go-version"},
		// This wrapper has never had a version input; it reads the
		// scanner's version from the environment.
		"gitleaks/gitleaks-action": {"GITLEAKS_VERSION"},
	}

	// notInstallers are the actions that install no tool, each with the
	// reason, so that adding one is a decision rather than an omission.
	notInstallers = map[string]string{
		"actions/checkout":                "checks out the repository",
		"actions/upload-artifact":         "uploads, installs nothing",
		"actions/download-artifact":       "downloads, installs nothing",
		"actions/attest-build-provenance": "calls the attestation API",
		"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
		"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
	}
)

// unpinnedTools reports every action whose tool is left to float, and
// every action nobody has classified.
func unpinnedTools(name, content string) (installers int, problems []string) {
	for _, loc := range usesLine.FindAllStringSubmatchIndex(content, -1) {
		ref := content[loc[2]:loc[3]]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
			continue
		}
		action, _, _ := strings.Cut(ref, "@")
		keys, isInstaller := installerPins[action]
		if !isInstaller {
			if _, known := notInstallers[action]; !known {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is not classified in scripts/gates/pins.go — add it to installerPins "+
						"with the input that pins its tool, or to notInstallers with the reason. "+
						"A SHA pins the wrapper, not the tool", name, action))
			}
			continue
		}
		installers++
		if !namesAnyKey(stepBlock(content, loc[0]), keys) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is pinned by SHA but the tool it installs is not — set %s. "+
					"A SHA pins the wrapper, not the tool", name, action, strings.Join(keys, " or ")))
		}
	}
	return installers, problems
}

// stepBlock returns the lines belonging to the step whose `uses:` line
// starts at start, so that a `with:` or `env:` key is read from the step
// that owns it rather than from the next one down the file.
func stepBlock(content string, start int) string {
	lines := strings.Split(content[start:], "\n")
	base := indent(lines[0])
	var out []string
	for i, ln := range lines {
		if i > 0 && strings.TrimSpace(ln) != "" {
			in := indent(ln)
			if in < base || (in == base && strings.HasPrefix(strings.TrimSpace(ln), "- ")) {
				break
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// namesAnyKey reports whether the step sets one of the given keys.
func namesAnyKey(block string, keys []string) bool {
	for _, k := range keys {
		if regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(k) + `:\s*\S`).MatchString(block) {
			return true
		}
	}
	return false
}
