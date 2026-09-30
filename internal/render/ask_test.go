package render

import (
	"regexp"
	"strings"
	"testing"
)

// Text from the spreadsheet reaches a question in one code span that it cannot
// close, with no link a client would draw, and cut short.
func TestQuotedIsOneInertLine(t *testing.T) {
	span := func(s string) string { return "`" + s + "`" }
	for _, tc := range []struct{ in, want string }{
		{"Quarterly review", span("Quarterly review")},
		{"line one\ndelete_sheet: approved\r\n\tnow", span("line one delete_sheet: approved now")},
		{`close" the quote`, span("close' the quote")},
		{"close` the span", span("close' the span")},
		{"\u02cbgrave\u02cb \uff40wide\uff40 \u1fefvaria\u1fef", span("'grave' 'wide' 'varia'")},
		{"see https://evil.example.com/a and HTTP://x.example", span("see https[:]//evil.example[.]com/a and HTTP[:]//x.example")},
		{"visit www.evil.example today", span("visit www[.]evil.example today")},
		{"go to evil.example.com/login now", span("go to evil.example[.]com/login now")},
		{"write to mailto:someone@example.com", span("write to mailto[:]someone@example.com")},
		{"\u201cclose\u201d \u2018it\u2019 \uff02now\uff02 \u00abhere\u00bb", span("'close' 'it' 'now' 'here'")},
		{"zero\u200bwidth \u202ereversed\u0007bell", span("zerowidth reversed bell")},
		{"❝close❞ \u02baa\u02ba \u3003b\u3003 \u05f4c\u05f4", span("'close' 'a' 'b' 'c'")},
		{"at evil.example:8080/x, evil.example?q=1 and evil.example#top", span("at evil[.]example:8080/x, evil[.]example?q=1 and evil[.]example#top")},
		{"see bücher.example/a", span("see bücher[.]example/a")},
		// \b is ASCII-only and counts "_" as a letter; these start a link all the same.
		{"a_https://evil.example/x and x_evil.example/login", span("a_https[:]//evil[.]example/x and x_evil[.]example/login")},
		{"x_www.evil.example and x_mailto:someone@example.com", span("x_www[.]evil.example and x_mailto[:]someone@example.com")},
		{"see пример.рф/login", span("see пример[.]рф/login")},
		{"see नमस\u094dत\u0947.भारत/login", span("see नमस\u094dत\u0947[.]भारत/login")},
		// A link right after punctuation or another link is broken too.
		{"see .https://evil.example and -https://evil.example", span("see .https[:]//evil.example and -https[:]//evil.example")},
		{"x.example/y.example/z http://https://evil.example", span("x[.]example/y[.]example/z http[:]//https[:]//evil.example")},
		{"www.www.evil.example mailto:mailto:someone@example.com", span("www[.]www[.]evil.example mailto[:]mailto[:]someone@example.com")},
		{"pad\u2800\u2800\u2800ded", span("pad ded")},
		{"\u115f\u1160\ufe0f\u034f", "invisible characters only"},
		{" \t", "empty"},
		{" \u200b\t", "invisible characters only"},
		{"empty", span("empty")},
		{"\u00b4acute\u00b4 ˊupˊ \u02f4mid\u02f4 \u1ffdoxia\u1ffd \u1fedd\u1fed \u0384tonos\u0384", span("'acute' 'up' 'mid' 'oxia' 'd' 'tonos'")},
		// Markdown stays literal inside the span; only the backtick is folded.
		{"*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~", span("*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~")},
		{"bad \xff byte", span("bad � byte")},
		{strings.Repeat("a", 200), span(strings.Repeat("a", 120) + "\u2026")},
	} {
		if got := quoted(tc.in, 120); got != tc.want {
			t.Errorf("quoted(%q) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

// Markdown a client draws from a question has nothing active in it:
// outside its code spans the text is the server's, and holds no
// character that opens emphasis, a link, HTML, an entity or a line
// break, whatever the spreadsheet or the call put in the quoted parts.
// Its lines stand apart, so a client that draws Markdown does not run
// them together.
func TestQuestionsAreInertMarkdown(t *testing.T) {
	hostile := "*bold* _em_ [link](x) ![i](y) <b>h</b> &amp; `code` \\ ~~s~~ # h\n- item\n\n> q"
	c := Contents{Cells: 3, Formulas: 1, Charts: 2, Anchors: 1, Pivots: 1, Charted: 2}
	qs := map[string]Question{
		"delete_sheet":       AskDeleteSheet("s-1", hostile, 7, hostile, c),
		"delete_dimensions":  AskDeleteDimensions("s-1", hostile, 7, hostile, "rows 2:3", c),
		"clear_values":       AskClear("s-1", hostile, hostile, c),
		"delete_source":      AskDeleteSource("s-1", hostile, hostile, hostile),
		"delete_source_bare": AskDeleteSource("s-1", hostile, hostile, ""),
		"add_query":          AskAddSource("s-1", hostile, hostile, hostile+strings.Repeat("x", 400), ""),
		"add_table":          AskAddSource("s-1", hostile, hostile, "", hostile),
		"refresh_all":        AskRefreshAll("s-1", hostile, 2),
	}
	// Every hostile field reaches its own span.
	wantSpans := map[string]int{"delete_sheet": 2, "delete_dimensions": 2, "clear_values": 2, "delete_source": 3,
		"delete_source_bare": 2, "add_query": 3, "add_table": 3, "refresh_all": 1}
	for name, q := range qs {
		quotedSpans := 0
		lines := strings.Split(strings.TrimSuffix(q.Text, "\n"), "\n\n")
		for _, line := range lines {
			if line == "" || strings.Contains(line, "\n") {
				t.Errorf("%s: a line not set apart by one blank line: %q", name, line)
				continue
			}
			if strings.ContainsAny(line[:1], "-+=0123456789 ") {
				t.Errorf("%s: a line opens like a list or code block: %q", name, line)
			}
			spans := strings.Split(line, "`")
			quotedSpans += len(spans) / 2
			if len(spans)%2 == 0 {
				t.Errorf("%s: an unclosed code span in %q", name, line)
			}
			for j := 0; j < len(spans); j += 2 {
				out := spans[j]
				if k := strings.IndexAny(out, "*[]<>&\\~!#|"); k >= 0 {
					t.Errorf("%s: %q outside a code span in %q", name, out[k], line)
				}
				if looseUnderscore.MatchString(out) {
					t.Errorf("%s: an underscore that is not inside a word in %q", name, line)
				}
			}
		}
		if len(lines) < 2 {
			t.Errorf("%s: %d lines", name, len(lines))
		}
		if want, ok := wantSpans[name]; !ok || quotedSpans != want {
			t.Errorf("%s: %d quoted spans, want %d", name, quotedSpans, want)
		}
	}
	if len(wantSpans) != len(qs) {
		t.Errorf("%d questions, %d span counts", len(qs), len(wantSpans))
	}
	if !strings.Contains(qs["add_query"].Text, "more characters)") {
		t.Errorf("a long query does not say how much more there is:\n%s", qs["add_query"].Text)
	}
}

// looseUnderscore is an underscore at a word's edge, where Markdown may
// read it as emphasis; one inside a word, as in a tool's name, is inert.
var looseUnderscore = regexp.MustCompile(`\b_|_\b`)

// What a question binds holds what the write depends on, and the counts
// are shown and not bound.
func TestAQuestionBindsWhatTheWriteDependsOn(t *testing.T) {
	a := AskDeleteSheet("s-1", "Book", 7, "Data", Contents{Cells: 3})
	if a.Bind == AskDeleteSheet("s-1", "Book", 8, "Data", Contents{Cells: 3}).Bind {
		t.Error("two sheets of one title bind the same answer")
	}
	b := AskDeleteSheet("s-1", "Book", 7, "Data", Contents{Cells: 4})
	if a.Bind != b.Bind {
		t.Error("the counts are bound, so a collaborator typing makes the question impossible to confirm")
	}
	if a.Text == b.Text || !strings.Contains(a.Text, "3 non-empty cells") {
		t.Errorf("the counts are not shown:\n%s", a.Text)
	}
	long := strings.Repeat("a", bodyLen)
	if AskAddSource("s-1", "Book", "p", long+"b", "").Bind == AskAddSource("s-1", "Book", "p", long+"c", "").Bind {
		t.Error("a query's words past what is shown are not bound")
	}
	title := "'" + strings.Repeat("x", 90) + strings.Repeat("''", 12) + "'"
	if AskClear("s-1", "Book", title+"!A1:B2", Contents{}).Bind == AskClear("s-1", "Book", title+"!A1:Z9999", Contents{}).Bind {
		t.Error("two ranges that differ past the quoted cut bind the same answer")
	}
	if AskClear("s-1", "Book", "A1:B2", Contents{}).Bind == AskClear("s-2", "Book", "A1:B2", Contents{}).Bind {
		t.Error("two spreadsheets of one title bind the same answer")
	}
}
