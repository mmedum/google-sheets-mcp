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
// fails on a change that breaks a caller, at any depth: a tool or
// resource removed; a field removed; an input that takes fewer types or
// loses a listed value; an output that may return another type or may be
// missing where it was required; or an input newly required. An output
// that may carry a value it did not list is named, not failed.
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
	module, err := modulePath(".")
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
		_, _ = fmt.Fprintf(w, "no baseline yet; %d items in the current surface\n", len(now.items))
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
	changes := compareSurfaces(was, now)
	changes.write(w, againstBaseline(was))
	verdict, err := judgeBreaks(len(changes.breaking), was.version, module)
	if err != nil {
		problems = append(problems, err.Error())
	} else {
		_, _ = fmt.Fprintln(w, verdict)
	}
	if len(problems) == 0 && want != "" && sectionFor(changelog, "Unreleased") == "" && !changes.empty() {
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
	fields  map[string]toolFields
	// items and rest are what dumpItems reads.
	items map[string]map[string]any
	rest  map[string]any
}

// stampKeys are the dump's build stamp. They change with every build and
// are not part of the surface; what an SDK bump changes on the wire shows
// up inside the tools.
var stampKeys = []string{"server", "version", "sdk"}

// dumpShape is the part of `--dump-schemas` the field walk reads.
type dumpShape struct {
	Version string `json:"version"`
	Tools   []struct {
		Name         string          `json:"name"`
		InputSchema  json.RawMessage `json:"inputSchema"`
		OutputSchema json.RawMessage `json:"outputSchema"`
	} `json:"tools"`
}

// readToolSurface reads one dump for the diff.
func readToolSurface(data []byte) (toolSurface, error) {
	var d dumpShape
	if err := json.Unmarshal(data, &d); err != nil {
		return toolSurface{}, err
	}
	items, rest, err := dumpItems(data)
	if err != nil {
		return toolSurface{}, err
	}
	s := toolSurface{version: d.Version, fields: map[string]toolFields{}, items: items, rest: rest}
	for _, t := range d.Tools {
		f := toolFields{inputs: newFieldSet(), outputs: newFieldSet()}
		for _, side := range []struct {
			raw  json.RawMessage
			into fieldSet
		}{{t.InputSchema, f.inputs}, {t.OutputSchema, f.outputs}} {
			if len(side.raw) == 0 {
				continue
			}
			var n schemaNode
			if err := json.Unmarshal(side.raw, &n); err != nil {
				return toolSurface{}, fmt.Errorf("%s: %w", t.Name, err)
			}
			n.walk("", side.into)
		}
		s.fields[t.Name] = f
	}
	return s, nil
}

