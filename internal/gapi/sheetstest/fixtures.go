package sheetstest

import (
	"fmt"
	"math"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// Sheet titles in the fixture. None is "Sheet1", and one is not ASCII,
// because Google names the first sheet in the account's language and a
// server that assumes otherwise fails on somebody else's spreadsheet.
const (
	FirstSheet     = "Vandel"
	SecondSheet    = "Ürväl"
	ApostropheName = "Yalmic's Bractal"
)

// Fixture builds the standard spreadsheet: one sheet with headings,
// numbers, a formula, an error cell, a note, a validation rule, a merge
// and a protected range; a second sheet whose title is not ASCII; and a
// third whose title carries an apostrophe and a space.
//
// It also carries the trap: a named range called Vandel, which is also
// the first sheet's title. An unquoted reference to Vandel resolves to
// the named range and reads a different rectangle without failing.
func Fixture() (*Doc, *gapi.File) {
	nums := Numbers(1, 60)
	first := &Sheet{Props: gsheets.SheetProperties{
		SheetID: 0, Title: FirstSheet, Index: gsheets.Ptr(0), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: 200, ColumnCount: 12, FrozenRowCount: 1},
	}}
	headings := []string{"Plimth", "Nardle", "Grivet", "Oblisk"}
	for i, h := range headings {
		first.Set(1, i+1, Str(h))
	}
	for r := range 20 {
		row := r + 2
		first.Set(row, 1, Str(fmt.Sprintf("%s-%02d", Vocabulary[r%len(Vocabulary)], r+1)))
		first.Set(row, 2, Num(nums[r], fmt.Sprintf("%.2f", nums[r])))
		first.Set(row, 3, Num(nums[r+20], fmt.Sprintf("%.2f", nums[r+20])))
		// A formula and its result render identically in a values read.
		total := math.Round((nums[r]+nums[r+20])*100) / 100
		first.Set(row, 4, Formula(fmt.Sprintf("=B%d+C%d", row, row), total, fmt.Sprintf("%.2f", total)))
	}
	// The three things a values read cannot show, one of each.
	WithNote(first.At(2, 1), "Quorbin reconciliation pending")
	WithValidation(first.At(3, 1), "Skerry", "Plimth", "Nardle")
	first.Set(22, 4, ErrorCell("=B22/C22", "DIVIDE_BY_ZERO", "Function DIVIDE parameter 2 cannot be zero."))
	first.Set(24, 1, Str("Umberly"))
	first.Set(24, 2, Bool(true))
	first.Merges = []*gsheets.GridRange{a1.Rect{FirstCol: 1, FirstRow: 26, LastCol: 3, LastRow: 26}.GridRange(0)}
	first.Set(26, 1, Str("Zephrin summary"))
	first.Protected = []*gsheets.ProtectedRange{{
		ProtectedRangeID:      11,
		Range:                 a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 1}.GridRange(0),
		Description:           "heading row",
		RequestingUserCanEdit: false,
	}}
	first.FilterViews = []*gsheets.FilterView{{
		FilterViewID: 21, Title: "Grivet over 500",
		Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 21}.GridRange(0),
	}}
	first.Tables = []*gsheets.Table{{
		TableID: "tbl-fixture-1", Name: "Oblisk",
		Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 4, LastRow: 21}.GridRange(0),
		ColumnProperties: []*gsheets.TableColumn{
			{ColumnIndex: 0, ColumnName: "Plimth", ColumnType: "TEXT"},
			{ColumnIndex: 1, ColumnName: "Nardle", ColumnType: "DOUBLE"},
		},
	}}

	second := &Sheet{Props: gsheets.SheetProperties{
		SheetID: 1837, Title: SecondSheet, Index: gsheets.Ptr(1), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: 50, ColumnCount: 8},
	}}
	second.Set(1, 1, Str("Trennow"))
	second.Set(1, 2, Str("Bractal"))
	second.Set(2, 1, Str("Skerry"))
	second.Set(2, 2, Num(42, "42"))
	// An interior gap: row 3 column 1 is empty and column 2 is not, so
	// the response comes back ragged rather than rectangular.
	second.Set(3, 2, Num(7.5, "7.50"))

	third := &Sheet{Props: gsheets.SheetProperties{
		SheetID: 2914, Title: ApostropheName, Index: gsheets.Ptr(2), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: 20, ColumnCount: 4},
		Hidden:         true,
	}}
	third.Set(1, 1, Str("Nardle"))

	doc := &Doc{
		ID: FixtureID, Title: "Quorbin Skerry", Locale: "en_GB",
		TimeZone: "Etc/GMT", AutoRecalc: "ON_CHANGE",
		Sheets: []*Sheet{first, second, third},
		NamedRanges: []*gsheets.NamedRange{{
			NamedRangeID: "nr-fixture-1",
			// Deliberately the same name as the first sheet.
			Name:  FirstSheet,
			Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 2}.GridRange(1837),
		}},
	}
	file := &gapi.File{
		ID: FixtureID, Name: doc.Title, MimeType: gapi.SpreadsheetMimeType,
		CreatedTime: "2026-01-04T09:00:00.000Z", ModifiedTime: "2026-03-11T14:25:00.000Z",
		Owners:      []*gapi.User{{DisplayName: "Fixture Account", EmailAddress: "fixture@example.test"}},
		WebViewLink: gapi.SpreadsheetURL(FixtureID),
	}
	return doc, file
}

