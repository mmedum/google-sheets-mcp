// Package a1 is the only place in this server that knows how A1
// notation and the API's GridRange relate.
//
// The two disagree in every way they can: A1 is one-based and
// inclusive, GridRange is zero-based and half-open, and a missing
// GridRange index means unbounded where A1 spells it "A5:A". Keeping
// the arithmetic here means the model never sees an index and no other
// package can get the conversion wrong.
//
// Three traps this package exists to close, all of them from servers
// that shipped without it:
//
//   - A column parser that is not total returns a plausible index for
//     nonsense: "A1" as a column is column K if only the empty string
//     is rejected. Every parse here is total and rejects.
//   - A sheet title concatenated into a range string breaks on spaces
//     and apostrophes. Quoting is also what keeps a whole-sheet
//     reference meaning the sheet: verified live, a bare name with no
//     "!" is resolved as a *named range* first, so "Data" returns the
//     named range called Data while "'Data'" returns the sheet. (With a
//     "!" the left side is always a sheet title, so "Data!A1:B2" reads
//     the sheet either way — the trap is narrower than the concepts
//     guide's wording suggests, and it is exactly the form this server
//     sends for a whole sheet.) Format always quotes.
//   - "A1" without a sheet is a cell of the first visible sheet, while
//     "'A1'" is the whole sheet named A1. Parse tells them apart.
//
// Nothing here touches the network.
package a1

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Limits the API enforces, and the reason a parse can refuse a
// syntactically fine reference.
const (
	// MaxColumns is 18278 columns per spreadsheet, which is column ZZZ.
	MaxColumns = 18278
	// MaxRows is the 10-million-cell ceiling read as a row number: no
	// row beyond it can exist in any spreadsheet.
	MaxRows = 10000000
)

// ErrInvalid wraps every parse failure, so a caller can classify without
// matching on message text.
var ErrInvalid = errors.New("a1: invalid reference")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Rect is a rectangle in A1's own coordinates: one-based and inclusive.
// Zero on any side means unbounded there, which is what "A5:A", "B:B"
// and a bare sheet name mean.
type Rect struct {
	FirstCol int
	FirstRow int
	LastCol  int
	LastRow  int
}

// WholeSheet is the rectangle a bare sheet reference names.
var WholeSheet = Rect{}

// Ref is a parsed reference: a rectangle, and the sheet title it named
// if it named one.
type Ref struct {
	Sheet    string
	HasSheet bool
	Rect     Rect
}

// ParseColumn turns column letters into a one-based column number.
//
// It is total: one to three letters and nothing else. "A1" is not a
// column, and neither is "" or "AA1" — a parser that accepts them
// returns an index that looks like an answer.
func ParseColumn(s string) (int, error) {
	if s == "" {
		return 0, invalid("empty column")
	}
	if len(s) > 3 {
		return 0, invalid("column %q is longer than three letters; the last column is ZZZ", s)
	}
	col := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			col = col*26 + int(c-'A') + 1
		case c >= 'a' && c <= 'z':
			col = col*26 + int(c-'a') + 1
		default:
			return 0, invalid("column %q must be letters only (A, B, ... AA, ... ZZZ)", s)
		}
	}
	if col > MaxColumns {
		return 0, invalid("column %q is past ZZZ, the last column a spreadsheet has", s)
	}
	return col, nil
}

// ColumnName turns a one-based column number into its letters.
func ColumnName(col int) (string, error) {
	if col < 1 || col > MaxColumns {
		return "", invalid("column number %d is outside 1..%d", col, MaxColumns)
	}
	var b []byte
	for col > 0 {
		col--
		b = append([]byte{byte('A' + col%26)}, b...)
		col /= 26
	}
	return string(b), nil
}

// CellName formats a one-based column and row as a cell address.
func CellName(col, row int) (string, error) {
	name, err := ColumnName(col)
	if err != nil {
		return "", err
	}
	if row < 1 || row > MaxRows {
		return "", invalid("row number %d is outside 1..%d", row, MaxRows)
	}
	return name + strconv.Itoa(row), nil
}

