package sheetstest

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The fake's half of the transforms.
//
// Two kinds of request here, and the difference is the rule this package
// is built on. A sort, a replacement, a trim, a de-duplication and a
// paste are mechanical: what they do follows from the request, so doing
// it here invents nothing. An autofill's series, a randomisation and a
// delimiter Google detects for itself are judgements — so those are
// validated and the cells are left alone, and a test that needs one uses
// the live driver instead.

// applyTransform is the transform half of the union.
func applyTransform(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.SortRange != nil:
		return reply(sortRange(d, req.SortRange))
	case req.FindReplace != nil:
		return findReplace(d, req.FindReplace)
	case req.TrimWhitespace != nil:
		return trimWhitespace(d, req.TrimWhitespace)
	case req.DeleteDuplicates != nil:
		return deleteDuplicates(d, req.DeleteDuplicates)
	case req.TextToColumns != nil:
		return reply(textToColumns(d, req.TextToColumns))
	case req.CopyPaste != nil:
		return reply(copyPaste(d, req.CopyPaste))
	case req.CutPaste != nil:
		return reply(cutPaste(d, req.CutPaste))

	// The two that turn on Google's own judgement. The range is checked,
	// so a request naming a sheet that does not exist still fails here,
	// and nothing is invented about what the cells become.
	case req.RandomizeRange != nil:
		_, _, err := sheetForRange(d, req.RandomizeRange.Range)
		return reply(err)
	case req.AutoFill != nil:
		if req.AutoFill.SourceAndDestination == nil {
			return nil, true, errors.New("this server always sends autoFill's explicit form")
		}
		_, _, err := sheetForRange(d, req.AutoFill.SourceAndDestination.Source)
		return reply(err)
	}
	return nil, false, nil
}

// sortRange orders the rows of a rectangle by the columns it is given.
//
// dimensionIndex is read as the sheet's own column, which is what this
// server sends and what the live driver checks: a sort on the second
// column of a range that starts at B has to move the same rows here and
// there, or the fake would agree with a mistake.
func sortRange(d *Doc, req *gsheets.SortRangeRequest) error {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return err
	}
	if len(req.SortSpecs) == 0 {
		return errors.New("sortRange needs at least one sort spec")
	}
	rows := take(sh, rect)
	sort.SliceStable(rows, func(i, j int) bool {
		for _, spec := range req.SortSpecs {
			col := spec.DimensionIndex + 1 - rect.FirstCol
			if col < 0 || col >= len(rows[i]) {
				continue
			}
			a, b := sortKey(rows[i][col]), sortKey(rows[j][col])
			if a == b {
				continue
			}
			if spec.SortOrder == gsheets.SortDescending {
				return a > b
			}
			return a < b
		}
		return false
	})
	put(sh, rect, rows)
	return nil
}

// sortKey is what a cell sorts on. Numbers are padded so they order as
// numbers rather than as the digits that spell them.
func sortKey(c *gsheets.CellData) string {
	if c == nil || c.EffectiveValue == nil {
		return ""
	}
	v := c.EffectiveValue
	switch {
	case v.NumberValue != nil:
		return "0" + strconv.FormatFloat(*v.NumberValue+1e12, 'f', 6, 64)
	case v.StringValue != nil:
		return "1" + *v.StringValue
	case v.BoolValue != nil:
		return "2" + strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

// take lifts a rectangle out as rows of cells, and put writes rows back.
func take(sh *Sheet, rect a1.Rect) [][]*gsheets.CellData {
	rows := make([][]*gsheets.CellData, 0, rect.Rows())
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		line := make([]*gsheets.CellData, 0, rect.Cols())
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			line = append(line, sh.At(row, col))
		}
		rows = append(rows, line)
	}
	return rows
}

func put(sh *Sheet, rect a1.Rect, rows [][]*gsheets.CellData) {
	for i, line := range rows {
		for j, cell := range line {
			key := [2]int{rect.FirstRow + i - 1, rect.FirstCol + j - 1}
			if cell == nil {
				delete(sh.Cells, key)
				continue
			}
			sh.Cells[key] = cell
		}
	}
}

