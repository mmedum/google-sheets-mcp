package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Claude Desktop bundle. A `.mcpb` is a zip carrying a manifest, the
// binaries for every platform it claims, and the licence and README —
// opened in Claude Desktop it installs the server and asks for the OAuth
// client JSON, so nobody has to edit a config file by hand.
//
// It is packed here rather than by the official Node CLI, because this
// repository is Go and only Go: an interpreter `make check` needs is a
// prerequisite nobody declared. A bundle is a deflate zip and the
// standard library writes those, so the only thing the CLI was really
// buying was validation of the manifest against its published schema.
//
// checkManifest below replaces that, and is not merely a substitute. A
// schema can say the manifest is well formed; it cannot say that
// entry_point names a file that is actually in the bundle, that a
// platform override points at something that was staged, or that a
// ${user_config.x} refers to a key somebody is asked for. Each of those
// is well formed by any schema and produces a bundle that installs
// cleanly and then does nothing.

// mcpbPlaceholder is the version the COMMITTED manifest carries. The
// real one is written in as the bundle is packed, so a manifest in the
// tree can never be stale: it does not claim a version at all.
const mcpbPlaceholder = "0.0.0-dev"

// mcpbManifestPath is the committed manifest.
const mcpbManifestPath = "packaging/mcpb/manifest.json"

// bundleName is what the binary is called, and what the manifest's paths
// are built from.
const bundleName = "google-sheets-mcp"

// staged is one file going into the bundle: where to find it in the
// build output, and what it is called inside.
//
// The mode is decided in writeBundle from the name rather than copied
// from the source, because everything under server/ needs the execute
// bit and nothing else does. The packer's own rule is that it forces the
// bit on the entry point alone, so a Windows binary staged from a
// filesystem that lost the mode arrives unrunnable.
type staged struct {
	glob, as, what string
}

// bundleFiles is what goes into the bundle, and it is the same list
// whether or not the binaries exist yet.
//
// A manifest picks a binary by platform and has no key for the
// architecture, so every platform it claims has to work on both. macOS
// does through a universal binary; Windows through amd64, which its
// arm64 build runs under emulation. Linux has neither, so the bundle
// carries both Linux binaries and a launcher that picks between them.
func bundleFiles() []staged {
	return []staged{
		{"*darwin_all*/" + bundleName, "server/" + bundleName, "darwin universal binary"},
		{"*windows_amd64*/" + bundleName + ".exe", "server/" + bundleName + ".exe", "windows amd64 binary"},
		{"*linux_amd64*/" + bundleName, "server/" + bundleName + "-amd64", "linux amd64 binary"},
		{"*linux_arm64*/" + bundleName, "server/" + bundleName + "-arm64", "linux arm64 binary"},
	}
}

// bundleExtras are the files that come from the tree rather than from a
// build, keyed by their name inside the bundle.
func bundleExtras() map[string]string {
	return map[string]string{
		"server/linux-launch.sh": "packaging/mcpb/linux-launch.sh",
		"LICENSE":                "LICENSE",
		"README.md":              "README.md",
	}
}

// bundleNames is every name the bundle will carry, without needing a
// single binary to exist.
//
// This is what lets the manifest be checked on every commit rather than
// only at a release: the names are decided here, so a manifest naming
// something that will never be staged is wrong today and not in three
// weeks when somebody tags.
func bundleNames() map[string]string {
	names := map[string]string{}
	for _, f := range bundleFiles() {
		names[f.as] = ""
	}
	for as, src := range bundleExtras() {
		names[as] = src
	}
	return names
}

// mcpbGate checks the committed manifest without building anything.
//
// It runs in `make check` and in CI, which is the point: the referential
// failures below are checkable against the *names* the packer stages,
// and those are known statically. Only the packing itself has to wait
// for binaries.
func mcpbGate(out io.Writer) error {
	manifest, err := readManifest()
	if err != nil {
		return err
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		return fmt.Errorf("%s carries version %q; the committed manifest must carry %q, "+
			"so a version in the tree can never be a stale one", mcpbManifestPath, got, mcpbPlaceholder)
	}
	// The placeholder is not a version the checks below should reason
	// about, so they see what a release would write.
	manifest["version"] = "1.0.0"
	contents := bundleNames()
	if problems := checkManifest(manifest, contents); len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), mcpbManifestPath)
	}
	for as, src := range bundleExtras() {
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("%s is packed as %s and does not exist: %w", src, as, err)
		}
	}
	_, _ = fmt.Fprintf(out, "bundle manifest ok: %d file(s), %d platform(s)\n",
		len(contents)+1, len(platformsOf(manifest)))
	return nil
}

func platformsOf(manifest map[string]any) []string {
	compat, _ := manifest["compatibility"].(map[string]any)
	list, _ := compat["platforms"].([]any)
	out := make([]string, 0, len(list))
	for _, p := range list {
		if s, ok := p.(string); ok {
			out = append(out, s)
		}
	}
	return out
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

	contents := bundleExtras()
	for _, f := range bundleFiles() {
		src, err := onlyMatch(filepath.Join(dist, f.glob), f.what)
		if err != nil {
			return err
		}
		contents[f.as] = src
	}

	manifest, err := readManifest()
	if err != nil {
		return err
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		return fmt.Errorf("%s carries version %q; it must carry %q", mcpbManifestPath, got, mcpbPlaceholder)
	}
	// Through a decode and an encode rather than a substitution over
	// text: the manifest is JSON, and a sed over JSON is how a quote
	// ends up inside a string.
	manifest["version"] = version
	if problems := checkManifest(manifest, contents); len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in the bundle manifest", len(problems))
	}

	bundle := filepath.Join(dist, fmt.Sprintf("%s_%s.mcpb", bundleName, version))
	if err := writeBundle(bundle, manifest, contents); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "mcpb-pack: wrote %s (%d files)\n", bundle, len(contents)+1)
	return nil
}

