package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The committed manifest has to survive its own check, and it has to
// carry the placeholder rather than a version.
func TestCommittedManifestPacks(t *testing.T) {
	atRepoRoot(t)
	var out bytes.Buffer
	if err := mcpbGate(&out); err != nil {
		t.Fatalf("the committed manifest: %v\n%s", err, out.String())
	}
	// And it read something: today's 8 files, 3 platforms and 6
	// user_config keys.
	var files, platforms, keys int
	if _, err := fmt.Sscanf(out.String(), "bundle manifest ok: %d file(s), %d platform(s), %d user_config key(s)",
		&files, &platforms, &keys); err != nil {
		t.Fatalf("cannot read the summary %q: %v", out.String(), err)
	}
	if files < 8 || platforms < 3 || keys < 6 {
		t.Errorf("summary %q is under its floors of 8 files, 3 platforms and 6 user_config keys", out.String())
	}
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		t.Errorf("the committed manifest claims version %q; a version in the tree is a stale one", got)
	}
}

// Break it every way the check exists for, and watch each refusal. A
// check nobody has watched fail is a check nobody knows the shape of —
// and each of these is well formed by any schema, which is why the
// schema is not the validation worth having.
func TestManifestIsHeldToTheStagedTree(t *testing.T) {
	atRepoRoot(t)
	contents := bundleNames()

	for name, breaks := range map[string]func(map[string]any){
		"an entry point nobody stages": func(m map[string]any) {
			server(m)["entry_point"] = "server/not-the-binary"
		},
		"a command nobody stages": func(m map[string]any) {
			mcpConfig(m)["command"] = "${__dirname}/server/typo"
		},
		"a win32 override with a typo in the .exe": func(m map[string]any) {
			overrides(m)["win32"] = map[string]any{"command": "${__dirname}/server/google-sheets-mcp.ex"}
		},
		"a linux override nobody stages": func(m map[string]any) {
			overrides(m)["linux"] = map[string]any{"command": "${__dirname}/server/launch.sh"}
		},
		"an env spending a key nobody declares": func(m map[string]any) {
			mcpConfig(m)["env"] = map[string]any{"GSHEETS_CLIENT_SECRET": "${user_config.oauth_json}"}
		},
		"an override for a platform nobody claims": func(m map[string]any) {
			overrides(m)["freebsd"] = map[string]any{"command": "${__dirname}/server/google-sheets-mcp"}
		},
		"a server that is not a binary": func(m map[string]any) {
			server(m)["type"] = "node"
		},
	} {
		manifest, err := readManifest()
		if err != nil {
			t.Fatal(err)
		}
		breaks(manifest)
		if problems := checkManifest(manifest, contents); len(problems) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}

	// And the manifest as it stands is accepted, so the cases above fail
	// for the reason they name rather than because everything fails.
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "1.0.0"
	if problems := checkManifest(manifest, contents); len(problems) > 0 {
		t.Errorf("the committed manifest was refused: %v", problems)
	}
}

func server(m map[string]any) map[string]any {
	s, _ := m["server"].(map[string]any)
	return s
}

func mcpConfig(m map[string]any) map[string]any {
	c, _ := server(m)["mcp_config"].(map[string]any)
	return c
}

func overrides(m map[string]any) map[string]any {
	o, _ := mcpConfig(m)["platform_overrides"].(map[string]any)
	return o
}

