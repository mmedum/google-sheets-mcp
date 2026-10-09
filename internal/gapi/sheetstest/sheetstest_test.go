package sheetstest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

func TestFixtureShape(t *testing.T) {
	doc, file := Fixture()
	if doc.ID != FixtureID || file.ID != FixtureID {
		t.Fatalf("ids disagree: %q and %q", doc.ID, file.ID)
	}
	if len(doc.Sheets) != 3 {
		t.Fatalf("the fixture has %d sheets", len(doc.Sheets))
	}
	// No sheet is called Sheet1, one title is not ASCII and one carries
	// an apostrophe: between them they cover the three ways a naive
	// server builds a range that does not parse.
	for _, sh := range doc.Sheets {
		if sh.Props.Title == "Sheet1" {
			t.Error("the fixture teaches the model that Sheet1 exists")
		}
	}
	if doc.Find(SecondSheet) == nil || doc.Find(ApostropheName) == nil {
		t.Error("the awkward titles are missing")
	}
	if doc.FindByID(1837) == nil || doc.FindByID(99) != nil {
		t.Error("FindByID is wrong")
	}
	// The trap: a named range with a sheet's name.
	if len(doc.NamedRanges) != 1 || doc.NamedRanges[0].Name != FirstSheet {
		t.Error("the named range that shadows a sheet is missing")
	}
}

func TestNumbersAreDeterministic(t *testing.T) {
	a, b := Numbers(7, 20), Numbers(7, 20)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("the generator is not seeded: %v vs %v", a, b)
		}
	}
	if c := Numbers(8, 20); c[0] == a[0] {
		t.Error("two seeds produced the same first value")
	}
}

func TestTrailingEmptiesAreOmitted(t *testing.T) {
	// The API omits trailing empty rows and trailing empty cells, and a
	// ragged response is normal. A fake that returned neat rectangles
	// would let a padding bug through.
	s := Standard(t)
	vr, err := s.Client().GetValues(context.Background(), FixtureID, "'"+SecondSheet+"'!A1:C10", gapi.ValueOptions{
		Render: gapi.RenderUnformatted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vr.Values) != 3 {
		t.Fatalf("got %d rows, want 3 (rows 4 to 10 are empty and are omitted)", len(vr.Values))
	}
	if len(vr.Values[0]) != 2 || len(vr.Values[2]) != 2 {
		t.Errorf("rows are %d and %d wide; trailing empties are omitted per row", len(vr.Values[0]), len(vr.Values[2]))
	}
	// An interior gap comes back as an empty string, not as a missing
	// element that would shift every column after it.
	if vr.Values[2][0] != "" {
		t.Errorf("the interior gap is %q, want an empty string", vr.Values[2][0])
	}
}

func TestRenderOptions(t *testing.T) {
	s := Standard(t)
	ctx := context.Background()
	for _, tc := range []struct {
		render string
		want   string
	}{
		{gapi.RenderFormula, "=B2+C2"},
		{gapi.RenderFormatted, "663.19"},
	} {
		vr, err := s.Client().GetValues(ctx, FixtureID, "'"+FirstSheet+"'!D2:D2", gapi.ValueOptions{Render: tc.render})
		if err != nil {
			t.Fatal(err)
		}
		if got := vr.Values[0][0]; got != tc.want {
			t.Errorf("%s gave %v, want %q", tc.render, got, tc.want)
		}
	}
	// An error cell renders as its display text, not as a struct.
	vr, err := s.Client().GetValues(ctx, FixtureID, "'"+FirstSheet+"'!D22:D22", gapi.ValueOptions{Render: gapi.RenderUnformatted})
	if err != nil {
		t.Fatal(err)
	}
	if got := vr.Values[0][0]; got != "#DIV/0!" {
		t.Errorf("an error cell rendered as %v", got)
	}
}

func TestBatchGetAndBadRange(t *testing.T) {
	s := Standard(t)
	ctx := context.Background()
	res, err := s.Client().BatchGetValues(ctx, FixtureID,
		[]string{"'" + FirstSheet + "'!A1:B2", "'" + SecondSheet + "'!A1:B2"}, gapi.ValueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ValueRanges) != 2 {
		t.Fatalf("got %d ranges", len(res.ValueRanges))
	}
	// The message a shipped server hit on a non-English account, kept
	// verbatim so the class mapping is tested against the real text.
	_, err = s.Client().GetValues(ctx, FixtureID, "Sheet1!A1:D3", gapi.ValueOptions{})
	if !errors.Is(err, gapi.ErrInvalid) || !strings.Contains(err.Error(), "Unable to parse range") {
		t.Errorf("a missing sheet gave %v", err)
	}
}

func TestUnmaskedGetIsRefused(t *testing.T) {
	s := Standard(t)
	// The fake refuses what this server refuses to send, so a call site
	// that dropped its field mask fails in tests rather than in
	// somebody's ten-million-cell spreadsheet.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+"/v4/spreadsheets/"+FixtureID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unmasked get returned %d", resp.StatusCode)
	}
}