// parseRow turns row digits into a one-based row number.
func parseRow(s string) (int, error) {
	if s == "" {
		return 0, invalid("empty row")
	}
	if len(s) > 8 {
		return 0, invalid("row %q is past the last row a spreadsheet can have", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, invalid("row %q must be digits only", s)
		}
	}
	// Every byte is a digit and the length is capped above, so this
	// cannot fail.
	n, _ := strconv.Atoi(s)
	if n < 1 {
		return 0, invalid("row %q is below row 1; A1 notation counts from 1", s)
	}
	if n > MaxRows {
		return 0, invalid("row %d is past the last row a spreadsheet can have (%d)", n, MaxRows)
	}
	return n, nil
}

// endpoint is one side of a range: a column, a row, or both. Zero means
// that half was not given.
type endpoint struct {
	col int
	row int
}

// parseEndpoint reads "B7", "B" or "7".
func parseEndpoint(s string) (endpoint, error) {
	if s == "" {
		return endpoint{}, invalid("empty endpoint")
	}
	i := 0
	for i < len(s) && isLetter(s[i]) {
		i++
	}
	letters, digits := s[:i], s[i:]
	switch {
	case letters != "" && digits != "":
		col, err := ParseColumn(letters)
		if err != nil {
			return endpoint{}, err
		}
		row, err := parseRow(digits)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{col: col, row: row}, nil
	case letters != "":
		col, err := ParseColumn(letters)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{col: col}, nil
	default:
		// digits != "", since s is not empty and letters is.
		row, err := parseRow(digits)
		if err != nil {
			return endpoint{}, err
		}
		return endpoint{row: row}, nil
	}
}

func isLetter(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }

// ParseRect parses the range half of an A1 reference: "B2:D40", "B:B",
// "2:5", "A5:A", "C3", or "" for a whole sheet.
func ParseRect(s string) (Rect, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return WholeSheet, nil
	}
	if strings.Contains(s, "!") {
		return Rect{}, invalid("%q carries a sheet name; parse it with Parse", s)
	}
	left, right, hasColon := strings.Cut(s, ":")
	if !hasColon {
		e, err := parseEndpoint(s)
		if err != nil {
			return Rect{}, err
		}
		if e.col == 0 || e.row == 0 {
			return Rect{}, invalid("%q names a column or a row, not a range; write %q for all of it", s, s+":"+s)
		}
		return Rect{FirstCol: e.col, FirstRow: e.row, LastCol: e.col, LastRow: e.row}, nil
	}
	if strings.Contains(right, ":") {
		return Rect{}, invalid("%q has more than one colon", s)
	}
	a, err := parseEndpoint(left)
	if err != nil {
		return Rect{}, err
	}
	b, err := parseEndpoint(right)
	if err != nil {
		return Rect{}, err
	}
	// "A:5" and "1:B" name no rectangle: the two sides have to agree on
	// whether columns are bounded.
	if (a.col == 0) != (b.col == 0) {
		return Rect{}, invalid("%q mixes a column with a row; write a rectangle (B2:D40), whole columns (B:D) or whole rows (2:5)", s)
	}
	return Rect{FirstCol: a.col, FirstRow: a.row, LastCol: b.col, LastRow: b.row}.Normalise(), nil
}

// Normalise puts a reversed rectangle the right way round. "D40:B2" and
// "B2:D40" are the same rectangle and Sheets accepts both.
func (r Rect) Normalise() Rect {
	if r.FirstCol != 0 && r.LastCol != 0 && r.FirstCol > r.LastCol {
		r.FirstCol, r.LastCol = r.LastCol, r.FirstCol
	}
	if r.FirstRow != 0 && r.LastRow != 0 && r.FirstRow > r.LastRow {
		r.FirstRow, r.LastRow = r.LastRow, r.FirstRow
	}
	return r
}

// SplitSheet separates a quoted or unquoted sheet title from the range
// after it.
//
// "'A1'" is the sheet named A1 and "A1" is a cell: the difference is the
// quoting, and it is the one trap here that fails silently rather than
// loudly.
func SplitSheet(s string) (sheet, rest string, hasSheet bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false, nil
	}
	if s[0] == '\'' {
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '\'' {
				b.WriteByte(s[i])
				continue
			}
			// Two apostrophes inside a quoted title are one apostrophe.
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			after := s[i+1:]
			switch {
			case after == "":
				return b.String(), "", true, nil
			case after[0] == '!':
				return b.String(), after[1:], true, nil
			default:
				return "", "", false, invalid("%q has text after the quoted sheet name; expected \"!\" and a range", s)
			}
		}
		return "", "", false, invalid("%q opens a quoted sheet name and never closes it", s)
	}
	if i := strings.LastIndexByte(s, '!'); i >= 0 {
		title := s[:i]
		if title == "" {
			return "", "", false, invalid("%q has no sheet name before the \"!\"", s)
		}
		return title, s[i+1:], true, nil
	}
	return "", s, false, nil
}