// readManifest decodes the committed manifest into a map rather than a
// struct: the packer rewrites one field and must not drop the rest, and
// the manifest schema is not this repository's to model.
func readManifest() (map[string]any, error) {
	raw, err := os.ReadFile(mcpbManifestPath)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode %s: %w", mcpbManifestPath, err)
	}
	return manifest, nil
}

// checkManifest holds the manifest to the bundle actually being built.
//
// Every path the manifest names has to be a file that is going in. A
// manifest whose entry_point points at a binary nobody staged is well
// formed by any schema and produces a bundle that installs and then does
// nothing — and the same is true of a platform override, which is the
// one a sibling's packer checked for two platforms and not the third.
func checkManifest(manifest map[string]any, contents map[string]string) []string {
	var problems []string
	for _, key := range []string{
		"$schema", "manifest_version", "name", "version", "description", "author", "server",
	} {
		if manifest[key] == nil {
			problems = append(problems, "manifest has no "+key)
		}
	}
	server, _ := manifest["server"].(map[string]any)
	if server == nil {
		return append(problems, "manifest has no server block")
	}
	if kind, _ := server["type"].(string); kind != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q, and this bundle ships binaries", kind))
	}

	inBundle := func(where, name string) {
		if name == "" {
			problems = append(problems, where+" is empty")
			return
		}
		if _, ok := contents[name]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s names %q, which is not one of the files being packed", where, name))
		}
	}
	entry, _ := server["entry_point"].(string)
	inBundle("server.entry_point", entry)

	cfg, _ := server["mcp_config"].(map[string]any)
	if cfg == nil {
		return append(problems, "manifest has no server.mcp_config")
	}
	command, _ := cfg["command"].(string)
	inBundle("server.mcp_config.command", bundlePath(command))

	// Every override, not the ones somebody remembered. A sibling's
	// packer verified the entry point and the Linux files and never the
	// win32 command, so a typo in the .exe path would have packed
	// cleanly, installed cleanly and been caught by nothing.
	overrides, _ := cfg["platform_overrides"].(map[string]any)
	for _, platform := range sortedKeys(overrides) {
		over, _ := overrides[platform].(map[string]any)
		c, _ := over["command"].(string)
		inBundle("platform_overrides."+platform+".command", bundlePath(c))
	}
	// A platform the manifest claims and does not override runs the
	// default command, which is the entry point; a platform it overrides
	// and does not claim is an override nobody reaches.
	claimed := map[string]bool{}
	for _, p := range platformsOf(manifest) {
		claimed[p] = true
	}
	for _, platform := range sortedKeys(overrides) {
		if !claimed[platform] {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides names %q, which compatibility.platforms does not claim", platform))
		}
	}

	// Every ${user_config.x} the config spends has to be a key somebody
	// is asked for at install, or the server starts without it.
	declared, _ := manifest["user_config"].(map[string]any)
	env, _ := cfg["env"].(map[string]any)
	for _, name := range sortedKeys(env) {
		value, _ := env[name].(string)
		key, ok := userConfigKey(value)
		if !ok {
			continue
		}
		if _, found := declared[key]; !found {
			problems = append(problems, fmt.Sprintf(
				"env %s spends ${user_config.%s}, which user_config does not declare", name, key))
		}
	}
	return problems
}

// bundlePath turns a manifest command into the name it has inside the
// bundle. ${__dirname} is where the bundle was unpacked.
func bundlePath(command string) string {
	return path.Clean(strings.TrimPrefix(strings.TrimPrefix(command, "${__dirname}"), "/"))
}

// userConfigKey reads the key out of a ${user_config.x} reference.
func userConfigKey(value string) (string, bool) {
	const prefix = "${user_config."
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "}") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(value, prefix), "}"), true
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
		return "", fmt.Errorf("expected exactly one %s matching %s, found %d: %s",
			what, pattern, len(found), strings.Join(found, " "))
	}
	return found[0], nil
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

// writeBundle writes the zip. Names are sorted so the same inputs give
// the same archive, which is the least a build can offer somebody
// checking a checksum.
func writeBundle(bundle string, manifest map[string]any, contents map[string]string) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.Create(bundle) //nolint:gosec // a path built from dist and the version
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)

	if err := addBytes(zw, "manifest.json", append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	for _, name := range sortedNames(contents) {
		if err := addFile(zw, name, contents[name], modeFor(name)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// modeFor decides the mode from the name inside the bundle rather than
// from the source file. Everything under server/ is run, the Windows
// .exe included, and a mode copied from a filesystem that lost the bit
// produces a bundle that installs and cannot start.
func modeFor(name string) os.FileMode {
	if strings.HasPrefix(name, "server/") {
		return 0o755
	}
	return 0o644
}

func sortedNames(contents map[string]string) []string {
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