// dumpItems reads every item of every list in a dump, keyed by itemKey,
// and every other key but the build stamp. A list with an item that has
// no identity is compared as one value, since a position is no identity.
func dumpItems(data []byte) (map[string]map[string]any, map[string]any, error) {
	var whole map[string]any
	if err := json.Unmarshal(data, &whole); err != nil {
		return nil, nil, err
	}
	for _, k := range stampKeys {
		delete(whole, k)
	}
	items := map[string]map[string]any{}
	rest := map[string]any{}
	for key, v := range whole {
		list, isList := v.([]any)
		keyed := map[string]map[string]any{}
		for _, item := range list {
			obj, _ := item.(map[string]any)
			id := itemKey(key, obj)
			if id == "" {
				keyed = nil
				break
			}
			if _, twice := keyed[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			keyed[id] = obj
		}
		if !isList || keyed == nil {
			rest[key] = v
			continue
		}
		for id, obj := range keyed {
			if _, twice := items[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			items[id] = obj
		}
	}
	return items, rest, nil
}

// itemKey is how an item of a dumped list is named in the diff: a tool
// by its name, a resource by the template or URI a client asks for, and
// an item of any other list by the list and its name. Empty when the
// item has none. A resource whose template changed breaks a client
// exactly as a renamed tool does.
func itemKey(list string, item map[string]any) string {
	id := ""
	for _, k := range []string{"uriTemplate", "uri", "name"} {
		if v, ok := item[k].(string); ok && v != "" {
			id = v
			break
		}
	}
	switch {
	case id == "":
		return ""
	case list == "tools":
		return id
	case list == "resourceTemplates" || list == "resources":
		return resourcePrefix + id
	}
	return list + " " + id
}

// againstBaseline is how the diff names the baseline it compares with.
func againstBaseline(was toolSurface) string {
	return fmt.Sprintf("%s, the %s surface", baselineFile, was.version)
}

// resourcePrefix tells a resource from a tool in the surface's map.
const resourcePrefix = "resource "

// itemName is what a footer names an item by, from its key in the
// surface. A tool's key is its name; every other key is a word for its
// list, a space and the name, as itemKey builds it, and a tool's name
// has no space.
func itemName(key string) string {
	if _, name, ok := strings.Cut(key, " "); ok {
		return name
	}
	return key
}

// A change is one difference between two dumps: the item it is to, which
// is what a footer names, and how the report shows it. The item is a
// tool's name, a resource's URI or template, a list item's name, or a
// top-level key such as instructions. field is what a break is matched
// by across two comparisons: on a tool still there the field it breaks,
// such as "input range", and on a removed item its whole key, list
// included, so a removed prompt and a tool of one name differ.
type change struct {
	item, text, field string
}

// surfaceChanges is what differs between two surfaces. breaking is what
// breaks a caller; changed is every other difference, an annotation or a
// _meta mark included, named by the part that moved. notes are values a
// kept field lists that a person should look at.
type surfaceChanges struct {
	added, changed, breaking []change
	notes                    []string
}

func (c surfaceChanges) empty() bool {
	return len(c.added) == 0 && len(c.changed) == 0 && len(c.breaking) == 0
}

// compareSurfaces is what changed from was to now. An item gone breaks a
// caller, and so does a tool still there that broke at any depth
// (brokenFields). A tool that broke still lists every part that moved,
// its schemas included: the break names only the fields it broke, and an
// output added beside it is a change a reviewer has to see.
func compareSurfaces(was, now toolSurface) surfaceChanges {
	var c surfaceChanges
	var removed []change
	fields := brokenFields(was.fields, now.fields)
	for _, key := range sortedUnion(was.items, now.items) {
		before, inWas := was.items[key]
		after, inNow := now.items[key]
		name := itemName(key)
		switch {
		case !inNow:
			removed = append(removed, change{item: name, text: key + " removed", field: key})
		case !inWas:
			c.added = append(c.added, change{item: name, text: key})
		default:
			if parts := changedParts(before, after); len(parts) > 0 {
				c.changed = append(c.changed, change{item: name, text: key + " (" + strings.Join(parts, ", ") + ")"})
			}
		}
	}
	for _, part := range changedParts(was.rest, now.rest) {
		top, _, _ := strings.Cut(part, ".")
		c.changed = append(c.changed, change{item: top, text: part})
	}
	c.breaking = slices.Concat(removed, fields)
	c.notes = valueNotes(was.fields, now.fields)
	return c
}

// write prints the changes to w, under a header naming what they are
// against.
func (c surfaceChanges) write(w io.Writer, against string) {
	_, _ = fmt.Fprintf(w, "against %s: %d added, %d changed, %d breaking\n", against, len(c.added), len(c.changed), len(c.breaking))
	for _, n := range c.added {
		_, _ = fmt.Fprintf(w, "  + %s\n", n.text)
	}
	for _, n := range c.changed {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n.text)
	}
	for _, n := range c.notes {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n)
	}
	for _, b := range c.breaking {
		_, _ = fmt.Fprintf(w, "  ! %s  BREAKING\n", b.text)
	}
}