// Parse reads a full reference: a range, or a sheet and a range.
func Parse(s string) (Ref, error) {
	sheet, rest, hasSheet, err := SplitSheet(s)
	if err != nil {
		return Ref{}, err
	}
	rect, err := ParseRect(rest)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Sheet: sheet, HasSheet: hasSheet, Rect: rect}, nil
}

// QuoteSheet wraps a sheet title in single quotes, doubling any
// apostrophe inside it. Every title this server sends goes through here,
// including the ones that would not need it.
//
// Two reasons, one of them load-bearing in a narrower place than it
// first appears. A title with a space or punctuation *requires* quotes.
// And a whole-sheet reference — the form with no "!" — is resolved as a
// named range first, so an unquoted "Data" returns the named range
// called Data rather than the sheet. Verified live: with a "!" present
// the left side is always a sheet title, so quoting changes nothing
// there. Quoting everything means neither case has to be thought about
// at the call site.
func QuoteSheet(title string) string {
	return "'" + strings.ReplaceAll(title, "'", "''") + "'"
}

// FormatRect renders a rectangle without its sheet. An unbounded
// rectangle renders as the empty string, which is a whole sheet.
//
// A side the rectangle leaves open is spelled out rather than dropped,
// because A1 can only leave the *end* of a column open ("A5:A"). An
// open start is the sheet's own start, and an open end that A1 cannot
// spell is the sheet's last column or row, which is what unbounded
// means anyway.
func FormatRect(r Rect) string {
	if r == WholeSheet {
		return ""
	}
	fc, fr, lc, lr := r.FirstCol, r.FirstRow, r.LastCol, r.LastRow
	if fc == 0 && lc != 0 {
		fc = 1
	}
	if fr == 0 && lr != 0 {
		fr = 1
	}
	if lc == 0 && fc != 0 {
		lc = MaxColumns
	}
	// "A5:A" says "column A from row 5 down" and needs no end row. A
	// row range with no columns has no such spelling, so it gets one.
	if lr == 0 && fr != 0 && fc == 0 {
		lr = MaxRows
	}
	first, last := corner(fc, fr), corner(lc, lr)
	if fc == lc && fr == lr && fc != 0 && fr != 0 {
		return first
	}
	return first + ":" + last
}

func corner(col, row int) string {
	name := ""
	if col != 0 {
		name, _ = ColumnName(col)
	}
	if row != 0 {
		name += strconv.Itoa(row)
	}
	return name
}

// Format renders a reference the way this server sends it: the sheet
// title, always quoted, then the range.
func Format(sheet string, r Rect) string {
	q := QuoteSheet(sheet)
	rect := FormatRect(r)
	if rect == "" {
		return q
	}
	return q + "!" + rect
}

// GridRange converts to the API's zero-based half-open form. An
// unbounded side becomes a missing index, which is what the API means by
// unbounded too.
func (r Rect) GridRange(sheetID int) *gsheets.GridRange {
	g := &gsheets.GridRange{SheetID: sheetID}
	if r.FirstRow != 0 {
		g.StartRowIndex = ptr(r.FirstRow - 1)
	}
	if r.LastRow != 0 {
		g.EndRowIndex = ptr(r.LastRow)
	}
	if r.FirstCol != 0 {
		g.StartColumnIndex = ptr(r.FirstCol - 1)
	}
	if r.LastCol != 0 {
		g.EndColumnIndex = ptr(r.LastCol)
	}
	return g
}

// FromGridRange converts back. Round-tripping either way is a table test.
func FromGridRange(g *gsheets.GridRange) Rect {
	if g == nil {
		return WholeSheet
	}
	var r Rect
	if g.StartRowIndex != nil {
		r.FirstRow = *g.StartRowIndex + 1
	}
	if g.EndRowIndex != nil {
		r.LastRow = *g.EndRowIndex
	}
	if g.StartColumnIndex != nil {
		r.FirstCol = *g.StartColumnIndex + 1
	}
	if g.EndColumnIndex != nil {
		r.LastCol = *g.EndColumnIndex
	}
	return r
}

