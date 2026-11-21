package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// parityFiles are the files parity reads, by the path a row edits.
var parityFiles = []string{
	fxMakefile, parityGoreleaser, parityHook, fxCI, fxRelease, workflowDir + "/publish-mcp.yml", fxCodeQL,
	workflowDir + "/assign-issues.yml",
}

// parityRepo is the repository's own parity inputs, as text.
func parityRepo(t *testing.T) map[string]string {
	t.Helper()
	atRepoRoot(t)
	files := map[string]string{}
	for _, f := range parityFiles {
		data, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		files[f] = string(data)
	}
	return files
}

// parityRepoTags is what the tree's //go:build lines need, read once: every
// row reads the same tree.
var parityRepoTags = sync.OnceValues(func() ([]parityTagNeed, error) {
	files, err := docsFiles(repoDir)
	if err != nil {
		return nil, err
	}
	return parityTreeTags(repoDir, files)
})

// parityRun runs the gate over files, with the tree's tags and those of
// the extra Go files, and returns its problems, sorted.
func parityRun(t *testing.T, files, extraGo map[string]string) []string {
	t.Helper()
	return parityRunExcused(t, files, extraGo, parityPrereqOnly)
}

// parityRunExcused is parityRun with prereqOnly in place of
// parityPrereqOnly, so no row writes the package's own.
func parityRunExcused(t *testing.T, files, extraGo, prereqOnly map[string]string) []string {
	t.Helper()
	atRepoRoot(t)
	tree, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	needs := slices.Clone(tree)
	if extraGo != nil {
		more, err := parityTreeTags(docsTree(t, extraGo), slices.Sorted(maps.Keys(extraGo)))
		if err != nil {
			t.Fatal(err)
		}
		needs = append(needs, more...)
	}
	var flows []workflowFile
	for _, f := range parityFiles {
		if strings.HasPrefix(f, workflowDir) {
			if text, ok := files[f]; ok {
				flows = append(flows, parseWorkflow(f, text))
			}
		}
	}
	r := parityCheck(parityInputs{mk: parseMakefile(files[fxMakefile]), flows: flows,
		goreleaser: parseGoreleaser(files[parityGoreleaser]), hook: parseHook(files[parityHook]), needs: needs, prereqOnly: prereqOnly}, commands)
	sort.Strings(r.problems)
	return r.problems
}

type parityCase struct {
	name  string
	edits []docsEdit
	// extraGo are Go files whose //go:build lines join the tree's.
	extraGo map[string]string
	want    []string
}

