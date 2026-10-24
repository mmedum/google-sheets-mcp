package main

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
	"github.com/mmedum/google-sheets-mcp/v4/internal/server"
)

// The Claude Desktop bundle. A `.mcpb` is a zip carrying a manifest, the
// binaries for every platform it claims, and the license and README —
// opened in Claude Desktop it installs the server and asks for the
// settings a client's configuration carries, so nobody has to edit a
// config file by hand.
//
// It is packed here rather than by the official Node CLI, because this
// repository is Go and only Go: an interpreter `make check` needs is a
// prerequisite nobody declared. A bundle is a deflate zip and the
// standard library writes those, so the only thing the CLI was really
// buying was validation of the manifest against its published schema.
//
// validateManifest below replaces that, and is not merely a substitute.
// A schema can say the manifest is well formed; it cannot say that
// entry_point names a file that is actually in the bundle, that a
// platform runs the binary staged for it, or that a ${user_config.x}
// refers to a key somebody is asked for. Each of those is well formed by
// any schema and produces a bundle that installs cleanly and then does
// nothing.
//
// The gate and the packer both start from committedManifest, so a
// release cannot pack a manifest the gate would refuse.

// mcpbPlaceholder is the version the COMMITTED manifest carries. The
// real one is written in as the bundle is packed, so a manifest in the
// tree can never be stale: it does not claim a version at all.
const mcpbPlaceholder = "0.0.0-dev"

// mcpbManifestPath is the committed manifest.
const mcpbManifestPath = "packaging/mcpb/manifest.json"

// launcherPath is the Linux launcher, which picks a binary by
// architecture and execs it.
const launcherPath = "packaging/mcpb/launch-linux.sh"

// bundleName is what the binary is called, and what the manifest's paths
// are built from: the server's own name.
const bundleName = server.Name

// staged is one file the packer puts in the bundle.
//
// bundleFiles is the single source of the staged NAMES. The gate reads
// it without building anything, and the packer reads the same rows to
// find the files, so a manifest naming a file nobody stages is a fact
// about two files in this repository, found on the commit that wrote it.
type staged struct {
	// path is where the file lands inside the bundle.
	path string
	// glob finds the file under the release's dist directory. Empty for
	// a file this repository ships itself.
	glob string
	// source is a file in this repository: the launcher, the license
	// and the README.
	source string
	// platform is the one this file is the entry point for, empty for a
	// file a launcher chooses rather than the manifest.
	platform string
	// launched marks a binary the Linux launcher picks between. Those
	// names appear in a shell script no manifest mentions, so nothing
	// else can check them.
	launched bool
}

// program reports whether the bundle runs the file: an entry point or a
// binary the launcher picks. The license and the README are packed to
// be read, and nothing may spawn them.
func (f staged) program() bool { return f.platform != "" || f.launched }

// mode is the mode a staged file is packed with, decided here rather
// than copied from the source. What runs is 0755, the Windows .exe
// included, because the packer's own rule forces the execute bit on the
// entry point alone, and a binary staged from a filesystem that lost the
// mode arrives unrunnable. The license and the README are 0644.
func (f staged) mode() os.FileMode {
	if f.program() {
		return 0o755
	}
	return 0o644
}

// bundleFiles is what a bundle contains, and it is the same list whether
// or not the binaries exist yet.
//
// A manifest picks a binary by platform and has no key for the
// architecture, so every platform it claims has to work on both. macOS
// does through a universal binary; Windows through amd64, which its
// arm64 build runs under emulation. Linux has neither, so the bundle
// carries both Linux binaries and a launcher that picks between them.
var bundleFiles = []staged{
	{path: "server/" + bundleName + "-darwin", glob: "*darwin_all*/" + bundleName, platform: "darwin"},
	{path: "server/" + bundleName + ".exe", glob: "*windows_amd64*/" + bundleName + ".exe", platform: "win32"},
	{path: "server/launch-linux.sh", source: launcherPath, platform: "linux"},
	{path: "server/" + bundleName + "-linux-amd64", glob: "*linux_amd64*/" + bundleName, launched: true},
	{path: "server/" + bundleName + "-linux-arm64", glob: "*linux_arm64*/" + bundleName, launched: true},
	{path: "LICENSE", source: "LICENSE"},
	{path: "README.md", source: "README.md"},
}

