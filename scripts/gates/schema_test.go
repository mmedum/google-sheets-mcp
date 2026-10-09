package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

const testModule = "github.com/mmedum/google-sheets-mcp"

func TestABreakFailsWithoutAMajorBump(t *testing.T) {
	for _, tc := range []struct{ release, module string }{
		{release: "v1.5.1", module: testModule},
		{release: "v2.0.0", module: testModule + "/v2"},
		// Going backwards is not a bump either.
		{release: "v3.1.0", module: testModule + "/v2"},
	} {
		if verdict, err := judgeBreaks(2, tc.release, tc.module); err == nil {
			t.Errorf("%s against %s passed a break: %s", tc.module, tc.release, verdict)
		}
	}
}

func TestABreakPassesOnAMajorBump(t *testing.T) {
	for _, tc := range []struct{ release, module string }{
		{release: "v1.5.1", module: testModule + "/v2"},
		{release: "v0.4.0", module: testModule},
		{release: "v2.3.0", module: testModule + "/v10"},
	} {
		verdict, err := judgeBreaks(2, tc.release, tc.module)
		if err != nil || !strings.Contains(verdict, "accepted") {
			t.Errorf("%s against %s: %q (%v)", tc.module, tc.release, verdict, err)
		}
	}
}

func TestNoBreakAlwaysPasses(t *testing.T) {
	verdict, err := judgeBreaks(0, "v1.5.1", testModule)
	if err != nil || verdict != "no breaking changes" {
		t.Errorf("no break gave %q (%v)", verdict, err)
	}
}

func TestAnUnreadableReleaseFailsABreak(t *testing.T) {
	for _, release := range []string{"1.5.1", "vnext", "release-2"} {
		if _, err := judgeBreaks(1, release, testModule+"/v2"); err == nil {
			t.Errorf("release %q was read as a version", release)
		}
	}
}