func checkParityCases(t *testing.T, cases []parityCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parityRun(t, applyEdits(t, parityRepo(t), tc.edits...), tc.extraGo)
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// dropCheck removes one prerequisite from the Makefile's `check:` line.
// Editing the whole file by substring would hit the target's own
// definition first and leave `check:` untouched.
func dropCheck(t *testing.T, files map[string]string, target string) {
	t.Helper()
	for _, line := range strings.Split(files[fxMakefile], "\n") {
		if !strings.HasPrefix(line, "check:") {
			continue
		}
		fields := strings.Fields(line)
		kept := slices.DeleteFunc(slices.Clone(fields), func(f string) bool { return f == target })
		if len(kept) == len(fields) {
			t.Fatalf("`check:` does not list %q, so removing it proves nothing", target)
		}
		files[fxMakefile] = strings.Replace(files[fxMakefile], line, strings.Join(kept, " "), 1)
		return
	}
	t.Fatal("no `check:` line in the Makefile")
}

// And it passes on the tree as it stands, or the failures below prove only
// that it fails on everything.
func TestParityPassesOnThisRepository(t *testing.T) {
	if got := parityRun(t, parityRepo(t), nil); got != nil {
		t.Errorf("the repository as it stands:\n  %s", strings.Join(got, "\n  "))
	}
	needs, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(needs) != 1 || needs[0].String() != "-tags=live" {
		t.Errorf("the tree's tags = %v, want one need of -tags=live", needs)
	}
}

// The gate has to fail on the drifts that happened, each named.
func TestParityFindsTheDriftsThatHappened(t *testing.T) {
	const tagged = "scripts/evals/ab.go builds only with -tags=live (//go:build live) and "
	for _, tc := range []struct {
		name   string
		remove string
		edits  []docsEdit
		want   []string
	}{
		{name: "CI does not vet the build-tagged code", edits: []docsEdit{
			replace(fxCI, "      - name: vet the tagged code\n        run: go vet -tags=live ./...\n", ""),
		}, want: []string{
			"`make vet` runs \"go vet -tags=live ./...\" and ci.yml never does",
			tagged + "ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
		}},
		{name: "make check does not scan for secrets", remove: "secrets", want: []string{
			".github/workflows/ci.yml:182 runs `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD " +
				"--config .gitleaks.toml --redact --no-banner .` and `make check` does not",
		}},
		{name: "make check does not check that go.mod is tidy", remove: "tidy", want: []string{
			".github/workflows/ci.yml:149 runs `go mod tidy -diff` and `make check` does not",
		}},
		{name: "a gate runs in CI and not in make check", remove: "staleness", want: []string{
			"the staleness gate runs in make check by the dispatch, and no target `make check` runs calls it",
		}},
		{name: "a gate runs in make check and not in CI", edits: []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates staleness", "run: true"),
		}, want: []string{"the staleness gate runs in make check by the dispatch, and ci.yml never calls it"}},
		{name: "the pull-request gate is gone from CI", edits: []docsEdit{
			replace(fxCI, `run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"`, "run: true"),
		}, want: []string{"the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"}},
		// A pull-request job does not run on a push to main, so a check gate
		// or a command it alone runs passes nowhere main is checked.
		{name: "a check gate and a command only the pr job runs", edits: []docsEdit{
			replace(fxCI, "      - name: workflow pins\n        run: go run ./scripts/gates pins\n", ""),
			replace(fxCI, "      - name: go.mod is tidy\n        run: go mod tidy -diff\n", ""),
			replace(fxCI, "      - name: schema change acknowledged\n", "      - run: go run ./scripts/gates pins\n      - run: go mod tidy -diff\n"+
				"      - name: schema change acknowledged\n"),
		}, want: []string{
			"`make tidy` runs \"go mod tidy -diff\", and ci.yml runs it only in its pr job, which a push to main does not run",
			"the pins gate runs in make check by the dispatch, and ci.yml calls it only in its pr job, which a push to main does not run",
		}},
		// A vet pass only the pr job runs vets nothing on a push to main.
		{name: "the tagged code vetted only in the pr job", edits: []docsEdit{
			replace(fxCI, "      - name: vet the tagged code\n        run: go vet -tags=live ./...\n", ""),
			replace(fxCI, "      - name: schema change acknowledged\n", "      - run: go vet -tags=live ./...\n      - name: schema change acknowledged\n"),
		}, want: []string{
			"`make vet` runs \"go vet -tags=live ./...\", and ci.yml runs it only in its pr job, which a push to main does not run",
			tagged + "ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
		}},
		{name: "the pull-request gate moved out of the pr job", edits: []docsEdit{
			replace(fxCI, `run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"`, "run: true"),
			replace(fxCI, "run: go run ./scripts/gates docs\n", "run: go run ./scripts/gates docs\n      - run: go run ./scripts/gates schema-ack origin/main HEAD\n"),
		}, want: []string{"the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"}},
		{name: "the pull-request gate in make check", edits: []docsEdit{
			replace(fxMakefile, "check: fmt", "check: schema-ack fmt"),
		}, want: []string{
			"the schema-ack gate runs on pull requests by the dispatch, and `make check` runs it, where there is no merge-base",
		}},
		{name: "the linter's action gone from CI", edits: []docsEdit{
			replace(fxCI, "      - uses: golangci/golangci-lint-action@", "      - uses: some/other-action@"),
		}, want: []string{
			"`make lint` runs \"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run\", " +
				"and ci.yml neither runs it nor uses golangci/golangci-lint-action, which stands in for it",
		}},
		{name: "a check only CI runs", edits: []docsEdit{
			replace(fxCI, "      - name: go.mod is tidy\n", "      - run: go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n      - name: go.mod is tidy\n"),
		}, want: []string{".github/workflows/ci.yml:148 runs `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` and `make check` does not"}},
		{name: "CI runs a tool at another version", edits: []docsEdit{
			replace(fxCI, "govulncheck@v1.7.0", "govulncheck@v1.7.1"),
		}, want: []string{
			".github/workflows/ci.yml:157 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.1 ./...` and `make check` does not",
			"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" and ci.yml never does",
		}},
		{name: "the release notes are no longer written", edits: []docsEdit{
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`, "run: true"),
		}, want: []string{
			"the release-notes gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		// The release is release.yml and what it calls: a workflow it stopped
		// calling runs nothing in a release.
		{name: "the registry workflow not called", edits: []docsEdit{
			replace(fxRelease, "    uses: ./.github/workflows/publish-mcp.yml\n    with:\n      tag: ${{ github.ref_name }}\n",
				"    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"),
		}, want: []string{
			"the registry-publish gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		{name: "the bundle packed only in a goreleaser comment", edits: []docsEdit{
			replace(parityGoreleaser, "      post: go run ./scripts/gates mcpb-pack {{ .Version }} dist\n",
				"      # post: go run ./scripts/gates mcpb-pack {{ .Version }} dist\n      post: true\n"),
		}, want: []string{
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		{name: "a manual gate nothing runs", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GATES) api-diff\n", "\t@true\n"),
		}, want: []string{
			"the api-diff gate is manual by the dispatch, and no Makefile target or hook runs it: " +
				"a manual gate nobody can find is a gate that never fires",
		}},
		{name: "a gate the dispatch does not have", edits: []docsEdit{
			replace(fxMakefile, "\t@$(GATES) docs\n", "\t@$(GATES) docs\n\t@$(GATES) ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		{name: "a gate the dispatch does not have, in another workflow", edits: []docsEdit{
			replace(fxCodeQL, "          build-mode: autobuild\n", "          build-mode: autobuild\n      - run: go run ./scripts/gates ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		// make ignores the failure of a command behind a -, so make check
		// passes where CI, which runs it bare, fails.
		{name: "a command whose failure make ignores", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run $(GOVULNCHECK) ./...\n", "\t-$(GO) run $(GOVULNCHECK) ./...\n"),
			replace(fxMakefile, ".PHONY: clean\n", ".IGNORE: licenses\n\n.PHONY: clean\n"),
		}, want: []string{
			".IGNORE names licenses: make ignores the failure of every command `make licenses` runs",
			"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" behind a - prefix: make ignores this command's failure",
		}},
		{name: "every failure ignored", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: clean\n", ".IGNORE:\n\n.PHONY: clean\n"),
		}, want: []string{".IGNORE names no target: make ignores the failure of every command"}},
		{name: "a prerequisite a second check rule adds", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: clean\n", "check: ghostcheck\n\n.PHONY: clean\n"),
		}, want: []string{"check: names ghostcheck, which the Makefile does not define"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := applyEdits(t, parityRepo(t), tc.edits...)
			if tc.remove != "" {
				dropCheck(t, files, tc.remove)
			}
			if got := parityRun(t, files, nil); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// Commenting a step out, naming a gate in a step's name or env, or quoting
// it in an echo is not the same as running it; a flag before the gates
// package does not hide one.
func TestParityReadsWhatRuns(t *testing.T) {
	const notInCI = "the staleness gate runs in make check by the dispatch, and ci.yml never calls it"
	stale := "      - name: staleness\n        run: go run ./scripts/gates staleness\n"
	checkParityCases(t, []parityCase{
		{"a gate's step commented out", []docsEdit{
			replace(fxCI, stale, "      # - run: go run ./scripts/gates staleness\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's name", []docsEdit{
			replace(fxCI, stale, "      - name: go run ./scripts/gates staleness\n        run: true\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's env", []docsEdit{
			replace(fxCI, stale, "      - name: check\n        run: true\n        env:\n          NOTE: go run ./scripts/gates staleness\n"),
		}, nil, []string{notInCI}},
		{"a gate quoted in an echo", []docsEdit{
			replace(fxCI, stale, "      - run: echo \"go run ./scripts/gates staleness\"\n"),
		}, nil, []string{notInCI}},
		{"a gate run past its build flags, and in a command substitution", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates staleness", "run: go run -trimpath ./scripts/gates staleness"),
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`,
				`run: NOTES="$(go run ./scripts/gates release-notes "${REF_NAME}")"`),
		}, nil, nil},
		{"a target named only in a Makefile comment", []docsEdit{
			replace(fxMakefile, "check: fmt", "# check: secrets\ncheck: fmt"),
			replace(fxMakefile, " secrets api-coverage", " api-coverage"),
		}, nil, []string{
			".github/workflows/ci.yml:182 runs `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD " +
				"--config .gitleaks.toml --redact --no-banner .` and `make check` does not",
		}},
		// CI's make line runs the recipes make check runs, by the Makefile's
		// own words, so the two cannot drift; a line that sets a variable
		// runs another command and is read as written.
		{"a CI make line that sets a variable", []docsEdit{
			replace(fxCI, "        run: make secrets\n", "        run: make GITLEAKS_FLAGS=--no-banner secrets\n"),
		}, nil, []string{
			"`make secrets` runs \"go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD " +
				"--config .gitleaks.toml --redact --no-banner .\" and ci.yml never does",
		}},
		{"a CI make line naming a target the Makefile lacks", []docsEdit{
			replace(fxCI, "        run: make secrets\n", "        run: make secret\n"),
		}, nil, []string{
			"`make secrets` runs \"go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD " +
				"--config .gitleaks.toml --redact --no-banner .\" and ci.yml never does",
		}},
		{"a CI make line with its output redirected", []docsEdit{
			replace(fxCI, "        run: make secrets\n", "        run: make secrets > gitleaks.log 2>&1\n"),
		}, nil, nil},
		{"the gates binary under another name", []docsEdit{
			replace(fxMakefile, "GATES     ?= ./.gates\n", "GATES     ?= bin/gates\n"),
		}, nil, nil},
		{"a recipe spaced and quoted otherwise than CI's line", []docsEdit{
			replace(fxMakefile, "\t$(GO) mod tidy -diff\n", "\t$(GO)  mod tidy '-diff'\n"),
		}, nil, nil},
		{"a recipe line's prefixes, which are not part of the command", []docsEdit{
			replace(fxMakefile, "\t$(GO) vet -tags=live ./...\n", "\t@+ $(GO) vet -tags=live ./...\n"),
		}, nil, nil},
		{"a Makefile line the reader does not read", []docsEdit{
			replace(fxMakefile, "GO        ?= go\n", "ifndef GO\nGO        ?= go\nendif\n"),
		}, nil, []string{
			`Makefile:3: "ifndef GO" is a directive or an assignment, which this reader does not read`,
			`Makefile:5: "endif" is a directive or an assignment, which this reader does not read`,
		}},
		// What the goreleaser reader refuses is a release command parity
		// cannot see.
		{"a goreleaser shape the reader does not read", []docsEdit{
			replace(parityGoreleaser, "project_name: google-sheets-mcp\n", "project_name: google-sheets-mcp\n'before':\n  hooks:\n    - go mod tidy\n"),
		}, nil, []string{".goreleaser.yaml:4: 'before': a quoted key; write the key plain"}},
		{"a step the workflow reader does not read", []docsEdit{
			replace(fxCI, stale, "      - {name: staleness, run: go run ./scripts/gates staleness}\n"),
		}, nil, []string{
			".github/workflows/ci.yml:95: an item of steps: is not a block mapping; write it one key a line",
			notInCI,
		}},
		{"a gate named only in a recipe's comment", []docsEdit{
			replace(fxMakefile, "\t@$(GATES) docs\n", "\t@$(GATES) pins # was: $(GATES) docs\n"),
		}, nil, []string{"the docs gate runs in make check by the dispatch, and no target `make check` runs calls it"}},
		{"a command beside a gate call on one recipe line", []docsEdit{
			replace(fxMakefile, "\t@$(GATES) docs\n", "\t@$(GATES) docs && $(GO) vet -tags=foo ./...\n"),
		}, nil, []string{"`make docs` runs \"go vet -tags=foo ./...\" and ci.yml never does"}},
		{"a command beside a gate call on one CI line", []docsEdit{
			replace(fxCI, "run: go mod tidy -diff", "run: go run ./scripts/gates docs && go mod tidy -diff"),
		}, nil, nil},
		// A pipeline's other command runs beside a gate call too, and a
		// redirection is neither a word nor an operator.
		{"a command piped from a gate call on one CI line", []docsEdit{
			replace(fxCI, "run: go mod tidy -diff", "run: go run ./scripts/gates docs 2>&1 | go mod tidy -diff"),
		}, nil, nil},
	})
}

