//go:build live

package main

import (
	"sort"
	"strings"
	"sync"

	redactpkg "github.com/mmedum/google-sheets-mcp/internal/redact"
)

// Redaction, in one place, for the same reason the live driver has it in
// one place — and with more to do here.
//
// A driver prints its own calls. This prints a *model's*: the prompts it
// was given, the arguments it chose, and the text of every result it
// read. All of it comes from a spreadsheet this harness created and
// filled, so the values are invented; what is not invented is the
// spreadsheet id in every argument, the link in every card, and the
// account address Drive puts on a search result. Those are exactly what
// §9.1 says must not reach a transcript, and an eval transcript is
// meant to be read and quoted.
//
// Registration covers what this harness knows in advance. redactpkg.Line
// masks what it does not — which is the half a sibling project left out,
// and found by reading a run rather than by running one.
var (
	mu           sync.Mutex
	replacements = map[string]string{}
)

// reg registers a value to replace wherever it is printed.
func reg(value, placeholder string) {
	if value == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	replacements[value] = placeholder
}

// redactLine is the only way this program reaches the terminal.
func redactLine(s string) string {
	return redactpkg.Line(substitute(s))
}

// substitute replaces every registered value, longest first so a value
// that contains another is handled before it.
func substitute(s string) string {
	mu.Lock()
	keys := make([]string, 0, len(replacements))
	values := make(map[string]string, len(replacements))
	for k, v := range replacements {
		keys = append(keys, k)
		values[k] = v
	}
	mu.Unlock()
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		s = strings.ReplaceAll(s, k, values[k])
	}
	return s
}
