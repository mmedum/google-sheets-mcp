package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// baselineFile is the tool surface of the CHANGELOG's newest release,
// recorded in that release's commit by `make schema-baseline`.
const baselineFile = "testdata/schema-baseline.json"

// changelogPath is where the releases are named.
const changelogPath = "CHANGELOG.md"

// schemaDiff compares this build's tool surface with the baseline, and
// fails on a change that breaks a caller: a tool or resource removed, an
// input or output field removed or retyped at any depth, or an input
// newly required.
//
// One invalid schema is not one broken tool: a client validating against
// draft 2020-12 rejects the whole request, so five bad schemas killed
// every one of forty-four tools in a shipped server's session, reporting
// only an array index that named no server. So the dump runs on every
// build.
//
// The baseline is a committed file, never a tag. The diff used to build
// the last tag in a worktree and compare top-level fields only, so a
// lost `threads[].cell` passed.
func schemaDiff(bin string) error {
	raw, current, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	if len(current.Tools) == 0 {
		return fmt.Errorf("this build registered no tools; the diff is not looking at a server")
	}
	// The artifact CI keeps is the same bytes the diff was computed
	// from, rather than a second run of the binary.
	if err := os.WriteFile("schemas.json", raw, 0o644); err != nil { //nolint:gosec // CI's artifact, read by everyone
		return err
	}
	changelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	module, err := modulePath()
	if err != nil {
		return err
	}
	base, err := os.ReadFile(baselineFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", baselineFile, err)
	}
	return schemaVerdict(os.Stdout, base, raw, string(changelog), module)
}