// A gate both sides call takes the same arguments on both, the Makefile's
// variables expanded. An omitted [BIN] is the binary the gate defaults to,
// and no other omitted argument is.
func TestParityHoldsAGateCallsArguments(t *testing.T) {
	checkParityCases(t, []parityCase{
		{"a coverage floor the Makefile sets apart from CI's", []docsEdit{
			replace(fxMakefile, "COVER_MIN ?= 80", "COVER_MIN ?= 85"),
		}, nil, []string{
			".github/workflows/ci.yml:60 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `cov.out 85` and ci.yml never does",
		}},
		// Only the binary compares as a path; every other argument is a word.
		{"a profile written another way", []docsEdit{
			replace(fxMakefile, "coverage cov.out $(COVER_MIN)", "coverage ./cov.out $(COVER_MIN)"),
		}, nil, []string{
			".github/workflows/ci.yml:60 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `./cov.out 80` and ci.yml never does",
		}},
		{"a binary other than the one the gate defaults to", []docsEdit{
			replace(fxMakefile, "\t@$(GATES) smoke $(BIN)\n", "\t@$(GATES) smoke ./old/google-sheets-mcp\n"),
		}, nil, []string{
			".github/workflows/ci.yml:90 calls the smoke gate with no arguments and `make check` never does",
			"`make smoke` calls the smoke gate with `./old/google-sheets-mcp` and ci.yml never does",
		}},
		{"the default binary named in CI", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke\n", "run: go run ./scripts/gates smoke ./google-sheets-mcp\n"),
		}, nil, nil},
		// exec looks a name with no slash up on PATH, so a bare name is
		// another binary than ./name.
		{"the binary written without ./", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke\n", "run: go run ./scripts/gates smoke google-sheets-mcp\n"),
		}, nil, []string{
			".github/workflows/ci.yml:90 calls the smoke gate with `google-sheets-mcp` and `make check` never does",
			"`make smoke` calls the smoke gate with `./google-sheets-mcp` and ci.yml never does",
		}},
		{"the default binary through a path that leaves and comes back", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke\n", "run: go run ./scripts/gates smoke ./cmd/../google-sheets-mcp\n"),
		}, nil, nil},
		{"the history scan in CI and the tree scan in make check", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates leaks\n", "run: go run ./scripts/gates leaks history\n"),
		}, nil, []string{
			".github/workflows/ci.yml:68 calls the leaks gate with `history` and `make check` never does",
			"`make leaks` calls the leaks gate with no arguments and ci.yml never does",
		}},
	})
}

