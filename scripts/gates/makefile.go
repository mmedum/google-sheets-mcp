package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// A reader for the parts of the Makefile the gates ask about: variables,
// targets, their prerequisites and their recipes. It is not make. It
// knows `NAME = v` with `?=`, `:=` or `+=`, after `export` or `override`,
// `target: prereqs`, and recipe lines starting with a TAB, read as make
// reads them. It refuses every other line by name, so a Makefile written
// another way fails rather than yielding nothing to check.

const makefilePath = "Makefile"

// makefile is a parsed Makefile.
type makefile struct {
	vars    map[string]string
	targets map[string]*makefileTarget
	// text is the raw file, for the rules about literal lines.
	text string
	// refused are the lines this reader does not read, each by name.
	refused []string
}

// makefileTarget is one target, all its rules merged.
type makefileTarget struct {
	prereqs []string
	recipe  []string
}

var (
	makefileVarLine    = regexp.MustCompile(`^(?:(?:export|override)[ \t]+)*([A-Za-z_][A-Za-z0-9_]*)[ \t]*([:?+]?=)[ \t]*(.*)$`)
	makefileTargetLine = regexp.MustCompile(`^([A-Za-z0-9_.\-]+)\s*:([^=].*)?$`)
	// makefileRuleHead is the targets of a rule of any shape, up to its
	// first colon.
	makefileRuleHead = regexp.MustCompile(`^([^:=#]*?)[ \t]*:`)
	makefileVarRef   = regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_]*)\)`)
)

// makefileReadFirst are the names GNU make tries before Makefile, in
// order, so a file by either name is the one `make` runs
// (https://www.gnu.org/software/make/manual/html_node/Makefile-Names.html).
var makefileReadFirst = []string{"GNUmakefile", "makefile"}

// readMakefile parses the repository's Makefile, refusing by name a file
// make reads in its place. A name is matched as the directory lists it:
// where the file system folds case, makefile is Makefile itself.
func readMakefile() (*makefile, error) {
	data, err := os.ReadFile(makefilePath)
	if err != nil {
		return nil, err
	}
	m := parseMakefile(string(data))
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	for _, name := range makefileReadFirst {
		if slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == name }) {
			m.refused = append(m.refused, fmt.Sprintf("%s: GNU make reads it before %s, so `make` runs a file "+
				"no gate reads; fold it into %s", name, makefilePath, makefilePath))
		}
	}
	return m, nil
}

// parseMakefile reads a Makefile as make does: a # starts a comment in an
// assignment, a later `=` or `:=` replaces a value, `?=` sets only a
// variable not yet set, and `+=` appends after a space
// (https://www.gnu.org/software/make/manual/html_node/Setting.html,
// https://www.gnu.org/software/make/manual/html_node/Appending.html). A
// target's rules merge their prerequisites, and the last recipe is the
// one that runs
// (https://www.gnu.org/software/make/manual/html_node/Multiple-Rules.html).
func parseMakefile(text string) *makefile {
	m := &makefile{vars: map[string]string{}, targets: map[string]*makefileTarget{}, text: text}
	var current *makefileTarget
	// fresh is whether the current rule has yet to give its first recipe
	// line, which replaces any recipe an earlier rule gave.
	fresh := false
	// start is the number of the first line of a continued one.
	pending, start := "", 0
	for i, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if pending == "" {
			start = i + 1
		} else {
			raw = pending + strings.TrimLeft(raw, " \t")
			pending = ""
		}
		if cont, ok := strings.CutSuffix(raw, "\\"); ok {
			pending = strings.TrimRight(cont, " \t") + " "
			continue
		}
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(raw, "\t"):
			// @, - and + tell make how to run a command and are not part of
			// it (https://www.gnu.org/software/make/manual/html_node/Recipe-Syntax.html).
			line = strings.TrimLeft(line, "@-+ \t")
			switch {
			case line == "" || strings.HasPrefix(line, "#"):
			case current == nil:
				m.refuse(start, "%q is a recipe line outside any rule this reader reads", line)
			default:
				if fresh {
					current.recipe, fresh = nil, false
				}
				current.recipe = append(current.recipe, line)
			}
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			current, fresh = m.statement(start, line), true
		}
	}
	return m
}

// statement reads line n, an assignment or a rule, and returns the target
// whose recipe the lines after it give, or nil.
func (m *makefile) statement(n int, line string) *makefileTarget {
	if v := makefileVarLine.FindStringSubmatch(line); v != nil {
		m.assign(v[1], v[2], v[3])
		return nil
	}
	t := makefileTargetLine.FindStringSubmatch(line)
	if t == nil {
		m.refuse(n, "%q is %s, which this reader does not read", line, makefileUnread(line))
		return nil
	}
	name, rest := t[1], t[2]
	prereqs, _, _ := strings.Cut(rest, "#")
	kind := ""
	switch {
	case strings.HasPrefix(rest, ":"):
		kind = "a double-colon rule"
	case strings.Contains(prereqs, "="):
		kind = "a target-specific variable"
	case strings.Contains(prereqs, ";"):
		kind = "a recipe after a semicolon"
	case name == ".PHONY":
		return nil
	default:
		return m.rule(name, prereqs)
	}
	m.refuse(n, "%q is %s, which this reader does not read", line, kind)
	return nil
}

// makefileUnread names the kind of a line that is neither an assignment
// nor a rule this reader reads.
func makefileUnread(line string) string {
	head := makefileRuleHead.FindStringSubmatch(line)
	switch {
	case head == nil:
		return "a directive or an assignment"
	case strings.Contains(head[1], "$"):
		return "a rule whose target is a variable"
	case strings.Contains(head[1], "%"):
		return "a pattern rule"
	case strings.ContainsAny(head[1], " \t"):
		return "a rule with more than one target"
	}
	return "a rule whose target is not a plain name"
}

// assign sets a variable as make does.
func (m *makefile) assign(name, op, raw string) {
	value := strings.TrimSpace(makefileValue(raw))
	old, set := m.vars[name]
	switch {
	case op == "?=" && set:
		return
	case op == "+=" && old != "":
		value = old + " " + value
	}
	m.vars[name] = value
}

// rule merges one rule's prerequisites into its target's and returns the
// target.
func (m *makefile) rule(name, prereqs string) *makefileTarget {
	t := m.targets[name]
	if t == nil {
		t = &makefileTarget{}
		m.targets[name] = t
	}
	for _, p := range strings.Fields(prereqs) {
		if !slices.Contains(t.prereqs, p) {
			t.prereqs = append(t.prereqs, p)
		}
	}
	return t
}

func (m *makefile) refuse(n int, format string, a ...any) {
	m.refused = append(m.refused, fmt.Sprintf("%s:%d: %s", makefilePath, n, fmt.Sprintf(format, a...)))
}

// makefileValue is an assignment's value with its comment cut. A # starts
// the comment unless a backslash escapes it, and the run of backslashes
// before a # is halved, as GNU make 4.4.1 reads it
// (https://www.gnu.org/software/make/manual/html_node/Makefile-Contents.html).
func makefileValue(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		slashes := len(s[i:]) - len(strings.TrimLeft(s[i:], `\`))
		if i+slashes < len(s) && s[i+slashes] == '#' {
			b.WriteString(strings.Repeat(`\`, slashes/2))
			if slashes%2 == 0 {
				return b.String()
			}
			b.WriteByte('#')
			i += slashes
			continue
		}
		b.WriteString(s[i : i+max(slashes, 1)])
		i += max(slashes, 1) - 1
	}
	return b.String()
}

// makefileExpandDepth is how deep expand follows a variable naming
// another, so a variable that names itself ends.
const makefileExpandDepth = 10

// expand replaces $(NAME) with the variable's value, recursively, and $$
// with the $ make hands the shell. A name it does not know stays in place.
func (m *makefile) expand(s string) string {
	const dollar = "\x00"
	s = strings.ReplaceAll(s, "$$", dollar)
	for range makefileExpandDepth {
		next := makefileVarRef.ReplaceAllStringFunc(s, func(ref string) string {
			// ref is $(NAME).
			if v, ok := m.vars[ref[2:len(ref)-1]]; ok {
				return strings.ReplaceAll(v, "$$", dollar)
			}
			return ref
		})
		if next == s {
			break
		}
		s = next
	}
	return strings.ReplaceAll(s, dollar, "$")
}

// refs is every variable s names as $(NAME), and every variable their
// values name in turn.
func (m *makefile) refs(s string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(s string) {
		for _, ref := range makefileVarRef.FindAllStringSubmatch(s, -1) {
			if name := ref[1]; !seen[name] {
				seen[name] = true
				walk(m.vars[name])
			}
		}
	}
	walk(s)
	return seen
}

// closure is the targets and everything they depend on, transitively,
// sorted.
func (m *makefile) closure(names ...string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		if t, ok := m.targets[n]; ok {
			for _, p := range t.prereqs {
				walk(p)
			}
		}
	}
	for _, n := range names {
		walk(n)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// recipe is a target's own recipe lines, expanded.
func (m *makefile) recipe(name string) []string {
	t, ok := m.targets[name]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(t.recipe))
	for _, line := range t.recipe {
		out = append(out, m.expand(line))
	}
	return out
}
