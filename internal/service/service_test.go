package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

func newService(t *testing.T, s *sheetstest.Server) *service.Service {
	t.Helper()
	return newServiceBudget(t, s, config.DefaultMaxCells)
}

// newServiceBudget is newService with the read budget named, for the
// tests about what a bounded window can and cannot reach.
func newServiceBudget(t *testing.T, s *sheetstest.Server, cells int) *service.Service {
	t.Helper()
	fs, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	fs.MaxCells = cells
	return service.New(service.Deps{API: s.Client(), Config: fs})
}

func settings() (config.Config, error) {
	s := &config.Settings{
		Profile: "default", LogLevel: "info", LogFormat: "text",
		MaxCells: "5000", MaxChars: "20000", HTTPTimeout: "60s", WriteTimeout: "180s",
	}
	return s.Build()
}

func standard(t *testing.T) (*sheetstest.Server, *service.Service) {
	t.Helper()
	srv := sheetstest.Standard(t)
	return srv, newService(t, srv)
}

func TestClassesAreClosedAndDistinct(t *testing.T) {
	cs := service.Classes()
	if len(cs) != 12 {
		t.Errorf("the vocabulary has %d classes; §6.5 fixes it at 12", len(cs))
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c] {
			t.Errorf("%q appears twice", c)
		}
		seen[c] = true
	}
	// The two pairs that must not blur: one asks the caller to choose,
	// the other to go and look; one is this server refusing, the other
	// the account being wrong.
	for _, pair := range [][2]string{{"ambiguous", "ambiguous_outcome"}, {"blocked", "forbidden"}} {
		if !seen[pair[0]] || !seen[pair[1]] {
			t.Errorf("%v are both part of the vocabulary", pair)
		}
	}
}

func TestErrorFormat(t *testing.T) {
	err := service.Errorf("not_found", "no sheet named %q", "Vandel")
	if got := err.Error(); got != `[not_found] no sheet named "Vandel"` {
		t.Errorf("Error() = %q", got)
	}
}

func TestParseReference(t *testing.T) {
	for _, tc := range []struct {
		in     string
		id     string
		gid    int
		hasGid bool
		ok     bool
	}{
		{sheetstest.FixtureID, sheetstest.FixtureID, 0, false, true},
		{"https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit#gid=1837", sheetstest.FixtureID, 1837, true, true},
		{"https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit?gid=1837#gid=1837", sheetstest.FixtureID, 1837, true, true},
		{"https://docs.google.com/spreadsheets/d/" + sheetstest.FixtureID + "/edit", sheetstest.FixtureID, 0, false, true},
		// A title is not an id, and must not be mistaken for one.
		{"Quorbin Skerry", "", 0, false, false},
		{"Q3", "", 0, false, false},
		{"", "", 0, false, false},
		{"a.b.c", "", 0, false, false},
	} {
		got, ok := service.ParseReference(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseReference(%q) recognised = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && (got.ID != tc.id || got.Gid != tc.gid || got.HasGid != tc.hasGid) {
			t.Errorf("ParseReference(%q) = %+v", tc.in, got)
		}
	}
}

func TestResolveByTitle(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()

	ref, err := svc.Resolve(ctx, "Quorbin Skerry")
	if err != nil {
		t.Fatalf("Resolve by exact title: %v", err)
	}
	if ref.ID != sheetstest.FixtureID {
		t.Errorf("resolved to %q", ref.ID)
	}

	// A title matching nothing gets the near misses rather than a bare
	// refusal, because the answer is usually a typo.
	_, err = svc.Resolve(ctx, "Quorbin missing")
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("a missing title gave %v", err)
	}
	if !strings.Contains(err.Error(), "Quorbin Skerry") {
		t.Errorf("the closest titles were not offered: %v", err)
	}

	if _, err := svc.Resolve(ctx, ""); err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("an empty reference gave %v", err)
	}
}

