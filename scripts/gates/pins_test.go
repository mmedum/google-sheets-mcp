package main

import (
	"slices"
	"strings"
	"testing"
)

// TestPinsShell is the check the rule needed to be a rule.
//
// The load-bearing case is "job level only": a defaults block nested
// under a job looks like the fix, satisfies a substring search, and
// leaves the next job added to the file bare again. The runner hands
// every run step to a shell, so the pin belongs at the workflow level
// where it covers the jobs nobody has written yet.
func TestPinsShell(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"workflow level", "name: ci\ndefaults:\n  run:\n    shell: bash\njobs:\n  test:\n    runs-on: ubuntu-latest\n", "bash"},
		{"workflow level with a comment", "defaults:\n  # every run step\n  run:\n    shell: bash # pipefail on\n", "bash"},
		{"job level only", "jobs:\n  test:\n    defaults:\n      run:\n        shell: bash\n", ""},
		{"both, workflow level present", "defaults:\n  run:\n    shell: bash\njobs:\n  test:\n    defaults:\n      run:\n        shell: pwsh\n", "bash"},
		{"defaults without run", "defaults:\n  something: else\njobs:\n  test:\n", ""},
		{"run without shell", "defaults:\n  run:\n    working-directory: ./x\njobs:\n  test:\n", ""},
		{"shell with no value", "defaults:\n  run:\n    shell:\n", ""},
		{"shell with only a comment", "defaults:\n  run:\n    shell: # bash\n", ""},
		{"nothing at all", "name: ci\njobs:\n  test:\n    runs-on: ubuntu-latest\n", ""},
		{"a step-level shell is not it", "jobs:\n  test:\n    steps:\n      - run: x\n        shell: bash\n", ""},
		// defaults.shell is a sibling of defaults.run, not a child of
		// it, and it pins nothing. A check that only asked whether both
		// words appeared inside the defaults block would say yes.
		{"shell beside run, not under it", "defaults:\n  run:\n  shell: bash\njobs:\n  test:\n", ""},
		{"shell under a sibling key", "defaults:\n  run:\n  other:\n    shell: bash\n", ""},
		// And the depth rule has to be a depth rule rather than a
		// two-space one, or it decays back into a substring search on
		// any file indented differently.
		{"four-space indentation", "defaults:\n    run:\n        shell: bash\njobs:\n    test:\n", "bash"},
		{"tab indentation", "defaults:\n\trun:\n\t\tshell: bash\n", "bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseWorkflow("w.yml", tc.in).shell; got != tc.want {
				t.Errorf("shell = %q, want %q", got, tc.want)
			}
		})
	}
	// And the shell is bash at every level a run step can take one from:
	// another shell reads the same command its own way.
	const other = " sets the shell to \"pwsh\", and every run step runs under bash: another shell reads the command its own way"
	jobShell := func(shell string) docsEdit {
		return replace(fxCI, "    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    defaults:\n      run:\n        shell: "+shell+"\n")
	}
	stepShell := func(shell string) docsEdit {
		return replace(fxCI, "govulncheck@v1.7.0 ./...\n", "govulncheck@v1.7.0 ./...\n        shell: "+shell+"\n")
	}
	for _, tc := range []struct {
		name  string
		edits []docsEdit
		want  []string
	}{
		{"pwsh for the workflow", []docsEdit{replace(fxCodeQL, "    shell: bash", "    shell: pwsh")},
			[]string{".github/workflows/codeql.yml:4: the workflow" + other}},
		{"no shell for the workflow, reported once", []docsEdit{replace(fxCodeQL, "    shell: bash", "    shell:")},
			[]string{".github/workflows/codeql.yml: no workflow-level `defaults: run: shell:`. " +
				"Every run step goes to a shell, so a job-level block only fixes the jobs that exist today"}},
		{"pwsh for a job", []docsEdit{jobShell("pwsh")}, []string{".github/workflows/ci.yml:11: job test" + other}},
		{"pwsh for a step", []docsEdit{stepShell("pwsh")}, []string{".github/workflows/ci.yml:15: a step of job test" + other}},
		{"no shell for a step", []docsEdit{stepShell("")}, []string{".github/workflows/ci.yml:15: a step of job test" +
			strings.Replace(other, `"pwsh"`, `""`, 1)}},
		{"bash for a job and a step", []docsEdit{jobShell("bash"), stepShell("bash")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pinsProblems(t, applyEdits(t, pinsFixture(), tc.edits...)); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestIndent(t *testing.T) {
	for in, want := range map[string]int{"a": 0, "  a": 2, "\ta": 1, "": -1, "   ": -1} {
		if got := indent(in); got != want {
			t.Errorf("indent(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPinPatterns(t *testing.T) {
	for _, tc := range []struct {
		version string
		exact   bool
	}{
		{"v2.18.0", true},
		{"2.18.0", true},
		{"v1.8.0-pre.2", true},
		{"v2.0.0-rc-1", true},
		// A narrowed range still floats: a `~>` value is a constraint
		// whatever follows it.
		{"~> v2.18.0", false},
		{"~> v2", false},
		{"v2", false},
		{"1.27", false},
		{"latest", false},
		{"", false},
		// A prerelease is dot-separated identifiers, none empty and each
		// holding a letter or a digit (https://semver.org/#spec-item-9).
		{"v1.2.3-0.x-y", true},
		{"v1.2.3-", false},
		{"v1.2.3-.", false},
		{"v1.2.3--", false},
		{"v1.2.3-rc..1", false},
		{"v1.2.3-rc.", false},
		{"v1.2.3-rc.-", false},
	} {
		if got := exactSemver.MatchString(tc.version); got != tc.exact {
			t.Errorf("exactSemver(%q) = %v, want %v", tc.version, got, tc.exact)
		}
	}
	// go-version-file names a file rather than a version, and go.mod is
	// the pin. A workflow's key is read in any case.
	for key, want := range map[string]bool{
		"go-version-file": false, "version": true, "cosign-release": true, "syft-version": true,
		"GITLEAKS_VERSION": true, "COSIGN_RELEASE": true, "persist-credentials": false,
		"Tool_Version": true, "TOOL_Release": true, "Syft-Version": true, "VERSION": true,
	} {
		if got := workflowVersionKey.MatchString(key); got != want {
			t.Errorf("%q read as a workflow's version key = %v, want %v", key, got, want)
		}
	}
	// The Makefile's VERSION is the server's, not a tool's.
	for name, want := range map[string]bool{
		"GITLEAKS_VERSION": true, "COSIGN_RELEASE": true, "VERSION": false, "GOVULNCHECK": false,
	} {
		if got := pinsVersionVar.MatchString(name); got != want {
			t.Errorf("%q read as a Makefile version variable = %v, want %v", name, got, want)
		}
	}
	if !fullSHA.MatchString("f06c13b6b1a9625abc9e6e439d9c05a8f2190e94") {
		t.Error("a full SHA was rejected")
	}
	for _, bad := range []string{"v7.2.3", "f06c13b", "F06C13B6B1A9625ABC9E6E439D9C05A8F2190E94"} {
		if fullSHA.MatchString(bad) {
			t.Errorf("%q was accepted as a full SHA", bad)
		}
	}
}

// A made-up commit SHA and a made-up image digest.
const (
	pinSHA    = "0123456789abcdef0123456789abcdef01234567"
	pinDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// The workflows the pins and parity rows edit, named once.
const (
	fxCI      = workflowDir + "/" + parityCI
	fxRelease = workflowDir + "/" + parityReleaseWorkflow
	fxCodeQL  = workflowDir + "/codeql.yml"
)

// pinsFixture is a checkout the pins gate accepts: every tool it knows
// pinned once or more at one version, each installer used, every action
// classified and pinned by SHA.
func pinsFixture() map[string]string {
	return map[string]string{
		"go.mod": textLines("module example.com/fixture", "", "go 1.27.3"),
		fxMakefile: textLines(
			"GO ?= go",
			"GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0",
			"GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0",
			"GOLICENSES    ?= github.com/google/go-licenses@v1.6.0",
			"GITLEAKS      ?= github.com/zricethezav/gitleaks/v8@v8.30.1",
			"",
			"lint:",
			"\t$(GO) run $(GOLANGCI_LINT) run",
			"vuln:",
			"\t$(GO) run $(GOVULNCHECK) ./...",
			"licenses:",
			"\t$(GO) run $(GOLICENSES) check ./...",
			"secrets:",
			"\t$(GO) run $(GITLEAKS) git --log-opts=HEAD .",
			"secrets-staged:",
			"\t$(GO) run $(GITLEAKS) git --staged .",
		),
		fxCI: textLines(
			"name: ci",
			"on: [push]",
			"defaults:",
			"  run:",
			"    shell: bash",
			"jobs:",
			"  test:",
			"    runs-on: ubuntu-latest",
			"    steps:",
			"      - uses: actions/checkout@"+pinSHA+" # v7.0.1",
			"      - uses: actions/setup-go@"+pinSHA+" # v7.0.0",
			"        with:",
			"          go-version-file: go.mod",
			"      - run: go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...",
			"      - uses: golangci/golangci-lint-action@"+pinSHA+" # v9.3.0",
			"        with:",
			"          version: v2.14.0",
			"      - run: make secrets",
			"      - run: go run github.com/google/go-licenses@v1.6.0 check ./...",
			"      - uses: actions/upload-artifact@"+pinSHA+" # v7.0.2",
		),
		fxRelease: textLines(
			"name: release",
			"on:",
			"  release:",
			"    types: [published]",
			"defaults:",
			"  run:",
			"    shell: bash",
			"jobs:",
			"  release:",
			"    runs-on: ubuntu-latest",
			"    steps:",
			"      - uses: actions/checkout@"+pinSHA+" # v7.0.1",
			"      - uses: sigstore/cosign-installer@"+pinSHA+" # v4.1.2",
			"        with:",
			"          cosign-release: v3.1.3",
			"      - uses: anchore/sbom-action/download-syft@"+pinSHA+" # v0.24.3",
			"        with:",
			"          syft-version: v1.51.1",
			"      - name: Release",
			"        with:",
			`          version: "v2.18.1"`,
			"        uses: goreleaser/goreleaser-action@"+pinSHA+" # v7.3.0",
			"      - run: ./mcp-publisher publish",
			"        env:",
			"          PUBLISHER_VERSION: v1.8.1",
		),
		fxCodeQL: textLines(
			"name: codeql",
			"defaults:",
			"  run:",
			"    shell: bash",
			"jobs:",
			"  analyze:",
			"    runs-on: ubuntu-latest",
			"    steps:",
			"      - uses: actions/checkout@"+pinSHA+" # v7.0.1",
			"      - uses: github/codeql-action/init@"+pinSHA+" # codeql-bundle-v2.26.4",
			"      - uses: github/codeql-action/analyze@"+pinSHA+" # codeql-bundle-v2.26.4",
		),
		"scripts/gates/main.go": textLines("package main", "", "// Nothing pinned here."),
		parityGoreleaser:        textLines("version: 2", "before:", "  hooks:", "    - go mod tidy"),
		parityHook:              textLines("#!/bin/sh", "# Point git here with `make hooks`.", "exec go run ./scripts/gates precommit"),
	}
}

// pinsProblems runs the gate over files and returns each problem it
// reports, or the whole error when it reports no list.
func pinsProblems(t *testing.T, files map[string]string) []string {
	t.Helper()
	t.Chdir(docsTree(t, files))
	err := pinGate()
	if err == nil {
		return nil
	}
	head, list, ok := strings.Cut(err.Error(), "\n")
	if !ok {
		return []string{head}
	}
	var out []string
	for _, line := range strings.Split(list, "\n") {
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// Each row is a way a version floats or a shape the reader cannot see,
// and every problem the gate names for it.
func TestPinsNamesEachProblem(t *testing.T) {
	const tag = "actions/checkout@v4"
	ci := func(old, new string) docsEdit { return replace(fxCI, old, new) }
	step := "      - uses: actions/checkout@" + pinSHA + " # v7.0.1\n      - uses: actions/setup-go"
	for _, tc := range []struct {
		name  string
		edits []docsEdit
		want  []string
	}{
		{"the fixture", nil, nil},
		{"an empty cosign-release, a key on the line below", []docsEdit{
			replace(fxRelease, "cosign-release: v3.1.3", "cosign-release:\n          other: x"),
		}, []string{
			".github/workflows/release.yml:15: cosign-release: has its value on another line; write it on the key's line",
			".github/workflows/release.yml:13: sigstore/cosign-installer is pinned by SHA and does not set cosign-release, " +
				"so the tool it installs is whatever is current that morning",
			"cosign is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
		}},
		{"an installer's input holding only a comment", []docsEdit{
			replace(fxRelease, "syft-version: v1.51.1", "syft-version: # v1.51.1"),
		}, []string{
			".github/workflows/release.yml:18: syft-version: has its value on another line; write it on the key's line",
			".github/workflows/release.yml:16: anchore/sbom-action/download-syft is pinned by SHA and does not set syft-version, " +
				"so the tool it installs is whatever is current that morning",
			"syft is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
		}},
		{"a uses: with its action on the next line", []docsEdit{
			ci(step, "      - uses:\n          "+tag+"\n      - uses: actions/setup-go"),
		}, []string{".github/workflows/ci.yml:10: uses: has its value on another line; write it on the key's line"}},
		{"a step as a flow mapping", []docsEdit{
			ci(step, "      - {uses: "+tag+"}\n      - uses: actions/setup-go"),
		}, []string{".github/workflows/ci.yml:10: uses: is not a block key on its own line; write the step in block style"}},
		{"a quoted action, read without its quotes", []docsEdit{
			ci("- uses: actions/checkout@"+pinSHA+" # v7.0.1", `- uses: "actions/checkout@`+pinSHA+`" # v7.0.1`),
		}, nil},
		{"a merge key", []docsEdit{
			ci("      - run: make secrets", "      - <<: {with: {x: y}}\n        run: make secrets"),
		}, []string{".github/workflows/ci.yml:18: GitHub Actions does not document merge keys; write the mapping out"}},
		{"an alias and a block scalar where an action goes", []docsEdit{
			ci(step, "      - uses: *checkout\n      - uses: >-\n          "+tag+"\n      - uses: actions/setup-go"),
		}, []string{
			".github/workflows/ci.yml:10: uses: an anchor, an alias or a tag; write the value out",
			".github/workflows/ci.yml:11: uses: a block scalar; write the value on the key's line",
		}},
		{"an action by tag, one with no comment, one nobody classified", []docsEdit{
			ci(step, "      - uses: "+tag+"\n      - uses: actions/upload-artifact@"+pinSHA+
				"\n      - uses: some-vendor/tool-installer@"+pinSHA+" # v1.0.0\n      - uses: actions/setup-go"),
		}, []string{
			`.github/workflows/ci.yml:10: actions/checkout is pinned to "v4", a tag or branch that can be moved; pin the 40-character commit SHA`,
			".github/workflows/ci.yml:11: actions/upload-artifact is pinned by SHA with no comment saying which version it is",
			".github/workflows/ci.yml:12: some-vendor/tool-installer is not classified: add it to pinsTools with the input that pins its tool, " +
				"or to pinsNotInstallers with the reason",
		}},
		{"a container image by tag, by digest and on the line after its key", []docsEdit{
			ci(step, "      - uses: docker://alpine:3.20\n      - uses: docker://alpine@"+pinDigest+"\n"+step),
			ci("    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    container: node:18\n"+
				"    services:\n      db:\n        image: postgres@"+pinDigest+"\n      cache:\n        image:\n          redis:7\n      api:\n        image: redis:7\n"),
		}, []string{
			".github/workflows/ci.yml:14: image: has its value on another line; write it on the key's line",
			".github/workflows/ci.yml:19: job test: docker://alpine:3.20 is a container image with no sha256 digest, so the image it runs can change",
			".github/workflows/ci.yml:9: job test runs the container from node:18, which has no sha256 digest, so the image it runs can change",
			".github/workflows/ci.yml:17: job test runs the service api from redis:7, which has no sha256 digest, so the image it runs can change",
		}},
		{"a range, a major line and a version under env rather than with", []docsEdit{
			replace(fxRelease, `          version: "v2.18.1"`, "          version: \"~> v2\"\n          setup-version: v2"),
			replace(fxRelease, "      - run: ./mcp-publisher", "      - uses: actions/checkout@"+pinSHA+" # v7.0.1\n        env:\n          version: v1.0.0\n      - run: ./mcp-publisher"),
		}, []string{
			`.github/workflows/release.yml:21: goreleaser/goreleaser-action: goreleaser is pinned to "~> v2", which is not one exact version`,
			`.github/workflows/release.yml:22: goreleaser/goreleaser-action: setup-version is "v2", which is not one exact version`,
			".github/workflows/release.yml:26: the env value version names a version of a tool pinsTools does not know",
		}},
		{"an installer whose version key sits under env:", []docsEdit{
			replace(fxRelease, "      - name: Release\n        with:\n", "      - name: Release\n        env:\n"),
		}, []string{
			".github/workflows/release.yml:22: goreleaser/goreleaser-action is pinned by SHA and does not set version, " +
				"so the tool it installs is whatever is current that morning",
			".github/workflows/release.yml:21: the env value version names a version of a tool pinsTools does not know",
			"goreleaser is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
		}},
		{"an env version of a tool nobody named", []docsEdit{
			replace(fxRelease, "PUBLISHER_VERSION: v1.8.1", "PUBLISHER_VERSION: v1.8.1\n          NODE_VERSION: 22.1.0"),
		}, []string{".github/workflows/release.yml:26: the env value NODE_VERSION names a version of a tool pinsTools does not know"}},
		// The with: inputs and the env keys are read by one predicate, so a
		// release key under env: is a version as cosign-release is.
		{"env versions named another way", []docsEdit{
			replace(fxRelease, "PUBLISHER_VERSION: v1.8.1", "PUBLISHER_VERSION: v1.8.1\n          COSIGN_RELEASE: latest\n          node-version: latest"),
		}, []string{
			".github/workflows/release.yml:26: the env value COSIGN_RELEASE names a version of a tool pinsTools does not know",
			".github/workflows/release.yml:27: the env value node-version names a version of a tool pinsTools does not know",
		}},
		{"@latest in a run line, with its flags before it", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: go run -mod=mod golang.org/x/vuln/cmd/govulncheck@latest ./..."),
		}, []string{
			".github/workflows/ci.yml:15: @latest installs whatever is newest. Name the version: what builds a release must not float",
			`.github/workflows/ci.yml:15: go run golang.org/x/vuln/cmd/govulncheck: govulncheck is pinned to "latest", which is not one exact version`,
			`govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and latest at ` +
				`.github/workflows/ci.yml:15: go run golang.org/x/vuln/cmd/govulncheck: one tool, one version`,
		}},
		{"a run's own arguments, which are not packages", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: go run ./cmd/notify -to user@example.com"),
		}, nil},
		{"a tool CI runs that the gate does not know", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./..."),
		}, []string{".github/workflows/ci.yml:15: go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 names a tool pinsTools does not know"}},
		{"tools CI installs, each read past the flags", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: go install -v golang.org/x/vuln/cmd/govulncheck@v1.7.1 honnef.co/go/tools/cmd/staticcheck@v0.6.1"),
		}, []string{
			".github/workflows/ci.yml:15: go install honnef.co/go/tools/cmd/staticcheck@v0.6.1 names a tool pinsTools does not know",
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.7.1 at " +
				".github/workflows/ci.yml:15: go install golang.org/x/vuln/cmd/govulncheck: one tool, one version",
		}},
		{"a version a reader cannot see, through bash -c", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: bash -c \"go run golang.org/x/vuln/cmd/govulncheck@master ./...\""),
		}, []string{
			`.github/workflows/ci.yml:15: golang.org/x/vuln/cmd/govulncheck: govulncheck is pinned to "master", which is not one exact version`,
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and master at " +
				".github/workflows/ci.yml:15: golang.org/x/vuln/cmd/govulncheck: one tool, one version",
		}},
		{"CI and the Makefile at two versions of each tool", []docsEdit{
			ci("govulncheck@v1.7.0", "govulncheck@v1.7.1"),
			ci("version: v2.14.0", "version: v2.13.0"),
		}, []string{
			"golangci-lint is v2.13.0 at .github/workflows/ci.yml:17: golangci/golangci-lint-action and v2.14.0 at Makefile: GOLANGCI_LINT: one tool, one version",
			"govulncheck is v1.7.1 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.7.0 at Makefile: GOVULNCHECK: one tool, one version",
		}},
		// make check's secrets target runs gitleaks at the Makefile's
		// version, and an env value a step hands `make` replaces it.
		{"a tool's version in an env value a make target takes", []docsEdit{
			ci("      - run: make secrets", "      - run: make vuln\n        env:\n          GOVULNCHECK: golang.org/x/vuln/cmd/govulncheck@v1.1.0\n      - run: make secrets"),
		}, []string{
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.1.0 at " +
				".github/workflows/ci.yml:20: golang.org/x/vuln/cmd/govulncheck: one tool, one version",
		}},
		{"the Makefile at @latest, and a pin no recipe runs", []docsEdit{
			replace(fxMakefile, "go-licenses@v1.6.0", "go-licenses@latest"),
			replace(fxMakefile, "\t$(GO) run $(GOVULNCHECK) ./...", "\t@echo $(GOVULNCHECK)\n\tgovulncheck ./..."),
		}, []string{
			"Makefile: GOLICENSES is pinned to @latest",
			"Makefile:13: @latest installs whatever is newest. Name the version: what builds a release must not float",
			"Makefile:11: names govulncheck as a word of its own, which runs it from the PATH at whatever version is installed there; " + recipeRemedy,
			"Makefile: GOVULNCHECK pins golang.org/x/vuln/cmd/govulncheck@v1.7.0 and no recipe runs it with the go command, so the tool runs from the PATH",
		}},
		{"a recipe that runs a tool at its own version", []docsEdit{
			replace(fxMakefile, "\t$(GO) run $(GITLEAKS) git --log-opts=HEAD .", "\t$(GO) run $(GITLEAKS) git --log-opts=HEAD .\n\tgo run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./..."),
		}, []string{
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.6.0 at Makefile: secrets: one tool, one version",
		}},
		{"a version kept apart from its module, and the server's own VERSION", []docsEdit{
			replace(fxMakefile, "GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0",
				"VERSION ?= dev\nGOVULNCHECK_VERSION ?= v1.7.0\nGOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)"),
		}, []string{
			"Makefile: GOVULNCHECK_VERSION holds a version apart from its module; write the tool as one `NAME ?= module@version` variable",
		}},
		// A known tool's name as a word runs it from the PATH, wherever it
		// sits, and a module whose path only ends in one is not it.
		{"a recipe that names known tools", []docsEdit{
			replace(fxMakefile, "\t$(GO) run $(GOLANGCI_LINT) run", "\t$(GO) run $(GOLANGCI_LINT) run\n"+
				"\tgolangci-lint version | tee lint.log\n\techo $$(/usr/local/bin/govulncheck -version) && env -i gitleaks git .\n"+
				"\t$(GO) run example.com/x/golangci-lint@v1.0.0 ./..."),
		}, []string{
			"Makefile:9: names golangci-lint as a word of its own, which runs it from the PATH at whatever version is installed there; " + recipeRemedy,
			"Makefile:10: names govulncheck as a word of its own, which runs it from the PATH at whatever version is installed there; " + recipeRemedy,
			"Makefile:10: names gitleaks as a word of its own, which runs it from the PATH at whatever version is installed there; " + recipeRemedy,
			"Makefile: lint runs go run example.com/x/golangci-lint@v1.0.0, a tool pinsTools does not know",
		}},
		{"a recipe that fetches the latest release through a variable", []docsEdit{
			replace(fxMakefile, "GO ?= go\n", "GO ?= go\nURL := https://github.com/x/y/releases/latest/download/z.tar.gz\n"),
			replace(fxMakefile, "\t$(GO) run $(GOVULNCHECK) ./...", "\t$(GO) run $(GOVULNCHECK) ./...\n\tcurl -fsSLO $(URL)"),
		}, []string{
			"Makefile:12: releases/latest/download installs whatever is newest. Name the version: what builds a release must not float",
		}},
		{"a hook that names a known tool, and one that fetches whatever is newest", []docsEdit{
			replace(parityHook, "exec go run", "gitleaks git --staged .\ncurl -fsSLO https://github.com/x/y/releases/latest/download/z\nexec go run"),
		}, []string{
			".githooks/pre-commit:3: names gitleaks as a word of its own, which runs it from the PATH at whatever version is installed there; " +
				"run it through a Makefile target, whose version this gate holds",
			".githooks/pre-commit:4: releases/latest/download installs whatever is newest. Name the version: what builds a release must not float",
		}},
		// A comment in the hook runs nothing.
		{"a known tool named in a hook comment", []docsEdit{
			replace(parityHook, "exec go run", "# gitleaks and golang.org/x/vuln/cmd/govulncheck@v1.1.0 run through make\nexec go run"),
		}, nil},
		// The hook's text is read for a known tool's version too, which no
		// rule above sees in a go run of the module.
		{"a tool's version in the hook", []docsEdit{
			replace(parityHook, "exec go run", "go run golang.org/x/vuln/cmd/govulncheck@v1.1.0 ./...\nexec go run"),
		}, []string{
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.1.0 at " +
				".githooks/pre-commit:3: golang.org/x/vuln/cmd/govulncheck: one tool, one version",
		}},
		// `make hooks` points git at all of .githooks/, and only pre-commit is read.
		{"a second hook beside pre-commit", []docsEdit{
			add(".githooks/pre-push", textLines("#!/bin/sh", "gitleaks git .")),
		}, []string{
			".githooks/pre-push: git runs each hook in .githooks by its name, and the gates read only .githooks/pre-commit; " +
				"move it out, or make the gates read it",
		}},
		{"a download from the latest release in a workflow, and latest in a comment", []docsEdit{
			ci("      - run: make secrets", "      # not releases/latest/download, which floats\n"+
				"      - run: curl -fsSLO https://github.com/x/y/releases/latest/download/z.tar.gz\n      - run: make secrets"),
		}, []string{
			".github/workflows/ci.yml:19: releases/latest/download installs whatever is newest. Name the version: what builds a release must not float",
		}},
		// goreleaser runs its hooks in the release, and a sign's or an SBOM's
		// words, so each is held as a run line is.
		{"goreleaser commands at a floating version, another version and through bash -c", []docsEdit{
			replace(parityGoreleaser, "    - go mod tidy", "    - go mod tidy\n    - go run golang.org/x/vuln/cmd/govulncheck@latest ./...\n"+
				"    - bash -c \"go run github.com/google/go-licenses@v1.5.0 check ./...\"\n"+
				"builds:\n  - id: x\n    hooks:\n      post:\n        - cmd: curl -fsSLO https://github.com/x/y/releases/latest/download/z\n"+
				"sboms:\n  - cmd: go\n    args:\n      - run\n      - github.com/zricethezav/gitleaks/v8@v8.29.0"),
		}, []string{
			".goreleaser.yaml:5: @latest installs whatever is newest. Name the version: what builds a release must not float",
			".goreleaser.yaml:11: releases/latest/download installs whatever is newest. Name the version: what builds a release must not float",
			"gitleaks is v8.30.1 at Makefile: GITLEAKS and v8.29.0 at .goreleaser.yaml:16: github.com/zricethezav/gitleaks/v8: one tool, one version",
			"go-licenses is v1.6.0 at .github/workflows/ci.yml:19: go run github.com/google/go-licenses and v1.5.0 at " +
				".goreleaser.yaml:6: github.com/google/go-licenses: one tool, one version",
		}},
		{"a goreleaser shape the reader does not read", []docsEdit{
			replace(parityGoreleaser, "    - go mod tidy", "    - go mod tidy\n'signs':\n  - cmd: cosign\nbuilds:\n  - hooks:\n      pre: &hook go run x\n"),
		}, []string{
			".goreleaser.yaml:5: 'signs': a quoted key; write the key plain",
			".goreleaser.yaml:9: pre: an anchor, an alias or a tag; write the value out",
		}},
		{"goreleaser at a version the config reader was not checked against", []docsEdit{
			replace(fxRelease, `          version: "v2.18.1"`, `          version: "v2.19.0"`),
		}, []string{
			".github/workflows/release.yml:21: goreleaser/goreleaser-action runs goreleaser v2.19.0, and the .goreleaser.yaml reader " +
				"is checked against v2.18.1's source; check its key paths and template rule against v2.19.0's, then set goreleaserChecked",
		}},
		// setup-go reads go-version before go-version-file, so a go-version
		// beside go.mod is the Go the step installs.
		{"setup-go without go-version-file, with another go.mod, and with a go-version beside go.mod", []docsEdit{
			ci("        with:\n          go-version-file: go.mod\n", ""),
			replace(fxCodeQL, "      - uses: github/codeql-action/init", "      - uses: actions/setup-go@"+pinSHA+" # v7.0.0\n        with:\n          go-version-file: tools/go.mod\n      - uses: github/codeql-action/init"),
			replace(fxRelease, "      - uses: sigstore/cosign-installer", "      - uses: actions/setup-go@"+pinSHA+" # v7.0.0\n        with:\n          go-version: 1.27.3\n          go-version-file: go.mod\n      - uses: sigstore/cosign-installer"),
		}, []string{
			".github/workflows/ci.yml:11: actions/setup-go is pinned by SHA and does not set go-version-file, " +
				"so the tool it installs is whatever is current that morning",
			`.github/workflows/codeql.yml:10: actions/setup-go sets go-version-file to "tools/go.mod", which is not the repository's go.mod`,
			".github/workflows/release.yml:15: actions/setup-go sets go-version, which setup-go reads before go-version-file; " +
				"pin Go by go.mod alone",
		}},
		// An installer's version given as an expression is no exact version,
		// whatever env: sets it.
		{"an installer's version given as an expression", []docsEdit{
			replace(fxRelease, `          version: "v2.18.1"`, "          version: ${{ env.GORELEASER_VERSION }}"),
		}, []string{
			`.github/workflows/release.yml:21: goreleaser/goreleaser-action: goreleaser is pinned to "${{ env.GORELEASER_VERSION }}", which is not one exact version`,
		}},
		{"a toolchain line that names a Go line, not a release", []docsEdit{
			replace("go.mod", "go 1.27.3", "go 1.27.3\n\ntoolchain go1.27"),
		}, []string{
			`.github/workflows/ci.yml:11: actions/setup-go: go is pinned to "1.27", which is not one exact version`,
			`go.mod: go is pinned to "1.27", which is not one exact version`,
		}},
		{"a copy of a pin in Go source, and one in a comment", []docsEdit{
			add("scripts/gates/tool.go", textLines("package main", "",
				"// go run golang.org/x/vuln/cmd/govulncheck@v9.9.9 is an example.",
				"const vuln = `golang.org/x/vuln/cmd/govulncheck@v1.7.1`")),
		}, []string{
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.7.1 at " +
				"scripts/gates/tool.go: golang.org/x/vuln/cmd/govulncheck: one tool, one version",
		}},
		{"an installer nothing uses", []docsEdit{
			replace(fxRelease, "      - uses: anchore/sbom-action/download-syft@"+pinSHA+" # v0.24.3\n        with:\n          syft-version: v1.51.1\n", ""),
		}, []string{
			"syft is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
			"no workflow step installs syft through anchore/sbom-action/download-syft; the reader is not seeing the step, " +
				"or the table names an installer nothing uses",
		}},
		{"a Makefile line the reader does not read", []docsEdit{
			replace(fxMakefile, "GO ?= go\n", "ifdef CI\nGO ?= go\nendif\n"),
		}, []string{
			`Makefile:1: "ifdef CI" is a directive or an assignment, which this reader does not read`,
			`Makefile:3: "endif" is a directive or an assignment, which this reader does not read`,
		}},
		{"jobs that call a reusable workflow, held to the SHA rule and an exact version", []docsEdit{
			replace(fxCodeQL, "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n", "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n"+
				"  call:\n    uses: o/r/.github/workflows/x.yml@"+pinSHA+" # v1.0.0\n    with:\n      cosign-release: latest\n"+
				"  tagged:\n    uses: o/r/.github/workflows/x.yml@v1\n"),
		}, []string{
			`.github/workflows/codeql.yml:15: o/r/.github/workflows/x.yml: cosign-release is "latest", which is not one exact version`,
			`.github/workflows/codeql.yml:17: o/r/.github/workflows/x.yml is pinned to "v1", a tag or branch that can be moved; pin the 40-character commit SHA`,
		}},
		// The runner reads an input's name in upper case, so an installer's
		// input in another case is the one that pins its tool.
		{"an installer's input in another case", []docsEdit{
			replace(fxRelease, "cosign-release: v3.1.3", "COSIGN-RELEASE: latest"),
		}, []string{
			`.github/workflows/release.yml:15: sigstore/cosign-installer: cosign is pinned to "latest", which is not one exact version`,
		}},
		{"a version input in another case on an action that installs nothing", []docsEdit{
			replace(fxCI, "# v7.0.1\n      - uses: actions/setup-go", "# v7.0.1\n        with:\n          Tool-Version: latest\n      - uses: actions/setup-go"),
		}, []string{
			`.github/workflows/ci.yml:12: actions/checkout: Tool-Version is "latest", which is not one exact version`,
		}},
		// This repository's own reusable workflow is read as a workflow, so a
		// version it is given is held where it uses it. A local action's
		// action.yml is read by no gate, so its inputs are held here.
		// A literal given to this repository's own reusable workflow is held
		// there too: a run step in it may fetch whatever version it is given.
		// A literal inside an expression is a literal all the same.
		{"a literal version given to a local reusable workflow", []docsEdit{
			replace(fxCodeQL, "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n", "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n"+
				"  call:\n    uses: ./.github/workflows/x.yml\n    with:\n      tool-version: latest\n"+
				"      Other-Release: v1\n      quoted-version: ${{ 'latest' }}\n      fallback-version: ${{ inputs.v || 'latest' }}\n"+
				"      ref-version: ${{ github.ref_name }}\n      exact-version: v1.2.3\n"),
		}, []string{
			`.github/workflows/codeql.yml:16: ./.github/workflows/x.yml: Other-Release is "v1", which is not one exact version`,
			`.github/workflows/codeql.yml:18: ./.github/workflows/x.yml: fallback-version is "${{ inputs.v || 'latest' }}", which is not one exact version`,
			`.github/workflows/codeql.yml:17: ./.github/workflows/x.yml: quoted-version is "${{ 'latest' }}", which is not one exact version`,
			`.github/workflows/codeql.yml:15: ./.github/workflows/x.yml: tool-version is "latest", which is not one exact version`,
		}},
		// An env key is a version in any case: a run step reading
		// $Tool_Version takes a version all the same.
		{"env versions in another case", []docsEdit{
			replace(fxRelease, "PUBLISHER_VERSION: v1.8.1", "PUBLISHER_VERSION: v1.8.1\n          Syft_Version: latest\n          TOOL_Release: v1"),
		}, []string{
			".github/workflows/release.yml:26: the env value Syft_Version names a version of a tool pinsTools does not know",
			".github/workflows/release.yml:27: the env value TOOL_Release names a version of a tool pinsTools does not know",
		}},
		// A step written after a lone CR is the rest of the line before to a
		// reader that breaks lines at LF.
		{"a step hidden after a lone CR", []docsEdit{
			ci("      - run: make secrets", "      - run: make secrets\r      - uses: evil-org/x@main"),
		}, []string{
			".github/workflows/ci.yml:18: a CR with no LF after it at byte 26 of the line, which YAML reads as a line break and this reader does not; " +
				"break lines with LF or CRLF alone",
		}},
		{"a version given to a local reusable workflow, and to a local action", []docsEdit{
			replace(fxCodeQL, "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n", "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n"+
				"  call:\n    uses: ./.github/workflows/x.yml\n    with:\n      release-version: ${{ github.ref_name }}\n"),
			replace(fxCI, "# v7.0.1\n      - uses: actions/setup-go", "# v7.0.1\n      - uses: ./.github/actions/setup\n        with:\n          tool-version: latest\n      - uses: actions/setup-go"),
		}, []string{
			`.github/workflows/ci.yml:13: ./.github/actions/setup: tool-version is "latest", which is not one exact version`,
		}},
		{"a workflow with no shell", []docsEdit{
			replace(fxCodeQL, "defaults:\n  run:\n    shell: bash\n", ""),
		}, []string{".github/workflows/codeql.yml: no workflow-level `defaults: run: shell:`. " +
			"Every run step goes to a shell, so a job-level block only fixes the jobs that exist today"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pinsProblems(t, applyEdits(t, pinsFixture(), tc.edits...))
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// recipeRemedy is how a recipe runs a tool instead of from the PATH.
const recipeRemedy = "run it as `$(GO) run $(NAME)`, NAME a `NAME ?= module@version` variable"

// A gate that reads nothing passes everything, so each count has a floor.
func TestPinsRefusesToReadTooLittle(t *testing.T) {
	got := pinsProblems(t, applyEdits(t, pinsFixture(), drop(fxCodeQL)))
	want := []string{
		"read 2 workflows, want at least 3: the reader is not seeing its input",
		"read 8 action references, want at least 10: the reader is not seeing its input",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
