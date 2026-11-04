//go:build live

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	redactpkg "github.com/mmedum/google-sheets-mcp/v4/internal/redact"
)

// Redaction lives here and only here.
//
// The driver reads only a spreadsheet it created and filled itself, so
// the values in a transcript are its own and this is a second line of
// defense rather than the control. It matters where it sits: scrubbing
// on the read path lets a step parse a placeholder out of one result and
// feed it back into the next call, which the API rejects. Every step
// here reads the untouched value and only the printing is redacted.
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
// The second half covers what registration cannot. Registration knows
// only the values this driver created; everything Drive returns about
// them, such as the owner's address or the folder id, would otherwise
// reach a transcript that has to be safe to paste into a commit message.
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
	// Through line, not around it. A sec that printed directly would
	// make the allowlist a list of functions permitted to reach the
	// terminal rather than a claim that everything reaching it is
	// redacted, and would bless a sec(someSheetTitle) by name. One
	// printer is the whole invariant.
	line("")
	line("== %s %s", title, strings.Repeat("=", max(0, 60-len(title))))
}

func line(format string, args ...any) {
	fmt.Println(redact(fmt.Sprintf(format, args...)))
}