func ptr[T any](v T) *T { return &v }

// Bounded reports whether every side of the rectangle is given.
func (r Rect) Bounded() bool {
	return r.FirstCol != 0 && r.FirstRow != 0 && r.LastCol != 0 && r.LastRow != 0
}

// Rows and Cols are the sizes of a bounded rectangle, and 0 otherwise.
func (r Rect) Rows() int {
	if r.FirstRow == 0 || r.LastRow == 0 {
		return 0
	}
	return r.LastRow - r.FirstRow + 1
}

// Cols is Rows for columns.
func (r Rect) Cols() int {
	if r.FirstCol == 0 || r.LastCol == 0 {
		return 0
	}
	return r.LastCol - r.FirstCol + 1
}

// Cells counts the cells a bounded rectangle covers. An unbounded
// rectangle has no answer, which is the point: a read resolves its
// window against the sheet's real extent before asking for it, so an
// open-ended range never becomes an unbounded fetch.
func (r Rect) Cells() (int, bool) {
	if !r.Bounded() {
		return 0, false
	}
	return r.Rows() * r.Cols(), true
}

// Clamp resolves every unbounded side against a sheet's allocated size
// and trims the rest to fit inside it.
func (r Rect) Clamp(rows, cols int) Rect {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	if r.FirstRow == 0 {
		r.FirstRow = 1
	}
	if r.FirstCol == 0 {
		r.FirstCol = 1
	}
	if r.LastRow == 0 || r.LastRow > rows {
		r.LastRow = rows
	}
	if r.LastCol == 0 || r.LastCol > cols {
		r.LastCol = cols
	}
	if r.FirstRow > r.LastRow {
		r.FirstRow = r.LastRow
	}
	if r.FirstCol > r.LastCol {
		r.FirstCol = r.LastCol
	}
	return r
}

// OffsetOf converts a response's own zero-based origin into a position
// inside this rectangle.
//
// The API answers with startRow and startColumn of its own, which need
// not be where the request asked it to start, and both are zero-based
// where the rectangle is one-based. That subtraction is the single
// easiest off-by-one in the project, so it lives here with the rest of
// the arithmetic rather than in whichever package decodes a response.
func (r Rect) OffsetOf(startRowIndex, startColumnIndex int) (row, col int) {
	return startRowIndex - (r.FirstRow - 1), startColumnIndex - (r.FirstCol - 1)
}

// LimitRows trims a bounded rectangle to at most n rows and says whether
// it had to.
func (r Rect) LimitRows(n int) (Rect, bool) {
	if n < 1 || r.FirstRow == 0 || r.LastRow == 0 || r.Rows() <= n {
		return r, false
	}
	r.LastRow = r.FirstRow + n - 1
	return r, true
}

// Overlaps reports whether two rectangles share a cell, treating an
// unbounded side as reaching to the edge of the sheet.
func (r Rect) Overlaps(o Rect) bool {
	return overlaps1D(r.FirstRow, r.LastRow, o.FirstRow, o.LastRow) &&
		overlaps1D(r.FirstCol, r.LastCol, o.FirstCol, o.LastCol)
}

func overlaps1D(aFirst, aLast, bFirst, bLast int) bool {
	if aFirst == 0 {
		aFirst = 1
	}
	if bFirst == 0 {
		bFirst = 1
	}
	if aLast != 0 && bFirst > aLast {
		return false
	}
	if bLast != 0 && aFirst > bLast {
		return false
	}
	return true
}

// Contains reports whether o lies wholly inside r. An unbounded side of
// r contains anything on that side; an unbounded side of o is contained
// only if r is unbounded there too.
func (r Rect) Contains(o Rect) bool {
	return contains1D(r.FirstRow, r.LastRow, o.FirstRow, o.LastRow) &&
		contains1D(r.FirstCol, r.LastCol, o.FirstCol, o.LastCol)
}

func contains1D(rFirst, rLast, oFirst, oLast int) bool {
	if rFirst == 0 {
		rFirst = 1
	}
	if oFirst == 0 {
		oFirst = 1
	}
	if oFirst < rFirst {
		return false
	}
	if rLast == 0 {
		return true
	}
	if oLast == 0 || oLast > rLast {
		return false
	}
	return true
}
