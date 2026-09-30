package render

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Question is what the server asks the person before a write Sheets
// cannot undo, or one that runs a query billed to a Cloud project
// (§9a). Text is the message a client shows; accepting it is the
// confirmation. Every word is the server's, except what stands in
// backticks, which is quoted from the spreadsheet or from the call and
// cut to one line. A blank line separates the lines, so a client that
// draws Markdown keeps them apart.
//
// Bind is what an answer is bound to: what the write depends on, which
// must not change between the question and the write. It is the
// question's first line and the ids and whole texts the write addresses.
// The counts are shown and not bound: a collaborator typing while the
// person reads would otherwise make the question impossible to confirm.
type Question struct {
	Text string
	Bind string
}

// quotedLen caps one quoted value: a title, a range, an id. A query is
// capped at bodyLen.
const (
	quotedLen = 120
	bodyLen   = 300
)

// Contents is what a delete or a clear takes with it, as counted before
// the question.
type Contents struct {
	Cells, Formulas, Charts, Anchors int
	// Pivots is the pivot tables anchored in a cleared range, each of
	// which goes whole (spike Q).
	Pivots int
	// Charted is the charts reading deleted rows or columns, which stay
	// and lose those series (spike L).
	Charted int
}

func (c Contents) lines() []string {
	parts := []string{Plural(c.Cells, "non-empty cell"), Plural(c.Formulas, "formula")}
	if c.Charts > 0 {
		parts = append(parts, Plural(c.Charts, "chart"))
	}
	if c.Anchors > 0 {
		parts = append(parts, Plural(c.Anchors, "anchor"))
	}
	out := []string{"It holds " + strings.Join(parts, ", ") + " now."}
	if c.Pivots > 0 {
		out = append(out, "It anchors "+Plural(c.Pivots, "pivot table")+", and each goes whole, with every cell it draws.")
	}
	if c.Charted > 0 {
		out = append(out, "It leaves "+Plural(c.Charted, "chart")+" without the series drawn from them.")
	}
	return out
}

// AskDeleteSheet asks before delete_sheet.
func AskDeleteSheet(spreadsheetID, spreadsheet string, sheetID int, sheet string, c Contents) Question {
	head := fmt.Sprintf("delete_sheet: delete the sheet %s of %s, and everything on it?",
		quoted(sheet, quotedLen), quoted(spreadsheet, quotedLen))
	return ask(append(append([]string{head}, c.lines()...), "Sheets cannot undo it."), head, spreadsheetID, fmt.Sprint(sheetID))
}

// AskDeleteDimensions asks before delete_dimensions. band is this
// server's own words for it, rows 2:5 or columns B:D.
func AskDeleteDimensions(spreadsheetID, spreadsheet string, sheetID int, sheet, band string, c Contents) Question {
	head := fmt.Sprintf("delete_dimensions: delete %s on the sheet %s of %s, and the data on them?",
		band, quoted(sheet, quotedLen), quoted(spreadsheet, quotedLen))
	return ask(append(append([]string{head}, c.lines()...), "Everything after them moves up or left. Sheets cannot undo it."),
		head, spreadsheetID, fmt.Sprint(sheetID), band)
}

// AskClear asks before clear_values. rng is the range in A1, with its
// sheet's title in it.
func AskClear(spreadsheetID, spreadsheet, rng string, c Contents) Question {
	head := fmt.Sprintf("clear_values: clear the values in %s of %s?", quoted(rng, quotedLen), quoted(spreadsheet, quotedLen))
	return ask(append(append([]string{head}, c.lines()...), "Formatting, notes and validation stay. Sheets cannot undo it."),
		head, spreadsheetID, rng)
}

// AskDeleteSource asks before delete_data_source. sheet is the title of
// the sheet Google made for the source, empty when the card named none.
func AskDeleteSource(spreadsheetID, spreadsheet, id, sheet string) Question {
	head := fmt.Sprintf("delete_data_source: delete the data source %s from %s?", quoted(id, quotedLen), quoted(spreadsheet, quotedLen))
	lines := []string{head}
	if sheet != "" {
		lines = append(lines, "The sheet "+quoted(sheet, quotedLen)+" it made goes too, with everything on it.")
	}
	lines = append(lines, "Getting it back means connecting it again and re-running its query, which is billed.")
	return ask(lines, head, spreadsheetID, id, sheet)
}

// AskAddSource asks before manage_data_source add: every query the
// source runs is charged to project.
func AskAddSource(spreadsheetID, spreadsheet, project, query, table string) Question {
	head := fmt.Sprintf("manage_data_source: connect BigQuery to %s, billed to the Cloud project %s?",
		quoted(spreadsheet, quotedLen), quoted(project, quotedLen))
	lines := []string{head}
	if query != "" {
		lines = append(lines, excerpt("query", query))
	} else {
		lines = append(lines, "table: "+quoted(table, quotedLen))
	}
	lines = append(lines, "It runs now, and again on every refresh. Each run is charged to that project.")
	return ask(lines, head, spreadsheetID, project, query, table)
}