// Second is a second spreadsheet whose title starts the same way as the
// first, so a title lookup has two candidates and has to say so rather
// than take one.
func Second() (*Doc, *gapi.File) {
	sh := &Sheet{Props: gsheets.SheetProperties{
		SheetID: 0, Title: "Grivet", Index: gsheets.Ptr(0), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: 100, ColumnCount: 6},
	}}
	sh.Set(1, 1, Str("Plimth"))
	sh.Set(2, 1, Num(11, "11"))
	doc := &Doc{
		ID: SecondFixtureID, Title: "Quorbin Skerry archive", Locale: "en_GB",
		TimeZone: "Etc/GMT", AutoRecalc: "ON_CHANGE", Sheets: []*Sheet{sh},
	}
	file := &gapi.File{
		ID: SecondFixtureID, Name: doc.Title, MimeType: gapi.SpreadsheetMimeType,
		CreatedTime: "2025-11-02T08:00:00.000Z", ModifiedTime: "2026-02-01T10:00:00.000Z",
		Owners:      []*gapi.User{{DisplayName: "Fixture Account", EmailAddress: "fixture@example.test"}},
		WebViewLink: gapi.SpreadsheetURL(SecondFixtureID),
	}
	return doc, file
}

// NotASpreadsheet is a Drive file of another type. The search query has
// to exclude it, and it is how a test can tell a working mimeType filter
// from one an apostrophe detached.
func NotASpreadsheet() *gapi.File {
	return &gapi.File{
		ID: "1SyntheticFixtureOtherFileIdZZZZZZZZZZZZZZZZZ", Name: "Quorbin notes",
		MimeType: "application/vnd.google-apps.document", ModifiedTime: "2026-04-01T00:00:00.000Z",
		Owners: []*gapi.User{{DisplayName: "Fixture Account", EmailAddress: "fixture@example.test"}},
	}
}

// Large builds a spreadsheet big enough that a read has to budget: one
// sheet of rows by cols, filled from a seeded generator.
func Large(rows, cols int) (*Doc, *gapi.File) {
	sh := &Sheet{Props: gsheets.SheetProperties{
		SheetID: 0, Title: "Bractal", Index: gsheets.Ptr(0), SheetType: "GRID",
		GridProperties: &gsheets.GridProperties{RowCount: rows, ColumnCount: cols},
	}}
	nums := Numbers(9, rows*cols)
	for c := range cols {
		sh.Set(1, c+1, Str(Vocabulary[c%len(Vocabulary)]))
	}
	for r := 1; r < rows; r++ {
		for c := range cols {
			v := nums[r*cols+c]
			sh.Set(r+1, c+1, Num(v, fmt.Sprintf("%.2f", v)))
		}
	}
	id := "1SyntheticFixtureLargeSheetIdXXXXXXXXXXXXXXXX"
	doc := &Doc{
		ID: id, Title: "Trennow bulk", Locale: "en_GB", TimeZone: "Etc/GMT",
		AutoRecalc: "ON_CHANGE", Sheets: []*Sheet{sh},
	}
	file := &gapi.File{
		ID: id, Name: doc.Title, MimeType: gapi.SpreadsheetMimeType,
		ModifiedTime: "2026-05-05T05:05:00.000Z",
		Owners:       []*gapi.User{{DisplayName: "Fixture Account", EmailAddress: "fixture@example.test"}},
		WebViewLink:  gapi.SpreadsheetURL(id),
	}
	return doc, file
}

// Standard starts a fake carrying the whole fixture set.
func Standard(t *testing.T) *Server {
	t.Helper()
	s := New(t)
	s.Add(Fixture())
	s.Add(Second())
	s.AddFile(NotASpreadsheet())
	return s
}
