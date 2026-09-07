package sheetstest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
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
