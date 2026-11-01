package main

import (
	"io"
	"slices"
	"strings"
	"testing"
)

// plain is a released surface: two tools and a resource template.
const plain = `{"version":"v1.0.0","tools":[
	{"name":"write_values","annotations":{"destructiveHint":false},
	 "inputSchema":{"type":"object","properties":{"range":{"type":"string"}}}},
	{"name":"read_range","annotations":{"readOnlyHint":true},
	 "inputSchema":{"type":"object","properties":{"range":{"type":"string"}}}}],
	"resourceTemplates":[{"uriTemplate":"gsheets://{id}","name":"spreadsheet"}]}`

// flipped is plain with write_values made destructive, and renamed is
// plain with write_values's range input renamed, which breaks it.
var (
	flipped = strings.Replace(plain, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	renamed = strings.Replace(plain, `"properties":{"range"`, `"properties":{"cells"`, 1)
)

// renameBreak is the break renamed makes of plain.
var renameBreak = []change{{"write_values", "write_values: input field range removed", "input range"}}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name":"spreadsheet"`, `"name":"sheet"`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate":"gsheets://{id}","name":"spreadsheet"}`, ``, 1)
	reworded := strings.Replace(plain, `"version":"v1.0.0",`, `"version":"v1.0.0","instructions":"new",`, 1)
	const (
		wantUnnamed      = "no footer names write_values"
		wantUnnamedBreak = "no BREAKING CHANGE: footer names write_values"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark write_values destructive", wantUnnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: write_values: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: read_range: destructiveHint true", wantUnnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: write_values_all: destructiveHint true", wantUnnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: read_range,\n  write_values: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", wantUnnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: write_values", wantUnnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: write_values", wantUnnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: write_values footer, which this is not.", wantUnnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: write_values", wantUnnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: write_values: hints\n\n    go run ./scripts/gates schema-ack", wantUnnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: write_values: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: write_values: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gsheets://{id}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: write_values: renamed", "no footer names gsheets://{id}"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", renamed, "Rename the field\n\nSCHEMA-CHANGE: write_values: range is cells", wantUnnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", renamed, "Rename the field\n\nBREAKING CHANGE: write_values: range is cells", ""},
		{"BREAKING-CHANGE: is its synonym", renamed, "Rename the field\n\nBREAKING-CHANGE: write_values: range is cells", ""},
		{"SCHEMA-CHANGE: in any case", flipped, "Mark it\n\nSchema-Change: write_values: destructiveHint true", ""},
		{"BREAKING CHANGE: only in uppercase", renamed, "Rename the field\n\nBreaking-Change: write_values: range is cells", wantUnnamedBreak},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gsheets://{id}: gone",
			"no BREAKING CHANGE: footer names gsheets://{id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// The fix a refusal names is the footer that fixes it: BREAKING CHANGE:
// for an unnamed break, SCHEMA-CHANGE: for the rest, each naming its
// items, and a message that ends with it passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	both := strings.Replace(renamed, `"name":"spreadsheet"`, `"name":"sheet"`, 1)
	for _, tc := range []struct{ name, built, wantFix string }{
		{"a change", flipped, "SCHEMA-CHANGE: write_values: what changed"},
		{"a break", renamed, "BREAKING CHANGE: write_values: what broke"},
		{"a break and a change", both, "BREAKING CHANGE: write_values: what broke\nSCHEMA-CHANGE: gsheets://{id}: what changed"},
		{"a break and a change to one tool", strings.Replace(renamed, `"destructiveHint":false`, `"destructiveHint":true`, 1),
			"BREAKING CHANGE: write_values: what broke"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Change it"}, "base")
			if err == nil {
				t.Fatal("checkAck passed with no footer")
			}
			_, fix, _ := strings.Cut(err.Error(), "\n\n")
			if fix != tc.wantFix {
				t.Errorf("the refusal's fix is %q, want %q", fix, tc.wantFix)
			}
			if err := checkAck(io.Discard, c, []string{"Change it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
		})
	}
}

// A name in backticks is taken whole, so a URI with a comma in it can be
// named; split on its comma, it names nothing.
func TestAFooterCanNameAURIWithACommaInBackticks(t *testing.T) {
	const tmpl = "gsheets://{id}{?range,sheet}"
	was := strings.Replace(plain, `"gsheets://{id}"`, `"`+tmpl+`"`, 1)
	now := strings.Replace(was, `"name":"spreadsheet"`, `"name":"sheet"`, 1)
	c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, now))
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := surfaceOf(t, plain)
	base := surfaceOf(t, strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1))
	head := surfaceOf(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	if want := []change{{item: "draft_tool", text: "draft_tool removed"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	var report strings.Builder
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: write_values: range is cells"
	if err := checkAck(&report, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
	want := "against the merge-base base: 0 added, 1 changed, 1 breaking\n" +
		"  ~ draft_tool removed\n" +
		"  ! write_values: input field range removed  BREAKING\n" +
		"the change since base is acknowledged\n"
	if report.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", report.String(), want)
	}
}

// A tool the base branch already broke since the release, in one field,
// does not make a pull request's break of another field, one no release
// shipped, a break of the release.
func TestABreakOfTheReleaseIsMatchedByItsField(t *testing.T) {
	withInputs := func(props string) string {
		return strings.Replace(plain, `"properties":{"range":{"type":"string"}}`, `"properties":{`+props+`}`, 1)
	}
	release := surfaceOf(t, withInputs(`"range":{"type":"string"},"note":{"type":"string"}`))
	base := surfaceOf(t, withInputs(`"range":{"type":"string"},"draft":{"type":"string"}`))
	head := surfaceOf(t, withInputs(`"range":{"type":"string"}`))
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want draft, which no release shipped, as a change", c.breaking)
	}
}

// A break of the release is matched by its field, not its text. Here the
// release takes a string, the merge-base widened that to an integer as
// well, and the head takes only an integer: the two breaks read
// differently, and both break the same tool.
func TestABreakOfTheReleaseIsNotMatchedByItsText(t *testing.T) {
	typed := func(typ string) toolSurface {
		return surfaceOf(t, strings.Replace(plain, `"properties":{"range":{"type":"string"}}`,
			`"properties":{"range":{"type":`+typ+`}}`, 1))
	}
	release, base, head := typed(`"string"`), typed(`["integer","string"]`), typed(`"integer"`)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 1 || c.breaking[0].item != "write_values" {
		t.Errorf("breaking = %v, want write_values's narrowed input kept as a break of the release", c.breaking)
	}
}

// The release is the baseline the base branch records. Before the first
// release there is none, and nothing breaks one.
func TestReleasedBreaksReadTheBaseBranchBaseline(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initRepo(t, dir)
	gitIn(t, dir, "commit", "--quiet", "--allow-empty", "-m", "Start")
	head := surfaceOf(t, renamed)

	breaks, err := releasedBreaks("HEAD", head)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no baseline = %v, %v; want none", breaks, err)
	}

	put(t, dir, baselineFile, plain)
	gitIn(t, dir, "add", baselineFile)
	gitIn(t, dir, "commit", "--quiet", "-m", "Release")
	// The working tree's baseline is a branch's, not the release's.
	put(t, dir, baselineFile, `{"tools":[]}`)
	breaks, err = releasedBreaks("HEAD", head)
	if err != nil || !slices.Equal(breaks, renameBreak) {
		t.Errorf("releasedBreaks = %v, %v; want %v", breaks, err, renameBreak)
	}
}