// findReplace replaces text in a rectangle and counts what it changed.
func findReplace(d *Doc, req *gsheets.FindReplaceRequest) (*gsheets.Reply, bool, error) {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return nil, true, err
	}
	if req.Find == "" {
		return nil, true, errors.New("findReplace needs something to find")
	}
	var re *regexp.Regexp
	if req.SearchByRegex {
		pattern := req.Find
		if !req.MatchCase {
			pattern = "(?i)" + pattern
		}
		if re, err = regexp.Compile(pattern); err != nil {
			return nil, true, errors.New("the regular expression could not be compiled")
		}
	}
	out := &gsheets.FindReplaceReply{}
	rowsChanged := map[int]bool{}
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil || cell.UserEnteredValue == nil {
				continue
			}
			formula := cell.UserEnteredValue.FormulaValue != nil
			if formula && !req.IncludeFormulas {
				continue
			}
			text, ok := replaceable(cell)
			if !ok {
				continue
			}
			replaced, n := replaceIn(text, req, re)
			if n == 0 {
				continue
			}
			out.OccurrencesChanged += n
			rowsChanged[row] = true
			if formula {
				out.FormulasChanged++
				sh.Set(row, col, &gsheets.CellData{
					UserEnteredValue: &gsheets.ExtendedValue{FormulaValue: &replaced},
				})
				continue
			}
			out.ValuesChanged++
			sh.Set(row, col, Str(replaced))
		}
	}
	out.RowsChanged = len(rowsChanged)
	if out.OccurrencesChanged > 0 {
		out.SheetsChanged = 1
	}
	return &gsheets.Reply{FindReplace: out}, true, nil
}

// replaceable is the text a replacement reads: a formula's own text, or
// a string cell's value. A number is not searched, which is the API's
// own rule.
func replaceable(c *gsheets.CellData) (string, bool) {
	v := c.UserEnteredValue
	switch {
	case v.FormulaValue != nil:
		return *v.FormulaValue, true
	case v.StringValue != nil:
		return *v.StringValue, true
	}
	return "", false
}

func replaceIn(text string, req *gsheets.FindReplaceRequest, re *regexp.Regexp) (string, int) {
	if re != nil {
		matches := re.FindAllString(text, -1)
		if len(matches) == 0 {
			return text, 0
		}
		return re.ReplaceAllString(text, req.Replacement), len(matches)
	}
	if req.MatchEntireCell {
		if equal(text, req.Find, req.MatchCase) {
			return req.Replacement, 1
		}
		return text, 0
	}
	if req.MatchCase {
		n := strings.Count(text, req.Find)
		if n == 0 {
			return text, 0
		}
		return strings.ReplaceAll(text, req.Find, req.Replacement), n
	}
	// Case-insensitive, without a regular expression: walk the string
	// and rebuild it, so the replacement keeps the case it was given
	// rather than the case it matched.
	var b strings.Builder
	lower, find := strings.ToLower(text), strings.ToLower(req.Find)
	count, i := 0, 0
	for {
		j := strings.Index(lower[i:], find)
		if j < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(text[i : i+j])
		b.WriteString(req.Replacement)
		i += j + len(find)
		count++
	}
	if count == 0 {
		return text, 0
	}
	return b.String(), count
}

func equal(a, b string, matchCase bool) bool {
	if matchCase {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// trimWhitespace strips leading and trailing whitespace and collapses
// the runs inside, which is what the API documents.
func trimWhitespace(d *Doc, req *gsheets.TrimWhitespaceRequest) (*gsheets.Reply, bool, error) {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return nil, true, err
	}
	changed := 0
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			cell := sh.At(row, col)
			if cell == nil || cell.UserEnteredValue == nil || cell.UserEnteredValue.StringValue == nil {
				continue
			}
			was := *cell.UserEnteredValue.StringValue
			now := strings.Join(strings.Fields(was), " ")
			if now == was {
				continue
			}
			sh.Set(row, col, Str(now))
			changed++
		}
	}
	return &gsheets.Reply{TrimWhitespace: &gsheets.TrimWhitespaceReply{CellsChangedCount: changed}}, true, nil
}

// deleteDuplicates removes rows whose compared columns repeat an earlier
// row, and moves what is below them up.
func deleteDuplicates(d *Doc, req *gsheets.DeleteDuplicatesRequest) (*gsheets.Reply, bool, error) {
	sh, rect, err := sheetForRange(d, req.Range)
	if err != nil {
		return nil, true, err
	}
	compare := make([]int, 0, len(req.ComparisonColumns))
	for _, c := range req.ComparisonColumns {
		compare = append(compare, c.StartIndex+1-rect.FirstCol)
	}
	rows := take(sh, rect)
	seen := map[string]bool{}
	kept := make([][]*gsheets.CellData, 0, len(rows))
	removed := 0
	for _, line := range rows {
		key := duplicateKey(line, compare)
		if seen[key] {
			removed++
			continue
		}
		seen[key] = true
		kept = append(kept, line)
	}
	for len(kept) < len(rows) {
		kept = append(kept, make([]*gsheets.CellData, rect.Cols()))
	}
	put(sh, rect, kept)
	return &gsheets.Reply{DeleteDuplicates: &gsheets.DeleteDuplicatesReply{DuplicatesRemovedCount: removed}}, true, nil
}

