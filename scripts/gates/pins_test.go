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
	} {
		if got := exactSemver.MatchString(tc.version); got != tc.exact {
			t.Errorf("exactSemver(%q) = %v, want %v", tc.version, got, tc.exact)
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
			"\t$(GO) run $(GITLEAKS) dir .",
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
			"      - uses: gitleaks/gitleaks-action@"+pinSHA+" # v3.0.0",
			"        env:",
			`          GITLEAKS_VERSION: "8.30.1"`,
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
	const gitleaksEnv = "        env:\n          GITLEAKS_VERSION: \"8.30.1\"\n"
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
			ci("      - uses: gitleaks", "      - <<: {with: {x: y}}\n        uses: gitleaks"),
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
		}},
		{"an installer whose version key sits under env:", []docsEdit{
			replace(fxRelease, "      - name: Release\n        with:\n", "      - name: Release\n        env:\n"),
		}, []string{
			".github/workflows/release.yml:22: goreleaser/goreleaser-action is pinned by SHA and does not set version, " +
				"so the tool it installs is whatever is current that morning",
			"goreleaser is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
		}},
		// GitHub passes the workflow's env and the job's to each step.
		{"an installer's env key in its job's env", []docsEdit{
			ci(gitleaksEnv, ""), ci("    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    env:\n      GITLEAKS_VERSION: \"8.30.1\"\n"),
		}, nil},
		{"an installer's env key in the workflow's env", []docsEdit{
			ci(gitleaksEnv, ""), ci("jobs:\n", "env:\n  GITLEAKS_VERSION: \"8.30.1\"\njobs:\n"),
		}, nil},
		{"an installer's env key in a service's env", []docsEdit{
			ci(gitleaksEnv, ""), ci("    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    services:\n      db:\n        image: postgres@"+pinDigest+
				"\n        env:\n          GITLEAKS_VERSION: \"8.30.1\"\n"),
		}, []string{".github/workflows/ci.yml:23: gitleaks/gitleaks-action is pinned by SHA and does not set GITLEAKS_VERSION, " +
			"so the tool it installs is whatever is current that morning"}},
		{"an installer's env key in another job's env", []docsEdit{
			ci(gitleaksEnv, "  other:\n    runs-on: ubuntu-latest\n    env:\n      GITLEAKS_VERSION: \"8.30.1\"\n"),
		}, []string{".github/workflows/ci.yml:18: gitleaks/gitleaks-action is pinned by SHA and does not set GITLEAKS_VERSION, " +
			"so the tool it installs is whatever is current that morning"}},
		{"an env version of a tool nobody named", []docsEdit{
			replace(fxRelease, "PUBLISHER_VERSION: v1.8.1", "PUBLISHER_VERSION: v1.8.1\n          NODE_VERSION: 22.1.0"),
		}, []string{".github/workflows/release.yml:26: the env value NODE_VERSION names a version of a tool pinsTools does not know"}},
		{"@latest in a run line, with its flags before it", []docsEdit{
			ci("govulncheck@v1.7.0 ./...", "govulncheck@v1.7.0 ./...\n      - run: go run -mod=mod golang.org/x/vuln/cmd/govulncheck@latest ./..."),
		}, []string{
			".github/workflows/ci.yml:15: something is pinned to @latest",
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
		}, []string{`.github/workflows/ci.yml:15: govulncheck is pinned to "master", which is not one exact version`}},
		{"CI and the Makefile at two versions of each tool", []docsEdit{
			ci("govulncheck@v1.7.0", "govulncheck@v1.7.1"),
			ci("version: v2.14.0", "version: v2.13.0"),
			ci(`GITLEAKS_VERSION: "8.30.1"`, `GITLEAKS_VERSION: "8.30.0"`),
		}, []string{
			"golangci-lint is v2.13.0 at .github/workflows/ci.yml:17: golangci/golangci-lint-action and v2.14.0 at Makefile: GOLANGCI_LINT: one tool, one version",
			"gitleaks is 8.30.0 at .github/workflows/ci.yml:20: GITLEAKS_VERSION and v8.30.1 at Makefile: GITLEAKS: one tool, one version",
			"govulncheck is v1.7.1 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.7.0 at Makefile: GOVULNCHECK: one tool, one version",
		}},
		{"the Makefile at @latest, and a pin no recipe runs", []docsEdit{
			replace(fxMakefile, "go-licenses@v1.6.0", "go-licenses@latest"),
			replace(fxMakefile, "\t$(GO) run $(GOVULNCHECK) ./...", "\t@echo $(GOVULNCHECK)\n\tgovulncheck ./..."),
		}, []string{
			"Makefile: GOLICENSES is pinned to @latest",
			"Makefile: GOVULNCHECK pins golang.org/x/vuln/cmd/govulncheck@v1.7.0 and no recipe runs it with the go command, so the tool runs from the PATH",
			"go-licenses is pinned nowhere: a tool this gate names that no file pins is either gone or unread",
		}},
		{"a recipe that runs a tool at its own version", []docsEdit{
			replace(fxMakefile, "\t$(GO) run $(GITLEAKS) dir .", "\t$(GO) run $(GITLEAKS) dir .\n\tgo run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./..."),
		}, []string{
			"govulncheck is v1.7.0 at .github/workflows/ci.yml:14: go run golang.org/x/vuln/cmd/govulncheck and v1.6.0 at Makefile: secrets: one tool, one version",
		}},
		{"setup-go without go-version-file, and with another go.mod", []docsEdit{
			ci("        with:\n          go-version-file: go.mod", "        with:\n          go-version: 1.27.3"),
			replace(fxCodeQL, "      - uses: github/codeql-action/init", "      - uses: actions/setup-go@"+pinSHA+" # v7.0.0\n        with:\n          go-version-file: tools/go.mod\n      - uses: github/codeql-action/init"),
		}, []string{
			".github/workflows/ci.yml:11: actions/setup-go is pinned by SHA and does not set go-version-file, " +
				"so the tool it installs is whatever is current that morning",
			`.github/workflows/codeql.yml:10: actions/setup-go sets go-version-file to "tools/go.mod", which is not the repository's go.mod`,
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
		{"jobs that call a reusable workflow, held to the SHA rule only", []docsEdit{
			replace(fxCodeQL, "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n", "analyze@"+pinSHA+" # codeql-bundle-v2.26.4\n"+
				"  call:\n    uses: o/r/.github/workflows/x.yml@"+pinSHA+" # v1.0.0\n  tagged:\n    uses: o/r/.github/workflows/x.yml@v1\n"),
		}, []string{
			`.github/workflows/codeql.yml:15: o/r/.github/workflows/x.yml is pinned to "v1", a tag or branch that can be moved; pin the 40-character commit SHA`,
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