func TestFailureInjection(t *testing.T) {
	s := Standard(t)
	ctx := context.Background()

	s.FailOnce("spreadsheets.get", Failure{Status: http.StatusServiceUnavailable})
	if _, err := s.Client().GetSpreadsheet(ctx, FixtureID, gapi.GetOptions{Fields: gapi.CardFields}); err != nil {
		t.Fatalf("a single 503 should have been retried: %v", err)
	}

	s.Fail("drive.files.list", Failure{Status: http.StatusTooManyRequests})
	_, err := s.Client().SearchSpreadsheets(ctx, "name contains 'x'", 5, "")
	if !errors.Is(err, gapi.ErrRateLimited) {
		t.Errorf("a persistent 429 gave %v", err)
	}
	s.Clear("drive.files.list")

	// A cut connection is not a status code, and the client has to tell
	// the two apart.
	s.Fail("drive.about.get", Failure{Cut: true})
	if _, err := s.Client().About(ctx); !errors.Is(err, gapi.ErrUnavailable) {
		t.Errorf("a cut connection gave %v", err)
	}
	s.Clear("drive.about.get")

	s.Fail("drive.about.get", Failure{Status: http.StatusOK, Body: `{}`, Delay: 10 * time.Millisecond})
	if _, err := s.Client().About(ctx); err == nil {
		t.Error("an about with no user was accepted")
	}
}

func TestCallsAreRecordedWithTheirMethod(t *testing.T) {
	s := Standard(t)
	s.Reset()
	if _, err := s.Client().About(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := s.Calls()
	if len(calls) != 1 || calls[0].Op != "drive.about.get" || calls[0].Method != http.MethodGet {
		t.Errorf("calls = %+v", calls)
	}
}

func TestDriveQueryParser(t *testing.T) {
	for _, tc := range []struct {
		q  string
		ok bool
	}{
		{"", true},
		{"mimeType = 'application/vnd.google-apps.spreadsheet'", true},
		{"mimeType = 'x' and trashed = false", true},
		{"name contains 'Quorbin' and mimeType = 'x'", true},
		{`name contains 'Yalmic\'s' and mimeType = 'x'`, true},
		{"'someone@example.test' in owners", true},
		{"modifiedTime > '2026-01-01T00:00:00Z'", true},
		// What an unescaped apostrophe looks like: the string closes
		// early and the rest of the query is nonsense.
		{"name contains 'Yalmic's' and mimeType = 'x'", false},
		{"name contains 'unterminated", false},
		{"name sounds_like 'x'", false},
		{"name contains 'x' or mimeType = 'y'", false},
		{"name contains", false},
	} {
		_, err := parseDriveQuery(tc.q)
		if tc.ok && err != nil {
			t.Errorf("parseDriveQuery(%q) = %v", tc.q, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseDriveQuery(%q) was accepted", tc.q)
		}
	}
}

// TestAnUnescapedApostropheDetachesTheFilter is the shape of the bug
// this fake exists to make visible. With the mimeType clause gone, a
// Drive search returns files of any type.
func TestAnUnescapedApostropheDetachesTheFilter(t *testing.T) {
	s := Standard(t)
	ctx := context.Background()

	// Escaped, as the server does it: the filter survives and only
	// spreadsheets come back.
	q := "mimeType = " + gapi.QuoteDriveValue(gapi.SpreadsheetMimeType) + " and name contains " + gapi.QuoteDriveValue("Quorbin")
	list, err := s.Client().SearchSpreadsheets(ctx, q, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range list.Files {
		if f.MimeType != gapi.SpreadsheetMimeType {
			t.Errorf("a %s came back through an escaped query", f.MimeType)
		}
	}

	// Without the filter, everything does — which is what the shipped
	// bug produced.
	list, err = s.Client().SearchSpreadsheets(ctx, "name contains 'Quorbin'", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range list.Files {
		if f.MimeType != gapi.SpreadsheetMimeType {
			found = true
		}
	}
	if !found {
		t.Error("the fake does not distinguish a query with the mimeType filter from one without it")
	}
}

func TestLargeFixture(t *testing.T) {
	doc, file := Large(100, 5)
	if len(doc.Sheets) != 1 || doc.Sheets[0].Props.GridProperties.RowCount != 100 {
		t.Fatalf("Large gave %+v", doc.Sheets[0].Props)
	}
	if doc.Sheets[0].At(100, 5) == nil {
		t.Error("the last cell is empty")
	}
	if file.ID != doc.ID {
		t.Error("the file and the document disagree about the id")
	}
}

func TestCellConstructors(t *testing.T) {
	if got := Str("Quorbin").FormattedValue; got != "Quorbin" {
		t.Errorf("Str = %q", got)
	}
	if got := Num(12.5, "").FormattedValue; got != "12.5" {
		t.Errorf("Num with no format = %q", got)
	}
	if got := Bool(false).FormattedValue; got != "FALSE" {
		t.Errorf("Bool = %q", got)
	}
	if got := Formula("=A1", 3, "").FormattedValue; got != "3" {
		t.Errorf("Formula with no format = %q", got)
	}
	for kind, want := range map[string]string{"REF": "#REF!", "N_A": "#N/A", "WHAT": "#WHAT"} {
		if got := ErrorCell("=x", kind, "").FormattedValue; got != want {
			t.Errorf("ErrorCell(%q) = %q, want %q", kind, got, want)
		}
	}
}

// The API applies nothing when any request in a batch is invalid, and
// the fake has to do the same: applying in order and stopping at the
// first failure leaves a state the real API never produces, and a test
// written against it would agree with the wrong thing.
func TestBatchUpdateAppliesNothingWhenOneRequestFails(t *testing.T) {
	srv := Standard(t)
	client := srv.Client()
	ctx := context.Background()

	_, err := client.BatchUpdate(ctx, FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{
			// Valid, and it would apply first.
			{AddSheet: &gsheets.AddSheetRequest{Properties: &gsheets.NewSheetProperties{Title: "Trennow"}}},
			// Invalid: no sheet has this id.
			{DeleteSheet: &gsheets.DeleteSheetRequest{SheetID: 987654}},
		},
	})
	if err == nil {
		t.Fatal("a batch with an invalid request was accepted")
	}
	if sh := srv.Doc(FixtureID).Find("Trennow"); sh != nil {
		t.Error("the first request of a failed batch was applied; the API applies none of them")
	}
}

// TestConditionalRulesAreChecked holds the fake to the refusals spike S
// saw Google make of a color scale, in Google's words, so no test passes
// on a color scale Google refuses. A rule of neither kind is refused in
// the fake's own words, since nothing asked Google.
func TestConditionalRulesAreChecked(t *testing.T) {
	point := func(kind, value string) *gsheets.InterpolationPoint {
		return &gsheets.InterpolationPoint{
			Type: kind, Value: value,
			ColorStyle: &gsheets.ColorStyle{RGBColor: &gsheets.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1}},
		}
	}
	condition := &gsheets.BooleanRule{
		Condition: &gsheets.BooleanCondition{Type: "NOT_BLANK"},
		Format:    &gsheets.CellFormat{TextFormat: &gsheets.TextFormat{Bold: true}},
	}
	for _, tc := range []struct {
		name string
		rule gsheets.ConditionalFormatRule
		want string // "" for accepted
	}{
		{"a color scale with a midpoint",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{
				Minpoint: point("MIN", ""), Midpoint: point("PERCENTILE", "50"), Maxpoint: point("MAX", ""),
			}}, ""},
		{"neither kind", gsheets.ConditionalFormatRule{},
			"exactly one of booleanRule and gradientRule is required"},
		{"both kinds",
			gsheets.ConditionalFormatRule{BooleanRule: condition, GradientRule: &gsheets.GradientRule{
				Minpoint: point("MIN", ""), Maxpoint: point("MAX", ""),
			}}, "Invalid value at 'requests[0].add_conditional_format_rule.rule' (oneof), oneof field 'rule' is " +
				"already set. Cannot set 'gradientRule'"},
		{"no maxpoint",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{Minpoint: point("MIN", "")}},
			"Invalid requests[0].addConditionalFormatRule: No interpolationPointType specified."},
		{"no minpoint",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{Maxpoint: point("MAX", "")}},
			"Invalid requests[0].addConditionalFormatRule: No interpolationPointType specified."},
		{"a number with no value",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{
				Minpoint: point("NUMBER", ""), Maxpoint: point("MAX", ""),
			}}, "Invalid requests[0].addConditionalFormatRule: InterpolationPoint.value is required."},
		{"a percentile midpoint with no value",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{
				Minpoint: point("MIN", ""), Midpoint: point("PERCENTILE", ""), Maxpoint: point("MAX", ""),
			}}, "Invalid requests[0].addConditionalFormatRule: InterpolationPoint.value is required."},
		{"a point with no type",
			gsheets.ConditionalFormatRule{GradientRule: &gsheets.GradientRule{
				Minpoint: point("", "1"), Maxpoint: point("MAX", ""),
			}}, "Invalid requests[0].addConditionalFormatRule: No interpolationPointType specified."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := Standard(t)
			rule := tc.rule
			rule.Ranges = []*gsheets.GridRange{{SheetID: 0}}
			_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
				Requests: []*gsheets.Request{{AddConditionalFormatRule: &gsheets.AddConditionalFormatRuleRequest{
					Index: 0, Rule: &rule,
				}}},
			})
			stored := srv.Doc(FixtureID).Find(FirstSheet).Conditional
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if len(stored) != 2 || stored[0].GradientRule == nil || stored[0].GradientRule.Midpoint.Value != "50" {
					t.Errorf("the color scale was not stored first: %+v", stored)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to say %q", err, tc.want)
			}
			if len(stored) != 1 {
				t.Errorf("a refused rule was stored: %d rules", len(stored))
			}
		})
	}
}

