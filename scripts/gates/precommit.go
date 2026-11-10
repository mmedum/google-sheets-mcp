package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// precommitSecrets is the Makefile target the hook scans the staged
// changes with.
const precommitSecrets = "secrets-staged"

// precommit is what the git hook runs: the checks that are fast enough
// to run on every commit and catch the things that are expensive to undo.
//
// The leak scans are here rather than only in CI because a leak reaches
// the history at commit time, and a leak deleted from the tip is still
// in the log. gitleaks runs through its Makefile target, at the version
// the pins gate holds.
func precommit() error {
	for _, s := range precommitSteps() {
		fmt.Printf("-- %s\n", s.name)
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// precommitStep is one check the hook runs. cmd is the command a command
// step runs, kept beside run so a test can read what the hook runs.
type precommitStep struct {
	name string
	cmd  []string
	run  func() error
}

// precommitCommand is a step that runs one command.
func precommitCommand(name string, cmd ...string) precommitStep {
	return precommitStep{name: name, cmd: cmd, run: func() error { return runCommand(cmd[0], cmd[1:]...) }}
}

func precommitSteps() []precommitStep {
	return []precommitStep{
		{name: "gofmt", run: func() error {
			out, err := exec.Command("gofmt", "-l", ".").Output()
			if err != nil {
				return err
			}
			if files := strings.TrimSpace(string(out)); files != "" {
				return fmt.Errorf("these files are not gofmt'd:\n%s", files)
			}
			return nil
		}},
		precommitCommand("go vet", "go", "vet", "./..."),
		{name: "leaks", run: func() error { return leakGate(false) }},
		precommitCommand("secrets", "make", "--no-print-directory", precommitSecrets),
		{name: "classes", run: classGate},
		{name: "transcript", run: transcriptGate},
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), strings.TrimSpace(string(out)))
	}
	return nil
}
