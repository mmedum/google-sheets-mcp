//go:build live

package main

import (
	"fmt"
	"strings"

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
var registry redactpkg.Registry

// reg registers a value to replace wherever it is printed.
func reg(value, placeholder string) { registry.Add(value, placeholder) }

// redact replaces every registered value and then masks anything
// identifying that was never registered.
//
// The second half covers what registration cannot. Registration knows
// only the values this driver created; everything Drive returns about
// them, such as the owner's address or the folder id, would otherwise
// reach a transcript that has to be safe to paste into a commit message.
func redact(s string) string { return registry.Line(s) }

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