// TestAColorScaleIsStoredAsGoogleStoresIt is spike S: a value sent on a
// min point is dropped (Q3), and under de_DE a number with a decimal
// point is refused while one with a decimal comma is kept as written
// (Q4).
func TestAColorScaleIsStoredAsGoogleStoresIt(t *testing.T) {
	white := &gsheets.ColorStyle{RGBColor: &gsheets.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1}}
	add := func(srv *Server, minpoint *gsheets.InterpolationPoint) error {
		minpoint.ColorStyle = white
		_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
			Requests: []*gsheets.Request{{AddConditionalFormatRule: &gsheets.AddConditionalFormatRuleRequest{
				Rule: &gsheets.ConditionalFormatRule{
					Ranges: []*gsheets.GridRange{{SheetID: 0}},
					GradientRule: &gsheets.GradientRule{
						Minpoint: minpoint, Maxpoint: &gsheets.InterpolationPoint{Type: "MAX", ColorStyle: white},
					},
				},
			}}},
		})
		return err
	}
	srv := Standard(t)
	if err := add(srv, &gsheets.InterpolationPoint{Type: "MIN", Value: "2"}); err != nil {
		t.Fatalf("a min point with a value was refused: %v", err)
	}
	if got := srv.Doc(FixtureID).Find(FirstSheet).Conditional[0].GradientRule.Minpoint.Value; got != "" {
		t.Errorf("the min point kept the value %q", got)
	}

	srv = Standard(t)
	srv.Doc(FixtureID).Locale = "de_DE"
	const want = "Invalid requests[0].addConditionalFormatRule: Invalid InterpolationPoint.value: 1.5"
	if err := add(srv, &gsheets.InterpolationPoint{Type: "NUMBER", Value: "1.5"}); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("1.5 under de_DE: error = %v, want it to say %q", err, want)
	}
	if err := add(srv, &gsheets.InterpolationPoint{Type: "NUMBER", Value: "1,5"}); err != nil {
		t.Fatalf("1,5 under de_DE was refused: %v", err)
	}
	if got := srv.Doc(FixtureID).Find(FirstSheet).Conditional[0].GradientRule.Minpoint.Value; got != "1,5" {
		t.Errorf("1,5 was stored as %q", got)
	}
}