// A gate that reads nothing passes everything, so each count has a floor.
func TestParityRefusesToReadTooLittle(t *testing.T) {
	r := parityCheck(parityInputs{mk: parseMakefile("check:\n"), flows: []workflowFile{parseWorkflow(fxCI, "jobs:\n")},
		prereqOnly: parityPrereqOnly}, map[string]command{})
	want := []string{
		"the Makefile defines no GATES, the gates binary its recipes run",
		"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse",
		"build is excused as a prerequisite and the Makefile does not define it; drop the excuse",
		"gates is excused as a prerequisite and the Makefile does not define it; drop the excuse",
		"read 0 check: prerequisites, want at least 15: the reader is not seeing its input",
		"read 0 ci.yml command lines, want at least 20: the reader is not seeing its input",
		"read 0 gates in check, want at least 10: the reader is not seeing its input",
		"read 0 distinct gate calls, want at least 15: the reader is not seeing its input",
		"read 0 //go:build lines that need -tags (live), want at least 1: the reader is not seeing its input",
	}
	if !slices.Equal(r.problems, want) {
		t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(r.problems, "\n  "), strings.Join(want, "\n  "))
	}
}

// An excuse that outlived what it excuses is refused: a prerequisite
// excuse for a target the Makefile lacks, and a CI-only one for a go
// command CI does not run.
func TestParityRefusesAStaleExcuse(t *testing.T) {
	excused := maps.Clone(parityPrereqOnly)
	excused["gone"] = "a target that was renamed"
	got := parityRunExcused(t, parityRepo(t), nil, excused)
	want := []string{"gone is excused as a prerequisite and the Makefile does not define it; drop the excuse"}
	if !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
	built := `run: |
          if [ "$RUNNER_OS" = "Windows" ]; then
            go build -o google-sheets-mcp.exe ./cmd/google-sheets-mcp
          else
            go build -o google-sheets-mcp ./cmd/google-sheets-mcp
          fi
`
	files := applyEdits(t, parityRepo(t), replace(fxCI, "      - run: go build ./...\n", ""), replace(fxCI, built, "run: true\n"))
	want = []string{"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse"}
	if got := parityRun(t, files, nil); !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
}

