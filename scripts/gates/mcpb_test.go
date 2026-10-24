package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
)

// The standard says: break the manifest all six ways and watch each be
// refused before you believe the check. A check nobody has watched fail
// is a check nobody knows the shape of — doing this in a sibling
// repository found a packer that verified the entry point and the Linux
// files and never the win32 override's command, so a typo in the .exe
// path would have packed cleanly, installed cleanly, and been caught by
// nothing.
//
// Each case below breaks exactly one thing in a manifest that is
// otherwise correct, and asserts the sentence that names it.

// repoDir is the repository root, found once, before any test moves the
// working directory.
var repoDir = func() string {
	root, err := repoRoot()
	if err != nil {
		panic(err)
	}
	return root
}()

// repoFile reads a path relative to the repository root.
func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// good is the committed manifest, which every case starts from.
func good(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(repoFile(t, mcpbManifestPath), &m); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	return m
}

// editedRaw is the committed manifest with one edit applied to its
// decoded document, encoded again.
func editedRaw(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, mcpbManifestPath), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// edited is the committed manifest with one edit applied, read back the
// way the gate reads it.
func edited(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(editedRaw(t, edit), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		doc = doc[key].(map[string]any)
	}
	return doc
}

func mentions(problems []string, want string) bool {
	return slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) })
}

// copiedRoot is a repository holding the files the gate and the packer
// read, copied from this one, so that one can be broken.
func copiedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{mcpbManifestPath, launcherPath, "go.mod", "LICENSE", "README.md"} {
		put(t, root, rel, string(repoFile(t, rel)))
	}
	return root
}

// put writes body to rel under root.
func put(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The committed manifest survives every check the gate runs, from the
// repository root, and it read something: today's 8 files, 3 platforms
// and 6 user_config keys.
func TestCommittedManifestPacks(t *testing.T) {
	atRepoRoot(t)
	var out bytes.Buffer
	if err := mcpbGate(&out); err != nil {
		t.Fatalf("the committed manifest: %v\n%s", err, out.String())
	}
	var files, platforms, keys int
	if _, err := fmt.Sscanf(out.String(), "bundle manifest ok: %d file(s), %d platform(s), %d user_config key(s)",
		&files, &platforms, &keys); err != nil {
		t.Fatalf("cannot read the summary %q: %v", out.String(), err)
	}
	if files < 8 || platforms < 3 || keys < 6 {
		t.Errorf("summary %q is under its floors of 8 files, 3 platforms and 6 user_config keys", out.String())
	}
}

// The OAuth client field may be left empty: it is optional and defaults
// to "", which reaches the server as no client path, so it takes the one
// login recorded for the profile.
func TestTheClientFileMayBeLeftEmpty(t *testing.T) {
	m := good(t)
	if opt := m.UserConfig["client_secret"]; opt.Required || string(opt.Default) != `""` {
		t.Errorf("user_config.client_secret is required %t with default %s; want it optional, defaulting to \"\"",
			opt.Required, opt.Default)
	}
	env, _ := hostEnv(m)
	got, given := env["GSHEETS_CLIENT_SECRET"]
	c, err := loadEnv(env)
	if !given || got != "" || err != nil || c.ClientSecretPath != "" {
		t.Errorf("the defaults give GSHEETS_CLIENT_SECRET %q (set %t), and the server %q, %v; want it set empty and read as unset",
			got, given, c.ClientSecretPath, err)
	}
}

func TestTheSixWaysAManifestBreaks(t *testing.T) {
	overrides := []string{"server", "mcp_config", "platform_overrides"}
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{
			name: "an entry point nobody stages",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/google-sheets-mcp-mac" },
			want: `entry_point "server/google-sheets-mcp-mac" is not a program the packer stages`,
		},
		{
			name: "no entry point at all",
			edit: func(d map[string]any) { delete(dig(d, "server"), "entry_point") },
			want: "the manifest declares no entry_point",
		},
		{
			// A staged program, and not the one the default command runs:
			// the bundle names the Windows binary as the server and spawns
			// the macOS one.
			name: "an entry point the default command does not run",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/google-sheets-mcp.exe" },
			want: `entry_point is "server/google-sheets-mcp.exe", and mcp_config.command runs ` +
				`"server/google-sheets-mcp-darwin"`,
		},
		{
			// Packed, and not a program: the license is there to be read.
			name: "an entry point naming the license",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "LICENSE" },
			want: `entry_point "LICENSE" is not a program the packer stages`,
		},
		{
			name: "a win32 override with a typo in the .exe",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "${__dirname}/server/google-sheets-mcp.ex"
			},
			want: `platform_overrides.win32.command is "${__dirname}/server/google-sheets-mcp.ex", ` +
				"which is not a program the packer stages",
		},
		{
			name: "a command outside the bundle",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "/usr/local/bin/google-sheets-mcp"
			},
			want: `mcp_config.command is "/usr/local/bin/google-sheets-mcp"; a command starts with ${__dirname}/`,
		},
		{
			// The specification shows this form once and never says what
			// it is resolved against (§18).
			name: "a relative command",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "server/google-sheets-mcp-darwin"
			},
			want: `mcp_config.command is "server/google-sheets-mcp-darwin"; a command starts with ${__dirname}/`,
		},
		{
			// 4.1.0 stripped the leading slash and read this as staged.
			name: "an override's absolute command",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "/server/google-sheets-mcp.exe"
			},
			want: `platform_overrides.win32.command is "/server/google-sheets-mcp.exe"; a command starts with ${__dirname}/`,
		},
		{
			name: "a command naming the README",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["command"] = "${__dirname}/README.md" },
			want: `mcp_config.command is "${__dirname}/README.md", which is not a program the packer stages`,
		},
		{
			name: "an override naming the license",
			edit: func(d map[string]any) { dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/LICENSE" },
			want: `platform_overrides.linux.command is "${__dirname}/LICENSE", which is not a program the packer stages`,
		},
		{
			// Staged, and a program, and the file staged for another
			// platform: macOS would spawn a Windows binary. entry_point
			// follows the command, so that only this is wrong.
			name: "macOS spawning the Windows binary",
			edit: func(d map[string]any) {
				dig(d, "server")["entry_point"] = "server/google-sheets-mcp.exe"
				dig(d, "server", "mcp_config")["command"] = "${__dirname}/server/google-sheets-mcp.exe"
			},
			want: `platform "darwin" runs "server/google-sheets-mcp.exe", and the file staged for it is ` +
				`"server/google-sheets-mcp-darwin"`,
		},
		{
			name: "Windows spawning the macOS binary",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "${__dirname}/server/google-sheets-mcp-darwin"
			},
			want: `platform "win32" runs "server/google-sheets-mcp-darwin", and the file staged for it is ` +
				`"server/google-sheets-mcp.exe"`,
		},
		{
			// Deleting the override passes every check above: win32 then
			// runs the default command, which is the macOS universal
			// binary, and that file really is in the bundle.
			name: "no win32 override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "win32") },
			want: `platform "win32" runs "server/google-sheets-mcp-darwin"`,
		},
		{
			name: "no linux override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "linux") },
			want: `platform "linux" runs "server/google-sheets-mcp-darwin", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			// Right for one of the two architectures Claude Desktop for
			// Linux ships.
			name: "one Linux binary for both architectures",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/server/google-sheets-mcp-linux-amd64"
			},
			want: `platform "linux" runs "server/google-sheets-mcp-linux-amd64", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			name: "an override for a platform the bundle does not claim",
			edit: func(d map[string]any) {
				dig(d, overrides...)["freebsd"] = map[string]any{"command": "${__dirname}/server/launch-linux.sh"}
			},
			want: `platform_overrides has "freebsd", which compatibility.platforms does not claim`,
		},
		{
			// Composed, not the whole value: a check that only looked at
			// values that ARE a reference would miss this one and the
			// server would start with ${user_config.dir} unsubstituted.
			name: "an env value spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config", "env")["GSHEETS_CONFIG_DIR"] = "${user_config.dir}/google-sheets-mcp"
			},
			want: "mcp_config.env.GSHEETS_CONFIG_DIR spends ${user_config.dir}, which user_config does not declare",
		},
		{
			name: "an argument spending a key nobody declared",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["args"] = []any{"${user_config.verbose}"} },
			want: "mcp_config.args spends ${user_config.verbose}, which user_config does not declare",
		},
		{
			name: "an override's argument spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["args"] = []any{"-profile=${user_config.account}"}
			},
			want: "platform_overrides.win32.args spends ${user_config.account}, which user_config does not declare",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := validateManifest(edited(t, c.edit), bundleFiles)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), c.want)
			}
		})
	}
	if problems := validateManifest(good(t), bundleFiles); len(problems) > 0 {
		t.Errorf("the committed manifest was refused: %v", problems)
	}
}