// manifest is the part of the document these checks are about.
type manifest struct {
	Schema          string   `json:"$schema"`
	ManifestVersion string   `json:"manifest_version"`
	Support         string   `json:"support"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	License         string   `json:"license"`
	Description     string   `json:"description"`
	LongDescription string   `json:"long_description"`
	PrivacyPolicies []string `json:"privacy_policies"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command string `json:"command"`
			// Args is raw, so that a key set to null is told apart from
			// one that is missing.
			Args              json.RawMessage   `json:"args"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig    map[string]userConfigOption `json:"user_config"`
	Compatibility struct {
		ClaudeDesktop string   `json:"claude_desktop"`
		Platforms     []string `json:"platforms"`
	} `json:"compatibility"`
}

// mcpbGate checks the committed manifest without building anything.
//
// It runs in `make check` and in CI, which is the point: the referential
// failures below are checkable against the *names* the packer stages,
// and those are known statically. Only the packing itself has to wait
// for binaries.
func mcpbGate(out io.Writer) error {
	_, m, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), mcpbManifestPath)
	}
	_, _ = fmt.Fprintf(out, "bundle manifest ok: %d file(s), %d platform(s), %d user_config key(s)\n",
		len(bundleFiles)+1, len(m.Compatibility.Platforms), len(m.UserConfig))
	return nil
}

// mcpbPack builds the bundle from the binaries goreleaser has just made.
//
// It runs as the universal binary's post hook, which is the one point in
// the pipeline where every binary exists and the checksum file has not
// been written yet. That is what puts the bundle into checksums.txt with
// the archives, under the same signature. Anywhere later and it ships
// unsigned while looking no different.
func mcpbPack(out io.Writer, version, dist string) error {
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-pack VERSION [DIST]")
	}
	if dist == "" {
		dist = "dist"
	}
	version = strings.TrimPrefix(version, "v")

	// The gate's own start, read once. The placeholder version is among
	// its checks: a committed manifest claiming a version means somebody
	// stamped it in the tree, and the next build would ship whatever they
	// left behind.
	raw, _, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in the bundle manifest", len(problems))
	}
	stamped, err := stampManifest(raw, version)
	if err != nil {
		return err
	}

	sources := map[string]string{}
	for _, f := range bundleFiles {
		src := f.source
		if f.glob != "" {
			if src, err = onlyMatch(filepath.Join(dist, f.glob), f.path); err != nil {
				return err
			}
		}
		sources[f.path] = src
	}

	bundle := filepath.Join(dist, fmt.Sprintf("%s_%s.mcpb", bundleName, version))
	f, err := os.Create(bundle) //nolint:gosec // a path built from dist and the version
	if err != nil {
		return err
	}
	if err := writeBundle(f, stamped, sources); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "mcpb-pack: wrote %s (%d files)\n", bundle, len(sources)+1)
	return nil
}

// readManifest decodes the committed manifest into a map, for the
// registry entry, which reads one field of it.
func readManifest() (map[string]any, error) {
	raw, err := os.ReadFile(mcpbManifestPath)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode %s: %w", mcpbManifestPath, err)
	}
	return document, nil
}

// committedManifest is where the gate and the packer both start: the
// committed manifest under root, read once and decoded once, and every
// problem manifestProblems finds in it. The packer ships this document
// with the real version stamped in and encoded again, not these bytes,
// and holds what it stamps to the vendored schema too.
//
// A key repeated in one object is refused before anything else is
// checked. The decode into manifest merges a repeated object into the
// first, while the schema validator, the packer's encode and the host
// each keep the last, so every other check would read a document that
// is not the one shipped.
func committedManifest(root string) (raw []byte, m manifest, problems []string, err error) {
	raw, err = os.ReadFile(filepath.Join(root, mcpbManifestPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, manifest{}, nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, manifest{}, []string{fmt.Sprintf("the manifest does not decode: %v", err)}, nil
	}
	if repeated := repeatedKeys(raw); len(repeated) > 0 {
		for _, at := range repeated {
			problems = append(problems, fmt.Sprintf("the manifest has %s more than once; the gate's decode merges "+
				"a repeated key, and the schema, the packer and the host keep the last, so they would read "+
				"different documents", at))
		}
		return raw, manifest{}, problems, nil
	}
	return raw, m, manifestProblems(raw, m, bundleFiles, root), nil
}

// repeatedKeys is the path of every key an object in raw has more than
// once, each once, in document order: "server" at the top level,
// "server.mcp_config.env.GSHEETS_PROFILE" inside, and [i] for an element
// of a list. A document that is not JSON has none here, since the
// decode reports it.
func repeatedKeys(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var repeated []string
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]int{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				at := key
				if path != "" {
					at = path + "." + key
				}
				if seen[key]++; seen[key] == 2 {
					repeated = append(repeated, at)
				}
				if err := walk(at); err != nil {
					return err
				}
			}
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		default:
			return nil
		}
		_, err = dec.Token() // the closing brace or bracket
		return err
	}
	if walk("") != nil {
		return nil
	}
	return repeated
}

// manifestProblems is every check the gate and the packer both run, so
// the release packs no manifest the gate would refuse: the placeholder
// version, the no-login sentence, the manifest's own declaration and
// the vendored schema it cites, the referential checks against the
// staged names, what the bundle says it is, the settings it asks for,
// and the launcher, go.mod, LICENSE and README beside it. raw is the
// manifest and m the same decoded, files the staged layout, and root
// the repository the files beside the manifest are read from. Sorted,
// so one run reads the same as the next.
func manifestProblems(raw []byte, m manifest, files []staged, root string) []string {
	problems := validateManifest(m, files)

	// The version placeholder, so a committed manifest cannot claim a
	// version that shipped.
	if m.Version != mcpbPlaceholder {
		problems = append(problems, fmt.Sprintf(
			"the committed manifest says version %q; it must say %q, and the packer writes the real one",
			m.Version, mcpbPlaceholder))
	}
	// The first-run failure, said in the manifest because it is the one
	// a user meets: the bundle cannot log anybody in.
	if !strings.Contains(strings.ToLower(m.LongDescription), "does not log you in") {
		problems = append(problems, "long_description does not say the bundle cannot log the user in, "+
			"which is the first thing that fails for somebody who installs it")
	}
	problems = append(problems, manifestShapeProblems(m.Schema, m.ManifestVersion, m.Support)...)
	problems = append(problems, identityProblems(m, files)...)
	problems = append(problems, settingProblems(m, bundleLeftOut)...)
	problems = append(problems, checkLauncher(files, root)...)

	// And against the schema the manifest cites, which the checks above
	// only name. They hold the DECLARATION — present, pinned to an
	// immutable ref, agreeing with manifest_version, not below the floor
	// — and a document can satisfy all of that while not satisfying the
	// schema itself.
	if err := validateDocument(mcpbSchemaFile, "the committed manifest", raw); err != nil {
		problems = append(problems, err.Error())
	}

	for _, f := range files {
		if f.source == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f.source)); err != nil {
			problems = append(problems, fmt.Sprintf("%s is packed as %s and cannot be read: %v", f.source, f.path, err))
		}
	}
	switch want, err := licenseOf(root); {
	case err != nil:
		problems = append(problems, err.Error())
	case want != "" && m.License != want:
		problems = append(problems, fmt.Sprintf(
			"the manifest says the license is %q and LICENSE is %s", m.License, want))
	}
	sort.Strings(problems)
	return problems
}

// userConfigRef matches every ${user_config.x} in a value, not only a
// value that is nothing but one: a composed "${user_config.dir}/sub"
// would otherwise skip the check and start the server with the variable
// unsubstituted.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([A-Za-z0-9_]+)\}`)

