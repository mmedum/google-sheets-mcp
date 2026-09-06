package main

import (
	"strings"
	"testing"
)

// cmd/ carries a lower floor than internal/, and the two must not be
// confused: a floor applied to the wrong package either fails the build
// for nothing or holds nothing.
func TestFloorPerPackage(t *testing.T) {
	const internalFloor = 80.0
	for _, tc := range []struct {
		pkg  string
		want float64
	}{
		{"cmd/google-sheets-mcp", cmdFloor},
		{"internal/service", internalFloor},
		{"internal/a1", internalFloor},
		// A package that merely mentions cmd is not under the cmd floor.
		{"internal/cmdish", internalFloor},
	} {
		if got := floorFor(tc.pkg, internalFloor); got != tc.want {
			t.Errorf("floorFor(%q) = %.0f, want %.0f", tc.pkg, got, tc.want)
		}
	}
}

// The package list is derived rather than written down, so a package
// added under either tree is under a floor from its first commit. That
// is the property that failed for cmd/ until phase 1: nothing listed it,
// so nothing noticed it had no tests at all.
func TestBothTreesAreUnderAFloor(t *testing.T) {
	atRepoRoot(t)
	module, err := moduleName()
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := coveredPackages(module)
	if err != nil {
		t.Fatal(err)
	}
	var hasInternal, hasCmd bool
	for _, p := range pkgs {
		if strings.HasPrefix(p, "internal/") {
			hasInternal = true
		}
		if strings.HasPrefix(p, "cmd/") {
			hasCmd = true
		}
	}
	if !hasInternal {
		t.Error("no package under internal/ is under the floor")
	}
	if !hasCmd {
		t.Error("no package under cmd/ is under the floor; that is the gap this check exists to close")
	}
}

// An exemption without a reason is how a floor quietly stops holding.
func TestExemptionsCarryReasons(t *testing.T) {
	for pkg, reason := range exemptFromFloor {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is exempt from the coverage floor with no reason", pkg)
		}
	}
}

// A package's number is its own. It used to include everything under it,
// which made a well-covered package fail on its subpackage's coverage
// and printed a percentage that described neither of them.
func TestAPackageIsNotScoredOnItsSubpackages(t *testing.T) {
	s := statements{total: map[string]int{}, hit: map[string]bool{}}
	// Ten statements in the parent, all covered; ten in the child, none.
	for i := range 10 {
		parent := "example.com/m/internal/gapi/client.go:" + itoa(i)
		child := "example.com/m/internal/gapi/sheetstest/server.go:" + itoa(i)
		s.total[parent], s.hit[parent] = 1, true
		s.total[child] = 1
	}
	if got := s.percent("example.com/m/internal/gapi"); got != 100 {
		t.Errorf("the parent scored %.1f%%, and every one of its own statements is covered", got)
	}
	if got := s.percent("example.com/m/internal/gapi/sheetstest"); got != 0 {
		t.Errorf("the child scored %.1f%%, and none of its statements are covered", got)
	}
}

func TestPackageOfReadsTheDirectory(t *testing.T) {
	for block, want := range map[string]string{
		"example.com/m/internal/a1/a1.go:12.3,14.4": "example.com/m/internal/a1",
		"example.com/m/cmd/x/main.go:1.1,2.2":       "example.com/m/cmd/x",
		"nocolon":                                   "",
		"noslash.go:1.1,2.2":                        "",
	} {
		if got := packageOf(block); got != want {
			t.Errorf("packageOf(%q) = %q, want %q", block, got, want)
		}
	}
}

func itoa(i int) string { return string(rune('0' + i)) }
