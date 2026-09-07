package sheetstest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"golang.org/x/oauth2"
)

// staticToken is a token source that never refreshes. The auth path has
// its own tests; these are about the API.
type staticToken struct{}

func (staticToken) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "fixture-access-token", TokenType: "Bearer"}, nil
}

// clause is one term of a Drive query.
type clause struct {
	field string
	op    string
	value string
}

// parseDriveQuery is a small Drive query parser, strict where Drive is
// strict.
//
// It exists to make one bug impossible to reintroduce: a caller's text
// interpolated into a query without escaping lets an apostrophe close
// the string early, which detaches the mimeType filter and returns
// arbitrary files. Here that produces the 400 Drive produces, so a test
// can prove the escaping is doing the work rather than the fixture
// happening to have no apostrophes in it.
func parseDriveQuery(q string) ([]clause, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	var out []clause
	for len(q) > 0 {
		c, rest, err := parseClause(q)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		rest = strings.TrimSpace(rest)
		switch {
		case rest == "":
			return out, nil
		case strings.HasPrefix(rest, "and "):
			q = strings.TrimSpace(rest[len("and "):])
		default:
			return nil, fmt.Errorf("expected \"and\" at %q", trim(rest))
		}
	}
	return out, nil
}

func parseClause(q string) (clause, string, error) {
	// "'someone@example.test' in owners" puts the value first.
	if strings.HasPrefix(q, "'") {
		value, rest, err := parseQuoted(q)
		if err != nil {
			return clause{}, "", err
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "in ") {
			return clause{}, "", fmt.Errorf("expected \"in\" after a quoted value at %q", trim(rest))
		}
		rest = strings.TrimSpace(rest[len("in "):])
		field, rest := token(rest)
		return clause{field: field, op: "in", value: value}, rest, nil
	}
	field, rest := token(q)
	if field == "" {
		return clause{}, "", errors.New("empty term")
	}
	rest = strings.TrimSpace(rest)
	op, rest := token(rest)
	switch op {
	case "=", "!=", ">", "<", ">=", "<=", "contains":
	default:
		return clause{}, "", fmt.Errorf("unknown operator %q", trim(op))
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "'") {
		value, after, err := parseQuoted(rest)
		if err != nil {
			return clause{}, "", err
		}
		return clause{field: field, op: op, value: value}, after, nil
	}
	value, after := token(rest)
	if value == "" {
		return clause{}, "", fmt.Errorf("no value for %q", field)
	}
	return clause{field: field, op: op, value: value}, after, nil
}

// parseQuoted reads a single-quoted string with backslash escapes, and
// refuses one that ends where Drive would not accept an ending.
func parseQuoted(s string) (string, string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", "", errors.New("a query ended in a backslash")
			}
			i++
			b.WriteByte(s[i])
		case '\'':
			rest := s[i+1:]
			// A closing quote followed immediately by more word
			// characters is what an unescaped apostrophe looks like.
			if rest != "" && rest[0] != ' ' {
				return "", "", errors.New("unexpected text after a quoted value; an apostrophe inside a value must be escaped")
			}
			return b.String(), rest, nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", errors.New("unterminated quoted value")
}

func token(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func trim(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// matches applies the parsed clauses to one file.
func matches(f *gapi.File, clauses []clause) bool {
	for _, c := range clauses {
		if !matchOne(f, c) {
			return false
		}
	}
	return true
}

func matchOne(f *gapi.File, c clause) bool {
	switch c.field {
	case "mimeType":
		if c.op == "!=" {
			return f.MimeType != c.value
		}
		return f.MimeType == c.value
	case "name":
		if c.op == "contains" {
			return strings.Contains(strings.ToLower(f.Name), strings.ToLower(c.value))
		}
		return f.Name == c.value
	case "fullText":
		return strings.Contains(strings.ToLower(f.Name), strings.ToLower(c.value))
	case "trashed":
		return f.Trashed == (c.value == "true")
	case "owners":
		for _, o := range f.Owners {
			if o.EmailAddress == c.value {
				return true
			}
		}
		return false
	case "modifiedTime":
		switch c.op {
		case ">", ">=":
			return f.ModifiedTime >= c.value
		case "<", "<=":
			return f.ModifiedTime <= c.value
		}
		return f.ModifiedTime == c.value
	}
	return true
}
