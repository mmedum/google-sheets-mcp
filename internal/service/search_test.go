package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func TestSearchByName(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Search(context.Background(), service.SearchRequest{Name: "Quorbin"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Spreadsheets) != 2 {
		t.Fatalf("found %d spreadsheets, want 2: %s", len(res.Spreadsheets), res.Results)
	}
	// The fixture set has a Drive file of another type with a matching
	// name. If the mimeType filter came off, it would be here.
	for _, h := range res.Spreadsheets {
		if strings.Contains(h.Title, "notes") {
			t.Errorf("a non-spreadsheet came back: %+v", h)
		}
	}
	if !strings.Contains(res.Render(), sheetstest.FixtureID) {
		t.Errorf("the rendering does not carry an id to pass on:\n%s", res.Render())
	}
}

// TestSearchEscapesTheCallersText is the injection a shipped server had:
// caller text was interpolated into the Drive query unescaped, so an
// apostrophe closed the string early, detached the mimeType filter and
// the search returned arbitrary files.
//
// The fake parses the query the way Drive does and refuses a malformed
// one, so an unescaped apostrophe would surface here as a 400 rather
// than as a silently wrong result.
func TestSearchEscapesTheCallersText(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	for _, needle := range []string{
		"Yalmic's",
		`' or mimeType != 'x`,
		`back\slash`,
		`quote" and 'quote'`,
	} {
		res, err := svc.Search(ctx, service.SearchRequest{Name: needle})
		if err != nil {
			t.Fatalf("search for %q was refused, so the escaping is not holding: %v", needle, err)
		}
		for _, h := range res.Spreadsheets {
			if strings.Contains(h.Title, "notes") {
				t.Errorf("search for %q returned a non-spreadsheet: %+v", needle, h)
			}
		}
	}
}

func TestSearchOwnerAndModifiedAfter(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	res, err := svc.Search(ctx, service.SearchRequest{Owner: "fixture@example.test"})
	if err != nil {
		t.Fatalf("Search by owner: %v", err)
	}
	if len(res.Spreadsheets) == 0 {
		t.Error("no results for the owner every fixture has")
	}

	res, err = svc.Search(ctx, service.SearchRequest{ModifiedAfter: "2026-03-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Search by modified_after: %v", err)
	}
	if len(res.Spreadsheets) != 1 || res.Spreadsheets[0].ID != sheetstest.FixtureID {
		t.Errorf("modified_after gave %+v", res.Spreadsheets)
	}

	if _, err := svc.Search(ctx, service.SearchRequest{ModifiedAfter: "last Tuesday"}); err == nil ||
		!strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("a prose date gave %v", err)
	}
}

func TestSearchNeedsACriterion(t *testing.T) {
	// Listing every spreadsheet in an account is a Drive job. Refusing
	// here keeps this server inside one spreadsheet, which is the whole
	// scope decision.
	_, svc := standard(t)
	_, err := svc.Search(context.Background(), service.SearchRequest{})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("an empty search gave %v", err)
	}
}

func TestSearchPages(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Search(context.Background(), service.SearchRequest{Name: "Quorbin", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Spreadsheets) != 1 || res.NextPageToken == "" {
		t.Fatalf("a limited search gave %d results and token %q", len(res.Spreadsheets), res.NextPageToken)
	}
	if !strings.Contains(res.Render(), "page_token") {
		t.Errorf("the rendering does not say how to get the next page:\n%s", res.Render())
	}
}

func TestSearchWithNoMatches(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Search(context.Background(), service.SearchRequest{Name: "Nothingatallhere"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Spreadsheets) != 0 || !strings.Contains(res.Render(), "no spreadsheets matched") {
		t.Errorf("an empty search rendered as %q", res.Render())
	}
}

func TestDescribeNamesTheAccount(t *testing.T) {
	_, svc := standard(t)
	who, err := svc.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(who, "@") {
		t.Errorf("Describe = %q", who)
	}
}

// The profile records the signed-in address and `doctor` masks it, and
// both come from one call so they cannot end up describing different
// accounts.
//
// Every sibling server records one; this did not until the convention
// was compared field by field. It is not taken from the token, because
// tokeninfo returns an address only for a token carrying an email scope
// and this server asks for neither.
func TestAccountIsStoredWholeAndPrintedMasked(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	stored, err := svc.Account(ctx)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if !strings.Contains(stored, "@") {
		t.Fatalf("Account() = %q, want the address as Drive reports it", stored)
	}

	shown, err := svc.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if shown == stored {
		t.Errorf("Describe() = %q, the same as what is stored; doctor's output is meant to be pasteable", shown)
	}
	// Masked rather than replaced: somebody has to recognise their own
	// account in it.
	if !strings.Contains(shown, "@") {
		t.Errorf("Describe() = %q, which nobody could recognise", shown)
	}
	local, domain, _ := strings.Cut(stored, "@")
	if strings.Contains(shown, local) || strings.Contains(shown, domain) {
		t.Errorf("Describe() = %q leaks a whole half of %q", shown, stored)
	}
}