// dirnameRef starts every command: ${__dirname} is the one path the
// specification fixes to the bundle. It shows a relative command once
// and never says what it is resolved against, and the reference host
// resolves no path (docs/architecture.md §18).
const dirnameRef = "${__dirname}/"

// validateManifest is the referential checks the manifest alone
// answers, as one function so a test can run them against a broken
// manifest without touching the repository. The launcher's names are
// checkLauncher's, which reads the script.
//
// It takes the staged list as an argument for the same reason: each
// check is watched failing before it is believed, and a check that could
// only be exercised by breaking the real manifest would not be.
func validateManifest(m manifest, files []staged) []string {
	// programs is what can be run. LICENSE and README.md are packed to be
	// read, and nothing may spawn them.
	programs := map[string]staged{}
	for _, f := range files {
		if f.program() {
			programs[f.path] = f
		}
	}
	problems, commands, runs := commandProblems(m, programs)
	problems = append(problems, spendProblems(m)...)
	return append(problems, platformProblems(m, files, commands, runs)...)
}

// commandProblems is checks 1 and 2: entry_point and every command name
// a program the packer stages, every command starts with ${__dirname}/,
// and entry_point is the file the default command runs. commands is each
// command by platform, "" for the default, and runs the program each one
// spawns; a command that names none is reported here and left out of
// runs, so check 5 does not report it again.
func commandProblems(m manifest, programs map[string]staged) (problems []string, commands map[string]string,
	runs map[string]staged) {
	// 1. entry_point names a program that is actually packed.
	_, entryStaged := programs[m.Server.EntryPoint]
	switch {
	case m.Server.EntryPoint == "":
		problems = append(problems, "the manifest declares no entry_point")
	case !entryStaged:
		problems = append(problems, fmt.Sprintf("entry_point %q is not a program the packer stages",
			m.Server.EntryPoint))
	}

	// 2. every command starts with ${__dirname}/ and names a program the
	// packer stages.
	commands = map[string]string{"": m.Server.MCPConfig.Command}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		if over.Command != "" {
			commands[platform] = over.Command
		}
	}
	runs = map[string]staged{}
	for _, platform := range slices.Sorted(maps.Keys(commands)) {
		cmd := commands[platform]
		where := "mcp_config.command"
		if platform != "" {
			where = "platform_overrides." + platform + ".command"
		}
		rel, prefixed := strings.CutPrefix(cmd, dirnameRef)
		file, isProgram := programs[rel]
		switch {
		case !prefixed:
			problems = append(problems, fmt.Sprintf("%s is %q; a command starts with %s, the one path the "+
				"specification fixes to the bundle", where, cmd, dirnameRef))
		case !isProgram:
			problems = append(problems, fmt.Sprintf("%s is %q, which is not a program the packer stages", where, cmd))
		default:
			runs[platform] = file
		}
	}
	// And check 1's other half: entry_point is the file the default
	// command runs, so the bundle does not name one file as the server
	// and spawn another. An entry point or a command that names no
	// program is reported above.
	if file, ok := runs[""]; ok && entryStaged && file.path != m.Server.EntryPoint {
		problems = append(problems, fmt.Sprintf("entry_point is %q, and mcp_config.command runs %q",
			m.Server.EntryPoint, file.path))
	}
	return problems, commands, runs
}

// spendProblems is check 3: every ${user_config.x} an env value or an
// argument spends is a declared key. Arguments included, though
// settingProblems refuses one, so that this check holds on its own.
func spendProblems(m manifest) []string {
	var problems []string
	spent := map[string][]string{"mcp_config.args": m.args()}
	for key, value := range m.Server.MCPConfig.Env {
		spent["mcp_config.env."+key] = []string{value}
	}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		spent["platform_overrides."+platform+".args"] = over.Args
		for key, value := range over.Env {
			spent["platform_overrides."+platform+".env."+key] = []string{value}
		}
	}
	for _, where := range slices.Sorted(maps.Keys(spent)) {
		for _, value := range spent[where] {
			for _, ref := range userConfigRef.FindAllStringSubmatch(value, -1) {
				if _, declared := m.UserConfig[ref[1]]; !declared {
					problems = append(problems, fmt.Sprintf(
						"%s spends ${user_config.%s}, which user_config does not declare", where, ref[1]))
				}
			}
		}
	}
	return problems
}

// platformProblems is checks 4 and 5, over the commands and what they
// run as commandProblems found them.
func platformProblems(m manifest, files []staged, commands map[string]string, runs map[string]staged) []string {
	var problems []string
	// 4. every platform_overrides entry names a platform the bundle
	// claims. An override nothing can reach is the same defect as a
	// command nobody staged.
	for _, platform := range slices.Sorted(maps.Keys(m.Server.MCPConfig.PlatformOverrides)) {
		if !slices.Contains(m.Compatibility.Platforms, platform) {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides has %q, which compatibility.platforms does not claim", platform))
		}
	}

	// 5. every claimed platform spawns the file staged FOR it, not merely
	// a staged program. Deleting an override passes every check above:
	// the platform then runs the default command, which is another
	// platform's binary, and that file really is in the bundle. A
	// claimed platform nothing is staged for is identityProblems'.
	for _, platform := range m.Compatibility.Platforms {
		i := slices.IndexFunc(files, func(f staged) bool { return f.platform == platform })
		if i < 0 {
			continue
		}
		from := ""
		if _, ok := commands[platform]; ok {
			from = platform
		}
		if file, ok := runs[from]; ok && file.path != files[i].path {
			problems = append(problems, fmt.Sprintf(
				"platform %q runs %q, and the file staged for it is %q", platform, file.path, files[i].path))
		}
	}
	return problems
}