// TestTableColumnsAreChecked holds the fake to what §18 records about a
// table's columns on add: Google's refusals of a dropdown with no list
// and a list on another type (spike T Q2), and the fake's own of the
// rest.
func TestTableColumnsAreChecked(t *testing.T) {
	list := &gsheets.TableColumnDataValidationRule{Condition: &gsheets.BooleanCondition{
		Type: "ONE_OF_LIST", Values: []*gsheets.ConditionValue{{UserEnteredValue: "Open"}},
	}}
	for _, tc := range []struct {
		name    string
		columns []*gsheets.TableColumn
		want    string // "" for accepted
	}{
		{"a dropdown with its list",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: gsheets.ColumnDropdown, DataValidationRule: list}}, ""},
		{"a dropdown with no list",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: gsheets.ColumnDropdown}},
			"Invalid requests[0].addTable: Condition must be set for dropdown column type."},
		{"a list on a number column",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: gsheets.ColumnDouble, DataValidationRule: list}},
			"Invalid requests[0].addTable: Cannot set condition for non-dropdown column type."},
		{"a list that is not one of a list",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: gsheets.ColumnDropdown,
				DataValidationRule: &gsheets.TableColumnDataValidationRule{Condition: &gsheets.BooleanCondition{Type: "NOT_BLANK"}}}},
			"the condition must be ONE_OF_LIST with values"},
		{"a type the enum lacks",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: "MONEY"}}, `Invalid value at 'column_type': "MONEY"`},
		{"a column past the table",
			[]*gsheets.TableColumn{{ColumnIndex: 2, ColumnType: gsheets.ColumnDate}}, "column index 2 is outside the table"},
		{"a column twice",
			[]*gsheets.TableColumn{{ColumnIndex: 1, ColumnType: gsheets.ColumnDate}, {ColumnIndex: 1, ColumnType: gsheets.ColumnTime}},
			"column index 1 is given twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := Standard(t)
			_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
				Requests: []*gsheets.Request{{AddTable: &gsheets.AddTableRequest{Table: &gsheets.Table{
					Name: "Trennow", Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}.GridRange(1837),
					ColumnProperties: tc.columns,
				}}}},
			})
			stored := srv.Doc(FixtureID).Find(SecondSheet).Tables
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				// As spike T Q1 read back: an entry for every column, the
				// one left out named by its header, and the typed one sent
				// with no name named "Column 1", over its header cell.
				columns, _ := json.Marshal(stored[0].ColumnProperties)
				const want = `[{"columnName":"Trennow"},{"columnIndex":1,"columnName":"Column 1","columnType":"DROPDOWN",` +
					`"dataValidationRule":{"condition":{"type":"ONE_OF_LIST","values":[{"userEnteredValue":"Open"}]}}}]`
				if string(columns) != want {
					t.Errorf("stored columns =\n%s\nwant\n%s", columns, want)
				}
				if got := srv.Doc(FixtureID).Find(SecondSheet).At(1, 2).FormattedValue; got != "Column 1" {
					t.Errorf("the header cell B1 reads %q, want Column 1", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to say %q", err, tc.want)
			}
			if len(stored) != 0 {
				t.Error("a refused table was stored")
			}
		})
	}
}

// TestATableHeaderTakesWhatIsWrittenIntoIt is spike T as Google
// answered it: text written into a table's header cell names the
// column, and a formula there is replaced by "Column" and the column's
// place in the table, which names it too (Q7).
func TestATableHeaderTakesWhatIsWrittenIntoIt(t *testing.T) {
	srv := Standard(t)
	ctx := context.Background()
	c := srv.Client()
	_, err := c.BatchUpdate(ctx, FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{AddTable: &gsheets.AddTableRequest{Table: &gsheets.Table{
			Name: "Trennow", Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 2, LastRow: 3}.GridRange(1837),
		}}}},
	})
	if err != nil {
		t.Fatalf("addTable: %v", err)
	}
	for _, write := range []struct{ cell, value string }{{"A1", "Quorbin"}, {"B1", "=1+2"}} {
		if _, err := c.UpdateValues(ctx, FixtureID, "'"+SecondSheet+"'!"+write.cell, [][]any{{write.value}},
			gapi.WriteOptions{Input: gapi.InputUserEntered}); err != nil {
			t.Fatalf("writing %s: %v", write.cell, err)
		}
	}
	sh := srv.Doc(FixtureID).Find(SecondSheet)
	if got := sh.At(1, 2); got.UserEnteredValue.FormulaValue != nil || got.FormattedValue != "Column 2" {
		t.Errorf("B1 holds %+v, want the text Column 2", got.UserEnteredValue)
	}
	columns, _ := json.Marshal(sh.Tables[0].ColumnProperties)
	if string(columns) != `[{"columnName":"Quorbin"},{"columnIndex":1,"columnName":"Column 2"}]` {
		t.Errorf("columns = %s", columns)
	}
}

