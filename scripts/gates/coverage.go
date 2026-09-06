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
// each with its reason.
//
// The list it *does* apply to is derived from `go list`, so a package
// added under internal/ or cmd/ is under a floor from its first commit.
// A hand-written list of covered packages goes stale in silence; a
// hand-written list of exemptions cannot, because every entry has to be
// written here where a reviewer sees it — and an entry with no reason is
// refused below.
var exemptFromFloor = map[string]string{}

// cmdFloor is the floor for the command package, which is lower than the
// one internal/ meets and is a floor rather than an exemption.
//
// cmd/ was outside the profile entirely until phase 1's audit: nothing
// measured it, so nothing could notice it had no tests at all — 571
// lines of login, logout, status and doctor. Most of what is left
// uncovered opens a browser, holds a token or serves stdio, and the live
// driver and the smoke gate cover those against the real thing. The
// number is a little under what the tests reach today, so it ratchets
// rather than describing an ambition. The gap is for the matrix: which
// branch `tokenStore` and `logout` take depends on whether a keyring is
// available, and that differs on macOS and Windows.
const cmdFloor = 40.0

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
	pkgs, err := coveredPackages(module)
	if err != nil {
		return err
	}
	if len(pkgs) < 5 {
		return fmt.Errorf("only %d packages under internal/ and cmd/; the list is not being read", len(pkgs))
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
		floor := floorFor(pkg, minimum)
		pct := covered.percent(module + "/" + pkg)
		note := ""
		if floor != minimum {
			note = fmt.Sprintf("   (floor %.0f%%)", floor)
		}
		fmt.Printf("%-30s %6.1f%%%s\n", pkg, pct, note)
		if pct < floor {
			below = append(below, fmt.Sprintf("%s (%.1f%%, floor %.0f%%)", pkg, pct, floor))
		}
	}
	if checked == 0 {
		return fmt.Errorf("every package is exempt; the floor is holding nothing")
	}
	if len(below) > 0 {
		return fmt.Errorf("coverage below its floor in: %s", strings.Join(below, ", "))
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

// percent is the coverage of one package: the blocks whose file sits in
// that directory, and not the ones in a package under it.
//
// A prefix match is what this used to do, and it meant a package was
// scored on its subpackages as well. It held for two phases because the
// only subpackage was small and well covered; the moment sheetstest grew
// it dragged gapi under a floor gapi was meeting on its own, and the
// number the gate printed for gapi was a number for neither package.
func (s statements) percent(pkg string) float64 {
	var total, cov int
	for block, n := range s.total {
		if packageOf(block) != pkg {
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

// packageOf is the import path a coverage block belongs to: everything
// before the file name in "path/to/file.go:1.2,3.4".
func packageOf(block string) string {
	file, _, ok := strings.Cut(block, ":")
	if !ok {
		return ""
	}
	i := strings.LastIndexByte(file, '/')
	if i < 0 {
		return ""
	}
	return file[:i]
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

// coveredPackages is everything under a floor: internal/ and cmd/.
//
// scripts/ is deliberately absent — the gates have their own tests and
// the live drivers are behind a build tag — but the two that ship in the
// binary are both here.
func coveredPackages(module string) ([]string, error) {
	out, err := exec.Command("go", "list", "./internal/...", "./cmd/...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
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

// floorFor is the minimum this package has to meet.
func floorFor(pkg string, minimum float64) float64 {
	if strings.HasPrefix(pkg, "cmd/") {
		return cmdFloor
	}
	return minimum
}
