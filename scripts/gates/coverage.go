package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// exemptFromFloor are the packages the coverage floor does not apply to,
// each with its reason. It is empty, and every package under internal/
// meets the floor.
//
// The list it *does* apply to is derived from `go list ./internal/...`,
// so a package added under internal/ is under the floor from its first
// commit. A hand-written list of covered packages goes stale in silence;
// a hand-written list of exemptions cannot, because every entry has to
// be written here where a reviewer sees it — and an entry with no reason
// is refused below.
var exemptFromFloor = map[string]string{}

// coverageFloor enforces statement coverage per package.
//
// The profile comes from -coverpkg=./internal/..., so a package's
// statements appear once per test binary that linked it and are
// de-duplicated here.
func coverageFloor(profile string, minimum float64) error {
	module, err := moduleName()
	if err != nil {
		return err
	}
	pkgs, err := internalPackages(module)
	if err != nil {
		return err
	}
	if len(pkgs) < 5 {
		return fmt.Errorf("only %d packages under internal/; the list is not being read", len(pkgs))
	}
	covered, err := readProfile(profile)
	if err != nil {
		return err
	}

	for pkg, reason := range exemptFromFloor {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("the exemption for %s has no reason; that is how a floor quietly stops holding", pkg)
		}
	}

	var below []string
	checked := 0
	for _, pkg := range pkgs {
		if reason, ok := exemptFromFloor[pkg]; ok {
			fmt.Printf("%-30s   exempt: %s\n", pkg, reason)
			continue
		}
		checked++
		pct := covered.percent(module + "/" + pkg + "/")
		fmt.Printf("%-30s %6.1f%%\n", pkg, pct)
		if pct < minimum {
			below = append(below, fmt.Sprintf("%s (%.1f%%)", pkg, pct))
		}
	}
	if checked == 0 {
		return fmt.Errorf("every package is exempt; the floor is holding nothing")
	}
	if len(below) > 0 {
		return fmt.Errorf("coverage below %.0f%% in: %s", minimum, strings.Join(below, ", "))
	}
	return nil
}

// statements counts covered and total statements per block, keyed by the
// block's position so the same block reported by several test binaries
// counts once.
type statements struct {
	total map[string]int
	hit   map[string]bool
}

func (s statements) percent(prefix string) float64 {
	var total, cov int
	for block, n := range s.total {
		if !strings.HasPrefix(block, prefix) {
			continue
		}
		total += n
		if s.hit[block] {
			cov += n
		}
	}
	if total == 0 {
		return 0
	}
	return 100 * float64(cov) / float64(total)
}

func readProfile(path string) (statements, error) {
	f, err := os.Open(path)
	if err != nil {
		return statements{}, fmt.Errorf("coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	s := statements{total: map[string]int{}, hit: map[string]bool{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first { // "mode: atomic"
			first = false
			continue
		}
		// path/to/file.go:1.2,3.4 <numStatements> <count>
		block, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		numStr, countStr, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		num, err1 := strconv.Atoi(numStr)
		count, err2 := strconv.Atoi(countStr)
		if err1 != nil || err2 != nil {
			continue
		}
		if _, seen := s.total[block]; !seen {
			s.total[block] = num
		}
		if count > 0 {
			s.hit[block] = true
		}
	}
	if err := sc.Err(); err != nil {
		return statements{}, err
	}
	if len(s.total) == 0 {
		return statements{}, fmt.Errorf("%s has no coverage blocks; the run that produced it covered nothing", path)
	}
	return s, nil
}

func moduleName() (string, error) {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func internalPackages(module string) ([]string, error) {
	out, err := exec.Command("go", "list", "./internal/...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./internal/...: %w", err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pkgs = append(pkgs, strings.TrimPrefix(line, module+"/"))
	}
	sort.Strings(pkgs)
	return pkgs, nil
}