// TestUpdateTableReplacesTheColumnsWhole is spike T Q3b: one column sent
// alone, named, replaces the whole list. The columns left out keep their
// headers' text as their names and lose their types, a dropdown its list
// with it. The name sent is written into its header cell (Q4).
func TestUpdateTableReplacesTheColumnsWhole(t *testing.T) {
	srv := Standard(t)
	sh := srv.Doc(FixtureID).Find(FirstSheet)
	// The fixture's table over A1:D21, with Oblisk made a dropdown.
	sh.Tables[0].ColumnProperties = append(sh.Tables[0].ColumnProperties, &gsheets.TableColumn{
		ColumnIndex: 3, ColumnName: "Oblisk", ColumnType: gsheets.ColumnDropdown,
		DataValidationRule: &gsheets.TableColumnDataValidationRule{Condition: &gsheets.BooleanCondition{
			Type: "ONE_OF_LIST", Values: []*gsheets.ConditionValue{{UserEnteredValue: "Open"}},
		}},
	})
	_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{UpdateTable: &gsheets.UpdateTableRequest{
			Table: &gsheets.Table{TableID: "tbl-fixture-1", ColumnProperties: []*gsheets.TableColumn{
				{ColumnIndex: 1, ColumnName: "Quorbin", ColumnType: gsheets.ColumnCurrency},
			}},
			Fields: "columnProperties",
		}}},
	})
	if err != nil {
		t.Fatalf("updateTable: %v", err)
	}
	sh = srv.Doc(FixtureID).Find(FirstSheet)
	columns, _ := json.Marshal(sh.Tables[0].ColumnProperties)
	const want = `[{"columnName":"Plimth"},{"columnIndex":1,"columnName":"Quorbin","columnType":"CURRENCY"},` +
		`{"columnIndex":2,"columnName":"Grivet"},{"columnIndex":3,"columnName":"Oblisk"}]`
	if string(columns) != want {
		t.Errorf("columns after the update =\n%s\nwant\n%s", columns, want)
	}
	if header := sh.At(1, 2).FormattedValue; header != "Quorbin" {
		t.Errorf("the header cell B1 reads %q, want Quorbin", header)
	}
}

// TestABooleanColumnTurnsTextAndEmptyCellsFalse is spike T Q5: typing a
// column boolean turned a word, the text "TRUE" and an empty cell into
// FALSE. A cell already TRUE is believed kept, which spike T still asks.
func TestABooleanColumnTurnsTextAndEmptyCellsFalse(t *testing.T) {
	srv := Standard(t)
	sh := srv.Doc(FixtureID).Find(SecondSheet)
	sh.Set(1, 3, Str("Flag")).Set(2, 3, Str("maybe")).Set(3, 3, Str("TRUE")).Set(5, 3, Bool(true))
	ctx := context.Background()
	c := srv.Client()
	if _, err := c.BatchUpdate(ctx, FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{AddTable: &gsheets.AddTableRequest{Table: &gsheets.Table{
			Name: "Trennow", Range: a1.Rect{FirstCol: 1, FirstRow: 1, LastCol: 3, LastRow: 5}.GridRange(1837),
		}}}},
	}); err != nil {
		t.Fatalf("addTable: %v", err)
	}
	if _, err := c.BatchUpdate(ctx, FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{UpdateTable: &gsheets.UpdateTableRequest{
			Table: &gsheets.Table{TableID: "tbl1", ColumnProperties: []*gsheets.TableColumn{
				{ColumnIndex: 0, ColumnName: "Trennow"}, {ColumnIndex: 1, ColumnName: "Bractal"},
				{ColumnIndex: 2, ColumnName: "Flag", ColumnType: gsheets.ColumnBoolean},
			}},
			Fields: "columnProperties",
		}}},
	}); err != nil {
		t.Fatalf("updateTable: %v", err)
	}
	sh = srv.Doc(FixtureID).Find(SecondSheet)
	var got []string
	for row := 2; row <= 5; row++ {
		got = append(got, sh.At(row, 3).FormattedValue)
	}
	if strings.Join(got, ", ") != "FALSE, FALSE, FALSE, TRUE" {
		t.Errorf("C2:C5 read %q, want FALSE, FALSE, FALSE, TRUE", got)
	}
	if v := sh.At(3, 3).UserEnteredValue; v.BoolValue == nil || v.StringValue != nil {
		t.Errorf("C3 holds %+v, want the value FALSE rather than text", v)
	}
}

// TestANameWrittenIntoAHeaderDropsItsRichText is spike T Q7: an update
// sending "Flag" back as read left the cell holding "Flag" with no runs,
// where two of its letters had been bold. Its whole-cell format is
// believed to stay, which spike T still asks.
func TestANameWrittenIntoAHeaderDropsItsRichText(t *testing.T) {
	srv := Standard(t)
	sh := srv.Doc(FixtureID).Find(FirstSheet)
	sh.At(1, 2).TextFormatRuns = []json.RawMessage{
		json.RawMessage(`{"format":{"bold":true}}`), json.RawMessage(`{"startIndex":2,"format":{}}`),
	}
	_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{UpdateTable: &gsheets.UpdateTableRequest{
			Table: &gsheets.Table{TableID: "tbl-fixture-1", ColumnProperties: []*gsheets.TableColumn{
				{ColumnIndex: 0, ColumnName: "Plimth"}, {ColumnIndex: 1, ColumnName: "Nardle"},
				{ColumnIndex: 2, ColumnName: "Grivet"}, {ColumnIndex: 3, ColumnName: "Oblisk"},
			}},
			Fields: "columnProperties",
		}}},
	})
	if err != nil {
		t.Fatalf("updateTable: %v", err)
	}
	b1 := srv.Doc(FixtureID).Find(FirstSheet).At(1, 2)
	if b1.FormattedValue != "Nardle" || b1.TextFormatRuns != nil {
		t.Errorf("B1 reads %q with runs %s, want Nardle with none", b1.FormattedValue, b1.TextFormatRuns)
	}
	if f := b1.UserEnteredFormat; f == nil || f.TextFormat == nil || !f.TextFormat.Bold {
		t.Errorf("B1 lost its whole-cell bold: %+v", f)
	}
}

