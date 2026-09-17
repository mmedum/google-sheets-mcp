package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed manifest has to survive its own check, and it has to
// carry the placeholder rather than a version.
func TestCommittedManifestPacks(t *testing.T) {
	atRepoRoot(t)
	if err := mcpbGate(io.Discard); err != nil {
		t.Fatalf("the committed manifest: %v", err)
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
			config(m)["command"] = "${__dirname}/server/typo"
		},
		"a win32 override with a typo in the .exe": func(m map[string]any) {
			overrides(m)["win32"] = map[string]any{"command": "${__dirname}/server/google-sheets-mcp.ex"}
		},
		"a linux override nobody stages": func(m map[string]any) {
			overrides(m)["linux"] = map[string]any{"command": "${__dirname}/server/launch.sh"}
		},
		"an env spending a key nobody declares": func(m map[string]any) {
			config(m)["env"] = map[string]any{"GSHEETS_CLIENT_SECRET": "${user_config.oauth_json}"}
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

func config(m map[string]any) map[string]any {
	c, _ := server(m)["mcp_config"].(map[string]any)
	return c
}

func overrides(m map[string]any) map[string]any {
	o, _ := config(m)["platform_overrides"].(map[string]any)
	return o
}

// The bundle is a zip with a fixed shape: a manifest at the root, the
// binaries under server/, and the licence and README beside them. The
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
		"server/google-sheets-mcp", "server/google-sheets-mcp.exe",
		"server/google-sheets-mcp-amd64", "server/google-sheets-mcp-arm64",
		"server/linux-launch.sh",
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