// Every excuse carries a reason, the same rule the coverage floor and the
// leak allowlist keep.
func TestParityExcusesHaveReasons(t *testing.T) {
	for _, excuses := range []map[string]string{parityPrereqOnly, parityCIOnly} {
		for name, reason := range excuses {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%q is excused from parity with no reason", name)
			}
		}
	}
}

func TestParityHoldsTheTreesTagsToAVetPass(t *testing.T) {
	const vet = "\t$(GO) vet -tags=live ./...\n"
	checkParityCases(t, []parityCase{
		{"a file an OS tag and a custom tag build, which no pass vets", nil,
			map[string]string{"internal/e2e/e2e_test.go": "//go:build e2e && linux\n\npackage e2e\n"},
			[]string{
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and `make check` vets no pass that builds it; a contributor cannot see it break",
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
			}},
		{"two tags set by one pass, the flag anywhere among vet's arguments", []docsEdit{
			replace(fxMakefile, vet, vet+"\t$(GO) vet -v -tags=integration,slow ./...\n"),
			replace(fxCI, "        run: go vet -tags=live ./...\n", "        run: go vet -tags=live ./...\n      - run: go vet -v -tags=integration,slow ./...\n"),
		}, map[string]string{"internal/soak/soak_test.go": "//go:build integration && slow\n\npackage soak\n"}, nil},
		{"a file either of two tags builds, with one of them vetted", nil,
			map[string]string{"internal/probe/probe.go": "//go:build live || evals\n\npackage probe\n"}, nil},
		{"a test that turns vet off, which is not a vet pass", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) test -vet=off -tags=live ./...\n"),
		}, nil, []string{
			".github/workflows/ci.yml:147 runs `go vet -tags=live ./...` and `make check` does not",
			"`make vet` runs \"go test -vet=off -tags=live ./...\" and ci.yml never does",
			"scripts/evals/ab.go builds only with -tags=live (//go:build live) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
		// A pass compiles a file when the file's line holds with exactly the
		// pass's tags: one more tag can keep it out.
		{"a file another tag keeps out, vetted only by a pass with that tag", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) vet -tags=live,evals ./...\n"),
		}, map[string]string{"internal/probe/probe.go": "//go:build live && !evals\n\npackage probe\n"}, []string{
			".github/workflows/ci.yml:147 runs `go vet -tags=live ./...` and `make check` does not",
			"`make vet` runs \"go vet -tags=live,evals ./...\" and ci.yml never does",
			"internal/probe/probe.go builds only with -tags=live (//go:build live && !evals) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
	})
}