// TestUpdateTableRefusesAnEntryWithNoName is spike T Q3 and Q5: Google
// refuses an update entry that carries no name, in these words.
func TestUpdateTableRefusesAnEntryWithNoName(t *testing.T) {
	srv := Standard(t)
	_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{UpdateTable: &gsheets.UpdateTableRequest{
			Table: &gsheets.Table{TableID: "tbl-fixture-1", ColumnProperties: []*gsheets.TableColumn{
				{ColumnIndex: 0, ColumnName: "Plimth", ColumnType: gsheets.ColumnText},
				{ColumnIndex: 1, ColumnType: gsheets.ColumnDate},
			}},
			Fields: "columnProperties",
		}}},
	})
	const want = "Invalid requests[0].updateTable: Table header row cell must have a value."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to say %q", err, want)
	}
	if got := srv.Doc(FixtureID).Find(FirstSheet).Tables[0].ColumnProperties[1].ColumnType; got != gsheets.ColumnDouble {
		t.Errorf("a refused update changed the columns: Nardle is %s", got)
	}
}

// salesSource is the pivot source over the sales block at A10:E16 of
// the second sheet, which salesSheet writes.
const salesSource = `"source":{"sheetId":1837,"startRowIndex":9,"endRowIndex":16,"startColumnIndex":0,"endColumnIndex":5}`

// salesSheet puts the sales block on the second sheet, at A10, and
// widens the sheet so a pivot anchored at H10 has room.
func salesSheet(t *testing.T) *Server {
	t.Helper()
	srv := Standard(t)
	sh := srv.Doc(FixtureID).Find(SecondSheet)
	sh.Props.GridProperties.ColumnCount = 20
	SalesBlock(sh, 10)
	return srv
}

// writePivotAt anchors a pivot at H10 of the second sheet through the
// client, as manage_pivot_table sends one.
func writePivotAt(srv *Server, pivot string) error {
	_, err := srv.Client().BatchUpdate(context.Background(), FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{UpdateCells: &gsheets.UpdateCellsRequest{
			Start:  &gsheets.GridCoordinate{SheetID: 1837, RowIndex: 9, ColumnIndex: 7},
			Rows:   []*gsheets.RowData{{Values: []*gsheets.CellData{{PivotTable: []byte(pivot)}}}},
			Fields: "pivotTable",
		}}},
	})
	return err
}

// drawnFrom is what the pivot at H10 drew, one line a row, cells
// separated by " | ", down to its first empty row.
func drawnFrom(srv *Server) string {
	sh := srv.Doc(FixtureID).Find(SecondSheet)
	var lines []string
	for r := 10; ; r++ {
		var cells []string
		for c := 8; c <= 12; c++ {
			if cell := sh.At(r, c); cell != nil && cell.FormattedValue != "" {
				cells = append(cells, cell.FormattedValue)
			}
		}
		if len(cells) == 0 {
			return strings.Join(lines, "\n")
		}
		lines = append(lines, strings.Join(cells, " | "))
	}
}

// TestPivotDrawsFiltersRulesAndCalculatedValues is the fake's model of
// what the reference documents: the filter keeps East and West before
// anything is summed, YEAR_MONTH labels as the enum's description shows,
// a SUM formula is worked out per row and summed, and a CUSTOM one once
// per group.
func TestPivotDrawsFiltersRulesAndCalculatedValues(t *testing.T) {
	srv := salesSheet(t)
	err := writePivotAt(srv, `{`+salesSource+`,`+
		`"rows":[{"sourceColumnOffset":1,"showTotals":true,"sortOrder":"ASCENDING","groupRule":{"dateTimeRule":{"type":"YEAR_MONTH"}}}],`+
		`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"},`+
		`{"formula":"=Revenue-Cost","summarizeFunction":"SUM","name":"Margin"},`+
		`{"formula":"=SUM(Revenue)/SUM(Cost)","summarizeFunction":"CUSTOM","name":"Ratio"}],`+
		`"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"visibleValues":["East","West"]}}]}`)
	if err != nil {
		t.Fatalf("the pivot was refused: %v", err)
	}
	const want = "Day | SUM of Revenue | Margin | Ratio\n" +
		"2026-Jan | 100 | 40 | 1.6666666666666667\n" +
		"2026-Feb | 250 | 90 | 1.5625\n" +
		"2026-Apr | 100 | 30 | 1.4285714285714286\n" +
		"Grand Total | 450 | 160 | 1.5517241379310345"
	if got := drawnFrom(srv); got != want {
		t.Errorf("drawn:\n%s\nwant:\n%s", got, want)
	}
}