// args is mcp_config.args as strings: none when the key is missing, null
// or not a list of strings. settingProblems reports the first two, and
// the schema the third.
func (m manifest) args() []string {
	var args []string
	if err := json.Unmarshal(m.Server.MCPConfig.Args, &args); err != nil {
		return nil
	}
	return args
}

// licenseOf reads the SPDX identifier the LICENSE file under root
// carries, so the manifest cannot claim a different one; "" for a
// license it does not recognize.
func licenseOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "LICENSE")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return "", fmt.Errorf("cannot read LICENSE: %w", err)
	}
	switch text := string(data); {
	case strings.Contains(text, "Apache License"):
		return "Apache-2.0", nil
	case strings.Contains(text, "MIT License"):
		return "MIT", nil
	default:
		return "", nil
	}
}

// onlyMatch resolves a glob that must name exactly one file.
//
// The layout under dist/ carries a build id and an amd64 variant, so a
// glob that quietly matched two would pack whichever sorted first — and
// a bundle built from the wrong binary is not something a checksum
// catches, because the checksum is of what was built.
func onlyMatch(pattern, what string) (string, error) {
	found, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(found) != 1 {
		return "", fmt.Errorf("expected exactly one file for %s matching %s, found %d: %s",
			what, pattern, len(found), strings.Join(found, " "))
	}
	return found[0], nil
}

// stampManifest is the committed manifest raw with version written in,
// as the bundle carries it. The version goes in through a decode and an
// encode, because a sed over JSON is how a quote ends up inside a
// string. That encode sorts the keys and escapes <, > and &, so what it
// returns is the committed document but not its bytes, and it is held
// to the vendored schema again before anything ships it.
func stampManifest(raw []byte, version string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document["version"] = version
	stamped, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := validateDocument(mcpbSchemaFile, "the stamped manifest", stamped); err != nil {
		return nil, err
	}
	return append(stamped, '\n'), nil
}

// zipEpoch is the timestamp every entry carries.
//
// Fixed rather than the source file's time, so the same inputs give a
// byte-identical archive — goreleaser already stamps the binaries with
// the commit's time for the same reason. It is the earliest a zip can
// represent: leaving it unset writes zeroes, which display as the
// impossible "1980-00-00" and are a malformed date rather than an
// absent one.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// writeBundle writes the zip to f and closes it: the stamped manifest at
// the root, then every staged file from sources, by name, so the same
// inputs give the same archive, which is the least a build can offer
// somebody checking a checksum.
func writeBundle(f io.WriteCloser, manifest []byte, sources map[string]string) error {
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)

	if err := addBytes(zw, "manifest.json", manifest, 0o644); err != nil {
		return err
	}
	files := slices.Clone(bundleFiles)
	slices.SortFunc(files, func(a, b staged) int { return strings.Compare(a.path, b.path) })
	for _, file := range files {
		if err := addFile(zw, file.path, sources[file.path], file.mode()); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	// Close's error is the pack's: the last of the archive can reach the
	// disk only there, and a bundle cut short must not pass. The deferred
	// Close then does nothing.
	return f.Close()
}

func addBytes(zw *zip.Writer, name string, body []byte, mode os.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func addFile(zw *zip.Writer, name, src string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // paths this repository owns or resolved from dist
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

// pinnedSchema matches the versioned manifest schema URL and captures the
// version it declares.
var pinnedSchema = regexp.MustCompile(`/mcpb-manifest-v(\d+\.\d+)\.schema\.json$`)

// upstreamSchema matches the host and path the schema is published at,
// and captures the ref it is served from.
//
// An allow-list over the whole URL rather than a list of refs to refuse,
// for §9.1's reason one level down: refusing `main`, `master` and `HEAD`
// passes a branch called anything else, a partial tag like `v2.1` that
// upstream re-points as it releases, and the right filename served by
// somebody who is not upstream.
var upstreamSchema = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/anthropics/mcpb/([^/]+)/schemas/mcpb-manifest-v\d+\.\d+\.schema\.json$`)

// immutableRef is a ref that cannot be moved under the document: a full
// release tag or a commit SHA. A partial tag is a branch with a version
// number in it.
var immutableRef = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$`)

// claudeDesktopFloor is the Claude Desktop version every bundle in the
// family claims.
const claudeDesktopFloor = ">=0.10.0"

// minManifestVersion is the format version this repository has checked,
// and the floor a manifest may not fall below.
//
// Every other claim below holds the manifest against ITSELF, and a stale
// manifest is perfectly self-consistent: 0.2 beside a 0.2 schema passes
// all of them, which is the shape this check was written to stop
// spreading. Checked 2026-09-17 against the published schemas: 0.2, 0.3
// and 0.4 are served and 0.5 is not, and 0.4's only change is a `uv`
// value in the `server.type` enum, which a `binary` server gains nothing
// from. Raise this only after checking what a newer format changes AND
// that a desktop installs a bundle declaring it.
const minManifestVersion = "0.3"

// olderThan reports whether a major.minor version is below the floor,
// numerically. As text "0.10" sorts before "0.3", which is a bug that
// waits for the tenth minor version and then looks like the manifest's
// fault.
func olderThan(version, floor string) bool {
	major, minor, ok := majorMinor(version)
	floorMajor, floorMinor, floorOK := majorMinor(floor)
	if !ok || !floorOK {
		return !ok
	}
	if major != floorMajor {
		return major < floorMajor
	}
	return minor < floorMinor
}

func majorMinor(v string) (int, int, bool) {
	before, after, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	major, err := strconv.Atoi(before)
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(after)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// manifestShapeProblems is the check itself, over the three values it
// reads. Taking strings rather than a struct so the sibling servers can
// call it from whatever they decode a manifest into.
func manifestShapeProblems(schema, manifestVersion, support string) []string {
	var problems []string

	switch {
	case schema == "":
		problems = append(problems, "the manifest has no $schema, so nothing says which version of the "+
			"format it is, and manifest_version is a claim with nothing to check it against")
	case !pinnedSchema.MatchString(schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not the pinned mcpb-manifest-v<version>.schema.json form. An "+
				"unpinned schema validates against whatever upstream serves today, which is the same "+
				"defect `pins` refuses for an action", schema))
	case !upstreamSchema.MatchString(schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not upstream's published path. A schema fetched from anywhere else "+
				"is not the document this format is defined by", schema))
	case !immutableRef.MatchString(upstreamSchema.FindStringSubmatch(schema)[1]):
		problems = append(problems, fmt.Sprintf(
			"$schema is served from %q, which can be re-pointed — a branch, or a partial tag. The path "+
				"pins the format and the ref pins the bytes, so an amendment upstream changes what this "+
				"document validates against. Name a full tag or a commit SHA",
			upstreamSchema.FindStringSubmatch(schema)[1]))
	default:
		declared := pinnedSchema.FindStringSubmatch(schema)[1]
		if declared != manifestVersion {
			problems = append(problems, fmt.Sprintf(
				"manifest_version is %q and $schema pins v%s; a document cannot claim one version and "+
					"validate against another", manifestVersion, declared))
		}
	}

	if olderThan(manifestVersion, minManifestVersion) {
		problems = append(problems, fmt.Sprintf(
			"manifest_version is %q and this repository has checked %s; a manifest agreeing with its own "+
				"$schema is still a manifest a version behind", manifestVersion, minManifestVersion))
	}

	if support == "" {
		problems = append(problems, "the manifest has no support URL, so a bundle that fails on "+
			"somebody's desktop does not say where to report it")
	}
	return problems
}