// changedParts names what differs between two dumped objects, one level
// deep, so a flipped hint reads as annotations.destructiveHint.
func changedParts(o, n map[string]any) []string {
	var parts []string
	for _, k := range sortedUnion(o, n) {
		if reflect.DeepEqual(o[k], n[k]) {
			continue
		}
		om, oMap := o[k].(map[string]any)
		nm, nMap := n[k].(map[string]any)
		if !oMap || !nMap {
			parts = append(parts, k)
			continue
		}
		for _, sub := range sortedUnion(om, nm) {
			if !reflect.DeepEqual(om[sub], nm[sub]) {
				parts = append(parts, k+"."+sub)
			}
		}
	}
	return parts
}

// sortedUnion is every key of a and b, sorted.
func sortedUnion[V any](a, b map[string]V) []string {
	keys := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// toolFields is what a caller relies on in each tool, at any depth: the
// fields it may send, and the ones it reads back.
//
// A path names a field the way a caller reaches it: `source.sheet`, and
// `threads[].cell` for a field of each element of a list.
type toolFields struct {
	inputs, outputs fieldSet
}

// fieldSet is one side of a tool: every field by its path, and which of
// them are required.
type fieldSet struct {
	fields   map[string]field
	required map[string]bool
}

// field is one field's type as the schema spells it, "" for no type,
// and the values it is limited to, nil when it is not.
type field struct {
	typ  string
	enum []string
}

func newFieldSet() fieldSet {
	return fieldSet{fields: map[string]field{}, required: map[string]bool{}}
}

// schemaNode is the part of a JSON Schema the diff walks. The dump
// carries no $ref, anyOf or oneOf, so properties and items reach every
// field.
type schemaNode struct {
	Type       json.RawMessage        `json:"type"`
	Enum       []json.RawMessage      `json:"enum"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
	Required   []string               `json:"required"`
}

// UnmarshalJSON takes a boolean schema too: `true`, which a list of
// any values has as its items, is any value and has no fields to walk.
// Its type reads as the boolean, which typesCover reads as any type for
// `true` and as none for `false`.
func (n *schemaNode) UnmarshalJSON(data []byte) error {
	if b := bytes.TrimSpace(data); string(b) == "true" || string(b) == "false" {
		n.Type = json.RawMessage(b)
		return nil
	}
	type plain schemaNode
	return json.Unmarshal(data, (*plain)(n))
}

// walk records every field under n, and which are required.
func (n *schemaNode) walk(prefix string, into fieldSet) {
	if n == nil {
		return
	}
	for _, r := range n.Required {
		into.required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		into.fields[path] = child.field()
		child.walk(path, into)
	}
	if n.Items != nil {
		into.fields[prefix+"[]"] = n.Items.field()
		n.Items.walk(prefix+"[]", into)
	}
}

// field is what the schema says of the field n describes.
func (n *schemaNode) field() field {
	if n == nil {
		return field{}
	}
	f := field{typ: compactJSON(n.Type)}
	for _, v := range n.Enum {
		f.enum = append(f.enum, compactJSON(v))
	}
	return f
}

// compactJSON is a schema value as the schema spells it, so `"string"`
// and `["null","string"]` differ.
func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
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

// brokenFields lists, as changes to each tool, what a tool kept by name
// lost: an input or output field removed, an input that takes fewer
// types or values, an output that may return more types or may now be
// missing, or an input newly required. A field inside one that was
// removed is not listed again. A removed tool is the caller's to report.
func brokenFields(prev, cur map[string]toolFields) []change {
	var out []change
	// broke records that name's field on side broke, in the way the
	// format says.
	broke := func(name, side, field, format string, args ...any) {
		out = append(out, change{name, name + ": " + side + " field " + field + " " + fmt.Sprintf(format, args...), side + " " + field})
	}
	for _, name := range slices.Sorted(maps.Keys(prev)) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			input    bool
			was, now fieldSet
		}{{"input", true, was.inputs, now.inputs}, {"output", false, was.outputs, now.outputs}} {
			for _, f := range slices.Sorted(maps.Keys(side.was.fields)) {
				w := side.was.fields[f]
				n, still := side.now.fields[f]
				if !still {
					if _, parentKept := side.now.fields[parentPath(f)]; parentPath(f) == "" || parentKept {
						broke(name, side.what, f, "removed")
					}
					continue
				}
				if typeBreaks(w.typ, n.typ, side.input) {
					broke(name, side.what, f, "changed type from %s to %s", typeWord(w.typ), typeWord(n.typ))
				}
				// A caller sent this value, and it is refused now.
				if side.input && n.enum != nil {
					for _, v := range w.enum {
						if !slices.Contains(n.enum, v) {
							broke(name, "input", f, "no longer takes %s", v)
						}
					}
				}
				// A caller read this field as always there.
				if !side.input && side.was.required[f] && !side.now.required[f] {
					broke(name, "output", f, "no longer required")
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range slices.Sorted(maps.Keys(now.inputs.required)) {
			parent := parentPath(f)
			_, parentWas := was.inputs.fields[parent]
			if !was.inputs.required[f] && (parent == "" || parentWas) {
				broke(name, "input", f, "newly required")
			}
		}
	}
	return out
}

// valueNotes lists what a person should look at in the values a kept
// field lists, where a caller may or may not be broken: an output that
// may carry a value it did not, which a caller may not handle, and an
// input newly limited to a list, which breaks a caller only if the
// server took other values before.
func valueNotes(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(prev)) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, f := range slices.Sorted(maps.Keys(was.outputs.fields)) {
			w := was.outputs.fields[f]
			n, still := now.outputs.fields[f]
			switch {
			case !still || w.enum == nil:
			case n.enum == nil:
				out = append(out, fmt.Sprintf("%s: output field %s may now be any value", name, f))
			default:
				for _, v := range n.enum {
					if !slices.Contains(w.enum, v) {
						out = append(out, fmt.Sprintf("%s: output field %s may now be %s", name, f, v))
					}
				}
			}
		}
		for _, f := range slices.Sorted(maps.Keys(was.inputs.fields)) {
			if n, still := now.inputs.fields[f]; still && was.inputs.fields[f].enum == nil && n.enum != nil {
				out = append(out, fmt.Sprintf("%s: input field %s now takes only %s", name, f, strings.Join(n.enum, ", ")))
			}
		}
	}
	return out
}

// typeBreaks reports whether a field's type change breaks a caller. An
// input may take more types than it did, and an output may return fewer;
// the other way round, a caller that sent or read the old type is
// broken. So `"string"` to `["null","string"]` breaks an output, where a
// caller read the field as always there, and not an input.
func typeBreaks(was, now string, input bool) bool {
	if was == now {
		return false
	}
	wide, narrow := now, was
	if !input {
		wide, narrow = was, now
	}
	return !typesCover(wide, narrow)
}

// typesCover reports whether every value narrow allows, wide allows too,
// as JSON Schema 2020-12 reads a type: a list allows any type in it, and
// an integer is a number. No type, or the schema `true`, is any value;
// the schema `false` lists no type, so it allows none.
func typesCover(wide, narrow string) bool {
	if anyType(wide) {
		return true
	}
	if anyType(narrow) {
		return false
	}
	allowed := map[string]bool{}
	for _, t := range typeList(wide) {
		allowed[t] = true
	}
	if allowed["number"] {
		allowed["integer"] = true
	}
	for _, t := range typeList(narrow) {
		if !allowed[t] {
			return false
		}
	}
	return true
}

// anyType reports whether a field takes any value: it has no type, or
// it is the boolean schema `true`.
func anyType(typ string) bool {
	return typ == "" || typ == "true"
}

// typeList is a schema's type as a list, whether it was written as one
// name or several. The schema `false` reads as no type.
func typeList(typ string) []string {
	var one string
	if json.Unmarshal([]byte(typ), &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal([]byte(typ), &many)
	return many
}

// typeWord is a type for a message: as the schema spells it, or any.
func typeWord(typ string) string {
	if anyType(typ) {
		return "any"
	}
	return typ
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
	changes := compareSurfaces(was, now)
	changes.write(w, againstBaseline(was))
	if breaking := changes.breaking; len(breaking) > 0 && !newMajor(was.version, want) {
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