// TestPivotDrawsAHistogramAsGoogleDoes is spike U4's labels: every 20
// from 25 to 70 drew "< 25", "25 - 44", "45 - 64" and "65 - 70", the
// last holding the end itself. Ending at 68, the one age of 68 is the
// end, and stays in the last bucket rather than going past it.
func TestPivotDrawsAHistogramAsGoogleDoes(t *testing.T) {
	for end, want := range map[string]string{
		"70": "Age | SUM of Revenue\n< 25 | 100\n25 - 44 | 270\n45 - 64 | 80\n65 - 70 | 300\nGrand Total | 750",
		"68": "Age | SUM of Revenue\n< 25 | 100\n25 - 44 | 270\n45 - 64 | 80\n65 - 68 | 300\nGrand Total | 750",
		"60": "Age | SUM of Revenue\n< 25 | 100\n25 - 44 | 270\n45 - 60 | 80\n> 60 | 300\nGrand Total | 750",
	} {
		srv := salesSheet(t)
		err := writePivotAt(srv, `{`+salesSource+`,`+
			`"rows":[{"sourceColumnOffset":2,"showTotals":true,"sortOrder":"ASCENDING",`+
			`"groupRule":{"histogramRule":{"interval":20,"start":25,"end":`+end+`}}}],`+
			`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"}]}`)
		if err != nil {
			t.Fatalf("end %s: the pivot was refused: %v", end, err)
		}
		if got := drawnFrom(srv); got != want {
			t.Errorf("end %s drawn:\n%s\nwant:\n%s", end, got, want)
		}
	}
}

// TestPivotDrawsAGroupingByHand is the reference's ManualRule: each
// listed item goes under its group's name, and "Items that do not appear
// in any group will appear on their own".
func TestPivotDrawsAGroupingByHand(t *testing.T) {
	srv := salesSheet(t)
	err := writePivotAt(srv, `{`+salesSource+`,`+
		`"rows":[{"sourceColumnOffset":0,"showTotals":true,"sortOrder":"ASCENDING","groupRule":{"manualRule":{"groups":`+
		`[{"groupName":{"stringValue":"Coast"},"items":[{"stringValue":"East"},{"stringValue":"West"}]}]}}}],`+
		`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"}]}`)
	if err != nil {
		t.Fatalf("the pivot was refused: %v", err)
	}
	const want = "Region | SUM of Revenue\nCoast | 450\nNorth | 300\nGrand Total | 750"
	if got := drawnFrom(srv); got != want {
		t.Errorf("drawn:\n%s\nwant:\n%s", got, want)
	}
}

