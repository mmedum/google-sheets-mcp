//go:build live

package main

import (
	redactpkg "github.com/mmedum/google-sheets-mcp/v4/internal/redact"
)

// Redaction, in one place, for the same reason the live driver has it in
// one place — and with more to do here.
//
// A driver prints its own calls. This prints a *model's*: the prompts it
// was given, the arguments it chose, and the text of every result it
// read. All of it comes from a spreadsheet this harness created and
// filled, so the values are invented; what is not invented is the
// spreadsheet id in every argument, the link in every card, and the
// account address Drive puts on a search result. Those must never reach
// a transcript, and an eval transcript is meant to be read and quoted.
//
// Registration covers what this harness knows in advance. redactpkg.Line
// masks what it does not, which is the half a run can leak without any
// test noticing.
var registry redactpkg.Registry

// reg registers a value to replace wherever it is printed.
func reg(value, placeholder string) { registry.Add(value, placeholder) }

// redactLine is the only way this program reaches the terminal.
func redactLine(s string) string { return registry.Line(s) }
