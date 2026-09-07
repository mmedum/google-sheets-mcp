package render

import (
	"fmt"
	"strconv"
	"strings"
)

// Card is the spreadsheet card: what get_spreadsheet knows before any
// cell is read. It is a view model rather than an API type, so this
// package stays below the client and can be tested without one.
type Card struct {
	Title    string
	ID       string
	Link     string
	Locale   string
	TimeZone string
	Recalc   string
	Owner    string
	Modified string

	Sheets      []CardSheet
	NamedRanges []NamedItem
	Tables      []NamedItem
	Protected   []NamedItem
	FilterViews []NamedItem
}

// CardSheet is one tab as the card reports it.
type CardSheet struct {
	Title      string
	ID         int
	Index      int
	Type       string
	Rows       int
	Cols       int
	FrozenRows int
	FrozenCols int
	Hidden     bool
	TabColor   string
	// Holds names the objects on the sheet — a table, a chart, a pivot
	// table, a data source — which no read of the values would reveal.
	Holds []string
	// Charts are those charts by name. A count says a sheet has three
	// charts; the names say which one a caller means.
	Charts []string
}

// NamedItem is a named thing with an A1 range.
type NamedItem struct {
	Name   string
	Range  string
	Detail string
}

// Spreadsheet renders the card.
//
// Sheet titles come first and in full, because every later call names
// one and none of them may be guessed: Google names the first sheet in
// the account's language, so a server that assumes Sheet1 fails on
// somebody else's spreadsheet with a parse error that says nothing
// useful.
func Spreadsheet(c Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", c.Title)
	fmt.Fprintf(&b, "id: %s\n", c.ID)
	if c.Link != "" {
		fmt.Fprintf(&b, "link: %s\n", c.Link)
	}
	var meta []string
	if c.Locale != "" {
		meta = append(meta, "locale "+c.Locale)
	}
	if c.TimeZone != "" {
		meta = append(meta, "time zone "+c.TimeZone)
	}
	if c.Recalc != "" {
		meta = append(meta, "recalculates "+strings.ToLower(strings.ReplaceAll(c.Recalc, "_", " ")))
	}
	if c.Owner != "" {
		meta = append(meta, "owner "+c.Owner)
	}
	if c.Modified != "" {
		meta = append(meta, "modified "+c.Modified)
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(meta, "; "))
	}

	fmt.Fprintf(&b, "\n%d sheet(s):\n", len(c.Sheets))
	for _, s := range c.Sheets {
		fmt.Fprintf(&b, "  %s (id %d", s.Title, s.ID)
		if s.Type != "" && s.Type != "GRID" {
			fmt.Fprintf(&b, ", %s", strings.ToLower(s.Type))
		}
		if s.Rows > 0 || s.Cols > 0 {
			fmt.Fprintf(&b, ", %d rows x %d columns", s.Rows, s.Cols)
		}
		if s.FrozenRows > 0 {
			fmt.Fprintf(&b, ", %s frozen", Plural(s.FrozenRows, "row"))
		}
		if s.FrozenCols > 0 {
			fmt.Fprintf(&b, ", %s frozen", Plural(s.FrozenCols, "column"))
		}
		if s.Hidden {
			b.WriteString(", hidden")
		}
		if s.TabColor != "" {
			fmt.Fprintf(&b, ", tab %s", s.TabColor)
		}
		if len(s.Holds) > 0 {
			fmt.Fprintf(&b, ", holds %s", strings.Join(s.Holds, ", "))
		}
		b.WriteString(")\n")
		// The charts by name, indented under the sheet that holds them.
		// A count is what the line above already gives; the names are
		// what a caller needs to say which chart they mean.
		if len(s.Charts) > 0 {
			fmt.Fprintf(&b, "    charts: %s\n", strings.Join(quoteAll(s.Charts), ", "))
		}
	}

	section(&b, "named ranges", c.NamedRanges)
	section(&b, "tables", c.Tables)
	section(&b, "protected ranges", c.Protected)
	section(&b, "filter views", c.FilterViews)
	return b.String()
}

// quoteAll quotes each of a list, for a line that names things somebody
// will type back.
func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strconv.Quote(s))
	}
	return out
}

// Plural is the one place this project pluralises a count, so the card
// and everything built beside it cannot end up saying it two ways.
func Plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// ProtectionState is how a protected range is described to the model.
// Both the card and the footer under a grid say it, and they have to say
// it the same way.
func ProtectionState(canEdit, warningOnly bool) string {
	switch {
	case warningOnly:
		return "warning only"
	case canEdit:
		return "you may edit it"
	}
	return "you may not edit it"
}

func section(b *strings.Builder, label string, items []NamedItem) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", label)
	for _, it := range items {
		fmt.Fprintf(b, "  %s", it.Name)
		if it.Range != "" {
			fmt.Fprintf(b, " -> %s", it.Range)
		}
		if it.Detail != "" {
			fmt.Fprintf(b, " (%s)", it.Detail)
		}
		b.WriteByte('\n')
	}
}