// TestPivotFiltersFollowTheReference is visibleByDefault as the
// reference defines it, and the criteria map a request may still carry.
func TestPivotFiltersFollowTheReference(t *testing.T) {
	rows := `"rows":[{"sourceColumnOffset":0,"showTotals":true,"sortOrder":"ASCENDING"}],` +
		`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"}]`
	greater := `"condition":{"type":"NUMBER_GREATER","values":[{"userEnteredValue":"60"}]}`
	for _, tc := range []struct {
		name, filters, want string
	}{
		{"a condition, the list ignored",
			`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{` + greater + `,"visibleValues":["West"],"visibleByDefault":true}}]`,
			"Region | SUM of Revenue\nEast | 100\nNorth | 300\nWest | 280\nGrand Total | 680"},
		{"a list and a condition, both holding",
			`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{` + greater + `,"visibleValues":["200","80"]}}]`,
			"Region | SUM of Revenue\nWest | 280\nGrand Total | 280"},
		// With visibleByDefault false, a value must be listed as well, and
		// none is: which is why manage_pivot_table sets it on a condition
		// alone.
		{"a condition alone, not visible by default",
			`"filterSpecs":[{"columnOffsetIndex":3,"filterCriteria":{` + greater + `}}]`,
			"Region | SUM of Revenue\nGrand Total | 0"},
		{"the older criteria map",
			`"criteria":{"0":{"visibleValues":["North"]}}`,
			"Region | SUM of Revenue\nNorth | 300\nGrand Total | 300"},
		{"filterSpecs over criteria",
			`"criteria":{"0":{"visibleValues":["North"]}},"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"visibleValues":["East"]}}]`,
			"Region | SUM of Revenue\nEast | 170\nGrand Total | 170"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := salesSheet(t)
			if err := writePivotAt(srv, `{`+salesSource+`,`+rows+`,`+tc.filters+`}`); err != nil {
				t.Fatalf("the pivot was refused: %v", err)
			}
			if got := drawnFrom(srv); got != tc.want {
				t.Errorf("drawn:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestAStoredPivotCarriesBothFilterForms is a response as the reference
// describes it: filterSpecs and criteria both populated, whichever the
// request sent.
func TestAStoredPivotCarriesBothFilterForms(t *testing.T) {
	srv := salesSheet(t)
	err := writePivotAt(srv, `{`+salesSource+`,"rows":[{"sourceColumnOffset":0,"sortOrder":"ASCENDING"}],`+
		`"values":[{"sourceColumnOffset":3,"summarizeFunction":"SUM"}],"criteria":{"0":{"visibleValues":["North"]}}}`)
	if err != nil {
		t.Fatalf("the pivot was refused: %v", err)
	}
	stored := string(srv.Doc(FixtureID).Find(SecondSheet).At(10, 8).PivotTable)
	for _, want := range []string{
		`"criteria":{"0":{"visibleValues":["North"]}}`,
		`"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"visibleValues":["North"]}}]`,
	} {
		if !strings.Contains(stored, want) {
			t.Errorf("the stored pivot does not carry %s: %s", want, stored)
		}
	}
}

// TestPivotRulesAreChecked holds the fake to the rules for values, group
// rules and filters: in Google's words where spike U saw Google refuse,
// in the fake's own elsewhere, and to what it refuses because it cannot
// evaluate it.
func TestPivotRulesAreChecked(t *testing.T) {
	group := func(offset, rule string) string {
		return `{"sourceColumnOffset":` + offset + `,"sortOrder":"ASCENDING","groupRule":` + rule + `}`
	}
	sum := `{"sourceColumnOffset":3,"summarizeFunction":"SUM"}`
	for _, tc := range []struct {
		name, rows, values, filters, want string
	}{
		{"an offset and a formula", group("0", "null"),
			`{"sourceColumnOffset":3,"formula":"=Revenue","summarizeFunction":"SUM"}`, "",
			"oneof field 'value' is already set"},
		{"neither an offset nor a formula", group("0", "null"), `{"summarizeFunction":"SUM"}`, "",
			"set exactly one of sourceColumnOffset and formula"},
		{"a formula without =", group("0", "null"), `{"formula":"Revenue","summarizeFunction":"SUM"}`, "",
			"a formula starts with ="},
		{"a formula averaged", group("0", "null"), `{"formula":"=Revenue","summarizeFunction":"AVERAGE"}`, "",
			`Invalid summarizeFunction: AVERAGE. Only "CUSTOM" or "SUM" are valid if PivotValue.calculatedField is set.`},
		{"CUSTOM on a column", group("0", "null"), `{"sourceColumnOffset":3,"summarizeFunction":"CUSTOM"}`, "",
			`"CUSTOM" may not be used in PivotValue.summarizeFunction unless PivotValue.calculatedField is set.`},
		{"a rule of both kinds",
			group("1", `{"dateTimeRule":{"type":"YEAR"},"histogramRule":{"interval":1}}`), sum, "",
			"set exactly one rule"},
		{"a rule of no kind", group("1", `{}`), sum, "", "set exactly one rule"},
		{"a rule by hand and by date", group("0", `{"dateTimeRule":{"type":"YEAR"},"manualRule":{"groups":[]}}`), sum, "",
			"set exactly one rule"},
		{"an item in two groups by hand", group("0", `{"manualRule":{"groups":[`+
			`{"groupName":{"stringValue":"One"},"items":[{"stringValue":"East"}]},`+
			`{"groupName":{"stringValue":"Two"},"items":[{"stringValue":"East"}]}]}}`), sum, "",
			"an item may appear in at most one group"},
		{"two groups by hand of one name", group("0", `{"manualRule":{"groups":[`+
			`{"groupName":{"stringValue":"One"},"items":[{"stringValue":"East"}]},`+
			`{"groupName":{"stringValue":"One"},"items":[{"stringValue":"West"}]}]}}`), sum, "",
			"each group must have a unique group name"},
		{"a group by hand named by a number", group("0", `{"manualRule":{"groups":[`+
			`{"groupName":{"numberValue":1},"items":[{"stringValue":"East"}]}]}}`), sum, "",
			"Found a manual group name of type number. Manual group names must be strings."},
		{"an interval of 0", group("2", `{"histogramRule":{"interval":0}}`), sum, "",
			"Histogram group rules require a positive value for interval."},
		{"a start past the end", group("2", `{"histogramRule":{"interval":5,"start":50,"end":20}}`), sum, "",
			"Start must be less than end."},
		{"a date type the enum lacks", group("1", `{"dateTimeRule":{"type":"FORTNIGHT"}}`), sum, "",
			`Invalid value at 'date_time_rule.type': "FORTNIGHT"`},
		{"a filter condition only data validation takes", group("0", "null"), sum,
			`,"filterSpecs":[{"columnOffsetIndex":0,"filterCriteria":{"condition":{"type":"ONE_OF_LIST","values":[{"userEnteredValue":"East"}]}}}]`,
			"Invalid requests[0].updateCells: ConditionType 'ONE_OF_LIST' is not supported in filters."},
		{"a date filter this fake does not evaluate", group("0", "null"), sum,
			`,"filterSpecs":[{"columnOffsetIndex":1,"filterCriteria":{"visibleByDefault":true,"condition":{"type":"DATE_AFTER","values":[{"userEnteredValue":"2026-01-15"}]}}}]`,
			"this fake does not evaluate a DATE_AFTER filter"},
		{"a heading alone in a CUSTOM formula", group("0", "null"),
			`{"formula":"=Revenue/2","summarizeFunction":"CUSTOM","name":"Half"}`, "",
			"this fake evaluates a heading in a CUSTOM formula only inside SUM, COUNT, AVERAGE, MIN or MAX: Revenue"},
		// One of each kind that is taken, so the table proves the checks
		// refuse what they name and nothing beside it.
		{"a plain group beside a rule on one column",
			group("1", "null") + `,` + group("1", `{"dateTimeRule":{"type":"YEAR"}}`), sum, "", ""},
		// The reference allows one rule per column, and Google took two
		// (spike U2).
		{"two rules on one column",
			group("1", `{"dateTimeRule":{"type":"YEAR"}}`) + `,` + group("1", `{"dateTimeRule":{"type":"MONTH"}}`), sum, "", ""},
		{"a grouping by hand", group("0", `{"manualRule":{"groups":[]}}`), sum, "", ""},
		{"a calculated value", group("0", "null"),
			`{"formula":"=SUM(Revenue)/COUNT(Cost)","summarizeFunction":"CUSTOM","name":"Mean"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := salesSheet(t)
			err := writePivotAt(srv, `{`+salesSource+`,"rows":[`+tc.rows+`],"values":[`+tc.values+`]`+tc.filters+`}`)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}