// identityProblems holds what the manifest says it is: the server's own
// name, a binary, the family's Claude Desktop floor, a privacy policy,
// and exactly the platforms the packer stages an entry point for, so
// dropping one platform and its override together is refused. The
// manifest specification requires privacy_policies of a server that
// connects to a service processing user data, as this one does to
// Google, and neither the schema nor `mcpb validate` enforces it.
// validateManifest holds each platform to the file staged for it.
func identityProblems(m manifest, files []staged) []string {
	var problems []string
	if m.Name != server.Name {
		problems = append(problems, fmt.Sprintf("the manifest's name is %q and the server's is %q", m.Name, server.Name))
	}
	if m.Server.Type != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q; the bundle carries a binary", m.Server.Type))
	}
	if m.Compatibility.ClaudeDesktop != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			m.Compatibility.ClaudeDesktop, claudeDesktopFloor))
	}
	var entryPoints []string
	for _, f := range files {
		if f.platform != "" {
			entryPoints = append(entryPoints, f.platform)
		}
	}
	slices.Sort(entryPoints)
	if claimed := slices.Sorted(slices.Values(m.Compatibility.Platforms)); !slices.Equal(claimed, entryPoints) {
		problems = append(problems, fmt.Sprintf("compatibility.platforms is %v; the packer stages entry points for %v",
			claimed, entryPoints))
	}
	if len(m.PrivacyPolicies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Google")
	}
	return problems
}

// checkLauncher holds the Linux launcher under root to the binaries the
// packer stages for it, and its install hint to go.mod, each read once.
func checkLauncher(files []staged, root string) []string {
	script, err := os.ReadFile(filepath.Join(root, launcherPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return []string{"cannot read the launcher: " + err.Error()}
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return []string{"cannot read go.mod: " + err.Error()}
	}
	return launcherProblems(files, string(script), string(goMod))
}

// launcherProblems is checkLauncher over the two files' contents. The
// launcher chooses between the packer's own names, which live in a shell
// script no manifest mentions, so nothing else can see them: renaming a
// staged binary otherwise passes every check and ships a bundle that
// fails for every user of that platform. It asserts the names rather
// than generating the script: a launcher is a thing a person should be
// able to read.
func launcherProblems(files []staged, launcher, goMod string) []string {
	var problems []string
	var launchable, chosen []string
	for _, f := range files {
		if f.launched {
			launchable = append(launchable, path.Base(f.path))
		}
	}
	for _, m := range launcherBinary.FindAllStringSubmatch(launcher, -1) {
		chosen = append(chosen, m[1])
	}
	slices.Sort(launchable)
	slices.Sort(chosen)
	chosen = slices.Compact(chosen)
	if !slices.Equal(launchable, chosen) {
		problems = append(problems, fmt.Sprintf("the launcher runs [%s]; the packer stages [%s]",
			strings.Join(chosen, " "), strings.Join(launchable, " ")))
	}
	return append(problems, installHintProblems(launcher, goMod)...)
}

// installHintProblems holds the launcher's install hint, given on an
// architecture the bundle does not carry, to the module go.mod declares.
// The path changes with every major version from v2, so a hint written
// before one names a module that `go install` no longer updates.
func installHintProblems(launcher, goMod string) []string {
	module := moduleLine.FindStringSubmatch(goMod)
	if module == nil {
		return []string{"go.mod names no module"}
	}
	want := "go install " + module[1] + "/cmd/" + bundleName + "@latest"
	hints := installHint.FindAllString(launcher, -1)
	if len(hints) == 0 {
		return []string{"the launcher gives no install hint for an architecture the bundle does not carry"}
	}
	var problems []string
	for _, hint := range hints {
		if hint != want {
			problems = append(problems, fmt.Sprintf("the launcher says %q, and go.mod's module gives %q", hint, want))
		}
	}
	return problems
}

var (
	// moduleLine is go.mod's module directive. Trailing space is allowed
	// because a checkout with Windows line endings ends it with "\r".
	moduleLine  = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	installHint = regexp.MustCompile(`go install [^\s"]+`)
	// launcherBinary is a binary the launcher picks, beside itself.
	launcherBinary = regexp.MustCompile(`\$dir/([A-Za-z0-9._-]+)`)
)

// Why bundleLeftOut leaves a setting out.
const (
	diagnostics = "diagnostics, set by hand while chasing a problem"
	tuning      = "tuning, whose default suits an install; a call may ask for more"
)

// bundleLeftOut is every variable the server reads that the bundle does
// not ask for, with why. A removed setting is left out without an
// entry, because config says which it is.
var bundleLeftOut = map[string]string{
	config.EnvPrefix + "LOG_LEVEL":     diagnostics,
	config.EnvPrefix + "LOG_FORMAT":    diagnostics,
	config.EnvPrefix + "MAX_CELLS":     tuning,
	config.EnvPrefix + "MAX_CHARS":     tuning,
	config.EnvPrefix + "HTTP_TIMEOUT":  "a timeout, whose default suits an install",
	config.EnvPrefix + "WRITE_TIMEOUT": "a timeout, matching the 180 seconds Sheets allows a request",
	config.EnvConfigDir:                "moves every profile, which tests do; a second account is a profile",
	config.EnvRefreshToken:             "a credential for automation; a bundle install uses the login's keyring entry",
}

// bundleVarsFrom is the settings the bundle asks for at install: every
// variable the server reads, vars, less the removed ones and less each
// one leftOut gives a reason for. A setting added later is asked for
// until the manifest asks for it or a reason is written. It also refuses
// an exclusion the server has no use for: one naming a variable it does
// not read, or one config already says is removed.
func bundleVarsFrom(vars []config.Var, leftOut map[string]string) (asked []config.Var, problems []string) {
	byName := make(map[string]config.Var, len(vars))
	for _, v := range vars {
		byName[v.Name] = v
		if !v.Removed && leftOut[v.Name] == "" {
			asked = append(asked, v)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(leftOut)) {
		v, read := byName[name]
		switch {
		case !read:
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which the server does not read", name))
		case v.Removed:
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which a release removed, "+
				"and so left out already", name))
		}
	}
	return asked, problems
}

