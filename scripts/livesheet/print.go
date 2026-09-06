//go:build live

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	redactpkg "github.com/mmedum/google-sheets-mcp/internal/redact"
)

// Redaction lives here and only here.
//
// The driver reads only a spreadsheet it created and filled itself, so
// the values in a transcript are its own and this is a second line of
// defence rather than the control. It matters where it sits: a sibling
// project scrubbed on the read path, and a step then parsed a
// placeholder out of one result and fed it back into the next call,
// which the API rejected. Every step here reads the untouched value and
// only the printing is redacted.
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

// redact replaces every registered value, longest first so a value that
// contains another is handled before it, and then masks anything
// identifying that was never registered.
//
// The second half is the one that was missing. Registration only covers
// values this driver created and therefore knew in advance; everything
// Drive returned about them — the owner's address, the folder id — went
// through untouched, into the transcript §9.1 calls safe to paste into a
// commit message. Found by reading a run rather than by running one.
func redact(s string) string {
	return redactpkg.Line(substitute(s))
}

func substitute(s string) string {
	mu.Lock()
	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	values := make(map[string]string, len(replacements))
	for k, v := range replacements {
		values[k] = v
	}
	mu.Unlock()

	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		s = strings.ReplaceAll(s, k, values[k])
	}
	return s
}

func sec(title string) {
	// Through line, not around it. sec used to print directly, which
	// made the allowlist a list of functions permitted to reach the
	// terminal rather than a claim that everything reaching it is
	// redacted — and a later sec(someSheetTitle) would have been blessed
	// by name. One printer is the whole invariant.
	line("")
	line("== %s %s", title, strings.Repeat("=", max(0, 60-len(title))))
}

func line(format string, args ...any) {
	fmt.Println(redact(fmt.Sprintf(format, args...)))
}
