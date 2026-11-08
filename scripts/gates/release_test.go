package main

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// The release attests every file it publishes: the archives, the bundle
// and the checksum file goreleaser writes. actions/attest v4.2.1, which
// attest-build-provenance v4.2.2 runs, splits subject-path at commas and
// newlines and trims each glob
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L303),
// so a space-separated list is one glob that matches nothing.
func TestTheReleaseAttestsEveryFileItPublishes(t *testing.T) {
	atRepoRoot(t)
	flows, err := readWorkflows()
	if err != nil {
		t.Fatal(err)
	}
	release, ok := workflowNamed(flows, parityReleaseWorkflow)
	if !ok {
		t.Fatal("no " + parityReleaseWorkflow)
	}
	cfg, err := os.ReadFile(parityGoreleaser)
	if err != nil {
		t.Fatal(err)
	}
	if p := releaseAttestProblems(release, string(cfg)); p != nil {
		t.Errorf("the release: %q", p)
	}
}

// releaseAttestProblems holds a release workflow's build-provenance step
// to the files the goreleaser config publishes.
func releaseAttestProblems(release workflowFile, cfg string) []string {
	files, problems := releaseFiles(cfg)
	attested := false
	for _, s := range release.steps {
		if s.action() != "actions/attest-build-provenance" {
			continue
		}
		attested = true
		subjects, ok := s.with["subject-path"]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s:%d: the attestation names no subject-path", release.path, s.line))
			continue
		}
		problems = append(problems, releaseSubjectProblems(subjects.value, files)...)
	}
	if !attested {
		problems = append(problems, release.path+" runs no build-provenance attestation")
	}
	return problems
}

// releaseFiles is the subject-path entry for each file the release
// publishes, in goreleaser's dist directory: an archive in each format,
// the bundle, and checksum.name_template's file, which a fixed
// subject-path has to match.
func releaseFiles(cfg string) (files, problems []string) {
	dist := goreleaserDist(cfg)
	for _, f := range []string{"*.tar.gz", "*.zip", "*.mcpb"} {
		files = append(files, path.Join(dist, f))
	}
	// Without a name_template goreleaser names the checksum file for the
	// project and version, which a fixed subject-path does not match.
	if checksum := checksumNameTemplate(cfg); checksum != "" {
		files = append(files, path.Join(dist, checksum))
	} else {
		problems = append(problems, "checksum.name_template is empty, so nothing holds the subject-path to the "+
			"file every other artifact is verified through")
	}
	return files, problems
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

// goreleaserDist is the directory goreleaser writes the release into: the
// config's top-level dist, or dist where it sets none
// (https://github.com/goreleaser/goreleaser/blob/v2.18.1/internal/pipe/dist/dist.go#L31-L32).
func goreleaserDist(cfg string) string {
	for _, raw := range strings.Split(cfg, "\n") {
		if v, ok := strings.CutPrefix(raw, "dist:"); ok {
			if value, _ := workflowValue(strings.TrimSpace(v)); value != "" {
				return value
			}
		}
	}
	return "dist"
}

// checksumNameTemplate is a goreleaser config's top-level
// checksum.name_template, or "".
func checksumNameTemplate(cfg string) string {
	in, child := false, -1
	for _, raw := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		n := indent(raw)
		if n == 0 {
			key, _ := yamlCutComment(t)
			in, child = key == "checksum:", -1
			continue
		}
		if !in {
			continue
		}
		if child < 0 {
			child = n
		}
		if v, ok := strings.CutPrefix(t, "name_template:"); ok && n == child {
			value, _ := workflowValue(strings.TrimSpace(v))
			return value
		}
	}
	return ""
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

// The step is read from the workflow as written: a literal block is a list
// and a folded one is one line of space-separated globs.
func TestReleaseAttestProblems(t *testing.T) {
	const cfg = "release:\n  name_template: x\nchecksum:\n  # the file the signature covers\n  name_template: 'checksums.txt' # fixed\n"
	step := func(with string) workflowFile {
		return parseWorkflow("release.yml", "jobs:\n  r:\n    steps:\n      - uses: actions/attest-build-provenance@x\n"+with)
	}
	for _, tc := range []struct {
		name string
		w    workflowFile
		cfg  string
		want []string
	}{
		{"a literal block", step("        with:\n          subject-path: |\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n"), cfg, nil},
		{"a folded block", step("        with:\n          subject-path: >\n            dist/*.tar.gz\n            dist/*.zip\n            dist/*.mcpb\n            dist/checksums.txt\n"), cfg, []string{
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" separates with a space, which is read as one glob; separate with commas or newlines`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name dist/*.tar.gz, so it carries no attestation`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name dist/*.zip, so it carries no attestation`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name dist/*.mcpb, so it carries no attestation`,
			`subject-path "dist/*.tar.gz dist/*.zip dist/*.mcpb dist/checksums.txt" does not name dist/checksums.txt, so it carries no attestation`,
		}},
		{"another checksum file", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"),
			"checksum:\n  name_template: '{{ .ProjectName }}_checksums.txt'\n", []string{
				`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name dist/{{ .ProjectName }}_checksums.txt, so it carries no attestation`,
			}},
		{"no checksum name, only another block's", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb\n"),
			"release:\n  name_template: x\nchecksum:\n  algorithm: sha256\n", []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a checksum name only under another key of checksum", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb\n"),
			"checksum:\n  extra_files:\n    - glob: x\n      name_template: other.txt\n", []string{
				"checksum.name_template is empty, so nothing holds the subject-path to the file every other artifact is verified through",
			}},
		{"a tab before each comment", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"),
			"checksum:\t# signed\n  name_template: checksums.txt\t# fixed\n", nil},
		// actions/attest skips a glob that matches nothing while another
		// matches, so a bundle in another folder carries no provenance.
		{"the bundle in another folder", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/unused/*.mcpb,dist/checksums.txt\n"), cfg, []string{
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/unused/*.mcpb,dist/checksums.txt" does not name dist/*.mcpb, so it carries no attestation`,
		}},
		{"the checksum file in another folder", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,out/checksums.txt\n"), cfg, []string{
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,out/checksums.txt" does not name dist/checksums.txt, so it carries no attestation`,
		}},
		{"another dist directory", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"), "dist: ./out # built here\n" + cfg, []string{
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name out/*.tar.gz, so it carries no attestation`,
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name out/*.zip, so it carries no attestation`,
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name out/*.mcpb, so it carries no attestation`,
			`subject-path "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt" does not name out/checksums.txt, so it carries no attestation`,
		}},
		{"the files in another dist directory", step("        with:\n          subject-path: out/*.tar.gz,out/*.zip,out/*.mcpb,out/checksums.txt\n"), "dist: out\n" + cfg, nil},
		{"a dist key with no value, and dist: inside another value", step("        with:\n          subject-path: dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt\n"),
			"dist: # goreleaser's default\nrelease:\n  footer: |\n    dist: out\nchecksum:\n  name_template: checksums.txt\n", nil},
		{"no subject-path", step(""), cfg, []string{"release.yml:4: the attestation names no subject-path"}},
		{"no attestation", parseWorkflow("release.yml", "jobs:\n  r:\n    steps:\n      - run: true\n"), cfg,
			[]string{"release.yml runs no build-provenance attestation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseAttestProblems(tc.w, tc.cfg); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