// bundleKey is the user_config key a setting is asked under: its name
// without the prefix, lowercased, which gives the family's bundles the
// same keys.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// userConfigOption is the part of a user_config entry these checks read.
// Default stays raw, so an absent default is told apart from a null one,
// and value is it decoded, once, as the manifest is.
type userConfigOption struct {
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Default     json.RawMessage `json:"default"`
	value       any
}

// UnmarshalJSON decodes an entry and its default.
func (o *userConfigOption) UnmarshalJSON(data []byte) error {
	type entry userConfigOption
	if err := json.Unmarshal(data, (*entry)(o)); err != nil {
		return err
	}
	if o.Default == nil {
		return nil
	}
	return json.Unmarshal(o.Default, &o.value)
}

// turnedTo is the value a switch takes when the person changes it from
// its default: off for one that defaults to true, and on for any other,
// a missing or unreadable default included.
func (o userConfigOption) turnedTo() bool {
	on, _ := o.value.(bool)
	return !on
}

// turnedNames is keys as a set gives them, each with the value turnedTo
// gives it: [enable_destructive on, read_only on].
func turnedNames(m manifest, keys []string) string {
	named := make([]string, len(keys))
	for i, key := range keys {
		named[i] = key + " off"
		if m.UserConfig[key].turnedTo() {
			named[i] = key + " on"
		}
	}
	return "[" + strings.Join(named, ", ") + "]"
}

// fieldRule is the exact words a setting's field must carry, since the
// server refuses anything else at start, and what a field without them
// fails to do.
type fieldRule struct{ words, fails string }

// fieldRules are the settings whose field says what the server accepts.
// This server has no list of modes: every other setting the bundle asks
// for is a file or a switch.
var fieldRules = map[string]fieldRule{
	config.EnvPrefix + "PROFILE": {words: config.ProfileRule, fails: "say a profile name is"},
}

// settingProblems holds what the bundle asks for to the server's own
// settings, and reports everything wrong rather than the first thing.
//
// Every setting bundleVarsFrom derives is asked for and reaches its
// variable, and nothing else is. A switch is asked as a boolean, so
// Claude Desktop shows it as one. An optional key has a default,
// because the reference host passes ${user_config.x} to the server as
// written when it has none and the person leaves the field blank (§18).
// The environment the defaults give the server configures it as no
// environment does: a bundle install may turn on nothing an
// unconfigured server leaves off, because in Claude Desktop the only
// other guard is its approval of each tool, which a person can set to
// always allow (§18). The sets of switches the server refuses together
// are the ones bundleRefused declares. mcp_config.args is there and
// empty, and a platform override carries neither env nor args, an empty
// one included: the reference host replaces the base's with an
// override's rather than merging, and an argument such as
// -enable-destructive=true would configure the server past every check
// on env (§18). The profile field says what the server accepts, and a
// bundle that stops asking for it is refused, so that check cannot go
// quiet. leftOut is bundleLeftOut, or a test's.
func settingProblems(m manifest, leftOut map[string]string) []string {
	vars := config.Vars()
	asked, problems := bundleVarsFrom(vars, leftOut)
	problems = append(problems, passByEnvProblems(m)...)

	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if opt := m.UserConfig[key]; !opt.Required && opt.Default == nil {
			problems = append(problems, fmt.Sprintf("user_config.%s is optional with no default; the reference host "+
				"passes ${user_config.%s} to the server as written when the field is left blank", key, key))
		}
	}

	var switches []string
	askedName, askedKey := map[string]bool{}, map[string]bool{}
	for _, v := range asked {
		key := bundleKey(v.Name)
		askedName[v.Name], askedKey[key] = true, true
		opt, ok := m.UserConfig[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, v.Name))
			continue
		}
		if got, want := m.Server.MCPConfig.Env[v.Name], "${user_config."+key+"}"; got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", v.Name, got, want))
		}
		if v.Switch {
			switches = append(switches, key)
			if opt.Type != "boolean" {
				problems = append(problems, fmt.Sprintf("user_config.%s is a %s; %s is a switch, so ask for a boolean",
					key, cmp.Or(opt.Type, "field with no type"), v.Name))
			}
		}
		if rule, ok := fieldRules[v.Name]; ok && !strings.Contains(opt.Description, rule.words) {
			problems = append(problems, fmt.Sprintf("user_config.%s does not %s %q", key, rule.fails, rule.words))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(fieldRules)) {
		if !askedName[name] {
			problems = append(problems, fmt.Sprintf("the bundle asks for no %s, so nothing holds its field to %q",
				name, fieldRules[name].words))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if !askedKey[key] {
			problems = append(problems, fmt.Sprintf("user_config asks for %s, which is no setting's key", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Server.MCPConfig.Env)) {
		switch {
		case askedName[name]:
		case slices.ContainsFunc(vars, func(v config.Var) bool { return v.Name == name && v.Removed }):
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which a release removed", name))
		case leftOut[name] != "":
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle leaves out: %s",
				name, leftOut[name]))
		default:
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the server does not read", name))
		}
	}
	slices.Sort(switches)
	defaults, defaultsErr := defaultsProblems(m)
	problems = append(problems, defaults...)
	return append(problems, refusalProblems(m, switches, bundleRefused, defaultsErr)...)
}