// committedManifest runs every check the gate has, on the files under
// the root it is given, not the working directory: a check broken in a
// copy of the repository is refused, and refused alone.
func TestManifestProblemsRunsEveryCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		// file is the one broken; edit applies to the manifest, old and
		// new to any other file, and gone removes it.
		file     string
		edit     func(map[string]any)
		old, new string
		gone     bool
		want     string
	}{
		{name: "a version in the tree", file: mcpbManifestPath, edit: func(d map[string]any) { d["version"] = "1.2.3" },
			want: `the committed manifest says version "1.2.3"`},
		{name: "a schema at a branch", file: mcpbManifestPath, edit: func(d map[string]any) {
			d["$schema"] = "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json"
		}, want: "can be re-pointed"},
		{name: "a document the schema refuses", file: mcpbManifestPath, edit: func(d map[string]any) { d["keywords"] = "sheets" },
			want: "the committed manifest does not satisfy mcpb-manifest-v0.3.schema.json"},
		// Every check before identityProblems passed this: no platform
		// was left to spawn the wrong file.
		{name: "Linux dropped with its override", file: mcpbManifestPath, edit: func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"}
			delete(dig(d, "server", "mcp_config", "platform_overrides"), "linux")
		}, want: "compatibility.platforms is [darwin win32]; the packer stages entry points for [darwin linux win32]"},
		{name: "a launcher of other names", file: launcherPath,
			old: "google-sheets-mcp-linux-amd64", new: "google-sheets-mcp-linux-x64",
			want: "the launcher runs [google-sheets-mcp-linux-arm64 google-sheets-mcp-linux-x64]; " +
				"the packer stages [google-sheets-mcp-linux-amd64 google-sheets-mcp-linux-arm64]"},
		// The decode into manifest merges the two, so every check sees
		// the env and the overrides; the schema and the host keep the
		// second, which has neither.
		{name: "a second server object", file: mcpbManifestPath, old: `  "user_config": {`,
			new: `  "server": {"type": "binary", "entry_point": "server/google-sheets-mcp-darwin", ` +
				`"mcp_config": {"command": "${__dirname}/server/google-sheets-mcp-darwin", "args": []}},` +
				"\n" + `  "user_config": {`,
			want: "the manifest has server more than once"},
		// The decode matches "Server" to the field as it does "server",
		// so it merges the two while the host keeps them apart.
		{name: "a server object in another case", file: mcpbManifestPath, old: `  "user_config": {`,
			new:  `  "Server": {"type": "binary"},` + "\n" + `  "user_config": {`,
			want: "the manifest has Server more than once, or in another case"},
		{name: "a variable mapped twice", file: mcpbManifestPath, old: `"GSHEETS_PROFILE": "${user_config.profile}",`,
			new:  `"GSHEETS_PROFILE": "${user_config.profile}", "GSHEETS_PROFILE": "${user_config.profile}",`,
			want: "the manifest has server.mcp_config.env.GSHEETS_PROFILE more than once"},
		// The install hint is held to go.mod, which moves at every major
		// version.
		{name: "a go.mod at another major version", file: "go.mod", old: "/v4", new: "/v3",
			want: `the launcher says "go install github.com/mmedum/google-sheets-mcp/v4/cmd/google-sheets-mcp@latest", ` +
				`and go.mod's module gives "go install github.com/mmedum/google-sheets-mcp/v3/cmd/google-sheets-mcp@latest"`},
		{name: "no README to pack", file: "README.md", gone: true, want: "README.md is packed as README.md and cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := copiedRoot(t)
			switch {
			case tc.gone:
				if err := os.Remove(filepath.Join(root, tc.file)); err != nil {
					t.Fatal(err)
				}
			case tc.edit != nil:
				put(t, root, tc.file, string(editedRaw(t, tc.edit)))
			default:
				body := string(repoFile(t, tc.file))
				if !strings.Contains(body, tc.old) {
					t.Fatalf("%s does not say %q", tc.file, tc.old)
				}
				put(t, root, tc.file, strings.Replace(body, tc.old, tc.new, 1))
			}
			_, _, problems, err := committedManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// A key is repeated only within one object, and each repeated key is
// named once, by its path, a list's elements and a repeated object's
// own keys included. Two keys equal but for case are one key, as Go's
// decode matches them, and a key a dot or a bracket would blur is
// quoted. A document the walk cannot read is an error, not a pass.
func TestRepeatedKeysNameEachPathOnce(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{`{"a": [{"b": 1, "b": 2, "b": 3}], "c": {"d": 1}, "d": 1}`, []string{"a[0].b"}},
		{`{"x": {"y": 1}, "x": {"y": 1, "y": 2}}`, []string{"x", "x.y"}},
		{`{"server": {"type": "binary"}, "user_config": {"type": {"type": "boolean"}}}`, nil},
		{`{"server": {"command": "a", "Command": "b", "COMMAND": "c"}}`, []string{"server.Command"}},
		{`{"k": 1, "\u212a": 2}`, []string{"\u212a"}},
		{`{"env": {"a.b": 1, "a.b": 2, "a": {"b": 1}}}`, []string{`env["a.b"]`}},
		{`{"env": {"a[0]": 1, "A[0]": 2, "": 1, "": 2}}`, []string{`env["A[0]"]`, `env[""]`}},
	} {
		got, err := repeatedKeys([]byte(tc.doc))
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("repeatedKeys(%s) = %q, %v; want %q", tc.doc, got, err, tc.want)
		}
	}
	if got, err := repeatedKeys([]byte(`{"a": 1,`)); err == nil {
		t.Errorf("a document cut short gives %q and no error", got)
	}
}

