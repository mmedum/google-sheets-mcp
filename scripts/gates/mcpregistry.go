package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// The MCP registry entry.
//
// It comes last in a release because the registry sends a HEAD to the
// bundle's download URL before it accepts an entry, so the release has to
// exist first. That ordering is also why this is not goreleaser's `mcp`
// block: that block takes a registry type, an identifier and a
// transport, and has nowhere to put a hash — and a client verifies the
// bundle against a SHA-256 before installing it, which the registry
// requires for an MCPB package.
//
// So the hash comes from the release's own checksums.txt. The entry then
// describes the bytes that were published rather than a rebuild of them,
// and the file it names is already under the signature.
//
// Everything else is derived rather than typed: the owner and repository
// from the module path, the description from the bundle manifest. A
// constant beside either would be a second copy with nothing keeping it
// honest.

const registrySchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"

// registryDescriptionMax is the schema's own limit on `description`,
// which is a required field. Checked against the published schema rather
// than assumed.
const registryDescriptionMax = 100

// sha256Line matches a checksums.txt row: the hash, then the file.
var sha256Line = regexp.MustCompile(`^([a-f0-9]{64})\s+\*?(\S+)$`)

// majorVersion matches the /vN a module path carries from v2 onwards.
//
// v2 onwards, not v1: Go adds the suffix from v2, so a `/v1` segment is
// an ordinary directory and stripping it would name the wrong repository.
var majorVersion = regexp.MustCompile(`^v([2-9]|[1-9][0-9]+)$`)