// schemaVerdict holds the built surface to the baseline, and the
// baseline to the CHANGELOG. base is nil where there is no baseline.
//
// The baseline must be the newest release's: an older one protects an
// older surface, so whatever shipped since could be dropped and nothing
// would say. A break passes only while go.mod's major version is above
// the baseline's, which is how a new major is built before it ships.
// With nothing under [Unreleased] the build is that release, so its
// surface must be the baseline's exactly, which proves a release commit
// recorded the baseline rather than relabeling it.
func schemaVerdict(w io.Writer, base, cur []byte, changelog, module string) error {
	now, err := readToolSurface(cur)
	if err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	want := baselineVersion(changelog)
	if base == nil {
		if want != "" {
			return fmt.Errorf("%s names %s as released, but %s is missing; record it in that release's commit "+
				"with `make schema-baseline VERSION=%s`", changelogPath, want, baselineFile, want)
		}
		// The first release is exactly this state, and it is not a
		// failure: there is nothing yet to be compatible with.
		_, _ = fmt.Fprintf(w, "no baseline yet; %d tools and resources in the current surface\n", len(now.entries))
		return nil
	}
	was, err := readToolSurface(base)
	if err != nil {
		return fmt.Errorf("parse %s: %w", baselineFile, err)
	}

	var problems []string
	if want != "" && was.version != want {
		problems = append(problems, fmt.Sprintf("the baseline is the %q surface, but %s's newest release is %s; "+
			"record it in that release's commit with `make schema-baseline VERSION=%s`",
			was.version, changelogPath, want, want))
	}
	breaking := compareSurfaces(w, was, now)
	verdict, err := judgeBreaks(len(breaking), was.version, module)
	if err != nil {
		problems = append(problems, err.Error())
	} else {
		_, _ = fmt.Fprintln(w, verdict)
	}
	if len(problems) == 0 && want != "" && sectionFor(changelog, "Unreleased") == "" && !sameSurface(base, cur) {
		problems = append(problems, fmt.Sprintf("nothing is under [Unreleased], so this build is %s, and its "+
			"surface differs from the baseline. In %s's release commit, run `make schema-baseline VERSION=%s`; "+
			"otherwise, say what changed under [Unreleased]", want, want, want))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// judgeBreaks decides whether breaking changes may ship.
//
// A break ships only with a major bump: the module path's major version,
// which Go carries as a /vN suffix from v2, must be above the
// baseline's release. Anything else fails.
func judgeBreaks(breaks int, release, module string) (string, error) {
	if breaks == 0 {
		return "no breaking changes", nil
	}
	from, err := tagMajor(release)
	if err != nil {
		return "", err
	}
	to := moduleMajor(module)
	if to > from {
		return fmt.Sprintf("%d breaking change(s) accepted: the module is at major %d and %s was major %d",
			breaks, to, release, from), nil
	}
	return "", fmt.Errorf("%d breaking schema change(s) since %s, and the module is still at major %d; "+
		"a break needs the next major version (/v%d in go.mod)", breaks, release, to, from+1)
}

// toolSurface is one schema dump, read for the diff.
type toolSurface struct {
	version string
	// entries is every tool and resource, keyed by name or by URI
	// template, with what a person should look at when it changes.
	entries map[string]string
	fields  map[string]toolFields
}

// dumpShape is the part of `--dump-schemas` the diff reads.
type dumpShape struct {
	Version string `json:"version"`
	Tools   []struct {
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		InputSchema  json.RawMessage `json:"inputSchema"`
		OutputSchema json.RawMessage `json:"outputSchema"`
	} `json:"tools"`
	ResourceTemplates []struct {
		Name        string `json:"name"`
		URITemplate string `json:"uriTemplate"`
		MIMEType    string `json:"mimeType"`
		Description string `json:"description"`
	} `json:"resourceTemplates"`
}

// readToolSurface reads one dump for the diff.
func readToolSurface(data []byte) (toolSurface, error) {
	var d dumpShape
	if err := json.Unmarshal(data, &d); err != nil {
		return toolSurface{}, err
	}
	s := toolSurface{version: d.Version, entries: map[string]string{}, fields: map[string]toolFields{}}
	for _, t := range d.Tools {
		s.entries[t.Name] = t.Description + "\x00" + canonicalJSON(t.InputSchema) + "\x00" + canonicalJSON(t.OutputSchema)
		f := toolFields{inputs: map[string]string{}, outputs: map[string]string{}, required: map[string]bool{}}
		for _, side := range []struct {
			raw      json.RawMessage
			types    map[string]string
			required map[string]bool
		}{{t.InputSchema, f.inputs, f.required}, {t.OutputSchema, f.outputs, map[string]bool{}}} {
			if len(side.raw) == 0 {
				continue
			}
			var n schemaNode
			if err := json.Unmarshal(side.raw, &n); err != nil {
				return toolSurface{}, fmt.Errorf("%s: %w", t.Name, err)
			}
			n.walk("", side.types, side.required)
		}
		s.fields[t.Name] = f
	}
	// Resources share the map, keyed by the template a client asks for.
	// A resource whose template changed breaks a client exactly as a
	// renamed tool does.
	for _, r := range d.ResourceTemplates {
		s.entries[resourcePrefix+r.URITemplate] = r.Name + "\x00" + r.MIMEType + "\x00" + r.Description
	}
	return s, nil
}

// resourcePrefix tells a resource from a tool in the surface's map.
const resourcePrefix = "resource "

// canonicalJSON is a schema with its keys in one order, so two dumps of
// one schema compare equal however they were indented.
func canonicalJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// compareSurfaces prints what changed from was to now and returns what
// breaks a caller.
func compareSurfaces(w io.Writer, was, now toolSurface) []string {
	var removed, changed, added []string
	for _, name := range slices.Sorted(maps.Keys(was.entries)) {
		cur, still := now.entries[name]
		switch {
		case !still:
			removed = append(removed, name)
		case cur != was.entries[name]:
			changed = append(changed, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(now.entries)) {
		if _, had := was.entries[name]; !had {
			added = append(added, name)
		}
	}
	_, _ = fmt.Fprintf(w, "against %s, the %s surface: %d added, %d changed, %d removed\n",
		baselineFile, was.version, len(added), len(changed), len(removed))
	for _, n := range added {
		_, _ = fmt.Fprintf(w, "  + %s\n", n)
	}
	for _, n := range changed {
		_, _ = fmt.Fprintf(w, "  ~ %s (description or schema changed)\n", n)
	}
	for _, n := range removed {
		_, _ = fmt.Fprintf(w, "  - %s  BREAKING\n", n)
	}
	fields := brokenFields(was.fields, now.fields)
	for _, b := range fields {
		_, _ = fmt.Fprintf(w, "  ! %s  BREAKING\n", b)
	}
	return append(removed, fields...)
}

// sameSurface reports whether two dumps publish the same tools and
// resources, field for field. The version and SDK stamps are not part of
// it, and neither is key order.
func sameSurface(a, b []byte) bool {
	type published struct {
		Tools             any `json:"tools"`
		ResourceTemplates any `json:"resourceTemplates"`
	}
	var x, y published
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// toolFields is what a caller relies on in each tool, at any depth: the
// fields it may send and their types, the ones it must send, and the
// ones it reads back and their types.
//
// A path names a field the way a caller reaches it: `source.sheet`, and
// `threads[].cell` for a field of each element of a list.
type toolFields struct {
	inputs, outputs map[string]string
	required        map[string]bool
}

// schemaNode is the part of a JSON Schema the diff walks. The dump
// carries no $ref, anyOf or oneOf, so properties and items reach every
// field.
type schemaNode struct {
	Type       json.RawMessage        `json:"type"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
	Required   []string               `json:"required"`
}

// UnmarshalJSON takes a boolean schema too: `true`, which a list of
// any values has as its items, is any value and has no fields to walk.
// Its type reads as the boolean, so a change to or from it is seen.
func (n *schemaNode) UnmarshalJSON(data []byte) error {
	if b := bytes.TrimSpace(data); string(b) == "true" || string(b) == "false" {
		n.Type = json.RawMessage(b)
		return nil
	}
	type plain schemaNode
	return json.Unmarshal(data, (*plain)(n))
}

// walk records the type of every field under n, and which are required.
func (n *schemaNode) walk(prefix string, types map[string]string, required map[string]bool) {
	if n == nil {
		return
	}
	for _, r := range n.Required {
		required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		types[path] = child.typeName()
		child.walk(path, types, required)
	}
	if n.Items != nil {
		types[prefix+"[]"] = n.Items.typeName()
		n.Items.walk(prefix+"[]", types, required)
	}
}

// typeName is the type as the schema spells it, so `"string"` and
// `["null","string"]` differ: a field that may now be null breaks a
// caller that read it as always there.
func (n *schemaNode) typeName() string {
	if n == nil {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, n.Type) != nil {
		return string(n.Type)
	}
	return buf.String()
}

func fieldPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// parentPath is the field a path sits in, or "" at the top.
func parentPath(path string) string {
	if strings.HasSuffix(path, "[]") {
		return strings.TrimSuffix(path, "[]")
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

// brokenFields lists what a tool kept by name lost: an input or output
// field removed or retyped, or an input newly required. A field inside
// one that was removed is not listed again. A removed tool is the
// caller's to report.
func brokenFields(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(prev)) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			was, now map[string]string
		}{{"input", was.inputs, now.inputs}, {"output", was.outputs, now.outputs}} {
			for _, f := range slices.Sorted(maps.Keys(side.was)) {
				t, still := side.now[f]
				_, parentKept := side.now[parentPath(f)]
				switch {
				case !still && (parentPath(f) == "" || parentKept):
					out = append(out, fmt.Sprintf("%s: %s field %s removed", name, side.what, f))
				case still && t != side.was[f]:
					out = append(out, fmt.Sprintf("%s: %s field %s changed type from %s to %s", name, side.what, f, side.was[f], t))
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range slices.Sorted(maps.Keys(now.required)) {
			parent := parentPath(f)
			_, parentWas := was.inputs[parent]
			if !was.required[f] && (parent == "" || parentWas) {
				out = append(out, fmt.Sprintf("%s: input field %s newly required", name, f))
			}
		}
	}
	return out
}

// baselineVersion is the release whose surface the baseline must hold:
// the CHANGELOG's newest heading, or empty before the first release.
//
// Between releases that heading is the last tag. In a release commit it
// is the release being cut, and the baseline is recorded in that same
// commit. Recording it after the tag instead would fail every branch
// from the moment the tag is pushed until a second change lands.
func baselineVersion(changelog string) string {
	for _, line := range strings.Split(changelog, "\n") {
		rest, ok := strings.CutPrefix(line, "## [")
		if !ok {
			continue
		}
		v, _, ok := strings.Cut(rest, "]")
		if ok && v != "Unreleased" {
			return "v" + v
		}
	}
	return ""
}

// writeBaseline records the surface of the release being cut as the
// baseline. The release commit runs it, so the baseline lands with the
// CHANGELOG heading that names it.
func writeBaseline(bin string) error {
	raw, _, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	changelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	base, err := os.ReadFile(baselineFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", baselineFile, err)
	}
	if err := baselineVerdict(os.Stdout, base, raw, string(changelog)); err != nil {
		return err
	}
	if err := writeThrough(baselineFile, raw); err != nil {
		return fmt.Errorf("write %s: %w", baselineFile, err)
	}
	fmt.Printf("%s is now the %s surface\n", baselineFile, baselineVersion(string(changelog)))
	return nil
}

// baselineVerdict says whether a build may be recorded as the baseline.
// base is the current baseline, nil where there is none.
//
// The build must be stamped as the release the CHANGELOG is cutting,
// with nothing left under [Unreleased], so the baseline cannot hold a
// surface under another release's name. And
// it is compared with the current baseline first: a change that breaks
// a caller is refused unless the release is a new major version, which
// is what a break has to ship as. Overwriting first would leave the diff
// comparing the release with itself.
func baselineVerdict(w io.Writer, base, cur []byte, changelog string) error {
	now, err := readToolSurface(cur)
	if err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	want := baselineVersion(changelog)
	if want == "" {
		return fmt.Errorf("%s names no release yet, so there is no surface to record", changelogPath)
	}
	if now.version != want {
		return fmt.Errorf("the build is stamped %q, but the release being cut is %s; "+
			"run `make schema-baseline VERSION=%s`", now.version, want, want)
	}
	// A release commit has moved every entry under the release's heading.
	// With some still unreleased, this build is not the release, and
	// recording it would put a newer surface under the older name.
	if sectionFor(changelog, "Unreleased") != "" {
		return fmt.Errorf("%s still has entries under [Unreleased], so this is not %s's release commit; "+
			"move them under its heading first", changelogPath, want)
	}
	if base == nil {
		return nil
	}
	was, err := readToolSurface(base)
	if err != nil {
		return fmt.Errorf("parse %s: %w", baselineFile, err)
	}
	if breaking := compareSurfaces(w, was, now); len(breaking) > 0 && !newMajor(was.version, want) {
		msg := fmt.Sprintf("%s breaks a caller of %s in %d way(s) above, and is not a new major version; %s is unchanged",
			want, was.version, len(breaking), baselineFile)
		if was.version == want {
			msg += fmt.Sprintf(". It already holds %s from an earlier run; restore the last release's "+
				"baseline from its tag, then run this again", want)
		}
		return errors.New(msg)
	}
	return nil
}

// newMajor reports whether release to is a later major version than
// release from. An unreadable version is not.
func newMajor(from, to string) bool {
	major := func(v string) int {
		head, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), ".")
		n, err := strconv.Atoi(head)
		if err != nil {
			return -1
		}
		return n
	}
	f, t := major(from), major(to)
	return f >= 0 && t > f
}

// writeThrough writes path through a temporary file beside it and a
// rename, so a failed write leaves whatever was there whole rather than
// truncated or half-written.
func writeThrough(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // a committed file, read by everyone
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