// A tool kept by name breaks a caller when it loses a field it took or
// returned, at any depth, when an output may now be null, or when an
// input is newly required; adding fields does not.
func TestBrokenFields(t *testing.T) {
	was, err := readToolSurface([]byte(`{"version":"v3.0.2","tools":[
		{"name":"read_cell_comments","inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"},
		   "sheet":{"type":"string"},"range":{"type":"string"}},"required":["spreadsheet"]},
		 "outputSchema":{"type":"object","properties":{"summary":{"type":"string"},"total":{"type":"integer"},
		   "threads":{"type":"array","items":{"type":"object","properties":{"comment_id":{"type":"string"},"cell":{"type":"string"}}}},
		   "left_out":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}},
		{"name":"write_values","inputSchema":{"type":"object","properties":{"range":{"type":"string"},
		   "values":{"type":"array","items":{"type":"array","items":true}}}},
		 "outputSchema":{"type":"object","properties":{"updated_range":{"type":"string"},"note":{"type":"string"}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	now, err := readToolSurface([]byte(`{"version":"dev","tools":[
		{"name":"read_cell_comments","inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"},
		   "sheet":{"type":"string"},"include_resolved":{"type":"boolean"}},"required":["spreadsheet","sheet"]},
		 "outputSchema":{"type":"object","properties":{"summary":{"type":"string"},"total":{"type":["null","integer"]},
		   "threads":{"type":"array","items":{"type":"object","properties":{"comment_id":{"type":"string"}}}}}}},
		{"name":"write_values","inputSchema":{"type":"object","properties":{"range":{"type":"string"},
		   "values":{"type":"array","items":{"type":"array","items":true}},
		   "format":{"type":"object","properties":{"bold":{"type":"boolean"}},"required":["bold"]}}},
		 "outputSchema":{"type":"object","properties":{"updated_range":{"type":"string"},"note":{"type":"string"},"kept":{"type":"string"}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := brokenFields(was.fields, now.fields)
	want := []string{
		`read_cell_comments: input field range removed`,
		`read_cell_comments: output field left_out removed`,
		`read_cell_comments: output field threads[].cell removed`,
		`read_cell_comments: output field total changed type from "integer" to ["null","integer"]`,
		`read_cell_comments: input field sheet newly required`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// fieldsOf is the fields of a dump of one tool, read_range, with the
// input and output schemas given.
func fieldsOf(t *testing.T, input, output string) map[string]toolFields {
	t.Helper()
	s, err := readToolSurface([]byte(`{"tools":[{"name":"read_range","inputSchema":` + input + `,"outputSchema":` + output + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return s.fields
}

// A type change breaks a caller in one direction only: an input that
// takes fewer types, or an output that may return more. An input that
// takes more, or an output that returns fewer, breaks nobody. No type
// and the schema `true` are any type, and the schema `false` is none.
func TestATypeChangeBreaksOneWay(t *testing.T) {
	const empty = `{"type":"object"}`
	field := func(typ string) string {
		switch typ {
		case "":
			return `{"type":"object","properties":{"x":{}}}`
		case "true", "false":
			return `{"type":"object","properties":{"x":` + typ + `}}`
		}
		return `{"type":"object","properties":{"x":{"type":` + typ + `}}}`
	}
	for _, c := range []struct {
		side, was, now, want string
	}{
		{"input", `"boolean"`, `["null","boolean"]`, ""},
		{"input", `"integer"`, `"number"`, ""},
		{"input", `"string"`, ``, ""},
		{"input", `"string"`, `true`, ""},
		{"input", `false`, `"string"`, ""},
		{"input", `"number"`, `"integer"`, `read_range: input field x changed type from "number" to "integer"`},
		{"input", `["null","string"]`, `"string"`, `read_range: input field x changed type from ["null","string"] to "string"`},
		{"input", ``, `"string"`, `read_range: input field x changed type from any to "string"`},
		{"input", `true`, `"string"`, `read_range: input field x changed type from any to "string"`},
		{"input", `"string"`, `false`, `read_range: input field x changed type from "string" to false`},
		{"output", `["null","string"]`, `"string"`, ""},
		{"output", `"number"`, `"integer"`, ""},
		{"output", `true`, `"string"`, ""},
		{"output", `"string"`, `false`, ""},
		{"output", `"string"`, `["null","string"]`, `read_range: output field x changed type from "string" to ["null","string"]`},
		{"output", `"integer"`, `"number"`, `read_range: output field x changed type from "integer" to "number"`},
		{"output", `"string"`, ``, `read_range: output field x changed type from "string" to any`},
		{"output", `"string"`, `true`, `read_range: output field x changed type from "string" to any`},
		{"output", `false`, `"string"`, `read_range: output field x changed type from false to "string"`},
	} {
		was, now := fieldsOf(t, field(c.was), empty), fieldsOf(t, field(c.now), empty)
		if c.side == "output" {
			was, now = fieldsOf(t, empty, field(c.was)), fieldsOf(t, empty, field(c.now))
		}
		got := strings.Join(brokenFields(was, now), "\n")
		if got != c.want {
			t.Errorf("%s %s to %s: got %q, want %q", c.side, c.was, c.now, got, c.want)
		}
	}
}

// An output field a caller read as always there breaks it when it may
// now be missing, at any depth. One removed is reported once, as removed.
// An input no longer required breaks nobody.
func TestAnOutputNoLongerRequiredBreaks(t *testing.T) {
	was := fieldsOf(t, `{"type":"object","properties":{"spreadsheet":{"type":"string"}},"required":["spreadsheet"]}`,
		`{"type":"object","properties":{"range":{"type":"string"},"sheet":{"type":"string"},
		"next_range":{"type":"string"},"note":{"type":"string"},
		"rows":{"type":"array","items":{"type":"object","properties":{"row":{"type":"integer"}},"required":["row"]}}},
		"required":["range","sheet","next_range","rows"]}`)
	now := fieldsOf(t, `{"type":"object","properties":{"spreadsheet":{"type":"string"}}}`,
		`{"type":"object","properties":{"range":{"type":"string"},"sheet":{"type":"string"},
		"note":{"type":"string"},
		"rows":{"type":"array","items":{"type":"object","properties":{"row":{"type":"integer"}}}}},
		"required":["range","rows","note"]}`)
	got := brokenFields(was, now)
	want := []string{
		`read_range: output field next_range removed`,
		`read_range: output field rows[].row no longer required`,
		`read_range: output field sheet no longer required`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// An input value a caller sent breaks it when the field no longer takes
// it, in a list's elements too. A value added, or the list dropped so
// any value goes, breaks nobody.
func TestAnInputThatLosesAValueBreaks(t *testing.T) {
	const output = `{"type":"object"}`
	was := fieldsOf(t, `{"type":"object","properties":{
		"show":{"type":"string","enum":["values","formulas","both"]},
		"input":{"type":"string","enum":["typed","literal"]},
		"render":{"type":"string","enum":["formatted","unformatted"]},
		"dimensions":{"type":"array","items":{"type":"string","enum":["rows","columns"]}}}}`, output)
	now := fieldsOf(t, `{"type":"object","properties":{
		"show":{"type":"string","enum":["values","both"]},
		"input":{"type":"string","enum":["typed","raw","literal"]},
		"render":{"type":"string"},
		"dimensions":{"type":"array","items":{"type":"string","enum":["columns"]}}}}`, output)
	got := brokenFields(was, now)
	want := []string{
		`read_range: input field dimensions[] no longer takes "rows"`,
		`read_range: input field show no longer takes "formulas"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// Listed values that may or may not break a caller are reported, not
// failed: an output that may carry a value it did not, and an input
// newly limited to a list. An output that lists fewer, or newly lists
// its values, is neither, and a field removed is a break, said once.
func TestValuesThatMayBreakAreReported(t *testing.T) {
	was := fieldsOf(t, `{"type":"object","properties":{"show":{"type":"string"},
		"input":{"type":"string","enum":["typed","literal"]}}}`,
		`{"type":"object","properties":{"range":{"type":"string"},"kind":{"type":"string"},
		"dimension":{"type":"string","enum":["rows","columns"]},
		"status":{"type":"string","enum":["complete","partial"]},
		"value_type":{"type":"string","enum":["number","text"]},
		"source":{"type":"string","enum":["typed","formula"]}}}`)
	now := fieldsOf(t, `{"type":"object","properties":{"show":{"type":"string","enum":["values","both"]},
		"input":{"type":"string","enum":["typed","literal"]}}}`,
		`{"type":"object","properties":{"range":{"type":"string"},"kind":{"type":"string","enum":["a","b"]},
		"status":{"type":"string","enum":["complete","partial","truncated"]},
		"value_type":{"type":"string"},
		"source":{"type":"string","enum":["typed"]}}}`)
	if b := brokenFields(was, now); !slices.Equal(b, []string{"read_range: output field dimension removed"}) {
		t.Fatalf("got breaks %q, want only the field removed", b)
	}
	got := valueNotes(was, now)
	want := []string{
		`read_range: output field status may now be "truncated"`,
		`read_range: output field value_type may now be any value`,
		`read_range: input field show now takes only "values", "both"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// The diff names an output that may carry a value it did not list, and
// passes it.
func TestANewOutputValueIsNamedAndPasses(t *testing.T) {
	dump := func(version, values string) []byte {
		return []byte(`{"version":"` + version + `","tools":[{"name":"read_range","description":"d",
			"inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"}}},
			"outputSchema":{"type":"object","properties":{"status":{"type":"string","enum":[` + values + `]}}}}]}`)
	}
	var out bytes.Buffer
	err := schemaVerdict(&out, dump("v3.0.2", `"complete"`), dump("dev", `"complete","partial"`), changelogOpen, testModule+"/v3")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if want := `  ~ read_range: output field status may now be "partial"` + "\n"; !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not name the new value as %q", out.String(), want)
	}
}

// The dumps the verdict tests below start from: the released surface,
// and builds that add to it, lose a nested output field, and lose a
// resource.
const (
	releasedDump = `{"version":"v3.0.2","tools":[{"name":"read_cell_comments","description":"d",
		"inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"}}},
		"outputSchema":{"type":"object","properties":{"threads":{"type":"array","items":{"type":"object",
		"properties":{"cell":{"type":"string"}}}}}}}],
		"resourceTemplates":[{"name":"spreadsheet","uriTemplate":"gsheets://{spreadsheet}","mimeType":"text/plain"}]}`
	addedDump = `{"version":"dev","tools":[{"name":"read_cell_comments","description":"d",
		"inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"},"sheet":{"type":"string"}}},
		"outputSchema":{"type":"object","properties":{"threads":{"type":"array","items":{"type":"object",
		"properties":{"cell":{"type":"string"}}}}}}}],
		"resourceTemplates":[{"name":"spreadsheet","uriTemplate":"gsheets://{spreadsheet}","mimeType":"text/plain"}]}`
	lostDump = `{"version":"dev","tools":[{"name":"read_cell_comments","description":"d",
		"inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"}}},
		"outputSchema":{"type":"object","properties":{"threads":{"type":"array","items":{"type":"object",
		"properties":{}}}}}}],
		"resourceTemplates":[{"name":"spreadsheet","uriTemplate":"gsheets://{spreadsheet}","mimeType":"text/plain"}]}`
	noResourceDump = `{"version":"dev","tools":[{"name":"read_cell_comments","description":"d",
		"inputSchema":{"type":"object","properties":{"spreadsheet":{"type":"string"}}},
		"outputSchema":{"type":"object","properties":{"threads":{"type":"array","items":{"type":"object",
		"properties":{"cell":{"type":"string"}}}}}}}]}`
)

// The changelogs: a release with something unreleased above it, a
// release with nothing above it, and a newer release than the baseline.
const (
	changelogOpen   = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- x\n\n## [3.0.2] - 2026-10-01\n"
	changelogClosed = "# Changelog\n\n## [Unreleased]\n\n## [3.0.2] - 2026-10-01\n"
	changelogNewer  = "# Changelog\n\n## [Unreleased]\n\n- x\n\n## [3.1.0] - 2026-10-10\n\n## [3.0.2] - 2026-10-01\n"
	// changelogCut is the release commit for 3.1.0: nothing unreleased.
	changelogCut = "# Changelog\n\n## [Unreleased]\n\n## [3.1.0] - 2026-10-10\n\n- x\n\n## [3.0.2] - 2026-10-01\n"
)

// Every way the diff fails, and the ways it passes, against the baseline
// and the CHANGELOG.
func TestSchemaVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, base, cur, changelog, module, want string
	}{
		{"an addition", releasedDump, addedDump, changelogOpen, testModule + "/v3", ""},
		{"a nested output field lost", releasedDump, lostDump, changelogOpen, testModule + "/v3",
			"1 breaking schema change(s) since v3.0.2, and the module is still at major 3"},
		{"a resource lost", releasedDump, noResourceDump, changelogOpen, testModule + "/v3",
			"1 breaking schema change(s) since v3.0.2"},
		{"a break on the next major", releasedDump, lostDump, changelogOpen, testModule + "/v4", ""},
		{"no baseline for a release", "", addedDump, changelogOpen, testModule + "/v3",
			"CHANGELOG.md names v3.0.2 as released, but testdata/schema-baseline.json is missing"},
		{"no baseline before the first release", "", addedDump, "# Changelog\n\n## [Unreleased]\n\n- x\n", testModule, ""},
		{"a baseline older than the newest release", releasedDump, releasedDump, changelogNewer, testModule + "/v3",
			`the baseline is the "v3.0.2" surface, but CHANGELOG.md's newest release is v3.1.0`},
		{"nothing unreleased and a surface that differs", releasedDump, addedDump, changelogClosed, testModule + "/v3",
			"nothing is under [Unreleased], so this build is v3.0.2, and its surface differs from the baseline"},
		{"nothing unreleased and the same surface", releasedDump, strings.Replace(releasedDump, "v3.0.2", "dev", 1),
			changelogClosed, testModule + "/v3", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base []byte
			if tc.base != "" {
				base = []byte(tc.base)
			}
			err := schemaVerdict(io.Discard, base, []byte(tc.cur), tc.changelog, tc.module)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("failed: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// What `make schema-baseline` refuses to record, and what it records.
func TestBaselineVerdict(t *testing.T) {
	stamped := func(dump, version string) string { return strings.Replace(dump, `"dev"`, `"`+version+`"`, 1) }
	for _, tc := range []struct {
		name, base, cur, changelog, want string
	}{
		{"the release being cut, adding", releasedDump, stamped(addedDump, "v3.1.0"), changelogCut, ""},
		{"a build stamped as another version", releasedDump, addedDump, changelogCut,
			`the build is stamped "dev", but the release being cut is v3.1.0`},
		{"entries still unreleased", releasedDump, stamped(addedDump, "v3.1.0"), changelogNewer,
			"CHANGELOG.md still has entries under [Unreleased], so this is not v3.1.0's release commit"},
		{"a break on a minor release", releasedDump, stamped(lostDump, "v3.1.0"), changelogCut,
			"v3.1.0 breaks a caller of v3.0.2 in 1 way(s) above, and is not a new major version"},
		{"a break on a major release", releasedDump, stamped(lostDump, "v4.0.0"),
			strings.Replace(changelogCut, "3.1.0", "4.0.0", 1), ""},
		{"nothing released", "", addedDump, "# Changelog\n\n## [Unreleased]\n\n- x\n", "names no release yet"},
		{"the first baseline", "", stamped(addedDump, "v3.1.0"), changelogCut, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base []byte
			if tc.base != "" {
				base = []byte(tc.base)
			}
			err := baselineVerdict(io.Discard, base, []byte(tc.cur), tc.changelog)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// The baseline must be the newest release's surface, which is the
// CHANGELOG's first heading after [Unreleased].
func TestBaselineVersion(t *testing.T) {
	for changelog, want := range map[string]string{
		changelogOpen:  "v3.0.2",
		changelogNewer: "v3.1.0",
		"# Changelog\n\n## [3.1.0] - 2026-10-10\n\n- x\n\n## [3.0.2] - 2026-10-01\n": "v3.1.0",
		"# Changelog\n\n## [Unreleased]\n\n- first\n":                                "",
	} {
		if got := baselineVersion(changelog); got != want {
			t.Errorf("baselineVersion(%q) = %q, want %q", changelog, got, want)
		}
	}
}

// A break is recorded only as a new major version.
func TestNewMajor(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"v3.0.2", "v4.0.0", true},
		{"v3.0.2", "v3.1.0", false},
		{"v3.0.2", "v3.0.2", false},
		{"", "v4.0.0", false},
		{"dev", "v4.0.0", false},
	} {
		if got := newMajor(c.from, c.to); got != c.want {
			t.Errorf("newMajor(%q, %q) = %t, want %t", c.from, c.to, got, c.want)
		}
	}
}

// Two dumps of one surface are the same whatever their stamps and key
// order; a changed description is not.
func TestSameSurface(t *testing.T) {
	a := []byte(`{"version":"v3.0.2","sdk":"v1.8.0","tools":[{"name":"t","description":"d","inputSchema":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	b := []byte(`{"tools":[{"inputSchema":{"properties":{"a":{"type":"string"}},"type":"object"},"description":"d","name":"t"}],"version":"dev","sdk":"v1.9.0"}`)
	c := []byte(`{"version":"v3.0.2","tools":[{"name":"t","description":"changed","inputSchema":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	if !sameSurface(a, b) {
		t.Error("the same surface under other stamps and key order differs")
	}
	if sameSurface(a, c) {
		t.Error("a changed description is the same surface")
	}
}