// Each rule the bundle's settings are held to, broken once in the
// committed manifest, and the shape that ships passing all of them.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	if got := settingProblems(good(t), bundleLeftOut); len(got) > 0 {
		t.Fatalf("the shape that ships was refused:\n%s", strings.Join(got, "\n"))
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"a required switch the bundle turns on, which Claude Desktop fills in", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
			dig(d, "user_config", "enable_destructive")["required"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"two defaults that configure something", func(d map[string]any) {
			dig(d, "user_config", "read_only")["default"] = true
			dig(d, "user_config", "profile")["default"] = "work"
		}, "the bundle's defaults configure the server otherwise than no environment does, in Profile, ReadOnly;"},
		{"a client file the bundle names", func(d map[string]any) {
			dig(d, "user_config", "client_secret")["default"] = "client.json"
		}, "the bundle's defaults configure the server otherwise than no environment does, in ClientSecretPath;"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "Not A Profile"
		}, "the environment the bundle's defaults give the server is refused: config: invalid: profile"},
		{"a field with a list for a default", func(d map[string]any) {
			dig(d, "user_config", "client_secret")["default"] = []any{"client.json"}
		}, "user_config.client_secret defaults to a list, which the defaults check cannot build an environment from"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "read_only")["type"] = "string"
			dig(d, "user_config", "read_only")["default"] = "false"
		}, "user_config.read_only is a string; GSHEETS_READ_ONLY is a switch, so ask for a boolean"},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "enable_data_sources"), "default")
		}, `the environment the bundle's defaults give the server is refused: config: invalid: enable-data-sources ` +
			`"${user_config.enable_data_sources}"`},
		{"an optional client file with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "client_secret"), "default")
		}, "user_config.client_secret is optional with no default"},
		{"an override's env, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["env"] = map[string]any{
				"GSHEETS_ENABLE_DESTRUCTIVE": "true",
			}
		}, "platform_overrides.win32.env is set"},
		{"an empty override env, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["env"] = map[string]any{}
		}, "platform_overrides.linux.env is set"},
		{"no argument list in mcp_config", func(d map[string]any) {
			delete(dig(d, "server", "mcp_config"), "args")
		}, "mcp_config.args is missing; keep it as []"},
		{"a null argument list in mcp_config", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = nil
		}, "mcp_config.args is null; keep it as []"},
		{"an argument that turns the deletes on", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = []any{"-enable-destructive=true"}
		}, `mcp_config.args is ["-enable-destructive=true"]`},
		{"an override's argument, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["args"] = []any{"-enable-destructive=true"}
		}, "platform_overrides.linux.args is set"},
		{"an empty override argument list, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["args"] = []any{}
		}, "platform_overrides.win32.args is set"},
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
		{"a setting the bundle leaves out", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GSHEETS_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GSHEETS_LOG_LEVEL, which the bundle leaves out: diagnostics"},
		{"a removed setting", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GSHEETS_REQUIRE_PROMPT"] = "false"
		}, "mcp_config.env sets GSHEETS_REQUIRE_PROMPT, which a release removed"},
		{"a variable the server does not read", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GSHEETS_DESTRUCTIVE"] = "${user_config.enable_destructive}"
		}, "mcp_config.env sets GSHEETS_DESTRUCTIVE, which the server does not read"},
		{"a profile field that does not say what a name is", func(d map[string]any) {
			dig(d, "user_config", "profile")["description"] = "Which stored login to use. Lower case, digits, - and _."
		}, `user_config.profile does not say a profile name is "up to 64 characters from a to z, 0 to 9, - and _, ` +
			`starting with a letter or digit"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := settingProblems(edited(t, tc.edit), bundleLeftOut); !mentions(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// What the bundle says it is, broken once each: the server's own name, a
// binary, the family's floor, a privacy policy, and exactly the
// platforms the packer stages an entry point for.
func TestTheIdentityRefusesEachBreak(t *testing.T) {
	const entryPoints = "; the packer stages entry points for [darwin linux win32]"
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"another name", func(d map[string]any) { d["name"] = "google-sheets" },
			`the manifest's name is "google-sheets" and the server's is "google-sheets-mcp"`},
		{"a node server", func(d map[string]any) { dig(d, "server")["type"] = "node" },
			`server.type is "node"; the bundle carries a binary`},
		{"no Claude Desktop floor", func(d map[string]any) { delete(dig(d, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
		{"another floor", func(d map[string]any) { dig(d, "compatibility")["claude_desktop"] = ">=0.9.0" },
			`compatibility.claude_desktop is ">=0.9.0"`},
		{"no Linux", func(d map[string]any) { dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"} },
			"compatibility.platforms is [darwin win32]" + entryPoints},
		{"a platform the packer stages nothing for", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "freebsd"}
		}, "compatibility.platforms is [darwin freebsd linux win32]" + entryPoints},
		{"a platform claimed twice", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "linux"}
		}, "compatibility.platforms is [darwin linux linux win32]" + entryPoints},
		{"no privacy policy", func(d map[string]any) { delete(d, "privacy_policies") }, "privacy_policies is empty"},
		{"an empty privacy policy list", func(d map[string]any) { d["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := identityProblems(edited(t, tc.edit), bundleFiles); len(problems) != 1 ||
				!strings.HasPrefix(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one starting %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
	// The platforms come from what is staged, not from a list of its own.
	files := slices.DeleteFunc(slices.Clone(bundleFiles), func(f staged) bool { return f.platform == "linux" })
	want := []string{"compatibility.platforms is [darwin linux win32]; the packer stages entry points for [darwin win32]"}
	if got := identityProblems(good(t), files); !slices.Equal(got, want) {
		t.Errorf("nothing staged for Linux gives %q, want %q", got, want)
	}
}

// Every default the check cannot read is reported, in key order, and
// the comparison still runs over the rest, so one run names everything
// wrong with the defaults.
func TestDefaultsProblemsReportsEveryUnreadableDefaultAndTheRest(t *testing.T) {
	m := edited(t, func(d map[string]any) {
		uc := dig(d, "user_config")
		dig(uc, "profile")["default"] = map[string]any{"name": "work"}
		dig(uc, "client_secret")["default"] = []any{"client.json"}
		dig(uc, "enable_destructive")["default"] = true
	})
	const cannot = ", which the defaults check cannot build an environment from: give it a boolean, a string or a number"
	want := []string{
		"user_config.client_secret defaults to a list" + cannot,
		"user_config.profile defaults to an object" + cannot,
	}
	got, err := defaultsProblems(m)
	if err != nil || len(got) != 3 || !slices.Equal(got[:2], want) ||
		!strings.HasPrefix(got[2], "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;") {
		t.Errorf("defaultsProblems = %v,\n%s\nwant\n%s\nand then the comparison", err, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The bundle asks for every variable the server reads, less the removed
// ones and less each one left out with a reason, so a setting added
// later is a decision about the bundle rather than a gap in it. An
// exclusion the server has no use for is refused.
func TestBundleVarsAreDerivedFromTheSettings(t *testing.T) {
	asked, problems := bundleVarsFrom(
		[]config.Var{{Name: "GSHEETS_ASKED"}, {Name: "GSHEETS_LEFT_OUT"}, {Name: "GSHEETS_REMOVED", Removed: true},
			{Name: "GSHEETS_ADDED_LATER", Switch: true}},
		map[string]string{"GSHEETS_LEFT_OUT": "a reason", "GSHEETS_GONE": "a reason", "GSHEETS_REMOVED": "a reason"},
	)
	if want := []config.Var{{Name: "GSHEETS_ASKED"}, {Name: "GSHEETS_ADDED_LATER", Switch: true}}; !slices.Equal(asked, want) {
		t.Errorf("asked = %v, want %v", asked, want)
	}
	want := []string{
		"bundleLeftOut names GSHEETS_GONE, which the server does not read",
		"bundleLeftOut names GSHEETS_REMOVED, which a release removed, and so left out already",
	}
	if !slices.Equal(problems, want) {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

// A stale exclusion fails the gate itself, not only the derivation, and
// a variable added later is asked for until the bundle asks or a reason
// is written.
func TestSettingProblemsRefuseAStaleExclusion(t *testing.T) {
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["GSHEETS_GONE"] = "a reason"
	want := "bundleLeftOut names GSHEETS_GONE, which the server does not read"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
	leftOut = maps.Clone(bundleLeftOut)
	delete(leftOut, "GSHEETS_WRITE_TIMEOUT")
	want = "user_config does not ask for write_timeout, which sets GSHEETS_WRITE_TIMEOUT"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
}

// The profile field is held to what the server accepts. A bundle that
// drops the field is told so once, and one that leaves the setting out
// is refused, so the field check cannot pass in silence.
func TestTheFieldRulesHaveAFloor(t *testing.T) {
	dropped := edited(t, func(d map[string]any) {
		delete(dig(d, "user_config"), "profile")
		delete(dig(d, "server", "mcp_config", "env"), "GSHEETS_PROFILE")
	})
	if p := slices.DeleteFunc(settingProblems(dropped, bundleLeftOut), func(p string) bool {
		return !strings.Contains(p, "GSHEETS_PROFILE")
	}); !slices.Equal(p, []string{"user_config does not ask for profile, which sets GSHEETS_PROFILE"}) {
		t.Errorf("a dropped profile field gives %q, want the one problem", p)
	}
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["GSHEETS_PROFILE"] = "left out"
	want := fmt.Sprintf("the bundle asks for no GSHEETS_PROFILE, so nothing holds its field to %q",
		"up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit")
	if got := settingProblems(dropped, leftOut); !slices.Contains(got, want) {
		t.Errorf("a bundle with no GSHEETS_PROFILE gives %q, want %q among them", got, want)
	}
}

// A field the defaults change is named, an unexported one included
// where its type compares by value. An unexported one that does not is
// compared by what it holds, a pointer's target included, and reported
// unnamed when it differs, beside any named field, rather than
// panicking.
func TestTheConfigDiffNamesTheFieldsThatDiffer(t *testing.T) {
	type cfg struct {
		Shown  int
		hidden int
		rest   []string
		held   any
		at     *int
		byKey  map[string]int
	}
	one, alsoOne, two := 1, 1, 2
	base := cfg{Shown: 1, hidden: 1, at: &one, byKey: map[string]int{"k": 1}}
	for _, tc := range []struct {
		name string
		b    cfg
		want []string
	}{
		{"an exported field", cfg{Shown: 2, hidden: 1, at: &one, byKey: map[string]int{"k": 1}}, []string{"Shown"}},
		{"an unexported field that compares", cfg{Shown: 1, hidden: 2, at: &one, byKey: map[string]int{"k": 1}},
			[]string{"hidden"}},
		{"a slice", cfg{Shown: 1, hidden: 1, rest: []string{"x"}, at: &one, byKey: map[string]int{"k": 1}},
			[]string{"an unexported field"}},
		{"a slice beside a named field", cfg{Shown: 2, hidden: 1, rest: []string{"x"}, at: &one,
			byKey: map[string]int{"k": 1}}, []string{"Shown", "an unexported field"}},
		{"another pointer to the same value", cfg{Shown: 1, hidden: 1, at: &alsoOne, byKey: map[string]int{"k": 1}}, nil},
		{"a pointer to another value", cfg{Shown: 1, hidden: 1, at: &two, byKey: map[string]int{"k": 1}},
			[]string{"an unexported field"}},
		{"a map holding another value", cfg{Shown: 1, hidden: 1, at: &one, byKey: map[string]int{"k": 2}},
			[]string{"an unexported field"}},
		{"the same", cfg{Shown: 1, hidden: 1, at: &one, byKey: map[string]int{"k": 1}}, nil},
	} {
		if got := fieldDiff(base, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("%s: fieldDiff = %q, want %q", tc.name, got, tc.want)
		}
	}
	// An interface holding a slice on both sides compares as a type and
	// panics in Value.Equal; it is reported unnamed instead.
	if got := fieldDiff(cfg{held: []string{"y"}}, cfg{held: []string{"x"}}); !slices.Equal(got,
		[]string{"an unexported field"}) {
		t.Errorf("an interface field differing gives %q, want [an unexported field]", got)
	}
	if got := configDiff(config.Config{Profile: "a", ReadOnly: true}, config.Config{Profile: "a"}); !slices.Equal(got,
		[]string{"ReadOnly"}) {
		t.Errorf("configDiff = %q, want [ReadOnly]", got)
	}
}

// bundleSwitches is the switches the committed bundle asks for.
var bundleSwitches = []string{"allow_external_formulas", "enable_data_sources", "enable_destructive", "read_only"}

// refusingReadOnly is the committed manifest with GSHEETS_REQUIRE_PROMPT
// spending read_only, so the server refuses read_only on, and nothing
// else. Build refuses no set of the switches, and no setting it reads
// can be made to refuse a pair, so this is how the refusal check is made
// to fire.
func refusingReadOnly(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	return edited(t, func(d map[string]any) {
		dig(d, "server", "mcp_config", "env")["GSHEETS_REQUIRE_PROMPT"] = "${user_config.read_only}"
		edit(d)
	})
}

// refusals is refusalProblems over the bundle's switches, the defaults
// checked first as settingProblems checks them.
func refusals(t *testing.T, m manifest, declared [][]string) []string {
	t.Helper()
	_, defaultsErr := defaultsProblems(m)
	return refusalProblems(m, bundleSwitches, declared, defaultsErr)
}

// undeclaredReadOnly is the problem refusingReadOnly gives with nothing
// declared.
const undeclaredReadOnly = "the server refuses [read_only on], and bundleRefused does not declare it"

// Every set of the switches is started, and the refused ones are held to
// the declared list both ways, each defect reported once, for the
// smallest set that shows it.
func TestTheBundleRefusalsAreDeclared(t *testing.T) {
	committed := good(t)
	if got := refusals(t, committed, bundleRefused); len(got) != 0 {
		t.Errorf("the committed bundle and bundleRefused give %q, want nothing", got)
	}
	if got := refusals(t, refusingReadOnly(t, func(map[string]any) {}), nil); len(got) != 1 ||
		!strings.HasPrefix(got[0], undeclaredReadOnly) {
		t.Errorf("an undeclared refusal gives %q, want the one problem %q", got, undeclaredReadOnly)
	}
	declared := [][]string{{"read_only"}}
	if got := refusals(t, refusingReadOnly(t, func(map[string]any) {}), declared); len(got) != 0 {
		t.Errorf("a declared refusal gives %q, want nothing", got)
	}
	const starts = "bundleRefused declares that the server refuses [read_only on], and it starts with them"
	if got := refusals(t, committed, declared); !slices.Equal(got, []string{starts}) {
		t.Errorf("a declared refusal the server starts with gives %q, want %q once", got, starts)
	}
	// When the defaults do not start, no set is started: the defaults
	// check reports it.
	if got := refusalProblems(committed, bundleSwitches, declared,
		errors.New("the defaults do not start")); len(got) != 0 {
		t.Errorf("a set was started beside defaults that do not start: %q", got)
	}
	if got := refusals(t, committed, [][]string{{"profile", "read_only"}}); !slices.Contains(got,
		"bundleRefused names profile, which is not a switch the bundle asks for") {
		t.Errorf("a declared key that is not a switch gives %q", got)
	}
}

// Each field in a declared set names the others' titles, trimmed, and a
// key with no title is reported once, however many sets name it.
func TestTheDeclaredSetsNameEachOther(t *testing.T) {
	pair := [][]string{{"enable_destructive", "read_only"}}
	named := edited(t, func(d map[string]any) {
		dig(d, "user_config", "read_only")["description"] = "Refused with Deleting on."
		dig(d, "user_config", "enable_destructive")["description"] = "Refused with Read-only on."
		dig(d, "user_config", "enable_destructive")["title"] = " Deleting "
	})
	if got := declaredProblems(named, bundleSwitches, pair); len(got) != 0 {
		t.Errorf("fields that name each other give %q, want nothing", got)
	}
	unnamed := edited(t, func(d map[string]any) {
		dig(d, "user_config", "read_only")["description"] = "Register only the reads."
		dig(d, "user_config", "enable_destructive")["description"] = "Register the deletes."
	})
	want := []string{
		`user_config.enable_destructive does not name "Read-only", which the server refuses it with`,
		`user_config.read_only does not name "Deleting", which the server refuses it with`,
	}
	if got := declaredProblems(unnamed, bundleSwitches, pair); !slices.Equal(got, want) {
		t.Errorf("fields that do not name each other give %q, want %q", got, want)
	}
	untitled := edited(t, func(d map[string]any) { dig(d, "user_config", "read_only")["title"] = " " })
	twice := [][]string{{"enable_destructive", "read_only"}, {"read_only"}}
	for _, defaultsErr := range []error{nil, errors.New("the defaults do not start")} {
		got := refusalProblems(untitled, bundleSwitches, twice, defaultsErr)
		if n := len(slices.DeleteFunc(got, func(p string) bool {
			return !strings.HasPrefix(p, "user_config.read_only has no title")
		})); n != 1 {
			t.Errorf("defaults error %v: an untitled read_only is reported %d times, want 1", defaultsErr, n)
		}
	}
}

// A switch in a set is on unless its default is true: a required one
// with no default, or one with a list, is turned on rather than left
// out, which would hide a refusal.
func TestTheRefusalsTurnASwitchOnUnlessItDefaultsOn(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"required with no default": func(d map[string]any) {
			dig(d, "user_config", "read_only")["required"] = true
			delete(dig(d, "user_config", "read_only"), "default")
		},
		"a list default": func(d map[string]any) { dig(d, "user_config", "read_only")["default"] = []any{"true"} },
	} {
		got := refusals(t, refusingReadOnly(t, edit), nil)
		if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, undeclaredReadOnly) }) {
			t.Errorf("%s: got %q, want one starting %q", name, got, undeclaredReadOnly)
		}
	}
}

// A switch that defaults to true is started off, so a refusal that
// needs it off is found. This server has no switch that defaults on, so
// read_only is made one here, and the profile is made to depend on it:
// "true" is four letters and "false" five, so after 60 letters the name
// is 64 characters with read_only on, which the profile rule allows, and
// 65 with it off, which it refuses.
func TestTheRefusalsTurnADefaultOnSwitchOff(t *testing.T) {
	m := edited(t, func(d map[string]any) {
		dig(d, "user_config", "read_only")["default"] = true
		dig(d, "server", "mcp_config", "env")["GSHEETS_PROFILE"] = strings.Repeat("a", 60) + "${user_config.read_only}"
	})
	const want = "the server refuses [read_only off], and bundleRefused does not declare it"
	if got := refusals(t, m, nil); len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Errorf("a refusal of read_only off gives %q, want the one problem %q", got, want)
	}
	if got := refusals(t, m, [][]string{{"read_only"}}); len(got) != 0 {
		t.Errorf("a declared refusal of read_only off gives %q, want nothing", got)
	}
}

// settingProblems runs the refusal check over the switches the bundle
// asks for, so a refusal Build adds fails the gate itself.
func TestSettingProblemsHoldTheRefusals(t *testing.T) {
	got := settingProblems(refusingReadOnly(t, func(map[string]any) {}), bundleLeftOut)
	if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, undeclaredReadOnly) }) {
		t.Errorf("settingProblems = %q, want one starting %q", got, undeclaredReadOnly)
	}
	// Defaults the server refuses start no set, which would each fail
	// with them: the defaults problem is the one report.
	got = settingProblems(edited(t, func(d map[string]any) {
		dig(d, "user_config", "profile")["default"] = "Not A Profile"
	}), bundleLeftOut)
	if len(got) != 1 || !strings.HasPrefix(got[0], "the environment the bundle's defaults give the server is refused") {
		t.Errorf("defaults the server refuses give %q, want that one problem", got)
	}
}

// The launcher's install hint names the module go.mod declares, which
// modulePath reads. The path moves with each major version, so a hint
// left behind sends somebody to an old one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v4/cmd/google-sheets-mcp@latest" >&2`
	const module = "example.com/m/v4"
	if problems := installHintProblems(hint, module); len(problems) > 0 {
		t.Errorf("the hint for %s: %v", module, problems)
	}
	for _, tc := range []struct{ name, launcher, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v4", "", 1),
			`the launcher says "go install example.com/m/cmd/google-sheets-mcp@latest"`},
		{"a hint a major version behind", strings.Replace(hint, "/v4", "/v3", 1),
			`the launcher says "go install example.com/m/v3/cmd/google-sheets-mcp@latest"`},
		{"no hint at all", "exit 1", "gives no install hint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(installHintProblems(tc.launcher, module), "\n")
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
	dir := t.TempDir()
	launcher := filepath.Join(dir, "launch-linux.sh")
	if err := os.WriteFile(launcher, repoFile(t, launcherPath), 0o700); err != nil {
		t.Fatal(err)
	}
	// The names come from the packer, so a rename that broke the pairing
	// fails here rather than on somebody's machine.
	var launched []string
	for _, f := range bundleFiles {
		if !f.launched {
			continue
		}
		name := path.Base(f.path)
		launched = append(launched, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho ran "+name+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(launched) != 2 {
		t.Fatalf("the packer stages %v for the launcher, want the two Linux binaries", launched)
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
			if !mentions(problems, tc.want) {
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
	if declared := good(t).ManifestVersion; declared != "0.3" {
		t.Fatalf("the committed manifest declares %q; this repository has checked 0.3", declared)
	}
}

// fakeDist builds a dist tree shaped like the one goreleaser leaves, so
// the packer's globs resolve without a real build. The files are 0600,
// so a pack that copied the source's mode would pack nothing runnable.
func fakeDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	for dir, name := range map[string]string{
		"universal_darwin_all":    bundleName,
		"server_linux_amd64_v1":   bundleName,
		"server_linux_arm64_v8.0": bundleName,
		"server_windows_amd64_v1": bundleName + ".exe",
	} {
		put(t, dist, dir+"/"+name, "binary for "+dir)
	}
	return dist
}

// openBundle is the zip a pack wrote for version under dist.
func openBundle(t *testing.T, dist, version string) *zip.ReadCloser {
	t.Helper()
	z, err := zip.OpenReader(filepath.Join(dist, bundleName+"_"+version+".mcpb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = z.Close() })
	return z
}

// The bundle is a zip with a fixed shape: a manifest at the root, the
// binaries under server/, and the license and README beside them. The
// modes are literals rather than staged.mode(), so the assertion does
// not check the code against itself.
func TestBundleLayoutAndModes(t *testing.T) {
	atRepoRoot(t)
	dist := fakeDist(t)
	if err := mcpbPack(&bytes.Buffer{}, "v1.2.3", dist); err != nil {
		t.Fatalf("pack: %v", err)
	}
	want := map[string]os.FileMode{
		"manifest.json":                        0o644,
		"LICENSE":                              0o644,
		"README.md":                            0o644,
		"server/launch-linux.sh":               0o755,
		"server/google-sheets-mcp-darwin":      0o755,
		"server/google-sheets-mcp.exe":         0o755,
		"server/google-sheets-mcp-linux-amd64": 0o755,
		"server/google-sheets-mcp-linux-arm64": 0o755,
	}
	z := openBundle(t, dist, "1.2.3")
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
		if f.Method != zip.Deflate {
			t.Errorf("%s is not deflated", f.Name)
		}
		mode, ok := want[f.Name]
		if !ok {
			t.Errorf("the bundle carries %s, which is not the family's layout", f.Name)
			continue
		}
		if got := f.Mode().Perm(); got != mode {
			t.Errorf("%s is packed %v, want %v", f.Name, got, mode)
		}
	}
	if len(names) != len(want) {
		t.Errorf("the bundle carries %v; want the %d files of the family's layout", names, len(want))
	}

	// The version reaches the manifest inside the bundle, through a
	// decode and an encode rather than a substitution over text, and
	// everything else survives the rewrite.
	rc, err := z.Open("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	var packed manifest
	if err := json.NewDecoder(rc).Decode(&packed); err != nil {
		t.Fatal(err)
	}
	if packed.Version != "1.2.3" || packed.Name != "google-sheets-mcp" || len(packed.UserConfig) != 6 {
		t.Errorf("the packed manifest is version %q of %q with %d user_config keys", packed.Version, packed.Name,
			len(packed.UserConfig))
	}
	if problems := manifestShapeProblems(packed.Schema, packed.ManifestVersion, packed.Support); len(problems) > 0 {
		t.Errorf("the packed manifest's own declaration is wrong:\n%s", strings.Join(problems, "\n"))
	}
}

// The same inputs give the same bytes, which is the least a build can
// offer somebody checking a checksum. The comparison alone does not
// discriminate, since zip stores times at two-second granularity, so
// every entry's time is held to a literal too.
func TestBundleIsReproducible(t *testing.T) {
	atRepoRoot(t)
	dist := fakeDist(t)
	var packs [2][]byte
	for i := range packs {
		if err := mcpbPack(&bytes.Buffer{}, "1.2.3", dist); err != nil {
			t.Fatal(err)
		}
		var err error
		if packs[i], err = os.ReadFile(filepath.Join(dist, bundleName+"_1.2.3.mcpb")); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(packs[0], packs[1]) {
		t.Error("two packs of the same inputs gave different bytes")
	}
	stamp := time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range openBundle(t, dist, "1.2.3").File {
		if !f.Modified.Equal(stamp) {
			t.Errorf("%s carries modification time %v, want %v", f.Name, f.Modified, stamp)
		}
	}
}

// A glob that matched two would pack whichever sorted first, and a
// bundle built from the wrong binary is not something a checksum
// catches: the checksum is of whatever was built. A dist with a binary
// missing is refused rather than packed short.
func TestOnlyMatchRefusesTwo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a_linux_amd64_v1", "a_linux_amd64_v2"} {
		put(t, dir, name+"/"+bundleName, "x")
	}
	if _, err := onlyMatch(filepath.Join(dir, "*linux_amd64*", bundleName), "linux amd64 binary"); err == nil {
		t.Error("a glob matching two files was accepted")
	}
	if _, err := onlyMatch(filepath.Join(dir, "*nothing*", bundleName), "nothing"); err == nil {
		t.Error("a glob matching nothing was accepted")
	}
	atRepoRoot(t)
	dist := fakeDist(t)
	if err := os.RemoveAll(filepath.Join(dist, "server_windows_amd64_v1")); err != nil {
		t.Fatal(err)
	}
	if err := mcpbPack(&bytes.Buffer{}, "1.0.0", dist); err == nil || !strings.Contains(err.Error(), "server/google-sheets-mcp.exe") {
		t.Errorf("a dist with no Windows binary packed: %v", err)
	}
}

// failingClose is a bundle file whose last write reaches the disk only
// at Close, and fails there.
type failingClose struct{ bytes.Buffer }

func (*failingClose) Close() error { return errors.New("no space left on device") }

// A bundle cut short at its close is not packed: the error is the
// pack's.
func TestAFailedCloseFailsThePack(t *testing.T) {
	sources, err := bundleSources(repoDir, fakeDist(t))
	if err != nil {
		t.Fatal(err)
	}
	out := &failingClose{}
	if err := writeBundle(out, []byte("{}\n"), sources); err == nil || err.Error() != "no space left on device" {
		t.Errorf("writeBundle = %v, want the close's error", err)
	}
	if out.Len() == 0 {
		t.Error("writeBundle wrote nothing before the close")
	}
}

// A pack that fails while writing leaves no file under the name the
// release publishes, and no temporary one beside it, so a bundle cut
// short cannot be uploaded as a good one. A bundle a pack wrote before
// is kept.
func TestAFailedWriteLeavesNoBundle(t *testing.T) {
	atRepoRoot(t)
	dist := fakeDist(t)
	bundle := filepath.Join(dist, bundleName+"_1.0.0.mcpb")
	// The macOS binary is a directory, so the archive fails part way,
	// after the license and the README are written.
	broken := filepath.Join(dist, "universal_darwin_all", bundleName)
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(broken, 0o750); err != nil {
		t.Fatal(err)
	}
	listing := func() []string {
		entries, err := os.ReadDir(dist)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	before := listing()
	if err := mcpbPack(&bytes.Buffer{}, "1.0.0", dist); err == nil {
		t.Fatal("a pack from a directory where a binary belongs succeeded")
	}
	if got := listing(); !slices.Equal(got, before) {
		t.Errorf("a failed pack left %v in dist, which held %v", got, before)
	}
	put(t, dist, bundleName+"_1.0.0.mcpb", "an earlier bundle")
	if err := mcpbPack(&bytes.Buffer{}, "1.0.0", dist); err == nil {
		t.Fatal("a pack from a directory where a binary belongs succeeded")
	}
	if got, err := os.ReadFile(bundle); err != nil || string(got) != "an earlier bundle" {
		t.Errorf("a failed pack replaced the bundle there: %q, %v", got, err)
	}
}

// The packer refuses what the gate refuses, in the gate's words,
// because both start from committedManifest: a check the gate alone
// used to run stops the pack, and so does a file beside the manifest.
func TestPackRunsEveryCheckTheGateRuns(t *testing.T) {
	root := copiedRoot(t)
	t.Chdir(root)
	dist := fakeDist(t)
	if err := mcpbPack(&bytes.Buffer{}, "1.0.0", dist); err != nil {
		t.Fatalf("the copied repository does not pack: %v", err)
	}
	for _, tc := range []struct{ name, file, old, new, want string }{
		{"no word of the login", mcpbManifestPath, "does NOT log you in", "is ready",
			"long_description does not say the bundle cannot log the user in"},
		{"another license", "LICENSE", "Apache License", "MIT License",
			`the manifest says the license is "Apache-2.0" and LICENSE is MIT`},
		{"a relative command", mcpbManifestPath, `"command": "${__dirname}/server/google-sheets-mcp-darwin"`,
			`"command": "server/google-sheets-mcp-darwin"`, "a command starts with ${__dirname}/"},
		{"an argument", mcpbManifestPath, `"args": []`, `"args": ["-read-only=false"]`,
			`mcp_config.args is ["-read-only=false"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := string(repoFile(t, tc.file))
			if !strings.Contains(body, tc.old) {
				t.Fatalf("%s does not say %q", tc.file, tc.old)
			}
			put(t, root, tc.file, strings.ReplaceAll(body, tc.old, tc.new))
			t.Cleanup(func() { put(t, root, tc.file, body) })
			packErr := mcpbPack(&bytes.Buffer{}, "1.0.0", dist)
			if packErr == nil || !strings.Contains(packErr.Error(), tc.want) {
				t.Errorf("pack = %v; want it refused, naming %q", packErr, tc.want)
			}
			if gateErr := mcpbGate(&bytes.Buffer{}); packErr != nil && (gateErr == nil || gateErr.Error() != packErr.Error()) {
				t.Errorf("the pack's refusal is not the gate's:\n pack %v\n gate %v", packErr, gateErr)
			}
		})
	}
}

// What ships is held to the vendored schema, not only what is
// committed, since the stamp encodes the document again.
func TestTheStampedManifestIsHeldToTheSchema(t *testing.T) {
	stamped, err := stampManifest(repoFile(t, mcpbManifestPath), "1.2.3")
	if err != nil {
		t.Fatalf("the committed manifest does not stamp: %v", err)
	}
	if !bytes.Contains(stamped, []byte(`"version": "1.2.3"`)) {
		t.Errorf("the stamped manifest does not carry the version:\n%s", stamped)
	}
	_, err = stampManifest(editedRaw(t, func(d map[string]any) { d["keywords"] = "sheets" }), "1.2.3")
	if want := "the stamped manifest does not satisfy mcpb-manifest-v0.3.schema.json"; err == nil ||
		!strings.Contains(err.Error(), want) {
		t.Errorf("stamping a document the schema refuses gave %v, want an error containing %q", err, want)
	}
}