type registryEntry struct {
	Schema      string            `json:"$schema"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	WebsiteURL  string            `json:"websiteUrl"`
	Repository  registryRepo      `json:"repository"`
	Packages    []registryPackage `json:"packages"`
}

type registryRepo struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type registryPackage struct {
	RegistryType string            `json:"registryType"`
	Identifier   string            `json:"identifier"`
	FileSHA256   string            `json:"fileSha256"`
	Version      string            `json:"version"`
	Transport    registryTransport `json:"transport"`
}

type registryTransport struct {
	Type string `json:"type"`
}

// githubRepo splits a module path into owner and repository.
func githubRepo(module string) (owner, name string, err error) {
	parts := strings.Split(module, "/")
	if len(parts) == 4 && majorVersion.MatchString(parts[3]) {
		parts = parts[:3]
	}
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("module path %q is not github.com/OWNER/REPO", module)
	}
	return parts[1], parts[2], nil
}

// modulePath reads the module this repository is, from go.mod.
func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("go.mod names no module")
}

// moduleMajor reads the major version off a module path: its /vN
// suffix, or 1 when it has none.
func moduleMajor(module string) int {
	last := module[strings.LastIndex(module, "/")+1:]
	if !majorVersion.MatchString(last) {
		return 1
	}
	n, _ := strconv.Atoi(last[1:])
	return n
}

// tagMajor reads N off a vN.x.y tag. A prerelease suffix is allowed; a
// sign is not, because strconv would read v+1 as major 1.
func tagMajor(tag string) (int, error) {
	rest, ok := strings.CutPrefix(tag, "v")
	major, _, dotted := strings.Cut(rest, ".")
	n, err := strconv.Atoi(major)
	if !ok || !dotted || err != nil || major[0] == '+' || major[0] == '-' {
		return 0, fmt.Errorf("tag %q is not vMAJOR.MINOR.PATCH", tag)
	}
	return n, nil
}

// tagMatchesModule refuses a tag whose major version is not the
// module's.
//
// `go install ...@latest` resolves within one module path, so a v2 tag
// on a module without /v2 is never served, and a v1 tag on a /v2 module
// is served to nobody who asked for v1. Every other gate passes both.
// v0 and v1 both mean no suffix.
func tagMatchesModule(tag, module string) error {
	got, err := tagMajor(tag)
	if err != nil {
		return err
	}
	want := moduleMajor(module)
	if got == want || (got == 0 && want == 1) {
		return nil
	}
	suffix := "no /vN suffix"
	if got >= 2 {
		suffix = fmt.Sprintf("/v%d", got)
	}
	return fmt.Errorf("tag %s is major %d and go.mod's module %s is major %d; "+
		"a v%d tag needs %s on the module path", tag, got, module, want, got, suffix)
}

// releaseTag is the release workflow's check that the tag it was started
// for agrees with go.mod, before anything is built or published.
func releaseTag(tag string) error {
	module, err := modulePath()
	if err != nil {
		return err
	}
	return tagMatchesModule(tag, module)
}

// bundleRow finds the single .mcpb in a checksums file.
//
// Two bundles or none is a release that did not build the way it was
// meant to, and picking one of them would publish a hash for a file
// nobody chose.
func bundleRow(checksums string) (name, sum string, err error) {
	data, err := os.ReadFile(checksums) //nolint:gosec // a path the release passes in
	if err != nil {
		return "", "", err
	}
	rows := 0
	for _, line := range strings.Split(string(data), "\n") {
		m := sha256Line.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		rows++
		if path.Ext(m[2]) != ".mcpb" {
			continue
		}
		if name != "" {
			return "", "", fmt.Errorf("%s lists more than one .mcpb: %s and %s", checksums, name, m[2])
		}
		name, sum = path.Base(m[2]), m[1]
	}
	switch {
	case rows == 0:
		return "", "", fmt.Errorf("%s has no checksum rows", checksums)
	case name == "":
		return "", "", fmt.Errorf("%s lists no .mcpb among %d rows; the bundle was not packed, "+
			"or was not named in checksum.extra_files", checksums, rows)
	}
	return name, sum, nil
}

// registryPublish writes the entry for one release to out.
func registryPublish(out io.Writer, version, checksums string) error {
	module, err := modulePath()
	if err != nil {
		return err
	}
	owner, repo, err := githubRepo(module)
	if err != nil {
		return err
	}

	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	semver := strings.TrimPrefix(tag, "v")
	if semver == "" {
		return fmt.Errorf("empty version")
	}
	// release.yml checks this before goreleaser runs. A dispatch of the
	// publish workflow never passes through there, so it is checked here
	// too.
	if err := tagMatchesModule(tag, module); err != nil {
		return err
	}

	manifest, err := readManifest()
	if err != nil {
		return err
	}
	description, _ := manifest["description"].(string)
	switch {
	case description == "":
		return fmt.Errorf("%s has no description for the registry entry to carry", mcpbManifestPath)
	case len(description) > registryDescriptionMax:
		// One owner for the short description: the bundle manifest's.
		// MCPB already separates it from long_description, so a
		// description too long for the registry is a manifest that has
		// put the wrong text in the short field.
		return fmt.Errorf("the manifest description is %d characters and the registry allows %d; "+
			"shorten it and keep the detail in long_description", len(description), registryDescriptionMax)
	}

	name, sum, err := bundleRow(checksums)
	if err != nil {
		return err
	}

	entry := registryEntry{
		Schema:      registrySchema,
		Name:        fmt.Sprintf("io.github.%s/%s", owner, repo),
		Description: description,
		Version:     semver,
		WebsiteURL:  fmt.Sprintf("https://github.com/%s/%s#readme", owner, repo),
		Repository: registryRepo{
			URL:    fmt.Sprintf("https://github.com/%s/%s", owner, repo),
			Source: "github",
		},
		Packages: []registryPackage{{
			RegistryType: "mcpb",
			Identifier: fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
				owner, repo, tag, name),
			FileSHA256: sum,
			Version:    semver,
			Transport:  registryTransport{Type: "stdio"},
		}},
	}
	encoded, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	// Against the schema the entry cites, before anybody publishes it.
	// The rules above hold the fields this repository fills in; this
	// holds the document the registry reads. A rejected publish costs a
	// dispatch against a tag that already shipped, and an entry that is
	// accepted and wrong cannot be withdrawn — so the refusal belongs on
	// the path that builds it.
	if err := validateDocument(registrySchemaFile, "the registry entry", encoded); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}
