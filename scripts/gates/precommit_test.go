package main

import (
	"slices"
	"strings"
	"testing"
)

// The pre-commit hook runs the Makefile target that scans the staged
// changes for credentials.
func TestTheHookRunsTheStagedSecretScan(t *testing.T) {
	for _, s := range precommitSteps() {
		if len(s.cmd) > 1 && s.cmd[0] == "make" && slices.Contains(s.cmd[1:], "secrets-staged") {
			return
		}
	}
	t.Error("no pre-commit step runs make secrets-staged, so a credential reaches the history unscanned")
}

// The pre-commit hook scans the staged changes with gitleaks at the
// Makefile's version: its target runs the module GITLEAKS names, which the
// pins gate holds to one exact version, over what is staged.
func TestTheHookScansTheStagedChangesAtTheMakefilesVersion(t *testing.T) {
	mk := parseMakefile(string(repoFile(t, makefilePath)))
	gitleaks := mk.expand("$(GITLEAKS)")
	if module, _, _ := strings.Cut(gitleaks, "@"); pinsToolByModule(module) != "gitleaks" {
		t.Fatalf("GITLEAKS is %q, which names no gitleaks module", gitleaks)
	}
	recipe := mk.recipe(precommitSecrets)
	if len(recipe) != 1 {
		t.Fatalf("make %s runs %q, want one line", precommitSecrets, recipe)
	}
	runs := goRunsIn(recipe[0])
	if len(runs) != 1 || runs[0].pkg != gitleaks || len(runs[0].args) == 0 || runs[0].args[0] != "git" ||
		!slices.Contains(runs[0].args, "--staged") {
		t.Errorf("make %s runs %q, want gitleaks git --staged at %s", precommitSecrets, recipe[0], gitleaks)
	}
}