func duplicateKey(line []*gsheets.CellData, compare []int) string {
	var b strings.Builder
	if len(compare) == 0 {
		for _, cell := range line {
			b.WriteString(sortKey(cell))
			b.WriteByte(0x1f)
		}
		return b.String()
	}
	for _, col := range compare {
		if col >= 0 && col < len(line) {
			b.WriteString(sortKey(line[col]))
		}
		b.WriteByte(0x1f)
	}
	return b.String()
}

// textToColumns splits one column on a delimiter it was given.
//
// With the delimiter left to Google to detect, nothing is changed: which
// character it picks is its judgement, and a fake that guessed would let
// a test agree with the guess.
func textToColumns(d *Doc, req *gsheets.TextToColumnsRequest) error {
	sh, rect, err := sheetForRange(d, req.Source)
	if err != nil {
		return err
	}
	sep := delimiterText(req)
	if sep == "" {
		return nil
	}
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		cell := sh.At(row, rect.FirstCol)
		if cell == nil || cell.UserEnteredValue == nil || cell.UserEnteredValue.StringValue == nil {
			continue
		}
		parts := strings.Split(*cell.UserEnteredValue.StringValue, sep)
		for i, part := range parts {
			sh.Set(row, rect.FirstCol+i, Str(part))
		}
	}
	return nil
}

func delimiterText(req *gsheets.TextToColumnsRequest) string {
	switch req.DelimiterType {
	case gsheets.DelimiterComma:
		return ","
	case gsheets.DelimiterSemicolon:
		return ";"
	case gsheets.DelimiterPeriod:
		return "."
	case gsheets.DelimiterSpace:
		return " "
	case gsheets.DelimiterCustom:
		return req.Delimiter
	}
	return ""
}

// copyPaste copies a rectangle somewhere else and leaves the source.
func copyPaste(d *Doc, req *gsheets.CopyPasteRequest) error {
	from, source, err := sheetForRange(d, req.Source)
	if err != nil {
		return err
	}
	to, destination, err := sheetForRange(d, req.Destination)
	if err != nil {
		return err
	}
	transpose := req.PasteOrientation == gsheets.PasteTranspose
	for i := 0; i < source.Rows(); i++ {
		for j := 0; j < source.Cols(); j++ {
			cell := from.At(source.FirstRow+i, source.FirstCol+j)
			row, col := destination.FirstRow+i, destination.FirstCol+j
			if transpose {
				row, col = destination.FirstRow+j, destination.FirstCol+i
			}
			to.Set(row, col, pasted(cell, to.At(row, col), req.PasteType))
		}
	}
	return nil
}

// pasted is what one cell becomes when another is pasted onto it, for
// the paste types that carry different halves of a cell.
func pasted(from, onto *gsheets.CellData, kind string) *gsheets.CellData {
	if from == nil {
		if kind == gsheets.PasteFormat {
			return onto
		}
		return nil
	}
	copied := *from
	switch kind {
	case gsheets.PasteValues:
		copied.UserEnteredFormat, copied.EffectiveFormat, copied.Note = nil, nil, ""
		if onto != nil {
			copied.UserEnteredFormat, copied.EffectiveFormat = onto.UserEnteredFormat, onto.EffectiveFormat
		}
	case gsheets.PasteFormat:
		kept := &gsheets.CellData{
			UserEnteredFormat: from.UserEnteredFormat, EffectiveFormat: from.EffectiveFormat,
		}
		if onto != nil {
			kept.UserEnteredValue, kept.EffectiveValue = onto.UserEnteredValue, onto.EffectiveValue
			kept.FormattedValue, kept.Note = onto.FormattedValue, onto.Note
		}
		return kept
	}
	return &copied
}

// cutPaste moves a rectangle and empties the cells it came from.
func cutPaste(d *Doc, req *gsheets.CutPasteRequest) error {
	from, source, err := sheetForRange(d, req.Source)
	if err != nil {
		return err
	}
	if req.Destination == nil {
		return errors.New("cutPaste needs a destination")
	}
	to := d.FindByID(req.Destination.SheetID)
	if to == nil {
		return errors.New("No sheet with id: " + strconv.Itoa(req.Destination.SheetID))
	}
	moved := take(from, source)
	for i := source.FirstRow; i <= source.LastRow; i++ {
		for j := source.FirstCol; j <= source.LastCol; j++ {
			delete(from.Cells, [2]int{i - 1, j - 1})
		}
	}
	for i, line := range moved {
		for j, cell := range line {
			if cell == nil {
				continue
			}
			to.Set(req.Destination.RowIndex+1+i, req.Destination.ColumnIndex+1+j, cell)
		}
	}
	return nil
}
