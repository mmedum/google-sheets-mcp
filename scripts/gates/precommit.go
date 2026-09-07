package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// precommit is what the git hook runs: the checks that are fast enough
// to run on every commit and catch the things that are expensive to undo.
//
// The leak scan is here rather than only in CI because a leak reaches
// the history at commit time, and a leak deleted from the tip is still
// in the log.
func precommit() error {
	steps := []struct {
		name string
		run  func() error
	}{
		{"gofmt", func() error {
			out, err := exec.Command("gofmt", "-l", ".").Output()
			if err != nil {
				return err
			}
			if files := strings.TrimSpace(string(out)); files != "" {
				return fmt.Errorf("these files are not gofmt'd:\n%s", files)
			}
			return nil
		}},
		{"go vet", func() error { return command("go", "vet", "./...") }},
		{"leaks", func() error { return leakGate(false) }},
		{"classes", classGate},
		{"transcript", transcriptGate},
	}
	for _, s := range steps {
		fmt.Printf("-- %s\n", s.name)
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

func command(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), strings.TrimSpace(string(out)))
	}
	return nil
}
