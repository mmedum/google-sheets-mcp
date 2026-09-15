package main

import "testing"

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
		want bool
	}{
		{"workflow level", "name: ci\ndefaults:\n  run:\n    shell: bash\njobs:\n  test:\n    runs-on: ubuntu-latest\n", true},
		{"workflow level with a comment", "defaults:\n  # every run step\n  run:\n    shell: bash\n", true},
		{"job level only", "jobs:\n  test:\n    defaults:\n      run:\n        shell: bash\n", false},
		{"both, workflow level present", "defaults:\n  run:\n    shell: bash\njobs:\n  test:\n    defaults:\n      run:\n        shell: pwsh\n", true},
		{"defaults without run", "defaults:\n  something: else\njobs:\n  test:\n", false},
		{"run without shell", "defaults:\n  run:\n    working-directory: ./x\njobs:\n  test:\n", false},
		{"shell with no value", "defaults:\n  run:\n    shell:\n", false},
		{"nothing at all", "name: ci\njobs:\n  test:\n    runs-on: ubuntu-latest\n", false},
		{"a step-level shell is not it", "jobs:\n  test:\n    steps:\n      - run: x\n        shell: bash\n", false},
		// defaults.shell is a sibling of defaults.run, not a child of
		// it, and it pins nothing. A check that only asked whether both
		// words appeared inside the defaults block would say yes.
		{"shell beside run, not under it", "defaults:\n  run:\n  shell: bash\njobs:\n  test:\n", false},
		{"shell under a sibling key", "defaults:\n  run:\n  other:\n    shell: bash\n", false},
		// And the depth rule has to be a depth rule rather than a
		// two-space one, or it decays back into a substring search on
		// any file indented differently.
		{"four-space indentation", "defaults:\n    run:\n        shell: bash\njobs:\n    test:\n", true},
		{"tab indentation", "defaults:\n\trun:\n\t\tshell: bash\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pinsShell(tc.in); got != tc.want {
				t.Errorf("pinsShell = %v, want %v", got, tc.want)
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
		// The narrowing a sibling recorded as the fix while still
		// floating: a `~>` value is a constraint whatever follows it.
		{"~> v2.18.0", false},
		{"~> v2", false},
		{"v2", false},
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

// TestUnpinnedTools is the rule the version keys could not reach: an
// action pinned by SHA whose tool is left to float. The two named cases
// are the two that actually shipped unpinned in the Pipedrive server,
// one of them failing a release.
func TestUnpinnedTools(t *testing.T) {
	const sha = "0000000000000000000000000000000000000000"
	cases := []struct {
		name    string
		yaml    string
		wantBad bool
	}{
		{
			"cosign with no release input — this failed a sibling's release",
			"jobs:\n  a:\n    steps:\n      - name: Install cosign\n        uses: sigstore/cosign-installer@" + sha + "\n",
			true,
		},
		{
			"cosign pinned",
			"jobs:\n  a:\n    steps:\n      - uses: sigstore/cosign-installer@" + sha + "\n        with:\n          cosign-release: v3.1.3\n",
			false,
		},
		{
			"syft with no version input — the same hole one step below",
			"jobs:\n  a:\n    steps:\n      - uses: anchore/sbom-action/download-syft@" + sha + "\n",
			true,
		},
		{
			"syft pinned",
			"jobs:\n  a:\n    steps:\n      - uses: anchore/sbom-action/download-syft@" + sha + "\n        with:\n          syft-version: v1.51.1\n",
			false,
		},
		{
			"setup-go pins by reference through the file",
			"jobs:\n  a:\n    steps:\n      - uses: actions/setup-go@" + sha + "\n        with:\n          go-version-file: go.mod\n",
			false,
		},
		{
			"gitleaks takes its scanner version from the environment",
			"jobs:\n  a:\n    steps:\n      - uses: gitleaks/gitleaks-action@" + sha + "\n        env:\n          GITLEAKS_VERSION: 8.31.0\n",
			false,
		},
		{
			"an action that installs nothing needs no version",
			"jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + sha + "\n",
			false,
		},
		{
			"an unknown action is not quietly trusted",
			"jobs:\n  a:\n    steps:\n      - uses: some-vendor/tool-installer@" + sha + "\n        with:\n          version: v1.2.3\n",
			true,
		},
		{
			"the next step's pin does not cover this one",
			"jobs:\n  a:\n    steps:\n      - uses: sigstore/cosign-installer@" + sha + "\n\n      - uses: anchore/sbom-action/download-syft@" + sha + "\n        with:\n          syft-version: v1.51.1\n",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := unpinnedTools("test.yml", c.yaml)
			if got := len(problems) > 0; got != c.wantBad {
				t.Errorf("found %d problem(s), want bad=%v: %v", len(problems), c.wantBad, problems)
			}
		})
	}
}
