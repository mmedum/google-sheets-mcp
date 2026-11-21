package main

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
)

// The release attests every file it publishes: the archives in each format
// the config packs, the bundle mcpb-pack writes and the checksum file
// goreleaser writes. The attest step sits in the job that runs goreleaser
// release, whose dist/ holds the files: another job, or a workflow it
// calls, can be skipped while the release ships, as publish-mcp.yml is on
// a prerelease. That job's token needs id-token: write and attestations:
// write to sign and store the provenance
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/README.md#L64-L76).
// actions/attest v4.2.1, which attest-build-provenance v4.2.2 runs, splits
// subject-path at commas and newlines and trims each glob
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L303),
// so a space-separated list is one glob that matches nothing.
func TestTheReleaseAttestsEveryFileItPublishes(t *testing.T) {
	flows, err := workflowsUnder(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	release, ok := workflowNamed(flows, parityReleaseWorkflow)
	if !ok {
		t.Fatal("no " + parityReleaseWorkflow)
	}
	cfg, err := readGoreleaser(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if p := releaseAttestProblems(release, cfg); p != nil {
		t.Errorf("the release: %q", p)
	}
}

// releaseAttestProblems holds release.yml's build-provenance steps to the
// files the goreleaser config publishes: each step in a job that runs
// goreleaser release, with the token to attest, naming every file.
func releaseAttestProblems(release workflowFile, cfg goreleaserFile) []string {
	files, problems := releaseFiles(cfg)
	problems = append(problems, release.refused...)
	jobs := releaseJobs(release)
	if len(jobs) == 0 {
		return append(problems, "no job of "+release.path+" runs goreleaser-action with args starting `release`, "+
			"so no attest step is held to the files the release publishes")
	}
	for _, job := range jobs {
		attested := false
		for _, s := range release.steps {
			if s.job != job || s.action() != "actions/attest-build-provenance" {
				continue
			}
			attested = true
			subjects, ok := s.input("subject-path")
			if !ok {
				problems = append(problems, fmt.Sprintf("%s:%d: the attestation names no subject-path", release.path, s.line))
				continue
			}
			problems = append(problems, releaseSubjectProblems(subjects.value, files)...)
		}
		if !attested {
			problems = append(problems, fmt.Sprintf("%s: job %s runs goreleaser release and has no build-provenance "+
				"attestation, so no file the release publishes carries one", release.path, job))
			continue
		}
		for _, scope := range []string{"id-token", "attestations"} {
			if !release.grantsWrite(job, scope) {
				problems = append(problems, fmt.Sprintf("%s: job %s attests without %s: write, which actions/attest needs "+
					"to sign and store the provenance; grant it in the job's permissions, or the workflow's when the job sets none",
					release.path, job, scope))
			}
		}
	}
	return problems
}

// releaseJobs are the jobs of w that publish the release, in file order:
// each runs goreleaser-action with args starting `release`.
func releaseJobs(w workflowFile) []string {
	var jobs []string
	for _, s := range w.steps {
		args, _ := s.input("args")
		words := strings.Fields(args.value)
		if s.action() == "goreleaser/goreleaser-action" && len(words) > 0 && words[0] == "release" && !slices.Contains(jobs, s.job) {
			jobs = append(jobs, s.job)
		}
	}
	return jobs
}

// releaseFiles is the subject-path entry for each file the release
// publishes: an archive in each format the config packs, in goreleaser's
// dist directory, the bundle in the directory the mcpb-pack hook writes
// it to, and checksum.name_template's file, which a fixed subject-path has
// to match.
func releaseFiles(cfg goreleaserFile) (files, problems []string) {
	// goreleaser writes into dist where the config sets none
	// (https://github.com/goreleaser/goreleaser/blob/v2.18.1/internal/pipe/dist/dist.go#L31-L32).
	dist := cmp.Or(cfg.first("dist"), "dist")
	formats, problems := releaseFormats(cfg)
	for _, f := range formats {
		files = append(files, path.Join(dist, "*."+f))
	}
	dir, line, ok := releaseBundleDir(cfg)
	switch {
	case !ok:
		problems = append(problems, parityGoreleaser+" runs no `gates mcpb-pack VERSION [DIST]`, so the bundle the "+
			"release publishes is unknown")
	case strings.Contains(dir, "{{"):
		problems = append(problems, fmt.Sprintf("%s:%d: mcpb-pack's DIST holds a template action, which this check "+
			"does not render; write the directory out", parityGoreleaser, line))
	default:
		files = append(files, path.Join(dir, "*.mcpb"))
		problems = append(problems, releaseUploadProblems(cfg, path.Join(dir, bundleFile(releaseVersion)))...)
	}
	// Without a name_template goreleaser names the checksum file for the
	// project and version, which a fixed subject-path does not match.
	if checksum := cfg.first("checksum.name_template"); checksum != "" {
		files = append(files, path.Join(dist, checksum))
	} else {
		problems = append(problems, "checksum.name_template is empty, so nothing holds the subject-path to the "+
			"file every other artifact is verified through")
	}
	return files, problems
}

// releaseFormats is every format the config's archives pack, in file
// order, each once: each archive's formats and its overrides'. goreleaser
// v2.18.1 packs an archive that names none, and a config with no archive,
// as tar.gz
// (https://github.com/goreleaser/goreleaser/blob/v2.18.1/internal/pipe/archive/archive.go#L57-L68).
// The singular format keys, which it still reads, and the binary format,
// which packs no archive, are refused by name: this check reads neither.
func releaseFormats(cfg goreleaserFile) (formats, problems []string) {
	archives := map[int]bool{}
	named := map[int]bool{}
	for _, v := range cfg.values {
		if !strings.HasPrefix(v.keys, "archives.") || len(v.items) == 0 {
			continue
		}
		archives[v.items[0]] = true
		switch v.keys {
		case "archives.format", "archives.format_overrides.format":
			problems = append(problems, fmt.Sprintf("%s:%d: %s is the singular key, which this check does not read; write formats",
				parityGoreleaser, v.line, v.keys))
		case "archives.formats", "archives.format_overrides.formats":
			if v.keys == "archives.formats" {
				named[v.items[0]] = true
			}
			if v.value == "binary" {
				problems = append(problems, fmt.Sprintf("%s:%d: %s is binary, which packs no archive this check reads",
					parityGoreleaser, v.line, v.keys))
				continue
			}
			if !slices.Contains(formats, v.value) {
				formats = append(formats, v.value)
			}
		}
	}
	if len(archives) == 0 || len(named) < len(archives) {
		if !slices.Contains(formats, "tar.gz") {
			formats = append(formats, "tar.gz")
		}
	}
	return formats, problems
}

// releaseBundleDir is the directory the release's mcpb-pack call writes
// the bundle into, its DIST or mcpb-pack's default, and the call's line.
// It reads the command as written, each action as goreleaserPlaceholder:
// a run renders an action inside a word as nothing, which would hide a
// DIST that is a template.
func releaseBundleDir(cfg goreleaserFile) (dir string, line int, ok bool) {
	for _, v := range cfg.values {
		if goreleaserKeys[v.keys] != goreleaserLine {
			continue
		}
		command := goreleaserTemplate.ReplaceAllStringFunc(strings.ReplaceAll(v.value, "\n", " "), func(action string) string {
			if goreleaserControl.MatchString(action) {
				return ""
			}
			return goreleaserPlaceholder
		})
		for _, c := range parityGateCalls("", shellSplit(command)) {
			if c.name != "mcpb-pack" {
				continue
			}
			if len(c.args) > 1 {
				return path.Clean(c.args[1]), v.line, true
			}
			return mcpbPackDist, v.line, true
		}
	}
	return "", 0, false
}

// releaseVersion stands in for the tag's version in the bundle's name,
// which the upload and checksum globs are matched against.
const releaseVersion = "X.Y.Z"

// releaseUploadProblems holds release.extra_files, which uploads the
// bundle, and checksum.extra_files, which puts it in the signed checksum
// file, to a glob that matches bundle. The bundle is the one file the
// release adds to goreleaser's own, so any other glob is refused, and so
// is a template, which this check does not render.
func releaseUploadProblems(cfg goreleaserFile, bundle string) []string {
	var problems []string
	for _, at := range []struct{ key, missed string }{
		{"release.extra_files", "so the release does not upload it"},
		{"checksum.extra_files", "so the checksum file does not cover it and it ships unsigned"},
	} {
		globs := cfg.at(at.key + ".glob")
		if !slices.ContainsFunc(globs, func(g goreleaserValue) bool { return releaseGlobMatches(g.value, bundle) }) {
			problems = append(problems, fmt.Sprintf("%s's %s has no glob that matches %s, the bundle mcpb-pack writes, %s",
				parityGoreleaser, at.key, bundle, at.missed))
		}
		for _, g := range globs {
			switch {
			case strings.Contains(g.value, "{{"):
				problems = append(problems, fmt.Sprintf("%s:%d: %s glob %s holds a template action, which this check "+
					"does not render; write the glob out", parityGoreleaser, g.line, at.key, g.value))
			case !releaseGlobMatches(g.value, bundle):
				problems = append(problems, fmt.Sprintf("%s:%d: %s glob %s matches no %s, and the bundle is the only "+
					"file the release adds to goreleaser's own", parityGoreleaser, g.line, at.key, g.value, bundle))
			}
		}
	}
	return problems
}

// releaseGlobMatches reports whether a goreleaser file glob, such as
// ./dist/*.mcpb, matches file, a slash-separated path.
func releaseGlobMatches(glob, file string) bool {
	ok, _ := path.Match(path.Clean(glob), file)
	return ok
}

// releaseSubjectProblems holds the attestation's subject-path, a comma-
// or newline-separated list, to name each of files as an entry of its
// own. A space-separated value is one literal glob.
func releaseSubjectProblems(subjects string, files []string) []string {
	var problems []string
	parts := strings.FieldsFunc(subjects, func(r rune) bool { return r == ',' || r == '\n' })
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	if slices.ContainsFunc(parts, func(p string) bool { return strings.ContainsAny(p, " \t") }) {
		problems = append(problems, fmt.Sprintf("subject-path %q separates with a space, which is read as one "+
			"glob; separate with commas or newlines", subjects))
	}
	// actions/attest skips a glob that matches nothing while another
	// matches, so dist/unused/*.mcpb attests no bundle and
	// dist/*.mcpb.sigstore.json only a signature
	// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L150).
	for _, want := range files {
		if !slices.Contains(parts, want) {
			problems = append(problems, fmt.Sprintf("subject-path %q does not name %s, so it carries no attestation",
				subjects, want))
		}
	}
	return problems
}

// releaseFixtureFiles is what releaseFiles makes of a config that names
// checksums.txt and no dist directory.
var releaseFixtureFiles = []string{"dist/*.tar.gz", "dist/*.zip", "dist/*.mcpb", "dist/checksums.txt"}

func TestReleaseSubjectProblems(t *testing.T) {
	for name, subjects := range map[string]string{
		"commas":                        "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt",
		"newlines":                      "dist/*.tar.gz\ndist/*.zip\ndist/checksums.txt\ndist/*.mcpb",
		"commas and a space after each": "dist/*.tar.gz, dist/*.zip, dist/checksums.txt, dist/*.mcpb",
	} {
		if p := releaseSubjectProblems(subjects, releaseFixtureFiles); len(p) != 0 {
			t.Errorf("%s: subjects refused: %v", name, p)
		}
	}
	want := []string{
		`subject-path "dist/*.tar.gz,dist/*.zip.sigstore.json,dist/*.mcpb.sigstore.json,dist/checksums.txt" does not name dist/*.zip, so it carries no attestation`,
		`subject-path "dist/*.tar.gz,dist/*.zip.sigstore.json,dist/*.mcpb.sigstore.json,dist/checksums.txt" does not name dist/*.mcpb, so it carries no attestation`,
	}
	if p := releaseSubjectProblems("dist/*.tar.gz,dist/*.zip.sigstore.json,dist/*.mcpb.sigstore.json,dist/checksums.txt", releaseFixtureFiles); !slices.Equal(p, want) {
		t.Errorf("problems\n got %q\nwant %q", p, want)
	}
}

// releaseCfg is a goreleaser config that packs tar.gz and, on Windows,
// zip, packs the bundle into dist, names its checksum file, and uploads
// and checksums the bundle.
var releaseCfg = textLines(
	"universal_binaries:",
	"  - id: universal",
	"    hooks:",
	"      post: go run ./scripts/gates mcpb-pack {{ .Version }} dist",
	"archives:",
	"  - ids: [server]",
	"    formats: [tar.gz]",
	"    format_overrides:",
	"      - goos: windows",
	"        formats: [zip]",
	"release:",
	"  name_template: x",
	"  extra_files:",
	"    - glob: ./dist/*.mcpb",
	"checksum:",
	"  # the file the signature covers",
	"  name_template: 'checksums.txt' # fixed",
	"  extra_files:",
	"    - glob: ./dist/*.mcpb",
)

// releaseStep is the step that runs goreleaser release.
const releaseStep = "      - uses: goreleaser/goreleaser-action@x\n        with:\n          args: release --clean\n"

// releaseFlow is release.yml with a goreleaser job granted perms, running
// goreleaser release and then steps.
func releaseFlow(perms, steps string) workflowFile {
	return parseWorkflow("release.yml", "jobs:\n  goreleaser:\n    runs-on: x\n"+perms+"    steps:\n"+releaseStep+steps)
}

// releaseGrants is a goreleaser job's permissions that let it attest.
const releaseGrants = "    permissions:\n      contents: write\n      id-token: write\n      attestations: write\n"

// releaseAttest is an attest step with with.
func releaseAttest(with string) string {
	return "      - uses: actions/attest-build-provenance@x\n" + with
}

// releaseSubjects is an attest step's with: naming subjects on one line.
func releaseSubjects(subjects string) string {
	return releaseAttest("        with:\n          subject-path: " + subjects + "\n")
}

// The step is read from the workflow as written: a literal block is a list
// and a folded one is one line of space-separated globs. Each file is read
// from the config: the formats its archives pack, the directory mcpb-pack
// writes the bundle to, and the checksum file's name.
func TestReleaseAttestProblems(t *testing.T) {
	const all = "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt"
	cfg := func(old, new string) string {
		if !strings.Contains(releaseCfg, old) {
			t.Fatalf("the config does not say %q", old)
		}
		return strings.ReplaceAll(releaseCfg, old, new)
	}
	missing := func(subjects string, files ...string) []string {
		var out []string
		for _, f := range files {
			out = append(out, fmt.Sprintf("subject-path %q does not name %s, so it carries no attestation", subjects, f))
		}
		return out
	}
	for _, tc := range []struct {
		name string
		w    workflowFile
		cfg  string
		want []string
	}{
		{"a literal block", releaseFlow(releaseGrants, releaseAttest("        with:\n          subject-path: |\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n")), releaseCfg, nil},
		{"a folded block", releaseFlow(releaseGrants, releaseAttest("        with:\n          subject-path: >\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n")), releaseCfg, append([]string{
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" separates with a space, which is read as one glob; separate with commas or newlines`},
			missing("dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt", "dist/*.tar.gz", "dist/*.zip", "dist/*.mcpb", "dist/checksums.txt")...)},
		// The runner reads an input's name in upper case.
		{"subject-path in another case", releaseFlow(releaseGrants, releaseAttest("        with:\n          Subject-Path: "+all+"\n")), releaseCfg, nil},
		{"another checksum file", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("'checksums.txt' # fixed", "'{{ .ProjectName }}_checksums.txt'"),
			missing(all, "dist/{{ .ProjectName }}_checksums.txt")},
		{"no checksum name, only another block's", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.zip,dist/*.mcpb")),
			cfg("  name_template: 'checksums.txt' # fixed\n", "  algorithm: sha256\n"), []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a checksum name only under another key of checksum", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.zip,dist/*.mcpb")),
			cfg("  name_template: 'checksums.txt' # fixed\n  extra_files:\n    - glob: ./dist/*.mcpb\n",
				"  extra_files:\n    - glob: ./dist/*.mcpb\n      name_template: other.txt\n"), []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a tab before each comment", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("checksum:\n  # the file the signature covers\n  name_template: 'checksums.txt' # fixed\n",
				"checksum:\t# signed\n  name_template: checksums.txt\t# fixed\n"), nil},
		// actions/attest skips a glob that matches nothing while another
		// matches, so a bundle in another folder carries no provenance.
		{"the bundle in another folder", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.zip,dist/unused/*.mcpb,dist/checksums.txt")),
			releaseCfg, missing("dist/*.tar.gz,dist/*.zip,dist/unused/*.mcpb,dist/checksums.txt", "dist/*.mcpb")},
		{"the checksum file in another folder", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.zip,dist/*.mcpb,out/checksums.txt")),
			releaseCfg, missing("dist/*.tar.gz,dist/*.zip,dist/*.mcpb,out/checksums.txt", "dist/checksums.txt")},
		{"another dist directory", releaseFlow(releaseGrants, releaseSubjects(all)), "dist: ./out # built here\n" + releaseCfg,
			missing(all, "out/*.tar.gz", "out/*.zip", "out/checksums.txt")},
		{"the files in another dist directory, the bundle packed there too", releaseFlow(releaseGrants, releaseSubjects("out/*.tar.gz,out/*.zip,out/*.mcpb,out/checksums.txt")),
			"dist: out\n" + strings.ReplaceAll(cfg("{{ .Version }} dist", "{{ .Version }} out"), "./dist/", "./out/"), nil},
		{"a dist key with no value, and dist: inside another value", releaseFlow(releaseGrants, releaseSubjects(all)),
			"dist: # goreleaser's default\n" + cfg("release:\n  name_template: x\n", "release:\n  footer: |\n    dist: out\n"), nil},
		// The archives' formats are the config's, not a list of the usual ones.
		{"the formats the archives pack", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("    formats: [tar.gz]\n", "    formats: [tar.xz, tar.gz]\n"), missing(all, "dist/*.tar.xz")},
		{"an override's format", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("        formats: [zip]\n", "        formats: [7z]\n"), missing(all, "dist/*.7z")},
		{"an archive that names no format, which goreleaser packs as tar.gz", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.mcpb,dist/checksums.txt")),
			cfg("    formats: [tar.gz]\n    format_overrides:\n      - goos: windows\n        formats: [zip]\n", ""), nil},
		{"no archive, which goreleaser packs as tar.gz", releaseFlow(releaseGrants, releaseSubjects("dist/*.tar.gz,dist/*.mcpb,dist/checksums.txt")),
			cfg("archives:\n  - ids: [server]\n    formats: [tar.gz]\n    format_overrides:\n      - goos: windows\n        formats: [zip]\n", ""), nil},
		{"a second archive that names no format", releaseFlow(releaseGrants, releaseSubjects("dist/*.zip,dist/*.mcpb,dist/checksums.txt")),
			cfg("    formats: [tar.gz]\n    format_overrides:\n      - goos: windows\n        formats: [zip]\n", "    formats: [zip]\n  - ids: [other]\n"),
			missing("dist/*.zip,dist/*.mcpb,dist/checksums.txt", "dist/*.tar.gz")},
		{"the singular format keys and the binary format", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("    formats: [tar.gz]\n    format_overrides:\n      - goos: windows\n        formats: [zip]\n",
				"    format: tar.gz\n    format_overrides:\n      - goos: windows\n        format: zip\n      - goos: linux\n        formats: [binary]\n"), []string{
				".goreleaser.yaml:7: archives.format is the singular key, which this check does not read; write formats",
				".goreleaser.yaml:10: archives.format_overrides.format is the singular key, which this check does not read; write formats",
				".goreleaser.yaml:12: archives.format_overrides.formats is binary, which packs no archive this check reads",
			}},
		// The bundle is where the mcpb-pack hook writes it.
		{"the bundle packed elsewhere", releaseFlow(releaseGrants, releaseSubjects(all)),
			strings.ReplaceAll(cfg("{{ .Version }} dist", "{{ .Version }} dist/out"), "./dist/", "./dist/out/"), missing(all, "dist/out/*.mcpb")},
		// Reading a run would render the action inside the word as nothing,
		// and the bundle as packed into dist.
		{"the bundle packed into a directory that is a template", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("{{ .Version }} dist", "{{ .Version }} dist/{{ .Env.BUNDLE_DIR }}"), []string{
				".goreleaser.yaml:4: mcpb-pack's DIST holds a template action, which this check does not render; write the directory out",
			}},
		// The checksum covers only its own artifacts and the release
		// uploads only its own, so each names the bundle, which is the one
		// file the release adds.
		{"a bundle the release does not upload", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("  name_template: x\n  extra_files:\n    - glob: ./dist/*.mcpb\n", "  name_template: x\n"), []string{
				".goreleaser.yaml's release.extra_files has no glob that matches dist/google-sheets-mcp_X.Y.Z.mcpb, " +
					"the bundle mcpb-pack writes, so the release does not upload it",
			}},
		{"a bundle the checksum does not cover", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("# fixed\n  extra_files:\n    - glob: ./dist/*.mcpb\n", "# fixed\n"), []string{
				".goreleaser.yaml's checksum.extra_files has no glob that matches dist/google-sheets-mcp_X.Y.Z.mcpb, " +
					"the bundle mcpb-pack writes, so the checksum file does not cover it and it ships unsigned",
			}},
		{"an upload glob that matches no file", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("    - glob: ./dist/*.mcpb\nchecksum:", "    - glob: ./dist/*.mcpb\n    - glob: ./dist/*.txt\nchecksum:"), []string{
				".goreleaser.yaml:15: release.extra_files glob ./dist/*.txt matches no dist/google-sheets-mcp_X.Y.Z.mcpb, " +
					"and the bundle is the only file the release adds to goreleaser's own",
			}},
		{"an upload glob that is a template", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("    - glob: ./dist/*.mcpb\nchecksum:", "    - glob: ./dist/*.mcpb\n    - glob: ./dist/{{ .ProjectName }}_*.mcpb\nchecksum:"), []string{
				".goreleaser.yaml:15: release.extra_files glob ./dist/{{ .ProjectName }}_*.mcpb holds a template action, " +
					"which this check does not render; write the glob out",
			}},
		{"the bundle packed into mcpb-pack's default", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("{{ .Version }} dist", "{{ .Version }}"), nil},
		{"no bundle packed", releaseFlow(releaseGrants, releaseSubjects(all)),
			cfg("      post: go run ./scripts/gates mcpb-pack {{ .Version }} dist\n", "      post: true\n"), []string{
				".goreleaser.yaml runs no `gates mcpb-pack VERSION [DIST]`, so the bundle the release publishes is unknown",
			}},
		{"no subject-path", releaseFlow(releaseGrants, releaseAttest("")), releaseCfg, []string{"release.yml:12: the attestation names no subject-path"}},
		// Another job can be skipped while the release ships, and has no dist/.
		{"the attestation in another job", parseWorkflow("release.yml", "jobs:\n  goreleaser:\n    runs-on: x\n"+releaseGrants+
			"    steps:\n"+releaseStep+
			"  attest:\n    runs-on: x\n"+releaseGrants+"    steps:\n"+releaseSubjects(all)), releaseCfg, []string{
			"release.yml: job goreleaser runs goreleaser release and has no build-provenance attestation, so no file the release publishes carries one",
		}},
		{"no job that runs goreleaser release", parseWorkflow("release.yml", "jobs:\n  r:\n    steps:\n"+
			"      - uses: goreleaser/goreleaser-action@x\n        with:\n          args: check\n"+releaseSubjects(all)), releaseCfg, []string{
			"no job of release.yml runs goreleaser-action with args starting `release`, so no attest step is held to the files the release publishes",
		}},
		// A job's permissions replace the workflow's, and write-all grants
		// every scope.
		{"a job that cannot write attestations", releaseFlow("    permissions:\n      id-token: write\n", releaseSubjects(all)), releaseCfg, []string{
			"release.yml: job goreleaser attests without attestations: write, which actions/attest needs to sign and store the provenance; " +
				"grant it in the job's permissions, or the workflow's when the job sets none",
		}},
		{"a job whose permissions replace the workflow's", parseWorkflow("release.yml", "permissions:\n  id-token: write\n  attestations: write\n"+
			"jobs:\n  goreleaser:\n    permissions:\n      contents: write\n    steps:\n"+
			releaseStep+releaseSubjects(all)), releaseCfg, []string{
			"release.yml: job goreleaser attests without id-token: write, which actions/attest needs to sign and store the provenance; " +
				"grant it in the job's permissions, or the workflow's when the job sets none",
			"release.yml: job goreleaser attests without attestations: write, which actions/attest needs to sign and store the provenance; " +
				"grant it in the job's permissions, or the workflow's when the job sets none",
		}},
		{"the workflow's permissions where the job sets none", parseWorkflow("release.yml", "permissions: write-all\n"+
			"jobs:\n  goreleaser:\n    steps:\n"+
			releaseStep+releaseSubjects(all)), releaseCfg, nil},
		{"no permissions anywhere", releaseFlow("", releaseSubjects(all)), releaseCfg, []string{
			"release.yml: job goreleaser attests without id-token: write, which actions/attest needs to sign and store the provenance; " +
				"grant it in the job's permissions, or the workflow's when the job sets none",
			"release.yml: job goreleaser attests without attestations: write, which actions/attest needs to sign and store the provenance; " +
				"grant it in the job's permissions, or the workflow's when the job sets none",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseAttestProblems(tc.w, parseGoreleaser(tc.cfg)); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