// passByEnvProblems holds that every setting passes by mcp_config.env:
// mcp_config.args is there and empty, and a platform override carries
// neither env nor args, an empty one included, since the reference host
// replaces the base's with an override's rather than merging (§18).
func passByEnvProblems(m manifest) []string {
	var problems []string
	const keepArgs = "keep it as [], since no Claude Desktop launch has shown the key optional"
	switch raw, args := m.Server.MCPConfig.Args, m.args(); {
	case raw == nil:
		problems = append(problems, "mcp_config.args is missing; "+keepArgs)
	case string(raw) == "null":
		problems = append(problems, "mcp_config.args is null; "+keepArgs)
	case len(args) > 0:
		problems = append(problems, fmt.Sprintf("mcp_config.args is %q; the bundle passes every setting "+
			"through mcp_config.env, and an argument would configure the server past these checks", args))
	}
	for _, platform := range slices.Sorted(maps.Keys(m.Server.MCPConfig.PlatformOverrides)) {
		over := m.Server.MCPConfig.PlatformOverrides[platform]
		if over.Env != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.env is set; a host replaces "+
				"mcp_config.env with it, an empty one too, so put every variable in mcp_config.env", platform))
		}
		if over.Args != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.args is set; a host replaces "+
				"mcp_config.args with it, an empty one too, and the bundle passes every setting through "+
				"mcp_config.env", platform))
		}
	}
	return problems
}

// hostEnv is the environment the reference host builds from the
// bundle's defaults, with each key in turned set to the value turnedTo
// gives it, as the person changing that field would. A boolean becomes
// "true" or "false", a "" default stays "", a number is written out,
// and a reference with no default is passed through as written.
//
// A variable spending a required key with no default is left out, since
// the person supplies it and it is theirs either way. One with a default
// is not: Claude Desktop's form may fill it in from the default, and a
// person may accept it unread. A default that is a list or an object is
// not guessed at: its variable is left out, and its key is in
// unreadable with what it is.
//
// It reads mcp_config.env alone. That is sound only because
// settingProblems refuses an argument and an override's env; relaxing
// either needs this to read the same.
func hostEnv(m manifest, turned ...string) (env, unreadable map[string]string) {
	env, unreadable = map[string]string{}, map[string]string{}
	for name, value := range m.Server.MCPConfig.Env {
		var b strings.Builder
		skip, last := false, 0
		for _, at := range userConfigRef.FindAllStringSubmatchIndex(value, -1) {
			key, sub := value[at[2]:at[3]], value[at[0]:at[1]]
			opt := m.UserConfig[key]
			def := opt.value
			if slices.Contains(turned, key) {
				def = opt.turnedTo()
			}
			switch d := def.(type) {
			case bool:
				sub = strconv.FormatBool(d)
			case string:
				sub = d
			case float64:
				sub = strconv.FormatFloat(d, 'f', -1, 64)
			case nil:
				skip = skip || opt.Required
			case []any:
				unreadable[key], skip = "a list", true
			case map[string]any:
				unreadable[key], skip = "an object", true
			}
			b.WriteString(value[last:at[0]] + sub)
			last = at[1]
		}
		if !skip {
			env[name] = b.String() + value[last:]
		}
	}
	return env, unreadable
}

// loadEnv starts the server's configuration from an environment alone.
func loadEnv(env map[string]string) (config.Config, error) {
	return config.Load(nil, func(name string) string { return env[name] })
}

// defaultsProblems reports a bundle whose defaults configure the server
// otherwise than no environment does, naming the fields that differ. A
// default that is a list or an object is reported rather than guessed
// at, every one in key order, and the comparison still runs over the
// rest. defaultsErr is what the server refuses the defaults with, which
// decides whether refusalProblems starts the sets of switches.
func defaultsProblems(m manifest) (problems []string, defaultsErr error) {
	env, unreadable := hostEnv(m)
	for _, key := range slices.Sorted(maps.Keys(unreadable)) {
		problems = append(problems, fmt.Sprintf("user_config.%s defaults to %s, which the defaults check cannot "+
			"build an environment from: give it a boolean, a string or a number", key, unreadable[key]))
	}
	got, defaultsErr := loadEnv(env)
	if defaultsErr != nil {
		problems = append(problems, fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v",
			defaultsErr))
		return problems, defaultsErr
	}
	want, err := loadEnv(nil)
	if err != nil {
		return append(problems, fmt.Sprintf("an unconfigured server is refused: %v", err)), nil
	}
	if diff := configDiff(got, want); len(diff) > 0 {
		problems = append(problems, fmt.Sprintf("the bundle's defaults configure the server otherwise than no "+
			"environment does, in %s; a bundle install may turn on nothing an unconfigured server leaves off",
			strings.Join(diff, ", ")))
	}
	return problems, nil
}