func TestResolveRefusesToChooseBetweenTwoSpreadsheets(t *testing.T) {
	srv := sheetstest.New(t)
	first, ff := sheetstest.Fixture()
	srv.Add(first, ff)
	second, sf := sheetstest.Second()
	sf.Name = ff.Name // the same title, twice
	srv.Add(second, sf)
	svc := newService(t, srv)

	_, err := svc.Resolve(context.Background(), ff.Name)
	if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous]") {
		t.Fatalf("two matches gave %v; taking the first is how a server writes to the wrong spreadsheet", err)
	}
	for _, id := range []string{sheetstest.FixtureID, sheetstest.SecondFixtureID} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("the refusal does not name candidate %s: %v", id, err)
		}
	}
}

func TestCard(t *testing.T) {
	srv, svc := standard(t)
	card, err := svc.Card(context.Background(), sheetstest.FixtureID)
	if err != nil {
		t.Fatalf("Card: %v", err)
	}

	// The sheet titles are the whole point of the call: every later one
	// names a sheet and none may be guessed.
	want := []string{sheetstest.FirstSheet, sheetstest.SecondSheet, sheetstest.ApostropheName}
	if !slices.Equal(card.Sheets, want) {
		t.Errorf("sheets = %v, want %v", card.Sheets, want)
	}
	for _, s := range want {
		if !strings.Contains(card.Card, s) {
			t.Errorf("the card does not name sheet %q", s)
		}
	}
	if strings.Contains(card.Card, "Sheet1") {
		t.Error("the card offers Sheet1, which does not exist on a non-English account")
	}
	for _, want := range []string{"Quorbin Skerry", "id: " + sheetstest.FixtureID, "named ranges", "protected ranges", "filter views", "tables"} {
		if !strings.Contains(card.Card, want) {
			t.Errorf("the card is missing %q:\n%s", want, card.Card)
		}
	}
	// The trap, named where somebody will read it.
	if !strings.Contains(card.Card, "shares its name with a sheet") {
		t.Errorf("a named range with a sheet's name was not called out:\n%s", card.Card)
	}
	if !strings.Contains(card.Card, "hidden") {
		t.Error("a hidden sheet must say so; it is invisible to the person otherwise")
	}
	if !strings.Contains(card.Card, "1 row frozen") {
		t.Error("frozen rows were not reported")
	}

	// The card never asks for cells: that is what makes it cheap on a
	// ten-million-cell spreadsheet.
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.get" && c.Query.Get("includeGridData") != "" {
			t.Error("the card asked for grid data")
		}
	}
}

func TestCardIsCachedBriefly(t *testing.T) {
	srv := sheetstest.Standard(t)
	cfg, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	svc := service.New(service.Deps{API: srv.Client(), Config: cfg, Now: func() time.Time { return now }})

	ctx := context.Background()
	for range 3 {
		if _, err := svc.Card(ctx, sheetstest.FixtureID); err != nil {
			t.Fatal(err)
		}
	}
	if n := countOp(srv, "spreadsheets.get"); n != 1 {
		t.Errorf("three cards cost %d metadata calls, want 1", n)
	}
	// The window is short on purpose: it saves a second call inside one
	// tool invocation, not across a person's session.
	now = now.Add(service.CacheWindow + time.Second)
	if _, err := svc.Card(ctx, sheetstest.FixtureID); err != nil {
		t.Fatal(err)
	}
	if n := countOp(srv, "spreadsheets.get"); n != 2 {
		t.Errorf("after the window the card cost %d calls, want 2", n)
	}
}

func countOp(s *sheetstest.Server, op string) int {
	n := 0
	for _, c := range s.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

// destructive is a service with the gated tools' configuration.
func destructive(t *testing.T) (*sheetstest.Server, *service.Service) {
	t.Helper()
	srv := sheetstest.Standard(t)
	fs, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	fs.EnableDestructive = true
	return srv, service.New(service.Deps{API: srv.Client(), Config: fs})
}