// The bundle is a zip with a fixed shape: a manifest at the root, the
// binaries under server/, and the license and README beside them. The
// modes come from the name, because a Windows .exe staged from a
// filesystem that lost the execute bit arrives unrunnable.
func TestBundleLayoutAndModes(t *testing.T) {
	atRepoRoot(t)
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "1.2.3"

	// Stand-ins for the binaries, so the layout is testable without a
	// release build.
	dir := t.TempDir()
	contents := bundleExtras()
	for _, f := range bundleFiles() {
		src := filepath.Join(dir, strings.ReplaceAll(f.as, "/", "_"))
		if err := os.WriteFile(src, []byte("binary\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		contents[f.as] = src
	}

	bundle := filepath.Join(dir, "test.mcpb")
	if err := writeBundle(bundle, manifest, contents); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()

	seen := map[string]os.FileMode{}
	for _, f := range zr.File {
		seen[f.Name] = f.Mode().Perm()
		if f.Method != zip.Deflate {
			t.Errorf("%s is not deflated", f.Name)
		}
		// Unset writes zeroes, which display as the impossible
		// 1980-00-00; a fixed time also makes the archive byte-identical
		// for the same inputs.
		if f.Modified.IsZero() || !f.Modified.Equal(zipEpoch) {
			t.Errorf("%s carries modification time %v", f.Name, f.Modified)
		}
	}
	for _, want := range []string{
		"manifest.json", "LICENSE", "README.md",
		"server/google-sheets-mcp-darwin", "server/google-sheets-mcp.exe",
		"server/google-sheets-mcp-linux-amd64", "server/google-sheets-mcp-linux-arm64",
		"server/launch-linux.sh",
	} {
		mode, ok := seen[want]
		if !ok {
			t.Errorf("the bundle has no %s", want)
			continue
		}
		if strings.HasPrefix(want, "server/") && mode&0o111 == 0 {
			t.Errorf("%s is mode %o and has to be runnable", want, mode)
		}
		if !strings.HasPrefix(want, "server/") && mode&0o111 != 0 {
			t.Errorf("%s is mode %o and is not run", want, mode)
		}
	}
	if len(seen) != 8 {
		t.Errorf("the bundle carries %d files, want 8: %v", len(seen), sortedNames(map[string]string{}))
	}

	// The version reaches the manifest inside the bundle, through a
	// decode and an encode rather than a substitution over text.
	rc, err := zr.Open("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	var packed map[string]any
	if err := json.NewDecoder(rc).Decode(&packed); err != nil {
		t.Fatal(err)
	}
	if got, _ := packed["version"].(string); got != "1.2.3" {
		t.Errorf("the packed manifest claims version %q", got)
	}
	if got, _ := packed["name"].(string); got != bundleName {
		t.Errorf("the packed manifest is for %q", got)
	}
}

// The same inputs give the same bytes, which is the least a build can
// offer somebody checking a checksum.
func TestBundleIsReproducible(t *testing.T) {
	atRepoRoot(t)
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "1.2.3"
	dir := t.TempDir()
	contents := bundleExtras()
	for _, f := range bundleFiles() {
		src := filepath.Join(dir, strings.ReplaceAll(f.as, "/", "_"))
		if err := os.WriteFile(src, []byte("binary\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		contents[f.as] = src
	}
	var bytesOf [2][]byte
	for i := range bytesOf {
		bundle := filepath.Join(dir, "run.mcpb")
		if err := writeBundle(bundle, manifest, contents); err != nil {
			t.Fatal(err)
		}
		if bytesOf[i], err = os.ReadFile(bundle); err != nil {
			t.Fatal(err)
		}
	}
	if string(bytesOf[0]) != string(bytesOf[1]) {
		t.Error("two packs of the same inputs gave different bytes")
	}
}

// A glob that matched two would pack whichever sorted first, and a
// bundle built from the wrong binary is not something a checksum
// catches: the checksum is of whatever was built.
func TestOnlyMatchRefusesTwo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a_linux_amd64_v1", "a_linux_amd64_v2"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, bundleName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := onlyMatch(filepath.Join(dir, "*linux_amd64*", bundleName), "linux amd64 binary"); err == nil {
		t.Error("a glob matching two files was accepted")
	}
	if _, err := onlyMatch(filepath.Join(dir, "*nothing*", bundleName), "nothing"); err == nil {
		t.Error("a glob matching nothing was accepted")
	}
}

// The three ways a manifest can misdeclare its own version, and the
// missing support URL. This check did not exist while three of the seven
// servers drifted apart on manifest_version — one carried no $schema,
// and two declared 0.2 while pointing at the UNPINNED schema path, which
// serves whatever upstream publishes today.
func TestTheWaysAManifestMisdeclaresItsVersion(t *testing.T) {
	const pinned = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.3.schema.json"
	const bySHA = "https://raw.githubusercontent.com/anthropics/mcpb/" +
		"0123456789abcdef0123456789abcdef01234567/schemas/mcpb-manifest-v0.3.schema.json"

	for _, ok := range []string{pinned, bySHA} {
		if problems := manifestShapeProblems(ok, "0.3", "https://example.invalid/issues"); len(problems) > 0 {
			t.Fatalf("a well-formed manifest was refused:\n%s", strings.Join(problems, "\n"))
		}
	}

	cases := []struct{ name, schema, version, support, want string }{
		{"no $schema at all", "", "0.3", "x", "no $schema"},
		{
			"the unpinned schema path",
			"https://raw.githubusercontent.com/anthropics/mcpb/main/dist/mcpb-manifest.schema.json",
			"0.2", "x", "not the pinned",
		},
		{"a pinned schema that disagrees", pinned, "0.2", "x", "cannot claim one version"},
		{"no support URL", pinned, "0.3", "", "no support URL"},
		{
			// The ref pins the BYTES. A branch can be amended under a
			// document that claims to conform to it.
			"a schema served from a branch",
			"https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "can be re-pointed",
		},
		{
			// What refusing branch NAMES passes: a tag upstream moves
			// as it releases. It reads as pinned.
			"a partial tag",
			"https://raw.githubusercontent.com/anthropics/mcpb/v2.1/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "can be re-pointed",
		},
		{
			// And what both of those pass: the right filename, served
			// by somebody else.
			"the right file from another host",
			"https://example.invalid/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "not upstream's published path",
		},
		{
			// The case the other claims structurally cannot see: 0.2
			// beside a 0.2 schema is stale and self-consistent, which
			// is how the shape spread between repositories.
			"a self-consistent manifest a version behind",
			"https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.2.schema.json",
			"0.2", "x", "still a manifest a version behind",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := manifestShapeProblems(tc.schema, tc.version, tc.support)
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("wanted %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// The floor compares numbers, not text. "0.10" sorts before "0.3" as a
// string, which is a bug that waits for the tenth minor version.
func TestTheFloorComparesVersionsNumerically(t *testing.T) {
	for _, c := range []struct {
		version, floor string
		want           bool
	}{
		{"0.3", "0.3", false},
		{"0.2", "0.3", true},
		{"0.4", "0.3", false},
		{"0.10", "0.3", false},
		{"0.9", "1.0", true},
		{"nonsense", "0.3", true},
	} {
		if got := olderThan(c.version, c.floor); got != c.want {
			t.Errorf("olderThan(%q, %q) = %v, want %v", c.version, c.floor, got, c.want)
		}
	}
}

// The floor is a claim about what somebody checked, so the committed
// manifest has to meet it. Stated against the constant, not read from
// the manifest: a test that takes its expected value from the file it
// checks passes on any file.
func TestTheCommittedManifestMeetsTheFloor(t *testing.T) {
	atRepoRoot(t)
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	declared, _ := manifest["manifest_version"].(string)
	if declared != "0.3" {
		t.Fatalf("the committed manifest declares %q; this repository has checked 0.3", declared)
	}
}

// committedManifest is the committed manifest with one edit applied.
func committedManifest(t *testing.T, edit func(map[string]any)) map[string]any {
	t.Helper()
	atRepoRoot(t)
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	edit(manifest)
	return manifest
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		doc = doc[key].(map[string]any)
	}
	return doc
}

// The two claims the family's bundles all make about themselves, each
// broken once in the committed manifest.
func TestTheManifestNamesAPrivacyPolicyAndTheFloor(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"no privacy policy", func(m map[string]any) { delete(m, "privacy_policies") }, "privacy_policies is empty"},
		{"an empty privacy policy list", func(m map[string]any) { m["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
		{"no Claude Desktop floor", func(m map[string]any) { delete(dig(m, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
		{"another floor", func(m map[string]any) { dig(m, "compatibility")["claude_desktop"] = ">=0.9.0" },
			`compatibility.claude_desktop is ">=0.9.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(checkManifest(committedManifest(t, tc.edit), bundleNames()), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", got, tc.want)
			}
		})
	}
}

// Each rule the bundle's settings are held to, broken once in the
// committed manifest, and the shape that ships passing all of them.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "the shape that ships", edit: func(map[string]any) {}},
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "user_config.enable_destructive is a boolean defaulting to true; GSHEETS_ENABLE_DESTRUCTIVE is a " +
			"switch the server defaults to false"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "read_only")["type"] = "string"
			dig(d, "user_config", "read_only")["default"] = "false"
		}, `user_config.read_only is a string defaulting to "false"; GSHEETS_READ_ONLY is a switch`},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "enable_data_sources"), "default")
		}, "user_config.enable_data_sources is a boolean defaulting to null"},
		{"an optional key with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "profile"), "default")
		}, "user_config.profile is optional with no default"},
		{"a setting the bundle no longer asks for", func(d map[string]any) {
			delete(dig(d, "user_config"), "allow_external_formulas")
			delete(dig(d, "server", "mcp_config", "env"), "GSHEETS_ALLOW_EXTERNAL_FORMULAS")
		}, "user_config does not ask for allow_external_formulas, which sets GSHEETS_ALLOW_EXTERNAL_FORMULAS"},
		{"a setting spent under another key", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GSHEETS_ENABLE_DATA_SOURCES"] = "${user_config.enable_destructive}"
		}, `mcp_config.env.GSHEETS_ENABLE_DATA_SOURCES is "${user_config.enable_destructive}", want ` +
			`"${user_config.enable_data_sources}"`},
		{"a key no setting has", func(d map[string]any) {
			dig(d, "user_config")["verbose"] = map[string]any{
				"type": "boolean", "title": "Verbose", "description": "d", "default": false}
		}, "user_config asks for verbose, which is no setting's key"},
		{"a variable the bundle does not ask for", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GSHEETS_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GSHEETS_LOG_LEVEL, which the bundle does not ask for"},
		{"a default that configures the server", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "work"
		}, "the bundle's defaults configure the server otherwise than no environment does"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "Not A Profile"
		}, `the environment the bundle's defaults give the server is refused: config: invalid: profile "Not A Profile"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := settingProblems(committedManifest(t, tc.edit), serverVars())
			if tc.want == "" {
				if len(problems) > 0 {
					t.Fatalf("the shape that ships was refused:\n%s", strings.Join(problems, "\n"))
				}
				return
			}
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// The 4.0.0 bundle's names are refused, so the family's layout cannot
// drift back in one file and not the others.
func TestTheOldLayoutIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, entry, linux, want string }{
		{"the old macOS name", "server/google-sheets-mcp", "server/launch-linux.sh",
			`server.entry_point names "server/google-sheets-mcp"`},
		{"the old launcher name", "server/google-sheets-mcp-darwin", "server/linux-launch.sh",
			`platform_overrides.linux.command names "server/linux-launch.sh"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := committedManifest(t, func(d map[string]any) {
				dig(d, "server")["entry_point"] = tc.entry
				dig(d, "server", "mcp_config", "platform_overrides", "linux")["command"] = "${__dirname}/" + tc.linux
			})
			problems := strings.Join(checkManifest(manifest, bundleNames()), "\n")
			if !strings.Contains(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant %q", problems, tc.want)
			}
		})
	}
	// And the launcher still looking for the old Linux names.
	old := "bin=$dir/google-sheets-mcp-amd64\nbin=$dir/google-sheets-mcp-arm64\n" +
		`echo "Install with: go install example.com/m/v4/cmd/google-sheets-mcp@latest" >&2`
	got := strings.Join(launcherProblems(old, "module example.com/m/v4\n"), "\n")
	for _, want := range []string{
		"the packer stages server/google-sheets-mcp-linux-amd64 and the launcher never names it",
		"the packer stages server/google-sheets-mcp-linux-arm64 and the launcher never names it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("problems:\n%s\nwant %q", got, want)
		}
	}
}

// Every variable the server reads has a verdict: asked for, or left out
// with a reason. A new one with neither fails, and so does a verdict on
// one the server no longer reads.
func TestEverySettingHasABundleVerdict(t *testing.T) {
	known := serverVars()
	if len(known) < 15 {
		t.Fatalf("the server reads %d variables; this would be checking a stub", len(known))
	}
	manifest := committedManifest(t, func(map[string]any) {})
	if p := settingProblems(manifest, known); len(p) != 0 {
		t.Fatalf("the committed manifest fails: %v", p)
	}
	got := strings.Join(settingProblems(manifest, append(slices.Clone(known), "GSHEETS_NEW")), "\n")
	if !strings.Contains(got, "the server reads GSHEETS_NEW, and the bundle neither asks for it nor says why not") {
		t.Errorf("a setting with no verdict passed: %s", got)
	}
	without := slices.DeleteFunc(slices.Clone(known), func(s string) bool { return s == "GSHEETS_WRITE_TIMEOUT" })
	got = strings.Join(settingProblems(manifest, without), "\n")
	if !strings.Contains(got, "the bundle has a verdict on GSHEETS_WRITE_TIMEOUT, which the server does not read") {
		t.Errorf("a verdict on a setting the server does not read passed: %s", got)
	}
}

// The launcher's install hint names the module go.mod declares, read
// under Windows line endings too. The path moves with each major
// version, so a hint left behind sends somebody to an old one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v4/cmd/google-sheets-mcp@latest" >&2`
	for _, goMod := range []string{"module example.com/m/v4\n\ngo 1.27\n", "module example.com/m/v4\r\n\r\ngo 1.27\r\n"} {
		if problems := installHintProblems(hint, goMod); len(problems) > 0 {
			t.Errorf("go.mod %q: %v", goMod, problems)
		}
	}
	for _, tc := range []struct{ name, launcher, goMod, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v4", "", 1), "module example.com/m/v4\n",
			`the launcher says "go install example.com/m/cmd/google-sheets-mcp@latest"`},
		{"a hint a major version behind", strings.Replace(hint, "/v4", "/v3", 1), "module example.com/m/v4\r\n",
			`the launcher says "go install example.com/m/v3/cmd/google-sheets-mcp@latest"`},
		{"no hint at all", "exit 1", "module example.com/m/v4\n", "gives no install hint"},
		{"no module line", hint, "go 1.27\n", "go.mod names no module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(installHintProblems(tc.launcher, tc.goMod), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

// The launcher runs the binary staged for the architecture, by the name
// the packer stages it under, and every way it fails leaves stdout,
// which is the JSON-RPC channel, empty.
func TestTheLinuxLauncherPicksABinaryAndKeepsStdoutClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is /bin/sh, and it is not the Windows entry point")
	}
	atRepoRoot(t)
	dir := t.TempDir()
	launcher := filepath.Join(dir, "launch-linux.sh")
	body, err := os.ReadFile(bundleExtras()[linuxLauncher])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, body, 0o700); err != nil {
		t.Fatal(err)
	}
	// The names come from the packer, so a rename that broke the pairing
	// fails here rather than on somebody's machine.
	var staged []string
	for _, b := range bundleFiles() {
		if !b.byLauncher {
			continue
		}
		name := path.Base(b.as)
		staged = append(staged, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho ran "+name+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(staged) != 2 {
		t.Fatalf("the packer stages %v for the launcher, want the two Linux binaries", staged)
	}
	fake := filepath.Join(dir, "fake")
	if err := os.Mkdir(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fake, "uname"), []byte("#!/bin/sh\necho \"$FAKE_ARCH\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(arch string) (stdout, stderr string, code int) {
		cmd := exec.Command("/bin/sh", launcher)
		cmd.Env = append(os.Environ(), "FAKE_ARCH="+arch, "PATH="+fake+":"+os.Getenv("PATH"))
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.String(), errOut.String(), exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("running the launcher for %s: %v", arch, err)
		}
		return out.String(), errOut.String(), 0
	}

	for _, c := range []struct{ arch, want string }{
		{"x86_64", "google-sheets-mcp-linux-amd64"},
		{"amd64", "google-sheets-mcp-linux-amd64"},
		{"aarch64", "google-sheets-mcp-linux-arm64"},
		{"arm64", "google-sheets-mcp-linux-arm64"},
	} {
		stdout, stderr, code := run(c.arch)
		if code != 0 {
			t.Errorf("%s: exit %d, stderr %q", c.arch, code, stderr)
		}
		if strings.TrimSpace(stdout) != "ran "+c.want {
			t.Errorf("%s ran %q, want %s", c.arch, strings.TrimSpace(stdout), c.want)
		}
	}

	stdout, stderr, code := run("riscv64")
	if code == 0 {
		t.Error("an architecture the bundle does not carry exited 0")
	}
	if stdout != "" {
		t.Errorf("the launcher wrote %q to stdout, which is the JSON-RPC channel", stdout)
	}
	if !strings.Contains(stderr, "riscv64") || !strings.Contains(stderr, "go install") {
		t.Errorf("the complaint does not name the architecture and the install command: %q", stderr)
	}

	// A binary missing from the bundle is the other way this ends, and
	// it must end the same way: nothing on stdout.
	if err := os.Remove(filepath.Join(dir, "google-sheets-mcp-linux-amd64")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = run("x86_64")
	if code == 0 {
		t.Error("a missing binary exited 0")
	}
	if stdout != "" {
		t.Errorf("the launcher wrote %q to stdout for a missing binary", stdout)
	}
	if !strings.Contains(stderr, "google-sheets-mcp-linux-amd64") {
		t.Errorf("the complaint does not name the missing binary: %q", stderr)
	}
}