// configDiff names the fields two configurations disagree on.
func configDiff(a, b config.Config) []string {
	return fieldDiff(a, b)
}

// fieldDiff names the fields of two values of one struct type that
// differ. An unexported field cannot be read out through Interface, so
// it is compared with Value.Equal where its type compares; one that does
// not, and differs, is reported unnamed.
func fieldDiff(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var fields []string
	unnamed := false
	for i := range va.NumField() {
		f := va.Type().Field(i)
		switch {
		case f.IsExported():
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				fields = append(fields, f.Name)
			}
		case strictlyComparable(f.Type):
			if !va.Field(i).Equal(vb.Field(i)) {
				fields = append(fields, f.Name)
			}
		default:
			unnamed = true
		}
	}
	if unnamed && len(fields) == 0 && !reflect.DeepEqual(a, b) {
		fields = append(fields, "an unexported field")
	}
	return fields
}

// strictlyComparable reports whether every value of t compares with
// Value.Equal. An interface type compares as a type, but Equal panics on
// a dynamic value that does not, so one anywhere inside t rules it out.
func strictlyComparable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return false
	case reflect.Struct:
		for i := range t.NumField() {
			if !strictlyComparable(t.Field(i).Type) {
				return false
			}
		}
		return true
	case reflect.Array:
		return strictlyComparable(t.Elem())
	}
	return t.Comparable()
}

// bundleRefused are the sets of the bundle's switches the server refuses
// at start, each sorted, each switch in a set turned from its default.
// Each field in one names the others' titles in its description. Build
// refuses none: read-only leaves the destructive and Connected Sheets
// switches nothing to register and external formulas no write to allow,
// rather than any pair being refused, and the fields say so. The gate
// starts every set of the switches and holds the refused ones to this
// list, both ways, so a refusal added to Build fails here until it is
// declared and the fields say so.
var bundleRefused [][]string

// refusalProblems starts every set of the bundle's switches over the
// defaults, each switch in it turned from its default, and holds the
// ones the server refuses to declared: a refusal it does not declare,
// which no description then warns of, and a declared one the server
// starts with, each reported for the smallest set that shows it.
// switches is the user_config key of every switch the bundle asks for,
// sorted. A declared key that is not one of them is refused too, and so
// is one with no title, which the others' descriptions would name, each
// once. When the defaults alone do not start, which defaultsErr says, it
// starts no set: the defaults check reports that, and every set would
// fail with it.
func refusalProblems(m manifest, switches []string, declared [][]string, defaultsErr error) []string {
	problems := declaredProblems(m, switches, declared)
	if defaultsErr != nil {
		return problems
	}
	return append(problems, startProblems(m, switches, declared)...)
}

// declaredProblems holds each key in a declared set to a switch with a
// title, each once, and each switch's description to the titles of the
// others in its sets, trimmed.
func declaredProblems(m manifest, switches []string, declared [][]string) []string {
	title := func(key string) string { return strings.TrimSpace(m.UserConfig[key].Title) }
	var problems []string
	inSets := map[string]bool{}
	for _, set := range declared {
		for _, key := range set {
			inSets[key] = true
		}
	}
	titled := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(inSets)) {
		switch {
		case !slices.Contains(switches, key):
			problems = append(problems, fmt.Sprintf("bundleRefused names %s, which is not a switch the bundle asks for", key))
		case title(key) == "":
			problems = append(problems, fmt.Sprintf("user_config.%s has no title, which the fields the server "+
				"refuses it with must name", key))
		default:
			titled[key] = true
		}
	}
	for _, set := range declared {
		for _, key := range set {
			if !slices.Contains(switches, key) {
				continue
			}
			description := m.UserConfig[key].Description
			for _, other := range set {
				if other != key && titled[other] && !strings.Contains(description, title(other)) {
					problems = append(problems, fmt.Sprintf("user_config.%s does not name %q, which the server "+
						"refuses it with", key, title(other)))
				}
			}
		}
	}
	return problems
}

// startProblems starts every set of the switches over the defaults,
// each switch in the set turned from its default, and holds the
// server's refusals to declared, both ways. Every subset of a set comes
// before it in this order, so the first set to show a defect is the
// smallest, and a superset is not reported again.
func startProblems(m manifest, switches []string, declared [][]string) []string {
	var problems []string
	var undeclared []int
	startsDeclared := map[int]bool{}
	for bits := 1; bits < 1<<len(switches); bits++ {
		var turned []string
		for i, key := range switches {
			if bits&(1<<i) != 0 {
				turned = append(turned, key)
			}
		}
		env, _ := hostEnv(m, turned...)
		_, err := loadEnv(env)
		var within []int
		for i, set := range declared {
			if !slices.ContainsFunc(set, func(key string) bool { return !slices.Contains(turned, key) }) {
				within = append(within, i)
			}
		}
		switch {
		case err != nil && len(within) == 0:
			if slices.ContainsFunc(undeclared, func(shown int) bool { return shown&bits == shown }) {
				continue
			}
			undeclared = append(undeclared, bits)
			problems = append(problems, fmt.Sprintf("the server refuses %s, and bundleRefused does not "+
				"declare it, so no field's description warns of it: %v", turnedNames(m, turned), err))
		case err == nil:
			for _, i := range within {
				if !startsDeclared[i] {
					startsDeclared[i] = true
					problems = append(problems, fmt.Sprintf("bundleRefused declares that the server refuses %s, "+
						"and it starts with them", turnedNames(m, declared[i])))
				}
			}
		}
	}
	return problems
}