// The tags each file needs: none where the go command sets them, and each
// smallest custom set otherwise, once per //go:build expression.
func TestParityTreeTags(t *testing.T) {
	files := map[string]string{
		"a/live.go":         "//go:build live && unix\n\npackage a\n",
		"a/other.go":        "//go:build !windows\n\npackage a\n",
		"a/windows.go":      "//go:build windows\n\npackage a\n",
		"a/solaris.go":      "//go:build solaris || illumos || aix\n\npackage a\n",
		"a/e2e.go":          "//go:build e2e && !experimental\n\npackage a\n",
		"a/race.go":         "//go:build evals || !race\n\npackage a\n",
		"a/arch.go":         "//go:build amd64.v2 && go1.22\n\npackage a\n",
		"a/exp.go":          "//go:build goexperiment.synctest\n\npackage a\n",
		"a/instrumented.go": "//go:build msan && asan\n\npackage a\n",
		"a/racy.go":         "//go:build race\n\npackage a\n",
		"a/mixed.go":        "//go:build (a && b) || c\n\npackage a\n",
		"a/live2.go":        "//go:build live\n\npackage a\n",
		"a/ignored.go":      "//go:build ignore\n\npackage a\n",
		"a/both.go":         "//go:build integration && slow\n\npackage a\n",
		"a/either.go":       "//go:build alpha || beta\n\npackage a\n",
		"a/plain.go":        "package a\n",
		"a/notes.txt":       "//go:build prose\n",
		"a/testdata/x.go":   "//go:build fixture\n\npackage x\n",
		".hidden/x.go":      "//go:build hidden\n\npackage x\n",
	}
	needs, err := parityTreeTags(docsTree(t, files), slices.Sorted(maps.Keys(files)))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range needs {
		got = append(got, n.file+": "+n.String())
	}
	want := []string{
		"a/both.go: -tags=integration,slow",
		"a/e2e.go: -tags=e2e",
		"a/either.go: -tags=alpha or -tags=beta",
		"a/live.go: -tags=live",
		"a/live2.go: -tags=live",
		"a/mixed.go: -tags=c or -tags=a,b",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parityTreeTags = %q, want %q", got, want)
	}
}

func TestParityVetPassesReadTheTagsFlagAnywhere(t *testing.T) {
	got := parityVetPasses([]string{
		"go vet -tags=live ./...",
		"go vet -v -tags a,b ./... && go test -tags=evals ./...",
		`go vet --tags="x" ./...; go vet ./...`,
		"go vet --tags y ./...",
		"make vet",
		"go test -vet=off -tags=live ./...",
		"go vet ./... -tags=z",
		"go vet -vettool /usr/local/bin/shadow -tags=sh ./...",
		"go vet -c 2 -printf.funcs Wrapf -tags c1 ./...",
		"go vet -tags=old -tags=new ./...",
	})
	want := [][]string{{"live"}, {"a", "b"}, {"x"}, {"y"}, {"sh"}, {"c1"}, {"new"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("parityVetPasses = %q, want %q", got, want)
	}
}

func atRepoRoot(t *testing.T) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
