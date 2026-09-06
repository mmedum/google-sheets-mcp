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
