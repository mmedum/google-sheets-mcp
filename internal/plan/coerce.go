package plan

import (
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// Change is one cell Google did not store as it was sent.
//
// Verified live, `USER_ENTERED` turns `1-2` into a date serial, `007`
// into 7 and `$100.15` into a number with a currency format. Every one
// of those is right for a person typing and wrong for a product code, so
// the caller is told rather than protected: they chose the input option,
// and the server never substitutes the other one (§17.3).
type Change struct {
	Address string
	// Row and Col are where the cell sits inside the write's rectangle.
	// Kept because WithDisplay used to parse Address back into the two
	// numbers that had just produced it.
	Row int
	Col int
	// Sent is what the caller gave, rendered as text.
	Sent string
	// SentKind and Kind are what it was and what it became: number,
	// boolean, text or empty. Both, because the change that matters most
	// is invisible in the values alone — the string "12" and the number
	// 12 print the same, and only the pair of kinds says which the cell
	// now holds.
	SentKind string
	// Stored is what Google kept, read back in the same response.
	Stored string
	Kind   string
	// Displayed is what the cell shows. A date is why this field exists:
	// stored 46270 and displayed 2026-09-05 are the same cell, and only
	// the pair reads as a format rather than as data loss.
	Displayed string
}

// Diff compares what was sent against what Google stored.
//
// Both sides are compared as (text, kind) pairs, so the string "7" and
// the number 7 are a change even though they print the same — which is
// exactly the change a caller writing a product code needs to see.
func Diff(target a1.Rect, sent, stored [][]any) []Change {
	var out []Change
	for i, row := range sent {
		for j, v := range row {
			sentText, sentKind := scalar(v)
			gotText, gotKind := scalar(at(stored, i, j))
			if sentText == gotText && sentKind == gotKind {
				continue
			}
			// After the comparison, not before: most cells are stored as
			// they were sent, and formatting an address for each of them
			// is work thrown away.
			addr, err := a1.CellName(target.FirstCol+j, target.FirstRow+i)
			if err != nil {
				continue
			}
			out = append(out, Change{
				Address: addr, Row: i, Col: j,
				Sent: sentText, SentKind: sentKind, Stored: gotText, Kind: gotKind,
			})
		}
	}
	return out
}

// WithDisplay fills in what each changed cell shows, from a formatted
// read of the same rectangle. A row or a cell the read did not reach is
// left blank rather than guessed.
func WithDisplay(changes []Change, displayed [][]any) []Change {
	for i := range changes {
		if text, _ := scalar(at(displayed, changes[i].Row, changes[i].Col)); text != "" {
			changes[i].Displayed = text
		}
	}
	return changes
}

// Formulas lists the cells a write leaves holding a formula.
//
// Read off what came back rather than predicted from what was sent, with
// one thing the response cannot say: under RAW a string beginning with
// "=" is stored as that string and never evaluated (verified live), and
// the FORMULA render returns the same text either way. Which of the two
// it was is the caller's to say.
func Formulas(target a1.Rect, stored [][]any, formulasEvaluated bool) []string {
	if !formulasEvaluated {
		return nil
	}
	var out []string
	for i, row := range stored {
		for j, v := range row {
			s, ok := v.(string)
			if !ok || !strings.HasPrefix(s, "=") {
				continue
			}
			addr, err := a1.CellName(target.FirstCol+j, target.FirstRow+i)
			if err != nil {
				continue
			}
			out = append(out, addr)
		}
	}
	return out
}

// at reads a cell out of a possibly ragged response. The API omits
// trailing empties, so a row can be shorter than the one that was sent.
func at(rows [][]any, i, j int) any {
	if i < 0 || j < 0 || i >= len(rows) || j >= len(rows[i]) {
		return nil
	}
	return rows[i][j]
}

// scalar renders one JSON value as text and names its kind.
func scalar(v any) (text, kind string) {
	switch t := v.(type) {
	case nil:
		return "", "empty"
	case string:
		if t == "" {
			return "", "empty"
		}
		return t, "text"
	case bool:
		if t {
			return "TRUE", "boolean"
		}
		return "FALSE", "boolean"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), "number"
	case int:
		return strconv.Itoa(t), "number"
	}
	return "", "empty"
}