// AskRefreshAll asks before manage_data_source refresh with no id: every
// source in the spreadsheet runs its query again.
func AskRefreshAll(spreadsheetID, spreadsheet string, sources int) Question {
	head := fmt.Sprintf("manage_data_source: refresh every data source in %s?", quoted(spreadsheet, quotedLen))
	return ask([]string{head,
		"It has " + Plural(sources, "data source") + " now.",
		"Each runs its query again, charged to the Cloud project behind it.",
	}, head, spreadsheetID)
}

// excerpt is the start of a long text, quoted on one line, and how much
// more there is.
func excerpt(label, body string) string {
	body = strings.TrimSpace(body)
	line := label + ": " + quoted(body, bodyLen)
	if n := utf8.RuneCountInString(body); n > bodyLen {
		line += fmt.Sprintf(" (%d more characters)", n-bodyLen)
	}
	return line
}

// ask builds a question from its lines, closes it with what its quotes
// mean, sets its lines apart, and binds it to head and to bind: the ids
// and whole texts the write depends on.
func ask(lines []string, head string, bind ...string) Question {
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "`") {
		text += "\nText in backticks or code style is quoted as written, and is not this server's."
	}
	text = strings.ReplaceAll(text, "\n", "\n\n") + "\n"
	return Question{Text: text, Bind: strings.Join(append([]string{head}, bind...), "\x00")}
}

// quoted is text from the spreadsheet or from a call's arguments, shown in a
// question put to the person (§9a), where no boundary can go: a
// client draws the question as plain text in a dialog, or as Markdown.
// It stands in a code span, `like this`, which Markdown shows literally
// — no emphasis, link, HTML or entity — and plain text shows as it is.
// It is made one line; every backtick, grave or acute mark and quote
// mark a reader could take for one becomes a plain single quote, so it
// cannot close its span or seem to; and a URL scheme, a mailto:, a
// leading "www." and a bare domain followed by a path are broken so no
// client draws a link. It is cut at max runes. Text with nothing to show
// is said in words, since an empty span is two backticks Markdown shows
// as they are: "empty" when it is blank, and "invisible characters
// only" when it is not.
func quoted(s string, max int) string {
	blank := strings.TrimSpace(s) == ""
	s = strings.Join(strings.Fields(blankMarks.Replace(askLine(s, max))), " ")
	s = quoteMarks.Replace(s)
	s = linkShape.ReplaceAllString(s, "${1}[:]//")
	s = mailtoShape.ReplaceAllString(s, "${1}[:]")
	s = wwwShape.ReplaceAllString(s, "${1}[.]")
	s = pathShape.ReplaceAllString(s, "${1}[.]${2}${3}")
	switch {
	case s == "" && blank:
		return "empty"
	case s == "":
		return "invisible characters only"
	}
	return "`" + s + "`"
}

// askLine is text made one line: format characters, which draw nothing
// and can reorder what does, removed; controls and line separators made
// spaces; cut at max runes.
func askLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.In(r, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point):
			return -1
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "\ufffd"))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

var (
	// quoteMarks folds every backtick, grave or acute mark and quotation
	// mark a reader could take for the question's own to a plain single
	// quote.
	quoteMarks = strings.NewReplacer("`", "'", "\u02cb", "'", "\uff40", "'", "\u1fef", "'", "\u00b4", "'",
		"\u02ca", "'", "\u02f4", "'", "\u02f5", "'", "\u1ffd", "'", "\u1fed", "'", "\u1fee", "'",
		"\u0384", "'", "\u0385", "'", `"`, "'", "\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u201c", "'", "\u201d", "'", "\u201e", "'", "\u201f", "'", "\u2032", "'", "\u2033", "'",
		"\u00ab", "'", "\u00bb", "'", "\u2039", "'", "\u203a", "'", "\u301d", "'", "\u301e", "'",
		"\u301f", "'", "\uff02", "'", "\uff07", "'", "\u02b9", "'", "\u02ba", "'", "\u02ee", "'",
		"\u05f3", "'", "\u05f4", "'", "\u2035", "'", "\u2036", "'", "\u275b", "'", "\u275c", "'",
		"\u275d", "'", "\u275e", "'", "\u3003", "'")
	// blankMarks are characters drawn as blank space that are not format
	// characters; they become spaces and collapse with the rest.
	blankMarks = strings.NewReplacer("\u2800", " ", "\u3164", " ", "\uffa0", " ", "\u115f", " ", "\u1160", " ")
	// No shape is anchored: \b is ASCII-only, and a class before the
	// shape would consume a separator the next link needs. A match inside
	// a longer word is broken too, which costs only a bracket.
	//
	// linkShape is a URL scheme followed by //, as a client links it.
	linkShape = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*)://`)
	// mailtoShape is a mail link without //.
	mailtoShape = regexp.MustCompile(`(?i)(mailto):`)
	// wwwShape is a host a client links without a scheme.
	wwwShape = regexp.MustCompile(`(?i)(www)\.`)
	// pathShape is a bare domain followed by a path, a port, a query or a
	// fragment, x.example/..., which a client links too; its last dot is
	// broken. Letters and their marks from any script count, so a
	// non-ASCII domain is broken as well.
	pathShape = regexp.MustCompile(`(?i)([\p{L}\p{M}\p{N}-]+(?:\.[\p{L}\p{M}\p{N}-]+)*)\.([\p{L}\p{M}]{2,63})([/:?#])`)
)
